# Next Session Handover — Wave 7 Phase 3 Complete

**Prepared:** 2026-07-26  
**Status:** Wave 7 Phase 3 (LLM provider CRUD cutover) complete and live.

---

## Current Objective

Wave 7 is done. The next task is Wave 8 — migrate the remaining Python-owned admin routes
to Go (see "Exact Next Task" below for scope).

---

## Branch and HEAD

```
branch: main
HEAD:   run `git log --oneline -1` to confirm after commit
```

---

## Commits Created This Session

1. `dd78cec` — feat(admin): Wave 7 Phase 3a — LLM provider CRUD handlers + MF-1 fix
2. `(pending)` — feat(infra): Wave 7 Phase 3b — Traefik cutover for LLM provider CRUD

---

## Push Status

Commits are local. HTTPS credentials were not available during this session — push with:
```bash
git push origin main
```

---

## Work Completed This Session

### Phase 3a (previous session segment)
- **MF-1 fix**: `ErrConflict` → HTTP 409 with static `"resource already exists"` message in `writeServiceError`
- **LLMProvidersHandler** (`go/internal/admin/llm_providers.go`): List, Create, Get, Update (PATCH with presence detection), Delete
- **BuildRouter** updated with `secretKey` parameter; **main.go** passes `cfg.SecretKey`
- **18 handler tests** in `admin_test.go` (LP-1 through LP-16, AZ)
- All 23 Go packages pass `go test ./...`

### Phase 3b (this session segment)
- **Root cause found**: Traefik constraint `Label('traefik-instance','them')` — Go bridge was missing this label
- **Fix 1**: Added `- "traefik-instance=them"` to `them-go-bridge` labels in `docker-compose.yml`
- **Fix 2**: Added Wave 6 and Wave 7 router blocks to `theM_gateway/docker-compose.traefik.yml`
- **Fix 3**: Added `THE_M_JWT_SECRET` to `/opt/docker/them/.env` so Go bridge validates auth-service tokens
- **Live verification**: All 7 operations confirmed routed to Go — POST 201, GET 200, PATCH 200, duplicate POST 409, DELETE 204, GET-after-delete 404, routing/config 200
- **Python sanity suite**: 55 passed, 0 failed
- **Go test suite**: 23 packages, all pass

---

## Deployed / Live State

| Container | Status |
|---|---|
| `them-traefik` | Up — routing Wave 7 LLM provider CRUD to Go |
| `them-go-bridge` | Up — serving /api/v1/admin/llm-providers/* (excl. /routing/config) |
| `them-bridge` | Up — Python still handles all non-migrated routes |
| `them-postgres` | Up (healthy) |
| `them-redis` | Up (healthy) |
| `them-auth-service` | Up (healthy) |

Traefik active Go routers (5):
- `them-go-admin-reads@docker` — priority 110
- `them-go-health-sub@docker` — priority 130
- `them-go-llm-providers@docker` — priority 120 (Wave 7, new)
- `them-go-llm-routing-config@docker` — priority 120 (Wave 6)
- `them-go-monitoring-config@docker` — priority 120 (Wave 6)

---

## Tests Executed

| Suite | Result |
|---|---|
| Go `go test ./...` (23 packages) | All pass |
| Python sanity 01 02 03 04 15 | 55 passed, 0 failed |
| Live smoke (LLM provider CRUD via Traefik) | All 7 scenarios pass |

---

## Architecture Decisions Made

1. **`traefik-instance=them` label is mandatory** for Go bridge Traefik discovery — added to `docker-compose.yml`.
2. **Dual-file Traefik label maintenance**: every wave must update both `docker-compose.yml` (dev) and `theM_gateway/docker-compose.traefik.yml` (production).
3. **`THE_M_JWT_SECRET` in `.env`** is required for Go bridge to validate auth-service tokens (L-10/L-11 in lessons-learned.md).

---

## Temporary Compatibility Code Still in Place

- Python still serves as fallback for all non-Go-owned routes.
- Wave 7 router has `!Path(...)` exclusion for `/routing/config` — both belt and suspenders since routing/config router at same priority would win by specificity, but the exclusion makes the intent explicit.
- `.env` file at `/opt/docker/them/.env` is not committed (correct — secrets never committed).

---

## Known Bugs and Blockers

None blocking.

---

## Hard Constraints That Must Remain in Force

- No plaintext API key in any log, response, or error — `crypto.Decrypt` output zeroed after use
- `api_key_encrypted` never in JSON output structs
- 500 responses use static strings — never `err.Error()` from service/DAL layers
- Handler must NOT log the request body (contains plaintext api_key)
- `THE_M_SECRET_KEY` must never be logged; `SafeString()` must omit it
- `ErrConflict` → 409 with static message (MF-1 — already fixed, must not regress)

---

## Files Most Relevant to the Next Task

For Wave 8 scoping:
- `docs/architecture-v2/implementation-status.md` — current route ownership table
- `go/internal/admin/router.go` — where to register new handlers
- `theM_gateway/docker-compose.traefik.yml` — production Traefik labels (must keep in sync with docker-compose.yml)
- `docker-compose.yml` lines 903-945 — Go bridge Traefik labels (dev/go profile)

---

## Exact Next Single Focused Task

**Wave 8 scoping** — Identify which Python admin routes remain unowned by Go and pick the next single subsystem. Good candidates:

1. Agent test/discover/security-scan — Python side-effect routes, may stay in Python
2. Application import/export/restore/bulk-delete/runtime — complex, higher priority
3. Dashboard WS `/ws/dashboard` — Python-specific broadcast channel

Read `docs/architecture-v2/implementation-status.md` Route Map for the full list.
Use Opus for scoping, Sonnet for implementation.

---

## Exact Commands for Starting the Next Session

```bash
# In /opt/docker/them/
git fetch origin
git log --oneline -3

# Verify stack is healthy
python3.12 scripts/tests/run_tests.py 01 02 03 04 15

# Verify Go bridge is serving LLM provider routes (expect 5 Go routers)
docker exec them-traefik wget -qO- http://localhost:8089/api/rawdata 2>/dev/null | python3 -c "
import json,sys
data=json.load(sys.stdin)
routers=data.get('routers',{})
go_routers={k:v for k,v in routers.items() if 'go' in k.lower()}
print(f'Go routers: {len(go_routers)}')
for k in sorted(go_routers.keys()): print(f'  {k}')
"
```

**First prompt for next session:**
> We're on main at HEAD (Wave 7 complete). LLM provider CRUD is now served by Go through Traefik.
> Read docs/architecture-v2/implementation-status.md and identify the next single subsystem to migrate in Wave 8.
> Use Opus for planning, Sonnet for implementation.

---

## Rollback Instructions for Wave 7

1. Remove the `them-go-llm-providers` router block from `theM_gateway/docker-compose.traefik.yml`
2. Remove the same block from `docker-compose.yml` (Go bridge service labels)
3. Recreate Go bridge: `cd /opt/docker/them && docker compose --profile go up --no-deps -d them-go-bridge`
4. Verify Traefik no longer shows `them-go-llm-providers@docker` in rawdata
5. Python continues to serve at priority 100 as before
