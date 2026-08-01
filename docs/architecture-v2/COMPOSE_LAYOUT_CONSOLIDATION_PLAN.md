# Compose Layout Consolidation Plan
**Date:** 2026-08-01
**Purpose:** Determine what must be preserved before moving the running Compose deployment from `theM_gateway/` to the repository root (`/home/avi/them`).
**Status:** Plan only — nothing executed.

---

## Executive Summary

The running production stack (`project=them_gateway`) was launched from `/home/avi/them/theM_gateway` using **7 layered Compose files**. The repository root (`/home/avi/them`) has only 2 Compose files. The legacy stack uses features — named Docker volumes, Cloudflare proxy-network integration, multi-file Traefik wave routing, Hetzner build-context overrides — that do not exist at the root.

**The root Compose is NOT currently sufficient to recreate the running stack.**

A migration requires merging 5 legacy overlay files into root-compatible equivalents before any stack restart.

---

## 1. Compose Structure Comparison

### Files in root only (not in `theM_gateway/`)
| File | Purpose |
|---|---|
| `docker-compose.local.yml` | Local dev overlay (proxy-network local, PathPrefix-only Traefik, Go Worker definitions) |

### Files in `theM_gateway/` only (not at root)
| File | Purpose | Critical for production? |
|---|---|---|
| `docker-compose.linux.yml` | Linux deployment: named volumes, production frontend, Python admin-only Traefik labels, removes source bind mounts | **YES** — production data storage |
| `docker-compose.traefik.yml` | All Wave 1–7 Go bridge Traefik routing labels (Waves 1–7) | **YES** — all Go route ownership |
| `docker-compose.integration.yml` | Go Worker definitions (them-go-worker, them-go-worker-2), Go bridge with full env, exposed host ports for testing | **YES** — Go Workers run from here |
| `docker-compose.soak.yml` | Second Go bridge replica (them-go-bridge-2), port 8003 | **YES** — currently running |
| `docker-compose.hetzner-build.yml` | Build contexts → `..` (parent repo), named volume overrides for postgres/redis/logs | **YES** — required for Hetzner layout |
| `docker-compose.cloudflare.yml` | `proxy-network` override → external `proxy-network` (Cloudflare tunnel ingress) | **YES** — public traffic routing |

### Services comparison: root `docker-compose.yml` vs legacy `docker-compose.yml`

Both base `docker-compose.yml` files are **identical in service definitions** (sha256 hashes differ only because root was updated independently). Core services are the same — the difference is in the overlay files.

**Services only in the root Compose system (missing from legacy):**
- `them-go-worker` / `them-go-worker-2` — in `docker-compose.local.yml` at root; NOT in any legacy base file (but ARE in `docker-compose.integration.yml`)

**Services only in the legacy system (missing from root):**
- `them-logs` (named log volume service) — in `docker-compose.linux.yml`
- `them-postgres-data` / `them-redis-data` (named volume services) — in `docker-compose.linux.yml`

**Services in both systems:**

| Service | Root profile | Legacy profile | Notes |
|---|---|---|---|
| `them-traefik` | (core) | (core) | Cloudflare overlay adds external proxy-network |
| `them-postgres` | (core) | (core) | Legacy uses named volume; root uses bind mount `./data/` |
| `them-redis` | (core) | (core) | Same — legacy uses named volume |
| `them-auth-service` | (core) | (core) | Identical |
| `them-bridge` | (core) | (core) | Legacy: no `/ws` or `/sse` Traefik labels; root has them |
| `them-bridge-2` | `replica` | (core, no profile) | Root is opt-in profile; legacy always enabled |
| `them-frontend` | (core) | (core) | Legacy: `NODE_ENV=production`, no source bind mount |
| `them-worker` | `temporal` | `temporal` | Legacy: no source bind mount |
| `them-go-bridge` | `go` | `temporal` | **Profile differs**: legacy starts Go bridge with temporal |
| `them-go-bridge-2` | not in root base | `temporal` (via soak.yml) | Root has no definition for go-bridge-2 |
| `them-go-worker` | `go-worker` (local.yml) | `temporal` (integration.yml) | **Profile differs**: legacy starts with temporal profile |
| `them-go-worker-2` | `go-worker` (local.yml) | `temporal` (integration.yml) | Same |
| `temporal-frontend` | `temporal` | `temporal` | Identical |
| `temporal-ui` | `temporal` | `temporal` | Identical |
| `temporal-admin-tools` | `temporal` | `temporal` | Identical |
| `a2a-echo/slow/stream` | `test-agents` | `test-agents` | Identical |
| `agent-evidence/logic/creative/judge` | `debate` | `debate` | Identical |
| `docu-writer` | `docu` | `docu` | Identical |
| `them-security-agent` | `security` | `security` | Identical |
| `them-workflow-advisor` | `advisor` | `advisor` | Identical |
| `vision-agent` | (core) | (core) | Identical |
| `livekit` / `livekit-agent` | `voice` | `voice` | Identical |

---

## 2. Go Services Analysis

### `them-go-bridge` and `them-go-bridge-2`
- **Defined in:** `theM_gateway/docker-compose.yml` (base), with build context override in `docker-compose.hetzner-build.yml` (context: `..`), Traefik labels in `docker-compose.traefik.yml`, soak replica in `docker-compose.soak.yml`
- **Root definition:** `docker-compose.yml` (profile: `go`), with basic Traefik labels (Wave 1 and partial Wave 7 only). Does NOT include Wave 2–5 Traefik routes (admin writes, WS/SSE, tokens, sessions).
- **Critical gap:** The root `docker-compose.yml` Go bridge labels are **a subset** of what `docker-compose.traefik.yml` provides. Root is missing: Wave 2 (admin writes), Wave 4 (WS/SSE), Wave 5 (tokens/sessions), Wave 6/7 (llm-providers via `them-go-svc` service name vs `them-go-bridge-svc`).
- **Service name discrepancy:** Legacy uses `them-go-svc` (shared LB pool for both bridges); root uses `them-go-bridge-svc` (single-bridge service). These are incompatible if combined.

### `them-go-worker` and `them-go-worker-2`
- **Currently running as:** `project=them`, launched via `docker run` (ad-hoc — no Compose working_dir label)
- **Defined in legacy:** `docker-compose.integration.yml` — profile: `temporal`; env includes `REDIS_PASSWORD` (missing), `JWT_SECRET`, proper `SECRET_KEY` fallback
- **Defined in root:** `docker-compose.local.yml` — profile: `go-worker`; env missing `JWT_SECRET`, `REDIS_PASSWORD`
- **Critical difference:** Legacy integration file uses `Dockerfile.go-worker` (no dot); root uses `Dockerfile.go.worker` (with dot). Both files exist at `/home/avi/them/`:
  - `Dockerfile.go-worker` — created 2026-07-29, 900 bytes (older, likely canonical for legacy stack)
  - `Dockerfile.go.worker` — created 2026-08-01, 592 bytes (created this session, smaller)
- **Root workers cannot be launched via Compose with `docker compose up`** if the `.env` doesn't exist at the root — `${THE_M_SECRET_KEY:-change-this-secret-key}` falls back to a rejected default.

### Can root Compose recreate all four Go services correctly?
**No.** Missing:
1. Traefik Wave 2–5 routing for Go bridges (must be added)
2. `them-go-bridge-2` definition (missing from root entirely)
3. Go Workers using correct profile and env (profile mismatch: `go-worker` vs `temporal`)
4. `REDIS_PASSWORD` env var for workers (missing from root local.yml)
5. `JWT_SECRET` env var for workers (missing from root local.yml)

---

## 3. Infrastructure Services

### PostgreSQL
| Aspect | Root | Legacy |
|---|---|---|
| Image | `postgres:16-alpine` | same |
| Data storage | bind mount `./data/them-postgres/pgdata` | **named volume** `them-postgres-data` |
| Init scripts | bind `./postgres/init` | bind `../postgres/init` (parent) |
| Credentials | `${THE_M_DB_USER:-them}` / `${THE_M_DB_PASSWORD:-them_secret}` | same |

**Named volume `them-postgres-data` exists and contains live data. A migration to root must preserve this volume.**

### Redis
| Aspect | Root | Legacy |
|---|---|---|
| Image | `redis:7-alpine` | same |
| Data storage | bind mount `./data/them-redis` | **named volume** `them-redis-data` |
| Config | bind `./redis/config/redis.conf` | bind `../redis/config/redis.conf` (parent) |

**Named volume `them-redis-data` exists and contains live data.**

### Temporal
- Identical in both: `temporalio/auto-setup:1.25.2`, postgres12 backend, `them-postgres` seed
- Temporal state (workflow history) is stored in Postgres tables `temporal` and `temporal_visibility` — preserved as long as the Postgres volume is preserved.

### Traefik
- Root: no Cloudflare / proxy-network integration; Traefik dashboard bound to `127.0.0.1:8089`
- Legacy: `docker-compose.cloudflare.yml` joins `proxy-network` (external, shared with `infra-traefik`) — this is the public traffic path via Cloudflare → `infra-traefik` → `them-traefik:8088`
- Config file: both point to `traefik/traefik.yml` (legacy uses `../traefik/traefik.yml` via hetzner-build.yml). Running container mounts `/home/avi/them/traefik/traefik.yml` — same file regardless of working dir.

### Python Bridge / Worker
- Root: source bind mount `.:/app` → `/app` (dev mode, live code reload)
- Legacy: **no source bind mount** (code baked into image); logs use named volume `them-logs`
- Running container confirms: `bind:/home/avi/them->/app` — code is live-mounted from repo root

### Auth Service
- Identical in both systems.

### Monitoring Services
- None defined in either system.

### Cloudflare Tunnel
- Defined only in legacy: `docker-compose.cloudflare.yml`
- Overrides `proxy-network` to be `external: true` (name: `proxy-network`)
- `infra-cloudflared` (separate `infrastructure` project) routes `them.avico78.com` → `infra-traefik:80` → `them-traefik:8088`
- Root has no equivalent.

### Integration / Soak
- `docker-compose.integration.yml`: exposes host ports 15432/16379/17233, defines Go Workers and Go bridge with full env
- `docker-compose.soak.yml`: adds `them-go-bridge-2` on port 8003
- Root has no equivalent files.

---

## 4. Persistent Data

### Named Docker volumes (live data — DO NOT DELETE)
| Volume | Container | Contents | At risk if root Compose used? |
|---|---|---|---|
| `them-postgres-data` | `them-postgres` | All application DB data + Temporal state | **YES** — root uses bind mount, not this volume |
| `them-redis-data` | `them-redis` | Redis persistence (RDB/AOF) | **YES** — root uses bind mount |
| `them-logs` | `them-bridge`, `them-worker` | Application log files | YES (minor) |

### Bind mounts (from running containers)
| Mount source | Container | Notes |
|---|---|---|
| `/home/avi/them` → `/app` | `them-bridge`, `them-worker` | Live code mount — points to repo root |
| `/home/avi/them/frontend` → `/app` | `them-frontend` | Live frontend source |
| `/home/avi/them/traefik/traefik.yml` | `them-traefik` | Traefik config |
| `/home/avi/them/redis/config/redis.conf` | `them-redis` | Redis config |
| `/home/avi/them/postgres/init` | `them-postgres` | DB init scripts |
| `/var/run/docker.sock` | `them-traefik` | Docker API access |

**Critical:** `them-postgres` and `them-redis` use named volumes (`them-postgres-data`, `them-redis-data`), NOT bind mounts under `./data/`. If Compose were run from the root without preserving these volume names, a new Postgres/Redis would start with empty storage.

---

## 5. Environment Loading

### `theM_gateway/` (legacy — running stack)
- `.env` **EXISTS** at `/home/avi/them/theM_gateway/.env` — loaded automatically by Compose
- `secrets.local` **EXISTS** — used to regenerate `.env` via `generate-env.sh`
- No `env_file:` directives in any service — all env comes from `.env` interpolation
- Variables defined (names only): `ANTHROPIC_API_KEY`, `APP_ENV`, `DEBATE_ANTHROPIC_API_KEY`, `DOCU_WRITER_ANTHROPIC_API_KEY`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`, `LIVEKIT_PUBLIC_URL`, `LOG_LEVEL`, `SECURITY_SCANNER_ANTHROPIC_API_KEY`, `THE_M_BRIDGE_WS_URL`, `THE_M_CORS_ORIGINS`, `THE_M_DB_PASSWORD`, `THE_M_DB_USER`, `THE_M_HOSTNAME`, `THE_M_JWT_SECRET`, `THE_M_REDIS_PASSWORD`, `THE_M_SECRET_KEY`, `THE_M_UI_HOSTNAME`

### Root (`/home/avi/them/`)
- `.env` **DOES NOT EXIST**
- `secrets.local` **DOES NOT EXIST**
- All `${VAR:-fallback}` expressions resolve to their fallback values — all secrets are wrong

### Required variables for Go Bridge and Go Worker
| Variable | Go Bridge | Go Worker | Notes |
|---|---|---|---|
| `SECRET_KEY` | Required | Required | Config validation rejects `change-this-secret-key` / `change-this-in-production` |
| `JWT_SECRET` | Required for HS256 token validation | Required | Missing from root `docker-compose.local.yml` workers |
| `DATABASE_PASSWORD` | Required | Required | Wrong fallback at root |
| `THE_M_REDIS_PASSWORD` | Required if set | Required if set | Missing from root `docker-compose.local.yml` workers |
| `ANTHROPIC_API_KEY` | Required for LLM | Required for LLM | |
| `THE_M_DB_USER` / `THE_M_DB_PASSWORD` | Required | Required | |

### Insecure fallbacks in root Compose
| Service | Variable | Fallback | Validation outcome |
|---|---|---|---|
| `them-go-bridge` (root `docker-compose.yml`) | `SECRET_KEY` | `change-this-in-production` | **FATAL** — rejected by config.validate() |
| `them-go-worker` / `them-go-worker-2` (local.yml) | `SECRET_KEY` | `change-this-secret-key` | **FATAL** — also the `DefaultSecretKey` constant, rejected |
| All services | `DATABASE_PASSWORD` | `them_secret` | FAIL — wrong password for production DB |

---

## 6. Docker Project Behavior — What Must Be Preserved

### Compose project name
- Running project name: `them_gateway` (derived from directory name `theM_gateway`)
- Root project name (if run from `/home/avi/them`): would be `them`
- **Impact:** Docker networks, volumes, and container names are all prefixed or associated with the project name. Named volumes (`them-postgres-data`, `them-redis-data`, `them-logs`) are NOT prefixed — they will survive a project name change. Container names are explicit (`container_name: them-postgres`) — they are also safe. **The project name change itself is low risk** as long as container names and volume names are explicit.

### Networks
| Network | Type | Creator | Notes |
|---|---|---|---|
| `them-network` | bridge, name=`them-network` | base `docker-compose.yml` | All internal traffic — safe to recreate (transient) |
| `proxy-network` | external | `docker-compose.cloudflare.yml` | Must remain external, name=`proxy-network` — already managed by `infra-traefik` |

### Volumes
| Volume | Named? | Must preserve? |
|---|---|---|
| `them-postgres-data` | YES (`name: them-postgres-data`) | **CRITICAL** |
| `them-redis-data` | YES (`name: them-redis-data`) | **CRITICAL** |
| `them-logs` | YES (`name: them-logs`) | Low risk (logs only) |

### Database data
- Lives in `them-postgres-data` Docker named volume
- Postgres tables: `them` schema (application), `auth_service` schema, `temporal` + `temporal_visibility` schemas
- **Not in any bind-mount path** — cannot be accidentally overwritten by running Compose from a different directory

### Redis data
- Lives in `them-redis-data` Docker named volume
- Contains: session data (short TTL), rate limit counters, agent/orchestrator cache, Redis Streams
- **Not in any bind-mount path**

### Temporal state
- Workflow history stored in Postgres `temporal` and `temporal_visibility` databases
- Preserved as long as `them-postgres-data` volume is preserved

### Traefik routing
- Configuration from `traefik/traefik.yml` bind-mounted from `/home/avi/them/traefik/traefik.yml`
- Routing rules from container labels — replayed when Traefik restarts
- Cloudflare integration via external `proxy-network` — must be preserved in new Compose

---

## 7. Migration Plan (not executed)

### Stage A — Prepare Root Compose

**A1. Copy `.env` and `secrets.local` to root**
```bash
# Only after confirming no active sessions
cp /home/avi/them/theM_gateway/.env /home/avi/them/.env
cp /home/avi/them/theM_gateway/secrets.local /home/avi/them/secrets.local
```
These are gitignored at root — safe to copy.

**A2. Create `docker-compose.linux.yml` at root**
Content: copy `theM_gateway/docker-compose.linux.yml` exactly, changing:
- `./postgres/init` → `./postgres/init` (already correct)
- `./redis/config/redis.conf` → `./redis/config/redis.conf` (already correct)
- Volume definitions for `them-postgres-data`, `them-redis-data`, `them-logs`
- Python bridge Traefik labels (admin-only, no WS/SSE)
- Frontend `NODE_ENV=production`, no source bind mount

**A3. Create `docker-compose.traefik.yml` at root**
Content: copy `theM_gateway/docker-compose.traefik.yml` exactly (it uses no relative paths — only container names and Traefik labels). No changes needed.

**A4. Create `docker-compose.soak.yml` at root**
Content: copy `theM_gateway/docker-compose.soak.yml` exactly.

**A5. Create `docker-compose.cloudflare.yml` at root**
Content: copy `theM_gateway/docker-compose.cloudflare.yml` exactly.

**A6. Update Go Worker definitions**
In `docker-compose.integration.yml` or a new `docker-compose.workers.yml` at root:
- Add `JWT_SECRET`, `REDIS_PASSWORD` to worker env (missing from root `docker-compose.local.yml`)
- Use `Dockerfile.go-worker` (the canonical file, 2026-07-29) not `Dockerfile.go.worker` (created this session)
- Set profile to `temporal` (matching how they currently run alongside temporal stack)
- Add proper `depends_on: temporal-frontend` (valid when in same profile)

**A7. Fix `docker-compose.yml` line 891**
Change `SECRET_KEY=${THE_M_SECRET_KEY:-change-this-in-production}` to `change-this-secret-key` for consistency. (Minor — the `.env` value always wins in practice.)

### Stage B — Validate Rendered Config

Before touching any running container:
```bash
cd /home/avi/them
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  config > /tmp/rendered_root.yml

# Compare with running legacy config
cd /home/avi/them/theM_gateway
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.hetzner-build.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  config > /tmp/rendered_legacy.yml

diff /tmp/rendered_legacy.yml /tmp/rendered_root.yml
```
Expected differences: `hetzner-build.yml` context overrides replaced by root-relative paths (which are identical since all source is in `/home/avi/them`).

### Stage C — Validate Images

```bash
# Verify existing images match expected code
docker images | grep them_gateway
# For each critical service, confirm the image digest matches what root would build:
# docker buildx build --no-cache --platform linux/amd64 -f Dockerfile.go . --iidfile /tmp/go-root.iid
# Compare with: docker inspect them-go-bridge --format '{{.Image}}'
```

### Stage D — Add Go Workers to Root Compose Under Temporal Profile

Add to root `docker-compose.integration.yml` (or new file):
```yaml
them-go-worker:
  build:
    context: .
    dockerfile: Dockerfile.go-worker
  container_name: them-go-worker
  profiles: [temporal]
  environment:
    THE_M_INSTANCE_ID: "go-worker-1"
    APP_ENV: "production"
    DATABASE_HOST: "them-postgres"
    DATABASE_PORT: "5432"
    DATABASE_NAME: "them"
    DATABASE_USER: ${THE_M_DB_USER:-them}
    DATABASE_PASSWORD: ${THE_M_DB_PASSWORD:-them_secret}
    REDIS_HOST: "them-redis"
    REDIS_PORT: "6379"
    REDIS_PASSWORD: ${THE_M_REDIS_PASSWORD:-}
    REDIS_DB: "0"
    SECRET_KEY: ${THE_M_SECRET_KEY:-change-this-secret-key}
    JWT_SECRET: ${THE_M_JWT_SECRET:-}
    LOG_FORMAT: "json"
    LOG_LEVEL: ${LOG_LEVEL:-INFO}
    ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY:-}
    TEMPORAL_ENABLED: "true"
    TEMPORAL_HOST_PORT: "temporal-frontend:7233"
    WORKER_TASK_QUEUE: "them-orchestration-go"
    RUN_EVENTS_MODE: ${RUN_EVENTS_MODE:-streams}
  networks:
    - them-network
  depends_on:
    them-postgres:
      condition: service_healthy
    them-redis:
      condition: service_healthy
    temporal-frontend:
      condition: service_started
  restart: unless-stopped

# them-go-worker-2: identical except THE_M_INSTANCE_ID=go-worker-2
```

### Stage E — Preserve Project Name, Networks, Volumes

The key mechanism is the `--project-name` flag:
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml \
  ... \
  config
```
Using `--project-name them_gateway` when running from the root causes Docker to adopt the same project name as the legacy stack. This means:
- Existing containers are recognized as belonging to this project
- Existing volumes are adopted (not recreated)
- `docker compose up` will only recreate services whose definition changed

Alternatively — and simpler — create a `compose.override.yml` or set `COMPOSE_PROJECT_NAME=them_gateway` in the root `.env`.

### Stage F — Controlled Recreate

```bash
cd /home/avi/them

# Optional: add COMPOSE_PROJECT_NAME=them_gateway to .env
# Then run with same profile as legacy stack
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  up -d --no-recreate
```

`--no-recreate` prevents restarting containers whose config is unchanged. Only services with actual definition changes are recreated.

### Stage G — Health Validation

```bash
# Core services
docker compose --project-name them_gateway ... ps
docker exec them-postgres pg_isready -U them -d them
docker exec them-redis redis-cli ping
curl -s http://localhost:8088/health/live
curl -s http://localhost:8088/health/ready

# Go bridges
curl -s http://localhost:8088/health/live   # → Go (priority 130)
docker logs them-go-bridge --tail 5
docker logs them-go-bridge-2 --tail 5

# Go workers
docker logs them-go-worker --tail 5   # → "Go Worker polling"
docker logs them-go-worker-2 --tail 5

# Python bridge
curl -s http://localhost:8088/api/v1/admin/agents  # → Go (GET, priority 110)
docker logs them-bridge --tail 5

# Temporal
docker exec temporal-admin-tools temporal workflow list --namespace default

# Python test suite
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

### Stage H — Rollback Procedure

If anything fails after Stage F:
```bash
# Stop the root-launched stack
docker compose --project-name them_gateway ... down --volumes=false
# --volumes=false CRITICAL: do NOT remove named volumes

# Restart from legacy directory
cd /home/avi/them/theM_gateway
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.hetzner-build.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  up -d
```

Named volumes (`them-postgres-data`, `them-redis-data`) survive both the `down` and the re-up because `--volumes=false` is the default for `down` — but always verify this flag is not accidentally set to `true`.

---

## Summary Tables

### Services only in legacy layout
| Service | File | Notes |
|---|---|---|
| `them-logs` (volume-service alias) | `docker-compose.linux.yml` | Non-critical |
| `them-postgres-data` (volume-service alias) | `docker-compose.linux.yml` | Non-critical |
| `them-redis-data` (volume-service alias) | `docker-compose.linux.yml` | Non-critical |

### Services only in root layout
None that are currently running. (Root `docker-compose.local.yml` defines workers under `go-worker` profile, but those run ad-hoc currently.)

### Critical configuration that must be preserved
1. Named volumes: `them-postgres-data`, `them-redis-data`, `them-logs`
2. External `proxy-network` (Cloudflare ingress path)
3. All 7 layers of Traefik routing labels (Waves 1–7 in `docker-compose.traefik.yml`)
4. Hetzner build contexts (`..` from `theM_gateway/` = `.` from root — equivalent, but must be verified)
5. `Dockerfile.go-worker` (canonical, 2026-07-29) not `Dockerfile.go.worker` (created this session)
6. `SECRET_KEY` / `JWT_SECRET` / `REDIS_PASSWORD` in worker env
7. `--project-name them_gateway` to preserve container identity

### Persistent volumes at risk
All three named volumes (`them-postgres-data`, `them-redis-data`, `them-logs`) would be at risk if:
- Root Compose were run without `--no-recreate` and without `--project-name them_gateway`
- `down --volumes` were accidentally used

### Whether root Compose is currently sufficient
**NO.** Root is missing: linux.yml overlay (named volumes), traefik.yml (Wave 2–5 routing), soak.yml (go-bridge-2), cloudflare.yml (public ingress), integration.yml-equivalent (Go Workers with correct env), and the `.env` file.

### Files that need merging / creating at root
1. `docker-compose.linux.yml` — copy from theM_gateway (update relative paths → root-relative)
2. `docker-compose.traefik.yml` — copy verbatim (no relative paths)
3. `docker-compose.soak.yml` — copy verbatim
4. `docker-compose.cloudflare.yml` — copy verbatim
5. `docker-compose.integration.yml` — copy, update build contexts from `..` to `.`
6. `.env` — copy from `theM_gateway/.env`
7. `secrets.local` — copy from `theM_gateway/secrets.local`

### Recommended migration command sequence
```bash
# 0. Copy secrets (do this first)
cp theM_gateway/.env .env
cp theM_gateway/secrets.local secrets.local

# 1. Create overlay files (Stages A2–A6 above)

# 2. Validate rendered config
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml -f docker-compose.cloudflare.yml \
  --profile temporal config > /tmp/rendered_root.yml

# 3. Diff against legacy render
cd theM_gateway && docker compose ... config > /tmp/rendered_legacy.yml
diff /tmp/rendered_legacy.yml /tmp/rendered_root.yml
cd ..

# 4. Adopt stack (no-recreate first pass)
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml -f docker-compose.cloudflare.yml \
  --profile temporal up -d --no-recreate

# 5. Validate health
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

### Rollback approach
```bash
docker compose --project-name them_gateway ... down  # (never --volumes)
cd theM_gateway
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml -f docker-compose.hetzner-build.yml \
  -f docker-compose.cloudflare.yml --profile temporal up -d
```
