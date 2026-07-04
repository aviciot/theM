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

---

## 2026-07-03 — pytest mock must target the import site, not the definition site

**Symptom:** `patch("app.services.auth_client.validate_jwt", ...)` had no effect inside WS tests — the WS router still called the real `validate_jwt` and got "Event loop is closed" because the httpx client was bound to a different event loop.
**Root cause:** `ws_orchestrator.py` does `from app.services.auth_client import validate_jwt` — creating a local name binding. Patching `app.services.auth_client.validate_jwt` replaces the original but the router's local binding is unaffected. Patching must target the module that *uses* the name: `app.routers.ws_orchestrator.validate_jwt`.
**Fix:** `patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt)`
**Watch for:** Always patch where the name is *used*, not where it's *defined*. This is the standard `unittest.mock` rule but easy to get wrong when the import is buried in service code.

---

## 2026-07-03 — TestClient (sync) and session-scoped asyncpg pool don't share an event loop

**Symptom:** WS tests using `starlette.testclient.TestClient` got "Event loop is closed" when the app tried to use the asyncpg pool or the httpx auth client.
**Root cause:** `TestClient` runs the ASGI app in a separate thread with its own event loop. The session-scoped asyncpg pool and httpx client were created in the pytest session loop — a different loop. Any await on those resources inside the TestClient thread fails.
**Fix:** For WS tests, patch all async I/O paths (auth, LLM provider) so they don't touch the session-pool resources. Use `new=async_fn` (not `side_effect=`) so the mock is a proper coroutine. Verify DB state afterward via the session pool (after the TestClient context exits and the thread is done).
**Watch for:** Never assume a session-scoped async resource is usable inside `TestClient`. Either patch it out or use an async WS client library (e.g. `httpx-ws`) that shares the test event loop.

---

## 2026-07-04 — asyncpg tries SSL first against a non-SSL Postgres

**Symptom:** `them-auth-service` crashed on startup with `ConnectionRefusedError: [Errno 111]` despite `them-postgres` being healthy. Stack trace showed `_create_ssl_connection`.
**Root cause:** asyncpg defaults to attempting an SSL handshake first. Our Postgres container has SSL disabled (`SHOW ssl` → `off`). The SSL connection attempt is refused at the TCP level, producing a misleading "connection refused" error rather than "SSL not supported".
**Fix:** Pass `ssl=False` explicitly to `asyncpg.create_pool(...)`. The bridge already had `connect_args={"ssl": False}` in SQLAlchemy — the auth service was missing it.
**Watch for:** Any new service using asyncpg directly (not via SQLAlchemy) must pass `ssl=False` when connecting to our Postgres. Do not rely on asyncpg's auto-detect.

---

## 2026-07-04 — Bash test scripts can't reach Docker from WSL

**Symptom:** `bash scripts/tests/run_all_tests.sh` silently failed — all docker commands returned empty output. The shell had bash but `docker` was not in WSL PATH.
**Root cause:** Docker Desktop on Windows does not add `docker` to WSL's PATH by default. The bash scripts used `docker exec` to run tests, which requires the host Docker CLI.
**Fix:** Rewrote all tests as a single Python runner (`scripts/tests/run_tests.py`) using `subprocess.run(["docker", ...])`. Python on Windows has Docker CLI in PATH via Docker Desktop. Runner is cross-platform — same command on Windows PowerShell and Linux bash.
**Watch for:** Never write test infrastructure that depends on bash + docker together. Python subprocess is the cross-platform safe choice. New tests go in `run_tests.py`, not new `.sh` files.

---

## 2026-07-05 — A2A SDK v1.1 broke AgentCard.url and added executor contract

**Symptom:** A2A agent containers crashed at startup with `AttributeError: Protocol message AgentCard has no "url" field`.
**Root cause:** SDK v1.1 removed `AgentCard.url`. The URL now lives in `card.supported_interfaces.add().url`.
**Fix:** Replace `card.url = "http://..."` with:
```python
iface = card.supported_interfaces.add()
iface.url = "http://..."
```
**Watch for:** Any agent built against SDK <1.1 will break on upgrade. Always check the AgentCard proto fields after an SDK upgrade.

---

## 2026-07-05 — A2A SDK v1.1 executor must enqueue Task object before TaskStatusUpdateEvent

**Symptom:** SDK raised `INVALID_AGENT_RESPONSE`: "Agent should enqueue Task before TaskStatusUpdateEvent" after the executor called `enqueue_event(TaskStatusUpdateEvent(...))` first.
**Root cause:** SDK v1.1 `DefaultRequestHandler` enforces ordering: the executor must enqueue a `Task` (with at minimum `id`, `context_id`, and `status.state=TASK_STATE_SUBMITTED`) before any `TaskStatusUpdateEvent`. The contract is documented in the `AgentExecutor.execute()` docstring.
**Fix:** Add this block at the very start of every executor's `execute()` method:
```python
task = Task()
task.id = context.task_id
task.context_id = context.context_id
task.status.state = TaskState.TASK_STATE_SUBMITTED
await event_queue.enqueue_event(task)
```
**Watch for:** Every new `AgentExecutor` subclass needs this. Forgetting it gives a misleading "invalid response" error at the SDK handler level, not in your code.

---

## 2026-07-05 — A2A SDK v1.1 JSON-RPC requires sse-starlette at import time

**Symptom:** A2A agent containers crashed with `ModuleNotFoundError: No module named 'sse_starlette'` even though `sse-starlette` was not listed in the agent's `requirements.txt`.
**Root cause:** `a2a-sdk`'s `jsonrpc_dispatcher.py` does a top-level `from sse_starlette.sse import EventSourceResponse` — even if the SSE path is never called, the import happens at module load. Any app that imports from `a2a.server.routes` (which we do via `add_a2a_routes_to_fastapi`) transitively imports the dispatcher.
**Fix:** Add `sse-starlette>=1.6.1` to every agent's `requirements.txt` that uses `a2a-sdk`.
**Watch for:** `a2a-sdk` has hidden transitive dependencies not declared in its own pyproject. After any SDK upgrade, spin up containers and watch for import errors before running tests.

---

## 2026-07-05 — A2A v1.0 wire protocol: role is int, part has no "kind", method is "SendMessage"

**Symptom:** Live test of `A2aAsyncAdapter` got three successive RPC errors when calling SDK v1.1 agents:
  - `Invalid enum value user` (role field)
  - `Message type has no field named kind` (part format)
  - `Method not found (-32601)` (method name)
  - `configuration has no field named blocking` (configuration field)
  - `A2A version '0.3' is not supported` (missing version header)

**Root cause:** Our adapter was written against an earlier spec. The A2A v1.0 proto enforces:
  - `role` is an integer enum (`ROLE_USER=1`), not a string `"user"`
  - `Part` uses oneof field names directly (`{"text": "..."}`) — no outer `"kind"` wrapper
  - JSON-RPC method name is `"SendMessage"` (CamelCase), not `"message/send"`
  - Non-blocking submit uses `configuration.returnImmediately: true`, not `blocking: false`
  - `A2A-Version: 1.0` header required — missing header triggers version validation failure (defaults to `0.3`)
  - Terminal state strings from SDK v1.1 are `TASK_STATE_COMPLETED` etc. (proto enum names), not lowercase `"completed"`

**Fix:** Updated `app/adapters/a2a_async_adapter.py`:
  - `_ROLE_USER = 1`, used directly as integer in message body
  - Part: `{"text": message}` (no `"kind"` key)
  - Method: `"SendMessage"`
  - Config: `{"returnImmediately": True}`
  - Headers: added `"A2A-Version": "1.0"`
  - `_TERMINAL` / `_INPUT_REQUIRED` sets include both `TASK_STATE_*` names and lowercase variants
  - `submit()` extracts `result.task.id` (SDK wraps result in `SendMessageResponse`)

**Watch for:** If the A2A spec or SDK changes the proto field names, state enum names, or method names — all three must stay in sync: agent executors, platform adapter, and `_TERMINAL`/`_INPUT_REQUIRED` sets. Verify with a live `stream_invoke` call against a real container, not just structural tests.
