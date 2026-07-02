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
