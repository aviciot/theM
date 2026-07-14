# the-M Status
# Last updated: 2026-07-14

## Build Progress

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Skeleton & infra | ✓ Complete | config, database, models, health, docker-compose |
| Phase 1 — Auth | ✓ Complete | auth_service, httpOnly cookie auth (`them_access_token`, `them_refresh_token`) |
| Phase 2 — LLM providers admin | ✓ Complete | admin_llm_providers.py, providers/ |
| Phase 3 — Agent registry & adapters | ✓ Complete | adapters/, agent_registry.py, admin_agents.py, admin_orchestrators.py |
| Phase 4 — Token cache & rate limiter | ✓ Complete | token_cache.py, rate_limiter.py, admin_tokens.py, _deps.py |
| Phase 5 — Orchestrator loop | ✓ Complete | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | ✓ Complete | ws_dashboard.py, runs.py, Redis pub/sub multiplexing |
| Phase 6.5 — Frontend admin UI | ✓ Complete | Orchestrators, Agents, Tokens, Runs pages; per-orch LLM config |
| Phase 6.6 — Playground UI | ✓ Complete | Split-pane chat + real-time Redis trace; mock agents |
| Phase 7 — Tests + compose finalize | ✓ Complete | 140+ tests passing (cross-platform Python runner), compose hardened |
| Rename: Odin → the-M | ✓ Complete | All identifiers, schemas, containers, Redis keys, cookies renamed |
| Local deployment | ✓ Complete | Stack running, DB seeded, users created, login works |
| **A2A migration Phase 3** | ✓ Complete | task_runner.py (durable agentic loop), task_store.py, ws_orchestrator rewired |
| **A2A migration Phase 4** | ✓ Complete | A2aAsyncAdapter, AdapterEvent extended, push webhook (/a2a/push), reaper |
| **A2A migration Phase 5** | ✓ Complete | context_service.py, Redis artifact cache `them:ctx:{ctx_id}:heads` |
| **A2A migration Phase 6** | ✓ Complete | runs/{id}/tasks, runs/{id}/artifacts, runs/context/{ctx_id}/artifacts endpoints; playground debug tabs |
| **A2A migration Phase 7** | ✓ Complete | a2a-echo, a2a-slow, a2a-stream agents; test-agents compose profile; seed SQL; test_16 |
| **Phase 8.1** | ✓ Complete | Provider-neutral durable history: serialize_turn/deserialize_history on LLMProvider ABC |
| **Phase 8.2** | ✓ Complete | OpenAI provider: full streaming, tool calls, durable history |
| **Phase 8.3** | ✓ Complete | Provider factory, per-orchestrator LLM config, llm_api_key_encrypted |
| **Phase 8.4** | ✓ Complete | Context summarization memory: memory_service.py, Redis `them:ctx:{id}:summary`, Haiku summarizer, memory UI in orchestrator admin |
| **Phase 8.5** | ✓ Complete | Orchestrator-as-agent (durable inbound A2A): a2a_server.py rewired to them.tasks, returnImmediately, GetTask from DB, fork-bomb guard |
| **Phase 8.6** | ✓ Complete | Pluggable edge adapters: app/edges/ (EdgeAdapter ABC, WebsocketEdge, SSEEdge), ws_orchestrator uses WebsocketEdge |
| **Agent discovery UI** | ✓ Complete | Row Discover button: fetches card, diffs vs stored, shows popup with changes highlighted, pulsing Save, orchestrator impact warning |
| **Persistent context threading** | ✓ Complete | Frontend passes context_id on follow-up messages; server reuses it so memory summary carries across turns |
| **Traefik reverse proxy** | ✓ Complete | traefik:v3.6, single port 8088, path-based routing, no sticky sessions — bridge is stateless via Temporal |
| **JWT auto-refresh** | ✓ Complete | `/api/auth/token` auto-refreshes when token has < 30s left; WS URL derived from `window.location` (no hardcoded port) |
| **Traefik stack isolation** | ✓ Complete | `traefik-instance=them` constraint — them-traefik ignores all non-the-M containers on shared Docker socket |
| **Phase 9 — A2A production hardening** | ✓ Complete | Token expiry enforcement, ownership isolation (owns_task), rate limiting (10 rpm), agent card strips system_prompt, default 30-min task deadline, 512 KB body + 10-item batch limits, TOCTOU scope check; `them.tasks.user_id` + `them.applications` schema; test_21 (47 checks) |
| **Phase 9 Phase 2 — Applications CRUD** | ✓ Complete | `app/routers/admin_applications.py`: CRUD for `them.applications`, slug+entry_point_type validation, orchestrator name join; wired in `main.py` |
| **Phase 9 Phase 3 — Pluggable entry points** | ✓ Complete | `app/routers/apps.py`: `GET /apps`, `POST /apps/{slug}` (REST), `GET /apps/{slug}/tasks/{task_id}`, `WS /apps/{slug}/ws`, `GET /apps/{slug}/sse`; test_22 (51 checks) |
| **Phase 10 — SSE edge** | ✓ Complete | `app/edges/sse_edge.py`: asyncio queue-backed streaming; `GET /apps/{slug}/sse` route; test_19 + test_22 updated |
| **Phase 11 — Multi-turn chat** | ✓ Complete | `_load_context_history()` loads prior task_messages; user message persisted at seq=0; `history_window` limits turns; `record_tool_results_activity` ensures tool_result rows are persisted so resumed sessions are valid |
| **True A2A typed input** | ✓ Complete | `docu_writer`: typed data parts, no regex; adapter: `input_modes`-aware `_build_parts()`; factory: reads `input_modes` from agent skills; test_25 (35 checks) |
| **Temporal migration (all 7 phases)** | ✓ Complete | Durable orchestration via Temporal; OrchestrationWorkflow replaces task_runner.run(); bridge is fully stateless; HITL signal endpoint; all WS runs go through Temporal |
| **Debate stack** | ✓ Complete | 4 A2A debate agents (evidence, logic, creative on Haiku; judge on Sonnet); `debate_flow` orchestrator; context compaction (JSON-aware); db/008_debate_stack.sql |
| **Session resume in playground** | ✓ Complete | `context_id` persisted to localStorage; "Resume last conversation?" banner on page load; Sessions debug tab; full history reconstructed from DB on resume |
| **History sanitization (full-pass)** | ✓ Complete | `_sanitize_history` drops orphaned tool_use/tool_result pairs anywhere in history, not just at the tail; prevents HTTP 400 on Anthropic API for resumed sessions |
| **Multi-EP playground** | ✓ Complete | Tabs (each EP = persistent live WS, switching is view toggle) + Compare mode (all tabs side-by-side, shared composer broadcasts to all). WebRTC EPs show voice-room button only. |
| **Poisoned context_id fix** | ✓ Complete | `DeadContextError` + pre-subscribe pattern: bridge checks if existing workflow is closed before re-attaching; client receives `context_id: null` signal and clears localStorage. Eliminates hung sessions after workflow failure. |
| **Entry point diff by slug** | ✓ Complete | `_apply_entry_point_diff` now keys on slug (not id). Frontend never needs to send EP id. Existing slug → UPDATE, new slug → CREATE, missing slug → DELETE. Eliminates 500 on canvas save when `_epId` was lost. |

## Infrastructure (as of 2026-07-14)

| Container | Image/Source | Data | Port |
|---|---|---|---|
| `them-traefik` | traefik:v3.6 | — | **8088** (host, all traffic), 127.0.0.1:8089 (dashboard) |
| `them-postgres` | postgres:16-alpine | `./data/them-postgres/pgdata/` | 5432 (internal) |
| `them-redis` | redis:7-alpine | `./data/them-redis/` | 6379 (internal) |
| `them-auth-service` | `auth_service/` | — | 8701 (internal) |
| `them-bridge` | `app/` | `./data/them-logs/` | 8001 (internal only) |
| `them-worker` | `app/` (Temporal worker) | — | — (internal, connects to Temporal) |
| `them-temporal` | temporalio/auto-setup | — | 7233 (internal) |
| `them-temporal-ui` | temporalio/ui | — | proxied through Traefik at `/temporal/` |
| `them-frontend` | `frontend/` | — | 3200 (internal only) |
| `vision-agent` | `agents/vision_agent/` | — | 9100 (internal) — **unhealthy** |
| `a2a-echo` | `agents/a2a_echo/` | — | 9200 (internal) — **profile: test-agents** |
| `a2a-slow` | `agents/a2a_slow/` | — | 9201 (internal) — **profile: test-agents** |
| `a2a-stream` | `agents/a2a_stream/` | — | 9202 (internal) — **profile: test-agents** |
| `agent-evidence` | `agents/debate/agent_evidence/` | — | 9401 (internal) — debate stack |
| `agent-logic` | `agents/debate/agent_logic/` | — | 9402 (internal) — debate stack |
| `agent-creative` | `agents/debate/agent_creative/` | — | 9403 (internal) — debate stack |
| `agent-judge` | `agents/debate/agent_judge/` | — | 9404 (internal) — debate stack |

## Users Seeded

| Username | Password | Role |
|---|---|---|
| `admin` | `admin123` | super_admin |
| `avi` | `avi123` | super_admin |

## API Routes (live)

| Route | Method | Status |
|---|---|---|
| `/health`, `/health/live`, `/health/ready` | GET | ✓ Live |
| `/api/v1/admin/llm-providers` | CRUD | ✓ Live |
| `/api/v1/admin/agents` | CRUD | ✓ Live |
| `/api/v1/admin/agents/discover` | POST | ✓ Live — fetch & diff agent card |
| `/api/v1/admin/agents/{id}/test` | POST | ✓ Live |
| `/api/v1/admin/orchestrators` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators/{id}/test-llm` | POST | ✓ Live |
| `/api/v1/admin/tokens` | CRUD | ✓ Live |
| `/ws/orchestrate/{name}` | WebSocket | ✓ Live — all runs via Temporal |
| `/ws/dashboard` | WebSocket | ✓ Live |
| `/api/v1/runs` | GET/DELETE | ✓ Live |
| `/api/v1/runs/{id}/tasks` | GET | ✓ Live |
| `/api/v1/runs/{id}/artifacts` | GET | ✓ Live |
| `/api/v1/runs/context/{ctx_id}/artifacts` | GET | ✓ Live |
| `/api/v1/runs/{run_id}/signal` | POST | ✓ Live — HITL human response signal |
| `/a2a/push/{task_id}` | POST | ✓ Live |
| `/.well-known/agent-card.json` | GET | ✓ Live |
| `/api/v1/admin/applications` | CRUD | ✓ Live |
| `/apps` | GET | ✓ Live |
| `/apps/{slug}` | POST | ✓ Live — REST fire-and-forget |
| `/apps/{slug}/tasks/{task_id}` | GET | ✓ Live — task poll |
| `/apps/{slug}/ws` | WebSocket | ✓ Live — streaming chat |
| `/apps/{slug}/sse` | GET (SSE) | ✓ Live — SSE streaming |

## Frontend Pages (live, http://localhost:8088)

| Page | Path | Status |
|---|---|---|
| Login | `/login` | ✓ — credentials pre-filled in dev mode |
| Dashboard | `/dashboard` | ✓ |
| Agents | `/agents` | ✓ |
| Run History | `/runs` | ✓ |
| Orchestrators | `/admin/orchestrators` | ✓ |
| Access Tokens | `/admin/tokens` | ✓ |
| Playground | `/admin/playground` | ✓ — chat + debug tabs + session resume + multi-EP tabs + Compare mode |
| Applications | `/admin/applications` | ✓ — canvas builder: multi-EP, slug-based diff, orchestrator wiring |

## Open Items

- **`vision-agent` unhealthy**: needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` set in `.env`. Not blocking anything else.
- **Git hooks not wired**: test runner exists (`python scripts/tests/run_tests.py`) but no pre-push hook. Planned as GitHub Actions.
- **Replica 2**: compose profile `replica`. Enable with `--profile replica`. Bridge is fully stateless via Temporal — safe to run N replicas.
- **DB reset trap**: if Postgres is wiped but Redis survives, orchestrator cache holds stale FK IDs. After any DB wipe: re-run DB init steps from CLAUDE.md, then recreate orchestrators via UI to refresh Redis cache.
- **Legacy task_runner / orchestrator_service**: `task_runner.py` and `orchestrator_service.py` in-RAM loop are no longer reachable from `ws_orchestrator.py` (Temporal is hardcoded). Pending cleanup — safe to delete once Temporal is proven stable.
- **User management UI**: no frontend for managing auth_service users/teams. Admin only via psql or curl.
- **WebRTCEdge**: planned future phase — real-time audio, needs ASR + TTS + signaling server.
- **Debate stack ANTHROPIC_API_KEY**: debate agents read `ANTHROPIC_API_KEY` from `.env`. Must be set and non-empty or debate runs will fail on agent invocation.
