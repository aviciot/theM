# Current Session State — the-M
# Last updated: 2026-08-15
# Replaces: NEXT_SESSION_BRIDGE_HANDOVER.md, NEXT_SESSION_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `ca29acd` — aligned to origin/main (local dev server, 2026-08-15)

---

## Deployment state

**Active deployment: local Linux server** (moved from Hetzner 2026-08-15)

Stack: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`
UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is sole auth service — Python `them-auth-service` removed from compose
- `them-bridge` (Python) handles all non-auth API routes in default dev mode
- `them-go-bridge` is NOT started in default dev mode (requires `--profile go`)
- Without `--profile go`, Traefik has no routers for `/api/v1/`, `/health/`, `/ws/`, `/sse/`, `/apps/`
- To match Hetzner prod routing: add `--profile go` to startup command
- `docker-compose.dev.yml` is the local Linux overlay (replaces old `docker-compose.local.yml`)
- Named Docker volumes: `them-postgres-data`, `them-redis-data`, `them-logs` — `external: true`
- Project name: `them_gateway` — required for volume/network ownership consistency

All containers healthy. See `docs/STATUS.md` for full container list.

---

## Environment alignment done this session

- `docker-compose.dev.yml` fixed: `THE_M_AUTH_URL` → `them-auth-go:8703`, Dockerfile names, named volumes, external network
- `.dockerignore` updated: added `theM_gateway/` to prevent build-context permission errors
- `docs/STATUS.md` updated: HEAD, startup command, container map
- `docs/architecture-v2/LOCAL_DEV_PYTHON_OFF_AUDIT.md` created: Phase 10/11 route audit
- Local repo aligned to `origin/main` at `ca29acd` (saved local R-4 work to `local-r4-backup` branch)

---

## Current migration slice

**Agents Store — COMPLETE** (888861b and prior commits this cycle)

What was done:
- `POST /agents/discover` → Go (classify via Anthropic, fetch card)
- `POST /agents/{id}/test` → Go (connectivity check, latency_ms)
- `POST /agents/{id}/security-scan` → Go (async scan job, 202 response)
- `AdminTenantMiddleware` replacing `BearerTenantMiddleware` for UI admin routes
- Go auth service cutover complete; Python auth container removed

---

## Next recommended task

**Fix runs auth mismatch** — this is blocking the entire Runs section of the admin UI.

Root cause: Go's runs routes (`GET /api/v1/runs`, `GET /api/v1/runs/{id}`) use `BearerTenantMiddleware`, which requires an opaque bearer token from `access_tokens`. The admin UI sends a session JWT (from `them-auth-go`). Go returns 401. Python used `require_jwt` (session JWT) for the same routes.

Additionally: `GET /api/v1/runs/stats` and `GET /api/v1/runs/contexts` are captured by the Go `admin-reads` Traefik rule but have no Go handler → 404 even with Python running.

**Two changes needed in one session:**
1. Add JWT super_admin auth path to Go runs list (`GET /runs`) and detail (`GET /runs/{id}`) — same auth pattern as admin routes (jwtMiddleware + RequireSuperAdmin, no BearerTenantMiddleware for super_admin callers)
2. Add Go handlers for `GET /runs/stats` and `GET /runs/contexts` — or narrow the Traefik regex to `PathRegexp(^/api/v1/runs/[0-9a-f-]{36}$)` to exclude static sub-paths

After that: port `GET /runs/{id}/tasks`, `GET /runs/{id}/artifacts`, `GET /runs/context/{ctx}/artifacts` to Go.
Then runs writes (cancel, delete, bulk-delete).

Full route inventory: `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`

---

## Real Python-OFF baseline (2026-08-15 with Go profile active)

**Confirmed working with Python OFF, Go active:**
- All of Wave 1-7 admin routes: agents, orchestrators, applications, tokens, sessions, LLM providers, monitoring-config ✓
- Agent CRUD, discover, test ✓ (discover takes `endpoint_url` not `url`)
- Security-scan returns 503 if `security_scanner` agent slug not registered (expected without `--profile security`)
- `/health/live`, `/health/ready` → Go 200 ✓
- Auth (login, me, refresh) → auth-go 200 ✓

**Broken with Python OFF, Go active:**
- `GET /api/v1/runs`, `GET /api/v1/runs/{id}` → Go 401 (BearerTenantMiddleware rejects session JWT)
- `GET /api/v1/runs/stats`, `GET /api/v1/runs/contexts` → Go 404 (captured by Traefik rule, no handler)
- `GET /api/v1/runs/{id}/tasks`, `GET /api/v1/runs/{id}/artifacts` → 404 (Python stopped, no Go handler)
- `GET /apps`, `GET /apps/{slug}`, `POST /apps/{slug}` → 404 (no Traefik router for non-WS/SSE apps routes; falls to frontend)
- `GET /health` (bare) → 404 (no Traefik router; falls to frontend)

**Important discovery:** Go binary in Docker was built 2026-07-28 — predated Wave 8 agent action handlers. Rebuilt on 2026-08-15. Always rebuild `them-go-bridge` at session start if Go source was changed since last container start.

---

## Known baseline issue

`GET /api/v1/runs` returns 401 with Go active (JWT auth incompatibility).
Pre-existing data issue: one run row has NULL fields — causes Python validation error when Python serves runs.

---

## Known blockers

1. Auth admin CRUD (users/roles/teams) — not exposed since Python auth removed. Needs Go port.
2. Python Temporal worker is still primary orchestration path. Go worker is parallel but not sole owner.
3. A2A server (`/a2a/*`) still on Python — not yet migrated to Go.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `app/services/auth_client.py`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Completed plans/reports → `docs/architecture-v2/archive/`.
3. Update this file (CURRENT.md) at session end — do NOT create new NEXT_SESSION_*.md files.
4. ADRs are permanent — never archive them.
5. STATUS.md describes now, not history.
6. ARCHITECTURE.md describes current design, not migration chronology.
7. REMAINING_ROUTE_OWNERSHIP_INVENTORY.md is temporary — remove when Python is gone.
8. Documentation changes ship in same commit as the code changes they describe.
9. Never create another competing active architecture directory.
10. Code is final truth; stale canonical docs are a bug.
