---
title: App Canvas / AppFlow (compile, publish, runtime, debug)
type: architecture
status: current
owns:
  - go/internal/appflow/
  - go/internal/nodedefs/
  - go/internal/debugcred/
  - go/internal/admin/service/publish.go
  - go/internal/admin/service/appflow_debug.go
  - go/internal/admin/appflow_debug.go
  - go/cmd/dag-worker/main.go
last_verified: 2026-10-24   # by reading code; nothing run live this session
---

# App Canvas / AppFlow

## Purpose
The App Canvas lets a user draw an application (entry points, agents, LLM/condition/router/HIL/fork/join/
cycle/http/transform/wait nodes, connections) and run it. This doc covers the life of that drawing:
the saved **definition JSON**, **validate + publish**, **compile to an `AppFlowSpec`**, the **Temporal
`AppFlowWorkflow`** that executes it, and **Debug Mode**. Admission and event delivery are shared with
the legacy path — see `run-lifecycle.md`. Canvas *agent* DAGs (`canvas-dag-nodes` queue) belong to
tracker item #9; export/import to #10.

## How it works

```
canvas UI ──save──► application_definitions (draft; JSON schema_version 2)
   │  validate:  DefinitionService.validateDoc  (publish.go)
   │  publish:   PublishDefinition → upsert app_orchestrators, entry_points, app_agent_bindings,
   │             deactivate stale rows, mark published, stamp `_resolved_agent_ids` into the JSON
   ▼
runtime request (WS/SSE) ── epconfig.Load ──► EPConfig{ExecutionBackend, ActiveDefinitionJSON}
   │   ExecutionBackend=="temporal" only if the active definition says so (else "" → legacy
   │   OrchestrationWorkflow, see run-lifecycle.md)
   ▼
ws/sse startAppFlow:  ResolveAgentByInstanceID → Compile → Validate → pick this EP's flow
   → Lifecycle.StartAppFlow  (queue appflow-dag, workflow ID appflow:{tenant}:{runID})
   ▼
dag-worker (queue appflow-dag): AppFlowWorkflow walks the graph; each node = activity or in-workflow logic
   │  events → XADD them:dash:run:{runID}:stream ; per-node trace → them.run_steps
   ▼
defer FinalizeRunActivity (disconnected ctx): UpdateRunStatus + terminal `done`/`error` event
```

### Definition JSON (what the compiler reads)
`appDoc`: `schema_version` (must be 2), `execution_backend`, `components[]` (`instance_id`,
`definition_ref{kind,namespace,name,version}`, `config`, `parent_instance_id` for cycle children),
`entry_points[]` (`instance_id`, `slug`, `protocol`, `root`, `config`), `connections[]` (`source`, `target`,
`type`, `label`). Server-stamped at publish: `_resolved_agent_ids{instance_id→agents.id}` (never taken
from client data; a definition published before the stamp needs a re-publish).

### Validate and publish (`admin/service/publish.go`)
- Structural checks, `incomplete_ref`/`missing_version`, duplicate instance IDs, registry resolution for non-builtin kinds (`flow_control` and `inline` are builtin), protocol ∈ `websocket|sse|voice|webrtc|a2a|gateway`, `dangling_root`, `dangling_connection`.
- If `execution_backend=="temporal"`: also `appflow.Compile` (with an empty agent map) + `appflow.Validate`, ignoring `unresolved_agent`. Local-backend apps skip graph rules on purpose.
- Publish projects the canvas into DB rows: orchestrator components → `app_orchestrators` (name = `instance_id`), each EP → `entry_points` (access mode, caps, queue, memory config, `allowed_principals`; dead `(access_mode, principals)` combos are rejected), agents → `app_agent_bindings` (agent must already exist in `agents`). Stale rows deactivated; definition marked published and set as `active_definition_id` atomically (`ErrConflict` if status changed concurrently).

### Compile (`appflow/compiler.go`) — pure, no DB
BFS from the EP (and `ep.Root`) over all connections, skipping cycle children; produces `EPFlow{Slug, Nodes, Edges, StartID}`. Node kinds: `agent`, `middleware`, `llm`, `condition`, `http`, `transform`, `router`, `hil`, `fork`, `join`, `cycle`, `wait_for_input`, `orchestrator`; unknown inline names stay `inline` so `Validate` reports `unknown_inline_node`. Edge labels are kept only for router/hil/condition sources. Cycle bodies are embedded into the cycle node's `CycleConfig`. `ApplyLLMOverrides` rewrites provider/model of `llm`/`router` nodes from stored Runtime-screen overrides, so a model change needs no re-publish. The spec is recompiled on every run, never stored.

### Validate rules (`appflow/validate.go`, pure)
`no_start_node`, `router_no_edges`, `fork_insufficient_branches` (<2 out), `join_insufficient_branches` (<2 in), `unresolved_agent`, `condition_no_expression|edge_count|missing_labels` (exactly two edges labelled true/false), `llm_no_prompt`, `llm_orphan`, `cycle_no_break_condition|invalid_max_iterations (1–100)|empty_body`, `wait_for_input_invalid_output_var|no_outgoing_edge`, `transform_no_functions|unknown_function`, `unknown_inline_node`.

### Execute (`appflow/workflow.go`, `nodes.go`, `graph.go`, `cycle.go`, `wait.go`)
Workflow state is `accumulated` (the running text) plus `vars` (flat `map[string]string`). Main loop follows `firstEdgeTarget` unless a node picks a label:

| Node | Behaviour |
|---|---|
| `agent` | Text Guard (input phase) → `InvokeAgentActivity` (A2A `SendMessage` to the agent's `endpoint_url`) → Text Guard (output) → File Guard (+ wait for async scan verdict) if the reply has a file part. Sets guard status vars `{node}_{guard}_status`. |
| `llm` | Text Guard in/out around `InlineLLMActivity` (streams `token` events); writes `vars[output_var or "output"]`. `PolicyOverrideLLM` (gateway) overrides provider/model/max tokens. |
| `router` | `RouterActivity` classifies → follows the edge with that label; if no match and exactly one edge, takes it; else fails `RouterNoMatch`. Sets `router_label`, `router_confidence` (+ per-node vars). |
| `condition` | Renders expression template over vars; truthy → `true` edge else `false`; missing edge → `ConditionNoMatch`. |
| `hil` | `ExecuteHILActivity` persists `hil_approvals`, then waits on signal `hil_approval:{nodeID}` (optional timer → `fallback_action` approve/reject). Follows `approved`/`rejected` edges; rejected with no edge ends the run `rejected`. |
| `fork`/`join` | Branches run as `workflow.Go` goroutines via `walkBranch` until the join found by `findJoinNode`; results merged by newline. |
| `cycle` | Repeats the body up to `max_iterations`, exits when `break_when_var` op/value matches. |
| `wait_for_input` | Writes `them:wait:{runID}` (4h), blocks on signal `appflow_user_input:{nodeID}`; WS handler / debug `send` deliver it. |
| `http` | `HTTPActivity`; credentials resolved from `app_flow_http_params` at run time (never in workflow history). GET retryable, others not. |
| `transform` | Pure function steps over vars; `vars["output"]` is promoted to `accumulated`. |
| `orchestrator`, `middleware` | Pass-through. Unknown kind → `UnknownNodeKind`. |

Defaults: activity timeout 10m, 2 attempts (override via app `TemporalCfg`), workflow timeout from `TemporalCfg.WorkflowTimeoutS`. Workflow code must stay deterministic; all I/O lives in `activities.go`. **No secrets in activity inputs** (Temporal history); activities resolve keys from the DB (`llmresolve`) at run time.

### Events and trace
`emitTrace` (from activities) and `traceNode` (from workflow code, for condition/fork/join) publish `node_start|node_done|node_error|node_paused` to the run stream and upsert `them.run_steps` keyed `(run_id, node_id)`. `LogVerbosity` (`off|status|full`, per app, resolved at start) controls only DB persistence: `off` = no row, `status` = row without output/error. The live Redis publish is unconditional.

### Debug Mode (`admin/appflow_debug.go`, `service/appflow_debug.go`, `debugcred/`)
`POST /admin/applications/{id}/debug/start` runs the **latest draft** (not the published one): resolves agents live from the registry, compiles and validates, requires an `llm_overrides` entry for every `llm`-kind node (validated before any run is admitted), then `AdmitDebug` (no gate/session; real `runs` row), writes per-node credential overrides to Redis `them:debug:{tenant}:{run}:{node}:llm_override` (TTL = `DebugRunMaxLifetime` 3h30m + 10m), and starts the workflow on `appflow-dag-debug` with `LogVerbosity=full` and a hard `WorkflowRunTimeout`. Companion endpoints: `/{run_id}/step` (signal `appflow_step`, one tick per click across all paused branches via a shared counter), `/{run_id}/send` (wait_for_input), `/{run_id}/result` (structured per-node verdict), plus presets. Overrides are deleted by `FinalizeRunActivity`; TTL is the fallback. Debug runs run on `them-dag-worker-debug` (same binary, `APPFLOW_TASK_QUEUE_OVERRIDE`).

### Worker (`cmd/dag-worker/main.go`)
One binary, two Temporal workers: `canvas-dag-nodes` (canvas agents, item #9) and the AppFlow queue (default `appflow-dag`) registering `AppFlowWorkflow` plus 12 activities (router, HIL, finalize, invoke agent, file gate + wait, text gate, inline LLM, trace, pending-wait, http, transform). Text Guards (PII, prompt-injection) run in-process here; File Guard scanning is async through `them-middleware-worker` and fails open if S3 is not configured.

## Key invariants
- Agent identity comes from `_resolved_agent_ids` (server-stamped), tenant-scoped; the agent lookup also filters `tenant_id` and `enabled`.
- `Validate` and `Compile` stay pure; workflow files stay deterministic (no I/O, wall clock, randomness, map iteration that affects control flow).
- No credential in any activity input; debug overrides are keyed by tenant+run+node and **not** consumed on read (retries re-read them).
- Anything configurable on the canvas must survive export/import (root `CLAUDE.md`): new node settings go inside the definition JSON, not a new table, unless registered in `app_deploy.go`.
- A new node kind needs: compile case, `Validate` rules (+ tests `AF-*`), workflow case (main loop; decide on `walkBranch`/`walkCycleBody` support), node registry meta (`noderegistry.go`), and a frontend palette entry.

## Failure modes
| What fails | Effect |
|---|---|
| Compile/validate at run start | WS/SSE: `failed to start appflow workflow`; run marked `failed` by `Release` |
| No `dag-worker` on `appflow-dag` | Run stays `running`; client sees `ready` then silence |
| Any node error | Workflow returns error → `FinalizeRunActivity` sets `failed` and emits `error` |
| Text Guard `block` / infected file | Non-retryable failure of the run |
| File Guard scan wait times out (60s) | Fails open (`timeout` status var), run continues |
| HIL rejected, no `rejected` edge | Run ends; DB status `failed`, event `error` ("rejected by HIL gate") |
| Redis publish for trace/token fails | Logged only; run unaffected |
| Debug run > 3h30m | Killed by `WorkflowRunTimeout` |

## Known gaps (found while writing, 2026-10-24; from reading code, not reproduced live)
1. **Fork branch failures are swallowed.** In the main loop a failed branch sets its result to `""` and the run continues; the comment says "detected via `out.Status`" but nothing sets it. A fork can complete `completed` with a branch having failed.
2. **`walkBranch` supports only `agent`, `llm`, `condition`, `orchestrator`, `middleware`, `join`.** `router`, `hil`, `http`, `transform`, `wait_for_input`, `cycle`, nested `fork` inside a branch return "unsupported node kind" — which gap 1 then swallows. `Validate` does not warn.
3. **Guards are bypassed inside fork branches.** `walkBranch` does not call the Text Guard or File Guard activities (main loop and `walkCycleBody` should be checked for the same), so PII/prompt-injection/file scanning only protects main-path agent/LLM nodes.
4. **Agent-only flows may deliver no answer text over WS/SSE.** `FinalizeRun` puts the answer in `done.final_text`, but both handlers forward only `run_id` from `done`; only LLM nodes emit `token` events. The text then reaches clients only inside `node_done.detail`. Related: `node_*` events (with output detail) are forwarded to production end-user connections regardless of debug — confirm this is intended.
5. **`Validate` is thin for some kinds:** no checks for `http` (empty URL/method), router `output_labels` vs edge labels, unknown `flow_control`/definition kinds (they fail at run time as `UnknownNodeKind`).
6. **Stale comments:** `workflow.go` header says agent nodes are "rejected … not yet implemented" (they are invoked); `compiler.go` says `local` is an "in-process goroutine loop" (local = legacy `OrchestrationWorkflow`); `publish.go` `isBuiltinKind` comment lists only router/hil/fork/join/llm/condition.
7. HIL rejection is stored as run status `failed`, not a distinct status (the rejection comment is dropped from the `error` message).
8. Reconciler cannot repair stuck AppFlow runs (workflow-ID mismatch) — see `run-lifecycle.md` gap 2.

## Where to change things
| To… | Edit |
|---|---|
| Add a node kind | `compiler.go` (`compileNode`), `validate.go`, `workflow.go` (+ `graph.go`/`cycle.go` if allowed there), `noderegistry.go`, activity in `activities.go` + registration in `cmd/dag-worker/main.go` |
| Change publish projections | `admin/service/publish.go` and `admin/dal/` publish queries |
| Change debug behaviour | `admin/service/appflow_debug.go`, `debugcred/`, `StartAppFlow` debug branch in `execution/lifecycle.go` |
| Change what clients see per event | `ws/handler.go` `writeEvent`, `sse/handler.go` `formatSSE` |
| Change timeouts/retries | `TemporalExecCfg` loader (`temporal_config_loader.go`) and constants in `workflow.go` |

After editing activities or `cmd/dag-worker/`: **rebuild and restart all dag-worker services, including `-debug`** (each has its own image tag; see `platform-overview.md`). For app-building/debug API sequence use the `/app-canvas` skill.
Tests: `go test ./internal/appflow/... ./internal/admin/... ./internal/debugcred/...`; index in `go/TEST_INDEX.md` (`AF-*`, `PUB-*`).

## Related
- `run-lifecycle.md` — admission, `Start`/`StartAppFlow`, run stream
- `platform-overview.md` — worker containers and queues
- `docs/plans/app-canvas-config-completeness-plan.md` — export/import completeness rule
- `docs/_archive/` plan docs referenced in code comments (`APP_CANVAS_DEBUG_PLAN`, `APPFLOW_TEXT_GUARDS_PLAN`, …) are history only
