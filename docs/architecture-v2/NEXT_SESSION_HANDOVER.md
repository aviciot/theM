# Session Handover
# Generated: 2026-07-25
# Scope: Wave 6 implementation — monitoring-config + llm-providers/routing/config

---

## Git State

**Branch:** `main`
**HEAD:** see `git log --oneline -1` (this file committed with all Wave 6 changes)
**origin/main:** push pending — no credentials available (local SSH key not configured)
**Working tree:** clean after this commit

### Session commits (newest first)

```
(this commit)  docs(wave6): TEST_INDEX.md, implementation-status.md, lessons-learned.md, WAVE6_IMPLEMENTATION_REPORT.md
55eb923        cutover(wave6): enable Traefik routing for monitoring-config + llm-providers/routing/config
b0d1a31        feat(admin): add MonitoringConfig + LLMRouting handlers (Wave 6 Phase 3)
e78a6bd        feat(admin/service): add ConfigService for monitoring + llm_routing (Wave 6 Phase 2)
69e2dca        feat(admin/dal): add config table GetConfig + UpsertConfig (Wave 6 Phase 1)
64729fd        docs(handover): session handover — CLAUDE.md alignment + docs consolidation complete
```

---

## Work Completed This Session

### Wave 6 — monitoring-config + llm-providers/routing/config

4 Python admin operations migrated to Go:
- `GET /api/v1/admin/monitoring-config` — reads `them.config['monitoring']`, returns defaults if absent
- `PUT /api/v1/admin/monitoring-config` — validates threshold ordering (heatmap, edge), upserts JSONB
- `GET /api/v1/admin/llm-providers/routing/config` — reads `them.config['llm_routing']`, returns defaults if absent
- `PUT /api/v1/admin/llm-providers/routing/config` — upserts JSONB, no Fernet

Architecture follows Handler → Service → DAL pattern. No SQL in handlers, no business logic in handlers.

### Bug fixed
Pre-existing JWT env var naming bug in `docker-compose.yml`: Go bridge was verifying HS256 tokens with `SECRET_KEY` (wrong), not `JWT_SECRET` (auth service signing key). Fixed by adding `JWT_SECRET=${THE_M_JWT_SECRET:-}` to Go bridge env block. Documented in `lessons-learned.md` L-10.

---

## Deployed/Live State

| Container | State |
|---|---|
| `them-traefik` | Running — port 8088 (host) |
| `them-postgres` | Healthy |
| `them-redis` | Healthy |
| `them-auth-service` | Healthy — port 8701 (internal) |
| `them-bridge` (Python) | Healthy — port 8001 (internal) |
| `them-go-bridge` | Healthy — port 8002 (internal) |
| `them-frontend` | Running — port 3200 (internal) |
| Temporal worker | Running — profile `temporal` |

**Note:** Go bridge is running with correct JWT_SECRET. If the container is restarted with `docker compose` without `THE_M_JWT_SECRET` set in the shell environment, it will revert to `SECRET_KEY` for auth. The `.env` file is missing from this deployment — credentials must be passed as env vars or the `.env` must be regenerated with `./generate-env.sh` (requires `secrets.local`).

---

## Tests Executed

### Go unit tests
All tests pass (Docker builder stage runs `go test ./...` as part of build). Build succeeded at each phase.

New tests added: 17 (10 service, 7 handler)

### Python test suite
```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 20
87 passed, 1 skipped (bridge-2 replica not running — expected)
```

### Contract tests (live)
All 4 operations match Python exactly. PUT writes verified to persist across Python read.

---

## Architecture Decisions Made

1. **`unprocessable` (422) for threshold ordering violations** — Python uses pydantic `model_validator` which FastAPI maps to 422. Go uses `unprocessable()` → `ErrUnprocessable` → `writeServiceError` → 422. Consistent.
2. **Defaults merge via `json.Unmarshal` over pre-filled struct** — Idiomatic Go equivalent of Python's `merged.update(row.config_value)`. Absent JSON keys leave struct fields at defaults.
3. **No cache invalidation for config PUT** — Python does none (direct SQLALCHEMY commit). Go does none. Not needed: the config JSONB is read fresh on every request.
4. **`Path()` Traefik rule for exact-match** — `Path("/api/v1/admin/llm-providers/routing/config")` not `PathPrefix()`. Prevents any overlap with future `/llm-providers/{id}` if that were ever claimed at lower priority.

---

## Temporary Compatibility Code

None. Wave 6 adds no shims or backward-compat bridges — the Python fallback is always available (Python bridge still running).

---

## Known Bugs and Blockers

| Item | Details |
|---|---|
| Missing `.env` file | `secrets.local` not present. Go bridge must be started with explicit env vars: `THE_M_SECRET_KEY=... THE_M_DB_PASSWORD=... THE_M_JWT_SECRET=...`. Python bridge started before this session and is still running with correct env. |
| `go-bridge-2` not running | The second Go bridge replica was removed when debugging the JWT issue. Restart with the same env vars if needed. |
| Push pending | `git push origin main` — no credentials available in this session. |

All other blockers from Wave 5 handover remain unchanged.

---

## Files Most Relevant to the Next Task (Wave 7)

| File | Purpose |
|---|---|
| `app/routers/admin_llm_providers.py` | Python source for LLM provider CRUD + Fernet usage |
| `app/utils/crypto.py` | `encrypt_value` / `decrypt_value` — Fernet AES-128-CBC implementation |
| `go/internal/admin/service/config.go` | Reference for service pattern used in Wave 6 |
| `go/internal/admin/dal/config.go` | Reference for config DAL used in Wave 6 |
| `docs/architecture-v2/WAVE6_PLAN.md` | Wave 7 deferred scope (LLM provider CRUD + Fernet) documented |
| `db/001_schema.sql` | `them.llm_providers` table definition |

---

## Hard Constraints That Must Remain in Force

- No SQL in handlers, no business logic in handlers (Handler → Service → DAL)
- Python bridge must keep running — it is the rollback path for all Go routes
- Never query `auth_service.*` tables directly — use `internal/auth/` from Go
- Never use DB name/schema `odin` — always `them`
- Secrets never appear in log output — use `cfg.SafeString()`
- All list endpoints return `[]` not `null`
- RequireSuperAdmin middleware on all admin routes
- Integration tests required before marking any schema-dependent code complete

---

## Exact Next Task

**Plan Wave 7** — LLM provider CRUD + Fernet key handling.

Wave 7 is a larger wave than Wave 6 because:
1. It involves Fernet encryption — `encrypt_value`/`decrypt_value` must be re-implemented in Go (AES-128-CBC + HMAC-SHA256, compatible byte format)
2. It has 4 CRUD operations (list, get, create, update, delete) + the routing/config ops already done
3. The `api_key_masked` field requires decryption just to mask — Go must be able to decrypt Python-encrypted keys

Use **Opus** for Wave 7 planning to properly scope the Fernet port.

---

## First Prompt for Next Session

```
Read first (in this order):
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/implementation-status.md

Verify:
- branch is main
- HEAD is 55eb923 or newer
- working tree is clean

The task is to plan Wave 7 (not implement). Use Opus.

Read:
  app/routers/admin_llm_providers.py     (full file — CRUD + Fernet usage)
  app/utils/crypto.py                    (encrypt_value / decrypt_value)
  db/001_schema.sql                      (them.llm_providers table)

Determine:
1. Can Fernet decryption be ported to Go without a new dependency?
   (Fernet = AES-128-CBC + HMAC-SHA256 — stdlib only)
2. What is the minimal Go type for LLMProvider that matches Python's LLMProviderOut?
3. Should api_key_masked require real decryption or can it be handled a different way?

Write the plan to: docs/architecture-v2/WAVE7_PLAN.md
Return only the plan file path and a one-paragraph summary. Do not write any Go code.
```
