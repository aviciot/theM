# Compose Consolidation Execution Report — Stages C–H
**Date:** 2026-08-01
**Executor:** Claude Code (Sonnet)
**Based on:** `docs/architecture-v2/COMPOSE_LAYOUT_CONSOLIDATION_PLAN.md`

---

## Summary

Production Compose deployment successfully migrated from `/home/avi/them/theM_gateway/` to the canonical repository root `/home/avi/them`. All 15 services are running. Named volumes preserved. Both Go Workers are now Compose-managed under `project=them_gateway`.

---

## Preflight Results

| Check | Result |
|---|---|
| Git status | Clean — HEAD `aa09490` |
| Root Compose config (`--quiet`) | No errors; version-obsolete warnings only |
| Services rendered by root Compose | **15** (matches running stack) |
| Named volume `them-postgres-data` | Present |
| Named volume `them-redis-data` | Present |
| Named volume `them-logs` | Present |
| External `proxy-network` | Present (id: `1ada4200e26c`) |
| `them-postgres` health | `Up 2 days (healthy)` |
| `them-redis` health | `Up 2 days (healthy)` |
| `temporal-frontend` health | `Up 2 days (healthy)` |
| `them-traefik` | Running |
| Go Worker 1 (`them-go-worker`) | Polling `them-orchestration-go` |
| Go Worker 2 (`them-go-worker-2`) | Polling `them-orchestration-go` |

Pre-migration Go Worker project labels: `project=them`, `working_dir=` (empty — ad-hoc docker run).

---

## Stage C — Image Validation

| Image | Dockerfile | Build result |
|---|---|---|
| `them_gateway-them-go-bridge` | `Dockerfile.go` | Built OK |
| `them_gateway-them-go-worker` | `Dockerfile.go-worker` | Built OK |
| `them_gateway-them-go-worker-2` | `Dockerfile.go-worker` | Built OK |

No secret values appeared in build output.

---

## Stage D — Rendered Config Comparison

Differences between root and legacy (`diff /tmp/rendered_legacy.yml /tmp/rendered_root.yml`):

| Service | Diff | Impact |
|---|---|---|
| `them-go-bridge` | Gains `go` profile (additive) | None — already under `temporal` |
| `them-go-bridge` | Gains `JWT_PUBLIC_KEY_PEM: ""` | None — empty value |
| `them-go-bridge` | Gains `REDIS_PASSWORD: ""` | None — empty value already set from `.env` |
| `them-go-bridge` | New Traefik label: `them-go-monitoring-config` route | Additive |
| `them-go-bridge` | New LB healthcheck labels for `them-go-bridge-svc` | Additive |

**No destructive configuration changes.** Safe to proceed.

---

## Stage F — Controlled Compose Adoption

### First pass (`--no-recreate`) — blocked by container name conflict

Root Compose attempted to create `them-go-worker` and `them-go-worker-2` but the container names were in use by ad-hoc containers (`project=them`, no working_dir).

**Resolution:** Stopped and removed the 2 ad-hoc Go Worker containers:
```bash
docker stop them-go-worker them-go-worker-2
docker rm them-go-worker them-go-worker-2
```

### Second pass (`--no-recreate`) — successful

Result:
- **`them-go-worker`** — Created (Compose-managed, `working_dir=/home/avi/them`)
- **`them-go-worker-2`** — Created (Compose-managed, `working_dir=/home/avi/them`)
- **`vision-agent`** — Created (was not running before)
- All other 12 services: `Running` — not recreated ✓

---

## Stage E — Controlled Recreation for working_dir Update

Services recreated with `--no-deps --force-recreate` to update `working_dir` label to `/home/avi/them`:

| Service | Recreated? | New working_dir |
|---|---|---|
| `them-traefik` | YES | `/home/avi/them` |
| `them-auth-service` | YES | `/home/avi/them` |
| `them-go-bridge` | YES | `/home/avi/them` |
| `them-go-bridge-2` | YES | `/home/avi/them` |
| `them-bridge` | YES | `/home/avi/them` |
| `them-worker` | YES | `/home/avi/them` |
| `them-frontend` | YES | `/home/avi/them` |
| `temporal-ui` | YES | `/home/avi/them` |
| `temporal-admin-tools` | YES | `/home/avi/them` |
| `them-go-worker` | NEW (Compose adoption) | `/home/avi/them` |
| `them-go-worker-2` | NEW (Compose adoption) | `/home/avi/them` |
| `vision-agent` | NEW (Compose adoption) | `/home/avi/them` |

### Services NOT recreated (live data, label change not worth the risk)

| Service | Reason | Current working_dir |
|---|---|---|
| `them-postgres` | Live DB data in named volume; label change alone insufficient justification | `/home/avi/them/theM_gateway` |
| `them-redis` | Live session/stream data; label change alone insufficient justification | `/home/avi/them/theM_gateway` |
| `temporal-frontend` | Holds Temporal connection state; label change alone insufficient justification | `/home/avi/them/theM_gateway` |

These 3 services will naturally update their label on the next planned maintenance recreate.

---

## Stage G — Validation

### Service health

| Service | Status |
|---|---|
| `them-traefik` | Up |
| `them-postgres` | Up 2 days (healthy) |
| `them-redis` | Up 2 days (healthy) |
| `them-auth-service` | Up (healthy) |
| `them-bridge` | Up (healthy) |
| `them-frontend` | Up (healthy) |
| `them-worker` | Up |
| `them-go-bridge` | Up (healthy) |
| `them-go-bridge-2` | Up (healthy) |
| `them-go-worker` | Up — polling `them-orchestration-go` |
| `them-go-worker-2` | Up — polling `them-orchestration-go` |
| `temporal-frontend` | Up 2 days (healthy) |
| `temporal-ui` | Up |
| `temporal-admin-tools` | Up |
| `vision-agent` | Up |

**Total: 15 services running**

### Data preservation

| Volume | Check | Result |
|---|---|---|
| `them-postgres-data` | `SELECT count(*) FROM them.llm_providers` = 2 | ✓ Preserved |
| `them-postgres-data` | Auth service schema intact | ✓ Preserved |
| `them-redis-data` | `db0:keys=16` in Redis info keyspace | ✓ Preserved |

### Routing validation

| Check | Result |
|---|---|
| Traefik health `GET /health/live` (via Traefik) | 200 OK — served by Go bridge |
| Auth `GET /api/v1/admin/llm-providers` (via Traefik) | 200 OK |
| Auth `GET /api/v1/admin/agents` (via Traefik) | 200 OK |
| `proxy-network` attached to `them-traefik` | ✓ (both `proxy-network` and `them-network`) |

### Python Worker isolation

Python Worker polls `them-orchestration` (not `them-orchestration-go`). Confirmed from logs.

### Test suite

```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
Total: 55 passed, 0 failed
```

### Workflow distribution — 6 consolidation test runs

| Worker | Runs processed |
|---|---|
| `them-go-worker` (go-worker-1) | 3 (consolidation-test-c34e4444, 2c59f3f8, 6d0a35b8) |
| `them-go-worker-2` (go-worker-2) | 3 (consolidation-test-7a577071, debcf42c, c36cbb07) |

Both workers processed at least 1 run. Distribution confirmed. (Runs failed at Anthropic LLM layer — expected for test inputs without valid orchestrator configs; the important validation is worker pickup and activity execution.)

### Redis Streams

Events confirmed written to `them:dash:run:<run_id>:stream` keys by Go Workers. Example: `them:dash:run:fcb306ab-a666-481d-bb44-735f8f6f2801:stream` contains 1 event with `type=error` and `run_id`.

### Inline Bridge orchestration

Python bridge logs: no `OrchestrationWorkflow` or `them-orchestration-go` references in last 2 minutes. No inline orchestration occurred.

---

## Stage H — Rollback

**Rollback not used.** All validations passed.

Rollback procedure (preserved for reference):
```bash
# Do NOT use --volumes
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml -f docker-compose.cloudflare.yml \
  --profile temporal down

cd /home/avi/them/theM_gateway
docker compose \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml -f docker-compose.hetzner-build.yml \
  -f docker-compose.cloudflare.yml --profile temporal up -d
```

---

## Stage 9 — Cleanup

- `theM_gateway/RETIRED.md` created — explains retired status, preserves rollback knowledge
- `theM_gateway/` directory retained (not deleted) — contains `secrets.local` backup, `docker-compose.hetzner-build.yml` (not needed at root), and source symlinks
- No unsafe ad-hoc restart scripts found to remove

---

## Deployment-Origin Verification

| Container | `project` | `working_dir` |
|---|---|---|
| `them-traefik` | `them_gateway` | `/home/avi/them` ✓ |
| `them-postgres` | `them_gateway` | `/home/avi/them/theM_gateway` (not recreated — data risk) |
| `them-redis` | `them_gateway` | `/home/avi/them/theM_gateway` (not recreated — data risk) |
| `them-auth-service` | `them_gateway` | `/home/avi/them` ✓ |
| `them-bridge` | `them_gateway` | `/home/avi/them` ✓ |
| `them-frontend` | `them_gateway` | `/home/avi/them` ✓ |
| `them-worker` | `them_gateway` | `/home/avi/them` ✓ |
| `them-go-bridge` | `them_gateway` | `/home/avi/them` ✓ |
| `them-go-bridge-2` | `them_gateway` | `/home/avi/them` ✓ |
| `them-go-worker` | `them_gateway` | `/home/avi/them` ✓ |
| `them-go-worker-2` | `them_gateway` | `/home/avi/them` ✓ |
| `temporal-frontend` | `them_gateway` | `/home/avi/them/theM_gateway` (not recreated — Temporal state) |
| `temporal-ui` | `them_gateway` | `/home/avi/them` ✓ |
| `temporal-admin-tools` | `them_gateway` | `/home/avi/them` ✓ |

**11/14 containers:** `working_dir=/home/avi/them`
**3/14 containers:** `working_dir=/home/avi/them/theM_gateway` — acceptable, pending natural maintenance recreate.

Active Compose files (from `them-go-worker` label):
```
/home/avi/them/docker-compose.yml
/home/avi/them/docker-compose.linux.yml
/home/avi/them/docker-compose.integration.yml
/home/avi/them/docker-compose.soak.yml
/home/avi/them/docker-compose.traefik.yml
/home/avi/them/docker-compose.cloudflare.yml
```

---

## Remaining Risks

1. **3 infrastructure containers** (`them-postgres`, `them-redis`, `temporal-frontend`) still show `working_dir=theM_gateway`. Will update naturally on next planned maintenance recreate. Not a functional risk.
2. **`theM_gateway/` directory** still exists and contains active `secrets.local` / `.env`. If someone runs `docker compose up` from that directory accidentally, it would conflict with the root-managed stack. Mitigated by `RETIRED.md`.
3. **Vision agent** (`vision-agent`) was not running before consolidation — now Compose-managed. First time under root project. Monitor for unexpected restarts.

---

## Production Compose Command (canonical)

```bash
cd /home/avi/them
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal up -d
```
