# Current Session State — the-M
# Last updated: 2026-09-04 (Step 19 F1+F2 complete)
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`

Recent commits (newest first):
```
61af130  feat(rls): F1+F2 — verify callers; enable RLS on run_steps/run_usage/artifacts/task_messages/middleware_audit
3496994  feat(rls): E1+E2 — migrate callers; enable RLS on runs/tasks/run_artifacts
e61d81c  feat(rls): E0 — backfill tasks.tenant_id NOT NULL
5031a32  docs(rls): E0+E1+E2 complete — update progress tracker and CURRENT.md
1659b34  test(rls): TestRLS_TwoTenantFullIsolation + fix them_owner schema USAGE
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
- **`them-bridge` (Python FastAPI) is permanently removed** — not in `docker-compose.yml`
- **`them-worker` (Python Temporal worker) is permanently removed** — not in `docker-compose.yml`. All WS/SSE sessions submit to `them-orchestration-go` handled by `them-go-worker`. `them-orchestration` queue is empty.
- `them-go-bridge` is the active API gateway on port 8002
- `them-go-worker` is the active Temporal worker — **no explicit profile in `docker-compose.dev.yml`**, starts by default
- `them-agent-runtime` runs 2 replicas (port 9300 internal), profile `[agents]`
- Frontend `THE_M_API_URL` points to `http://them-traefik:8088`
- Named Docker volumes: `them-postgres-data`, `them-redis-data` — `external: true` (`them-logs` volume removed — Python bridge is deleted)
- **Project name: `them_gateway`** — required for all compose commands

---

## Current migration slice

**Step 19 — Postgres Row-Level Security**

Design: `docs/design/rls-option-a-plan.md` (v3, commit 61730f3) — COMPLETE, reviewed, unblocked.
Progress tracker: `docs/STEP19_RLS_HANDOVER.md` — read this before starting implementation.

Implementation progress: **Phases A–F complete**. HEAD: `61af130`.

### Next recommended task for a new session

Start `docs/STEP19_RLS_HANDOVER.md` Phase **G1** — migrate callers for `llm_providers`, `audit_logs`, `middleware_jobs`, `authserver.GetTenantIDPConfig`:
- `llm_providers` needs a split policy (SELECT allows platform NULLs + own rows; write own only)
- `audit_logs` — them_app INSERT only (no SELECT, already in 070 grants)
- `middleware_jobs` — gateway enqueue via TenantTx; worker Claim/Complete/Fail via AdminQuerier
- authserver `GetTenantIDPConfig` → AdminQuerier (pool via Pools.Admin)
- After G1, G2: `db/077_rls_phase_g.sql`

### Known blockers / pre-conditions

None that block A1. The design is fully approved. Proceed when ready.

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
them-bridge (Python)  ❌ REMOVED — not in docker-compose.yml
them-worker (Python)  ❌ REMOVED — not in docker-compose.yml (them-orchestration queue empty; all traffic on them-orchestration-go)
them-dag-worker (Go)  ✅ Running — polls canvas-dag-nodes for CanvasAgentWorkflow
```

---

## Go route ownership (all confirmed via Traefik labels)

All routes are owned by `them-go-bridge` (`them-go-bridge-svc`, port 8002).
A catch-all router at priority 90 (`them-go-catchall`, `PathPrefix /`) ensures all unmatched paths reach Go.
Explicit routers at priority 110–150 still win over the catch-all.

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

### App entry points (WS/SSE/A2A/Voice)
- `GET /apps/{app_slug}/{ep_slug}/ws`
- `GET|POST /apps/{app_slug}/{ep_slug}/sse`
- `POST /a2a/{app_slug}/{ep_slug}` (A2A JSON-RPC)
- `GET /a2a/{app_slug}/{ep_slug}/.well-known/agent.json` (per-agent card)
- `GET|POST /apps/{app_slug}/{ep_slug}/voice/chat|stream|transcribe|tts`
- `GET /ws/orchestrate/{orch}/{ep}` (two-segment legacy path)
- `GET|POST /sse/orchestrate/{orch}/{ep}` (two-segment legacy path)

### Dashboard
- `GET /ws/dashboard`

### Health
- `GET|HEAD /health/live`, `/health/ready`


### Not in Go (no handler or Traefik route)
- `GET /api/v1/admin/users`, `/roles`, `/teams` — auth admin CRUD (served by `them-auth-service` on port 8701 directly from frontend; no Go handler needed unless we want to proxy it)
- `GET /runs/context/{ctx}/artifacts` — not used by admin UI
- Applications export/import/restore — Python-only, not migrated

---

## DB schema state (live)

All migrations applied through `db/072_rls_phase_c.sql` (Step 19 Phase C complete):

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
| `db/048_application_slug.sql` | ✅ applied — `applications.slug` column, `UNIQUE(tenant_id,slug)`, EP uniqueness relaxed to `UNIQUE(application_id,slug)` |
| `db/049_ep_agent_card.sql` | ✅ applied |
| `db/050_middleware_pipeline.sql` | ✅ applied — `run_artifacts` scan columns, `middleware_jobs`, `middleware_audit`, `applications.security_config` |
| `db/051_quarantine.sql` | ✅ applied — `quarantine_artifacts` table, `run_artifacts.data` nullable + `storage_key` column, `middleware_jobs.quarantine_id` |
| `db/052_middleware_jobs_nullable_artifact.sql` | ✅ applied — `middleware_jobs.artifact_id` made nullable; FK re-added allowing NULL; fixes quarantine-first FK violation |
| `db/053_*` through `db/071_rls_phase_b.sql` | ✅ applied — B1+B2 RLS on mcp_servers/tenant_group_mappings/agent_definitions/agent_runtime_specs |
| `db/072_rls_phase_c.sql` | ✅ applied — C2 RLS on agents/orchestrators/applications/entry_points/access_tokens |
| `db/073_rls_phase_d.sql` | ✅ applied — D2 EXISTS-based RLS on app_agent_bindings/app_orchestrators/app_mcp_credentials/middleware_wirings |

---

## Test state

```
go test ./...  — 53 packages, 0 failures (verified 2026-09-02, Session C scan subscriber)
S1-84: 6 gate tests (quarantine-first)
S1-85: 4 admin security_config handler tests
S1-87: 25 middleware pipeline + AV scanner tests
S1-88: 5 job DAL quarantine-path tests
S1-89: 3 storage client tests
S1-30: 16 artifact download handler tests (MinIO path, 410 infected, MinIO error 500)
S1-90: 4 orchestrator scan subscriber tests (FileScanningEvent, Clean, Infected, Timeout)
S1-14: 30 A2A server tests
S1-72..S1-83: all prior DAG/canvas/A2A tests passing
S2-06: 3 integration-tagged Temporal E2E tests
Total go test ./...: 946

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
| Pre-4-C parity | Per-attempt timeout, vars isolation, typed non-retryable, idempotency guard in activity path, method-aware UI defaults; 5 new tests (EP-L9..EP-L13) | ✅ |
| Pre-4-C final | MCP mutating hard-clamp; removed string-match from `isNonRetryable`; fresh interp clone per retry; 3 new tests (EP-10, EP-L14, EP-L15) | ✅ commit `3a8f0f6` |
| Pre-4-C concurrency | Per-run `MaxConcurrentTasks` semaphore in `LocalExecutor` + `CanvasAgentWorkflow`; `DAG_WORKER_MAX_CONCURRENT_ACTIVITIES` config; `ResolveMaxConcurrentTasks`; 5 new tests (CONC-1..5) | ✅ commit `df4b19e` |
| 4-C | `TemporalExecutor`, `them-dag-worker`, `agent-runtime` wiring, Docker service | ✅ commit `0b68dcb` |
| 4-C hardening | 7 production blockers fixed: Compose env vars, fail-closed, stable workflow ID, policy concurrency, tenant-scoped DB queries, bounded cancel, integration tests | ✅ commits `1c44aa0`..`30f9f95` |
| 4-C gap-2 | 5 additional fixes: tenant-scope ALL lookups, safe errors, conditional Temporal overlay, HumanWait 24h timeout, real full-path E2E, binding 4-ID enforcement | ✅ commits `8d815cc`..`b3bd71a` |
| 4-D | Frontend execution_backend toggle (Local / ⚡ Temporal pill in top bar) | ✅ commit `7d39d44` |
| 5-A | StepLoop — LocalExecutor + Temporal + frontend config panel + durable loop architecture + canvas ports + gap fixes (compileLoopBodyPlan JoinOf/JoinMode, ExecuteBody onTerminal, bodyIterState isolation, BFS boundary, accum scoping) + 8 new tests (EP-LOOP-6/7/8, CT-LOOP-DURABLE-6/7, PC-LOOP-4/5/6) | ✅ |
| 5-B | HumanWait async — Phase 1 (commit `3b1052f`): HITLStore, PlanHasHumanWait, CanvasSubmitter/Signaler, Submit/SignalCanvasStep, executeSkill HITL async path, signalHITL; Phase 2 hardening (commit `0487797`): HITLHandle 6-field state machine (tenant_id, wait_token, state), UpdateWaitToken/TrySignal CAS/MarkDone, deterministic wait_token (sha256, no uuid), hitl_status workflow query handler, per-step timeout via workflow.Select, loop-body HumanWait, HITLRequestHandler (GetTask/SubscribeToTask/CancelTask), RedisA2ATaskStore (SDK taskstore.Store), signal endpoint moved to JWT-auth admin router `/admin/canvas-tasks/{task_id}/signal`; 20 total tests (HS-1..11, RT-HITL-1..5, CSIG-1..4) | ✅ commit `0487797` |
| 5-C | A2A Call node — `A2ACaller` abstraction, `HTTPA2ACaller`, depth tracking, self-call rejection, HumanWait+local validation, agent-runtime + dag-worker wiring | ✅ |
| 5-D | StreamOut node — `execStreamOut`, `StreamOutStepConfig`, `STREAM_OUT_MISSING_FROM_VAR` validation, `DeriveInputs` | ✅ |

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

## Middleware Security Pipeline — Phase 4 complete (2026-09-01)

### Overview
Pluggable per-application artifact security middleware that intercepts file artifacts from A2A agents before delivery to users.

### What was built

**Phase 1 — Foundation (prior session)**
- `db/050_middleware_pipeline.sql` — `run_artifacts.scan_status/scan_result/scanned_at`, `middleware_jobs`, `middleware_audit`, `applications.security_config`
- `go/internal/middleware/processor.go` — `Processor` interface, `Registry`, `Part`, `Result`
- `go/internal/middleware/config.go` — `SecurityConfig`, `AVScanConfig`, `MergeDefaults`, `Validate`, `EnabledProcessors`
- `go/internal/middleware/pipeline.go` — `Pipeline.Run` chaining with block-on-infected semantics
- `go/internal/middleware/job.go` — `JobDAL`: `Enqueue`, `Claim` (SKIP LOCKED), `LoadFileBytes` (reads `run_artifacts.data`), `Complete` (updates `run_artifacts.scan_status`), `Fail`, `WriteAudit`
- `go/internal/middleware/progress.go` — `ScanPublisher` → `them:scan:<artifactID>` Redis channel
- `go/internal/middleware/middleware_test.go` — 15 tests
- `go/internal/admin/security_config.go` — `GET|PUT /admin/applications/{id}/security-config`

**Phase 2 — ClamAV processor (prior session)**
- `go/internal/middleware/av/clamav.go` — INSTREAM protocol scanner (fail-open)
- `go/internal/middleware/av/clamav_test.go` — 9 tests (mock clamd)
- `go/cmd/middleware-worker/main.go` — worker binary polling middleware_jobs (8 goroutines)
- `Dockerfile.middleware-worker` — build + test at build time
- `docker-compose.dev.yml` — `them-clamd` + `them-middleware-worker` services (profile `security`)

**Phase 3 — Gateway intercept + download gate (this session)**
- `go/internal/middleware/gate.go` — `FileGate.Intercept()`: checks app security config (30s cache), fetches file from agent URL, stores in `run_artifacts` with `scan_status='pending'`, enqueues middleware_job; fail-open on any error
- `go/internal/middleware/pgx.go` — `PgxQuerier` adapter (implements `Querier` + `GateQuerier`)
- `go/internal/a2a/server.go` — `FileInterceptor` interface + `FileInterceptInput/Result` types; `WithFileGate` builder
- `go/internal/a2a/executor.go` — "file" event handler calls `fileGate.Intercept()` when set; replaces `download_url` with gated artifact URL `/api/v1/runs/{run_id}/artifacts/{artifact_id}`
- `go/internal/runrecorder/recorder.go` — `GetArtifactScanStatus()` lightweight query (no BYTEA)
- `go/internal/artifacts/handler.go` — Download gate: 202 when pending/scanning, 451 when infected, 200 when clean/disabled
- `go/cmd/them/main.go` — `fileGateAdapter` bridges `middleware.FileGate` to `a2a.FileInterceptor`; wired into A2A server via `WithFileGate`
- Tests: S1-30 +4, S1-84 (3), S1-85 (4)

**Phase 4 — Quarantine-first MinIO storage (this session)**
- `go/internal/storage/storage.go` — `*Client` wrapping minio-go: PutQuarantine, GetQuarantine, DeleteQuarantine, PutArtifact, PresignArtifact
- `go/go.mod` — added `minio/minio-go/v7 v7.3.0`
- `go/internal/config/config.go` — S3Endpoint/AccessKey/SecretKey/QuarantineBucket/ArtifactsBucket fields
- `go/internal/middleware/gate.go` — rewrite: writes bytes to MinIO quarantine, inserts `quarantine_artifacts` row (no BYTEA in Postgres), enqueues job with quarantine_id
- `go/internal/middleware/job.go` — EnqueueWithQuarantine; LoadFileBytes reads from MinIO; Complete promotes clean bytes to artifacts bucket / inserts infected row with data=NULL
- `go/cmd/them,worker,middleware-worker/main.go` — build storage.Client from S3 config; pass to NewFileGate / dal.Complete
- `db/051_quarantine.sql` — `quarantine_artifacts` table; `run_artifacts.data` nullable + `storage_key` column; `middleware_jobs.quarantine_id`
- Tests: S1-84 (6), S1-88 (5 new job DAL), S1-89 (3 new storage)

### Migrations applied
```
db/050_middleware_pipeline.sql                    ✅ applied
db/051_quarantine.sql                             ✅ applied
db/052_middleware_jobs_nullable_artifact.sql      ✅ applied
```

### Containers to rebuild to pick up quarantine-first changes
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge
# Security profile (ClamAV + middleware worker + MinIO):
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile security up -d
```

**Phase 5 — UI + WS scan subscription (prior session)**
- `go/internal/dashboard/handler.go` — `scan:<artifact_id>` added to `IsValidChannel`; `sendScanSnapshot` delivers current scan status from `them:scan:state:{artifactID}` Redis key; `sendSnapshots` dispatches to scan channel
- `go/internal/dashboard/handler_test.go` — `TestDashboard_ScanSnapshot` + `TestIsValidChannel` updated (2 new tests; S1-52: 11→13)
- `frontend/src/lib/apiTypes.ts` — `SecurityConfig`, `AVScanConfig`, `ArtifactScanEvent` types added
- `frontend/src/lib/api.ts` — `getSecurityConfig(appId)`, `putSecurityConfig(appId, cfg)` added to `themApi`
- `frontend/src/app/admin/applications/components/MonitorView.tsx` — `artifact_scan` event row with scan status badge (pending/scanning/clean/infected/error/disabled icons)
- `frontend/src/app/admin/applications/components/RuntimeView.tsx` — Security section: enable/disable file artifact scanning toggle + Save Security button

**Session B complete (2026-09-02, commit 3f74d70):**
- `internal/artifacts/handler.go`: three-path byte resolution: MinIO (storage_key set) → 410 Gone (infected, data=nil) → legacy Postgres BYTEA
- `internal/runrecorder/recorder.go`: `ArtifactMeta.StorageKey` + `COALESCE(storage_key,'')` in `GetArtifact`
- `cmd/them/main.go`: `storageClient *storage.Client` (concrete type); `artifacts.NewWithFetcher` wired
- Tests: S1-30 13→16 (MinIO path, 410 infected, MinIO error 500)
- them-go-bridge rebuilt and restarted ✅

**Session C complete (2026-09-02, commit 3cf93b1):**
- `internal/orchestrator/orchestrator.go`: `ScanResult`/`ScanSubscriber` interfaces; `WithScanSubscriber`; `emitArtifactEvent` emits `file_scanning` when gated, then goroutine waits and emits `file` (clean/error/timeout) or `file_blocked` (infected); `copyMap` helper
- `internal/orchestrator/scan_subscriber.go`: `RedisScanSubscriber` — subscribes to `them:run:<runID>` pub/sub, filters `artifact_scan_result` by artifactID, cancels on first match
- `cmd/worker/main.go`: `RedisScanSubscriber` wired into factory
- Tests: S1-90 (4 new orchestrator scan subscriber tests)
- them-go-worker rebuilt and restarted: polling ✅

**Session E complete (2026-09-02, commits 34d99bd–793e7f6):**
- `go/cmd/middleware-worker/main.go`: health heartbeat goroutine — writes `them:dash:services:health` (TTL 30s) every 10s; heartbeat does NOT publish to `services:stats` (scan job completion does)
- `go/internal/admin/services_stats.go`: reads health key, adds `worker_up bool` to response envelope
- `go/internal/admin/router.go`: passes `redis` to `NewServicesStatsHandler`
- `go/internal/dashboard/handler.go`: `sendServicesHealthSnapshot` — pushes `{type:services_health, worker_up}` on subscribe so badge is immediately correct on tab open
- `frontend/src/app/admin/services/page.tsx`: green/red "Scanner online/offline" badge; `load(showSpinner)` — WS-triggered refreshes are silent (no screen flash); `services_health` event updates badge only without re-fetch; state var `window` renamed to `timeWindow` (was shadowing `globalThis.window`, breaking WS connect); Recent jobs show `toLocaleString()` local time
- `go/internal/admin/dal/services_stats.go`: quarantine count filters `WHERE storage_key IS NOT NULL` — only counts files genuinely awaiting scan, not post-scan tombstones
- `frontend/src/lib/apiTypes.ts`: `worker_up: boolean` on `ServicesStats`
- Scan error decision: `error` outcome passes file through to user (fail-open) — intentional, can be changed to block

**Session D complete (2026-09-02, commit a174665):**
- `frontend/src/app/admin/playground/playgroundTypes.ts`: `FileMsg` gains `artifact_id`, `scanning`, `blocked`, `threat` fields
- `frontend/src/app/admin/playground/useChatConnection.ts`: `file_scanning` handler (spinner bubble); `file_blocked` handler (finds and replaces scanning bubble, or adds new blocked bubble); `file` handler (replaces scanning bubble in-place for clean result)
- `frontend/src/app/admin/playground/ChatColumn.tsx`: renders three states — scanning (spinner + "Scanning…" label), blocked (red border/icon + threat text), clean (download button + previews)
- TypeScript `tsc --noEmit` passes with zero errors ✅

### What's NOT done yet
- Reaper job for stuck quarantine objects (rows with `storage_key IS NOT NULL AND expires_at < now()`)
- Additional processors: `pii_redact`, `prompt_inject`, `schema_validate`, `audit_capture`

### Key design decisions
- **Quarantine-first**: file bytes go to MinIO quarantine bucket BEFORE any Postgres row; infected bytes never touch `run_artifacts.data`
- `run_artifacts.data` is now nullable — infected rows have `data=NULL, storage_key=NULL`
- `run_artifacts.storage_key` holds MinIO artifacts key for clean files (replaces BYTEA streaming)
- Two MinIO buckets: `them-quarantine` (bytes pre-scan, TTL 1hr) and `them-artifacts` (confirmed clean)
- Gateway fail-open: MinIO write error → disabled path, original URL used
- ClamAV via TCP `them-clamd:3310` (Unix socket cross-namespace doesn't work in Docker)
- Security scanning per-application via `applications.security_config` JSONB
- Download gate: 451 for infected, 202 for pending/scanning
- Redis cache invalidation: `them:security_config:invalidated:{app_id}` pub/sub on PUT

---

## A2A EP SDK Migration — COMPLETE (commit 45a0e23, 2026-08-31)

### What was done
- `go/internal/a2a/server.go`: rewritten to use `a2asrv.NewJSONRPCHandler` — 100% A2A v1.0 wire format
- `go/internal/a2a/executor.go` (new): `orchExecutorFunc` bridges `Lifecycle.Start` + run-stream bus to `iter.Seq2[a2a.Event, error]`; maps `token/file/done/error` bus events to SDK types
- `go/internal/a2a/card.go` (new): `buildSDKAgentCard` from DB row or fallback; served via `StaticAgentCardHandler`
- `go/cmd/them/main.go`: wires `RedisA2ATaskStore` via `WithTaskStore` + `WithSessionPublisher` retained
- `go/internal/a2a/server_test.go`: all fixtures updated for `SendMessage`/`SendStreamingMessage` and `TASK_STATE_COMPLETED`; 3 new compliance tests A2A-WF01/WF02/WF03
- `go/TEST_INDEX.md`: count 27→30, 3 new compliance rows

### Breaking change for external A2A clients
External clients calling `/a2a/{app_slug}/{ep_slug}` MUST update:
- Method name: `message/send` → `SendMessage`; `message/stream` → `SendStreamingMessage`
- TaskState: `"completed"` → `"TASK_STATE_COMPLETED"`, `"failed"` → `"TASK_STATE_FAILED"`
- Internal callers (agentregistry) already use `SendMessage`/`SendStreamingMessage` — unaffected.

---

## Frontend file-split refactor — COMPLETE (commit a54a796, 2026-08-31)

Split 4 oversized admin pages into focused sub-components. No logic changes. TypeScript passes with zero errors.

| Page | Before | page.tsx after | New files |
|---|---|---|---|
| `admin/agents/page.tsx` | 2,529 lines | 660 lines | `AgentCard.tsx`, `FolderHeader.tsx`, `AgentModals.tsx`, `agentTypes.ts`, `agentUtils.ts` |
| `admin/orchestrators/page.tsx` | 863 lines | 212 lines | `OrchestratorCard.tsx`, `OrchestratorForm.tsx`, `orchestratorConstants.ts` |
| `admin/mcp-servers/page.tsx` | 1,022 lines | 157 lines | `MCPBadges.tsx`, `MCPServerCard.tsx`, `MCPToolRow.tsx`, `MCPPropertiesPanel.tsx`, `MCPCreateModal.tsx`, `mcpConstants.ts` |
| `admin/settings/page.tsx` | 905 lines | 189 lines | `RoleCard.tsx`, `MonitoringPanel.tsx`, `settingsConstants.ts` |

## Frontend file-split — waves 1–5 (complete, 2026-08-31)

All splits: no logic changes. TypeScript passes with zero errors throughout.

| File | Before | After | New files |
|---|---|---|---|
| `lib/api.ts` | 1,152 lines | 476 lines | `lib/apiTypes.ts` (728 lines), `lib/apiClient.ts` (71 lines) |
| `applications/components/PropertiesPanel.tsx` | 936 lines | 125 lines | `panel/AppPanel.tsx`, `panel/EntryPointPanel.tsx`, `panel/OrchestratorPanel.tsx` (484), `panel/AgentPanel.tsx`, `panel/MiddlewarePanel.tsx`, `panel/panelStyles.ts` |
| `applications/components/CanvasBuilderView.tsx` | 1,152 lines | 625 lines | `cbv/CanvasNodePropertiesPanel.tsx` (573 lines) |
| `admin/playground/page.tsx` | 2,309 lines | 276 lines | `playgroundTypes.ts` (169), `MarkdownRenderer.tsx` (240), `DebugPanel.tsx` (588), `ChatColumn.tsx` (957) |
| `admin/playground/ChatColumn.tsx` | 957 lines | 495 lines | `ChatBubbles.tsx` (95), `useChatConnection.ts` (449) |

All oversized frontend files have been split. No files remain above 600 lines in the pages that were targeted.

---

## Go file-split refactor — in progress

### Completed this session
| File | Before | After | New files | Commit |
|---|---|---|---|---|
| `go/cmd/agent-runtime/main.go` | 1123 lines | 115 lines | `runtime.go` (376), `hitl.go` (205), `spec.go` (333), `llm.go` (150) | `ca51f2a` |

### Remaining candidates (next session picks one)

| File | Lines | Suggested split |
|---|---|---|
| `go/internal/agentgen/compiler.go` | 1056 | `compiler.go` (entry points) + `validate.go` (all validate* funcs) + `topo.go` (topoSort/resolveBindings/deriveStepVars/collect*) |
| `go/internal/agentgen/nodes.go` | 1040 | `nodes.go` (registry + spec types) + `nodes_exec.go` (all exec* funcs) |
| `go/internal/admin/applications.go` | 972 | `applications.go` (CRUD handlers) + `applications_llm.go` (TestLLM/Patch*/probe* funcs) |
| `go/internal/temporal/canvas_workflow.go` | 939 | Harder — single workflow; defer unless it grows |
| `go/internal/orchestrator/orchestrator.go` | 925 | Risky without full E2E; defer |
| `go/internal/agentregistry/registry.go` | 834 | `registry.go` (cache + lookup) + `registry_invoke.go` (A2A invocation logic) |
| `go/internal/admin/service/applications.go` | 756 | Mirrors handler split — defer until handler split is done |

**Start with `compiler.go` — clearest responsibility boundaries, pure functions, no live state.**

**Full step-by-step instructions (exact file map, imports, verification, commit message) are in:**
`docs/SPLIT_COMPILER_INSTRUCTIONS.md`

### First prompt for next session
> Read docs/SPLIT_COMPILER_INSTRUCTIONS.md in full before touching any code. Follow the procedure exactly: pre-flight → create compiler_validate.go → create compiler_topo.go → trim compiler.go → run all 4 verification steps → commit and push.

---

## Next recommended task (features)

### Phase 5-A: StepLoop — FULLY COMPLETE (commit 81c3a31)

Done (initial, commit a69f01a):
- `LoopConfig`: `ItemsVar`, `ItemVar`, `AccumVar`, `Condition`, `MaxIterations`, `BodySteps`
- `PlanNode.SubPlan *ExecutionPlan` — loop body compiled by `compileLoopBodyPlan`
- `plan_compiler.go`: `compileLoopBodyPlan`, `resolveLoopOuterNext`
- `nodes.go`: `execLoop` — LocalExecutor path only
- Frontend: Loop config panel in `StepConfigSection.tsx`
- Tests: EP-LOOP-1..5, CT-LOOP-1..3

Done (durable loop, commit 05351bd) — addresses all 4 Phase 5-A audit findings:
- `local_executor.go`: `ExecNodeWithPolicy` exported; `execNode` is now a thin wrapper
- `nodes.go`: `execLoop` uses `ExecNodeWithPolicy` per body step (retry/timeout per body node);
  accum_var snapshots only declared body `Outputs` keys; `Validate` errors on empty `BodySteps`
- `plan_compiler.go`: `ValidateLoopBodies` — unknown body step IDs + `MaxLoopHistoryBudget` (5000) check; wired into `agent-runtime` invocation path
- `canvas_workflow.go`: `runBranch` intercepts `StepLoop` before `ExecuteActivity`; `runLoopNode` iterates items sequentially, schedules each body step as its own `ExecuteStepActivity` with its own policy/retry/timeout/history entry. Branch inside body works.
- Tests: CT-LOOP-DURABLE-1..5, PC-LOOP-1..3; all 42 packages pass

Done (canvas ports, commit 81c3a31):
- `nodes.go`: `ControlOutputPorts: [{loop-body}, {loop-done}]`; `EdgeRules.MaxOut: 2`
- `nodeRegistry.ts`: loop added to `SUMMARY_FNS` (shows `items_var`)
- `useDefinitionLifecycle.ts`: both serialization paths (validation useEffect + `buildDefinitionDoc`) derive `body_steps` via BFS from `ctrl-out-loop-body` edge chain; set `next` to only `ctrl-out-loop-done` target; load path reconstructs both edges from `step.next[0]` (done) and `config.body_steps[0]` (body entry)

### Phase 5-B: HumanWait async — COMPLETE (two commits: `3b1052f` + pending hardening)

**Phase 1** (commit `3b1052f`) built the initial async path.

**Phase 2 hardening** (pending commit) adds:
- `HITLHandle` 6-field schema: `{workflow_id, run_id, tenant_id, step_id, wait_token, state}` — state machine "submitted"→"waiting"→"signalled"→deleted
- `UpdateWaitToken()`, `TrySignal()` (atomic CAS), `MarkDone()` on HITLStore
- Deterministic `wait_token` via `sha256(runID+":"+stepID+":"+counter)[:16]` — never `uuid.New()` in workflow code
- `hitl_status` workflow query handler registered at workflow start — polled by agent-runtime
- Per-step HITL timeout via `workflow.Select` + timer (configurable via `HumanWaitConfig.TimeoutSeconds`)
- `runBodyBranch` WaitingForHuman block — loop body `human_wait` support
- `HITLRequestHandler` — intercepts GetTask/SubscribeToTask/CancelTask for HITL A2A tasks; polls `QueryHITLStatus` to sync state; no permanent background goroutines
- `RedisA2ATaskStore` — proper `taskstore.Store` implementation connected to SDK via `WithTaskStore`
- `CanvasAwaiter`, `CanvasCanceler`, `CanvasHITLQuerier` interfaces on `TemporalExecutor`
- Signal endpoint moved from unauthenticated port 9300 to JWT-authenticated admin router: `POST /admin/canvas-tasks/{task_id}/signal` behind `RequireSuperAdmin + AdminTenantMiddleware`
- `CanvasTasksHandler` in `internal/admin/canvas_tasks.go` — tenant ownership check + TrySignal CAS
- 20 total tests: HS-1..11, RT-HITL-1..5, CSIG-1..4; `go test ./...` → 0 failures

### Phase 5-C: A2A call node — COMPLETE (commits 89c7e67 + pending gap fix)

**What was built (Phase 5-C initial, commit 89c7e67):**
- `go/internal/agentgen/a2a_caller.go`: `A2ACaller` interface + `HTTPA2ACaller` + `AgentEndpointResolver` + `DBAgentEndpointResolver`
- `go/internal/agentgen/interpreter.go`: `a2aCaller` field + `WithA2ACaller()` + `execA2ACall`
- `go/internal/agentgen/context.go`: `A2ACallDepth int` on `InvocationContext`
- `go/internal/agentgen/nodes.go`: `StepA2ACall` — Execute set; Validate checks required fields
- `go/internal/agentgen/node_executor.go`: `A2ACallDepth` on `ActivityIC`
- `go/internal/agentgen/compiler.go`: `validateHumanWaitBackend`
- `go/internal/agentgen/spec.go`: `A2ACallStepConfig` with AgentSlug/InputVar/OutputVar/TimeoutSeconds
- `go/cmd/agent-runtime/main.go` + `go/cmd/dag-worker/main.go`: wired HTTPA2ACaller

**What was fixed (Phase 5-C gap fixes, pending commit):**
- `A2ACallParams` struct — replaces positional `Call()` args; adds `InvocationID` + `StepID`
- `ResolvedEndpoint` struct — resolver now returns `AgentID` + `BindingID`
- `AgentEndpointQueryer.QueryAgentEndpoint` — takes `applicationID`; JOINs `app_agent_bindings + applications`; returns 4 columns
- `DBAgentEndpointResolver.ResolveEndpoint` — fail-closed when binding or endpoint missing
- `HTTPA2ACaller.Call` — sends `X-Them-Agent-Id` + `X-Them-Binding-Id` headers
- `stableCallUUID` — UUID v5 from `invocationID:stepID:agentSlug:role` so retries re-use same IDs
- `sanitizeRemoteError` — strips URLs → `[url-redacted]`, truncates at 300 chars
- 5 new tests: A2A-9b, A2A-14..18 (fail-closed, stable UUIDs, sanitized errors, E2E Local + Temporal)
- Total: 18 tests (A2A-1..18), suite total 898 → 916

**Security constraints enforced:**
- Binding required — no call without verified `app_agent_bindings` row (fail closed)
- `X-Them-Agent-Id` + `X-Them-Binding-Id` sent so callee can verify tenant ownership
- Endpoint + auth token from DB only — never user-supplied
- `X-Them-A2A-Depth` propagated; `MaxA2ACallDepth = 3` hard cap
- Self-call rejection before resolver is invoked
- Remote error messages sanitized (no internal URLs in logs/responses)
- No secrets in Temporal history

### Phase 5-D: StreamOut node — COMPLETE

**What was built (Phase 5-D):**
- `go/internal/agentgen/spec.go`: `StreamOutStepConfig{FromVar, MediaType}`
- `go/internal/agentgen/interpreter.go`: `execStreamOut` — reads `from_var`, defaults to `"output"`, sets `result.Text` + `result.MediaType` (same semantics as `execResponse`; incremental streaming is a transport-layer concern handled by agent-runtime's A2A artifact events)
- `go/internal/agentgen/nodes.go`: `StepStreamOut` — `Execute` wired to `execStreamOut`; `Validate` checks `from_var` required (`STREAM_OUT_MISSING_FROM_VAR`); `DeriveInputs` declares `from_var` as required; description updated (no longer stub)
- `go/internal/agentgen/noderegistry_test.go`: `StepStreamOut` moved from stubs list → implemented list; `TestNodeRegistry_StreamOutIsSink` asserts `Execute≠nil`; `TestNodeRegistry_StubTypesHaveNilExecute` updated (only `human_wait` remains)
- `go/internal/agentgen/compiler_test.go`: `stubGraph` updated from `stream_out` to `human_wait` (stream_out is no longer a stub)
- `go/internal/agentgen/stream_out_test.go`: 10 new tests (SO-1..10)
- `go/TEST_INDEX.md`: S1-83 added, totals 916→926

**Design note:** At the interpreter level, StreamOut and Response are functionally identical — both read a variable and set `result.Text`. The transport differentiation (incremental artifact events vs. single artifact) happens in `agent-runtime/main.go`'s `executeSkill`, which already emits `ArtifactEvent` at the end of every execution. A true token-by-token streaming path would require a callback/writer interface injected into the interpreter — that's a future transport-layer enhancement, not a canvas-node concern.

**Next recommended task:**
- UI: StreamOut properties panel in canvas `RightPanel.tsx` (from_var + media_type fields) — mirrors Response panel
- UI: A2A Call node properties panel in canvas RightPanel (slug + var config)
- Smoke test the two-segment URL paths end-to-end (WS connect, SSE connect, A2A card fetch) via the playground
- Frontend: expose app slug as an editable field in the application properties panel so users can rename/customize it

### Phase 4-C Advisory items (deferred)
- Advisory A: DB round-trips per Temporal activity (4 queries/node) — cache spec in `ActivityIC`
- Advisory B: `PipelineVars` payload growth — prune vars before each `StepActivityInput`
- Advisory C: DB pool (20) vs DAGWorkerMaxConcurrentActivities (50) mismatch — raise pool
- Advisory D: dag-worker health/readiness HTTP endpoint (currently no /healthz)
- Advisory E: HumanWait — RESOLVED by Phase 5-B. `input-required` path fully async. Reconnect via SDK `SubscribeToTask` (no code needed — SDK handles it).

### Other tasks (lower priority)
- DAG live canvas validation — smoke test a Branch/Parallel canvas agent live with `--profile temporal`
- Docker E2E test (`THEM_TEMPORAL_E2E=true`) against live stack to validate all 7 blockers end-to-end
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
- **`them-bridge` (Python FastAPI) has been deleted.** `app/`, `Dockerfile`, `Dockerfile.worker` removed from filesystem. Not in `docker-compose.yml`.
- **`them-worker` (Python Temporal) has been deleted.** Not in `docker-compose.yml`. All WS/SSE sessions submit to `them-orchestration-go`.
- **No global LLM key fallback.** Apps with no key get an explicit error.
- **No secrets in Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history.**
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** Global key must not be written or read.
- **EP cache key is `"{tenantID}:{appSlug}:{epSlug}"`.** Invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{appSlug}:{epSlug}"`. DB resolves by `(tenant_id, app_slug, ep_slug)`.
- **`entry_points.tenant_id` is NOT NULL.** `UNIQUE(application_id, slug)` enforced at DB level (relaxed from tenant-scoped to application-scoped in migration 048).
- **`applications.slug` is NOT NULL** after migration 048. `UNIQUE(tenant_id, slug)` enforced at DB level. URL shape is `/apps/{app_slug}/{ep_slug}/...`.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** — never globally by name.
- **Project name: `them_gateway`** — required for all compose commands.

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Update this file at session end — do NOT create new NEXT_SESSION_*.md files.
3. ADRs are permanent — never archive them.
4. Trust code over docs; update docs when they diverge.
5. Documentation changes ship in same commit as the code they describe.
