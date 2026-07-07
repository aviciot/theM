# the-M Status
# Last updated: 2026-07-07

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
| **Phase 8.6** | ✓ Complete | Pluggable edge adapters: app/edges/ (EdgeAdapter ABC, WebsocketEdge, VoiceEdge stub, RestEdge stub), ws_orchestrator uses WebsocketEdge |
| **Agent discovery UI** | ✓ Complete | Row Discover button: fetches card, diffs vs stored, shows popup with changes highlighted, pulsing Save, orchestrator impact warning |
| **Persistent context threading** | ✓ Complete | Frontend passes context_id on follow-up messages; server reuses it so memory summary carries across turns |
| **Traefik reverse proxy** | ✓ Complete | traefik:v3.6, single port 8088, path-based routing, sticky sessions (`them_lb` cookie), Docker provider label discovery |
| **JWT auto-refresh** | ✓ Complete | `/api/auth/token` auto-refreshes when token has < 30s left; WS URL derived from `window.location` (no hardcoded port) |
| **Traefik stack isolation** | ✓ Complete | `traefik-instance=them` constraint — them-traefik ignores all non-the-M containers on shared Docker socket |
| **Phase 9 — A2A production hardening** | ✓ Complete | Token expiry enforcement, ownership isolation (owns_task), rate limiting (10 rpm), agent card strips system_prompt, default 30-min task deadline, 512 KB body + 10-item batch limits, TOCTOU scope check; `them.tasks.user_id` + `them.applications` schema; test_21 (47 checks) |
| **Phase 9 Phase 2 — Applications CRUD** | ✓ Complete | `app/routers/admin_applications.py`: CRUD for `them.applications`, slug+entry_point_type validation, orchestrator name join; wired in `main.py` |
| **Phase 9 Phase 3 — Pluggable entry points** | ✓ Complete | `app/routers/apps.py`: `GET /apps`, `POST /apps/{slug}` (REST fire-and-forget), `GET /apps/{slug}/tasks/{task_id}` (poll), `WS /apps/{slug}/ws` (streaming chat); public/token access policy; ownership isolation; frontend Applications page + Sidebar nav; test_22 (51 checks) |
| **Phase 10 — SSE edge** | ✓ Complete | `app/edges/sse_edge.py`: asyncio queue-backed streaming; `GET /apps/{slug}/sse` route; `entry_point_type` updated to `websocket\|sse\|webrtc`; DB migration 005_phase10.sql; test_19 + test_22 updated |
| **Phase 11 — Multi-turn chat** | ✓ Complete | `task_runner.py`: user message saved as `task_message seq=0`; `_load_context_history()` loads prior root tasks' messages for `context_id`; prior history prepended to LLM messages each turn; `history_window` (default 20) limits turns loaded; test_10 updated |

## Infrastructure (as of 2026-07-06)

| Container | Image/Source | Data | Port |
|---|---|---|---|
| `them-traefik` | traefik:v3.6 | — | **8088** (host, all traffic), 127.0.0.1:8089 (dashboard) |
| `them-postgres` | postgres:16-alpine | `./data/them-postgres/pgdata/` | 5432 (internal) |
| `them-redis` | redis:7-alpine | `./data/them-redis/` | 6379 (internal) |
| `them-auth-service` | `auth_service/` | — | 8701 (internal) |
| `them-bridge` | `app/` | `./data/them-logs/` | 8001 (internal only) |
| `them-frontend` | `frontend/` | — | 3200 (internal only) |
| `vision-agent` | `agents/vision_agent/` | — | 9100 (internal) — **unhealthy** |
| `a2a-echo` | `agents/a2a_echo/` | — | 9200 (internal) — **profile: test-agents** |
| `a2a-slow` | `agents/a2a_slow/` | — | 9201 (internal) — **profile: test-agents** |
| `a2a-stream` | `agents/a2a_stream/` | — | 9202 (internal) — **profile: test-agents** |

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
| `/api/v1/admin/agents/discover` | POST | ✓ Live — fetch & diff agent card; accepts agent_id to reuse stored token |
| `/api/v1/admin/agents/{id}/test` | POST | ✓ Live |
| `/api/v1/admin/orchestrators` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators/{id}/test-llm` | POST | ✓ Live |
| `/api/v1/admin/tokens` | CRUD | ✓ Live |
| `/ws/orchestrate/{name}` | WebSocket | ✓ Live |
| `/ws/dashboard` | WebSocket | ✓ Live |
| `/api/v1/runs` | GET/DELETE | ✓ Live |
| `/api/v1/runs/{id}/tasks` | GET | ✓ Live (A2A Phase 6) |
| `/api/v1/runs/{id}/artifacts` | GET | ✓ Live (A2A Phase 6) |
| `/api/v1/runs/context/{ctx_id}/artifacts` | GET | ✓ Live (A2A Phase 6) |
| `/a2a/push/{task_id}` | POST | ✓ Live (A2A Phase 4) |
| `/.well-known/agent-card.json` | GET | ✓ Live (A2A Phase 4) |
| `/api/v1/admin/applications` | CRUD | ✓ Live (Phase 9 Phase 2) |
| `/apps` | GET | ✓ Live — public app catalogue |
| `/apps/{slug}` | POST | ✓ Live — REST fire-and-forget entry point |
| `/apps/{slug}/tasks/{task_id}` | GET | ✓ Live — task poll |
| `/apps/{slug}/ws` | WebSocket | ✓ Live — streaming chat entry point |
| `/apps/{slug}/sse` | GET (SSE) | ✓ Live — SSE streaming entry point (text/event-stream) |

## Frontend Pages (live, http://localhost:8088)

| Page | Path | Status |
|---|---|---|
| Login | `/login` | ✓ — credentials pre-filled in dev mode |
| Dashboard | `/dashboard` | ✓ |
| Agents | `/agents` | ✓ |
| Run History | `/runs` | ✓ |
| Orchestrators | `/admin/orchestrators` | ✓ |
| Access Tokens | `/admin/tokens` | ✓ |
| Playground | `/admin/playground` | ✓ — chat + debug tabs (Trace, Tasks, Artifacts, Memory) |

## Open Items

- **`vision-agent` unhealthy**: needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` set in `.env`. Not blocking anything else.
- **Git hooks not wired**: test runner exists (`python scripts/tests/run_tests.py`) but no pre-push hook. Planned as GitHub Actions.
- **Replica 2**: compose profile `replica`, not running by default. Enable with `--profile replica`.
- **DB reset trap**: if Postgres is wiped but Redis survives, orchestrator cache holds stale FK IDs → run INSERT fails. After any DB wipe: re-run DB init steps from CLAUDE.md, then recreate orchestrators via UI to refresh Redis cache.
- **Mock agents removed**: `mock-agent-assistant`, `mock-agent-researcher`, `mock-agent-coder` disabled in DB and stopped. Only real A2A agents remain.
- **RestEdge / VoiceEdge real implementations**: planned next — see Open Items below.
- **WebRTCEdge**: planned future phase — real-time audio, needs ASR + TTS + signaling server.
- **Multi-turn chat**: implemented — `task_runner.py` loads prior turns via `_load_context_history()` and prepends to LLM context. Frontend already threads `context_id`. Works across reconnects.
- **User management UI**: no frontend for managing auth_service users/teams.
