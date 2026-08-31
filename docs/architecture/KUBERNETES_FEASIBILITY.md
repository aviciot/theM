# Kubernetes Migration Feasibility Assessment — the-M
**Date:** 2026-08-31  
**Scope:** Full stack as defined in `docker-compose.yml` + `docker-compose.dev.yml`  
**Purpose:** Analysis and feasibility only. No implementation.

---

## Executive Summary

the-M's Go services are architecturally well-suited for Kubernetes. They are stateless, write nothing to the local filesystem, read all configuration from environment variables, implement graceful shutdown (except `agent-runtime`), and use Redis and PostgreSQL as the authoritative state stores. The multi-replica patterns already in production (two bridge replicas, two workers, two agent-runtime instances) validate the multi-pod model.

The blockers are specific and fixable — not structural:

| Severity | Issue |
|---|---|
| **High** | `agent-runtime` has no SIGTERM handler (container killed mid-request) |
| **High** | Agent card URL hardcoded as `http://them-agent-runtime:9300/...` in DB — breaks in K8s |
| **Medium** | Traefik uses Docker socket for service discovery — replace with K8s Ingress controller |
| **Medium** | MCP leader election uses Redis SETNX — safe but add jitter to avoid thundering herd |
| **Medium** | No schema migration framework — DB init is currently Postgres init-script based |
| **Low** | `mcp-service` Postgres pool size hardcoded at 10 — make env-configurable |
| **Low** | Temporal runs as a single container — needs its own K8s deployment strategy |

PostgreSQL and Redis should be treated as managed services (AWS RDS/ElastiCache, Azure Database, GCP CloudSQL/Memorystore, or Helm-deployed) rather than in-cluster Deployments on day one.

---

## Container-by-Container Assessment

---

### 1. `them-traefik`

| Property | Detail |
|---|---|
| **Current role** | Reverse proxy, single external entry point, path-based routing to all services |
| **Stateful?** | No |
| **K8s safe?** | Remove — replace with standard K8s Ingress |
| **K8s primitive** | **Remove / Replace** — use `nginx-ingress` or `Traefik Ingress Controller` as a proper K8s Ingress |

**The problem:** Traefik in compose discovers services by reading `/var/run/docker.sock`. That model does not exist in Kubernetes. Traefik can run as a K8s Ingress controller (it has a proper K8s mode), but the Docker-socket discovery mechanism must be completely replaced with `IngressRoute` CRDs or standard Kubernetes `Ingress` objects.

**Required changes before migration:**
- Remove Docker-socket volume entirely
- Replace all Traefik Docker labels with `Ingress` or `IngressRoute` manifests
- Decide between Traefik K8s Ingress Controller, NGINX, or AWS ALB/GCP GLB (managed is simpler)
- Sticky session behavior is not currently configured in compose, so no sticky session migration concern

**Networking:** Single `LoadBalancer` Service in K8s terminates at the Ingress controller. Path routing rules mirror the current Traefik router priorities.

**Health checks:** Not applicable — Ingress controller manages its own liveness.

**Scaling:** Ingress controller scales independently; 2–3 replicas standard for HA.

**Migration risks:**
- Translating priority-ordered Traefik routers to Ingress path rules requires care (e.g., `/ws/dashboard` exact match must beat `/ws` prefix)
- Dashboard at `127.0.0.1:8089` (localhost-only) must become a restricted `ClusterIP` or removed

---

### 2. `them-postgres`

| Property | Detail |
|---|---|
| **Current role** | Primary relational store — all application and auth data |
| **Stateful?** | Yes — bind-mount `./data/them-postgres/pgdata` (prod), named volume in dev |
| **K8s safe?** | Not recommended in-cluster without dedicated DBA ops |
| **K8s primitive** | **External managed service** (RDS, Azure DB, CloudSQL, or Bitnami Helm with PVC) |

**Required changes before migration:**
- Connection string vars (`DATABASE_HOST`, `DATABASE_PORT`, etc.) already externalized — no code changes needed
- Add a proper migration framework (Goose, Atlas, or Flyway) — current init-script pattern won't work in K8s without a dedicated init Job
- Add a K8s `Job` to run migrations before app Pods start (use `initContainers` or a pre-upgrade Helm hook)
- Consider enabling `sslmode=require` — currently disabled in all DSNs

**Persistent storage:** If running in-cluster: `StatefulSet` with a `PersistentVolumeClaim`, `ReadWriteOnce` access mode, provisioned SSD storage class. Backup strategy (WAL archiving or pg_dump CronJob) required.

**Networking:** `ClusterIP` Service `them-postgres:5432`. Apps connect via Service DNS — no change to env var values beyond the hostname.

**Health checks:**
- `startupProbe`: `exec pg_isready` — wait up to 120s before declaring startup failure
- `readinessProbe`: `exec pg_isready` — gate traffic until Postgres is truly accepting connections
- No `livenessProbe` on database pods (avoid killing a healthy-but-slow Postgres)

**Scaling:** PostgreSQL does not horizontally scale without read-replica routing (PgBouncer + streaming replicas). For phase 1, single primary is sufficient. Read replicas can be added later with connection-level routing.

**Migration risks:**
- Data migration from bind-mount to PVC must be done with a snapshot or `pg_dump`/`pg_restore`
- Schema changes have no rollback mechanism today — migration framework must be adopted before K8s
- `pg_try_advisory_lock` (used by reconciler) works correctly in K8s as long as connections come from the same DB cluster — no change needed

---

### 3. `them-redis`

| Property | Detail |
|---|---|
| **Current role** | Session state, pub/sub channels, run event streams, rate limiting, caches, leader election, admission queues |
| **Stateful?** | Yes — AOF persistence, bind-mount `./data/them-redis` (prod), named volume in dev |
| **K8s safe?** | Not recommended in-cluster for production without dedicated ops |
| **K8s primitive** | **External managed service** (ElastiCache, Azure Cache for Redis, GCP Memorystore) or Bitnami Redis Helm chart |

**Critical Redis features in use:**
- Lua scripts (atomic eval) — supported by all Redis-compatible managed services
- Redis Streams (XADD/XREAD BLOCK) — supported in Redis 5+; all current managed services qualify
- pub/sub — fully standard
- BLPOP — standard
- `SET NX PX` (leader election) — standard

**Required changes before migration:**
- `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` already externalized — no code changes needed
- Verify managed service supports `EVAL` (Lua) — AWS ElastiCache in cluster mode restricts cross-slot Lua scripts; confirm all Lua scripts operate on a single slot (they appear to — all keys are per-entity, not cross-entity) or use Redis standalone/non-cluster mode
- Add `REDIS_DB` env var support (already present in config) — some managed services only support DB 0

**Persistent storage:** AOF persistence is currently enabled. Managed services handle this automatically. If in-cluster: `StatefulSet` with PVC.

**Health checks:**
- `readinessProbe`: `redis-cli ping` (same as compose healthcheck)
- `livenessProbe`: `redis-cli ping` with 10s failureThreshold

**Scaling:** Redis Streams and pub/sub require single-instance or Redis Cluster with careful key-slot design. Current design (all keys under `them:` prefix) is compatible with a single master. Cluster mode would require validating all Lua scripts operate on co-located keys.

**Migration risks:**
- Redis Cluster mode is a non-trivial change to the Lua scripts if required for scale
- Session data, HITL handles (24h TTL), and A2A task state (24h TTL) live in Redis — these will be lost during a migration cutover unless `DUMP`/`RESTORE` or MIGRATE commands are used

---

### 4. `them-auth-go`

| Property | Detail |
|---|---|
| **Current role** | Authentication service — JWT issuance, session management, user/role CRUD |
| **Stateful?** | No |
| **K8s safe?** | Yes — immediately ready |
| **K8s primitive** | **Deployment** |

**Why it's ready:** No local state, no filesystem writes, no fixed hostnames, graceful SIGTERM handling (20s drain), all config from env vars, `/health` liveness endpoint.

**Required changes:** None. Minor polish only:
- Split `/health` into `/health/live` (liveness — always OK) and `/health/ready` (readiness — probes Postgres) — the `/health/live` endpoint already exists per the Traefik healthcheck labels

**Probes:**
- `livenessProbe`: `GET /health/live`, initialDelaySeconds: 10, periodSeconds: 15
- `readinessProbe`: `GET /health/ready` (probes Postgres), initialDelaySeconds: 5, periodSeconds: 10
- `startupProbe`: `GET /health/live`, failureThreshold: 12 (give 120s to start)

**Secrets:** `DATABASE_PASSWORD`, `JWT_SECRET` → K8s Secrets; all other config → ConfigMap.

**Scaling:** Stateless — scale horizontally to any replica count. No session affinity required (JWTs are self-contained).

**Migration risks:** Low. The only concern is JWT_SECRET consistency across replicas — a K8s Secret ensures this.

---

### 5. `them-go-bridge`

| Property | Detail |
|---|---|
| **Current role** | Main API gateway — REST, WebSocket, SSE, A2A, admin, observability |
| **Stateful?** | Mostly no. Has per-pod in-memory L1 caches (token, agent registry, EP config), but all are backed by Redis/Postgres and cross-invalidated via pub/sub |
| **K8s safe?** | Yes, with one required change (WebSocket connection handling) |
| **K8s primitive** | **Deployment** |

**In-memory state analysis:**
- **L1 bearer token cache** (`sync.Map`, 300s TTL): per-pod. Cross-pod eviction via `them:token:revoked` Redis pub/sub. On cache miss, falls back to Redis L2 then Postgres. Safe for multi-replica.
- **L1 agent registry cache** (`sync.Map`, 600s TTL): per-pod. Cross-pod eviction via `them:agents:changed`. Safe.
- **EP config cache** (mutex + map, 30s TTL): per-pod. Cross-pod eviction via `them:ep:config:changed`. Safe.

**WebSocket/SSE:** The current code stores no WebSocket connection state in memory beyond the connection goroutine and `connWriter` mutex (per-connection write serializer). All durable session state is in Redis. The Ingress controller must support WebSocket upgrades (pass `Connection: Upgrade` and `Upgrade: websocket` headers through). NGINX Ingress handles this with `nginx.ingress.kubernetes.io/proxy-read-timeout` and `proxy-send-timeout` set to a high value for long-lived connections.

**Required changes:**
- Ensure Ingress controller config passes WebSocket upgrade headers
- No sticky sessions are in use today (confirmed: no `loadbalancer.sticky` labels in compose) — multi-pod WebSocket is safe because all run state is in Temporal + Redis + Postgres
- Add `GOMAXPROCS` tuning (use `automaxprocs` library to respect K8s CPU limits)
- Fix hardcoded `them-agent-runtime:9300` hostname in `agent_definitions_publish.go:73` — replace with a configurable env var `THE_M_AGENT_RUNTIME_URL`

**Probes:**
- `livenessProbe`: `GET /health/live`, periodSeconds: 15
- `readinessProbe`: `GET /health/ready` (probes Postgres + Redis), initialDelaySeconds: 5, periodSeconds: 10
- `startupProbe`: `GET /health/live`, failureThreshold: 20 (allow 200s for Temporal connection)

**Secrets:** `DATABASE_PASSWORD`, `REDIS_PASSWORD`, `SECRET_KEY`, `JWT_SECRET`, `ANTHROPIC_API_KEY` → K8s Secrets.

**Scaling:** Horizontally scalable. Run 2–5 replicas minimum for HA. The reconciler background goroutine uses a PostgreSQL advisory lock — only one replica runs the reconcile sweep at a time, others wait and skip — this is correct and safe.

**Migration risks:**
- Hardcoded agent-runtime DNS name in DB is a blocking issue — any agent published in the DB points to a Docker Compose hostname that won't resolve in K8s
- WebSocket drain during rolling updates: use `preStop` lifecycle hook with a sleep matching `SHUTDOWN_DRAIN_SECONDS` (default 30s) so the load balancer removes the pod from rotation before connections close

---

### 6. `them-go-worker` / `them-go-worker-2`

| Property | Detail |
|---|---|
| **Current role** | Temporal worker — polls `them-orchestration-go`, runs `OrchestrationWorkflow` + `RunOrchestratorActivity` |
| **Stateful?** | No — all durable state in Temporal + Postgres + Redis |
| **K8s safe?** | Yes — immediately ready |
| **K8s primitive** | **Deployment** |

**Why it's ready:** No HTTP server, no filesystem writes, graceful SIGTERM (calls `goWorker.Stop()`), all config from env vars, Temporal SDK handles re-queue on worker crash.

**Required changes:** None significant. Improvements:
- Remove the `container_name` constraint (already no `container_name` issue — dev compose sets names but K8s ignores them)
- Tune `MaxConcurrentWorkflowTaskPollers` and `MaxConcurrentActivityTaskPollers` via env vars (currently uses SDK defaults)

**Probes:**
- No HTTP endpoint — use `exec` probe or Temporal worker health endpoint if SDK exposes one
- Recommended: use `exec` to check that the process is running (`/bin/sh -c "pgrep them-worker"`)
- Or expose a minimal `GET /healthz` that returns 200 (small addition)
- `startupProbe` failureThreshold: 20 (Temporal connection may take time)

**Secrets:** Same as bridge — `DATABASE_PASSWORD`, `REDIS_PASSWORD`, `SECRET_KEY`, `ANTHROPIC_API_KEY`.

**Scaling:** Temporal handles work distribution across multiple worker replicas via task queue polling. Scale horizontally based on queue depth — KEDA can poll Temporal queue backlog if Temporal exposes metrics, or use Prometheus metrics from the worker. Multiple replicas poll the same queue safely.

**Migration risks:** Low. Temporal SDK is designed for multi-worker deployments.

---

### 7. `them-dag-worker`

| Property | Detail |
|---|---|
| **Current role** | Temporal worker — polls `canvas-dag-nodes`, executes canvas DAG steps via `CanvasAgentWorkflow` |
| **Stateful?** | No |
| **K8s safe?** | Yes — immediately ready |
| **K8s primitive** | **Deployment** |

**Same analysis as `them-go-worker`.** Adds one configurable parameter: `DAG_WORKER_MAX_CONCURRENT_ACTIVITIES` (default 50) — this is already env-configurable and becomes a key tuning knob in K8s for controlling per-pod parallelism.

**Probes:** Same approach as `them-go-worker` — add a minimal HTTP health endpoint.

**Scaling:** Scale based on canvas workflow queue depth. `MaxConcurrentActivityExecutionSize` per pod × pod count = total concurrency. Set `podAntiAffinity` to spread dag-workers across nodes for fault tolerance.

**Migration risks:** Low.

---

### 8. `them-agent-runtime`

| Property | Detail |
|---|---|
| **Current role** | Serves A2A API for canvas-designed agents. Loads `AgentSpec` from Postgres on demand |
| **Stateful?** | Mostly no. Has per-pod `specCache` (mutex+map, 60s TTL) not shared or pub/sub invalidated |
| **K8s safe?** | Yes, with two required fixes |
| **K8s primitive** | **Deployment** |

**Required changes — both are blocking:**

**1. Add graceful shutdown (HIGH):** Currently calls `http.ListenAndServe` with no SIGTERM handler. In K8s, a pod deletion sends SIGTERM followed by SIGKILL after `terminationGracePeriodSeconds`. Without a handler, in-flight A2A requests are killed mid-execution. Fix: replace `http.ListenAndServe` with the same `http.Server` + `Shutdown` pattern used by all other Go services. Should take ~30 minutes.

**2. Fix hardcoded hostname (HIGH):** `specCache` contains per-pod cache that isn't cross-invalidated. When an agent spec is updated in the DB, replicas continue serving the old spec for up to 60s. This is acceptable and by design. However, the agent card URL returned by `GET /agents/{slug}/.well-known/agent-card.json` is hardcoded to `http://them-agent-runtime:9300/agents/{slug}`. In K8s this Docker Compose DNS name won't resolve from external callers. This URL is also written into `them.agents.endpoint_url` during canvas publish. Fix: introduce `THE_M_AGENT_RUNTIME_BASE_URL` env var used when constructing agent card URLs.

**Probes (Dockerfile healthcheck already exists — reuse):**
- `livenessProbe`: `GET /healthz` (`{"status":"ok"}`), periodSeconds: 20
- `readinessProbe`: `GET /healthz`, initialDelaySeconds: 5
- `startupProbe`: `GET /healthz`, failureThreshold: 12

**Secrets:** `DATABASE_PASSWORD`, `REDIS_PASSWORD`, `SECRET_KEY`, `THE_M_INVOCATION_JWT_KEY`, `ANTHROPIC_API_KEY`, `MCP_SERVICE_URL`.

**Scaling:** Horizontally scalable once graceful shutdown is fixed. The `specCache` is per-pod with no cross-invalidation — a spec update will take up to 60s to propagate across all replicas. This is acceptable. If tighter consistency is needed: add pub/sub invalidation on `them:agents:changed` channel (same pattern as the bridge's agent registry cache).

**Migration risks:**
- Missing graceful shutdown is a correctness issue — any rolling update kills in-flight agent invocations
- Hardcoded hostname in agent card/DB breaks agent discovery from outside K8s pod network

---

### 9. `them-mcp-service`

| Property | Detail |
|---|---|
| **Current role** | MCP server supervisor (leader) + tool executor (all replicas). Manages health checks for registered MCP HTTP servers |
| **Stateful?** | Leader has in-memory per-server goroutine state; non-leaders are stateless |
| **K8s safe?** | Yes, with care around leader election |
| **K8s primitive** | **Deployment** (with Redis-based leader election — already implemented) |

**Leader election analysis:** The current `SET NX PX 30s` Redis lock is a valid distributed leader election pattern. The leader runs the supervisor goroutines; non-leaders serve `/internal/execute` as pure stateless HTTP. If the leader pod dies, the Redis lock TTL expires within 30s and another replica acquires leadership. This is correct behavior and works identically in K8s.

**Improvement:** Consider using K8s-native leader election (`coordination.k8s.io/v1` Lease object) instead of Redis SETNX — this avoids Redis being a dependency for the leadership decision. However, the Redis approach works correctly and requires no code change.

**Required changes:**
- Make Postgres pool size configurable via env var — currently hardcoded at 10 (`mcp/config.go:89`)
- Add jitter to supervisor reconcile ticker start to avoid thundering herd on pod restart

**Probes:**
- `livenessProbe`: `GET /health/live`, periodSeconds: 20
- `readinessProbe`: `GET /health/ready` (probes Postgres + Redis), initialDelaySeconds: 5
- `startupProbe`: `GET /health/live`, failureThreshold: 12

**Secrets:** `DATABASE_PASSWORD`, `REDIS_PASSWORD`, `SECRET_KEY`.

**Scaling:** Scale horizontally — only the leader runs health-check goroutines, all serve tool execution. 2–3 replicas provides HA.

**Migration risks:**
- Traefik currently has `traefik.enable=false` for this service — in K8s it remains `ClusterIP` only (not exposed externally). No change needed.
- `MCP_ALLOW_STDIO=false` — stdio transport is disabled; no subprocess spawning concern

---

### 10. `them-frontend`

| Property | Detail |
|---|---|
| **Current role** | Next.js dashboard — canvas builder, admin UI, observability views |
| **Stateful?** | No |
| **K8s safe?** | Yes |
| **K8s primitive** | **Deployment** |

**Required changes:**
- `THE_M_API_URL` and `THE_M_AUTH_URL` must point to K8s Service DNS names or the external Ingress hostname
- `NEXT_PUBLIC_BRIDGE_WS_URL` must be set to the public WebSocket URL (currently empty — the frontend proxies via the Next.js route handler)
- Ensure `COOKIE_SECURE=true` when TLS is terminated at the Ingress

**Probes:**
- `livenessProbe`: `GET /login` (existing compose healthcheck path), periodSeconds: 30
- `readinessProbe`: `GET /login`, initialDelaySeconds: 15
- `startupProbe`: `GET /login`, failureThreshold: 12 (Next.js cold start can be slow)

**Scaling:** Stateless — scale to 2+ replicas. Next.js server-side rendering is CPU-bound; set resource requests accordingly (500m–1000m CPU).

**Migration risks:** Low. Env vars cover all configuration surface. Dev mode (`Dockerfile.dev` with bind-mounted source) should not run in K8s.

---

### 11. `temporal-frontend` (Temporal Server)

| Property | Detail |
|---|---|
| **Current role** | Temporal workflow engine — schedules and persists all durable workflows |
| **Stateful?** | Yes — uses PostgreSQL as its backend (separate `temporal` and `temporal_visibility` databases) |
| **K8s safe?** | Yes — Temporal has production-grade K8s Helm charts |
| **K8s primitive** | **External managed service** (Temporal Cloud) or **Helm chart** (`temporal/temporalite` or `temporal/temporal`) |

**The `temporalio/auto-setup` image** (used in compose) is a single-container all-in-one that includes the frontend, history, matching, and worker services co-located. This is a dev/test image. For production K8s, the proper multi-service Temporal Helm chart deploys each service independently.

**Required changes:**
- Switch from `auto-setup` to the production Helm chart or Temporal Cloud
- `TEMPORAL_HOST_PORT` env var in all workers/bridge → K8s Service DNS (e.g., `temporal-frontend:7233`)
- Temporal databases (`temporal`, `temporal_visibility`) must be pre-initialized on the Postgres instance before Temporal pods start
- `temporal-admin-tools` (currently runs `sleep infinity`) → becomes a K8s Job for one-time DB setup

**Scaling:** Temporal's Helm chart deploys each service (frontend, history, matching, worker) as separate Deployments with independent replica counts. History service is the most resource-intensive.

**Migration risks:**
- The `auto-setup` image creates Temporal DB schema automatically on first start. In K8s this must be done via an `initContainer` or pre-upgrade Helm hook.
- Temporal is a critical dependency — all orchestrated workflows halt if Temporal is unavailable. Plan for 3-replica quorum in production.

---

### 12. A2A Test Agents (`a2a-echo`, `a2a-slow`, `a2a-stream`)

| Property | Detail |
|---|---|
| **Current role** | Test agents — echo, slow response, streaming. Profile `test-agents` |
| **Stateful?** | No |
| **K8s safe?** | Yes |
| **K8s primitive** | **Deployment** (or omit from production cluster) |

These are test fixtures. In K8s they belong in a `test` namespace or behind a feature flag, not in the production cluster. Each is a simple stateless HTTP service. No migration concerns.

---

### 13. Domain Agents (`vision-agent`, `them-security-agent`, `docu-writer`, etc.)

| Property | Detail |
|---|---|
| **Current role** | Specialized A2A agents (vision, security scanner, document writer, etc.) |
| **Stateful?** | No |
| **K8s safe?** | Yes |
| **K8s primitive** | **Deployment** per agent |

Each agent is a standalone HTTP service exposing a fixed port. All configuration comes from env vars. No filesystem writes identified.

**Considerations:**
- `ANTHROPIC_API_KEY` and other model provider keys → K8s Secrets
- Each agent should have its own `livenessProbe` and `readinessProbe` on its health endpoint
- Agents that use external APIs (Google Maps, FAL) need appropriate resource limits and retry budgets

---

### 14. `livekit` / `livekit-agent` (profile: `voice`)

| Property | Detail |
|---|---|
| **Current role** | Real-time voice — LiveKit media server + Python voice agent |
| **Stateful?** | LiveKit server has state (active rooms); agent is stateless |
| **K8s safe?** | LiveKit in K8s requires UDP port exposure (`7882/UDP`) — non-trivial |
| **K8s primitive** | **External managed service** (LiveKit Cloud) strongly recommended; or NodePort/HostNetwork for UDP |

**The UDP problem:** LiveKit uses UDP for WebRTC media streams (port 7882). Standard K8s Services and Ingress controllers do not support UDP routing well. Options: `NodePort` (exposes UDP on the node IP), `HostNetwork: true` (pod uses node network namespace), or managed LiveKit Cloud. Managed is strongly recommended.

**Migration risks:** UDP support is a K8s pain point. This should be phase 4 or moved to LiveKit Cloud before K8s migration begins.

---

## Multi-Replica Safety Analysis

Things that could break when multiple pod replicas run simultaneously:

| Component | Issue | Impact | Fix |
|---|---|---|---|
| `agent-runtime` | No SIGTERM handler | In-flight requests killed on pod eviction or rolling update | Add `http.Server.Shutdown` pattern |
| `agent-runtime` | `specCache` not cross-invalidated | Up to 60s stale spec served after an update | Add `them:agents:changed` pub/sub subscription |
| `agent-runtime` | Agent card URL `http://them-agent-runtime:9300/...` hardcoded in code and DB | Card URL won't resolve in K8s; wrong URL written to DB | Introduce `THE_M_AGENT_RUNTIME_BASE_URL` env var |
| `them-go-bridge` | Same hardcoded hostname in `agent_definitions_publish.go:73` | Agents published via bridge have wrong endpoint_url in DB | Same fix — env var for base URL |
| `mcp-service` | Postgres pool hardcoded at 10 | Not configurable per replica | `MCP_DATABASE_POOL_SIZE` env var |
| All workers | No HTTP health endpoint | K8s cannot probe liveness via HTTP | Add minimal `/healthz` or use exec probe |
| `them-go-worker` | Temporal worker defaults (no explicit concurrency) | Untuned concurrency under K8s HPA | Add env vars for `MaxConcurrentWorkflowTaskPollers`, etc. |
| `reconciler` | Runs in every bridge replica | Only one runs at a time (pg advisory lock) | Already safe — no change needed |
| `appliveness` loop | Runs in every bridge replica | Every replica probes every EP; produces redundant pub/sub traffic | Acceptable at current scale; at high replica count, move to a dedicated liveness worker |
| `mcp-service` supervisor | Runs goroutines only on leader | Redis SETNX leader election is per-replica | Already safe — no change needed |
| `pod heartbeat` | Runs in every bridge replica | Independent per-instance_id | Already safe — instance_id is unique |

---

## Deployment Architecture (Target State)

```
                     ┌──────────────────────────────────────────┐
                     │         K8s Ingress Controller            │
                     │  (Traefik / NGINX / ALB)                  │
                     │  /auth → auth-go                          │
                     │  /api/v1 /ws /sse /apps /a2a → go-bridge  │
                     │  /temporal → temporal-ui                  │
                     │  / → frontend                             │
                     └───────────────┬──────────────────────────┘
                                     │
           ┌─────────────────────────┼──────────────────────────────────┐
           │                         │                                  │
    ┌──────▼──────┐          ┌───────▼───────┐                 ┌───────▼──────┐
    │ them-auth-go │          │ them-go-bridge│                 │them-frontend │
    │ Deployment   │          │  Deployment   │                 │ Deployment   │
    │ 2+ replicas  │          │  2–5 replicas │                 │ 2 replicas   │
    └─────────────┘          └───────┬───────┘                 └──────────────┘
                                     │
          ┌──────────────────────────┼────────────────────────────┐
          │                          │                            │
   ┌──────▼──────┐          ┌────────▼───────┐          ┌────────▼───────┐
   │ them-go-    │          │ them-dag-worker │          │them-agent-     │
   │ worker      │          │   Deployment   │          │runtime         │
   │ Deployment  │          │   1–N replicas │          │Deployment      │
   │ 2+ replicas │          │   (KEDA)       │          │2–N replicas    │
   └──────┬──────┘          └───────┬────────┘          └───────┬────────┘
          │                         │                           │
          └─────────────────────────┼───────────────────────────┘
                                    │
               ┌────────────────────┼───────────────────────┐
               │                    │                       │
        ┌──────▼──────┐    ┌────────▼───────┐    ┌─────────▼──────┐
        │  Temporal   │    │  PostgreSQL    │    │    Redis       │
        │  Helm Chart │    │  (Managed /   │    │  (Managed /    │
        │  or Cloud   │    │   StatefulSet)│    │   StatefulSet) │
        └─────────────┘    └───────────────┘    └────────────────┘
```

---

## Autoscaling and High-Performance Operation

### Horizontal Pod Autoscaling (HPA)

All stateless services support HPA out of the box once resource `requests` are set.

| Service | HPA trigger | Min | Max | Notes |
|---|---|---|---|---|
| `them-go-bridge` | CPU > 60%, memory > 75% | 2 | 10 | WS connections add memory; set memory limit conservatively |
| `them-auth-go` | CPU > 60% | 2 | 5 | Stateless — scales freely |
| `them-frontend` | CPU > 70% | 2 | 5 | SSR is CPU-bound |
| `them-agent-runtime` | CPU > 60%, or agent invocation rate | 2 | 20 | Primary scale-out target |
| `them-go-worker` | Temporal queue depth (KEDA) | 2 | 10 | Scale based on pending workflows |
| `them-dag-worker` | Temporal queue depth (KEDA) | 1 | 20 | `MaxConcurrentActivityExecutionSize` is the per-pod bottleneck |
| `them-mcp-service` | CPU > 60% | 2 | 5 | Leader does more work; non-leaders are idle on health |

### KEDA — Queue-Depth Based Scaling

[KEDA](https://keda.sh) is the correct tool for scaling Temporal workers based on actual queue backlog, not CPU proxy metrics.

**For `them-go-worker`:**
```yaml
# ScaledObject targeting them-go-worker Deployment
triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus:9090
      metricName: temporal_worker_task_slots_available
      threshold: "5"
      query: |
        temporal_workflow_task_schedule_to_start_latency_bucket{
          namespace="default",
          task_queue="them-orchestration-go"
        }
```

Or use KEDA's Temporal scaler if available for the SDK version in use.

**For `them-dag-worker`:**  
Same approach on `canvas-dag-nodes` queue. `DAG_WORKER_MAX_CONCURRENT_ACTIVITIES` controls per-pod throughput — KEDA controls pod count.

### Concurrency and Backpressure

The existing admission gate (`internal/gate/gate.go`) already implements per-entry-point concurrency limits and rate limiting using Redis. This mechanism works correctly in K8s with N replicas — Redis is the shared coordination layer.

In K8s, these limits become configurable per-application:
- `them:ep:{slug}:gate:queue:{slug}` BLPOP queue absorbs bursts
- Per-token rate limits (`rl:them:token:{hash}:{minute}`) are cross-replica via Redis INCR
- Per-app rate limits (`rl:them:app:{app_id}:{minute}`) are cross-replica

No code changes needed here.

### Graceful Shutdown and Connection Draining

| Service | Current state | K8s requirement |
|---|---|---|
| `them-go-bridge` | SIGTERM → 30s drain → Shutdown ✅ | Add `preStop: sleep 5` to let Ingress remove pod from rotation before drain starts |
| `them-auth-go` | SIGTERM → 20s drain → Shutdown ✅ | Add `preStop: sleep 5` |
| `them-go-worker` | SIGTERM → `goWorker.Stop()` ✅ | Temporal handles in-flight activity re-queuing |
| `them-dag-worker` | SIGTERM → `dagWorker.Stop()` ✅ | Temporal handles in-flight activity re-queuing |
| `them-mcp-service` | SIGTERM → leader lock release → 20s drain ✅ | Add `preStop: sleep 5` |
| `them-agent-runtime` | **No SIGTERM handler ❌** | Fix required before K8s migration |

**Recommended `preStop` pattern for HTTP services:**
```yaml
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 5"]
```

This gives the Ingress controller time to stop routing new requests to the pod before the pod begins draining.

### Rolling Updates

Use `RollingUpdate` strategy for all Deployments:

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
```

`maxUnavailable: 0` ensures capacity is never reduced during a deployment. For `them-go-bridge` with active WebSocket connections, set `terminationGracePeriodSeconds: 60` (matching `SHUTDOWN_DRAIN_SECONDS`).

### Pod Disruption Budgets

```yaml
# Ensure at least 1 replica of each critical service is always available
apiVersion: policy/v1
kind: PodDisruptionBudget
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: them-go-bridge
```

Apply PDBs to: `them-go-bridge`, `them-auth-go`, `them-go-worker`, `them-dag-worker`, `them-agent-runtime`.

### Anti-Affinity and Topology Spread

Spread replicas across nodes and availability zones:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: them-go-bridge
```

Critical for: `them-go-bridge` (WS connections), `them-go-worker` (Temporal polling), `them-agent-runtime` (agent invocations).

### Retry Behavior

The existing retry model is Temporal-managed (activity retry policies) for all workflow execution paths. HTTP retries for the bridge → agent-runtime path are not currently implemented — bridge calls `agent-runtime` once and passes the response to Temporal. In K8s, add a `retryOn: 5xx` policy at the service mesh level (Istio/Linkerd) or implement exponential backoff in the `invoke_agent` activity.

---

## Performance and Stability Requirements

### High Concurrency

**What needs attention:**
- Each WebSocket connection holds a goroutine pair (read + write) for its lifetime. At 1,000 concurrent connections, `them-go-bridge` is running 2,000+ goroutines. Go handles this efficiently, but set memory limits generously (≥512Mi per replica) and monitor `go_goroutines` via Prometheus.
- Postgres connection pool: 20 connections per bridge pod × 5 pods = 100 connections. Add PgBouncer in front of Postgres if scaling beyond 5–8 bridge replicas, or use managed Postgres with a high connection limit.
- Redis pub/sub: each bridge pod holds several pub/sub subscriptions. These are long-lived connections and count against Redis `maxclients`. Monitor and set Redis `maxclients` appropriately.

**Prometheus metrics** are already served at `/metrics` on the bridge — wire these into a K8s-native Prometheus stack (kube-prometheus-stack Helm chart).

### High Agent Execution Volume

- `them-dag-worker` is the primary throughput bottleneck — `MaxConcurrentActivityExecutionSize` (default 50) × pod count = total canvas agent concurrency. Scale pods via KEDA on queue depth.
- `them-agent-runtime` serves each agent invocation as an HTTP handler — stateless, scales horizontally without limit. Each invocation makes an LLM API call (Anthropic) — external API rate limits are the actual ceiling.
- Token bucket rate limits (per-app, per-token) in Redis are the gate. Adjust limits per-application as volume grows.

### Predictable Latency

- Set CPU `requests` = CPU `limits` for latency-sensitive services (auth, bridge) to avoid CPU throttling from burstable pods
- Use `Guaranteed` QoS class for bridge and auth pods
- Set `priorityClassName: system-cluster-critical` or a custom high-priority class for `them-go-bridge` and `them-auth-go`

### Worker Autoscaling

- KEDA on Temporal queue depth is the correct mechanism — do not use CPU-based HPA for Temporal workers
- Set `minReplicas: 2` for `them-go-worker` to avoid cold-start delay when work arrives
- `them-dag-worker` can start at `minReplicas: 1` if canvas execution is not always-on

### Failure Recovery

- Temporal guarantees workflow completion even if all worker pods are simultaneously unavailable — work is re-queued when workers restart
- Redis session data has 90s TTL — a full Redis restart causes all active sessions to expire (user reconnection required)
- Postgres connection pool has built-in retry logic via `pgxpool` — pool reconnects automatically
- The reconciler (60s tick, pg advisory lock) will detect stuck runs within ~120s of a worker crash and Temporal timeout

### Zero/Low Downtime Deployments

- `maxUnavailable: 0` rolling update strategy (described above)
- `preStop: sleep 5` lifecycle hook on all HTTP services
- WebSocket connections will be terminated on pod replacement — this is the nature of long-lived TCP connections. Clients should implement reconnect-with-resume (the frontend playground already does this via session resume logic)
- Temporal workflows survive pod replacement transparently

### Production Observability

Already partially in place:
- Prometheus metrics at `/metrics` on bridge ✅
- OTEL support in code (off by default — enable via `OTEL_ENABLED=true`) ✅
- Structured JSON logging (`LOG_FORMAT=json`) ✅
- Health endpoints on all services ✅

**Missing for K8s production:**
- Centralized log aggregation (ship to Loki, CloudWatch, or similar)
- Distributed tracing with OTEL endpoint (enable `OTEL_ENABLED`, configure `OTEL_EXPORTER_OTLP_ENDPOINT`)
- Temporal metrics (expose Temporal SDK metrics to Prometheus)
- Alert rules for: Temporal queue depth, Redis memory, Postgres connection count, error rate on `/api/v1`

---

## Migration Summary Table

| Component | K8s Ready | Difficulty | Required Changes | Scaling Strategy | Recommendation |
|---|---|---|---|---|---|
| `them-traefik` | Replace | Medium | Replace with K8s Ingress controller; translate all routing rules | Ingress controller scales independently | Replace — do not migrate Traefik Docker-socket mode |
| `them-postgres` | With changes | Medium | Migration framework, K8s init Job, TLS | Managed service or StatefulSet — no horizontal scale without read replicas | Managed service (RDS/CloudSQL) |
| `them-redis` | With changes | Low-Medium | Validate Lua scripts in cluster mode; check managed service limits | Managed service or StatefulSet | Managed service (ElastiCache/Memorystore) |
| `them-auth-go` | ✅ Yes | Low | None | HPA on CPU, min 2 | Migrate early — Phase 1 |
| `them-go-bridge` | ✅ Yes | Low | Fix hardcoded agent-runtime URL; Ingress WebSocket config; `preStop` | HPA on CPU, min 2 | Migrate early — Phase 1 |
| `them-go-worker` | ✅ Yes | Low | Add health endpoint; tune concurrency env vars | KEDA on Temporal queue depth | Migrate — Phase 2 |
| `them-dag-worker` | ✅ Yes | Low | Add health endpoint | KEDA on Temporal queue depth | Migrate — Phase 2 |
| `them-agent-runtime` | Needs fixes | Medium | **Add SIGTERM handler; fix hardcoded URL** | HPA on CPU or invocation rate | Fix then migrate — Phase 2 |
| `them-mcp-service` | ✅ Yes | Low | Make DB pool size env-configurable; add startup jitter | HPA on CPU, min 2 | Migrate — Phase 2 |
| `them-frontend` | ✅ Yes | Low | Update env vars for K8s DNS | HPA on CPU, min 2 | Migrate — Phase 1 |
| `temporal-frontend` | With changes | High | Switch to production Helm chart; pre-init DB; multi-service deploy | Temporal Helm chart manages its own scaling | Helm chart or Temporal Cloud — Phase 3 |
| `temporal-ui` | ✅ Yes | Low | None | Single replica | Migrate with Temporal |
| A2A test agents | ✅ Yes | Low | Separate test namespace | Single replica | Migrate — Phase 1 (non-prod) |
| Domain agents (vision, security, etc.) | ✅ Yes | Low | K8s Secrets for API keys | Single replica → HPA | Migrate — Phase 2 |
| `livekit` | Needs work | High | UDP exposure, NodePort or managed LiveKit | Managed service | LiveKit Cloud strongly recommended |
| `livekit-agent` | ✅ Yes | Low | Update LiveKit URL to K8s Service | Single replica | Migrate with LiveKit |

---

## Phased Migration Plan

### Phase 1 — Stateless Services (Lowest Risk)

**Goal:** Core HTTP services running in K8s with external managed PostgreSQL and Redis.

**Pre-conditions:**
- Managed PostgreSQL provisioned, schema initialized
- Managed Redis provisioned
- K8s Ingress controller deployed (Traefik K8s or NGINX)
- K8s Secrets created for all credentials
- `THE_M_AGENT_RUNTIME_BASE_URL` env var added (code fix)
- `agent-runtime` SIGTERM handler added (code fix)

**Services to migrate:**
1. `them-auth-go` — Deployment, 2 replicas, HPA
2. `them-frontend` — Deployment, 2 replicas
3. A2A test agents — Deployment, 1 replica each (test namespace)
4. Ingress routing rules for all Phase 1 services

**Validation:** Auth flow, admin login, agent browsing, health endpoints.

---

### Phase 2 — Workers and Runtime

**Goal:** Full orchestration stack in K8s with Temporal still external (Docker Compose or Cloud).

**Pre-conditions:**
- Phase 1 complete and stable
- Temporal accessible from K8s pods (network path open, `TEMPORAL_HOST_PORT` configured)
- KEDA installed in cluster

**Services to migrate:**
1. `them-go-bridge` — Deployment, 2 replicas, HPA, WebSocket Ingress config
2. `them-go-worker` — Deployment, 2 replicas, KEDA ScaledObject
3. `them-dag-worker` — Deployment, 1–2 replicas, KEDA ScaledObject
4. `them-agent-runtime` — Deployment, 2 replicas, HPA (after SIGTERM fix confirmed)
5. `them-mcp-service` — Deployment, 2 replicas
6. Domain agents — Deployment, 1 replica each

**Validation:** Full end-to-end run — WS connection, agent invocation, canvas workflow, MCP tool call, HITL flow.

---

### Phase 3 — Temporal and Infrastructure

**Goal:** Temporal running in-cluster or migrated to Temporal Cloud. PostgreSQL and Redis are managed services.

**Services to migrate:**
1. Temporal — Helm chart (`temporal/temporal`) with dedicated Postgres databases, or switch to Temporal Cloud
2. `temporal-ui` — Deployment, 1 replica

**Migration procedure for Temporal:**
- Provision `temporal` and `temporal_visibility` Postgres databases
- Run Temporal schema init Job (`temporal-admin-tools`) before deploying the chart
- Point all workers/bridge to new Temporal address
- Validate all active workflows continue executing

**Validation:** Existing in-flight workflows continue; new workflows start; Temporal UI shows correct state.

---

### Phase 4 — Autoscaling, HA Hardening, and Observability

**Goal:** Production-grade operation with full autoscaling, observability, and zero-downtime deployments.

**Tasks:**
1. Enable OTEL tracing — configure `OTEL_ENABLED=true`, deploy OpenTelemetry Collector
2. Deploy kube-prometheus-stack — wire all `/metrics` endpoints, Temporal metrics
3. Configure alert rules — Temporal queue depth, Redis memory, Postgres connections, error rates
4. Add PodDisruptionBudgets to all critical services
5. Add `topologySpreadConstraints` to spread replicas across nodes/AZs
6. Tune resource `requests` and `limits` based on observed production metrics
7. Set `priorityClassName` for critical services
8. Add PgBouncer if Postgres connection count exceeds limits
9. LiveKit — migrate to LiveKit Cloud or add NodePort UDP configuration
10. Load test — validate autoscaling triggers, graceful shutdown, and rolling update behavior under realistic traffic

---

## Appendix — Required Code Changes Before Migration

These are the specific code changes needed — none are architectural rewrites:

| Change | File | Effort |
|---|---|---|
| Add SIGTERM handler to `agent-runtime` | `go/cmd/agent-runtime/main.go` | ~30 min |
| Replace hardcoded `http://them-agent-runtime:9300/agents/...` with `THE_M_AGENT_RUNTIME_BASE_URL` env var | `go/cmd/agent-runtime/main.go:1012` | ~30 min |
| Replace hardcoded `http://them-agent-runtime:9300/agents/` in SQL | `go/internal/admin/dal/agent_definitions_publish.go:73` | ~30 min |
| Make `mcp-service` Postgres pool size configurable | `go/internal/mcp/config.go:89` | ~15 min |
| Add `/healthz` HTTP endpoint to `them-go-worker` | `go/cmd/worker/main.go` | ~1 hour |
| Add `/healthz` HTTP endpoint to `them-dag-worker` | `go/cmd/dag-worker/main.go` | ~1 hour |
| Add env vars for Temporal worker concurrency (`MaxConcurrentWorkflowTaskPollers`, etc.) | `go/cmd/worker/main.go`, `go/cmd/dag-worker/main.go` | ~1 hour |
| Add `automaxprocs` to all Go binaries for correct GOMAXPROCS under K8s CPU limits | all `cmd/*/main.go` | ~30 min |
| Add migration framework (Goose recommended) | new `go/cmd/migrate/` + migration files | ~4 hours |

Total estimated effort for required code changes: **~9 hours** across all items.

---

*This document reflects the state of the codebase as of 2026-08-31. Re-verify hardcoded values and env var lists before executing migration.*
