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

## 2026-07-05 — SQLAlchemy AsyncSession is not safe for concurrent use

**Symptom:** Parallel agent tool calls (via `asyncio.gather`) caused `Method 'rollback()' can't be called here; method '_prepare_impl()' is already in progress` — one session transaction corrupted by another coroutine touching it simultaneously.
**Root cause:** SQLAlchemy's `AsyncSession` is not thread-safe or coroutine-safe for concurrent access within a single session. When `asyncio.gather` runs multiple `_invoke_agent` coroutines sharing the same outer `db` session, they step on each other's transaction state.
**Fix:** Each parallel `_invoke_agent` call opens its own `AsyncSessionLocal()` context manager. The outer `db` session is kept for the planning loop only. The `db` parameter is retained in the signature for backwards compatibility but not used inside the function.
**Watch for:** Any `asyncio.gather` across coroutines that accept the same `AsyncSession` — each must open its own session. Never share one session across concurrent coroutines.

---

## 2026-07-05 — agents FK to tasks must be ON DELETE SET NULL, not RESTRICT

**Symptom:** Deleting an agent via the UI returned HTTP 500 with `ForeignKeyViolationError: update or delete on table "agents" violates foreign key constraint "tasks_agent_id_fkey"`.
**Root cause:** `them.tasks.agent_id` and `them.run_steps.agent_id` were defined as plain `REFERENCES them.agents(id)` — which defaults to `ON DELETE RESTRICT`. Any agent that was ever invoked (has child task or run_step rows) cannot be deleted.
**Fix:** Both FKs changed to `ON DELETE SET NULL`. Task and run_step history is preserved; `agent_id` becomes NULL to indicate the agent no longer exists. Applied live via `ALTER TABLE` and updated `db/001_schema.sql`.
**Watch for:** Any new FK from a history/audit table to an admin-managed entity should default to `ON DELETE SET NULL` or `ON DELETE CASCADE`, not `RESTRICT`. History must survive entity deletion.

---

## 2026-07-06 — JWT "Invalid or disabled token" in playground WS

**Symptom:** Playground shows "Error: Invalid or disabled token" immediately when trying to connect the WebSocket, even though the user was logged in.
**Root cause:** `/api/auth/token` returned the JWT from the httpOnly cookie without checking expiry. JWTs have a 2-hour TTL. A user who logged in and left the tab open would silently get an expired token passed to the WS `?token=` parameter.
**Fix:** Added `jwtExpiresIn()` in `frontend/src/app/api/auth/token/route.ts` — if the token has < 30s left, call `/api/v1/auth/refresh`, set fresh cookies, and return the new token. Expired tokens are never returned to JS.
**Watch for:** Any place that hands a JWT to client-side JS for WS auth must check expiry first. The browser cannot refresh JWTs on its own because they live in httpOnly cookies.

---

## 2026-07-06 — Traefik v3.x fails on Docker 29 with "client version 1.24 is too old"

**Symptom:** Traefik v3.1/v3.3 Docker provider logged `Error 400: client version 1.24 is too old. Minimum supported API version is 1.44` and failed to discover any containers.
**Root cause:** Traefik's Go Docker client hard-codes `/v1.24/_ping` for initial API version negotiation. Docker 29 raised its minimum supported API version to 1.44 and rejects the v1.24 request. Setting `DOCKER_API_VERSION` env var on Traefik has no effect on this negotiation path.
**Fix:** Use `traefik:v3.6` (v3.6.1+). PR #12256 added `WithAPIVersionNegotiation()` to Traefik's Docker client — it now performs a proper version handshake instead of hardcoding v1.24.
**Watch for:** If Traefik is ever downgraded below v3.6 on Docker 29+, the Docker provider will silently fail to discover containers. `tecnativa/docker-socket-proxy` does NOT fix this — it doesn't rewrite the URL version path.

---

## 2026-07-06 — Traefik Docker provider silently skips unhealthy containers

**Symptom:** Traefik DEBUG logs showed `"Filtering unhealthy or starting container: them-frontend-..."`. The `them-ui` router never appeared in the Traefik API even though the labels were correct.
**Root cause:** Traefik's Docker provider skips containers that are not in `healthy` state (i.e., `starting` or `unhealthy`). The `them-frontend` healthcheck used `curl -f -L` but the Node.js Alpine container doesn't have `curl` — every check returned exit code -1 (executable not found), keeping the container perpetually `unhealthy`.
**Fix:** Changed healthcheck to `wget -q -O/dev/null http://localhost:3200/login || exit 1`. `wget` is available in Alpine busybox. Also: `docker compose restart` does NOT pick up a changed healthcheck — must use `docker compose up -d --force-recreate`.
**Watch for:** Never use `curl` in healthchecks for Node.js Alpine containers. Always check that the healthcheck binary exists in the target image before writing the check. A container that is `unhealthy` is invisible to Traefik.

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

---

## 2026-07-07 — Phase 9: count_context_tasks AttributeError silently swallowed — dead guard

**Symptom:** Fork-bomb guard in `a2a_server.py` contained `except AttributeError: pass` — if `task_store.count_context_tasks` didn't exist, the guard silently did nothing instead of blocking.

**Root cause:** The guard was written before the function was implemented in `task_store.py`. The `except AttributeError: pass` was a temporary shim that was never removed.

**Fix:** Implement `count_context_tasks` in `task_store.py` using `func.count()` + `notin_()` for terminal states. Remove the `AttributeError` catch — the guard now either works or raises properly.

**Watch for:** Silent exception suppression on security-critical paths. Never use bare `except Exception: pass` or `except AttributeError: pass` around auth/ownership/rate-limit checks.

---

## 2026-07-07 — TOCTOU: orchestrator scope check must be inside the same DB session as task creation

**Symptom:** `_handle_send_message` did an orchestrator lookup in one `async with` block, then a task creation in another. A race condition could allow a token scoped to orchestrator A to create a task for orchestrator B if the orchestrator was swapped between the two sessions.

**Fix:** Moved orchestrator lookup + scope check + task creation into a single `async with db_module.AsyncSessionLocal() as db:` block. Both the scope check and the `create_task` call now use the same session snapshot.

**Watch for:** Any multi-step operation that checks a permission then acts on it — always do both in the same DB transaction or session to avoid TOCTOU gaps.

---

## 2026-07-07 — Traefik silently stops routing when a container is unhealthy after restart

**Symptom:** `http://<host>:8088` appeared unreachable after a `docker compose up -d`. No Traefik errors, no obvious reason.

**Root cause:** Traefik's Docker provider drops any container from the routing table the moment it transitions to `unhealthy` or `starting`. A `docker compose up -d` that recreates containers puts them back in the `health: starting` window — during which Traefik refuses to route to them. If the healthcheck binary doesn't exist (e.g., `curl` missing on Alpine), the container stays `unhealthy` indefinitely.

**Fix:** `docker compose ps` immediately — look for any container showing `(unhealthy)` or `(health: starting)`. That is the thing Traefik is not routing to. Fix the healthcheck binary, recreate the container, wait for `(healthy)`.

**Watch for:** After any `docker compose up -d` that recreates `them-bridge` or `them-frontend`, give Traefik 10–20s to mark them healthy before assuming the URL is broken. Do not chase Traefik config or network issues until you've confirmed `docker compose ps` shows all containers `(healthy)`.

---

## 2026-07-07 — Multiple Traefik instances on one host — logs are not the same Traefik

**Symptom:** Investigating `them-traefik` unreachability, found Traefik container logs showing health check failures for `omni-bridge-svc` and other services we didn't recognize. Concluded Traefik was misconfigured.

**Root cause:** Multiple Traefik instances were running on the host — `them-traefik` (port 8088), `traefik-external` (8090/8091), `omni-traefik` (3000). All three share the Docker socket and pick up labels from all containers. The container whose logs we were reading was a *different* Traefik, not ours. Its errors were for Omni's containers, not the-M's.

**Fix:** Always confirm which Traefik instance you are looking at: `docker logs them-traefik` (not `traefik` or another alias). `docker compose ps` in `/opt/docker/odin` shows only our containers; the other Traefik instances belong to other stacks.

**Watch for:** On shared Docker hosts with multiple stacks, `docker ps` shows all containers from all projects. Always target by container name (`them-traefik`), never by image name alone.

---

## 2026-07-07 — Multi-turn: user message must be written to task_messages at task creation, not reconstructed on demand

**Symptom:** When building prior-turn history in `_load_context_history`, tasks that had no `task_messages` (e.g. if the agentic loop was interrupted before any messages were persisted) produced empty history, silently dropping the user's prior message from LLM context.

**Fix:** Save the user message as `task_message seq=0` immediately after root task creation, before entering the agentic loop. This makes the user message DB-durable at creation time, independent of whether the loop completes. Future turns always find it via `_load_context_history`.

**Watch for:** Any new "initial" data that subsequent turns need (system metadata, injected context, etc.) should also be stored in `task_messages` at task creation time — don't rely on `task.input_message` being available to future turns, as `_load_context_history` only reads `task_messages`.

---

## 2026-07-07 — Token expiry not checked at API layer — token_cache payload must include expires_at

**Symptom:** Access tokens with `expires_at` set in the DB were accepted by `/a2a` even after expiry, because `_row_to_payload` in `token_cache.py` didn't serialize `expires_at` into the cached dict.

**Fix:** Added `"expires_at": row.expires_at.isoformat() if row.expires_at else None` to `_row_to_payload`. Added an expiry check in `_resolve_bearer` that parses the ISO string and rejects if `expires_at < now(UTC)`.

**Watch for:** Any new token payload fields needed for policy enforcement must be added to `_row_to_payload` — the cache is the sole source of truth once a token is cached. If a field is missing from the payload dict, it cannot be enforced without a DB round-trip.
