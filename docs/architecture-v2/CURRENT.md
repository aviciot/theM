# Current Session State — the-M
# Last updated: 2026-08-29
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `(pending commit)` — feat(agentgen): Pre-4-C parity — per-attempt timeout, vars isolation, typed non-retryable, idempotency guard in activity path, method-aware UI defaults

Recent commits (newest first):
```
(pending)  feat(agentgen): Pre-4-C parity — per-attempt timeout, vars isolation, typed non-retryable, idempotency guard in activity path, method-aware UI defaults
7f6eb97  feat(agentgen): Pre-4-C hardening — retry/backoff, non-retryable stops, idempotency guard, policy UI
a0457bd  feat(agentgen): Pre-4-C — unified ExecutionPolicy, per-node timeout, NoResult fix
d442def  docs(current): record approved ExecutionPolicy plan
dceb844       docs(temporal): fix all contradictions in TEMPORAL_EXECUTOR_DESIGN.md vs Phase 4-B code
68da87c       feat(temporal): Phase 4-B — CanvasAgentWorkflow + ExecuteStepActivity + 16 conformance tests
a1adbe8       feat(agentgen): Phase 4-A — ExecutionBackend field, ExecuteNodeForActivity adapter, ActivityIC
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
go test ./...  — all packages, 0 failures (verified 2026-08-29, Pre-4-C parity — full test suite)
S1-72: 17 plan compiler tests (unchanged from previous session)
S1-73: 25 LocalExecutor tests (20 prior + 5 new: EP-L9 per-attempt timeout, EP-L10 vars isolation,
        EP-L11 NonRetryableError interface, EP-L12/EP-L13 idempotency guard in ExecuteNodeForActivity)
S1-74: 3 DAG E2E smoke tests (BranchConvergence true/false + ParallelTransforms both run)
S1-75: 16 Phase 4-A tests (NA-01..NA-16: ExecuteNodeForActivity, ActivityIC, ExecutionBackend)
S1-76: 18 Phase 4-B+EP tests (16 prior + 2 new CT-EP1/CT-EP2: NoResult bug fix + policy in plan)
S1-54: 18 node registry tests (added TestNodeRegistry_ParallelIsImplemented)
go test ./... total: 1061

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
| Data-flow contracts | `VarRef`, `DeriveInputs/DeriveOutputs` on all 11 nodes, Stage 5 path-sensitive `validateDataFlow` | ✅ |
| Frontend spec consumer | `AgentValidationReport.StepContracts`; RightPanel READS/WRITES from compiled contract post-validate | ✅ |
| Explicit bindings Stage A | `PortDef`, `VarRef.SourceStep/SourcePort`, `Binding`/`canvasStep.Inputs`, `resolveBindings`, `validateBindings`, `BROKEN_BINDING`; backward-compat | ✅ |
| Explicit bindings Stage B | `api.ts` Binding/VarRef/AgentStepDoc.inputs; `nodeRegistry.ts` PortDef/input_ports/output_ports; `types.ts` StepData.inputs; `page.tsx` save/load round-trip | ✅ |
| Explicit bindings Stage C | StepNode data-port handles (orange input squares, indigo output squares); DataEdge dashed wire; onPipeConnect data-edge branch; isPipeConnectionValid skips data edges; both save paths derive inputs from data edges; load path reconstructs data edges from step.inputs | ✅ |
| Multi-port Phase 1+2 | NodeDef as single source of truth: `PortDef.Color/MaxConnections`, `ControlOutputPorts`, `DynamicOutputSource` in Go registry; `resolveInputPorts`/`resolveOutputPorts` in nodeRegistry.ts; StepNode zero-conditional rewrite; branch true/false named control handles; transform dynamic output ports from config | ✅ |
| Multi-port Phase 3 | BundleEdge: groups data edges between same node pair into port-rail cable visual; EdgeLabelRenderer dots+labels; count badge; `applyBundleGroups()`; data handles invisible (1×1) — geometry only; `useStore(s.edges)` for wired-port detection; Dagre height scales with port count | ✅ |
| Unified port model | All flow (→) and data ports in one unified hover-reveal list per side; no separate ctrl-in/ctrl-out center handles; `PortDot` scale+opacity CSS transition; wired ports permanently visible; branch gets named true/false flow out ports; port alias rename in RightPanel READS section | ✅ |
| Canvas port UX clean rewrite | Removed all backward-compat code (PORTS_V2 flag, PortDot component, breathing animation, legacy paths). `PortsPopover` as absolutely-positioned child of `StepNode`; ctrl handles as always-visible 18px circles with ‹/› arrows. `BundleEdge` rewritten: single bezier + circular N-badge + MappingSheet popover; `callDeleteMapping` module registry for delete callbacks. `resolveOutputPorts` always includes static ports. | ✅ |
| Canvas port bug fixes (cd9632f) | CRITICAL: PortsPopover duplicate Handle IDs → display-only (no `<Handle>` JSX). HIGH: PortsPanelContext broadcast closes all popovers on node/pane click. MEDIUM: card height now counts only data ports. LOW: dead `hasCtrlIn`/`hasCtrlOut` vars removed. | ✅ |

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

**What's done (MCP-3 — commit cf882d8):**
- `spec.go`: StepMCPCall constant + MCPCallConfig struct
- `nodes.go`: mcp_call node registered with Validate, Execute, DeriveInputs, DeriveOutputs; 11→12 types
- `interpreter.go`: MCPCaller interface + WithMCPCaller; execMCP + renderMCPArgs
- `mcp_caller.go`: HTTPMCPCaller — POST /internal/execute on them-mcp-service (stateless per call)
- `cmd/agent-runtime/main.go`: wires MCPServiceURL from config into HTTPMCPCaller; nil when URL unset
- 10 new tests MCP-1..10; go test ./... 815→825

**What's done (MCP-3 E2E — commit e6f9660):**
- `docker-compose.yml`: `MCP_SERVICE_URL=http://them-mcp-service:8010` + `depends_on: them-mcp-service` in agent-runtime
- Full E2E validated 2026-08-27: create → validate → publish → bind → A2A `SendMessage` → agent-runtime → them-mcp-service `/internal/execute` → `get_latest_session` tool → result written to `session_data` var → artifact in A2A response
- Failure paths: unknown tool → 422, unknown server slug → 422
- `them-mcp-service` already runs without a profile (default service)
- A2A SDK v2.5: method is `SendMessage` (not `message/send`), params.message must have `messageId`

**MCP-3 is FULLY COMPLETE and live-verified.**

---

## Data-flow architecture — current state (2026-08-27)

### What's live
- **`VarRef` + `DeriveInputs/DeriveOutputs`** on all 11 node types in `go/internal/agentgen/nodes.go`
- **`StepSpec.Inputs/Outputs []VarRef`** (omitempty) — populated by compiler, absent from canvas JSON, present in compiled `AgentSpec`
- **Stage 5 `validateDataFlow`** — path-sensitive available-definitions lattice (intersection over predecessors). A var is guaranteed only if written on every execution path to the reading step. Branch convergence handled correctly.
- **`AgentValidationReport.StepContracts`** — validate endpoint returns compiled `{inputs, outputs}` per step
- **RightPanel READS/WRITES panel** — uses authoritative compiled contract post-validate (shows `✓ compiled contract`), falls back to `extractNodeVars` (heuristic) for live pre-validate UX
- **`nodeVars.ts`** — unchanged; still used for live edge labels and pre-validate UX

### Boundary: canvas JSON stays clean
`Inputs`/`Outputs` are NEVER written to canvas JSON (the `definition` column). They exist only in:
1. The compiled `AgentSpec` (persisted to `agent_runtime_specs.spec`)
2. The validate endpoint response (`step_contracts` field, transient)

### What remains (per DATAFLOW_EXPLICIT_FEASIBILITY.md)
- Step 6: **COMPLETE** (fa879b7) — ExposedVars removed from TransformStepConfig; DB data-migrated; frontend cleaned up
- Stage 6: **COMPLETE** (0edcf2a) — Scoped input resolution + output-only promotion in interpreter.executeStep; ErrContractViolation type; execTransform simplified; 12 new CONT tests
- Explicit bindings (wiring vars between steps with explicit edges) — **Stages A/B/C COMPLETE**: Go compiler (PortDef, resolveBindings, validateBindings, BROKEN_BINDING, 10 BND tests); TypeScript types; canvas port handles (orange data-in, indigo data-out) + DataEdge dashed wire; onPipeConnect data-edge path; save/load round-trip; backward-compat. Runtime unchanged.
- Structured per-var trace events — not yet (requires trace sink design)
- Temporal/ADK integration — not yet

---

## DAG Execution Engine — complete through Phase 3 + hardening (2026-08-29)

Goal: upgrade the Canvas execution engine from sequential-only to real DAG fan-out/join.

| Phase | What | State |
|---|---|---|
| 0 | `ExecutionPlan`/`PlanNode`/`JoinMode` types + `CompileExecutionPlan()` + 4 tests (S1-72) | ✅ commit `0d99d68` |
| 1 | `ExecutionBackend` interface + `LocalExecutor` (goroutine fan-out, wait_all join, deep-copy, cancel) + 6 tests (S1-73) | ✅ commit `f5737c0` |
| 2 | Race detector validation — `go test -race ./...` green; `Interpreter.clone()` fix for `nextStepOverride` contention | ✅ commit `ddaca40` |
| 3 | Canvas unlock — `max_out: 0` on LLM/HTTP/A2ACall/HumanWait/MCPCall in `nodes.go` | ✅ commit `ddaca40` |
| Hardening | Branch-aware joins (`JoinBranchMerge` vs `JoinWaitAll`); deterministic merge (predecessor-keyed map + JoinOf order); causal error preservation; 4 new tests (S1-72/73 expanded) | ✅ commit `82c5be4` |
| Compiler fix | `classifyJoin()` rewrite — fixes fan-out source vs fan-out target level confusion; MixedFanOut + BranchMerge tests | ✅ commit `d471f9f` |
| Wired | `cmd/agent-runtime/main.go` uses `LocalExecutor` (not sequential `Interpreter.Execute`) | ✅ commit `a9528d6` |
| E2E tests | `agentgen_test.go` — 3 smoke tests through CompileExecutionPlan + LocalExecutor + real node types (S1-74) | ✅ commit `bd2ffbd` |
| StepParallel | `StepParallel.Execute` implemented (no-op fan-out coordinator); removed from stub list | ✅ commit `b5767b0` |
| 4-A | `ExecutionBackend` field in `AgentSpec`; `ExecuteNodeForActivity` adapter; `ActivityIC`; `Interpreter.Clone()`; 16 tests (S1-75) | ✅ commit `a1adbe8` |
| 4-B | `CanvasAgentWorkflow` + `ExecuteStepActivity` + conformance tests CT-01..CT-10 + CT-A..CT-F in `internal/temporal/`; 16 tests (S1-76) | ✅ commit `68da87c` |
| Pre-4-C | Unified `ExecutionPolicy` — `NodeDef` defaults, compiler resolution, `LocalExecutor` timeout, Temporal policy wiring, NoResult bug fix; 13 new tests (EP-1..9, EP-L1/2, CT-EP1/2) | ✅ |
| Pre-4-C hardening | LocalExecutor retry loop + backoff; non-retryable short-circuit; idempotency guard; `RequiresIdempotencyKey` logic fix; frontend Execution Policy section in node Properties; 9 new tests (EP-2b, EP-L3..EP-L8) | ✅ |
| 4-C | `TemporalExecutor`, `them-dag-worker`, `agent-runtime` wiring, Docker service | ⬜ |
| 4-D | Frontend publish toggle | ⬜ |
| 5 | Loop, HumanWait, A2A in DAG context | ⬜ |

### DAG join semantics (hardening summary)
- **JoinWaitAll**: join node whose predecessors originate from a non-Branch fan-out (e.g. LLM with `len(Next)>1`). All branches always run — must wait for all.
- **JoinBranchMerge**: join node whose predecessors are ALL direct arm-children of a single Branch step. Only one arm runs — first arrival continues; subsequent arrivals are silently dropped.
- Detection in `classifyJoin()`: JoinBranchMerge requires (1) every predecessor has exactly one parent, (2) that parent is a Branch step, (3) all share the same Branch parent B, (4) B's full Next set == predecessor set. Anything else → JoinWaitAll.
- Merge is deterministic: `joinState.arrived` is `map[string]map[string]PipelineVars` (predecessor-keyed); merge iterates `JoinOf` slice in order — later entries win on key collisions.
- `drainFirstCausalError`: reads all errors from buffered chan, prefers first non-`context.Canceled` over Canceled. Causal error survives sibling cancellation.

### Key files
- `go/internal/agentgen/spec.go` — `ExecutionPlan`, `PlanNode`, `JoinMode` (JoinNone/JoinWaitAll/JoinBranchMerge/JoinWaitAny)
- `go/internal/agentgen/plan_compiler.go` — `CompileExecutionPlan()`, `classifyJoin()`, `NodeByID()`
- `go/internal/agentgen/executor.go` — `ExecutionBackend` interface
- `go/internal/agentgen/local_executor.go` — `LocalExecutor` with goroutine fan-out + per-goroutine `clone()` + `joinState` + `drainFirstCausalError` + `deepCopyVars`
- `go/internal/agentgen/nodes.go` — canvas nodes; all fan-out-capable nodes have `MaxOut: 0` (unlimited)

---

## Next recommended task

### Phase 4-C: TemporalExecutor + them-dag-worker (start here — Pre-4-C complete)

Phases 4-A and 4-B are committed and green. Phase 4-C wires everything end-to-end:

1. `go/internal/temporal/temporal_executor.go` — `TemporalExecutor` implementing `agentgen.ExecutionBackend`:
   - `Execute(ctx, ic, plan, initial)` → `client.ExecuteWorkflow(ctx, opts, CanvasAgentWorkflow, input)`
   - Workflow ID: `"canvas:{agentID}:{invocationID}"` (stable across retries)
   - Re-attach on `AlreadyStarted`: `client.GetWorkflow(ctx, id, "")`
   - Block via `run.Get(ctx, &out)` — returns on ctx cancel or workflow completion
   - On ctx cancel: `client.CancelWorkflow(background5s, id, runID)` (best-effort)

2. `go/cmd/dag-worker/main.go` — `them-dag-worker` binary:
   - Connects to Temporal, registers `CanvasAgentWorkflow` + `ExecuteStepActivity` on `"canvas-dag-nodes"`
   - Wires `CanvasAgentActivities{InterpTemplate, Loader}` where `Loader` is a real DB-backed `ContextLoader`
   - `ContextLoader.Load` must query with all 4 IDs: `TenantID`, `ApplicationID`, `AgentID`, `BindingID`

3. `go/cmd/agent-runtime/main.go` — add Temporal client + `execution_backend` branch:
   - When `AgentSpec.ExecutionBackend == "temporal"`: use `TemporalExecutor`
   - Otherwise: use existing `LocalExecutor` (no change)
   - Init Temporal client only when `cfg.TemporalEnabled`

4. `Dockerfile.dag-worker` + `docker-compose.yml` `them-dag-worker` service under `profiles: [temporal]`

5. `go/internal/config/config.go`: add `DAGWorkerTaskQueue` (default `"canvas-dag-nodes"`), `DAGWorkerConcurrency` (default 20), `DAGWorkflowTimeout` (default `"12m"`)

6. Run conformance tests against real Temporal dev server (`docker compose --profile temporal`)

7. Live smoke test: publish one canvas agent with `execution_backend: "temporal"`, invoke it, verify Temporal UI shows node-level activity history

Key design constraints (from `TEMPORAL_EXECUTOR_DESIGN.md §16`):
- Task queue: `"canvas-dag-nodes"` (distinct from `"them-orchestration-go"`)
- `ContextLoader.Load` scopes by all 4 IDs — never just `BindingID`
- Workflow timeout: 12 min (`DAGWorkflowTimeout`), activity `StartToCloseTimeout`: from `node.Policy.TimeoutSeconds` (default 300s)
- Retry policy comes entirely from `node.Policy` (no hardcoded switch)

### Other tasks (lower priority)
- Phase 4-D: frontend publish toggle for `execution_backend` (Local/Temporal) in builder top bar
- DAG live canvas validation — smoke test a Branch/Parallel canvas agent live
- Auth admin CRUD Go proxy — when `them-auth-service` Python retirement is decided

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

1. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. Frontend hits it directly. No Go proxy until we decide to retire the Python binary.

2. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims. Not started.

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
