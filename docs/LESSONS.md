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

---

## 2026-07-09 — A2A typed input: `Part.data` vs string concatenation

**Symptom:** `docu_writer` received `FORMAT: html\nTITLE: ...\nCONTENT:\n...` as plain text and parsed it with regex — brittle, breaks if LLM varies the format, and requires agent-specific instructions in the orchestrator system prompt.

**Root cause:** The adapter always sent `{"parts": [{"text": message}]}`. The agent card had no `input_modes` declaration. The LLM had no way to know the agent expected structured input.

**Fix:**
1. Declare `input_modes: ["application/json"]` on agent skills so the LLM and adapter know the contract.
2. `_build_parts()` in the adapter sends `{"data": input_dict}` when `application/json` is declared.
3. Agent reads `part.HasField("data")` + `json_format.MessageToDict(part.data.struct_value)` — no regex.
4. `_ensure_agent_skills` stores `input_modes`/`output_modes` from the fetched card so the factory can wire them.
5. Orchestrator system prompt only describes the goal — never agent-specific format strings.

**Watch for:** Any new A2A agent that expects structured fields: declare `input_modes: ["application/json"]` on its skills. Never encode format contracts in the orchestrator system prompt — the agent card is the sole contract.

---

## 2026-07-09 — `_Proxy` + `setattr` for orchestrator cache is a silent bug magnet

**Symptom:** Adding a new field to the orchestrator cache required updating: the Redis write payload, the `setattr` loop, AND `getattr()` calls at each use site — three places that could drift.

**Root cause:** The `_Proxy` class had no schema — any missing field silently returned `None` or raised `AttributeError` at runtime, not at construction.

**Fix:** Replaced with `@dataclass _OrchestratorProxy` with typed fields and explicit defaults. Missing fields are caught at construction time, not at use.

**Watch for:** When adding a new orchestrator config field: add it to `_OrchestratorProxy`, the cache write payload in `_load_orchestrator_row`, and the DB schema — three places, same as before, but now the dataclass guards the runtime contract.

---

## 2026-07-11 — Temporal activity heartbeat timeout fires before first poll event

**Symptom:** `invoke_agent_activity` fails with "activity Heartbeat timeout" after exactly 30 seconds. Agent is slow but functional. Two orphaned `run_steps` rows (one per retry attempt) remain stuck in `running` state.

**Root cause:** Two issues compounded:
1. `heartbeat_timeout=timedelta(seconds=30)` in `workflows.py` — the activity heartbeats only on `status` events from the adapter. But the adapter's `stream_invoke` first does `await self.submit()` (POST SendMessage) which can take 10-40+ seconds depending on agent load, before yielding any event. During that window, **no heartbeat fires**, so Temporal kills the activity.
2. The adapter yields a `task_created` event after submit, but `invoke_agent_activity` didn't handle this event type — so no heartbeat on submit completion.

**Fix:**
1. In `workflows.py`: set `heartbeat_timeout=timedelta(seconds=agent_timeout + 30)` where `agent_timeout = int(agent_dict.get("timeout_seconds", 30))`. This gives the activity enough time for the full submit + poll cycle.
2. In `invoke_agent_activity`: add `activity.heartbeat({"phase": "submitted"})` on the `task_created` event — fires immediately after the POST completes.

**Watch for:**
- Any Temporal activity that calls a slow external HTTP endpoint before its first heartbeat: add an explicit heartbeat immediately after the call returns.
- Always set `heartbeat_timeout >= max possible time between external I/O calls`, not a fixed small value.
- On activity retry (RetryPolicy.maximum_attempts > 1), each attempt creates new DB rows (child_task, run_step) — orphaned "running" rows accumulate on timeout. Consider idempotency keys or cleanup on retry if this becomes a problem.

---

## 2026-07-11 — Temporal workflow cancel stores status="failed" not "canceled"

**Symptom:** User clicks Stop in the playground. Run appears canceled in the UI but DB shows `status=failed, error=Cancelled`.

**Root cause:** When Temporal cancels a workflow mid-activity, the Python SDK raises `ActivityError` (not `CancelledError` directly) — the cancellation is wrapped. The `except ActivityError` block set `run_status = "failed"` unconditionally, instead of inspecting whether the cause was a cancellation.

**Fix:** In `except ActivityError`, check `isinstance(exc.cause, CancelledError)` and set `run_status = "canceled"` in that case.

**Watch for:** When adding new except blocks in workflows, always check both `CancelledError` (direct) and `ActivityError` wrapping a `CancelledError` (mid-activity cancellation).

---

## 2026-07-11 — Trace tab empty: events on wrong Redis channel

**Symptom:** Playground trace tab shows no events even though runs complete successfully.

**Root cause:** Two Redis channels exist for each run:
- `them:dash:run:{run_id}` — dashboard pub/sub channel; `ws_dashboard` multiplexes to `run:{run_id}` channel subscribers
- `them:dash:run:{run_id}:tokens` — streaming side-channel; bridge's `stream_run_events` reads this and forwards to the WS client directly

`tool_start` and `tool_done` events were only published to `:tokens`. The dashboard WS (which feeds the trace tab) subscribes to the plain channel — so trace received nothing except `usage`/`run_start`/`run_end`.

**Fix:** Publish `tool_start`, `tool_done`, and `iteration_start` to BOTH channels: use `_publish_dash()` (which fans out to the plain channel) AND separately publish to `:tokens`.

**Watch for:** Any new event type added in activities must be published to both channels if it should appear in both the trace tab AND the streaming client view. `:tokens` = real-time stream to the connected WS client; plain channel = observable audit log for dashboard/trace.

---

## 2026-07-11 — Debate flow slow (264s) — three compounding root causes

**Symptom:** debate_flow run takes 264s and fails with "activity Heartbeat timeout" on iteration 3.

**Root causes (by impact):**

### 1. Context explosion from large tool results (biggest impact)
Iteration 2 plan call received 14,981 input tokens vs 1,038 for iteration 1. The 3 debate agents each returned ~4,000 tokens of detailed argument text. These were passed verbatim as `tool_result` blocks into the LLM context. The LLM had to process 13k tokens of new content just to compose the judge tool call.

**Fix:** JSON-aware compaction in `_compact_tool_result()` — keeps only routing fields (`_COMPACT_JSON_KEEP`: agent, main_point, confidence, approach, round, winner, etc.) from JSON tool results. Falls back to `_MAX_TOOL_RESULT_CHARS = 2000` char slice for non-JSON. Full results are persisted in DB/artifacts. `_slim_tool_use_inputs()` also strips heavy `arguments`/`opponent_arguments` fields from tool_use blocks stored in `self.messages`.

### 2. `plan_turn heartbeat_timeout=30s` too short for large contexts
When context reaches 15k+ tokens, TTFT (time-to-first-token) from Anthropic can exceed 30s. The activity heartbeats on every token — if the first token arrives after >30s, Temporal kills the activity.

**Fix:** Raised `heartbeat_timeout` for `plan_turn_activity` to 90s.

### 3. System prompt routing debate outputs through full argument text
Original system prompt instructed the orchestrator to forward complete agent arguments. R3 update changed it to route only summary fields (main_point, confidence, approach, round) and keep full arguments inside the A2A task artifacts.

**Architecture note:** Full argument text lives in `them.artifacts`. Orchestrator LLM context holds only routing fields. Agents receive only the prior round's summary, not full transcripts.

**Watch for:** Any orchestrator calling agents that produce large outputs (>2k chars each) will experience context growth proportional to `n_agents × output_size × n_rounds`. Always cap tool results in the LLM context; full content lives in artifacts.

---

## 2026-07-11 — tool_use/tool_result mismatch on resumed conversations (two root causes)

**Symptom:** After resuming a conversation (same context_id), next message fails with Anthropic API HTTP 400: "tool_use ids were found without tool_result blocks immediately after".

**Root cause A — tool_results never persisted:** The agentic loop persisted assistant turns (tool_use blocks) but never persisted the corresponding tool_result messages to `task_messages`. When the next turn loaded history via `_load_context_history`, it found assistant messages containing tool_use IDs with no matching tool_result rows → invalid conversation.

**Fix A:** Added `record_tool_results_activity` — a new Temporal activity that persists the tool_result message (role='user', content=[{type:tool_result, ...}]) to `task_messages` immediately after all agents complete and before the next iteration. This is idempotent (seq-keyed) and runs with retry_policy.maximum_attempts=3.

**Root cause B — _sanitize_history only checked tail:** Even with new runs, old DB rows from before the fix had orphaned tool_use IDs in the middle of history. `_sanitize_history` only stripped the tail (last assistant turn if dangling) — it missed orphaned pairs in earlier turns.

**Fix B:** Full-pass `_sanitize_history` — (1) collect all `tool_result` IDs from the entire history in one pass, (2) rebuild history dropping any assistant message whose tool_use IDs have no matching result anywhere in history, (3) drop the orphaned tool_result message immediately following any dropped assistant turn.

**Watch for:**
- Every assistant turn that includes tool_use blocks MUST be followed immediately by a user message containing one `tool_result` block per tool_use. This is an Anthropic API hard requirement.
- Always persist tool_results to DB before the next planning turn — if the bridge crashes between agents completing and the next LLM call, the DB must have enough data to reconstruct a valid history.
- `_sanitize_history` is a last-resort safety net; it should never need to drop rows in a healthy run.

---

## 2026-07-14 — Multi-replica deploy: always restart ALL bridge replicas

**Symptom:** One WS session works, the other is silent — but no error is logged on the bridge you're watching.

**Root cause:** Traefik load-balances WS connections across all running bridge replicas. When only one replica was restarted with new code, half of connections (routed by Traefik to the old replica) silently ran the old code — without the pre-subscribe fix or `DeadContextError` — and hung.

**Fix:** Always restart ALL replicas when deploying code changes:
```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile replica restart them-bridge them-bridge-2
```

**Watch for:** If parallel sessions behave differently (one works, one silent), check `docker logs them-bridge-2` — the affected session may be on a replica you didn't touch.

---

## 2026-07-14 — Ready-event race: subscribe to Redis BEFORE starting the workflow

**Symptom:** WS client hangs for 15 seconds then receives "Timed out waiting for workflow ready event", even though the workflow completed successfully.

**Root cause:** `stream_run_events` subscribed to the Redis context channel AFTER calling `start_workflow`. Fast workflows (echo agents) published their `ready` event before the subscriber was registered — the event was missed, the 15s timeout fired.

**Fix:** In `start_orchestration_workflow`, subscribe to the context channel BEFORE calling `client.start_workflow()`. Return the pre-subscribed `pubsub` object to the caller, who passes it to `stream_run_events(..., pubsub=pubsub)`. `stream_run_events` skips its own subscribe when a pre-subscribed pubsub is passed in.

**Watch for:** Any new code path that starts a Temporal workflow and then tries to listen for its Redis events must use the pre-subscribe pattern. Subscribe first, start second — never the reverse.

---

## 2026-07-14 — Poisoned context_id: dead workflow silently swallows all future messages

**Symptom:** User sends a message, gets an error. Subsequent messages on the same context are also silent — no response, no error.

**Root cause:** On `WorkflowAlreadyStartedError`, the old code re-attached to the existing workflow handle without checking if it was already closed. A closed workflow streams nothing and returns immediately — every subsequent message attached to the same dead workflow ID and silently completed with no output.

**Fix:** On `WorkflowAlreadyStartedError`, call `handle.describe()` and check the status. If the workflow is in a closed state (`COMPLETED`, `FAILED`, `CANCELED`, `TERMINATED`, `TIMED_OUT`), raise `DeadContextError`. The router catches it and emits `{"type": "error", "context_id": null}` — the `null` context_id is the protocol signal for the client to clear its stored context_id and start fresh.

**Watch for:** Any new entry point (WS, SSE, REST) that calls `start_orchestration_workflow` must catch `DeadContextError` and emit `context_id: null` to the client. Without this signal, the client's localStorage retains the dead context_id and every future session on that tab is poisoned.

---

## 2026-07-14 — Entry point save 500: diff keyed on id that the frontend lost

**Symptom:** Saving an existing application in the canvas builder returns 500 with `UniqueViolationError: duplicate key value violates unique constraint "entry_points_slug_key"`.

**Root cause:** `_apply_entry_point_diff` matched entry points by their database `id`. The frontend stored this id in `_epId` on each canvas node. If `_epId` was missing (lost due to any canvas interaction), the server treated the existing EP as a new insert, hit the slug unique constraint, and crashed.

**Fix:** Changed the diff to key on `slug` instead of `id`. Slug is already globally unique (DB constraint) and immutable by design (it's a URL — renaming is a breaking change). The server owns identity: existing slug → UPDATE, new slug → CREATE, missing slug → DELETE. Frontend never needs to track or send `id`.

**Watch for:** Slug is now the stable identity for entry points. If a user wants to "rename" a slug they must delete the old EP and create a new one — this is intentional, as renaming a slug breaks external clients pointing at the old URL.

---

## 2026-07-15 — `isinstance()` on ORM class fails for cache-hit proxy objects

**Symptom:** Phase 6 used `isinstance(orch, AppOrchestrator)` to detect app orchestrators and conditionally enforce scope. The check worked on the DB-miss path (fresh SQLAlchemy row) but silently returned `False` on cache hits, because `load_orchestrator_row` returns an `_OrchestratorProxy` dataclass on cache hits — not an ORM instance.

**Root cause:** The cache layer deserializes Redis JSON into a plain dataclass (`_OrchestratorProxy`). `isinstance(proxy, AppOrchestrator)` always returns `False` regardless of what the original DB row was. Two successive wrong fixes made things worse:
1. *First wrong fix:* Skipped scope enforcement entirely when `isinstance` returned `False` — no check at all on cache hits, a security hole.
2. *Second wrong fix:* Added a parent FK fallback (`str(getattr(orch, "orchestrator_id", None) or "")`) — still wrong because `orchestrator_id` isn't serialized into the cache dict, so cache-hit proxies returned empty string → `PermissionError` on every legitimate unscoped request.

**Fix:** Carry `"is_app_orchestrator": isinstance(row, AppOrchestrator)` in the cache dict, evaluated on the DB-miss path where `row` IS a real ORM object. Populate the `_OrchestratorProxy` field as `data.get("is_app_orchestrator", False)`. Consume at call sites as `orch.is_app_orchestrator` — a plain `bool` attribute, no `isinstance` anywhere outside the cache writer.

**Watch for:** Never use `isinstance(obj, OrmModel)` on objects returned from a cache-loading function. The cache layer always returns a proxy/dataclass, not an ORM row. If a type distinction must influence runtime behavior, carry a type-flag in the serialized cache dict and read it back as a plain attribute on the proxy. This rule applies to any future cache layer wrapping SQLAlchemy models (`Agent`, `AccessToken`, `EntryPoint`, etc.).

---

## 2026-07-15 — Expand/contract: drop fallback paths before dropping the column

**Symptom:** Phase 12 dropped `applications.orchestrator_id` and `orchestrators.a2a_exposed`. Several files still had fallback branches that referenced those columns at runtime: `apps.py` (REST/SSE/WS), `a2a_server.py` (legacy `Application.orchestrator_id → Orchestrator.name` resolution), `webrtc.py` (`_load_app` queried `Application.slug` — a column that never existed), and `loaders.py` (the `a2a_exposed == True` fallback query in `load_agents`). None of these caused an import error; they would all have blown up at request time.

**Root cause:** The expand/contract pattern (`add column → use both → drop old`) requires an explicit audit pass before the drop. The fallback code was written to be safe during the expand phase, but it was never removed going into the drop phase. The pre-migration fallback in `apps.py` masked the real bug in `webrtc.py`: `_load_app` was querying `Application.slug` which does not exist on the `Application` model — slug lives on `EntryPoint`. This would have raised `AttributeError` at query time, but was never exercised because `webrtc.py` is not covered by the live test suite.

**Fix:** Before dropping a column: `grep -rn "column_name" app/` across the whole codebase, not just the files you remember touching. Check every reference — some will be load-bearing code paths, others will be dead fallbacks.

**Watch for:** When you see `if row.old_column: ...` as a fallback in an expand/contract migration, treat it as a time bomb. It must be removed in the same PR that drops the column. Also: any loader that queries by a model attribute (e.g. `Application.slug`) is a lie until you've confirmed that attribute exists in `models.py`. grep for it before assuming.

Corollary: **always restart the bridge after a column drop**, even if the code change looks complete. The running Python process has the old ORM module in memory. Until restarted it generates queries for the dropped column and returns HTTP 500 on every affected endpoint.

---

## 2026-07-16 — omni-traefik ingested them-bridge labels → 503 on all A2A calls

**Symptom:** Test and Discover on any external A2A agent (e.g. `http://10.55.125.43:3000/a2a/codeagent/`) returned `503 - no available server`. The bridge could reach the host fine; the proxy on the other machine had the route but no healthy backend.

**Root cause:** `omni-traefik` (the reverse proxy on `10.55.125.43:3000`) had no `--providers.docker.constraints` set, so it ingested Traefik labels from **all** containers visible on the Docker socket — including `them-bridge` from this stack. `them-bridge` registers `/a2a/` at priority 100; OMNI's own A2A router was only priority 25. them-bridge won every route match and forwarded to its own (dead) backends → 503.

**Fix applied on OMNI side:**
- *Immediate:* raised OMNI's A2A router priority to 110 so it beats them-bridge even when both labels are visible.
- *Structural:* added `--providers.docker.constraints=!Label('traefik-instance','them')` to `omni-traefik` — it now ignores all `them-*` containers entirely.

**Watch for:** Any time two independent stacks share a Docker socket (same host), their Traefik instances will cross-contaminate unless both have `--providers.docker.constraints` set to filter out the other stack's labels. `them-traefik` already does this correctly — verify `omni-traefik` does too after any OMNI upgrade or redeploy.

**Corollary for this stack:** `them-traefik` is correctly isolated — verify by checking `traefik/traefik.yml` has a constraints filter that excludes OMNI labels. If `them-traefik` ever gets a socket mount without constraints, it will ingest OMNI's labels the same way.

---

## Canvas save: graph-centric model prevents "3 EPs → 3 orchs" duplication (2026-07-15)

**What burned us:** When 3 entry points all wired to the same orchestrator node were saved, the old EP-centric save code created a separate `app_orchestrators` row per EP. On reload it looked like 3 independent orchestrators. Root cause: upsert keyed by orchestrator *name* alone — multiple rows with the same name could coexist when the loop ran per-EP.

**Fix:** DB unique index `(application_id, node_id)` on `app_orchestrators`. Frontend sends the full `graph: {nodes, edges}` from React Flow state — the compiler walks edges once and upserts one AO row per orch node, regardless of how many EPs point to it.

**Corollary:** Canvas positions must be keyed by the React Flow **node id** that was actually used when saving. The load function (`buildNodesFromApp`) generates its own node ids (`ep_<slug>`, `orch_<ao_id>`, `agent_<agent_id>`). After a save, positions are stored under those ids — the load function must look them up by the same id it just generated, with legacy-prefix fallback for old saves.

**Watch for:** If you add a new node type to the canvas, make sure `handleSave` includes its id in `canvasLayout` and `buildNodesFromApp` uses `pos(nodeId, legacyKey)` to restore its position.

---

## 2026-07-16 — Live notifications that survive page navigation: Hash + Pub/Sub pattern

**Symptom:** Scan progress badge stuck on "Scanning…" after navigating away and back. Pub/Sub events fired while WS was closed are gone forever.

**Root cause:** Redis Pub/Sub is fire-and-forget. No replay. If the browser is not subscribed at the exact moment an event fires, it is lost permanently.

**The correct pattern — Hash (snapshot) + Pub/Sub (live):**

```
On every state update (backend):
  1. HSET them:<feature>:state:<id>  field value  ← update snapshot first
  2. PUBLISH them:dash:<channel>  {payload}        ← then publish live

On WS subscribe (server):
  1. Subscribe to Pub/Sub first                    ← no gap, events buffered
  2. HGETALL them:<feature>:state:<id>             ← read current snapshot
  3. Send snapshot to browser as one message       ← browser renders current state
  4. Forward live Pub/Sub events as they arrive    ← live updates from here

On scan complete:
  DEL them:<feature>:state:<id>  (or short TTL)
```

**Why subscribe to Pub/Sub BEFORE reading the Hash:**
If you read the Hash first, then subscribe — any event that fires in that gap is missed. Subscribe first means Pub/Sub is already buffering before you read the snapshot. No gap.

**Why update Hash BEFORE publishing:**
Pub/Sub is faster than a Hash read. If you publish first, a reconnecting client might read a stale Hash. Hash first guarantees snapshot is never behind live events.

**Frontend:**
- Handle a `snapshot` event type that sets state directly (not incremental)
- Use a version/sequence number so a delayed Pub/Sub event cannot overwrite newer state

**Apply this pattern anywhere you need:**
- Live progress that survives page navigation
- Any feature where the browser might connect after events already fired
- Any "current state" that a new subscriber needs immediately

**What NOT to do:**
- Do not use Pub/Sub alone for anything that needs replay
- Do not use Redis Streams for live UIs — they replay ALL history, causing stale messages to flash on screen
- Do not poll the DB for N items on reconnect — use the Hash snapshot via the existing WS subscribe flow
- Do not add complex reconnect logic on the frontend — the server handles catch-up, frontend just reconnects normally

**Watch for:** Any new real-time feature (run progress, agent status, task updates) — always ask: "can the user navigate away while this is running?" If yes, use Hash + Pub/Sub.

---

## 2026-07-18 — System `python3` is 3.6 — silently breaks the entire test runner

**Symptom:** Running `python scripts/tests/run_tests.py` produced `0 passed, 57+ failed` with every live test showing `(got '')`. No exception, no error message — just empty strings everywhere.
**Root cause:** The host OS is RHEL/CentOS 8 — system `python3` is Python 3.6. `subprocess.run(..., capture_output=True)` was added in Python 3.7. Every `docker()` helper call hit the `except Exception: return ""` fallback silently.
**Fix:** Always use `python3.12` (installed at `/usr/bin/python3.12`) — this is the same Python the app and containers run on. CLAUDE.md and INDEX.md both updated to say `python3.12`.
**Watch for:** Any script that runs `python` or `python3` directly. The system Python is 3.6 — it won't crash, it will silently degrade. The tell is everything returning `""`.

---

## 2026-07-18 — CI migration list must be kept in sync manually

**Symptom:** GitHub Actions CI was failing on live tests with schema errors even though everything passed locally. The `ci.yml` "Apply DB migrations" step was frozen at `006_phase11.sql` — 15 newer migrations (007–021) were never being applied in CI, so `them.canvas_layout`, `them.app_orchestrators`, `them.entry_points` etc. didn't exist on the CI schema.
**Root cause:** Migrations are added to `db/` incrementally but `ci.yml` is a static list that nobody updated for 15 consecutive migrations.
**Fix:** Expanded `ci.yml` to apply all 21 migrations in order (001–021). Going forward, every new `db/0NN_*.sql` file MUST also be added to both `ci.yml` and `CLAUDE.md` common DB commands.
**Watch for:** After adding any new migration file — immediately add it to `ci.yml` and `CLAUDE.md`. If CI fails with "column does not exist" or "table does not exist" on a live test, the first thing to check is whether all migrations are in `ci.yml`.

---

## 2026-07-18 — Stale migration breaks clean schema apply (entry_point_type constraint)

**Symptom:** CI live job failed on test_01 (schema check) with "column does not exist" errors on `them.canvas_layout` — seemingly unrelated tables. The real failure was earlier and silent: `005_phase10.sql` aborted its transaction, which caused psql to skip that migration without a clear error, leaving the schema partially applied and cascading failures downstream.
**Root cause:** `005_phase10.sql` tried to `ADD CONSTRAINT ... CHECK (entry_point_type IN ...)` on `them.applications`. That column was moved to `them.entry_points` by `010_app_entrypoints.sql`. On a live DB that already had `010` applied, the constraint was already gone. On a clean CI schema, the column never existed — so the ALTER TABLE failed, aborted the transaction, and the rest of that migration was skipped.
**Fix:** Neutralized `005_phase10.sql` to only `DROP CONSTRAINT IF EXISTS` (always a no-op). Principle: never ADD or reference a column that a later migration moves or drops — either update the earlier migration or make it a no-op.
**Watch for:** Any migration that does `ADD CONSTRAINT`, `ADD COLUMN`, or `ALTER COLUMN` on a table that a later migration restructures. On a live DB it looks fine (the column exists from the original schema); on a clean CI schema it fails because that migration runs before the column is added.

---

## 2026-07-18 — SCARD+SADD race and ghost EP set entries

**Symptom (potential):** Under a concurrent session cap, two simultaneous connections both read SCARD=N (below cap) before either SADDs, both proceed, cap is exceeded by 1. Under pod crashes, session_ids left in `them:ep:{slug}:sessions` permanently consume cap slots even though the session is long dead.

**Root cause (race):** A pipeline-based SCARD + SADD is NOT atomic across Redis replicas (or even concurrent connections to the same Redis). Two clients can both see the same count before either writes.

**Fix (race):** Use a server-side Lua script via `EVAL`. The script executes atomically on Redis: prunes ghost entries (session_ids whose `them:sess:{id}` hash no longer exists), checks the live count, then SADDs the new session_id — all in a single uninterruptible operation. Pipeline is wrong for this; only Lua EVAL or SETNX/WATCH/MULTI gives true atomicity.

**Fix (ghost entries):** The Lua gate script checks `EXISTS them:sess:{sid}` for every member before counting. If the session hash has expired (90s TTL, pod crash), the member is SREMed before the cap comparison. This ensures the SCARD reflects only genuinely live sessions.

**Watch for:** Any Redis pattern that does read-then-write where the correctness depends on the count being stable between the two operations. Prefer Lua EVAL for any check-and-modify pattern.

---

## 2026-07-18 — asyncio.wait result guard must check task result, not just task presence in done set

**Symptom:** Admin session terminate worked but so did Redis errors — any exception in the control listener caused the workflow to be cancelled and WS closed with 4000 "terminated by administrator".

**Root cause:** `asyncio.wait([stream_t, cancel_t, control_t])` returns a task in `done` regardless of whether it completed normally, raised, or returned `None`. The guard `if control_t in done:` was missing the result check — it fired on both normal termination signals AND on any Redis error that caused the coroutine to fall through with `None`.

**Fix:** Always check `task.result()` after checking task membership in `done`: `if control_t in done and control_t.result() == "admin_terminate":`. The control listener explicitly returns `"admin_terminate"` only when a real signal was received; exceptions swallowed by `except: pass` leave it returning `None`.

**Watch for:** Any pattern using `asyncio.wait` with tasks that have multiple exit paths — always check both `in done` AND the actual return value or exception.

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

## 2026-07-21 — Docker BuildKit cannot follow symlinks outside the build context

**Symptom:** `them-bridge` and `them-worker` Docker builds failed with `too many links` / `failed to read dockerfile`. theM_gateway/ contains symlinks to ../traefik, ../auth_service, etc.

**Root cause:** Docker BuildKit resolves the build context at the directory level and does NOT follow symlinks that point outside the build context root. A symlink `theM_gateway/traefik -> ../traefik` resolves to `/opt/docker/them/traefik` which is outside the `/opt/docker/them/theM_gateway/` build context.

**Fix:** Replace symlinks with actual copies: `cp -r ../traefik ./traefik`, `cp -r ../auth_service ./auth_service`. Use rsync for directories that need partial overrides: `rsync -av --ignore-existing ../app/ ./app/`.

**Watch for:** Never use symlinks inside the Docker build context that point outside it. This is a hard limitation of BuildKit — no flag overrides it.

---

## 2026-07-21 — Test suite uses user_id=99 which doesn't exist in fresh DB

**Symptom:** test_11 (WS orchestrate) failed: `[FAIL] Can create bearer token for WS auth` with `ForeignKeyViolationError` on `access_tokens.user_id`.

**Root cause:** Test 11 creates a bearer token with `user_id: 99`. On the Windows dev DB, user 99 happened to exist. On a fresh Linux install from seed_users.sql (only admin=1 and avi=2), user 99 does not exist. The FK `access_tokens_user_id_fkey` → `auth_service.users(id)` rejects the insert.

**Fix:** Changed test 11 to use `user_id: 1` (admin). General principle: tests that hardcode user IDs must use IDs guaranteed to exist in a fresh seeded DB (1 = admin, 2 = avi).

**Watch for:** Any test that hardcodes a `user_id` other than 1 or 2 — may fail on fresh installs.
