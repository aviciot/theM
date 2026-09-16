# Handover — Application Canvas Upgrade
# Created: 2026-09-16
# Last updated: 2026-09-16 (Phase 3 — COMPLETE: HIL API + agent invocation + E2E)
# Use this doc when starting a fresh Claude session to continue this work.

---

## First prompt for new session

```
Read docs/HANDOVER_APP_CANVAS.md and docs/APP_CANVAS_UPGRADE_PLAN.md, then continue the App Canvas work. Phase 3 is complete. Next: full canvas E2E test — create an app with Router→HIL→Agent flow, publish, start a session, verify HIL pause, approve, verify agent invoked, run completes.
```

---

## Strategic framing (read this first)

The-M is an AI governance and control platform for organizations. It manages agents and exposes them with governance, security, RBAC, and observability.

Two distinct canvases:
- **Agent builder** — defines *what* an agent does (internal DAG of steps)
- **Application canvas** — defines *how* agents are governed and exposed (entry points, middleware, routing, runtime config)

These stay separate. The application canvas is the governance shell — not a replacement for the agent builder.

---

## Current HEAD and state

Branch: `main`
HEAD: `45206503  fix(hil): grant DB permissions, make temporal signal non-fatal, add E2E test`

Recent work completed (this feature):
- **Phase 3 HIL E2E fix** (`45206503`) — GRANT SELECT/INSERT/UPDATE on hil_approvals to them_app/them_admin; temporal signal non-fatal; test_39 passes (4/4)
- **Phase 3 agent invocation** (`4dc372d0`) — `InvokeAgentActivity` + `pgxAgentA2ACaller` (A2A JSON-RPC call by agent UUID); 6 unit tests
- **Phase 3 HIL API** (`b3fe3841`) — `POST /runs/{id}/hil/{node}/approve|reject`; RBAC-gated; `SignalNamedWorkflow`; 5 unit tests
- **Phase 3 second review fixes** (`b7f4c83f`) — finalization on all exit paths (defer+disconnected ctx), XAdd error returned, _resolved_agent_ids stamped at publish, orchestrator LLM config from EPConfig
- **Phase 3 dispatch switch** (commit `4ceef04b`) — WS/SSE branch on execution_backend=temporal
- **Phase 3 core** (commit `7f898f40`) — AppFlow compiler + Temporal workflow + Router/HIL activities + frontend panels
- **Phase 2 complete** (commit `01ddb610`) — Router + HIL flow control nodes, topology only, frontend-only
- **Phase 1 complete** (commit `61a3d915`) — middleware node registry: emoji/color/bg_color from DB

Active stack: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`

---

## The 4-phase plan

Full details in `docs/APP_CANVAS_UPGRADE_PLAN.md`.

| Phase | What | Effort | Status |
|---|---|---|---|
| 1 | Middleware node registry — emoji/color/bg from DB, not hardcoded | S | ✅ **DONE** (commit `61a3d915`) |
| 2 | Router + HIL nodes in app canvas (topology only) | M | ✅ **DONE** (commit `01ddb610`) |
| **3** | App canvas DAG execution — local loop + Temporal | L | ✅ **DONE** (commit `7f898f40`) |
| 4 | Full node unification (optional/future) | L | Future |

---

## Phase 3 — COMPLETE

### Phase 3a — core compiler + workflow (commit `7f898f40`, fixes `03192156`..`3e6c1bbb`)

**Go — new package `go/internal/appflow/`:**
- `compiler.go`: `Compile(raw json.RawMessage, agentByInstanceID map[string]string) (*AppFlowSpec, error)` — parses `AppDefinitionDoc` (schema_version 2), BFS walk from EP + ep.Root, classifies components as `agent | middleware | router | hil | orchestrator`, builds `EPFlow{Slug, Protocol, Nodes, Edges, StartID}`. `Validate(*AppFlowSpec)` checks router_no_edges and unresolved_agent.
- `workflow.go`: `AppFlowWorkflow` — Temporal workflow on `appflow-dag` task queue. Walks `EPFlow` from `StartID`; router nodes run `ExecuteRouterActivity` (LLM intent → label → outgoing edge); HIL nodes run `ExecuteHILActivity` (persist, then wait for `hil_approval:<nodeID>` signal with configurable timeout + fallback); agent/middleware nodes pass through. `AppFlowActivities{LLMCaller, ExecuteRouterActivity, ExecuteHILActivity}`. Types: `RouterConfig`, `HILConfig`, `RouterActivityInput/Output`, `HILActivityInput/Output`, `HILApprovalPayload`.

**Go — `go/cmd/dag-worker/main.go`:**
- Imports `internal/appflow`.
- Second `temporalworker` registered on `appflow-dag` task queue with `AppFlowWorkflow` + `ExecuteRouterActivity` + `ExecuteHILActivity`. `AppFlowActivities.LLMCaller` wired with `dbRouterLLMCaller` (3-tier key resolution). `AppFlowActivities.DB` wired for HIL persistence.
- Both workers stopped on SIGTERM.

**Frontend:**
- `apiTypes.ts`: `AppDefinitionDoc.execution_backend?: 'local' | 'temporal'`, `ConnectionDef.label?: string`.
- `CanvasHelpers.ts`: `canvasToDoc` preserves full flowControl config + edge labels. `docToCanvas` restores edge labels.
- `CanvasBuilderView.tsx`: `executionBackend` state initialized from `def.definition.execution_backend` in `loadDef`; dropdown toggle in top bar.
- `CanvasNodePropertiesPanel.tsx`: Router panel (output_labels[], classifier_prompt textarea); HIL panel (approver_role select, prompt textarea, timeout_seconds input, fallback_action select).
- `constants.ts`: `NODE_PORTS['flowControl']` entry added so flow_control connections are accepted.

**DB:** `db/098_hil_approvals.sql` — `hil_approvals` table. Applied.

**Tests:** S1-112 (10 tests, AF-01..10). 0 failures.

### Phase 3b — dispatch switch (commit `4ceef04b`)

**Go — dispatch switch:**
- `go/internal/epconfig/pgx.go`: LEFT JOIN `application_definitions` to fetch active definition JSON in the same query. Zero extra round-trips on admission.
- `go/internal/epconfig/epconfig.go`: `EPConfig.ExecutionBackend` + `EPConfig.ActiveDefinitionJSON` — only populated when `execution_backend="temporal"` (safe to carry in 30s cache, `local` never stored).
- `go/internal/appflow/workflow.go`: `WorkflowIDForRun(tenantID, runID)` → `"appflow:{tenantID}:{runID}"` — collision-free Temporal ID per run.
- `go/internal/execution/lifecycle.go`: `StartAppFlow(ctx, handle, AppFlowWorkflowInput)` — dispatches on `appflow-dag` task queue; overwrites identity fields from handle (same security model as `Start`).
- `go/internal/ws/handler.go` + `go/internal/sse/handler.go`: branch on `EPConfig.ExecutionBackend == "temporal"`:
  - Parses definition JSON for `agentByInstanceID` (instance_id → definition_id = agents.id)
  - Calls `appflow.Compile()` + `appflow.Validate()` — rejects invalid specs at admission
  - Calls `lc.StartAppFlow()` with compiled spec
  - Else: existing `lc.Start()` path (OrchestrationWorkflow) — SEC-04 preserved

**Tests:** 3 EC-EB epconfig tests + 1 WS dispatch switch test (asserts `appflow-dag` task queue). 1178 tests total, 0 failures.

**Stack:** `them-go-bridge` + `them-dag-worker` rebuilt and running. Both workers polling: `canvas-dag-nodes` + `appflow-dag`.

### Phase 3 first review findings — fixed (`62431c3e`, `ca6c5fe7`)

1. **Finding 1 — Agent invocation (FIXED):** Agent/orchestrator nodes return `NonRetryableApplicationError("AgentInvocationNotImplemented")` — explicit failure, not silent completion.
2. **Finding 2 — Run lifecycle (FIXED):** `FinalizeRunActivity` wired at terminal exits; updates DB + publishes Redis Stream event.
3. **Finding 3 — agentByInstanceID resolution (FIXED):** Extracted to `appflow.ResolveAgentByInstanceID` shared by WS + SSE.
4. **Finding 4 — LLM provider/model (FIXED - partial):** `ParseLLMConfig` reads from definition JSON; defaults to "anthropic".
5. **Finding 5 — User context (FIXED):** `UserID`/`ExternalUserID` wired in `AppFlowWorkflowInput`.
6. **Finding 6 — Agent→flowControl connectivity (FIXED):** `NODE_PORTS['flowControl'].accepts` includes `'result'`.

### Phase 3 second review findings — fixed (`b7f4c83f`)

1. **Finding 1 — Centralized finalization (FIXED):** `AppFlowWorkflow` uses named returns `(out AppFlowWorkflowOutput, retErr error)` + `defer` + `workflow.NewDisconnectedContext`. `FinalizeRunActivity` now runs on **every exit path** including workflow cancellation, `AgentInvocationNotImplemented`, router errors, HIL persist errors, unknown node kind.
2. **Finding 2 — XAdd error returned (FIXED):** `FinalizeRunActivity` returns XAdd error so Temporal retries on Redis stream failure. `pgxRunStatusUpdater.UpdateRunStatus` adds idempotency guard `AND status NOT IN ('completed','failed','canceled')` — repeated finalize is safe.
3. **Finding 3 — Server-stamped agent UUIDs (FIXED):** `PublishDefinition` (dal + service) stamps `_resolved_agent_ids: {instance_id→agents.id}` into definition JSON via jsonb merge. `ResolveAgentByInstanceID` reads `_resolved_agent_ids` only — client `definition_id` is ignored. Old definitions without the stamp return empty map → Validate catches `unresolved_agent` → re-publish required.
4. **Finding 4 — Orchestrator LLM config from EPConfig (FIXED):** `epConfigQuery` fetches `ao.llm_provider`/`ao.llm_model`. `EPConfig` carries `OrchestratorLLMProvider`/`OrchestratorLLMModel`. `ParseLLMConfig` now takes `LLMOrchConfig` struct (not raw JSON). WS + SSE handlers pass `EPConfig.OrchestratorLLMProvider`/`OrchestratorLLMModel`. Silent "anthropic" substitution eliminated for OpenAI-configured apps.

### Phase 3c — HIL API + agent invocation + E2E (commits `b3fe3841`, `4dc372d0`, `45206503`)

**HIL approval API (`b3fe3841`):**
- `go/internal/admin/dal/hil_approvals.go`: `GetHILApproval` + `UpdateHILApprovalStatus`. 12-column scan. `IsNoRows` check.
- `go/internal/admin/hil_approvals.go`: `POST /runs/{run_id}/hil/{node_id}/approve|reject` — loads row, checks status='pending' (409), RBAC via `hasHILApproverRole` (admin/super_admin/configured role), updates DB first (durable), signals Temporal best-effort (non-fatal if workflow already complete).
- `go/internal/admin/router.go`: routes registered in `jwtGroup` runs group.
- `go/internal/admin/service/service.go` + `go/internal/temporal/signaler.go`: `SignalNamedWorkflow` added to `Temporal` interface and implemented.
- Tests AF-HIL-01..05 (5 unit tests).

**Real agent invocation (`4dc372d0`):**
- `go/internal/appflow/workflow.go`: `AgentInvoker` interface + `InvokeAgentActivity` method; agent case calls `AppFlowInvokeAgentActivityName`; orchestrator case passes through.
- `go/cmd/dag-worker/main.go`: `pgxAgentA2ACaller` — queries `them.agents` by UUID, decrypts auth_token_encrypted, POSTs A2A JSON-RPC `message/send`.
- Tests AF-WF-04..06 (3 unit tests).

**DB fix + E2E (`45206503`):**
- `db/098_hil_approvals.sql`: GRANT SELECT/INSERT/UPDATE to them_app + them_admin (was missing — caused all HIL API calls to fail with "db error").
- Temporal signal made non-fatal: DB update runs first; signal failure is logged and ignored.
- `scripts/tests/test_39_appflow_hil.py`: E2E test (4/4 pass).

**Next task:** Full canvas E2E — create an app with Router→HIL→Agent flow, publish, start WS session, verify HIL pause at gate, approve, verify agent invoked, run completes. Or: Phase 4 (canvas observability / run inspection).

---

## Phase 2 — COMPLETE

Everything shipped in commit `01ddb610`. Frontend-only. Summary of what was built:

- `frontend/src/lib/apiTypes.ts` — `ConnectionDef.type` extended with `'flow_control'`.
- `frontend/src/app/admin/applications/types.ts` — `FlowControlNodeData` interface added; `CanvasNodeData` union updated.
- `CanvasNodes.tsx` — `FC_META` lookup, `FlowControlNode` component (dashed border, emoji, cyan/purple colors), `NODE_TYPES['flowControl']` registered.
- `CanvasHelpers.ts` — `genInstanceId` handles `'flow_control'`; `canvasToDoc` serializes flowControl nodes + connections; `docToCanvas` restores them.
- `CanvasBuilderView.tsx` — `flow_control` drop handler; Flow Control palette section (Router + HIL draggable items).
- `CanvasInner.tsx` — minimap nodeColor for `flowControl` nodes (`#a855f7`).
- TypeScript: `npx tsc --noEmit` — clean.
- No Go/DB changes.

---

## Phase 1 — COMPLETE

Everything shipped in commit `61a3d915`. Summary of what was built:

- `db/097_middleware_defs_visual.sql` — `emoji`/`color`/`bg_color` columns added to `middleware_defs`; File Guard seeded. **Applied to DB.**
- `go/internal/admin/dal/middleware_wirings.go` — `MiddlewareDefSummary` extended; `ListMiddlewareDefs` SELECT updated.
- `go/internal/admin/middleware_wirings.go` — new `ListDefs` handler.
- `go/internal/admin/router.go` — `GET /admin/middleware-defs` registered.
- Frontend: `MiddlewareDef`, `MiddlewareData`, `MwNodeData` all have `emoji?`/`color?`/`bg_color?`.
- `CanvasBuilderView.tsx` — fetches `middlewareDefs`, builds `mwVisualById` map, applies on drop + load.
- `CanvasHelpers.ts` (`docToCanvas`) — new optional `mwVisualById` param.
- `CanvasNodes.tsx` (`MiddlewareNode`) — reads emoji/color/bg_color from node data; plain `<div>` render.
- `NodeLibrary.tsx` — `m.emoji ?? fallback`; passes visual fields on drag.
- Test S1-111 added (`TestMiddlewareWirings_ListDefs`). Full suite: 1164 tests, 0 failures.

---

## Phase 3 — what to do (concise)

**Goal:** When a user saves and publishes an application canvas that includes Router and/or HIL nodes, the Go backend executes the canvas as a DAG — dispatching to agents in the correct order, routing based on LLM intent classification (for Router), and pausing for human approval (for HIL). Temporal handles retries and long-running HIL waits.

This is the execution layer for the topology established in Phase 2.

### Key areas to touch

**Backend (Go):**
- `go/internal/admin/applications.go` — read flow_control nodes from the definition JSON on publish/compile
- `go/internal/agentgen/compiler.go` — extend `Compile` to handle `flow_control` node kinds (router, hil) in the DAG
- `go/internal/temporal/workflow.go` — add Router activity (LLM intent classification → pick outgoing edge) and HIL activity (pause → signal channel → resume)

**DB:**
- No new tables needed — Router intent config can live in `flow_control.config`; HIL approvals can use the existing `tasks` table (or a new `hil_approvals` table if tasks.type can't cover it)

**Frontend:**
- Router node properties panel: configure the classifier prompt and the list of outgoing labels (one label per outgoing edge)
- HIL node properties panel: configure the approver role, timeout, and fallback action

### Order of implementation
1. Extend `Compile` to produce a DAG with `router` and `hil` step types
2. Add `ExecuteRouter` Temporal activity (calls LLM, returns chosen label)
3. Add `ExecuteHIL` Temporal activity (persists approval request, waits on signal)
4. Wire the new step types into the Temporal workflow executor
5. Add Router and HIL properties panels in the frontend (node click → sidebar)
6. E2E test: canvas with Router → two agents; verify correct agent is called based on intent

### Constraints
- HIL approvals must be tenant-scoped and RBAC-gated
- Router LLM call must use the application's configured LLM provider (not hardcoded)
- If a Router has no matching outgoing edge, fail the run with a clear error (not a silent drop)
- All new Temporal activities must be registered at worker startup

---

## Key infrastructure to reuse

| What | Where | Notes |
|---|---|---|
| Phase 1 pattern | `CanvasNodes.tsx` `MiddlewareNode` | Same round-node structure — copy for `FlowControlNode` |
| `genInstanceId` | `CanvasHelpers.ts` line ~102 | Add `'flow_control'` case |
| `canvasToDoc` / `docToCanvas` | `CanvasHelpers.ts` | Both need a `flow_control` branch |
| `NODE_TYPES` map | `CanvasNodes.tsx` bottom | Add `flowControl: FlowControlNode` |
| Agent builder node types | `GET /admin/node-types` | Phase 2 does NOT need this — flow control nodes are static in the app canvas |
| ExecutionBackend concept | `go/internal/agentgen/compiler.go` | Phase 3 only |
| Temporal workflow | `go/internal/temporal/workflow.go` | Phase 3 only |

---

## Rules to follow

- Read `CLAUDE.md` and `go/CLAUDE.md` at session start
- Phase 2 is **frontend-only** — no Go changes needed, no DB migration
- If any Go file is touched, run `docker run --rm -v /opt/docker/them/go:/src -w /src golang:1.25-alpine go test ./...` before committing
- `TEST_INDEX.md` updated in same commit as any Go tests
- Never commit `.env` or `secrets.local`
- One phase at a time — do not start Phase 3 in the same session as Phase 2
- After Phase 2 is complete and tested, update `docs/CURRENT.md` and this doc
- TypeScript must compile clean (`npx tsc --noEmit`) before committing frontend changes
- When context tokens drop below ~2M, stop, commit current state, update `docs/CURRENT.md` and this doc, and hand over
