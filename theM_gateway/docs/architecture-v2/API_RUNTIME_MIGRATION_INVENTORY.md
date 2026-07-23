# the-M — API & Runtime Migration Inventory (Python → Go)

> **Purpose:** Complete, source-verified inventory of every route, streaming
> endpoint, Redis channel/stream, Temporal interaction, session/state
> mechanism, and frontend contract in the active **Python** bridge (`app/`),
> compared against what the **Go** bridge (`go/`) actually implements today.
> This document drives the Python→Go gateway migration. A missed route or
> Redis dependency here becomes a production incident.
>
> **Method:** Inventory was produced by reading source directly — not the
> existing docs. Where code and docs diverge, code wins (and the divergence is
> flagged).
>
> **Repo layout note:** The Python app is duplicated at `/opt/docker/them/app`
> (root) and `/opt/docker/them/theM_gateway/app` (byte-identical copy). The Go
> bridge lives only at `/opt/docker/them/go` (module `github.com/aviciot/them`,
> port **8002**). All Python line numbers below refer to `app/` at the repo
> root. Traefik/compose/db config paths follow the task spec under
> `theM_gateway/`.

---

## 0. Executive Summary

| Metric | Count |
|---|---|
| Python HTTP routes (admin + runs + media) | 45 |
| Python WebSocket endpoints | 4 (`/ws/orchestrate/{name}`, `/ws/dashboard`, `/apps/{slug}/ws`, plus reserved `workflow_advisor` via orchestrate) |
| Python SSE endpoints | 1 (`/apps/{slug}/sse`) |
| Python A2A endpoints | 3 (`/.well-known/agent-card.json`, `/a2a`, `/a2a/push/{task_id}`) |
| Python health endpoints | 3 |
| **Total Python surface** | **~56 endpoints** |
| Go HTTP/WS/SSE/A2A/admin routes registered today | 27 |
| Go routes reachable via Traefik today | **1** (`/go-health/*` only) |
| Redis pub/sub channels | 12 |
| Redis stream keys | 1 (`them:dash:run:{runID}:stream` — Go reads, **no live Python producer**) |
| Temporal workflows | 1 (`OrchestrationWorkflow`) |
| Python test cases (scripts/tests) | 35 + multi-turn + e2e |
| Go test cases (go/TEST_INDEX.md) | 255 |

**The single most important fact:** the Go bridge is a **greenfield
redesign**, not a drop-in replacement. Its routes use different paths, HTTP
verbs, key fields, and workflow-ID schemes than Python. Today **no user-facing
traffic reaches Go** — Traefik routes only `/go-health/*` to it. Everything
(`/ws`, `/sse`, `/api/v1`, `/apps`, `/a2a`) stays on Python `them-bridge:8001`.

### Top 3 migration risks

1. **Contract incompatibility between Python and Go (routes, verbs, IDs).**
   Go WS is `/ws/orchestrate/{app_slug}/{entry_point_slug}`; Python is
   `/ws/orchestrate/{name}` **and** `/apps/{slug}/ws`. Go SSE is
   `/sse/orchestrate/{…}`; Python is `/apps/{slug}/sse`. Go admin uses `PUT`
   for updates and keys orchestrators by `{name}`; Python uses `PATCH` and
   keys by `{id}`. Cutting Traefik over to Go without a compatibility shim
   **breaks the frontend and every existing bearer-token client immediately.**

2. **The two orchestration paths are mutually incompatible at the Temporal +
   Redis layer.** Python starts `OrchestrationWorkflow` with workflow ID
   `ctx-{context_id}` and streams run events over Redis **Pub/Sub** on
   `them:dash:run:{context_id}:ctx` → `them:dash:run:{run_id}:tokens`. Go starts
   the same workflow type with workflow ID = a fresh **run UUID** and expects
   events on `them:dash:run:{runID}:tokens` (Pub/Sub) or
   `them:dash:run:{runID}:stream` (Streams). **Go has no Temporal worker** — it
   dispatches to the *Python* worker. But the current Python worker
   (`activities.py`) **only publishes Pub/Sub `:tokens`/`:ctx` and never XADDs
   the `:stream` key.** So Go's `dual`/`streams` mode reads an empty stream
   against today's worker, and Go's workflow-ID scheme won't line up with the
   Python worker's `ctx-` convention or Pub/Sub channel derivation.

3. **Runtime governance is implemented in Go but NOT wired.** The gate
   (session caps, per-EP/app concurrency, queue), rate limiter, agent registry
   (A2A outbound), pod heartbeat, and admin session-disconnect all exist as Go
   packages but are never constructed/attached in `go/cmd/them/main.go`. If Go
   takes `/apps/*` traffic in its current state, **all concurrency caps,
   blocked-token/user checks, app rate limits, and admin "terminate session"
   silently stop enforcing.**

Secondary risks: Python validates JWT via an **HTTP call** to the auth service
(`POST /api/v1/auth/verify`), Go validates **locally via RS256** — different
trust model and failure modes. Python A2A supports `SendMessage/GetTask/
CancelTask` and drives the orchestrator **inline (no Temporal)**; Go A2A
supports only `message/send`. Several admin entities (tokens, llm_providers,
middleware, system_agents, monitoring_config, sessions) have **no Go routes at
all**.

---

## 1. Recommended Migration Waves

Ordering is driven by (a) statefulness, (b) shared Redis/Temporal seams, and
(c) frontend contract coupling.

| Wave | Scope | Rationale |
|---|---|---|
| **Wave 1 — Stateless reads + health** | `/health*`, admin **GET/list** for agents/orchestrators/applications/runs, `/apps` + `/apps/{slug}` (catalogue), agent-card. | No writes, no Redis pub/sub, no Temporal. Go already has most. Low blast radius. Requires only adding a compatibility layer for path/verb/field names. |
| **Wave 2 — Admin writes (CRUD)** | Create/Update/Delete for agents, orchestrators, applications, entry_points, tokens, llm_providers, middleware, system_agents, monitoring_config; runs delete/cancel/bulk-delete. | Writes touch Postgres + **cache-invalidation pub/sub** (`them:agents:changed`, `them:orchestrators:changed`, `them:ep:config:changed`, `them:mw:chain:*` DEL). Both Python and Go must dual-subscribe during transition so a write on one invalidates the other's cache. Go must add the 6 missing entities + switch verbs to PATCH to match the frontend. |
| **Wave 3 — Runtime governance + sessions** | `runtime_gate` (rate limit, blocked token/user, EP/app caps, queue), `session_manager` (register/end/touch, EP/app index sets, pod heartbeat), admin sessions list + disconnect (`them:sess:control:*`). | Wire the already-written Go `gate`, `ratelimit`, `session` packages. Must be done **before** Go takes any `/apps/*` or `/ws/orchestrate/*` traffic or caps stop enforcing. Shares `them:sess:*`, `them:ep:*`, `them:app:*`, `rl:them:*` keys with Python — safe to run concurrently once Go writes the same key formats. |
| **Wave 4 — Live orchestration streaming (WS/SSE/A2A)** | `/ws/orchestrate/*`, `/apps/{slug}/ws`, `/apps/{slug}/sse`, `/a2a`, run-event streaming, HITL signal, dashboard WS. | Highest risk. Requires reconciling the workflow-ID scheme (`ctx-{context_id}` vs run UUID), the run-event transport (Pub/Sub `:tokens`/`:ctx` vs Streams `:stream`), a Go Temporal **worker** (or a shared worker contract), and A2A method-name parity. Dashboard WS (`them:dash:*` subscriber) migrates last since it consumes every other component's pub/sub output. |

---

## 2. Python HTTP Routes — Admin (all `require_admin` JWT)

Registered in `app/main.py:200-208` with `prefix="/api/v1"` and
`dependencies=[Depends(require_admin)]`. `require_admin` (`app/_deps.py:47`) =
`require_jwt` (validates via auth-service HTTP `POST /api/v1/auth/verify`) +
role ∈ {`admin`,`superadmin`,`super_admin`}. **No Temporal calls in any admin
route.** Cache-invalidation helpers are noted per group.

### 2.1 agents — base `/api/v1/admin/agents` (`admin_agents.py`)

| Method + Path | Handler:line | Request | Response | DB | Redis (invalidation) | Go target | Diff | Wave |
|---|---|---|---|---|---|---|---|---|
| GET `/api/v1/admin/agents` | `list_agents`:209 | q:`enabled_only` | `List[AgentOut]` | agents | — | yes (exists) | Go path identical | 1 |
| POST `/api/v1/admin/agents` | `create_agent`:221 | `AgentCreate` | `AgentOut` 201 | agents, config | DEL `them:agents:registry` + PUB `them:agents:changed` | yes (exists) | — | 2 |
| GET `/api/v1/admin/agents/{id}` | `get_agent`:275 | path uuid | `AgentOut` | agents | — | yes | — | 1 |
| PATCH `/api/v1/admin/agents/{id}` | `update_agent`:280 | `AgentUpdate` | `AgentOut` | agents | DEL `them:agents:registry` + PUB `them:agents:changed` | partial | **Go uses PUT, not PATCH** | 2 |
| POST `/api/v1/admin/agents/{id}/test` | `test_agent`:334 | — | `{ok,latency_ms}` | agents | — | no (not in Go) | external card probe | 2 |
| POST `/api/v1/admin/agents/discover` | `discover_agent`:370 | `DiscoverRequest` | `DiscoverResult` | agents, config | — | no | external HTTP | 2 |
| DELETE `/api/v1/admin/agents/{id}` | `delete_agent`:477 | — | 204 | agents | DEL `them:agents:registry` + PUB `them:agents:changed` | partial | Go = soft delete (`enabled=false`); Python = hard DELETE | 2 |
| POST `/api/v1/admin/agents/{id}/security-scan` | `security_scan`:496 | — | `ScanResponse` 202 | agents (bg UPDATE) | bg → PUB `them:dash:agent:{id}`, HSET `them:scan:state:{id}` | no | spawns `asyncio` bg job (not Temporal) | 2/3 |

### 2.2 orchestrators — base `/api/v1/admin/orchestrators` (`admin_orchestrators.py`)

Invalidation `_invalidate(name)` :192 → DEL `them:orch:tmpl:{name}` + `them:orch:loc:{name}`, PUB `them:orchestrators:changed`.

| Method + Path | Handler:line | Request | Response | DB | Redis | Go target | Diff | Wave |
|---|---|---|---|---|---|---|---|---|
| GET `/api/v1/admin/orchestrators` | `list_orchestrators`:211 | q:`enabled_only` | `List[OrchestratorOut]` | orchestrators | — | yes | Go returns `allowed_agents=[]` always (not from DB) | 1 |
| POST `/api/v1/admin/orchestrators` | `create_orchestrator`:220 | `OrchestratorCreate` | 201 | orchestrators | `_invalidate` | yes | — | 2 |
| GET `/api/v1/admin/orchestrators/{orch_id}` | `get_orchestrator`:265 | path uuid | `OrchestratorOut` | orchestrators | — | partial | **Go keys by `{name}`, Python by `{id}`** | 1 |
| PATCH `/api/v1/admin/orchestrators/{orch_id}` | `update_orchestrator`:270 | `OrchestratorUpdate` | `OrchestratorOut` | orchestrators | `_invalidate` | partial | Go PUT + `{name}` | 2 |
| DELETE `/api/v1/admin/orchestrators/{orch_id}` | `delete_orchestrator`:339 | — | 204 | orchestrators | `_invalidate` | partial | Go soft delete + `{name}` | 2 |
| POST `/api/v1/admin/orchestrators/{orch_id}/test-llm` | `test_llm`:349 | `LLMTestRequest` | `LLMTestResult` | orchestrators | — | no | external LLM probe | 2 |
| POST `/api/v1/admin/orchestrators/{orch_id}/test-voice` | `test_voice`:373 | `VoiceTestRequest` | `LLMTestResult` | orchestrators | — | no | external STT | 2 |
| POST `/api/v1/admin/orchestrators/{orch_id}/test-tts` | `test_tts`:407 | `TTSTestRequest` | `LLMTestResult` | orchestrators | — | no | external TTS | 2 |

### 2.3 tokens — base `/api/v1/admin/tokens` (`admin_tokens.py`) — **MISSING IN GO**

Invalidation `invalidate_token(hash)` (`token_cache.py:183`) → L1 delete + DEL `them:session:token:{sha256}`.

| Method + Path | Handler:line | Request | Response | DB | Redis | Go | Wave |
|---|---|---|---|---|---|---|---|
| GET `/api/v1/admin/tokens` | `list_tokens`:98 | q:`user_id?` | `List[TokenOut]` | access_tokens | — | **no** | 2 |
| POST `/api/v1/admin/tokens` | `create_token`:110 | `TokenCreate` | `TokenCreatedOut` 201 (plaintext once) | orchestrators, access_tokens | — | **no** | 2 |
| GET `/api/v1/admin/tokens/{id}` | `get_token`:142 | path uuid | `TokenOut` | access_tokens | — | **no** | 2 |
| PATCH `/api/v1/admin/tokens/{id}` | `update_token`:147 | `TokenUpdate` | `TokenOut` | access_tokens | `invalidate_token` | **no** | 2 |
| DELETE `/api/v1/admin/tokens/{id}` | `delete_token`:171 | — | 204 | access_tokens | `invalidate_token` | **no** | 2 |

> Go has token *validation* (`auth.Cache`) and a `Revoke()` method, but **no admin
> CRUD route and no revoke HTTP endpoint**. Full parity requires new Go routes.

### 2.4 llm-providers — base `/api/v1/admin/llm-providers` (`admin_llm_providers.py`) — **MISSING IN GO**

| Method + Path | Handler:line | DB | Notes | Wave |
|---|---|---|---|---|
| GET `/api/v1/admin/llm-providers` | `list_providers`:113 | llm_providers | — | 2 |
| POST `/api/v1/admin/llm-providers` | `create_provider`:119 | llm_providers | — | 2 |
| GET `/api/v1/admin/llm-providers/{id}` | `get_provider`:142 | llm_providers | — | 2 |
| PATCH `/api/v1/admin/llm-providers/{id}` | `update_provider`:147 | llm_providers | no cache invalidation | 2 |
| DELETE `/api/v1/admin/llm-providers/{id}` | `delete_provider`:174 | llm_providers | — | 2 |
| GET `/api/v1/admin/llm-providers/routing/config` | `get_routing`:186 | config[`llm_routing`] | — | 2 |
| PUT `/api/v1/admin/llm-providers/routing/config` | `set_routing`:194 | config[`llm_routing`] | — | 2 |

### 2.5 applications — base `/api/v1/admin/applications` (`admin_applications.py`)

Invalidation: `_flush_orch_caches(app_id, names)`:945 → DEL `them:app:{id}:orch:{name}`, `them:orch:loc:{name}`, `them:agents:registry` (direct DEL, **no pub/sub**). `_flush_mw_chain_cache(app_id)`:927 → SCAN+DEL `them:mw:chain:{app_id}:*`. Graph compile/export lives in `app/services/app_compiler.py`.

| Method + Path | Handler:line | Request | DB | Redis | Go target | Diff | Wave |
|---|---|---|---|---|---|---|---|
| POST `.../bulk-delete` | `bulk_delete_applications`:617 | `BulkDeleteAppsIn` (≤200) | applications (cascade EP/AO/MW) | `_flush_orch_caches` | no | Go has no bulk-delete | 2 |
| GET `.../applications` | `list_applications`:647 | q:`enabled_only` | applications,entry_points,app_orchestrators,middleware_wirings | — | yes | — | 1 |
| POST `.../applications` | `create_application`:667 | `ApplicationCreate` (graph or legacy EPs) | applications,entry_points,app_orchestrators | `_flush_orch_caches` | partial | Python compiles a **graph**; Go creates plain rows | 2 |
| GET `.../applications/{id}` | `get_application`:713 | uuid | applications(+children) | — | yes | frontend `getAppRuntime` needs embedded `runtime_config` | 1 |
| PATCH `.../applications/{id}` | `update_application`:719 | `ApplicationUpdate` | applications,entry_points,app_orchestrators | `_flush_orch_caches` | partial | Go PUT; Go publishes `them:ep:config:changed` | 2 |
| DELETE `.../applications/{id}` | `delete_application`:768 | — | applications (cascade) | `_flush_orch_caches` | partial | Go soft delete | 2 |
| PUT `.../applications/{id}/runtime` | `put_app_runtime`:779 | `AppRuntimeConfig` | applications.runtime_config | `_flush_orch_caches` | no | drives runtime gate; needed with Wave 3 | 2/3 |
| GET `.../applications/{id}/export` | `export_application`:806 | — | applications(+children) | — | no | graph export | 2 |
| POST `.../applications/import` | `import_application`:827 | `ApplicationExport` | applications,entry_points,app_orchestrators | `_flush_orch_caches` | no | — | 2 |
| PUT `.../applications/{id}/restore` | `restore_application`:866 | `ApplicationExport` | applications,entry_points,app_orchestrators | `_flush_orch_caches` | no | — | 2 |
| PUT `.../applications/{id}/middleware-wirings` | `put_middleware_wirings`:966 | `MiddlewareWiringsBody` | applications, middleware_wirings | `_flush_mw_chain_cache` (SCAN+DEL `them:mw:chain:{app_id}:*`) | no | — | 2 |
| POST `.../applications/{id}/orchestrators/{ao_id}/test-llm` | `test_app_orch_llm`:1014 | `_AOTestRequest` | app_orchestrators | — | no | external | 2 |
| POST `.../{id}/orchestrators/{ao_id}/test-voice` | `test_app_orch_voice`:1047 | `_AOVoiceTestRequest` | app_orchestrators | — | no | external | 2 |
| POST `.../{id}/orchestrators/{ao_id}/test-tts` | `test_app_orch_tts`:1088 | `_AOTTSTestRequest` | app_orchestrators | — | no | external | 2 |

> **Go entry-point CRUD divergence:** Go exposes standalone EP routes
> `POST/PUT/DELETE /api/v1/admin/applications/{id}/entry-points[/{ep_id}]`
> (`applications.go:91-93`). Python has **no** standalone EP endpoints — EPs are
> managed through the application graph payload. This is a structural mismatch
> the frontend does not use today.

### 2.6 middleware-defs — base `/api/v1/admin/middleware-defs` (`admin_middleware.py`) — **MISSING IN GO**

Invalidation `_flush_chain_cache()`:57 → SCAN+DEL `them:mw:chain:*` (all apps).

| Method + Path | Handler:line | DB | Redis | Wave |
|---|---|---|---|---|
| GET `.../middleware-defs` | `list_defs`:74 | middleware_defs | — | 2 |
| GET `.../middleware-defs/{id}` | `get_def`:80 | middleware_defs | — | 2 |
| POST `.../middleware-defs` | `create_def`:85 | middleware_defs | SCAN+DEL `them:mw:chain:*` | 2 |
| PATCH `.../middleware-defs/{id}` | `update_def`:106 | middleware_defs | SCAN+DEL `them:mw:chain:*` | 2 |
| DELETE `.../middleware-defs/{id}` | `delete_def`:124 | middleware_defs | SCAN+DEL `them:mw:chain:*` | 2 |

### 2.7 system-agents — base `/api/v1/admin/system-agents` (`admin_system_agents.py`) — **MISSING IN GO**

| Method + Path | Handler:line | DB | Wave |
|---|---|---|---|
| GET `.../system-agents` | `get_system_agents`:123 | config[`system_agents`] | 2 |
| PUT `.../system-agents` | `put_system_agents`:129 | config[`system_agents`] | 2 |
| POST `.../system-agents/{role}/test-llm` | `test_role_llm`:166 | config[`system_agents`] | 2 |

### 2.8 sessions — base `/api/v1/admin/sessions` (`admin_sessions.py`) — **MISSING IN GO** (Redis-only, no DB)

| Method + Path | Handler:line | Redis | Wave |
|---|---|---|---|
| GET `.../sessions` | `list_sessions`:22 | SMEMBERS `them:app:{id}:sessions` OR `them:ep:{slug}:sessions`; per-session HGETALL `them:sess:{id}` | 3 |
| POST `.../sessions/{id}/disconnect` | `disconnect_session`:54 | HGETALL `them:sess:{id}` then PUB `them:sess:control:{id}` `"disconnect"` | 3 |

### 2.9 monitoring-config — base `/api/v1/admin/monitoring-config` (`admin_monitoring_config.py`) — **MISSING IN GO**

| Method + Path | Handler:line | DB | Wave |
|---|---|---|---|
| GET `.../monitoring-config` | `get_monitoring_config`:61 | config[`monitoring`] | 2 |
| PUT `.../monitoring-config` | `put_monitoring_config`:69 | config[`monitoring`] | 2 |

---

## 3. Python HTTP Routes — Runs (`/api/v1/runs`, `runs.py`)

Registered `main.py:211` prefix `/api/v1`; router prefix `/runs`. All routes:
`require_jwt`. Non-admins scoped to `Run.user_id == user_id`; admin =
role ∈ {admin,superadmin,super_admin}.

| Method + Path | Handler:line | Auth | DB | Temporal | Go target | Diff | Wave |
|---|---|---|---|---|---|---|---|
| GET `/api/v1/runs` | `list_runs`:132 | jwt (own/admin) | runs | — | partial | Go `?context_id=` filter; Go = **jwt only, no super_admin/ownership** | 1 |
| GET `/api/v1/runs/stats` | `run_stats`:157 | jwt (own/admin) | runs | — | no | — | 1 |
| GET `/api/v1/runs/contexts` | `list_contexts`:188 | jwt (own/admin) | tasks, runs, task_messages | — | no | — | 1 |
| POST `/api/v1/runs/bulk-delete` | `bulk_delete_runs`:268 | jwt **admin only** | runs (DELETE ≤500) | — | no | — | 2 |
| GET `/api/v1/runs/{id}` | `get_run`:284 | jwt (own/admin) | runs, run_steps, run_usage | — | partial | Go jwt only | 1 |
| DELETE `/api/v1/runs/{id}` | `delete_run`:317 | jwt **admin only** | runs (cascade) | — | no | — | 2 |
| PATCH `/api/v1/runs/{id}/cancel` | `cancel_run`:335 | jwt (own/admin) | runs (UPDATE) | — | no | — | 2 |
| POST `/api/v1/runs/{id}/signal` | `signal_run`:370 | jwt (own/admin); 501 if no Temporal; 409 if not running | tasks (find root ctx) | **signal** `OrchestrationWorkflow.submit_human_response` to `ctx-{context_id}` via **direct client** (`get_workflow_handle_for`, :406-416) — bypasses bridge_client | partial | Go signal name = **`human_input`**, target = **run UUID**; Python signal name = **`submit_human_response`**, target = **`ctx-{context_id}`**. Incompatible. | 4 |
| GET `/api/v1/runs/{id}/tasks` | `get_run_tasks`:479 | jwt (own/admin) | tasks | — | no | — | 1 |
| GET `/api/v1/runs/{id}/artifacts` | `get_run_artifacts`:496 | jwt (own/admin) | artifacts, tasks | — | no | — | 1 |
| GET `/api/v1/runs/context/{context_id}/artifacts` | `get_context_artifacts`:514 | jwt (no ownership filter) | artifacts | — | no | — | 1 |

**Redis:** none in `runs.py`.

---

## 4. Python HTTP Routes — Media (`transcription.py`, `tts.py`, `webrtc.py`)

| Method + Path | Handler:line | Auth | Request | Response | DB | External | Go | Wave |
|---|---|---|---|---|---|---|---|---|
| POST `/api/v1/orchestrators/{name}/transcribe` | `transcription.transcribe_audio`:41 | `require_jwt` | multipart `audio` | `{text,provider,model}` | orchestrators (voice cfg) | OpenAI or Groq STT | no | 4 |
| POST `/api/v1/orchestrators/{name}/tts` | `tts.text_to_speech`:53 | `require_jwt` | `TTSRequest{text}` | `StreamingResponse audio/mpeg` | orchestrators (tts cfg) | OpenAI TTS (`tts-1`, mp3) | no | 4 |
| GET `/apps/{slug}/webrtc/token` | `webrtc.webrtc_token`:94 | public EP → anon; else bearer/admin-JWT | q:`context_id?` | `{token,url,room,context_id}` | entry_points⋈applications | LiveKit `AccessToken` mint | no | 4 |
| POST `/apps/{slug}/voice/transcribe` | `apps.voice_transcribe`:763 | bearer unless public; requires voice EP | multipart `audio` | `{text,...}` | EP/app/AO STT cfg | voice_service | no | 4 |
| POST `/apps/{slug}/voice/tts` | `apps.voice_tts`:813 | bearer unless public; voice EP | `{text}` | `StreamingResponse audio/mpeg` | EP/app/AO TTS cfg | voice_service | no | 4 |

> The `/api/them` frontend proxy special-cases multipart requests and `audio/*`
> streaming responses; TTS and transcribe depend on this proxy behavior.

---

## 5. WebSocket Endpoints

### 5.1 `/ws/orchestrate/{name}` (`ws_orchestrator.py:77`) — no prefix

- **Auth** (`_parse_bearer`:44 / `_resolve_auth`:51): `Authorization: Bearer` header **or `?token=` query param**. Opaque bearer (`validate_bearer_token`, `them.access_tokens`) OR admin/super_admin JWT (`validate_jwt` via auth-service HTTP). No token → close 4001.
- **Edge guard** (:120): orchestrator's `edges` column must contain `"websocket"` else close 4003.
- **First client message:** `{"content": str, "context_id"?: uuid}` (:136). Later: `{"type":"cancel"}` (:246).
- **Server → client event types:** `ready` (carries run_id/task_id), `token`, `tool_start`, `tool_done`, `done`, `error`, `canceled` (:316). (Emitted by the Temporal workflow via Redis, relayed by `WebsocketEdge.emit`.)
- **DB:** `orchestrators` (SELECT edges/history_window/rate_limit_rpm :107); `access_tokens` via validation.
- **Redis:** `runtime_gate` (rate limit only, ep_slug=None): INCR `rl:them:{user_id}:{hour_slot}` (TTL 7200). `session_register/end`: HSET/EXPIRE `them:sess:{session_id}` TTL 90s. **SUB `them:sess:control:{session_id}`** (`_control_listener`:267, admin terminate). Via `bridge_client`: **SUB `them:dash:run:{context_id}:ctx`** (pre-subscribed :80) → **SUB `them:dash:run:{run_id}:tokens`** (:227).
- **Temporal:** always on (`_TEMPORAL_ENABLED=True` :37). `start_orchestration_workflow` → `OrchestrationWorkflow.run`, id `ctx-{context_id}` (:216). `stream_run_events` (:280). `cancel_workflow` on admin-terminate (:306) / client cancel (:314).
- **Go target:** partial. Go path is `/ws/orchestrate/{app_slug}/{entry_point_slug}` — **different path shape**; no plain `{name}` route. **Wave 4.**

### 5.2 `/apps/{slug}/ws` (`apps.py:502`) — no prefix

- **Auth** (`_resolve_bearer_ws`:94): public EP → synthetic `user_id=0`; else bearer/admin-JWT. Orchestrator scope enforced vs bound orch. Rejects `a2a` EP type.
- **First message** `{content, context_id?}` (:551); later `{"type":"cancel"}`.
- **Event types:** `ready`,`token`,`tool_start`,`tool_done`,`done`,`error`,`waiting` (queue-full :606).
- **DB:** entry_points⋈applications⋈app_orchestrators; tasks (budget); session tables.
- **Redis — full runtime gate** (`runtime_gate`:591): INCR `rl:them:app:{app_id}:{hour}`, SCARD `them:app:{app_id}:sessions` (soft app cap), **Lua EVAL on `them:ep:{slug}:sessions`** (atomic prune+cap+SADD), INCR `rl:them:{user_id}:{hour}`. `ep_gate_try` retry loop on queue (:610). `session_register/end`: HSET `them:sess:{id}`, SADD/SREM `them:ep:{slug}:sessions` + `them:app:{app_id}:sessions`, PUB `them:dash:sessions:{app_id}` + HSET `them:dash:sessions:state:{app_id}`. **SUB `them:sess:control:{session_id}`** (:673). Via bridge: **SUB `them:dash:run:{context_id}:ctx`** + **`them:dash:run:{run_id}:tokens`** (:685-693).
- **Temporal:** `start_orchestration_workflow` (entry_point_slug=slug, :636) + `stream_run_events` + `cancel_workflow`.
- **Go target:** partial (Go `/ws/orchestrate/{app_slug}/{entry_point_slug}` is the nearest analog but the gate is NOT wired). **Wave 4** (gate wiring is Wave 3).

### 5.3 `/ws/dashboard` (`ws_dashboard.py:159`) — no prefix

- **Auth:** JWT only (`?token=` or header), any valid user (no role gate). Close 4001 on invalid.
- **First message:** `{"type":"subscribe","channels":[...]}` (10s timeout). Valid channels (`_is_valid_channel`:39): `runs`, `agents`, `metrics`, `apps`, `run:<uuid>`, `agent:<id>`, `sessions:<app_id>`.
- **Server → client:** `subscribed`, `error`, `ping` (30s), and relayed events `{"channel":name,"event":{...}}`. Snapshots: agent scan-state, `session_snapshot`, `app_status`.
- **Redis — SUB only:** **SUB `them:dash:{channel}`** for each subscribed channel (:77). Reads HGETALL `them:dash:sessions:state:{app_id}`, HGETALL `them:scan:state:{agent_id}`, GET `them:dash:app_status_cache`.
- **Temporal:** none.
- **Go target:** no (Go has no dashboard WS). This is the **fan-in consumer** of every other component's pub/sub. **Wave 4 (last).**

### 5.4 Reserved: `/ws/orchestrate/workflow_advisor`

Same endpoint as 5.1; the frontend Applications page connects here for the AI
workflow advisor (`frontend/.../applications/page.tsx:2954`). No special
server code — it's a normal orchestrator named `workflow_advisor`.

---

## 6. SSE Endpoint

### `/apps/{slug}/sse` (`apps.py:408`, GET) — no prefix

- **Auth:** bearer unless public; rejects `a2a`; orch scope check.
- **Request:** query `message` (required), `context_id?`.
- **Response:** `StreamingResponse text/event-stream` via `SSEEdge` (`app/edges/sse_edge.py`). `token` events → bare `data:` frames; other types (`ready`/`tool_start`/`tool_done`/`done`/`error`) → `event: <type>\ndata: <json>`; terminal `event: done\ndata: {}` sentinel.
- **Redis:** via bridge SUB `them:dash:run:{context_id}:ctx` → `them:dash:run:{run_id}:tokens` (:463-468).
- **Temporal:** `start_orchestration_workflow` + `stream_run_events` (:454-463).
- **Go target:** partial — Go SSE is `/sse/orchestrate/{app_slug}/{entry_point_slug}` (GET **and POST**), **different path**, emits `token`/`tool_call`/`done`/`error`/`replay_unavailable` (no `tool_result`), supports `Last-Event-ID` resume. **Wave 4.**

---

## 7. A2A Endpoints (`a2a_server.py`) — no prefix

| Method + Path | Handler:line | Auth | Methods / body | DB | Temporal | Go |
|---|---|---|---|---|---|---|
| GET `/.well-known/agent-card.json` | `agent_card`:96 | public | — | entry_points⋈applications⋈app_orchestrators | — | Go = `/.well-known/agent.json` (**different filename**), static card |
| POST `/a2a` | `a2a_rpc`:553 → `_dispatch_single`:612 | `_resolve_bearer` (opaque or admin-JWT); **rate limit 10 rpm**; body ≤512KB; batch ≤10 | JSON-RPC 2.0 methods **`SendMessage`** (:622), **`GetTask`** (:624), **`CancelTask`** (:626) | entry_points, app_orchestrators, orchestrators, tasks, artifacts | **NONE** — runs orchestrator **inline** via `task_runner_run` (`_run_and_finalize`:267), sync or detached (`returnImmediately`) | Go = `POST /a2a/{app_slug}`, only **`message/send`**, no auth, inline |
| POST `/a2a/push/{task_id}` | `a2a_push`:649 | `_resolve_bearer`; ownership `owns_task` | raw A2A Task body | tasks, artifacts | — | no |

> **Divergences:** (a) method names — Python CamelCase `SendMessage/GetTask/
> CancelTask` vs Go slash-form `message/send` only; (b) agent-card filename
> (`agent-card.json` vs `agent.json`); (c) A2A path shape (`/a2a` vs
> `/a2a/{app_slug}`); (d) auth (Python requires bearer + 10rpm; Go = none);
> (e) Python A2A **does not use Temporal**, Go A2A also inline. **Wave 4.**

---

## 8. Health Endpoints

| Method + Path | Handler:line | Python behavior | Go equivalent |
|---|---|---|---|
| GET `/health` | `health`:13 | DB `SELECT 1` + Redis PING → `{status,db,redis,redis_db,instance_id}` | **not present in Go** (Go has only `/health/live` + `/health/ready`). Frontend `themApi.health()` hits `/health` via `/api/bridge` rewrite (no `/api/v1`, no auth). |
| GET `/health/ready` | `health_ready`:42 | DB SELECT 1 → 200/503 | `health.Ready` (`health.go:63`) — pings PG+Redis, 2s timeout |
| GET `/health/live` | `health_live`:54 | `{status,instance_id}` | `health.Live` (`health.go:53`) |

---

## 9. Redis Pub/Sub — Channel-by-Channel Map

Direction is relative to the component. **This map drives migration order:** a
channel can only be eliminated once *both* its publisher and every subscriber
have moved to the same runtime.

| Channel | Publisher (file:line) | Subscriber (file:line) | Purpose | Migration handling |
|---|---|---|---|---|
| `them:dash:run:{context_id}:ctx` | worker `activities.py:62,345` (`_publish_dash` echo + `init_run` ready) | bridge_client.py:80 (pre-sub), :191 | Bootstrap: deliver `ready` before run_id known | Move with orchestration (Wave 4). Go derives channel from run UUID instead — **reconcile ID scheme first.** |
| `them:dash:run:{run_id}:tokens` | worker `activities.py:349,398,485,503,512,599,652,667,749` | bridge_client.py:227; **Go `runstream/stream.go:124`** | Streamed token/tool/terminal events → WS/SSE | **Shared Python↔Go seam.** Dual-consumed during transition. Eliminate only after all edges are Go. |
| `them:dash:run:{run_id}` | task_runner.py:59 / activities.py:59 | ws_dashboard.py (`run:<uuid>`) | Per-run dashboard trace fan-out | Move with dashboard (Wave 4). |
| `them:dash:runs` | task_runner.py:61 / activities.py:64 | ws_dashboard.py (`runs`) | Run summary feed | Wave 4. |
| `them:dash:agents` | (reserved) `dashboard_broadcaster.py:99` | ws_dashboard.py (`agents`) | Agents-changed notify | Wave 2/4. |
| `them:dash:metrics` | (reserved) | ws_dashboard.py (`metrics`) | Metrics feed | Wave 4. |
| `them:dash:apps` | main.py `_app_liveness_loop` (30s) → `publish_app_status`:200 | ws_dashboard.py (`apps`) | App liveness/status | Wave 4 (needs Go liveness loop). |
| `them:dash:agent:{agent_id}` | admin_agents `_run_scan_job` → broadcaster :105/111/126/138 | ws_dashboard.py (`agent:<id>`) | Security-scan progress | Wave 2/4. |
| `them:dash:sessions:{app_id}` | dashboard_broadcaster.py:175 (via session_manager register/end/active) | ws_dashboard.py (`sessions:<app_id>`) | Session start/end/update | Wave 3/4. |
| `them:sess:control:{session_id}` | runtime_manager.py:238 (`signal_disconnect`) | ws_orchestrator.py:267, apps.py:673 (`_control_listener`) | Admin force-disconnect (close WS 4000) | **Wave 3.** Go has `session.SignalDisconnect/SubscribeControl` but **not wired**. |
| `them:agents:changed` | admin_agents (`invalidate_registry` `agent_registry.py:127`) | agent_registry.py listener | Agent-registry cache bust | **Wave 2 — dual-subscribe.** Go uses `them:agents:invalidate` (**different name**) in the (unwired) `agentregistry` pkg. |
| `them:orchestrators:changed` | admin_orchestrators.py:192 | (reserved) | Orchestrator cache bust | Wave 2. |
| `them:tasks:{task_id}:events` | task_store.py | ws_orchestrator subscribers | A2A/task event feed | Wave 4. |
| `them:token:revoked` | **Go** `auth/token_cache.go:195` | Go `:211` (started `main.go:140`) | Cross-pod bearer L1 eviction (Go-only) | Go-native. Python uses direct DEL `them:session:token:*` + L1 (no pub/sub) — **dual-invalidation gap** during transition. |
| `them:ep:config:changed` | **Go** admin `applications.go:128,149` | Go `epLoader.Subscribe` (`main.go:176`) | EP-config cache invalidation (Go-only) | Go-native. Python `admin_applications` uses direct DEL — **cross-runtime EP cache staleness** if both serve admin writes. |

### Channels that can be **eliminated** after full Go migration
`them:dash:run:{run_id}:tokens` (once Streams `:stream` is the sole transport
and dashboard is Go), and the `ctx` bootstrap channel if Go adopts a single
run-ID scheme. All `them:dash:*` fan-out channels survive (dashboard needs
them) but their publisher/subscriber both become Go.

### Channels needing **dual-publish / dual-subscribe** during transition
- `them:agents:changed` (Python) vs `them:agents:invalidate` (Go) — **name
  mismatch**; pick one canonical name and have both runtimes pub+sub it.
- `them:dash:run:{run_id}:tokens` — Python worker publishes, both Python
  bridge and Go bridge subscribe.
- Token revocation: Python (DEL only) vs Go (`them:token:revoked` pub/sub) —
  Python admin writes must also publish `them:token:revoked` so Go pods evict L1.
- EP-config invalidation: Python (DEL) vs Go (`them:ep:config:changed`) — same.

---

## 10. Redis Streams

| Stream key | Producer | Consumer | Reality |
|---|---|---|---|
| `them:dash:run:{runID}:stream` | **Documented** as Python worker (Lua XADD, MAXLEN ~5000, field `data`) per `theM_gateway/docs/REDIS.md` | Go `runstream/streamer.go` (XRANGE trim-check :144, XRANGE replay :175, XREAD BLOCK live :209) — **read only** | **CRITICAL: the current `app/temporal/activities.py` has NO `xadd`.** `grep -rn "xadd\|XADD" app/` returns nothing. The `:stream` producer does not exist in this codebase yet. Go's `dual`/`streams` mode therefore reads an empty stream against today's Python worker. Streams delivery is future work (Phase 11c-D); today only Pub/Sub `:tokens` carries events. |

**Migration implication:** Wave 4 must either (a) add XADD to the Python
worker before Go consumes Streams, or (b) keep Go on Pub/Sub `:tokens` until a
Go worker owns the workflow and produces the stream.

---

## 11. Temporal Interactions

| Aspect | Python | Go |
|---|---|---|
| Workflow type | `OrchestrationWorkflow` (`workflows.py`) | `"OrchestrationWorkflow"` (`temporal/workflow.go:22`) — same name |
| Task queue | from `temporal/config.py` (`_get_task_queue`) | `"them-orchestration"` (`workflow.go:19`) |
| Workflow ID | **`ctx-{context_id}`** (one per conversation; `bridge_client.py:72`) | **run UUID** (`ws/handler.go:349,458`) — **DIFFERENT scheme** |
| Worker | Python worker registers workflow+activities (`app/temporal/worker.py`) | **NONE** — Go is a Temporal *client* only; dispatches to the Python worker. Go's own workflow/activity types are dead code. |
| Start | `client.start_workflow(...)` via `bridge_client.start_orchestration_workflow` (ws_orchestrator, apps SSE/WS/REST) | `ExecuteWorkflow(temporal.WorkflowType, PythonOrchestrationInput{RunID:runID,...})` (ws/sse handlers) |
| HITL signal | **`submit_human_response`** to `ctx-{context_id}` via **direct client** (`runs.py:413`, bypasses bridge_client) | **`human_input`** to run UUID (`signaler.go:33`, admin runs `/signal`) — **name + target mismatch** |
| Cancel | `handle.cancel()` (`bridge_client.cancel_workflow`) from ws_orchestrator/apps | (WS ctx cancel propagates) |
| Reconciler | none | `reconciler.Run` (Temporal-gated): sweeps stale `them.runs status=running`, PG advisory lock `987654321`, maps Temporal status→DB. **`RECONCILER_DRY_RUN=true` everywhere** (no writes). NotFound leaves DB unchanged (safe for Python `ctx-` IDs). |

**A2A does NOT touch Temporal in either runtime** — orchestration runs inline
via `task_runner_run`.

---

## 12. Session Lifecycle & Runtime Governance

### 12.1 `session_manager.py` (best-effort, never raises)

| Key | Type / TTL | Ops | Line |
|---|---|---|---|
| `them:sess:{session_id}` | Hash, 90s | register HSET+EXPIRE, touch EXPIRE, end DEL, get HGETALL | :80-84,136,113,147 |
| `them:sess:{session_id}:active` | Set, 90s | set/clear active agent | :214-243 |
| `them:ep:{ep_slug}:sessions` | Set, none | SADD/SREM; SCARD/SMEMBERS | :87,115,161,185 |
| `them:app:{app_id}:sessions` | Set, none | SADD/SREM; SCARD/SMEMBERS | :89,117,173,196 |
| `them:pod:{instance_id}` | Hash, 30s | heartbeat HSET+EXPIRE (every 15s from main.py) | :264-269 |
| `them:pods` | Set, none | SADD | :270 |

Side effects: register/end/active-agent publish `them:dash:sessions:{app_id}`
(dashboard). `signal_disconnect` delegates to `runtime_manager` → PUB
`them:sess:control:{session_id}`.

> **Note:** CLAUDE.md/REDIS.md also list `them:bridge:{instance_id}:heartbeat`
> (30s). The code path actually uses `them:pod:{instance_id}` +
> `them:pods` (`session_manager.py`). Reconcile the pod-liveness key name in Wave 3.

### 12.2 `runtime_manager.py` (raises `RuntimeLimitError` / `RuntimeQueueFull`)

Gate order (`runtime_gate`): (1) blocked token `sha256` ∈ `app_runtime.blocked_tokens`; (2) blocked user_id; (3) app rate limit INCR `rl:them:app:{app_id}:{hour}` (TTL 7200) vs `rate_limit_rpm*60`; (4) **soft** app cap SCARD `them:app:{app_id}:sessions`; (5) orchestrator rate limit `rl:them:{user_id}:{hour}`; (6) **atomic** EP cap via Lua EVAL on `them:ep:{slug}:sessions` (prune ghosts + cap + SADD), queue via `RuntimeQueueFull` + `ep_gate_try` retry. Fail-open on Redis error; fail-closed on explicit cap.

> **Go status:** `internal/gate` (Check/Register/Confirm reservation pattern +
> queue BLPOP) and `internal/ratelimit` are **fully implemented but NOT wired**
> — `main.go:116` discards the limiter (`_ = limiter`) and WS/SSE never call
> `.WithGate(...)`. **Wave 3 must wire these before Go serves `/apps/*`** or all
> caps/blocks/rate-limits silently stop enforcing.

### 12.3 Run/task persistence

- `run_recorder.py`: writes **only** `them.runs` (start :23 / complete :146),
  `them.run_steps` (:58/:90), `them.run_usage` (:118). **No Redis.** Fire-and-forget.
- `activities.py` (Python worker): all run-event Redis publishing (Pub/Sub).
- Go `runrecorder`: writes `them.runs`, `them.run_steps`, `them.run_usage`;
  stamps `events_transport` per `RUN_EVENTS_MODE` (`recorder.go:60`).

---

## 13. Auth-Service HTTP Calls (`auth_client.py`, base `http://them-auth-service:8701`)

| Function:line | Method + Path | Validates |
|---|---|---|
| `validate_token`:25 | POST `/api/v1/mcp/tokens/validate` `{token}` | Opaque MCP token |
| `get_user`:39 | GET `/api/v1/users/{user_id}` | User lookup |
| `validate_jwt`:50 | POST `/api/v1/auth/verify` (Bearer) | JWT validity → payload; used by `require_jwt` + WS + webrtc |

> **Trust-model divergence:** Python calls the auth service over HTTP for every
> JWT check. Go validates JWT **locally via RS256** (`internal/auth/jwt.go`,
> requires `JWT_PUBLIC_KEY_PEM`). If unset in Go, admin/runs routes are
> **unauthenticated**. Bearer/opaque tokens: both use L1→L2 (`them:session:token:{sha256}`)→PG `them.access_tokens`. Go note: Go's `access_tokens` query has no `application_id` column so `AppID` is always 0.

---

## 14. Frontend Contracts (must keep working)

Proxies: `/api/them/[...path]` → `${THE_M_API_URL}/api/v1/<path>` (cookie→bearer);
`/api/apps/[...path]` → `${BRIDGE}/apps/<path>` (cookie→bearer);
`/api/bridge/:path*` → `${BRIDGE}/:path*` (**no auth, no `/api/v1`**);
`/api/auth/*` → `${THE_M_AUTH_URL}/api/v1/auth/*`.

**WS auth is via `?token=` query param** obtained from `/api/auth/token`, NOT
the httpOnly cookie — every WS endpoint must accept `?token=`.

| Prefix | Endpoints frontend depends on |
|---|---|
| `/api/v1/admin/*` | agents, orchestrators (+test-llm/voice/tts), system-agents, applications (+bulk-delete, runtime, middleware-wirings, orch tests), middleware-defs, tokens, sessions (+disconnect), monitoring-config |
| `/api/v1/runs/*` | list, stats, {id}, cancel, delete, bulk-delete, tasks, artifacts, context/{id}/artifacts, contexts |
| `/api/v1/orchestrators/{name}/*` | tts (audio stream), transcribe (multipart) |
| `/apps/*` | `/apps/{slug}` (liveness ping), `/apps/{slug}/webrtc/token` |
| `/health` | via `/api/bridge/health` (no prefix, no auth) — **breaks if `/health` moves under `/api/v1` or gains auth** |
| WS (same host, `?token=`) | `/ws/orchestrate/{name}` (incl. `workflow_advisor`), `/ws/dashboard`, `/apps/{slug}/ws` (also advertised on `:8088`) |
| LiveKit | `serverUrl` from `/apps/{slug}/webrtc/token` |
| Auth service | `/api/v1/auth/{login,refresh,me,logout}`; cookies `them_access_token`+`them_refresh_token`; JWT must carry `exp` |
| External | `{agent.endpoint_url}/.well-known/agent-card.json` (direct, not bridge) |

No `EventSource`/SSE usage in the frontend — realtime is WS + LiveKit only.
`fetchAgentCard` hits arbitrary agent hosts directly.

> **Frontend-breaking Go gaps today:** Go WS/SSE paths differ
> (`/ws/orchestrate/{app_slug}/{ep_slug}`, `/sse/orchestrate/...`); Go admin uses
> `PUT` (frontend sends `PATCH`) and orchestrator `{name}` (frontend sends
> `{id}`); Go lacks `/health` (bare), tokens, llm-providers, middleware,
> system-agents, sessions, monitoring-config, media, `/apps/{slug}` catalogue,
> webrtc, and dashboard WS. A compatibility shim or frontend rework is required
> before any Traefik cutover.

---

## 15. Traefik Routing — Current State

Entry point `:8088` (web), `:8089` (dashboard). Routing is defined both by
container **labels** (`docker-compose.yml`) and a **file provider**
(`traefik/dynamic.yml` — the file provider additionally sets a sticky cookie
`them_lb` that the labels do not).

| Path prefix | Backend today | Router / priority |
|---|---|---|
| `/api/v1` | **Python** them-bridge:8001 | them-api / 100 |
| `/ws` | **Python** them-bridge:8001 | them-ws / 100 |
| `/sse` | **Python** them-bridge:8001 | (gateway overlay: Python owns `/sse` until Go SSE built) |
| `/apps` | **Python** them-bridge:8001 | them-apps / 100 |
| `/a2a` | **Python** them-bridge:8001 | them-a2a / 100 |
| `/health` | **Python** them-bridge:8001 | them-health / 90 |
| **`/go-health`** | **Go** them-go-svc:8002 (rewrite → `/health`) | them-go-health / 120 (gateway `docker-compose.traefik.yml` only) |
| `/temporal` | temporal-ui:8080 | 150 |
| `/livekit` | livekit:7880 (stripprefix) | 100 |
| `/` | them-frontend:3200 | 1 (catch-all) |

**Bottom line: no product traffic reaches Go.** Go serves only `/go-health/*`.

### Go bridge compose

| Stack | Service | Port | Profile | Traefik | TEMPORAL_ENABLED | RUN_EVENTS_MODE | RECONCILER_DRY_RUN |
|---|---|---|---|---|---|---|---|
| root `docker-compose.yml` | them-go-bridge | 8002 (no host publish) | `go` | **disabled** | not set | not set | not set |
| `theM_gateway/docker-compose.integration.yml` | them-go-bridge | 8002:8002 | `temporal` | direct (no Traefik) | `true` | `dual` | `true` |
| `theM_gateway/docker-compose.soak.yml` | them-go-bridge-2 | 8003:8002 | `temporal` | direct | `true` | `dual` | `true` |

`RUN_EVENTS_MODE`: missing/invalid → `pubsub` (prod default); `dual` = both;
`streams` gated. `RECONCILER_DRY_RUN` defaults `true` (fail-safe).

---

## 16. Go Bridge — What It Actually Implements Today

Registered routes (27): health/live+ready, `/metrics`, WS
`/ws/orchestrate/{app_slug}/{entry_point_slug}`, SSE
`/sse/orchestrate/{app_slug}/{entry_point_slug}` (GET+POST), A2A
`POST /a2a/{app_slug}` + `/.well-known/agent.json`, admin agents (5, PUT/soft-del),
orchestrators (5, keyed by `{name}`, PUT/soft-del), applications (5) +
entry-points (3), runs list/get/signal (3, jwt-only).

**Implemented + wired:** WS/SSE text orchestration (EP-config-driven auth,
Pub/Sub + Streams dispatcher), A2A `message/send`, bearer 3-tier cache +
`them:token:revoked` revocation, local RS256 JWT + `RequireSuperAdmin`, session
store (Hash + shadow-TTL sets + ghost prune), health, metrics, admin CRUD for
agents/orchestrators/applications/entry_points, runs list/get/signal, Temporal
**client** + HITL signal + reconciler (dry-run).

**Implemented but NOT wired (dead at runtime):**
- **Gate** (`internal/gate`) — session caps, per-EP/app concurrency, queue. `.WithGate()` never called.
- **Rate limiter** (`internal/ratelimit`) — discarded at `main.go:116`.
- **Agent registry** (`internal/agentregistry`) — A2A outbound + cache; orchestrator built with `nil` agents (`main.go:111`).
- **Pod heartbeat** + **admin session-disconnect** (`session.WriteHeartbeat`, `SignalDisconnect`, `SubscribeControl`) — never started/subscribed.
- Go `OrchestrationWorkflow`/activity types — **no worker registers them.**

**Missing entirely (vs Python):** `/health` (bare), tokens admin CRUD + revoke
route, llm-providers, middleware, system-agents, monitoring-config, admin
sessions routes, `/apps` catalogue + `/apps/{slug}`, webrtc token, media
(tts/transcribe), dashboard WS, A2A `GetTask`/`CancelTask`/push, voice EPs
(501), SSE `tool_result`, `orchestrator.allowed_agents` persistence, runs
stats/contexts/tasks/artifacts/cancel/delete/bulk-delete.

**Doc drift found:** go/CLAUDE.md says L2 token key is `them:token:{sha256}`;
code uses `them:session:token:` (`auth/token_cache.go:77`). `server.go:96` has a
stale "TODO mount protected routes" comment.

---

## 17. Tests

**Python (`scripts/tests/`, runner `python3.12 run_tests.py`) — 35 + MT + e2e.**
Migration-relevant coverage:
- ws_orchestrate: 10 (structure), 11 (live), 19 (edges), 31/33 (session/control)
- apps: 22 (EP REST/WS/SSE/poll), 31/33/34/35 (runtime/session/queue)
- runs: 12 (auth), 13 (wiring), 14 (e2e)
- a2a: 16, 18, 21, 23, 24, 25
- dashboard: 13, 26, 32
- sessions/runtime: 31, 33, 34, 35
- admin CRUD: 05 agents, 06 orchestrators, 08 tokens, 22 applications, 32 monitoring
- traefik/multi-replica: 20

**Go (`go/TEST_INDEX.md`) — 255 total** (212 unit / 20 integration / 23 live).
Notable: ws (16), sse (15), admin (19), gate (16), epconfig (26), reconciler
(15), runstream pub/sub (10) + streamer/dispatcher (15), config
(RUN_EVENTS_MODE/RECONCILER_DRY_RUN defaults).

> Every migrated route needs a Go test mirroring the Python check it replaces
> (go/CLAUDE.md rule: no code change without a test + `TEST_INDEX.md` update).

---

## 18. DB Tables (schema `them`)

`access_tokens, agents, applications, app_orchestrators, artifacts,
audit_logs, config, entry_points, llm_providers, middleware_defs,
middleware_wirings, orchestrators, runs, run_steps, run_usage,
schema_migrations, task_messages, tasks`. Auth tables live in schema
`auth_service` — **never queried directly by the bridge** (HTTP to :8701 only).

Go currently reads/writes: runs, run_steps, run_usage (runrecorder); agents,
orchestrators, applications, entry_points (admin, full CRUD); access_tokens
(read-only, no `application_id`); entry_points⋈applications (epconfig). Not yet
touched by Go: config, llm_providers, middleware_defs, middleware_wirings,
app_orchestrators, artifacts, audit_logs, task_messages, tasks.
