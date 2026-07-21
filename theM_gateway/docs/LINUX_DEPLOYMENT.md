# Linux Deployment Guide
# the-M — Multi-Agent Orchestration Platform

**Last updated:** 2026-07-21  
**Applies to:** Docker Engine >= 24, docker compose v2, Linux (Ubuntu 22.04/24.04)  
**Windows development:** unchanged — use `docker-compose.local.yml` as documented in CLAUDE.md

---

## Design: Go-first runtime

The Go gateway is the primary runtime on Linux. It owns all client-facing connections:

| Route | Owner | Why |
|---|---|---|
| `/ws/*` | **Go bridge** (both replicas, round-robin) | WS upgrade, session gate, run streaming |
| `/sse/*` | **Go bridge** (both replicas, round-robin) | SSE stream, Redis Streams replay |
| `/go-health/*` | **Go bridge** (path-rewritten by Traefik) | Liveness + readiness |
| `/api/v1/*` | Python bridge | Admin API — not yet rewritten in Go |
| `/health/*` | Python bridge | Python-side liveness |
| `/apps/*`, `/a2a/*` | Python bridge | App management, A2A server |
| `/temporal/*` | Temporal UI | Workflow dashboard |
| `/` | Frontend | Next.js dashboard |

Python components that still run alongside Go (because they are not yet rewritten):

| Service | Role | Replaceable? |
|---|---|---|
| `them-worker` | Temporal activity worker — LLM calls, tool routing, run recording | When Go activity rewrites land |
| `them-auth-service` | JWT issuing and user management | When Go auth service is complete |
| `them-bridge` | Admin API `/api/v1/*`, app/EP management | When Go admin API is complete |
| `them-frontend` | Next.js dashboard | When Go-rendered UI is built |

Redis Pub/Sub is kept active alongside Redis Streams (`RUN_EVENTS_MODE=dual` in integration/soak; `pubsub` in production). Phase 11c-D (remove Pub/Sub) requires explicit approval.

---

## Startup command

```bash
cd theM_gateway
./scripts/linux-start.sh [--build]
```

That is the single command for both first-time installs and subsequent restarts. It:
1. Validates `.env` (required secrets set, no placeholders)
2. Validates compose config
3. Starts Postgres + Redis, waits for health
4. Starts Temporal
5. Initializes DB schema if fresh; no-op if already initialized
6. Starts auth-service
7. Starts Python worker (Temporal activities)
8. Starts **both Go bridge replicas** (primary gateway)
9. Starts Traefik
10. Starts Python bridge (admin API) + frontend
11. Prints endpoint summary

---

## Compose file stacking

### Linux (deployment)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \       # ← Linux overlay: Go-first routes, named volumes
  -f docker-compose.integration.yml \ # ← Go bridge + host-port exposure
  -f docker-compose.soak.yml \        # ← Second Go bridge replica
  -f docker-compose.traefik.yml \     # ← WS/SSE/go-health Traefik labels
  --profile temporal up -d
```

### Windows (development)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \       # ← local.yml instead of linux.yml
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d
```

Only `docker-compose.linux.yml` vs `docker-compose.local.yml` differs.

---

## First-time setup

```bash
# 1. Clone and enter the gateway directory
git clone <repo-url> theM
cd theM/theM_gateway

# 2. Make deployment scripts executable
chmod +x scripts/linux-*.sh generate-env.sh

# 3. Generate secrets from a master passphrase
cp secrets.local.example secrets.local
#   Edit secrets.local: replace THE_M_MASTER_SECRET with a strong random value
#      openssl rand -hex 32
nano secrets.local
./generate-env.sh

# 4. Add ANTHROPIC_API_KEY (not derived — must be set manually)
echo "ANTHROPIC_API_KEY=sk-ant-..." >> .env

# 5. Start the stack (detects fresh DB and bootstraps schema automatically)
./scripts/linux-start.sh --build

# 6. Verify
./scripts/linux-health.sh
```

DB schema is initialized automatically at step 5. No separate migration command is needed on a fresh install.

---

## Database schema management

### Fresh install
`linux-start.sh` calls `scripts/linux-db-init.sh` internally. That script:
- Checks whether `them.runs` exists
- If absent: applies `db/001_schema.sql` + `auth_service/SCHEMA.sql` + `db/002_seed.sql` + all `db/[0-9][0-9][0-9]_*.sql` in order
- If present: no-op (prints "schema already initialized")

No historical replay is required. The numbered SQL files are idempotent — they can be applied to a fresh DB or an existing DB safely.

### Existing deployment (upgrade)
When new migrations are added to `db/`:
```bash
# Apply only the new file(s)
./scripts/linux-db-upgrade.sh db/026_new_feature.sql

# Or apply a range
./scripts/linux-db-upgrade.sh $(ls db/026_*.sql db/027_*.sql | sort)
```

`linux-db-upgrade.sh` does not replay all migrations — it applies only what you specify. Each file must be idempotent.

---

## Pre-deployment validation checklist

Run before every deployment to Linux staging or production.

### 1 — Compose config
```bash
docker compose \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml --profile temporal \
  config --quiet
echo "Compose config: OK"
```

### 2 — Go unit tests (on build host or CI)
```bash
cd go
go test ./...                 # must pass — zero failures
go test -race ./...           # requires gcc (apt-get install gcc on Ubuntu)
```

### 3 — Clean stack startup
```bash
cd theM_gateway
./scripts/linux-stop.sh --remove-orphans
./scripts/linux-start.sh --build
./scripts/linux-health.sh    # exits 0 = all checks passed
```

### 4 — DB schema verification
```bash
# Confirm events_transport column (Phase 11c)
docker exec them-postgres psql -U them -d them \
  -c "\d them.runs" | grep events_transport

# Confirm all tables present
docker exec them-postgres psql -U them -d them -c "\dt them.*"
```

### 5 — Go integration tests against live stack
```bash
cd go
REDIS_ADDR=localhost:16379 \
  go test -tags=integration -timeout 300s \
  -run "TestMAXLEN|TestIntegration_WS|TestIntegration_Cross" \
  ./internal/runstream/...
```

### 6 — Traefik route ownership verification
```bash
HOST_IP=$(hostname -I | awk '{print $1}')

# Go bridge owns /ws and /sse (expect 401/400 from Go, not Python)
curl -sf -o /dev/null -w "%{http_code}\n" \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  "http://${HOST_IP}:8088/ws/orchestrate/app/ep"
# Expected: 401 (Go requires auth) — NOT 404 or 426 from Python

# Go health via Traefik (path-rewritten: /go-health/* → /health/*)
curl -sf "http://${HOST_IP}:8088/go-health/live" | grep '"status":"ok"'
curl -sf "http://${HOST_IP}:8088/go-health/ready" | grep '"redis":"ok"'

# Python bridge still owns /api/v1
curl -sf -o /dev/null -w "%{http_code}\n" \
  "http://${HOST_IP}:8088/api/v1/admin/agents"
# Expected: 401 (JWT required)
```

### 7 — Prometheus metrics
```bash
# Both Go bridges must report them_runstream_mode
curl -sf http://localhost:8002/metrics | grep "them_runstream_mode"
curl -sf http://localhost:8003/metrics | grep "them_runstream_mode"
# Expected: them_runstream_mode 1 (dual) or 0 (pubsub)
```

### 8 — Multi-replica restart
```bash
# Stop one bridge, verify the other serves Traefik health check
docker stop --timeout=10 them-go-bridge-2
curl -sf http://localhost:8088/go-health/live | grep '"status":"ok"'

# Restart and verify both healthy
docker start them-go-bridge-2
sleep 8
docker inspect them-go-bridge-2 --format='{{.State.Health.Status}}'
# Expected: healthy
```

### 9 — Graceful shutdown verification
```bash
docker stop --timeout=15 them-go-bridge
EXIT=$(docker inspect them-go-bridge --format='{{.State.ExitCode}}')
echo "Exit code: ${EXIT}  (expected: 0)"
docker start them-go-bridge
```

### 10 — MAXLEN / replay scenarios
```bash
cd go
REDIS_ADDR=localhost:16379 \
  go test -tags=integration -timeout 300s \
  -run "TestMAXLEN" -v ./internal/runstream/...
```

---

## Route priority map (Traefik)

| Priority | Router | Path | Service |
|---|---|---|---|
| 150 | `them-temporal-ui` | `/temporal` | Temporal UI |
| 120 | `them-go-health` | `/go-health` | Go bridge (path-rewritten) |
| 110 | `them-go-ws` | `/ws` | Go bridge (both replicas) |
| 110 | `them-go-sse` | `/sse` | Go bridge (both replicas) |
| 100 | `them-api` | `/api/v1` | Python bridge |
| 100 | `them-apps` | `/apps` | Python bridge |
| 100 | `them-a2a` | `/a2a` | Python bridge |
| 90 | `them-health` | `/health` | Python bridge |
| 10 | `them-ui` | `/` | Frontend |

Go owns all client-facing routes (WS, SSE, health). Python bridge never sees WebSocket upgrades.

---

## Key differences: Windows vs Linux

| Aspect | Windows dev | Linux deployment |
|---|---|---|
| Compose overlay | `docker-compose.local.yml` | `docker-compose.linux.yml` |
| Python bridge WS/SSE routes | Registered (lower priority, Go wins) | **Not registered** — Go owns exclusively |
| Source bind mounts | `.:/app` for live reload | Removed — code baked into image |
| Data persistence | `./data/` bind mounts | Named Docker volumes |
| Traefik dashboard | `127.0.0.1:8089` | `0.0.0.0:8089` (all interfaces) |
| Secret generation | `.\generate-env.ps1` | `./generate-env.sh` |
| DB schema init | Manual `scripts/init_db.sh` | `linux-start.sh` → `linux-db-init.sh` (auto) |
| Python test runner | `python3.12` (explicit) | Auto-detected (3.10+) |

---

## Scripts reference

| Script | Purpose | When to run |
|---|---|---|
| `linux-start.sh` | Start full stack (validates env, inits DB, starts all services) | Every deploy / restart |
| `linux-stop.sh` | Graceful shutdown (SIGTERM, configurable timeout) | Before maintenance or redeploy |
| `linux-db-init.sh` | Initialize fresh DB schema (called by start; no-op if exists) | Called automatically |
| `linux-db-upgrade.sh` | Apply specific new migration files to existing deployment | When new `db/*.sql` land |
| `linux-health.sh` | Verify all containers + HTTP endpoints healthy | After start; in CI |
| `linux-logs.sh` | Collect logs per service, optionally archive | Debugging; incident response |
| `linux-rollback.sh` | Roll back Go bridge to a previous image tag | After a bad Go deploy |

---

## Rollback

```bash
# List available Go bridge images
./scripts/linux-rollback.sh --list

# Roll back to a specific tag
./scripts/linux-rollback.sh --tag them_gateway-them-go-bridge:20260721-abc1234

# Verify
./scripts/linux-health.sh
```

Python bridge, worker, and database are not rolled back this way — use a DB backup restore for schema rollbacks (outside scope of this runbook).

---

## Known considerations

| Item | Status |
|---|---|
| Containers run as root (except `them-auth-service`) | Acceptable for private network; add `user:` for hardened envs |
| Race detector requires gcc | `apt-get install -y gcc` on test runner or CI image |
| `auth_service/docker-compose.yml` references legacy `omni` DB | Orphaned file — do not use standalone |
| Traefik dashboard on `0.0.0.0:8089` | Restrict with firewall to trusted IPs |
| `RUN_EVENTS_MODE` defaults to `pubsub` in `.env.linux.example` | Change to `dual` for staging validation; `streams` requires Phase 11c-D approval |
