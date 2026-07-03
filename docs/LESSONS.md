# Lessons Learned
# Append-only. Format: symptom → root cause → fix → watch for next time.

---

## 2026-07-02 — PgBouncer rejects startup parameters

**Symptom:** auth-service crashed with `unsupported startup parameter: search_path` (and earlier `options`).
**Root cause:** PgBouncer's default config strips unknown startup parameters. Both `?options=-c search_path=...` in the DSN and `server_settings={"search_path": ...}` in asyncpg are sent as startup params — PgBouncer drops them.
**Fix:** Use an asyncpg `init` callback to run `SET search_path TO auth_service` after each connection is established. Works with PgBouncer and direct Postgres.
**Watch for:** Any new service connecting through PgBouncer — never use `server_settings` or `?options=` in the DSN. Always use an `init` callback.

---

## 2026-07-02 — Health endpoint imported globals before init

**Symptom:** `/health` returned `status: degraded`, db/redis both `error`, even though startup logs showed "Database connection OK" and "Redis connection OK".
**Root cause:** `health.py` imported `engine` and `redis_client` from `app.database` at module load time. At that point both were `None` (set later by `init_db()` in lifespan). The health check was always calling `None.ping()`.
**Fix:** Import `app.database` as a module reference and access `db_module.engine` / `db_module.redis_client` at call time, not import time.
**Watch for:** Any router that needs a resource initialized in lifespan — never import the resource value directly, always import the module.

---

## 2026-07-02 — Postgres initdb rejects non-empty mount directory

**Symptom:** `odin-postgres` crash-looped with `initdb: error: directory exists but is not empty` after adding a `.gitkeep` file to `volumes/postgres/`.
**Root cause:** Postgres `initdb` refuses to initialize into a directory containing any file, including hidden ones like `.gitkeep`.
**Fix:** Mount a subdirectory: `./volumes/postgres/pgdata:/var/lib/postgresql/data`. The `.gitkeep` stays in `volumes/postgres/`, Postgres initializes into the clean `pgdata/` subdirectory.
**Watch for:** Never put any file directly inside the Postgres data directory mount point.

---

## 2026-07-02 — agents_slug_check rejects hyphens

**Symptom:** POST `/api/v1/admin/agents` returned 500 with `CheckViolationError: agents_slug_check`.
**Root cause:** DB constraint `slug ~ '^[a-z0-9_]{1,48}$'` — hyphens not allowed. Test was using `test-smoke-agent`.
**Fix:** Test slugs must use underscores: `test_smoke_agent`.
**Watch for:** All agent slugs must be `[a-z0-9_]`, max 48 chars. Validate this in the API layer too (currently only enforced by DB constraint).

---

## 2026-07-02 — Shared infra creates hard coupling

**Symptom:** Auth service crashed due to PgBouncer auth config mismatch; Redis DB index created namespace risk.
**Root cause:** Original design shared `omni-postgres` (via PgBouncer) and `omni-redis` with the Omni stack — any config change on Omni's side could break Odin.
**Fix:** Full isolation — own `odin-postgres`, own `odin-redis`, own `odin-network`. Each service has its own source folder. All data in bind-mounted `volumes/` subdirectories.
**Watch for:** Never reintroduce shared infrastructure. If a future service needs Postgres or Redis, add it to Odin's own compose file.

---

## 2026-07-03 — NEXT_PUBLIC_ env vars are baked at build time, not runtime

**Symptom:** Playground WS connected to `ws://localhost:8001` in the browser — connection refused because the bridge is not on localhost from the user's machine.
**Root cause:** `NEXT_PUBLIC_*` variables in Next.js are inlined at build time by webpack. Setting them in docker-compose `environment:` only affects the Next.js server process, not the browser bundle. The browser always got the hardcoded fallback `ws://localhost:8001`.
**Fix:** Derive the WS URL at runtime using `window.location.hostname` so the browser connects to the correct host. Expose bridge port 8001 on the host so the browser can reach it directly.
**Watch for:** Never rely on `NEXT_PUBLIC_*` for values that differ between dev/prod unless the image is rebuilt for each environment. Runtime config must come from `window.location` or an API call.

---

## 2026-07-03 — mock_agent containers need rebuild, not just restart

**Symptom:** Fixed a `NameError: name 'path' is not defined` in `mock_agent/agent.py`, ran `docker compose restart mock-agent-*` — error persisted.
**Root cause:** mock agent containers are built from a Dockerfile (no volume mount). `restart` reuses the existing image. Code change only takes effect after `docker compose build`.
**Fix:** `docker compose build mock-agent-assistant mock-agent-researcher mock-agent-coder && docker compose up -d mock-agent-*`
**Watch for:** Any container built from a Dockerfile without a source volume mount requires `build` to pick up code changes. Containers with `volumes: - .:/app` only need `restart`.

---

## 2026-07-03 — Redis cache survives DB wipe; stale FK causes silent run failure

**Symptom:** Playground ran successfully (LLM responded, trace showed events) but `odin.runs` table was empty. No errors in the UI.
**Root cause:** Postgres was recreated (DB wiped) but Redis survived. The orchestrator cache (`odin:orchestrators:default`) still held the old `orchestrator_id` UUID. `run_recorder.start_run()` inserted a Run with that UUID as FK — but the orchestrators table was empty, causing a FK violation. `start_run()` had no try/except, so it raised, was swallowed somewhere upstream, and the run proceeded without a DB record.
**Fix:** (1) Added try/except to `start_run()` so it logs the error clearly and returns a dummy UUID rather than crashing silently. (2) After any DB reset, recreate orchestrators via the UI — this writes a fresh DB row and updates Redis with the correct ID.
**Watch for:** After `docker compose down -v` or any DB volume wipe, always recreate all orchestrators and agents via the UI before testing. Redis TTLs mean stale cache can persist for up to 600s even after a DB reset.
