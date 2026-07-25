# Wave 5 Cutover — Handover Document
# Generated: 2026-07-24

---

## Final HEAD

```
4728ef8 cutover(wave5): enable Traefik routing for /api/v1/admin/tokens + /admin/sessions
```

Branch: `main`  
Ahead of `origin/main` by **9 commits** (push pending — requires user action).

---

## Wave 5 Commits (this session)

```
7faec8b session: add ListEPSessions, ListAppSessions + SignalDisconnect delivered count
ac9e583 admin/dal: add access_tokens DAL + IsUniqueViolation + Token types
0e4748b admin/service: TokenService + SessionAdminService + Dal interface extensions
bf6ac0e feat(wave5): tokens + sessions HTTP handlers, router wiring, 34 handler tests
8caa86e test(wave5): integration tests for tokens + sessions admin API (11 tests)
27cb8bf docs+test(wave5): Python↔Go contract tests + 3 lessons-learned entries
7a8e43b fix(wave5): correct SQL cast operator precedence and ExecLua return type
44e3d00 test(wave5): add JWT auth to contract tests; align Python↔Go behavioral diffs
4728ef8 cutover(wave5): enable Traefik routing for /api/v1/admin/tokens + /admin/sessions
```

---

## Push Status

**NOT PUSHED** — push was blocked by auto-mode classifier during SSH push attempt.

**To push:**
```bash
git push origin main
```

If HTTPS credentials aren't cached, use SSH:
```bash
git push git@github.com:aviciot/theM.git main
```

---

## Live Route Ownership (post Wave 5 cutover)

| Route | Owner | Traefik Priority |
|---|---|---|
| `GET /health/live`, `GET /health/ready` | Go | 130 |
| `GET /api/v1/admin/agents*` | Go | 110 |
| `GET /api/v1/admin/orchestrators*` | Go | 110 |
| `GET /api/v1/admin/applications*` | Go | 110 |
| `GET /api/v1/runs*` | Go | 110 |
| `POST /api/v1/admin/agents` | Go | 115 |
| `PUT/PATCH/DELETE /api/v1/admin/agents/{id}` | Go | 115 |
| `POST /api/v1/admin/orchestrators` | Go | 115 |
| `PUT/PATCH/DELETE /api/v1/admin/orchestrators/{name}` | Go | 115 |
| `POST /api/v1/admin/applications` | Go | 115 |
| `PUT/PATCH/DELETE /api/v1/admin/applications/{id}` | Go | 115 |
| `POST/PUT/PATCH/DELETE /api/v1/admin/applications/{id}/entry-points{/ep_id}` | Go | 115 |
| `POST /api/v1/runs/{run_id}/signal` | Go | 115 |
| `GET /apps/{slug}/ws` | Go | 120 |
| `GET,POST /apps/{slug}/sse` | Go | 120 |
| `GET /ws/orchestrate/{app}/{ep}` | Go | 120 |
| `GET,POST /sse/orchestrate/{app}/{ep}` | Go | 120 |
| **`ALL /api/v1/admin/tokens*`** | **Go (Wave 5)** | **120** |
| **`ALL /api/v1/admin/sessions*`** | **Go (Wave 5)** | **120** |
| All other `/api/v1/*` routes | Python | 100 |

**Python still owns:**
- `/ws/orchestrate/{name}` (one-segment, playground/legacy)
- `/api/v1/auth/*` (auth service proxy)
- `/api/v1/admin/agents/{id}/test`, `/discover`, `/security-scan`
- `/api/v1/admin/orchestrators/{name}/test-llm`
- `/api/v1/admin/applications/{id}/import`, `/export`, `/restore`, `/bulk-delete`, `/runtime`, etc.
- `/api/v1/admin/monitoring-config`
- `/api/v1/admin/providers`
- All WS one-segment orchestrate paths
- Dashboard WS `/ws/dashboard`

---

## Rollback Method

Remove the two Wave 5 router blocks from `theM_gateway/docker-compose.traefik.yml`:
```
them-go-tokens.*
them-go-sessions.*
```

Then restart Go bridges with the traefik overlay:
```bash
cd theM_gateway
docker compose \
  -f docker-compose.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d them-go-bridge them-go-bridge-2
```

---

## Tests Executed and Totals

### Python test suite
```
929 passed, 6 skipped, 0 failed
```

### Go Wave 5 contract tests
```
40 passed, 0 failed, 0 skipped
```
Run command (from inside them-bridge):
```bash
docker cp scripts/tests/test_go_wave5_contracts.py them-bridge:/tmp/
docker exec them-bridge env \
  PYTHON_BRIDGE=http://localhost:8001 \
  GO_BRIDGE=http://them-go-bridge:8002 \
  AUTH_SERVICE=http://them-auth-service:8701 \
  python3 /tmp/test_go_wave5_contracts.py
```

### Wave 5 smoke test (manual, all passed)
- Bridge 1 tokens: POST/GET/PATCH/DELETE/LIST/auth/404 — all pass
- Bridge 1 sessions: no-params 400, both-params 400, app_id 200, ep_slug 200, disconnect 404 — all pass
- Bridge 2 tokens: POST/DELETE — pass
- Bridge 2 sessions: list — pass
- Via Traefik: POST/GET/PATCH/DELETE tokens, GET sessions (app_id and ep_slug) — all pass

---

## Bugs Fixed in This Session

1. **`tokenSelectCols` SQL cast operator precedence** — `go/internal/admin/dal/tokens.go`
   - `AT TIME ZONE 'UTC' AT TIME ZONE 'UTC'::text` applied `::text` to the string `'UTC'` not the expression
   - Fixed to `(col AT TIME ZONE 'UTC')::text`
   - Also added no-timezone-suffix formats to `parseTS` for AT TIME ZONE output

2. **`ExecLua` in session_adapter.go** — `go/internal/cache/session_adapter.go`
   - Always called `res.AsInt64()` which fails for Lua scripts returning arrays
   - Fixed to `res.ToAny()` matching the gate_adapter pattern
   - Root cause: `luaPruneAndList` returns an array, `luaPruneAndCount` returns an int

3. **Traefik labels not applied** — stack was running without `-f docker-compose.traefik.yml`
   - Fixed by restarting Go bridges with the full compose command including traefik overlay

---

## Architecture Decisions Made

- `ExecLua` must use `res.ToAny()` for any Lua script that may return non-integer results
- PG `AT TIME ZONE 'UTC'` on `timestamptz` returns `timestamp` (no tz suffix in text form) — always needs explicit UTC parsing in `parseTS`
- Contract tests must acquire a JWT from auth service; both Python and Go require admin auth

---

## Working Tree State

```
Untracked (not committed):
  docs/architecture-v2/GO_WAVE_REVIEW.md  (planning doc, can commit or ignore)
  docs/architecture-v2/WAVE5_PLAN.md      (planning doc, can commit or ignore)
  go/them                                  (compiled binary, gitignored)
```

No staged or unstaged changes in tracked files.

---

## Known Python↔Go Behavioral Differences (documented, not bugs)

| Route | Python | Go | Notes |
|---|---|---|---|
| POST /tokens missing `label` | 422 (Pydantic) | 400 | Both 4xx — acceptable |
| GET /sessions with both `app_id` and `ep_slug` | 200 (ignores `ep_slug`) | 400 | Go more correct |
| POST /sessions/{bad}/disconnect | 400 | 404 | Go more correct |
| Error envelope key | `"detail"` | `"error"` | FastAPI vs Go custom |

---

## Routes Still Owned by Python

See "Route Ownership" table above. Key ones to complete next:

1. `POST /api/v1/admin/providers` (LLM provider CRUD)
2. `GET/POST/PUT/DELETE /api/v1/admin/monitoring-config`
3. `POST /api/v1/admin/applications/{id}/import|export|restore|bulk-delete`
4. `PUT /api/v1/admin/applications/{id}/runtime`

---

## Wave 6 Has NOT Started

Wave 6 is undefined at this point. No Wave 6 code has been written.

---

## Next Focused Task

**Option A (recommended for next session):** Add lessons-learned L-10 for the ExecLua/ToAny bug, then start **Wave 6** — identify the next batch of Python routes to migrate.

**Option B:** Push pending commits first (requires git credentials):
```bash
git push origin main
# or
git push git@github.com:aviciot/theM.git main
```

**Option C:** Run Go integration tests against the live stack:
```bash
cd go
TEST_POSTGRES_DSN="host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable" \
go test -tags=integration -v ./internal/admin/...
```

---

## Starting and Validating the Next Session

```bash
# 1. Verify stack health
python3.12 scripts/tests/run_tests.py 01 02 03 04 15

# 2. Verify Go bridges healthy
docker ps --filter name=them-go-bridge --format "{{.Names}} {{.Status}}"

# 3. Verify Wave 5 routes working via Traefik
curl -s http://localhost:8088/api/v1/admin/tokens \
  -H "Authorization: Bearer $(curl -s -X POST http://localhost:8701/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | python3.12 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')" | python3.12 -m json.tool | head -5

# 4. Confirm Traefik labels active
docker inspect them-go-bridge | python3.12 -c "
import json,sys; d=json.load(sys.stdin)[0]
labels=d['Config']['Labels']
print('Wave 5 labels:', 'them-go-tokens' in ' '.join(labels.keys()))
"
```

---

## First Prompt for Next Session

```
Continuing from docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md

HEAD is 4728ef8. Wave 5 cutover is complete. 929 Python tests pass, 40 contract
tests pass, all smoke tests pass on both Go bridges and via Traefik.

Push is pending — run: git push origin main

After pushing, the next task is to identify Wave 6 routes: which remaining Python
admin routes can be migrated to Go without schema changes or new dependencies.
Read docs/architecture-v2/GO_WAVE_REVIEW.md for the planning context, then propose
a Wave 6 route list for review.
```
