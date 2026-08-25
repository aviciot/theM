# Current Session State — the-M
# Last updated: 2026-08-25
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `e6cebde` — fix(proxy): add /agents/{id}/llm-nodes pattern to Go bridge routing

Recent commits (newest first):
```
e6cebde fix(proxy): add /agents/{id}/llm-nodes pattern to Go bridge routing
f4b58bd feat(canvas-llm): per-node LLM provider+model overrides in RuntimeView + debug panel
1e5c76c test(app-params): Phase 4 — handler + runtime tests; fix plain: decryption
b308e84 feat(app-params): Phase 3 — frontend UI for app global params
5328fb6 feat(app-params): Phase 2 — compiler/interpreter/runtime for app_param_ref
d2c4283 feat(app-params): Phase 1 — app-level global named parameters (DB + backend)
```

---

## Deployment state

**Active deployment: local Linux server**

Stack startup command:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
```

UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is the sole auth service (HS256 JWT + bcrypt)
- **`them-bridge` (Python) is permanently retired** — behind `profiles: [legacy]`; does NOT start in default or `--profile temporal` mode
- **`them-worker` (Python) is permanently retired** — behind `profiles: [legacy]`
- `them-go-bridge` is the active API gateway on port 8002
- `them-go-worker` is the active Temporal worker — **no explicit profile in `docker-compose.dev.yml`**, starts by default
- `them-agent-runtime` runs 2 replicas (port 9300 internal), profile `[agents]`
- Frontend `THE_M_API_URL` points to `http://them-traefik:8088`
- Named Docker volumes: `them-postgres-data`, `them-redis-data`, `them-logs` — `external: true`
- **Project name: `them_gateway`** — required for all compose commands

Currently running containers (verified):
```
them-go-bridge        ✅ healthy
them-go-worker        ✅ running (no profile — default service)
them-auth-go          ✅ healthy
them-agent-runtime-1  ✅ healthy (port 9300)
them-agent-runtime-2  ✅ healthy (port 9300)
them-frontend         ✅ running
them-postgres         ✅ healthy
them-redis            ✅ healthy
them-traefik          ✅ healthy
temporal-frontend     ✅ (with --profile temporal)
them-bridge (Python)  ❌ NOT running — profiles: [legacy]
them-worker (Python)  ❌ NOT running — profiles: [legacy]
```

---

## Go route ownership (all confirmed via Traefik labels)

All routes below are owned by `them-go-bridge` (`them-go-bridge-svc`, port 8002):

### Admin — read
- `GET /api/v1/admin/agents` (list)
- `GET /api/v1/admin/orchestrators` (list)
- `GET /api/v1/admin/applications` (list)

### Admin — write
- `POST /api/v1/admin/agents` — create
- `PUT|PATCH|DELETE /api/v1/admin/agents/{id}` — update/delete
- `POST /api/v1/admin/agents/discover`, `/agents/{id}/test`, `/agents/{id}/security-scan`
- `POST|PUT|PATCH|DELETE /api/v1/admin/orchestrators/{name}`
- `POST /api/v1/admin/applications`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}`
- `POST /api/v1/admin/applications/{id}/entry-points`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}`
- `PathRegexp /api/v1/admin/applications/{id}/.+` — all methods (covers provider-keys, runtime, agent-bindings subroutes)

### Admin — full ownership
- `PathPrefix /api/v1/admin/system-agents` — all methods
- `PathPrefix /api/v1/admin/tokens` — all methods
- `PathPrefix /api/v1/admin/sessions` — all methods
- `PathPrefix /api/v1/admin/component-definitions` — all methods
- `PathPrefix /api/v1/admin/agent-definitions` — all methods
- `GET|PUT /api/v1/admin/llm-providers/routing/config`
- `PathPrefix /api/v1/admin/llm-providers` — all methods
- `GET|POST|PATCH|DELETE /api/v1/admin/monitoring-config`

### Runs
- `GET /api/v1/runs` (list)
- `GET /api/v1/runs/stats`
- `GET /api/v1/runs/{id}` (detail)
- `GET /api/v1/runs/{id}/tasks`
- `GET /api/v1/runs/{id}/artifacts`
- `PATCH /api/v1/runs/{id}/cancel`
- `DELETE /api/v1/runs/{id}`
- `POST /api/v1/runs/bulk-delete`
- `POST /api/v1/runs/{id}/signal`

### App entry points (WS/SSE)
- `GET /apps/{slug}/ws`
- `GET|POST /apps/{slug}/sse`
- `GET /ws/orchestrate/{orch}/{ep}` (two-segment legacy path)
- `GET|POST /sse/orchestrate/{orch}/{ep}` (two-segment legacy path)

### Dashboard
- `GET /ws/dashboard`

### Health
- `GET|HEAD /health/live`, `/health/ready`

### Not yet migrated to Go Traefik (handler exists but route not wired)
- **`/a2a/*`** — Go handler implemented at `go/internal/a2a/` and mounted in `main.go`, but Traefik router `them-a2a` still points to `them-bridge-svc` (port 8001, Python — dead). **Active bug: `/a2a/` is currently broken.** Fix: redirect `them-a2a` router to `them-go-bridge-svc` in compose labels.

### Not in Go (no handler or Traefik route)
- `GET /api/v1/admin/users`, `/roles`, `/teams` — auth admin CRUD (served by `them-auth-service` on port 8701 directly from frontend; no Go handler needed unless we want to proxy it)
- `GET /runs/context/{ctx}/artifacts` — not used by admin UI
- Applications export/import/restore — Python-only, not migrated

---

## DB schema state (live)

All migrations applied through `db/037_agents_transport_canvas.sql`:

| Migration | Status |
|---|---|
| `db/001_schema.sql` through `db/027_*` | ✅ applied |
| `db/028_entry_points_tenant_scoped_slug.sql` | ✅ applied |
| `db/029_component_registry_foundation.sql` | ✅ applied |
| `db/030_component_subtype_adoption.sql` | ✅ applied |
| `db/031_phase_c_compiler_pins.sql` | ✅ applied |
| `db/032_ep_memory_config.sql` | ✅ applied — `entry_points` has 6 memory columns; `tasks.tenant_id` exists |
| `db/033_*` through `db/034_*` | ✅ applied |
| `db/035_agent_definitions.sql` | ✅ applied — `agent_definitions` table exists |
| `db/036_canvas_a2a_runtime.sql` | ✅ applied — `agent_runtime_specs` + `app_agent_bindings` exist |
| `db/037_agents_transport_canvas.sql` | ✅ applied — `agents_transport_check` includes `'canvas_a2a'` |
| `db/038_app_agent_params.sql` | ✅ applied — `app_agent_bindings.agent_params` JSONB column |
| `db/045_app_global_params.sql` | ✅ applied — `applications.app_params` JSONB column |

---

## Test state

```
go test ./...  — all packages, 0 failures (verified 2026-08-25, commit f4b58bd)
S1 total: 772 tests (unchanged — CMP/INT tests rewritten not added)
  S1-63: CMP-10..14 (compiler LLM node collection — 5 tests, rewrote from AppParamRefs)
  S1-64: INT-10..14 (interpreter AppParamRef HTTP + NodeLLMOverride — 5 tests, INT-14 rewritten)
  S1-65: RT-20..24 (runtime decodeAppGlobalParams — 5 tests)
  S1-66: HTTP-20..25+ (handler layer — 11 tests)
  fake DAL stubs updated in all 4 test files for GetAgentLLMNodes/UpsertNodeLLMOverride
go test ./... total: 793

Live e2e confirmed 2026-08-23:
  - run 23aeb8bf: streaming single zip artifact via a2a-stream ✅
  - run 5691b24a: streaming two files (HTML + zip) via a2a-stream ✅
App global params: e2e validated 2026-08-25 — GET/PUT/DELETE live ✅
```

---

## A2A feature state — COMPLETE AND LIVE VERIFIED (2026-08-23)

| Scenario | Agent | Status |
|---|---|---|
| Sync single file | `docu-writer` (HTML/PDF/MD) | ✅ live |
| Streaming single file | `a2a-stream` v1.1 | ✅ live (run 23aeb8bf) |
| Streaming multi-file | `a2a-stream` v1.2 | ✅ live (run 5691b24a) |

### Playground + Artifacts tab
- Artifacts tab renders all file types: `image/*` → `<img>`, `application/pdf` → iframe,
  `text/html` → srcDoc iframe, `text/markdown`/text → `<pre>`, unknown → download
- `ArtifactPart.data` (base64) in Go DAL + frontend API types for binary transport
- Binary artifacts base64-encoded in `GetRunArtifacts` for transport to browser

### Multi-artifact (A2A spec compliant)
- `extractA2AResult` loops ALL artifact objects and ALL parts within each
- Single file → backward-compat `{"artifact":{}}` shape
- Multiple files → `{"artifacts":[...]}` plural shape
- Orchestrator fans out each artifact to `emitArtifactEvent` + `run_artifacts` independently
- Strips both keys before LLM sees the result

### Streaming (SendStreamingMessage / SSE)
- `AgentConfig.SupportsStreaming` set from agent card `capabilities.streaming` on discover
- `invokeA2AStreaming`: `bufio.Scanner` SSE reader, `onArtifact` callback per `lastChunk:true` event
- Wire format: `"role":"ROLE_USER"` (string, not int), camelCase JSON tags (`artifactUpdate`, `lastChunk`)
- Non-streaming agents fall through to `InvokeForRun` transparently
- **All worker replicas must be rebuilt together** — Temporal load-balances across all workers on same task queue

### docu-writer agent
- Model: `claude-haiku-4-5-20251001` (async) — ~15-25s vs ~84s with sync Sonnet
- Formats: `html`, `markdown`, `pdf` (fpdf2)
- PDF: Claude → Markdown → fpdf2 → `bytes(pdf.output())` → `part.raw` (NOT `part.data`)
- `part.data` is `google.protobuf.Value` (JSON only) — cannot hold binary bytes
- Markdown fence stripping applied before rendering

### a2a-stream test agent (v1.2.0)
- Streams ~16 text words word-by-word (0.1s apart)
- Emits `stream_report.html` (`text/html`, text part with filename)
- Emits `stream_output.zip` (`application/zip`, raw bytes via `part.raw`)
- `capabilities.streaming: true` → `supports_streaming=true` set automatically on discover

---

## Canvas A2A Agent Builder — all phases complete

| Phase | What | State |
|---|---|---|
| A | Step config panels (LLM/HTTP/Transform/Response/Input forms) | ✅ |
| B | Skill editor, node library, data-flow subtitles, round-trip serialization | ✅ |
| C | `kind:"data"` part input mode; variadic `extraVars` in interpreter | ✅ |
| D | `a2a-go/v2` SDK replaces hand-rolled JSON-RPC dispatch; 12 tests (S1-53) | ✅ |
| Compiler | `go/internal/agentgen/compiler.go` — Compile + topoSort + DFS cycle detection | ✅ |
| Publish | `go/internal/admin/service/agent_definitions_publish.go` — compile + 3-table atomic CTE | ✅ |
| Binding UI | `AgentCredentialPanel` in applications page — per-slot credential entry | ✅ |
| Runtime wiring | `InvokeForRun` in `agentregistry` + `GetBindingID` | ✅ |
| Debug mode | Browser-side pipeline step-through with per-session provider+model+key (all 4 providers) | ✅ |
| Bug fixes | polJSON unmarshal, AllowedSkillIDs enforcement, skill selection by ID, slug cache | ✅ |
| BuildValidator UI | Debounced backend validation, node/field highlighting, issues panel, Publish gate | ✅ |

### Key security constraints (always in force)
- Credentials decrypted per-request, held only in `InvocationContext.Credentials`, never logged/persisted
- Port 9300 NOT in Traefik — agent-runtime is internal Docker network only
- `TaskState` has no credentials — re-decrypted from binding on each resume
- Binding invariant: `DefinitionID` pinned at publish, mismatch → 409

---

## LLM key architecture (current)

- Per-app keys stored in `applications.provider_keys` JSONB (AES-GCM encrypted)
- Format: `{"anthropic": {"ct": "enc:...", "hint": "XXXX"}}` (new) or `{"anthropic": "sk-ant-..."}` (legacy flat)
- **No global key fallback** — apps with no key get an explicit error (non-retryable Temporal failure)
- Worker: `resolveProvider` returns error when `cfg.LLMAPIKey == ""`
- Agent-runtime: `anthropicLLMFactory.NewProvider` returns error when `apiKey == ""`
- UI: Runtime tab in Applications view → provider + model + API key per app

---

## App-level agent params — fully complete (2026-08-23)

Backend + frontend + tests all done. See commits around 2f29cd3.

---

## App-level global named parameters — fully complete (2026-08-25)

**All 4 phases shipped (commits d2c4283..1e5c76c). DB migration applied.**

What was built:
- `db/045_app_global_params.sql` — `applications.app_params` JSONB column (AES-GCM secrets / plaintext non-secrets)
- DAL: `GetAppParams`, `SetAppParam`, `DeleteAppParam` on `*DB`
- Service: `GetAppParams`, `SetAppParam`, `DeleteAppParam`, `GetPlaintextAppParams`
- REST: `GET/PUT/DELETE /admin/applications/{id}/app-params[/{name}]`
- Compiler: `collectAppParamRefs` → `AgentSpec.AppParamRefs`; `AppParamRef` on `HTTPStepConfig`; `ModelOverrideParamRef` on `LLMStepConfig`
- Interpreter: `AppParamRef` (global) takes precedence over `AppParamKey` (per-binding); `injectAuthParam` helper; `ModelOverrideParamRef` takes precedence over `ModelOverrideParamKey`
- Runtime: `decodeAppGlobalParams` pure helper (testable); `loadAppGlobalParams` wired into `handle()` after `AgentParams`; fixed `plain:` prefix check (was dead code in error branch)
- Frontend (`api.ts`): `AppGlobalParam` interface + `getAppParams`/`setAppParam`/`deleteAppParam`
- Frontend (`RuntimeView.tsx`): "App Global Parameters" section — add/update/remove; type selector; secret masking
- Frontend (`RightPanel.tsx`): HTTP node → toggle Per-binding / App-global source; LLM node → `model_override_param_ref` free-text field
- Tests: S1-62 (AGP-1..8 service), S1-63 (CMP-10..14 compiler), S1-64 (INT-10..14 interpreter), S1-65 (RT-20..24 runtime), S1-66 (HTTP-20..25+ handler) — 34 new tests

**DB migration must be applied before deploying:**
```bash
docker cp db/045_app_global_params.sql them-postgres:/tmp/045.sql
docker exec them-postgres psql -U them -d them -f /tmp/045.sql
```

**Containers to rebuild after deploying:**
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge them-agent-runtime
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge them-agent-runtime
```

---

## Canvas Agent LLM Node Overrides — fully complete (2026-08-25)

**Removes old `model_override_param_ref`/`model_override_param_key` mechanism entirely.**
**No DB migration needed — uses existing `app_agent_bindings.config_overrides` JSONB column.**

What was built:
- `AgentSpec.LLMNodes []AgentLLMNodeSpec` — compiler collects all LLM steps (provider+model from canvas config) via `collectLLMNodes`; old `AppParamRefs`/`AgentAppParamRef` removed
- `InvocationContext.NodeLLMOverrides map[string]NodeLLMOverride` — per-node override map; loaded from `config_overrides["llm_nodes"][nodeID]` in agent-runtime via `extractNodeLLMOverrides`
- Interpreter `execLLM` reads `NodeLLMOverrides[step.ID]` before falling back to compiled provider+model
- DAL: `GetAgentLLMNodes` (reads spec `llm_nodes` + binding override map) + `UpsertNodeLLMOverride` (jsonb_set into `config_overrides["llm_nodes"][nodeID]`)
- Service: `GetAgentLLMNodes` + `PutNodeLLMOverride` (validates non-empty)
- REST: `GET /admin/applications/{id}/agents/{agent_id}/llm-nodes` + `PUT /agents/{id}/llm-nodes/{node_id}`
- Frontend proxy routing: added `/agents/[^/]+/llm-nodes` pattern to Go bridge
- RuntimeView: "Canvas Agent LLM Nodes" section — one card per LLM node, provider+model dropdowns (all 4 providers; ✓ marks those with saved key), Save per node
- RightPanel: removed MODEL OVERRIDE (APP GLOBAL PARAM) panel from LLM canvas node config
- Debug panel: replaced hardcoded `__anthropic_key` with `__debug_provider` (dropdown) + `__debug_model` (dropdown) + `__debug_api_key` (password) for session-level override
- Debug proxy (`/api/debug/llm`): now supports anthropic, openai, groq, gemini; normalizes all responses to Anthropic `content[{type,text}]` format for frontend consumption

**Action required after deploy:** Re-publish any canvas agents to get `llm_nodes` populated in the spec.

---

## MCP-1 Migration Status

**Completed in this session:**
- `them-mcp-service` binary — `go/cmd/mcp-service/main.go` + `go/internal/mcp/` (all files: config, dal, client, registry, leader, supervisor, health, executor, server, health_test)
- `Dockerfile.mcp-service` + `docker-compose.yml` service entry (port 8010, Traefik disabled)
- DB migrations applied: `db/041_mcp_servers.sql` (them.mcp_servers) + `db/042_mcp_app_credentials.sql` (them.app_mcp_credentials)
- Admin CRUD API: `go/internal/admin/dal/mcp_servers.go`, `go/internal/admin/service/mcp_servers.go`, `go/internal/admin/mcp_servers.go`
- Service added to router: `go/internal/admin/router.go` (tenantScoped group)
- Dal interface updated: `go/internal/admin/service/service.go`
- All fake Dal structs updated: `service_test.go`, `agent_definitions_test.go`, `definitions_publish_test.go`, `tenant_isolation_test.go`
- 11 new unit tests: `go/internal/admin/service/mcp_servers_test.go`
- Docs updated: REDIS.md, SCHEMA.md, CLAUDE.md (trigger map + container map), TEST_INDEX.md (S1-62), CURRENT.md

**What's done (UI-1 — commit 7c0ec59):**
- `frontend/src/lib/api.ts`: MCPServer, MCPTool, MCPCredentialMeta types + 9 themApi methods
- `frontend/src/components/Sidebar.tsx`: MCP Store nav entry after Agents
- `frontend/src/app/admin/mcp-servers/page.tsx`: full card grid + centered modal properties panel (General + Status & Tools tabs) + tool manifest viewer + ProbeButton + CreateModal + Sidebar

**What's done (MCP-2 — commit 901dc36):**
- DB: `db/043_mcp_streamable.sql` — constraint updated to include `streamable-http`, drop `stdio`
- `go/internal/config/config.go`: `MCPServiceURL` field read from `MCP_SERVICE_URL` env
- `go/internal/admin/mcp_servers.go`: `Probe` handler proxies to `them-mcp-service /internal/probe/{id}`
- `go/internal/admin/router.go`: `BuildRouter` gains `mcpServiceURL` param
- `docker-compose.yml`: `MCP_SERVICE_URL=http://them-mcp-service:8010` added to `them-go-bridge`
- Frontend: transport list updated (`streamable-http` default, `http`/`sse` legacy, `stdio` removed)
- Frontend: auth info banner in Create modal explains per-app credential flow

**What's done (UI-2 — commit 900b8d4):**
- `frontend/src/app/admin/applications/components/MCPCredentialsView.tsx`: new — per-application MCP credential management (key-set badge, Save/Update/Remove, flash feedback)
- `AppCard.tsx`: MCP button added (indigo, `electrical_services` icon)
- `ListView.tsx` + `page.tsx`: `mcp-credentials` view state + `openMCPCredentials` handler

**What's done (UI-3 — commit 9bf1328):**
- `go/internal/agentgen/spec.go`: `StepMCPCall = "mcp_call"` constant added
- `go/internal/agentgen/nodes.go`: `mcp_call` stub NodeDef registered (label "MCP Tool", 🔌, single-input/output, Execute=nil)
- `go/internal/agentgen/noderegistry_test.go`: allStepTypes 11→12; KnownStepTypesCount 11→12; mcp_call in StubTypesHaveNilExecute
- `frontend/src/lib/nodeRegistry.ts`: `mcp_call` UI supplement (indigo accent, server/tool summary)
- `frontend/src/app/admin/agents/builder/components/RightPanel.tsx`: `mcp_call` properties panel — server dropdown, tool dropdown/input, args template, output var, credentials info banner
- `them-go-bridge` rebuilt and restarted — `mcp_call` now appears in `GET /admin/node-types`

**What's NOT done yet (MCP-3 onward):**
- MCP-3: runtime executor wired into agent-runtime tool calls (StepMCPCall Execute function)
- `them-mcp-service` not yet started in production (needs `--profile mcp` in compose)

---

## Next recommended task

### Step 1 — Deploy app_params and E2E validate (immediate)

The app global params feature is fully coded and tested but containers have not been rebuilt yet. Do this before starting any new feature:

```bash
# 1. Apply DB migration (if not already done)
docker cp db/045_app_global_params.sql them-postgres:/tmp/045.sql
docker exec them-postgres psql -U them -d them -f /tmp/045.sql

# 2. Rebuild and restart
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge them-agent-runtime
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge them-agent-runtime

# 3. Smoke test
curl -s -H "Authorization: Bearer $JWT" http://localhost:8088/api/v1/admin/applications/<app_id>/app-params | jq .
```

E2E validation path:
1. Open app → Runtime tab → "App Global Parameters" section visible
2. Add a `string` param (e.g. `target_city: "Tel Aviv"`) → appears in list with value
3. Add a `secret` param (e.g. `api_key`) → appears masked with hint
4. In agent builder HTTP node → toggle to "App global param" → type param name → publish
5. Invoke the agent via playground → confirm the param value reaches the HTTP step

### Step 2 — MCP-3: StepMCPCall runtime executor

The `mcp_call` canvas node type is registered with `Execute: nil`. Implement the executor in `go/internal/agentgen/` that:
- Reads `mcp_server_slug`, `tool_name`, `args_template` from step config
- Looks up the MCP server via `them-mcp-service` HTTP internal API
- Calls the tool, returns result as pipeline var
- Uses per-app MCP credential from `app_mcp_credentials` (already stored)

### Step 3 — Auth admin CRUD Go proxy (lower priority)
- `them-auth-service` (Python, port 8701) still serves user/role/team management
- Frontend hits it directly via its own Traefik routes
- When ready: implement Go proxy at `go/internal/authadmin/` + Traefik redirect

### Step 4 — Wave 9 tenant items
- Session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims
- Not started — begin only after Steps 1–2 are complete

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

1. **MCP-3 not implemented** (moved from #2) — see below.

2. **MCP-3 not implemented** — `mcp_call` canvas step has `Execute: nil`. The runtime stub is registered but will return "not yet implemented" if invoked. MCP execution via `them-mcp-service` not yet wired.

3. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. Frontend hits it directly. No Go proxy until we decide to retire the Python binary.

4. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims. Not started.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `go/internal/authserver/`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`
- **Python is permanently retired.** `them-bridge` and `them-worker` MUST remain behind `profiles: [legacy]`.
- **No global LLM key fallback.** Apps with no key get an explicit error.
- **No secrets in Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history.**
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** Global key must not be written or read.
- **EP cache key is `"{tenantID}:{slug}"`.** Invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{slug}"`.
- **`entry_points.tenant_id` is NOT NULL.** `UNIQUE(tenant_id, slug)` enforced at DB level.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** — never globally by name.
- **Project name: `them_gateway`** — required for all compose commands.

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Update this file at session end — do NOT create new NEXT_SESSION_*.md files.
3. ADRs are permanent — never archive them.
4. Trust code over docs; update docs when they diverge.
5. Documentation changes ship in same commit as the code they describe.
