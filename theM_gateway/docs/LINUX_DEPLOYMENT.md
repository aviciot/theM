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

| Service | Role | When replaceable |
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
5. Bootstraps DB schema if fresh; no-op if already initialized (via `linux-db-init.sh`)
6. Starts auth-service
7. Starts Python worker (Temporal activities); waits for readiness marker
8. Starts **both Go bridge replicas** (primary gateway)
9. Starts Traefik
10. Starts Python bridge (admin API) + frontend
11. Prints endpoint summary

---

## Database schema management

### Schema snapshot vs. migration replay

The-M uses a **snapshot-based fresh install** combined with **versioned upgrade migrations**:

| Scenario | Tool | File |
|---|---|---|
| Fresh install | `linux-db-init.sh` | `db/schema_current.sql` |
| Upgrade existing | `linux-db-upgrade.sh` | `db/NNN_name.sql` |
| Recovery / debug replay | `linux-db-legacy-replay.sh` | all `db/*.sql` |

Fresh installations **never replay the migration history** (001–025). They apply the current schema snapshot directly in a single pass.

### `db/schema_current.sql` — the canonical snapshot

This file:
- Creates both schemas (`them`, `auth_service`)
- Creates `them.schema_migrations` for tracking
- Creates all tables at their current final shape (no ALTER chains)
- Creates all indexes and constraints
- Seeds minimal required system data (LLM providers, middleware defs, config defaults)
- Records versions 001–025 as applied in `schema_migrations`
- Does **not** insert demo agents, orchestrators, or user accounts

When a new migration file is added (e.g., `db/026_name.sql`), **also update `schema_current.sql`** to incorporate that change. The snapshot and migrations must stay in sync.

### Migration tracking: `them.schema_migrations`

```sql
CREATE TABLE them.schema_migrations (
    version     TEXT        PRIMARY KEY,
    description TEXT        NOT NULL DEFAULT '',
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    checksum    TEXT
);
```

Every migration records itself here after successful application. The table is created by `schema_current.sql` and pre-populated with versions 001–025.

### Detection logic in `linux-db-init.sh`

| State | Outcome |
|---|---|
| `schema_migrations` table absent | Fresh DB — apply `schema_current.sql` |
| `schema_migrations` present, rows > 0 | Initialized — no-op, exit 0 |
| `schema_migrations` present, rows = 0 | Partial/failed init — **error** (use `--force-fresh` only after investigation) |

### Advisory lock — single-session guarantee

Both `linux-db-init.sh` and `linux-db-upgrade.sh` acquire `pg_try_advisory_lock(987654321)` before touching the schema.

**Critical implementation detail:** PostgreSQL advisory locks are session-scoped. Acquiring the lock in one `psql` call and applying the schema in another connection releases the lock the moment the first session ends. Both scripts therefore run the entire operation — acquire lock, validate state, apply SQL, record in `schema_migrations` — within a **single persistent `psql` session** using a streamed heredoc. The lock is held until the session exits (on success or failure).

If another process holds the lock, the script exits immediately with a diagnostic message.

### Fresh install (no data needed)

```bash
# linux-start.sh calls linux-db-init.sh automatically.
# No demo data, no user accounts — just the schema.
./scripts/linux-start.sh --build
```

### Fresh install with dev user accounts (dev/staging only)

```bash
./scripts/linux-db-init.sh --seed-users
# or via linux-start.sh after it calls init:
docker exec -i them-postgres psql -U them -d them < db/seed_users.sql
```

**Never use `--seed-users` on production.** User accounts are managed through the auth-service API.

### Upgrade existing deployment

```bash
# Apply specific new migration file(s)
./scripts/linux-db-upgrade.sh db/026_new_feature.sql

# Apply a range
./scripts/linux-db-upgrade.sh db/026_one.sql db/027_two.sql
```

`linux-db-upgrade.sh` checks `schema_migrations` before applying each file — already-applied versions are skipped safely. Each file must be idempotent.

### Seed data policy

| Data type | Location | When to apply |
|---|---|---|
| Required system records | `schema_current.sql` (Section 3) | Always — applied with schema |
| Built-in middleware defs | `schema_current.sql` | Always |
| LLM provider stubs | `schema_current.sql` | Always (no API key — operators set those) |
| Dev user accounts (admin, avi) | `db/seed_users.sql` | Dev/staging only, `--seed-users` |
| Demo agents + orchestrators | `db/seed_demo.sql` | Optional, `--seed-demo` |
| Test A2A agents | `db/002_seed.sql` | Legacy path; use `seed_demo.sql` instead |

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
#   Replace THE_M_MASTER_SECRET with a strong random value:
#     openssl rand -hex 32
nano secrets.local
./generate-env.sh

# 4. Add ANTHROPIC_API_KEY (not derived — must be set manually)
echo "ANTHROPIC_API_KEY=sk-ant-..." >> .env

# 5. Start the stack (detects fresh DB and bootstraps schema automatically)
./scripts/linux-start.sh --build

# 6. Verify
./scripts/linux-health.sh
```

DB schema is initialized automatically at step 5 via `schema_current.sql`. No separate migration command is required.

---

## Traefik routing — explicit verification

Route ownership on Linux (priorities prevent ambiguity):

| Priority | Router | Rule | Service | Verified |
|---|---|---|---|---|
| 150 | `them-temporal-ui` | `PathPrefix(/temporal)` | Temporal UI (port 8080) | ✓ |
| 120 | `them-go-health` | `PathPrefix(/go-health)` | Go bridge + `replacePathRegex` | ✓ |
| 110 | `them-go-ws` | `PathPrefix(/ws)` | Go bridge (both replicas) | ✓ |
| 110 | `them-go-sse` | `PathPrefix(/sse)` | Go bridge (both replicas) | ✓ |
| 100 | `them-api` | `PathPrefix(/api/v1)` | Python bridge (port 8001) | ✓ |
| 100 | `them-apps` | `PathPrefix(/apps)` | Python bridge | ✓ |
| 100 | `them-a2a` | `PathPrefix(/a2a)` | Python bridge | ✓ |
| 90 | `them-health` | `PathPrefix(/health)` | Python bridge | ✓ |
| 10 | `them-ui` | `PathPrefix(/)` | Frontend (port 3200) | ✓ |

**No `/ws+/sse` combined matcher exists.** The two routes are separate routers at the same priority (110), each matching a distinct prefix. The Python bridge in `docker-compose.linux.yml` has no Traefik labels for `/ws` or `/sse` — those paths are exclusively registered by `docker-compose.traefik.yml` pointing to `them-go-svc`.

Go health rewrite: `^/go-health(.*)` → `/health$1` (via `replacePathRegex` middleware). Verified live: `/go-health/live` → `{"status":"ok"}`, `/go-health/ready` → `{"redis":"ok"}`.

---

## Compose file stacking

### Linux (deployment)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \          # Go-first: named volumes, no WS/SSE on Python bridge
  -f docker-compose.integration.yml \    # Go bridge definition + host-port exposure
  -f docker-compose.soak.yml \           # Second Go bridge replica
  -f docker-compose.traefik.yml \        # WS/SSE/go-health Traefik labels
  --profile temporal up -d
```

### Windows (development)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \          # local.yml instead of linux.yml
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d
```

Only `docker-compose.linux.yml` vs `docker-compose.local.yml` differs.

---

## Pre-deployment validation checklist

### 1 — Compose config
```bash
docker compose \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml --profile temporal \
  config --quiet
echo "Compose config: OK"
```

### 2 — Go unit tests
```bash
cd go
go test ./...
go test -race ./...   # requires gcc (apt-get install gcc on Ubuntu)
```

### 3 — Clean-install validation (full automated test)
```bash
cd theM_gateway
./scripts/linux-validate-clean-install.sh
# Runs 7 phases: infrastructure → schema → partial-init detection →
# full stack → Traefik routing → restart → data integrity
```

### 4 — Go integration tests
```bash
cd go
REDIS_ADDR=localhost:16379 \
  go test -tags=integration -timeout 300s \
  -run "TestMAXLEN|TestIntegration_WS|TestIntegration_Cross" \
  ./internal/runstream/...
```

### 5 — Manual route ownership spot-check
```bash
HOST_IP=$(hostname -I | awk '{print $1}')

# Go owns /ws (expect 401 from Go JWT gate)
curl -sf -o /dev/null -w "%{http_code}\n" \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  "http://${HOST_IP}:8088/ws/orchestrate/app/ep"
# Expected: 401

# Go health via Traefik path-rewrite
curl -sf "http://${HOST_IP}:8088/go-health/live" | grep '"status":"ok"'
curl -sf "http://${HOST_IP}:8088/go-health/ready" | grep '"redis":"ok"'

# Python bridge owns /api/v1
curl -sf -o /dev/null -w "%{http_code}\n" \
  "http://${HOST_IP}:8088/api/v1/admin/agents"
# Expected: 401
```

### 6 — Prometheus metrics
```bash
curl -sf http://localhost:8002/metrics | grep "them_runstream_mode"
curl -sf http://localhost:8003/metrics | grep "them_runstream_mode"
# Expected: them_runstream_mode 1 (dual) or 0 (pubsub)
```

---

## Scripts reference

| Script | Purpose | When to run |
|---|---|---|
| `linux-start.sh` | Start full stack (validates env, inits DB, starts all services) | Every deploy / restart |
| `linux-stop.sh` | Graceful shutdown | Before maintenance or redeploy |
| `linux-db-init.sh` | Bootstrap fresh DB from `schema_current.sql` (no-op if initialized) | Called by `linux-start.sh` automatically |
| `linux-db-upgrade.sh` | Apply specific new migration files to existing deployment | When new `db/NNN_*.sql` files land |
| `linux-validate-clean-install.sh` | 7-phase clean-install automated test | Before every production deploy |
| `scripts/tests/test_db_infra.sh` | DB bootstrap + migration infra unit tests (T1–T6) | After changing linux-db-init.sh or linux-db-upgrade.sh |
| `linux-health.sh` | Verify all containers + HTTP endpoints healthy | After start; in CI |
| `linux-logs.sh` | Collect logs per service | Debugging; incident response |
| `linux-rollback.sh` | Roll back Go bridge to a previous image tag | After a bad Go deploy |
| `linux-db-legacy-replay.sh` | Full sequential migration replay (NOT normal startup) | Recovery/debug only |

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
| DB schema init | Manual `scripts/init_db.sh` | `linux-start.sh` → `linux-db-init.sh` → `schema_current.sql` |
| User seeding | Included in legacy `002_seed.sql` | `db/seed_users.sql` (opt-in, dev only) |
| Demo agents | `002_seed.sql` applied by default | `db/seed_demo.sql` (opt-in) |
| Temporal worker wait | N/A | Temporal CLI task-queue poll — hard failure if not ready |

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

## Schema maintenance workflow

When a new migration is developed:

1. Write `db/026_new_feature.sql` — idempotent, transactional, records itself via `linux-db-upgrade.sh`
2. **Also update `db/schema_current.sql`** — add the new column/table/index to Section 2, and add a row to the `INSERT INTO them.schema_migrations` list in Section 4
3. Test fresh install: `./scripts/linux-validate-clean-install.sh`
4. Test upgrade: `./scripts/linux-db-upgrade.sh db/026_new_feature.sql` against a running stack

Both paths must work independently. A developer on Windows applies `026_new_feature.sql` directly. A fresh Linux deployment applies `schema_current.sql` which already includes it.

---

## Known considerations

| Item | Status |
|---|---|
| Containers run as root (except `them-auth-service`) | Acceptable for private network; add `user:` for hardened envs |
| Race detector requires gcc | `apt-get install -y gcc` on test runner or CI image |
| Traefik dashboard on `0.0.0.0:8089` | Restrict with firewall to trusted IPs |
| `RUN_EVENTS_MODE` defaults to `pubsub` | Change to `dual` for staging validation; `streams` requires Phase 11c-D approval |
| `auth_service/docker-compose.yml` references legacy `omni` DB | Orphaned file — do not use standalone |
| `linux-db-legacy-replay.sh` | For recovery/debug only — NOT part of normal startup |
| Temporal worker readiness check | Uses `temporal task-queue describe` in `temporal-admin-tools` container; hard failure if not ready within 120s |
| Advisory lock on schema ops | Both init and upgrade run all steps (lock + apply + record) in one psql session; lock is session-scoped and released on session exit |
| Migration atomicity | Each upgrade migration is wrapped in BEGIN/COMMIT; schema_migrations INSERT is inside the same transaction; rollback on failure leaves no record |
| Migration skip detection | `linux-db-upgrade.sh` uses `RAISE EXCEPTION 'upgrade-skip:VERSION'` to abort the psql session before migration SQL runs; prior `RAISE NOTICE` approach let the session fall through and execute the SQL again |
| Test migration version names | Must match `^\d{3}[a-z]?(_[a-z0-9_]+)?$` (enforced by `ck_schema_migrations_version` check constraint); use `900_test_*`, `901_test_*`, etc. — not `test_NNN_name` |
| Traefik double-bind | `docker compose` MERGES port arrays across overlay files; never list the same port in both `docker-compose.yml` and `docker-compose.linux.yml` |
| TCP TIME_WAIT | After stack stop on Docker Desktop (Windows), OS holds port reservations 35–120s; `linux-start.sh` retries Traefik once with 35s wait |

---

## Validation results — 2026-07-21

`linux-validate-clean-install.sh`: **27/27 PASSED**

`scripts/tests/test_db_infra.sh`: **17/17 PASSED** (T1–T6 all green)
