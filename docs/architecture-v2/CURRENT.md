# Current Session State — the-M
# Last updated: 2026-08-15
# Replaces: NEXT_SESSION_BRIDGE_HANDOVER.md, NEXT_SESSION_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `a6b9953` — Runs WRITE slice complete (2026-08-15)

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

**Runs WRITE — COMPLETE** (2026-08-15)

What was done:
- New DAL methods: `CancelRun` (UPDATE...RETURNING), `DeleteRun` (DELETE...RETURNING), `BulkDeleteRuns` (DELETE...RETURNING with IN list)
- New service methods: `Cancel` (404/409 distinction via fallback GetRun), `Delete`, `BulkDelete` (max 500 IDs enforced)
- New handlers: `PATCH /runs/{run_id}/cancel`, `DELETE /runs/{run_id}`, `POST /runs/bulk-delete`
- `POST /runs/bulk-delete` registered as static route before `/{run_id}` to prevent wildcard shadowing
- Traefik Wave 2f: 3 new routers — `them-go-runs-cancel` (PATCH, priority 116), `them-go-runs-delete` (DELETE, priority 114), `them-go-runs-bulk-delete` (POST, priority 116)
- 6 new handler tests (RW-1 through RW-6) in `go/internal/admin/runs_test.go`
- `isolationFakeDal` and `fakeDal` in service tests updated to satisfy Dal interface
- All 30 Go packages pass `go test ./...`

**Runs READ/UI — COMPLETE** (cf953cf, 2026-08-15)

What was done:
- Auth fixed: runs routes moved from `BearerTenantMiddleware` to `JWT + RequireSuperAdmin + AdminTenantMiddleware` — session JWTs from auth-go now work
- New handlers: `GET /runs/stats`, `GET /runs/{id}` (now returns RunDetail with steps/usage/children), `GET /runs/{id}/tasks`, `GET /runs/{id}/artifacts`
- New DAL types: `RunStep`, `RunUsage`, `RunDetail`, `Task`, `ArtifactPart`, `Artifact`, `RunStats`
- New DAL methods: `GetRunStats`, `GetRunDetail`, `GetRunTasks`, `GetRunArtifacts`
- Static route `/runs/stats` registered before `/{run_id}` to prevent chi wildcard shadowing
- Traefik Wave 2e added: `them-go-runs-sub` rule captures `/{id}/tasks` and `/{id}/artifacts`
- `THE_M_API_URL` in dev overlay changed to `http://them-traefik:8088` — Next.js proxy now routes through Go
- 8 new tests (RS-1/2, RD-1, RT-1/2, RA-1/2, RO-1) in `go/internal/admin/runs_test.go`
- Python-OFF verified: all 5 GET endpoints return 200 with `them-bridge` stopped

**Agents Store — COMPLETE** (888861b)

- `POST /agents/discover` → Go
- `POST /agents/{id}/test` → Go
- `POST /agents/{id}/security-scan` → Go
- `AdminTenantMiddleware` for UI admin routes
- Go auth service cutover; Python auth container removed

---

## Next recommended task

**Applications export/import/restore + middleware-wirings** — still on Python.

Routes not yet migrated to Go:
- `GET /runs/context/{ctx}/artifacts` — Python (not used by admin UI; low priority)
- Applications export/import/restore routes
- Middleware-wiring admin routes

After runs writes complete: the Applications page still partially relies on Python. Consider as next slice.

Full route inventory: `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`

Full route inventory: `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`

---

## Python-OFF baseline (2026-08-15, verified with cf953cf)

**Confirmed working with Python OFF, Go active:**
- All admin routes (Waves 1-8): agents CRUD+discover+test+security-scan, orchestrators, applications, tokens, sessions, LLM providers, monitoring-config ✓
- Runs READ: `GET /runs`, `/runs/stats`, `/runs/{id}`, `/runs/{id}/tasks`, `/runs/{id}/artifacts` → all 200 ✓
- Auth (login, me, refresh) → auth-go 200 ✓
- `/health/live`, `/health/ready` → Go 200 ✓

**Still broken with Python OFF:**
- Runs writes: cancel, delete, bulk-delete → requires deploying updated Go image (`--profile go` stack rebuild)
- `GET /runs/context/{ctx}/artifacts` → 404 (no Traefik rule, no Go handler; not used by admin UI)
- `GET /apps`, `GET /apps/{slug}` → 404 (Traefik only captures WS/SSE paths for apps)
- `GET /health` (bare) → 404 (no Traefik router)
- Applications export/import/restore/middleware-wirings → Python only

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
