---
title: Lessons — Python-era (historical, platform is Go now)
type: lessons
status: superseded
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-07-02 — PgBouncer rejects startup parameters

**Symptom:** auth-service crashed with `unsupported startup parameter: search_path` (and earlier `options`).
**Root cause:** PgBouncer's default config strips unknown startup parameters. Both `?options=-c search_path=...` in the DSN and `server_settings={"search_path": ...}` in asyncpg are sent as startup params — PgBouncer drops them.
**Fix:** Use an asyncpg `init` callback to run `SET search_path TO auth_service` after each connection is established. Works with PgBouncer and direct Postgres.
**Watch for:** Any new service connecting through PgBouncer — never use `server_settings` or `?options=` in the DSN. Always use an `init` callback.

---

---

## 2026-07-02 — Health endpoint imported globals before init

**Symptom:** `/health` returned `status: degraded`, db/redis both `error`, even though startup logs showed "Database connection OK" and "Redis connection OK".
**Root cause:** `health.py` imported `engine` and `redis_client` from `app.database` at module load time. At that point both were `None` (set later by `init_db()` in lifespan). The health check was always calling `None.ping()`.
**Fix:** Import `app.database` as a module reference and access `db_module.engine` / `db_module.redis_client` at call time, not import time.
**Watch for:** Any router that needs a resource initialized in lifespan — never import the resource value directly, always import the module.

---

---

## 2026-07-02 — Postgres initdb rejects non-empty mount directory

**Symptom:** `odin-postgres` crash-looped with `initdb: error: directory exists but is not empty` after adding a `.gitkeep` file to `volumes/postgres/`.
**Root cause:** Postgres `initdb` refuses to initialize into a directory containing any file, including hidden ones like `.gitkeep`.
**Fix:** Mount a subdirectory: `./volumes/postgres/pgdata:/var/lib/postgresql/data`. The `.gitkeep` stays in `volumes/postgres/`, Postgres initializes into the clean `pgdata/` subdirectory.
**Watch for:** Never put any file directly inside the Postgres data directory mount point.

---

---

## 2026-07-02 — agents_slug_check rejects hyphens

**Symptom:** POST `/api/v1/admin/agents` returned 500 with `CheckViolationError: agents_slug_check`.
**Root cause:** DB constraint `slug ~ '^[a-z0-9_]{1,48}$'` — hyphens not allowed. Test was using `test-smoke-agent`.
**Fix:** Test slugs must use underscores: `test_smoke_agent`.
**Watch for:** All agent slugs must be `[a-z0-9_]`, max 48 chars. Validate this in the API layer too (currently only enforced by DB constraint).

---

---

## 2026-07-02 — Shared infra creates hard coupling

**Symptom:** Auth service crashed due to PgBouncer auth config mismatch; Redis DB index created namespace risk.
**Root cause:** Original design shared `omni-postgres` (via PgBouncer) and `omni-redis` with the Omni stack — any config change on Omni's side could break Odin.
**Fix:** Full isolation — own `odin-postgres`, own `odin-redis`, own `odin-network`. Each service has its own source folder. All data in bind-mounted `volumes/` subdirectories.
**Watch for:** Never reintroduce shared infrastructure. If a future service needs Postgres or Redis, add it to Odin's own compose file.

---

---

## 2026-07-03 — mock_agent containers need rebuild, not just restart

**Symptom:** Fixed a `NameError: name 'path' is not defined` in `mock_agent/agent.py`, ran `docker compose restart mock-agent-*` — error persisted.
**Root cause:** mock agent containers are built from a Dockerfile (no volume mount). `restart` reuses the existing image. Code change only takes effect after `docker compose build`.
**Fix:** `docker compose build mock-agent-assistant mock-agent-researcher mock-agent-coder && docker compose up -d mock-agent-*`
**Watch for:** Any container built from a Dockerfile without a source volume mount requires `build` to pick up code changes. Containers with `volumes: - .:/app` only need `restart`.

---

---

## 2026-07-03 — pytest mock must target the import site, not the definition site

**Symptom:** `patch("app.services.auth_client.validate_jwt", ...)` had no effect inside WS tests — the WS router still called the real `validate_jwt` and got "Event loop is closed" because the httpx client was bound to a different event loop.
**Root cause:** `ws_orchestrator.py` does `from app.services.auth_client import validate_jwt` — creating a local name binding. Patching `app.services.auth_client.validate_jwt` replaces the original but the router's local binding is unaffected. Patching must target the module that *uses* the name: `app.routers.ws_orchestrator.validate_jwt`.
**Fix:** `patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt)`
**Watch for:** Always patch where the name is *used*, not where it's *defined*. This is the standard `unittest.mock` rule but easy to get wrong when the import is buried in service code.

---

---

## 2026-07-03 — TestClient (sync) and session-scoped asyncpg pool don't share an event loop

**Symptom:** WS tests using `starlette.testclient.TestClient` got "Event loop is closed" when the app tried to use the asyncpg pool or the httpx auth client.
**Root cause:** `TestClient` runs the ASGI app in a separate thread with its own event loop. The session-scoped asyncpg pool and httpx client were created in the pytest session loop — a different loop. Any await on those resources inside the TestClient thread fails.
**Fix:** For WS tests, patch all async I/O paths (auth, LLM provider) so they don't touch the session-pool resources. Use `new=async_fn` (not `side_effect=`) so the mock is a proper coroutine. Verify DB state afterward via the session pool (after the TestClient context exits and the thread is done).
**Watch for:** Never assume a session-scoped async resource is usable inside `TestClient`. Either patch it out or use an async WS client library (e.g. `httpx-ws`) that shares the test event loop.

---

---

## 2026-07-04 — asyncpg tries SSL first against a non-SSL Postgres

**Symptom:** `them-auth-service` crashed on startup with `ConnectionRefusedError: [Errno 111]` despite `them-postgres` being healthy. Stack trace showed `_create_ssl_connection`.
**Root cause:** asyncpg defaults to attempting an SSL handshake first. Our Postgres container has SSL disabled (`SHOW ssl` → `off`). The SSL connection attempt is refused at the TCP level, producing a misleading "connection refused" error rather than "SSL not supported".
**Fix:** Pass `ssl=False` explicitly to `asyncpg.create_pool(...)`. The bridge already had `connect_args={"ssl": False}` in SQLAlchemy — the auth service was missing it.
**Watch for:** Any new service using asyncpg directly (not via SQLAlchemy) must pass `ssl=False` when connecting to our Postgres. Do not rely on asyncpg's auto-detect.

---

---

## 2026-07-04 — Bash test scripts can't reach Docker from WSL

**Symptom:** `bash scripts/tests/run_all_tests.sh` silently failed — all docker commands returned empty output. The shell had bash but `docker` was not in WSL PATH.
**Root cause:** Docker Desktop on Windows does not add `docker` to WSL's PATH by default. The bash scripts used `docker exec` to run tests, which requires the host Docker CLI.
**Fix:** Rewrote all tests as a single Python runner (`scripts/tests/run_tests.py`) using `subprocess.run(["docker", ...])`. Python on Windows has Docker CLI in PATH via Docker Desktop. Runner is cross-platform — same command on Windows PowerShell and Linux bash.
**Watch for:** Never write test infrastructure that depends on bash + docker together. Python subprocess is the cross-platform safe choice. New tests go in `run_tests.py`, not new `.sh` files.

---

---

## 2026-07-05 — A2A SDK v1.1 JSON-RPC requires sse-starlette at import time

**Symptom:** A2A agent containers crashed with `ModuleNotFoundError: No module named 'sse_starlette'` even though `sse-starlette` was not listed in the agent's `requirements.txt`.
**Root cause:** `a2a-sdk`'s `jsonrpc_dispatcher.py` does a top-level `from sse_starlette.sse import EventSourceResponse` — even if the SSE path is never called, the import happens at module load. Any app that imports from `a2a.server.routes` (which we do via `add_a2a_routes_to_fastapi`) transitively imports the dispatcher.
**Fix:** Add `sse-starlette>=1.6.1` to every agent's `requirements.txt` that uses `a2a-sdk`.
**Watch for:** `a2a-sdk` has hidden transitive dependencies not declared in its own pyproject. After any SDK upgrade, spin up containers and watch for import errors before running tests.

---

---

## 2026-07-05 — SQLAlchemy AsyncSession is not safe for concurrent use

**Symptom:** Parallel agent tool calls (via `asyncio.gather`) caused `Method 'rollback()' can't be called here; method '_prepare_impl()' is already in progress` — one session transaction corrupted by another coroutine touching it simultaneously.
**Root cause:** SQLAlchemy's `AsyncSession` is not thread-safe or coroutine-safe for concurrent access within a single session. When `asyncio.gather` runs multiple `_invoke_agent` coroutines sharing the same outer `db` session, they step on each other's transaction state.
**Fix:** Each parallel `_invoke_agent` call opens its own `AsyncSessionLocal()` context manager. The outer `db` session is kept for the planning loop only. The `db` parameter is retained in the signature for backwards compatibility but not used inside the function.
**Watch for:** Any `asyncio.gather` across coroutines that accept the same `AsyncSession` — each must open its own session. Never share one session across concurrent coroutines.

---

---

## 2026-07-07 — Phase 9: count_context_tasks AttributeError silently swallowed — dead guard

**Symptom:** Fork-bomb guard in `a2a_server.py` contained `except AttributeError: pass` — if `task_store.count_context_tasks` didn't exist, the guard silently did nothing instead of blocking.

**Root cause:** The guard was written before the function was implemented in `task_store.py`. The `except AttributeError: pass` was a temporary shim that was never removed.

**Fix:** Implement `count_context_tasks` in `task_store.py` using `func.count()` + `notin_()` for terminal states. Remove the `AttributeError` catch — the guard now either works or raises properly.

**Watch for:** Silent exception suppression on security-critical paths. Never use bare `except Exception: pass` or `except AttributeError: pass` around auth/ownership/rate-limit checks.

---

---

## 2026-07-09 — `_Proxy` + `setattr` for orchestrator cache is a silent bug magnet

**Symptom:** Adding a new field to the orchestrator cache required updating: the Redis write payload, the `setattr` loop, AND `getattr()` calls at each use site — three places that could drift.

**Root cause:** The `_Proxy` class had no schema — any missing field silently returned `None` or raised `AttributeError` at runtime, not at construction.

**Fix:** Replaced with `@dataclass _OrchestratorProxy` with typed fields and explicit defaults. Missing fields are caught at construction time, not at use.

**Watch for:** When adding a new orchestrator config field: add it to `_OrchestratorProxy`, the cache write payload in `_load_orchestrator_row`, and the DB schema — three places, same as before, but now the dataclass guards the runtime contract.

---

---

## 2026-07-15 — `isinstance()` on ORM class fails for cache-hit proxy objects

**Symptom:** Phase 6 used `isinstance(orch, AppOrchestrator)` to detect app orchestrators and conditionally enforce scope. The check worked on the DB-miss path (fresh SQLAlchemy row) but silently returned `False` on cache hits, because `load_orchestrator_row` returns an `_OrchestratorProxy` dataclass on cache hits — not an ORM instance.

**Root cause:** The cache layer deserializes Redis JSON into a plain dataclass (`_OrchestratorProxy`). `isinstance(proxy, AppOrchestrator)` always returns `False` regardless of what the original DB row was. Two successive wrong fixes made things worse:
1. *First wrong fix:* Skipped scope enforcement entirely when `isinstance` returned `False` — no check at all on cache hits, a security hole.
2. *Second wrong fix:* Added a parent FK fallback (`str(getattr(orch, "orchestrator_id", None) or "")`) — still wrong because `orchestrator_id` isn't serialized into the cache dict, so cache-hit proxies returned empty string → `PermissionError` on every legitimate unscoped request.

**Fix:** Carry `"is_app_orchestrator": isinstance(row, AppOrchestrator)` in the cache dict, evaluated on the DB-miss path where `row` IS a real ORM object. Populate the `_OrchestratorProxy` field as `data.get("is_app_orchestrator", False)`. Consume at call sites as `orch.is_app_orchestrator` — a plain `bool` attribute, no `isinstance` anywhere outside the cache writer.

**Watch for:** Never use `isinstance(obj, OrmModel)` on objects returned from a cache-loading function. The cache layer always returns a proxy/dataclass, not an ORM row. If a type distinction must influence runtime behavior, carry a type-flag in the serialized cache dict and read it back as a plain attribute on the proxy. This rule applies to any future cache layer wrapping SQLAlchemy models (`Agent`, `AccessToken`, `EntryPoint`, etc.).

---

---

## 2026-07-15 — Expand/contract: drop fallback paths before dropping the column

**Symptom:** Phase 12 dropped `applications.orchestrator_id` and `orchestrators.a2a_exposed`. Several files still had fallback branches that referenced those columns at runtime: `apps.py` (REST/SSE/WS), `a2a_server.py` (legacy `Application.orchestrator_id → Orchestrator.name` resolution), `webrtc.py` (`_load_app` queried `Application.slug` — a column that never existed), and `loaders.py` (the `a2a_exposed == True` fallback query in `load_agents`). None of these caused an import error; they would all have blown up at request time.

**Root cause:** The expand/contract pattern (`add column → use both → drop old`) requires an explicit audit pass before the drop. The fallback code was written to be safe during the expand phase, but it was never removed going into the drop phase. The pre-migration fallback in `apps.py` masked the real bug in `webrtc.py`: `_load_app` was querying `Application.slug` which does not exist on the `Application` model — slug lives on `EntryPoint`. This would have raised `AttributeError` at query time, but was never exercised because `webrtc.py` is not covered by the live test suite.

**Fix:** Before dropping a column: `grep -rn "column_name" app/` across the whole codebase, not just the files you remember touching. Check every reference — some will be load-bearing code paths, others will be dead fallbacks.

**Watch for:** When you see `if row.old_column: ...` as a fallback in an expand/contract migration, treat it as a time bomb. It must be removed in the same PR that drops the column. Also: any loader that queries by a model attribute (e.g. `Application.slug`) is a lie until you've confirmed that attribute exists in `models.py`. grep for it before assuming.

Corollary: **always restart the bridge after a column drop**, even if the code change looks complete. The running Python process has the old ORM module in memory. Until restarted it generates queries for the dropped column and returns HTTP 500 on every affected endpoint.

---

---

## 2026-07-18 — System `python3` is 3.6 — silently breaks the entire test runner

**Symptom:** Running `python scripts/tests/run_tests.py` produced `0 passed, 57+ failed` with every live test showing `(got '')`. No exception, no error message — just empty strings everywhere.
**Root cause:** The host OS is RHEL/CentOS 8 — system `python3` is Python 3.6. `subprocess.run(..., capture_output=True)` was added in Python 3.7. Every `docker()` helper call hit the `except Exception: return ""` fallback silently.
**Fix:** Always use `python3.12` (installed at `/usr/bin/python3.12`) — this is the same Python the app and containers run on. CLAUDE.md and INDEX.md both updated to say `python3.12`.
**Watch for:** Any script that runs `python` or `python3` directly. The system Python is 3.6 — it won't crash, it will silently degrade. The tell is everything returning `""`.

---

---

## 2026-07-18 — CI migration list must be kept in sync manually

**Symptom:** GitHub Actions CI was failing on live tests with schema errors even though everything passed locally. The `ci.yml` "Apply DB migrations" step was frozen at `006_phase11.sql` — 15 newer migrations (007–021) were never being applied in CI, so `them.canvas_layout`, `them.app_orchestrators`, `them.entry_points` etc. didn't exist on the CI schema.
**Root cause:** Migrations are added to `db/` incrementally but `ci.yml` is a static list that nobody updated for 15 consecutive migrations.
**Fix:** Expanded `ci.yml` to apply all 21 migrations in order (001–021). Going forward, every new `db/0NN_*.sql` file MUST also be added to both `ci.yml` and `CLAUDE.md` common DB commands.
**Watch for:** After adding any new migration file — immediately add it to `ci.yml` and `CLAUDE.md`. If CI fails with "column does not exist" or "table does not exist" on a live test, the first thing to check is whether all migrations are in `ci.yml`.

---

---

## 2026-07-18 — Stale migration breaks clean schema apply (entry_point_type constraint)

**Symptom:** CI live job failed on test_01 (schema check) with "column does not exist" errors on `them.canvas_layout` — seemingly unrelated tables. The real failure was earlier and silent: `005_phase10.sql` aborted its transaction, which caused psql to skip that migration without a clear error, leaving the schema partially applied and cascading failures downstream.
**Root cause:** `005_phase10.sql` tried to `ADD CONSTRAINT ... CHECK (entry_point_type IN ...)` on `them.applications`. That column was moved to `them.entry_points` by `010_app_entrypoints.sql`. On a live DB that already had `010` applied, the constraint was already gone. On a clean CI schema, the column never existed — so the ALTER TABLE failed, aborted the transaction, and the rest of that migration was skipped.
**Fix:** Neutralized `005_phase10.sql` to only `DROP CONSTRAINT IF EXISTS` (always a no-op). Principle: never ADD or reference a column that a later migration moves or drops — either update the earlier migration or make it a no-op.
**Watch for:** Any migration that does `ADD CONSTRAINT`, `ADD COLUMN`, or `ALTER COLUMN` on a table that a later migration restructures. On a live DB it looks fine (the column exists from the original schema); on a clean CI schema it fails because that migration runs before the column is added.

---

---

## 2026-07-18 — SCARD+SADD race and ghost EP set entries

**Symptom (potential):** Under a concurrent session cap, two simultaneous connections both read SCARD=N (below cap) before either SADDs, both proceed, cap is exceeded by 1. Under pod crashes, session_ids left in `them:ep:{slug}:sessions` permanently consume cap slots even though the session is long dead.

**Root cause (race):** A pipeline-based SCARD + SADD is NOT atomic across Redis replicas (or even concurrent connections to the same Redis). Two clients can both see the same count before either writes.

**Fix (race):** Use a server-side Lua script via `EVAL`. The script executes atomically on Redis: prunes ghost entries (session_ids whose `them:sess:{id}` hash no longer exists), checks the live count, then SADDs the new session_id — all in a single uninterruptible operation. Pipeline is wrong for this; only Lua EVAL or SETNX/WATCH/MULTI gives true atomicity.

**Fix (ghost entries):** The Lua gate script checks `EXISTS them:sess:{sid}` for every member before counting. If the session hash has expired (90s TTL, pod crash), the member is SREMed before the cap comparison. This ensures the SCARD reflects only genuinely live sessions.

**Watch for:** Any Redis pattern that does read-then-write where the correctness depends on the count being stable between the two operations. Prefer Lua EVAL for any check-and-modify pattern.

---

---

## 2026-07-18 — asyncio.wait result guard must check task result, not just task presence in done set

**Symptom:** Admin session terminate worked but so did Redis errors — any exception in the control listener caused the workflow to be cancelled and WS closed with 4000 "terminated by administrator".

**Root cause:** `asyncio.wait([stream_t, cancel_t, control_t])` returns a task in `done` regardless of whether it completed normally, raised, or returned `None`. The guard `if control_t in done:` was missing the result check — it fired on both normal termination signals AND on any Redis error that caused the coroutine to fall through with `None`.

**Fix:** Always check `task.result()` after checking task membership in `done`: `if control_t in done and control_t.result() == "admin_terminate":`. The control listener explicitly returns `"admin_terminate"` only when a real signal was received; exceptions swallowed by `except: pass` leave it returning `None`.

**Watch for:** Any pattern using `asyncio.wait` with tasks that have multiple exit paths — always check both `in done` AND the actual return value or exception.

---

---

## 2026-07-21 — schema_current.sql must be audited against models.py on every fresh Linux install

**Symptom:** Fresh Linux install from `schema_current.sql` caused 500 errors on GET /admin/tokens, DELETE /admin/agents, all runs endpoints. Errors: `column access_tokens.label does not exist`, `column run_steps.error does not exist`.

**Root cause:** `schema_current.sql` was created as a snapshot of the Windows dev DB dump — but the dump was from an old restore point that predated several column additions (label, enabled on access_tokens; error, latency_ms, ended_at on run_steps; error, iterations, ended_at on runs). Those columns existed in 001_schema.sql but were absent from the dump snapshot.

**Fix:** Before committing a new `schema_current.sql`, diff every table DDL against `app/models.py`:
1. Every `Mapped[...]` column in the model must appear in the corresponding `CREATE TABLE` in schema_current.sql
2. No column in models.py should be missing from the SQL
Added `label TEXT NOT NULL DEFAULT 'default'`, `enabled BOOLEAN NOT NULL DEFAULT true` to `access_tokens`; `error TEXT`, `latency_ms INTEGER`, `ended_at TIMESTAMPTZ` to `run_steps`; `error TEXT`, `iterations INTEGER NOT NULL DEFAULT 0`, `ended_at TIMESTAMPTZ` to `runs`.

**Watch for:** Any time `schema_current.sql` is regenerated from a live DB dump — always validate it against models.py before committing. Prefer generating from `001_schema.sql` + applied migrations rather than from a dump.

---

---

## 2026-07-21 — Test suite uses user_id=99 which doesn't exist in fresh DB

**Symptom:** test_11 (WS orchestrate) failed: `[FAIL] Can create bearer token for WS auth` with `ForeignKeyViolationError` on `access_tokens.user_id`.

**Root cause:** Test 11 creates a bearer token with `user_id: 99`. On the Windows dev DB, user 99 happened to exist. On a fresh Linux install from seed_users.sql (only admin=1 and avi=2), user 99 does not exist. The FK `access_tokens_user_id_fkey` → `auth_service.users(id)` rejects the insert.

**Fix:** Changed test 11 to use `user_id: 1` (admin). General principle: tests that hardcode user IDs must use IDs guaranteed to exist in a fresh seeded DB (1 = admin, 2 = avi).

**Watch for:** Any test that hardcodes a `user_id` other than 1 or 2 — may fail on fresh installs.

---
