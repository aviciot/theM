# Linux Session Handoff
# the-M — Next steps for remote/Linux environment
# Last updated: 2026-07-21 (post-deployment session)

---

## Current deployment status

Linux stack is **fully running and validated**:
- Full compose stack up (temporal profile)
- DB initialized from schema_current.sql (29 versions)
- Dev users seeded (admin/admin123, avi/avi123)
- 985 tests passed, 0 failed, 6 skipped (as of 2026-07-21)
- All 55 sanity checks passing

```bash
cd theM_gateway
git pull origin main
git log --oneline -5
```

---

## Factual route ownership (current, not target)

| Path | Active owner | Traefik router | Notes |
|---|---|---|---|
| `/ws/*` | Python bridge | `them-ws@docker` priority 100 | Go router removed 2026-07-21 |
| `/sse/*` | Python bridge | (no dedicated router — falls to Python base) | Go router removed 2026-07-21 |
| `/apps/*` | Python bridge | `them-apps@docker` priority 100 | Always Python |
| `/api/v1/*` | Python bridge | `them-api@docker` priority 100 | Always Python |
| `/go-health/*` | Go bridge | `them-go-health@docker` priority 120 | Path-rewritten by Traefik |
| `/metrics` | Go bridge | Direct ports 8002/8003 | Not through Traefik |

Go WS/SSE handlers: **code exists in Go binary but no active Traefik route**. Phase 11c implemented Redis Streams infrastructure (`internal/runstream/`, `internal/runrecorder/`, dispatcher). It did not implement Go WS/SSE ownership.

---

## Code changes committed in this push

Both files are safe to commit — no secrets, no DB changes baked in.

### 1. `docker-compose.traefik.yml`

Removed `them-go-ws` (PathPrefix `/ws`, priority 110, service `them-go-svc`) and `them-go-sse` (PathPrefix `/sse`, priority 110, service `them-go-svc`) Traefik router labels.

**Why:** Go bridges have no WS/SSE handlers. These routers were shadowing Python's `them-ws@docker` (priority 100) and returning 404 for all Playground WebSocket connections.

**Effect:** `them-ws@docker` (Python bridge) is now the sole handler for `/ws`.

### 2. `app/temporal/activities.py`

Added `ready` event publish to `them:dash:run:{context_id}:ctx` pubsub channel inside `init_run_activity`, after the existing `run_start` publish.

**Why:** `bridge_client.stream_run_events()` subscribes to `them:dash:run:{context_id}:ctx` and waits for `{"type":"ready"}` to extract `run_id` (Phase 1 of its two-phase subscribe). Nobody was publishing this event — all WS sessions timed out silently after 15s with no events delivered to the browser.

**Code added (lines ~425-438 of `app/temporal/activities.py`):**
```python
if db_module.redis_client is not None:
    try:
        ctx_channel = f"{_DASH_RUN_PREFIX}{context_id}:ctx"
        ready_event = json.dumps({
            "type": "ready",
            "run_id": actual_run_id_str,
            "task_id": root_task_id_str,
            "context_id": context_id,
        })
        await db_module.redis_client.publish(ctx_channel, ready_event)
    except Exception as exc:
        logger.warning("init_run: context channel ready publish failed", error=str(exc))
```

---

## Windows live-DB-only changes (NOT in any committed file)

These were applied manually to the Windows dev Postgres instance. The Linux environment uses `schema_current.sql` for fresh installs — these issues **do not exist on Linux** (the current schema file has all these columns already). This section is for record only.

### ADD COLUMN fixes (dump was stale vs 001_schema.sql)

```sql
ALTER TABLE them.tasks ADD COLUMN IF NOT EXISTS error TEXT;
ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS error TEXT;
ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS iterations INTEGER NOT NULL DEFAULT 0;
ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
ALTER TABLE them.run_steps ADD COLUMN IF NOT EXISTS error TEXT;
ALTER TABLE them.run_steps ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
ALTER TABLE them.access_tokens ADD COLUMN IF NOT EXISTS label TEXT NOT NULL DEFAULT 'default';
ALTER TABLE them.access_tokens ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;
```

### FK constraints dropped — PENDING DECISION

These were dropped on the Windows Postgres instance during debugging. Status: **not approved as permanent design changes, not committed as a migration**.

| Constraint | Table | Exact SQL used | Why it blocked | Rollback SQL |
|---|---|---|---|---|
| `runs_orchestrator_id_fkey` | `them.runs` | `ALTER TABLE them.runs DROP CONSTRAINT IF EXISTS runs_orchestrator_id_fkey;` | App-based runs pass `app_orchestrators.id` as `orchestrator_id`; FK pointed to `them.orchestrators` (different table, different UUIDs — Phase 14 split) | `ALTER TABLE them.runs ADD CONSTRAINT runs_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE SET NULL;` |
| `tasks_orchestrator_id_fkey` | `them.tasks` | `ALTER TABLE them.tasks DROP CONSTRAINT IF EXISTS tasks_orchestrator_id_fkey;` | Same as above | `ALTER TABLE them.tasks ADD CONSTRAINT tasks_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE SET NULL;` |
| `runs_user_id_fkey` | `them.runs` | `ALTER TABLE them.runs DROP CONSTRAINT IF EXISTS runs_user_id_fkey;` | Public entry points produce `user_id=0` (anonymous); user 0 does not exist in `auth_service.users` | `ALTER TABLE them.runs ADD CONSTRAINT runs_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;` |
| `tasks_user_id_fkey` | `them.tasks` | `ALTER TABLE them.tasks DROP CONSTRAINT IF EXISTS tasks_user_id_fkey;` | Same as above | `ALTER TABLE them.tasks ADD CONSTRAINT tasks_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;` |

**Underlying ID-model conflict:**
- `orchestrator_id` conflict: `them.app_orchestrators` and `them.orchestrators` are separate tables with independent UUID sequences (Phase 14). App-based runs store `app_orchestrators.id` in `runs.orchestrator_id`. The old FK assumed the ID comes from `orchestrators`. They are incompatible.
- `user_id=0` conflict: Public entry points (`access_policy: {"mode":"public"}`) assign `user_id=0` (anonymous). The `auth_service.users` table has no row with `id=0`. The FK blocks all public-access runs.

**Possible permanent solutions (decision required before creating a migration):**

For orchestrator FK:
- (a) Drop FK permanently — `orchestrator_id` is informational only (recommended; no join query relies on FK integrity)
- (b) Change FK to reference `app_orchestrators` — but a run might come from either table depending on mode
- (c) Nullify `orchestrator_id` for app runs — loses the traceability link

For user FK:
- (a) Drop FK permanently — `user_id` is informational; auth is enforced at the WS gate, not the DB
- (b) Insert sentinel `user_id=0` row in `auth_service.users` — couples auth schema to orchestration schema
- (c) Use `NULL` instead of `0` for anonymous runs — requires code change in `apps.py`

**Linux fresh install:** `schema_current.sql` does not have these FK constraints (they were never in the canonical schema snapshot). A fresh Linux install will not have this problem.

---

## Pending FK decision

Before creating `db/026_relax_orchestrator_fkeys.sql`, the following must be decided:

1. Are the FK drops permanent design changes? (Recommended: yes for orchestrator FK, yes for user FK — option a for both)
2. Should `schema_current.sql` be audited to confirm these FKs are absent from the clean-install path?

Do not create the migration file until this decision is made.

---

## Current blocker: Anthropic API workspace limit

All LLM calls return HTTP 400:
```
"You have reached your specified workspace API usage limits.
 You will regain access on 2026-08-01 at 00:00 UTC."
```

Playground validation cannot complete until 2026-08-01 or an alternate API key is provided.

The `.env` file path: `theM_gateway/.env`  
Secret generation: `./generate-env.sh` (Linux) — requires `secrets.local` with `THE_M_MASTER_SECRET`.  
`ANTHROPIC_API_KEY` is not derived — must be set manually in `.env`.

---

## First commands for next session

```bash
# 1. Pull any remote changes
cd theM_gateway
git pull origin main

# 2. Run sanity tests (stack is already running)
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
# Expected: 55 passed

# 3. Run full suite
ADMIN_JWT=$(docker exec them-bridge python3 -c "
import urllib.request, json
body = json.dumps({'username':'admin','password':'admin123'}).encode()
req = urllib.request.Request('http://them-auth-service:8701/api/v1/auth/login', data=body, headers={'Content-Type':'application/json'}, method='POST')
with urllib.request.urlopen(req, timeout=10) as r:
    print(json.loads(r.read())['access_token'])
")
ADMIN_JWT=$ADMIN_JWT python3.12 scripts/tests/run_tests.py
# Expected: 985 passed, 0 failed, 6 skipped

# 4. Wait for API limit reset (2026-08-01) or provide alternate ANTHROPIC_API_KEY
# Then run Playground validation:
# http://localhost:8088/admin/playground
```

---

## Playground validation test matrix (blocked until API key available)

15 flows to validate:
1. Application selection (list apps, select debator)
2. Entry point selection (WS vs SSE types visible)
3. Runtime config loading (max_iterations, model shown)
4. Run creation via WS connect
5. Auth flow (JWT from `/api/auth/token` → WS `?token=` param)
6. Traefik routing (WS reaches Python bridge, not Go 404)
7. `ready` event received (run_id extracted, Phase 1 complete)
8. Token stream events (Phase 2 subscribe active)
9. Run status updates (working → done)
10. Reconnect / session resume
11. Error display (LLM errors shown in UI, not silent)
12. Timeout display
13. Tasks tab (run tasks visible)
14. Artifacts tab (if any artifacts produced)
15. Cancellation (cancel button stops workflow)

---

## Environment and secrets

- `.env` at `theM_gateway/.env` (generated from `secrets.local` via `./generate-env.sh`)
- `secrets.local` at `theM_gateway/secrets.local` (contains `THE_M_MASTER_SECRET` — never commit)
- `ANTHROPIC_API_KEY` must be set manually in `.env`
- DB credentials derived from `THE_M_MASTER_SECRET` — re-run `./generate-env.sh` if `.env` is missing

Installation scripts:
- `./scripts/linux-start.sh --build` — full stack start with fresh build
- `./scripts/linux-health.sh` — verify all containers + endpoints
- `./scripts/linux-validate-clean-install.sh` — 7-phase automated validation

---

## What NOT to do at session start

- Do not create `db/026_relax_orchestrator_fkeys.sql` without explicit approval
- Do not start Phase 11c-D (remove Pub/Sub)
- Do not begin the two-week staging observation period
- Do not perform a production deployment
- Do not start a broad Python-to-Go API rewrite
- Do not remove Pub/Sub (production/default mode remains pubsub)
