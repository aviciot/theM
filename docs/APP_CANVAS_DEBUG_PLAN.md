# App Canvas — Debug Mode (real execution, not simulated)
# Status: PLANNED, phased. Phase 1 + 2 + 3 + 4 + 5 + 6 COMPLETE. Plan done.
# Date: 2026-09-22 (Phase 5 frontend + a real backend bug fix: 2026-09-23; Phase 6 Step controls: 2026-09-23)

---

## Progress

| Phase | What | Status |
|---|---|---|
| 1 — Debug worker pool + routing | New Temporal queue, worker container(s), per-run routing | ✅ COMPLETE (2026-09-22) |
| 2 — Per-node trace instrumentation | AppFlow activities emit node_start/node_done/node_error | ✅ COMPLETE (2026-09-22) |
| 3 — Durable trace storage | Extend `them.run_steps`; make existing Flow tree populate for Graph-mode runs | ✅ COMPLETE (2026-09-22) |
| 4 — Runtime log-verbosity setting | Per-app off/status/full config, gates persistence in Phase 3 | ✅ COMPLETE (2026-09-22) |
| 5 — Debug UI: setup + Run All | Dynamic param-spec scan (mirrors agent builder), Run All button, WS/SSE consumer | ✅ COMPLETE (2026-09-23) |
| 6 — Debug UI: Step controls | Step button, lockstep multi-branch pause/resume, canvas node highlighting | ✅ COMPLETE (2026-09-23) |

**One phase per session** (same discipline as `docs/NODE_REGISTRY_PLAN.md`). Update this table
at the end of every session. Read `docs/CURRENT.md` for exact HEAD/status before starting.

**Recommended order rationale:** 1→2→3 build the invisible backend foundation in dependency
order (nowhere to route without a queue; nothing to store without events; nothing to view without
storage). 4 is a small, independent slot-in once 3 exists. 5→6 are the user-visible payoff,
split because "watch it run" (5) is meaningfully simpler than "pause and inspect mid-flight" (6)
and each deserves its own focused session.

---

## Goal

Give the app canvas a debug/step-through experience for Graph-mode (Temporal) runs, driven by
**real execution on the real backend** — not a browser-side simulator like the agent builder's
existing "Debug" feature. A user builds a flow, clicks Debug, fills in only the runtime
parameters the canvas actually needs (LLM provider/model/key or mock, HTTP secrets, etc. —
mirroring the agent builder's existing dynamic param-spec pattern), then runs the whole flow or
steps through it one node at a time, watching each node light up on the canvas as it really
executes, with real inputs/outputs visible per node. Runs on an isolated worker pool so debug
traffic never competes with or risks production traffic.

---

## What already exists, and why it doesn't transfer directly

**Agent builder's "Debug" is a full client-side simulator** (`useDebugSession.ts`,
`DebugPanel.tsx`, `StepDebugSection.tsx`, `DebugEdge.tsx`, `StepNode.tsx`'s debug-state styling).
Confirmed by reading the code: every node's execution is faked in the browser with plain
TypeScript (`executeStep()` in `useDebugSession.ts`) — an LLM node's "execution" is a direct
`fetch()` from the browser to a thin proxy (`POST /api/debug/llm` or
`POST /api/v1/admin/debug-proxy`, `go/internal/admin/debug_proxy.go` — a stateless HTTP forwarder
with zero pipeline/node awareness). `go/internal/agentgen`'s real interpreter is never invoked.
This works because the agent builder's real execution is single-request/synchronous enough that
a browser can plausibly stand in for it.

**The app canvas cannot copy this**, because Graph-mode execution is not synchronous or
single-process — it's a multi-node Temporal workflow running on a separate worker container
(`them-dag-worker`), asynchronous by nature. A browser-side fake has nothing real to observe or
stand in for. Confirmed: **as of this session's "Simple"/"Graph" rename, BOTH app-canvas execution
modes run on Temporal** — there is no non-Temporal code path anywhere in this system (see
`docs/CURRENT.md`'s 2026-09-22 rename entry). So this plan must be Temporal-aware from day one;
there's no simpler "local" target to prototype against first.

**The deeper gap this plan closes:** today, **no** app-canvas run — debug or not — records a
per-node trace anywhere. Confirmed in code: `RecordAgentStep` (the only writer of
`them.run_steps`) is called exclusively from `internal/orchestrator/` — grep confirms zero
references from `internal/appflow/`, `internal/temporal/`, or `cmd/dag-worker/`. Both Temporal
workflows (`AppFlowWorkflow` for Graph mode, `CanvasAgentWorkflow` for the agent-builder's own
Temporal path) only ever write start/terminal run status and a flat `token`/`done`/`error` event
stream — nothing per-node, and nothing durable enough to inspect after the run ends. This is the
"AppFlow does not populate run_steps yet" note from `test_42`, confirmed to be accurate and to
extend to the agent-builder's Temporal path too, not just AppFlow.

**Practical consequence:** because the underlying gap is "no per-node trace exists at all," this
plan is strictly additive — there's no existing behavior to preserve or migrate away from. It is
also, by construction, **better than the agent builder's simulator** once built: it reflects what
actually happened on the real backend, and (per the "durable" design goal below) survives the
browser tab closing, unlike the agent builder's in-memory-only debug session.

---

## Decisions confirmed with the user this session (do not re-litigate)

1. **Debug is an opt-in toggle, not always-on.** A normal run does not pay any tracing/recording
   overhead unless debug mode was explicitly started for it.
2. **Turning on debug mode is what routes a run to the debug worker pool** — one switch does
   both (enables tracing AND changes which Temporal task queue the run dispatches to). There is
   no separate "test mode" flag on the app itself; debug is decided per debug-session, and it
   is available to any tenant/any user/any app — it is not an app-level setting.
3. **The debug worker pool must support multiple replicas**, shared across all tenants and users
   — same pattern already used for production (`them-go-worker`/`them-go-worker-2`,
   `them-dag-worker`/`them-dag-worker-2`, both added this session). Temporal's SDK already
   distributes work across however many workers poll a given task queue; adding N debug-pool
   replicas is the same mechanism, no new design needed there.
4. **Setup must be smart/dynamic**, mirroring the agent builder's `buildDebugParamSpecs()`
   pattern exactly: scan the actual nodes on the canvas, and only ask for what those specific
   node types need (LLM node → provider/model/key-or-mock; HTTP-flavored node → its declared
   app_param keys; a flow with no such nodes → nothing to fill in, go straight to Run/Step).
5. **Controls: Run All + Step**, same interaction model as the agent builder's debug bar — Step
   pauses after each node so its real input/output can be inspected before continuing.

---

## Architecture

### Phase 1 — COMPLETE (2026-09-22)

All checklist items done:

- [x] `go/internal/appflow/workflow.go`: `AppFlowDebugTaskQueue = "appflow-dag-debug"` constant
      added next to `AppFlowTaskQueue`. `AppFlowWorkflowInput.Debug bool` added. New pure helper
      `activityTaskQueueFor(debug bool) string` selects the queue; `AppFlowWorkflow` uses it for
      both `ActivityOptions` blocks it builds (`shortAO` for finalize/HIL, `ao` for the main node
      walk) — this was the part not called out explicitly in the original plan prose: the
      workflow itself dispatches activities via `ActivityOptions.TaskQueue`, independently of
      which queue the *workflow* was started on, so both needed the same debug/production switch.
- [x] `go/internal/execution/lifecycle.go`: `StartAppFlow` now takes `debug bool`, selects
      `AppFlowDebugTaskQueue`/`AppFlowTaskQueue` for `StartWorkflowOptions.TaskQueue`, and sets
      `input.Debug = debug` so the workflow's internal activity dispatch matches. Both existing
      callers (`internal/ws/handler.go`, `internal/sse/handler.go`) updated to pass `false` —
      no debug-start entry point exists yet; that's Phase 5/6.
- [x] `go/cmd/dag-worker/main.go` + `go/internal/config/config.go`: new env var
      `APPFLOW_TASK_QUEUE_OVERRIDE` — when set, the AppFlow worker registers on that queue instead
      of `appflow.AppFlowTaskQueue`. Same binary/image as production, no build-time distinction
      needed. The worker's `canvas-dag-nodes` registration (agent-builder Temporal path) is
      **not** affected — still always registers on the production `canvas-dag-nodes` queue on
      every dag-worker container, including the debug one (documented as intentional; that path
      has no debug queue yet, out of scope for this plan).
- [x] `docker-compose.dev.yml` + `docker-compose.hetzner.yml`: new `them-dag-worker-debug` service
      in both, same `Dockerfile.dag-worker` image, `APPFLOW_TASK_QUEUE_OVERRIDE=appflow-dag-debug`
      set. Both validated with `docker compose ... config --quiet` (0 errors) and
      `config --services` (service present). Hetzner block intentionally has no
      `THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN` — matches the pre-existing gap already flagged for
      `them-dag-worker`/`-2` in that file (see `docs/CURRENT.md` 2026-09-22 entry); not
      reintroducing anything new, just not fixing a pre-existing issue as a side effect here.
- [x] Proven end-to-end on the live local stack: built + started `them-dag-worker-debug`; logs
      confirmed `"appflow-worker polling" task_queue=appflow-dag-debug` (and, as expected,
      `canvas-dag-nodes` too). Started a workflow directly via `temporal workflow start
      --task-queue appflow-dag-debug --type AppFlowWorkflow ... --input '{...,"debug":true}'`
      using the `temporal` CLI already present in the `temporal-admin-tools` container. Confirmed
      via `docker logs`: the workflow's `AppFlowFinalizeRunActivity` executed **only** on
      `them-dag-worker-debug` (`TaskQueue":"appflow-dag-debug"` in its log lines); `them-dag-worker`
      and `them-dag-worker-2`'s logs show no activity for this workflow ID at all — their most
      recent entries predate this test. Deliberately fed an invalid empty UUID as `run_id` so the
      workflow would fail fast (no need for a real `them.runs` row) — the failure itself (in
      `pgxRunStatusUpdater`) is expected and irrelevant to what this test proves.
- [x] `go test ./...` — 0 failures (full suite, via Docker golang:1.25-alpine per the "no local Go
      toolchain" constraint). New tests: `TestActivityTaskQueueFor` (`internal/appflow`),
      `TestLifecycle_StartAppFlow_NotDebug_UsesProductionQueue` +
      `TestLifecycle_StartAppFlow_Debug_UsesDebugQueue` (`internal/execution`). `go build ./...`
      also clean. `go/TEST_INDEX.md` updated (S1-128, S1-129; S1 total 1322→1325).

**Phase 1 gate — MET:** a workflow started with `debug=true` is picked up by the debug worker,
not the production one, proven by log inspection. No UI, no trace events, no storage yet — that
is all later phases' job, unchanged from the original plan.

**Not done / deliberately deferred to a later phase (not a regression):** no WS/HTTP entry point
sets `debug=true` yet — both live callers pass `false` explicitly, so there is zero behavior
change for any existing app today. `them-dag-worker-debug` on this local dev box is running (not
just built); the Hetzner compose addition is config-only, not deployed to the actual Hetzner host
(consistent with how `them-dag-worker`/`-2` were handled on Hetzner in the prior session).

### Phase 2 — COMPLETE (2026-09-22)

**Scope deliberately kept minimal, per explicit user direction:** every node kind emits
`node_start`/`node_done`/`node_error`, with only small kind-specific detail (condition's chosen
branch, router's chosen label, fork's branch count, HIL's approval outcome) — no per-node debug
config, no `TraceMode`/redaction, no persistence. Goal: watch a real Graph-mode run's actual path,
node by node, live. Nothing more.

**Revised 2026-09-24 (Platform-as-Tenant Phase 6):** the "never a full prompt/response" framing
below (§ "What was built") turned out to be the wrong boundary once the Debug Mode inspector
(Phase 6, `AppFlowDebugInspector.tsx`) was actually built to *show* a node's captured output —
LLM/agent nodes reported `state: done` with no output at all, a real regression in usefulness, not
a safety feature. `InlineLLMActivity`/`InvokeAgentActivity` now pass their real
`responseText`/`text` into `node_done`'s `detail`, matching what router/condition/fork already did
from Phase 2 onward. The existing `verbosity` gate (`"off"`/`"status"`/`"full"`,
`docs/APP_CANVAS_DEBUG_PLAN.md` Phase 4) is unchanged and still the control point for how much
gets persisted — this fix only stops discarding data before that gate is even reached. See
`docs/LESSONS.md`'s entry for the full detail.

**Architecture investigated before implementing** (user asked for this explicitly — see the
"do not assume the current proposed solution is correct" research request this session): Router,
HIL, Agent, and Inline LLM already dispatch through real Temporal Activities (allowed to do I/O);
Condition, Fork, and Join execute entirely in workflow code (pure in-memory branching, no I/O,
subject to Temporal's determinism/replay rules). Evaluated and rejected as alternatives: Temporal
Queries (pull-only, can't push a live event), Updates (solve synchronous external mutation, not
this), workflow memo/search attributes (visibility metadata, not a live feed), `workflow.SideEffect`
(makes a *value* replay-safe, not an I/O mechanism). Conclusion: a small trace-only Activity is the
correct, not just convenient, answer — there is no other Temporal-native way to get an I/O-free
workflow decision published live. Full write-up not preserved as a separate doc; summarized here.

**What was built:**

- `go/internal/appflow/activities.go`: `TraceEventInput` (small: `run_id`, `node_id`, `kind`,
  `event_type`, optional `detail` string — never a full prompt/response). New
  `AppFlowActivities.TraceNodeEventActivity` — one `StreamPub.XAdd` call to the existing
  `them:dash:run:{runID}:stream` key, same wire shape as the existing `token`/`done`/`error`
  events. New shared `emitTrace` helper used by both `TraceNodeEventActivity` and inline calls
  from `ExecuteRouterActivity`, `ExecuteHILActivity`, `InvokeAgentActivity`, `InlineLLMActivity` —
  these four already do I/O, so they publish directly instead of calling the new activity.
  **Never fails the caller** — a tracing publish failure must never affect node execution or
  trigger a retry, same rule already established for `InlineLLMActivity`'s token-streaming XAdd.
- `go/internal/appflow/workflow.go`: new `AppFlowTraceNodeEventActivityName` constant; new
  `traceNode(ctx, ...)` workflow-side helper (fire-and-ignore via `.Get(ctx, nil)`, error
  discarded) used by Condition, Fork, and Join in the main dispatch loop — the three kinds with no
  activity of their own. Fork's `node_done` detail is `branches=<N>`; **the join node's own trace
  fires from the fork case here, once, after `wg.Wait()`** — not from `walkBranch`, since each
  branch's loop stops as soon as it reaches the join node and never actually visits it (see below).
- `go/internal/appflow/graph.go` (`walkBranch`): same Condition trace calls, for a Condition node
  reached inside a fork branch. The `case "join"` here is defensive/correct for a join reached via
  some other path (not the fork-designated `stopID`) but is not exercised by the simple 2-branch
  fork/join topology this phase tested — not a bug, just an untested edge case worth knowing about.
- HIL is special-cased: `ExecuteHILActivity` only emits `node_start` (and `node_error` if it fails
  before persisting) — the actual approval outcome (`approved`/`rejected`/timeout) is only known
  later, in `execHILNode` (`nodes.go`), after the signal or timer resolves, so that function calls
  `traceNode` directly once the decision is in.
- `go/cmd/dag-worker/main.go`: `AppFlowTraceNodeEventActivity` registered on the AppFlow worker
  alongside the other 5 AppFlow activities (same task queue selection, so debug runs' trace
  activity also executes on the debug pool).
- **10 new tests**, S1-130/S1-131 in `go/TEST_INDEX.md` (S1 total 1325→1335): 7 activity-level
  (`workflow_test.go`) covering start/done/error emission for all 4 activity-backed kinds plus
  `TraceNodeEventActivity` directly; 3 workflow-level (`workflow_temporal_test.go`, new
  `testsuite.WorkflowTestSuite`) proving Condition/Fork/Join tracing actually fires correctly
  end-to-end through a real Temporal workflow test environment — activity-level mocking alone
  can't exercise `traceNode`'s `workflow.ExecuteActivity` call. `go test ./...` 0 failures, full
  suite. One existing test (`TestInlineLLMActivity_StreamPublishesToken`) updated to filter for
  the `"token"`-type payload specifically, since `node_start`/`node_done` now also publish to the
  same stream key.
- `docs/REDIS.md`: added the previously-undocumented `them:dash:run:{run_id}:stream` key (a
  pre-existing gap, not introduced this session) with the new `node_start`/`node_done`/`node_error`
  types noted alongside the existing ones. Confirmed and documented: unknown `type` values are
  silently ignored by both `sse/handler.go` (`return true // skip unknown event types`) and
  `ws/handler.go` (`default: return nil`) — these new types are safe to ship without any consumer
  change or wire-format migration.

**Phase 2 gate — MET:** every node kind in a real Graph-mode run now publishes a live
`node_start`/`node_done`/`node_error` event to the run's existing Redis Stream, unconditionally,
provably via an end-to-end Temporal workflow test — not just unit-level activity mocks. No
persistence, no UI consumption, no per-node config — all explicitly deferred to later phases.

**Deliberately deferred, not decided here (do not assume settled):**
- Whether input/output snapshots are ever captured in `detail` beyond the current short strings —
  the user explicitly scoped this phase to avoid capturing full prompts/inputs/outputs "unless
  already safely available and clearly required," and none of the 7 node kinds needed that to hit
  this phase's goal.
- Per-node debug configuration (a `DebugConfig`/`TraceMode` field on `AppFlowNode`, redaction
  rules, etc.) — discussed and explicitly deferred by the user as a possible Phase 3/4 concern,
  not built here. If it resurfaces, the candidate shape discussed was one generic field on
  `AppFlowNode` (e.g. `TraceMode: ""|"full"|"redacted"|"off"`), not a per-field redaction schema.
- The Phase 1 plan doc's original open question ("unconditional vs debug-gated emission") is now
  answered by this phase's implementation: **unconditional emission**, matching the user's explicit
  instruction. Persistence gating is Phase 3/4's job, unchanged.

---

### 1. A separate Temporal task queue + worker pool for debug runs

New task queue constants (mirroring the existing `AppFlowTaskQueue = "appflow-dag"` /
`CanvasDAGTaskQueue = "canvas-dag-nodes"` in `go/internal/appflow/workflow.go` /
`go/internal/temporal/canvas_workflow.go`):
- `AppFlowDebugTaskQueue = "appflow-dag-debug"`
- (If agent-builder Temporal-mode debug is ever wanted too: `CanvasDAGDebugTaskQueue =
  "canvas-dag-nodes-debug"` — out of scope for this plan, app-canvas only, but naming reserved.)

New worker containers, same image as `them-dag-worker`, polling the debug queue instead:
`them-dag-worker-debug` (+ optional `-debug-2` for a second replica, same pattern as the
production replica added this session). Same `Dockerfile.dag-worker`, same activities registered
— **debug workers run the exact same code as production workers**, this is not a separate
codebase or a simulator; it is the same interpreter, isolated by queue so debug load and
experimentation cannot starve or interfere with real app traffic. This is what makes it "better
than" a from-scratch simulator: zero behavioral drift between what debug shows you and what
production actually does, by construction.

**Routing:** `go/internal/execution/lifecycle.go`'s `StartAppFlow` currently hardcodes
`TaskQueue: appflow.AppFlowTaskQueue` (line ~680). Needs a `debug bool` (or equivalent) parameter
threaded from the WS/HTTP debug-start call down to this dispatch point, selecting
`AppFlowDebugTaskQueue` instead when true.

### 2. Per-node trace events — the new instrumentation every activity needs — ✅ DONE (Phase 2)

**RESOLVED (Phase 2, see the completion section above):** unconditional emission, every run,
every node kind. Actual shape shipped is deliberately smaller than originally sketched here —
`{run_id, node_id, kind, detail?}`, no `started_at`/`output_snapshot`/`latency_ms` — the user
scoped Phase 2 down to "just enough to follow the path live," explicitly deferring snapshots and
timing to whenever Phase 3 (persistence) actually needs them.

### 3. Durable per-node trace storage

Needs a `run_steps`-equivalent for AppFlow node executions — either a new nullable-`node_id`/
`kind` pair added to the existing `them.run_steps` table (reusing the table, since its current
shape — `agent_id`/`agent_slug`/`iteration`/`tool_call_id` — doesn't fit a DAG node, but the
table's *purpose*, "queryable trace of one run," does), or a new sibling table
`them.appflow_run_steps`. This is what makes a debug session's trace **inspectable after the run
ends**, not just while a browser tab happens to be open watching it live — the concrete
improvement over the agent builder's in-memory-only session.

### 4. Frontend: setup panel + live canvas overlay

- New `useAppFlowDebugSession` hook (app-canvas equivalent of `useDebugSession.ts`, but as a real
  WS/SSE consumer of the trace events from point 2, not a simulator loop).
- Setup panel reusing the `buildDebugParamSpecs`-style scan: walk the canvas's actual nodes,
  build a form for whatever they need (LLM provider/model/key-or-mock, HTTP app_params), same as
  agent builder's `DebugPanel.tsx`.
- Canvas overlay: same visual language as `StepNode.tsx`'s debug border/glow states
  (`idle|pending|running|done|error`), applied to `CanvasNodes.tsx`'s node components
  (`InlineNode`, `FlowControlNode`, `AgentNode`, etc.) when a debug session is active — driven by
  real `node_start`/`node_done`/`node_error` events arriving over the WS/SSE connection, not by a
  synchronous in-browser `await` loop.
- Run All / Step controls in the toolbar, same interaction model as the agent builder's debug bar.

---

## Phase 5 — design decisions (confirmed with user, 2026-09-23)

Research before starting Phase 5 found the plan above assumed two things that don't exist yet:
1. Trace events (`node_start`/`node_done`/`node_error`, shipped in Phase 2) never reach a browser
   today — `ws/handler.go`'s and `sse/handler.go`'s event writer silently drops any event type it
   doesn't recognize (`default: return nil`). Must add cases for these three types first.
2. There is no "Run" trigger anywhere in the app-canvas UI at all, debug or not. The only existing
   run-start paths (`ws/handler.go` route, `sse/handler.go` route) require a **published** entry
   point (`handle.EPConfig.ActiveDefinitionJSON`) — but a canvas being debugged is normally an
   unpublished **draft**. `StartAppFlow(..., debug bool)` is hardcoded `false` at exactly two call
   sites: `go/internal/ws/handler.go:543`, `go/internal/sse/handler.go:445`.

**Decisions:**
- **Debug runs the draft directly — no publish required.** Publishing is a production/deployment
  step and must stay unrelated to debugging. A new backend path is needed to start an AppFlow
  workflow from draft JSON (not `ActiveDefinitionJSON`).
- **New dedicated debug-start route**, not a flag added to the production WS/SSE routes. Keeps
  production request handling completely untouched, and can accept draft JSON directly (which the
  production routes have no reason to ever accept). Shape: something like
  `POST /admin/applications/{id}/debug/start` taking the draft canvas JSON in the body, returning a
  run/workflow ID the frontend then follows over WS/SSE for live trace events.
- Also confirmed: no AppFlow node kind declares `app_params` today (unlike the agent builder's HTTP
  node) — the "HTTP node → param spec" branch of `buildDebugParamSpecs()` has no direct analogue to
  port yet. LLM node provider/model/key live in Runtime config, not canvas node config, so the
  param-spec scan for Phase 5 is narrower than the agent builder's until a future AppFlow node kind
  adds declared params.
- `CanvasNodes.tsx` (427 lines) and `CanvasBuilderView.tsx` (645 lines) are already over the
  400-line guideline — new debug-state styling and Run/Debug controls land in new sibling files,
  not added to those two directly.

**Known limitation, found while writing Phase 5's service-layer tests — FIXED 2026-09-24
(Platform-as-Tenant Phase 6, `docs/PLATFORM_AS_TENANT_PLAN.md`):** a draft canvas containing agent
nodes could not be debugged until it had been **published at least once**.
`appflow.ResolveAgentByInstanceID` reads a `_resolved_agent_ids` map that is only stamped into the
definition JSON by `PublishDefinition` at publish time — a draft that has never been published has
no such stamp, so `Validate` correctly rejected it with `unresolved_agent`, even though "debug the
draft directly, no publish required" was this phase's whole design decision. Flows made entirely of
inline nodes (LLM, Condition, Router, etc. — no agent components) were unaffected, since they need
no such resolution.

**Fixed** by `AppFlowDebugService.resolveDraftAgentIDs` (`internal/admin/service/
appflow_debug.go`) — resolves any agent-kind component the publish-time stamp didn't already cover,
live, using the exact same `RegistryResolver.ResolveForPublish` + `AgentExists` pair
`PublishDefinition` itself uses (same server-side, tamper-proof lookup, just invoked at debug-start
time too, not only at publish). No stamping-on-every-save needed — this took the second of the two
options originally proposed here. See `docs/PLATFORM_AS_TENANT_PLAN.md`'s "Phase 6 — COMPLETE"
section for the live end-to-end verification (a real, still-unpublished app's debug/start call went
from `unresolved_agent` to a real run, without ever publishing it) and `docs/LESSONS.md` for why
this was a real design gap and not just an acceptable documented limitation.

---

## Phase 5 — frontend (setup panel + Run All + real WS consumer) — COMPLETE (2026-09-23)

**What was built**, mirroring the agent builder's `useDebugSession.ts`/`DebugPanel.tsx`/
`StepNode.tsx` UX but as a real WS consumer, not a simulator:

- `frontend/src/app/admin/applications/hooks/useAppFlowDebugSession.ts` (new) — `runAll()` POSTs
  `/admin/applications/{id}/debug/start` with the selected entry-point slug + test message, then
  opens `/ws/dashboard`, subscribes to `run:{run_id}`, and maps incoming `node_start`/`node_done`/
  `node_error`/`done`/`error` events into per-node state (`idle|pending|running|done|error`) plus a
  `decorateNodes()` helper that stamps `_debug` onto matching canvas nodes for the overlay below.
  Entry-point options are scanned live from the canvas's own `entryPoint` nodes (`n.data.slug`) —
  no separate lookup needed. No LLM/HTTP param-spec branch exists yet (confirmed in the earlier
  design-decisions section: no AppFlow node kind declares `app_params` today), so setup is just
  entry point + test message.
- `frontend/src/app/admin/applications/components/AppFlowDebugPanel.tsx` (new) — setup form +
  Run All/Reset/Close buttons + status line, styled like the agent builder's `DebugPanel.tsx`.
- `frontend/src/app/admin/applications/components/CanvasNodes.tsx` — `InlineNode`/`FlowControlNode`
  gained a `_debug` overlay (border/glow keyed by state, small "running…"/detail text), reusing
  `StepNode.tsx`'s exact color scheme. Kept as a small addition to two existing files rather than a
  third sibling file, since the alternative (threading debug state through a wrapper component) was
  a bigger diff for no real benefit — the file-size guideline note in the design-decisions section
  above was about *new* debug-state styling files, not modifying existing node renderers in place.
- `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx` — new amber "▶ Debug"
  button next to Export JSON (only shown once a draft is loaded), renders `AppFlowDebugPanel` when
  active, and passes `appFlowDebug.decorateNodes(nodes)` into the canvas instead of raw `nodes`.
- `frontend/src/lib/api.ts`/`apiTypes.ts` — `themApi.startAppFlowDebug()` + `AppFlowDebugStartResult`.

**A real, pre-existing backend bug was found and fixed while live-testing this** (no
browser-automation tool was available — see the note below on how this was verified instead):
**`/ws/dashboard`'s `run:*` channels never delivered live events, only a one-shot snapshot.**
`internal/runstream.PublishEvent` (called by every AppFlow node's `emitTrace`) only `XADD`s to the
run's Redis Stream — nothing ever `PUBLISH`es to the parallel pub/sub channel `/ws/dashboard`
actually subscribes to. The old `sendRunSnapshot` ran exactly once, at subscribe time, via
`XRevRange` — so any run fast enough to finish before that single Redis round-trip landed (every
debug run tested this session: mock-LLM AppFlow runs complete in well under 1 second end-to-end)
delivered **zero** events to the client, live or otherwise. This wasn't a Phase-5-only bug — the
playground's own `run:*` trace pane (`useChatConnection.ts`'s `openDashWs`) has the identical latent
gap; it went unnoticed there because that pane is secondary (the primary chat response streams over
a different WS that already uses the correct mechanism) and because orchestrator-mode runs are
usually slow enough that a human clicking around happens to subscribe after some entries already
exist.

**The fix:** `go/internal/dashboard/handler.go` — `run:*` channels are no longer handed to the
generic pub/sub `Subscribe` call at all. Each one is now tailed by a new `tailRunChannel` method
that calls `internal/runstream.StreamFromRedis` — the same replay-then-live-XREAD-BLOCK primitive
`internal/ws`/`internal/sse` already use for the production routes — so a `run:*` subscriber now
gets full history-so-far plus every event published after subscribing, with no gap, exactly like
the production WS/SSE routes already guaranteed. `Handler` gained a `streamer
runstream.RedisStreamer` field (threaded from `cmd/them/main.go`'s existing `rsStreamer` instance,
already built for the WS/SSE handlers — no new Redis client needed); nil-safe, so the old
one-shot-snapshot path still runs when a test constructs a `Handler` without one (`NewForTest`'s
existing signature gained the parameter; every pre-existing test passes `nil` and is unaffected).

**Verified against the live stack, not just unit tests** — no browser-automation tool was available
in this environment (Playwright's Chromium downloaded but its shared-library dependencies couldn't
be installed without interactive sudo; confirmed the same limitation every prior session on this
plan hit). Instead: a Node script run inside the already-running `them-frontend` container (same
Docker network, so it can reach `them-auth-go`/`them-go-bridge` directly) logged in, created a real
throwaway application + draft definition (EP + inline LLM + condition + two branch LLM nodes, no
agent nodes — sidesteps the known agent-node limitation above) + its `them.entry_points` row (a
separate table from the draft JSON, discovered via this exercise — canvas save presumably keeps it
in sync via `POST .../entry-points`, which this script also called directly), called
`debug/start`, and opened the exact same `/ws/dashboard` subscribe flow the new hook uses. Before
the fix: `{"type":"subscribed",...}` and then nothing for 20s, despite the run completing and its
Redis Stream containing the full correct event sequence (confirmed via direct `XRANGE`). After the
fix: the full sequence arrived live over the WS —
`node_start(llm_1)→node_done(llm_1)→token→node_start(cond_1)→node_done(cond_1,
detail=branch=true)→node_start(llm_true)→node_done(llm_true)→token→done` — in order, matching
exactly what the new frontend hook parses. Also separately confirmed the documented "agent nodes
need publish first" limitation is real and correctly surfaced as a 422
(`validate: [unresolved_agent] ...`) by trying an existing draft (`stage2-graph-llm-condition-v2`)
that does have agent nodes.

**Tests:** 2 new in `go/internal/dashboard/handler_test.go` (`TestDashboard_RunChannel_
TailsLiveInsteadOfPubSub`, `TestDashboard_RunChannel_NoStreamerFallsBackToOneShotSnapshot`) using a
small fake `runstream.RedisStreamer` — `go/TEST_INDEX.md` S1-52 bumped 13→15, new S1-149 row, S1
total 1417→1419. `go test ./...` — 0 failures, full suite (58 packages), run via
`docker run golang:1.25-alpine` (no local Go toolchain on this box). `npx tsc --noEmit` — 0 errors.
`them-go-bridge` rebuilt (Dockerfile runs the full suite in-image, 0 failures confirmed again
there) and force-recreated; logs confirm healthy startup. `docs/REDIS.md`'s
`them:dash:run:{run_id}:stream` entry updated to document the new consumer + the bug it fixes.

**Not done / deferred to Phase 6 (not a regression):** Step controls (pause after each node,
lockstep multi-branch stepping) — this phase only built Run All, per the plan's own phase split.
The known "agent nodes need publish first" limitation from the backend slice above remains
unfixed — still flagged for a later session, unrelated to this phase's WS-delivery fix. No actual
logged-in browser click-through was performed (see the live-stack verification note above for why,
and what was done instead) — recommend a manual pass through the new "▶ Debug" button before fully
trusting the UI layer specifically (the WS/backend contract it depends on is now proven live).

---

## Explicitly out of scope for this plan

- Agent-builder-side changes — this plan is app-canvas only. (Reserved naming for a future
  `CanvasDAGDebugTaskQueue` if the agent builder's own Temporal-mode execution ever wants the
  same treatment, but not designed here.)
- Persisting per-node traces for non-debug (production) runs — see the open design question in
  section 2; deferred pending explicit decision.
- Any change to "Simple" (orchestrator) mode's debug story — out of scope; this plan targets
  Graph mode specifically, since that's where the per-node concept exists at all.

---

## Decisions confirmed with the user (round 2)

1. **The Run History "Flow" tree tab already exists and is the target to fix, not replace.**
   `frontend/src/app/runs/page.tsx`'s `buildGraph()` (in `runsTypes.ts`) renders a tree from
   `detail.steps` (`them.run_steps`) — this works correctly for "Simple" (orchestrator) runs
   today. It renders **empty for Graph-mode runs**, because `RecordAgentStep` (the only writer
   of `run_steps`) is called exclusively from `internal/orchestrator/` — confirmed zero call
   sites from `internal/appflow/`. This plan's job is to make AppFlow write to the same trace
   infrastructure so this same tree populates for Graph-mode runs too, not to build a second,
   parallel visualizer.
2. **Add a runtime-configurable verbosity/log-level setting** (per app, in Runtime settings —
   mirrors the existing per-app Temporal config pattern, e.g. `RuntimeTemporalTab.tsx` /
   `app_temporal_config`) controlling how much per-node detail gets recorded: e.g. `off` (no
   per-node trace — today's behavior), `status` (just node_id/kind/status/latency, no
   input/output snapshots — cheap), `full` (status + input/output snapshots — what debug mode
   needs). Debug mode always forces `full` for its own run regardless of the app's configured
   default; the app-level setting governs non-debug runs.
3. **Lean toward extending `them.run_steps`** with nullable DAG-node columns (`node_id`, `kind`)
   rather than a new sibling table — reuses the existing history infrastructure (`buildGraph()`,
   the Run History UI, the runs API) instead of duplicating it. Exact column design deferred to
   implementation time, but this is the stated direction, not still fully open.
4. **Debug worker pool supports N replicas**, same mechanism as the production pool — confirmed
   this needs no new design: Temporal's SDK already load-balances any number of workers polling
   one task queue, exactly like `them-go-worker`/`them-go-worker-2` and
   `them-dag-worker`/`them-dag-worker-2` added earlier this session. Sizing (smaller/cheaper
   hardware than production) is a `docker-compose` resource-limit concern, orthogonal to the code
   above, decided at rollout time.
5. **Step semantics for branching (Condition/Router/Fork): step ALL currently-active nodes
   together, one tick at a time** — not one node at a time globally. If Fork just split into 2
   branches, the next Step click runs one node in *each* branch simultaneously, keeping them in
   lockstep. This is the honest representation of how the engine actually executes (branches
   genuinely run concurrently) rather than an artificial single-file ordering. Confirmed with the
   user as the chosen approach over "step one branch, auto-run the others."

### Phase 3 — COMPLETE (2026-09-22)

**Bug found and fixed first, before Phase 3 could safely extend this table:**
`them.run_steps.tool_call_id` was `TEXT NOT NULL` with no default, but
`internal/runrecorder.RecordAgentStep` (its only writer) never included it in the INSERT column
list — every orchestrator-mode insert should have been failing this constraint against a real
Postgres instance. Not caught earlier because the unit test mocks the DB (`mockDB` records SQL/args
only, never executes). Fixed by dropping the column in migration `db/103_run_steps_appflow_trace.sql`
— no reader depended on it beyond an always-empty `COALESCE(tool_call_id, '')` display field. Full
write-up in `docs/LESSONS.md`.

**What was built:**

- `db/103_run_steps_appflow_trace.sql`, applied to the live DB: drops `tool_call_id`; adds nullable
  `node_id TEXT`, `node_kind TEXT`; sets `iteration` default `0` (AppFlow rows have no loop-iteration
  concept, so `0` is a not-applicable sentinel rather than making the column nullable); adds a
  partial unique index `idx_run_steps_run_node ON (run_id, node_id) WHERE node_id IS NOT NULL` so an
  AppFlow node's `node_start` insert and its later `node_done`/`node_error` update target the same
  row via `ON CONFLICT`, while never constraining orchestrator-mode rows (`node_id` always NULL
  there, `run_id` legitimately repeats across many of them).
- `go/internal/appflow/activities.go`: new `(a *AppFlowActivities) persistTrace(...)`, called from
  `emitTrace` (the single choke point every node kind's trace already flows through — Router/HIL/
  Agent/Inline-LLM inline, Condition/Fork/Join via `TraceNodeEventActivity`). `node_start` inserts a
  `running` row; `node_done`/`node_error` update that same row to `completed`/`failed` with
  `ended_at`/`latency_ms`/`output`-or-`error` set. Uses `a.DB` directly (already wired to the
  Admin/BYPASSRLS pool — same pool `ExecuteHILActivity`'s `them.hil_approvals` insert already uses,
  confirmed by dedicated research this session), so no RLS `SET app.tenant_id` dance is needed, only
  a plain `INSERT`/`UPDATE`. Never fails the caller, same rule as `emitTrace` itself.
- `go/internal/admin/dal/dal.go` (`RunStep` struct) + `go/internal/admin/dal/runs.go`
  (`GetRunDetail`'s steps query): `ToolCallID` field/column replaced with `NodeID`/`NodeKind`.
- `frontend/src/lib/apiTypes.ts` (`RunStep`): same field swap.
- `frontend/src/app/runs/runsTypes.ts` (`buildGraph`): now branches on whether any step has a
  non-empty `node_id` (the only signal distinguishing an AppFlow run's steps from an orchestrator
  run's — no separate mode field exists on `Run`). AppFlow rows go through new `buildDagGraph`,
  which renders nodes as rows in `started_at` order — nodes starting within 1 second of each other
  group into one "parallel" row (an approximation for fork branches; the trace data has no explicit
  branch/group id to do this exactly). This is **not** a true branch/merge graph layout with drawn
  edges — deliberately reuses the existing renderer's row/parallel-row model instead of building a
  second visualizer, per this plan's own direction ("fix the existing Flow tree, don't replace it").
  Orchestrator runs go through the unchanged (renamed) `buildOrchestratorGraph`.
- `frontend/src/app/runs/RunGraph.tsx`: new `dagnode` card kind (renders `node_kind`/`node_id`/
  `status`/`latency_ms`, and on expand, `output` as "Detail" + `error`).
- **Go tests:** `go/internal/appflow/trace_persist_integration_test.go` (new, `-tags=integration`,
  needs live Postgres — `persistTrace` calls a concrete `*pgxpool.Pool`, not an interface, so this
  can't be a plain unit test) — S2-12 in `go/TEST_INDEX.md`, 4 tests (PT-1..4): upsert-on-conflict
  collapses node_start+node_done into one row; node_error sets failed status with error text
  preserved; nil-DB no-ops without panicking; empty-node_id inserts nothing. All 4 pass against the
  live `them-postgres` (using the real `THEM_DB_URL_ADMIN` value from `.env`, not the test file's
  placeholder default). `go test ./...` 0 failures, full suite (55 packages) — confirmed both via
  direct `docker run golang:1.25-alpine` and again inside the `them-dag-worker` image build (which
  runs the full suite at build time).
- **Frontend:** `tsc --noEmit` 0 errors. No frontend test framework exists anywhere in this repo to
  add a `buildGraph` unit test to (confirmed by search) — verification is `tsc` + the container
  rebuild/health-check below, consistent with how every other frontend-only change in this repo's
  history has been verified.
- **Deployed and verified healthy:** `them-dag-worker`, `them-dag-worker-2`, `them-dag-worker-debug`
  (activities.go changed, all three are the same image), `them-go-bridge` (DAL changed), and
  `them-frontend` (buildGraph/RunGraph changed) all rebuilt and force-recreated; logs confirm clean
  startup (dag-workers polling both queues, go-bridge answering `/health/live` 200, frontend serving
  `/login` 200) — no crash loops.

**Not done / explicitly deferred to a later phase (not a regression):**
- No live full end-to-end Temporal-workflow-through-WS-to-Postgres round trip was performed this
  session (unlike Phase 1/2, which used the `temporal` CLI directly against a hand-built workflow
  input). The integration test instead calls `persistTrace` directly with a real `*pgxpool.Pool` —
  the exact same function `emitTrace` calls internally, so the SQL/upsert logic is proven against
  live Postgres, but a real Condition/Fork/Join node hasn't been watched writing its row through a
  live workflow run in this session. Recommend a manual `temporal workflow start` verification
  (same recipe as Phase 1/2) before fully trusting this against a truly live run, if that matters
  before Phase 4 begins.
- `buildDagGraph`'s 1-second time-window grouping is a heuristic, not a structural fact — it can
  mis-group two genuinely sequential (not concurrent) fast nodes as "parallel," or fail to group two
  fork branches that happen to start slightly more than 1 second apart. A structurally correct
  rendering would need the trace data (or `run_steps`) to carry an explicit branch/fork-group id,
  which doesn't exist yet — flagged as a candidate improvement, not built here (was explicitly out
  of scope: this phase's gate was "populate the storage + make the existing tree not blank," not
  "draw a pixel-perfect DAG").
- No per-app log-verbosity setting exists yet — that's Phase 4. Every AppFlow run's steps are
  persisted unconditionally right now (matching Phase 2's unconditional trace-emission decision),
  with no way to turn it off for a high-volume production app. Phase 4's job.
- `agent_id` on `run_steps` remains unpopulated by either writer (orchestrator or AppFlow) — flagged
  in Phase 3's research but intentionally left alone; out of scope for this phase.

### Phase 4 — COMPLETE (2026-09-22)

**Open question RESOLVED, by explicit user decision this session:** `off` means zero
`them.run_steps` writes — a full revert to Phase 2's live-Redis-only behavior for that run, not a
second "cheap but not nothing" tier. Rationale recorded at decision time: a verbosity setting whose
cheapest tier still writes a row isn't really "off," and the existing precedent in this codebase is
that nothing persists today, so `off` mapping to "unchanged, no surprise write" is the least
surprising default. This makes `status` the meaningful middle tier (lightweight history, no
payloads) and `full` debug's tier (everything) — a clean three-way split with no overlap.

**What was built:**

- `db/104_app_log_verbosity.sql`, applied to the live DB: new table `them.app_debug_config`
  (`application_id` PK, `log_verbosity TEXT NOT NULL DEFAULT 'status' CHECK IN ('off','status','full')`,
  `updated_at`) — single-tier per-app setting, no platform-level default row like
  `app_temporal_config` has, since the plan only ever specified "per app." Mirrors
  `app_temporal_config`'s shape (same PK/FK/GRANT pattern) per `docs/SCHEMA.md`.
- **Handler → Service → DAL**, mirroring `TemporalConfigHandler`/`ConfigService`/`temporal_config.go`
  exactly: `internal/admin/dal/log_verbosity.go` (`GetAppLogVerbosity`/`UpsertAppLogVerbosity`,
  `IsValidLogVerbosity`, exported `LogVerbosityOff`/`Status`/`Full`/`DefaultLogVerbosity` constants),
  `internal/admin/service/config.go` (`GetLogVerbosity`/`PutLogVerbosity`, enum validation via the
  existing `unprocessable()` helper → 422), `internal/admin/log_verbosity.go`
  (`LogVerbosityHandler.AppRoutes`, `GET`/`PUT`). Mounted in `router.go` alongside the existing
  per-app `temporal-config` route (`GET`/`PUT /admin/applications/{id}/log-verbosity`,
  tenant-scoped).
- **Resolution at workflow-start time**, mirroring `TemporalConfigLoader`/`PgxTemporalConfigLoader`
  exactly: new `AppFlowWorkflowInput.LogVerbosity string` field; `internal/appflow/log_verbosity_loader.go`
  (`PgxLogVerbosityLoader`); `internal/execution/lifecycle.go` gained the `LogVerbosityLoader`
  interface + `WithLogVerbosityLoader` + resolution logic in `StartAppFlow` — fail-open to
  `dal.DefaultLogVerbosity` on nil loader or load error, **and unconditionally overridden to
  `dal.LogVerbosityFull` when `debug=true`**, regardless of what the loader returned. Wired in
  `cmd/them/main.go` next to the other AppFlow loaders.
- **Threaded through every trace call site**, since the resolved level must be visible inside the
  activity process (a separate process boundary from the workflow that resolved it), the same
  reason `Debug` itself never needed to cross that boundary but `LogVerbosity` does:
  `TraceEventInput` and all 4 activity input structs (`RouterActivityInput`, `HILActivityInput`,
  `AgentInvokeActivityInput`, `InlineLLMActivityInput`) gained a `Verbosity string` field;
  `traceNode` (workflow.go) and `emitTrace` (activities.go) both gained a `verbosity` parameter; all
  10 call sites across `workflow.go`, `graph.go` (`walkBranch`), and `nodes.go`
  (`execRouterNode`/`execHILNode`) pass `input.LogVerbosity` through.
- `persistTrace` now branches on verbosity: `off` returns immediately (no DB call at all — the live
  Redis publish in `emitTrace` happens independently and is unaffected); `status` writes/updates the
  row but passes empty strings for `output`/`error` regardless of event type; `full` (or empty, for
  fail-open compatibility with any caller that predates this field) is unchanged from Phase 3.
- **Frontend:** new Runtime tab "Trace Logging" (`RuntimeLogVerbosityTab.tsx`, mirrors
  `RuntimeTemporalTab.tsx`'s load/save/status-message structure but as a single dropdown, not
  numeric override fields) — off/status/full with an inline description of each level so a tenant
  admin doesn't need to read this doc to make the choice. `apiTypes.ts` (`LogVerbosity`,
  `LogVerbosityConfig`), `api.ts` (`getLogVerbosity`/`putLogVerbosity`), wired into `RuntimeView.tsx`
  as a third tab alongside General/Temporal.
- **Tests:** 5 service tests (LV-SVC-1..5), 5 handler tests (LV-1..5), 4 `Lifecycle.StartAppFlow`
  tests covering no-loader-default / loaded-value-used / debug-forces-full / loader-error-fail-open
  (S1-132..134 in `go/TEST_INDEX.md`, S1 total 1335→1349), plus 3 new integration tests against live
  Postgres (PT-5..7 in S2-12) proving `off` writes nothing and `status` withholds detail on both
  success and failure. `go test ./...` 0 failures, full suite (confirmed via
  `docker run golang:1.25-alpine`, no local Go toolchain on this box). The integration suite (S2-12,
  all 7 tests including the 3 new ones) was run for real against the live `them-postgres` container
  on this box, joined via its `them-network` Docker network using the DSN read live from
  `them-dag-worker`'s own running environment (never printed) — not skipped for lack of a host-exposed
  Postgres port. `tsc --noEmit` 0 errors.

**Not done / deliberately deferred (not a regression):** no live end-to-end Temporal-workflow run
was started this session to watch a real `off`/`status` run's rows (or lack thereof) land through
the full stack — verification was via the integration test calling `persistTrace` directly (same
function `emitTrace` calls internally) plus the full unit suite. No container rebuild/redeploy was
performed this session either (`them-dag-worker`/`-2`/`-debug`, `them-go-bridge`, `them-frontend`
all need a rebuild to pick up this phase's code before it's live on this box) — recommended as the
first step of whichever session verifies this phase or starts Phase 5.

---

## Remaining open question (Phase 4)

None. The Phase 4 open question (see above) is resolved. No new open questions raised by that
phase.

---

## Phase 6 — Step controls — COMPLETE (2026-09-23)

**The mechanism problem, found before writing any code:** the plan's round-2 decision #5 requires
"one Step click advances every currently-active node together, in lockstep" — including every node
in every currently-active fork branch simultaneously. The obvious first design (send one
`SignalWorkflow` call per currently-paused branch) was checked against the Temporal Go SDK's actual
source before implementing (`vendor/go.temporal.io/sdk/internal/internal_workflow.go`,
`sendAsyncImpl`) and confirmed wrong: a named signal channel is a FIFO mailbox — one `SignalWorkflow`
call wakes exactly the one longest-waiting blocked `Receive`, never all of them. With N branches each
blocked on their own `Receive` for the same signal name, one signal would silently release only one
branch, stranding the rest — exactly the bug decision #5 exists to avoid.

**The fix — a shared tick-generation counter + `workflow.Await`, not per-branch signals:**
- `stepTick` (`go/internal/appflow/workflow.go`): one `Gen int` field, one instance per workflow
  execution, created only when `AppFlowWorkflowInput.StepMode` is true.
- `startStepListener`: the ONLY goroutine that ever calls `Receive` on the new `AppFlowSignalStep`
  signal. Each signal received increments `tick.Gen` by one and loops back to `Receive` again.
- `stepGate(ctx, tick, lastSeenGen, ...)`: called at the top of every node dispatch point (the main
  loop in `workflow.go`, and `walkBranch`'s loop in `graph.go`). Fires a new `node_paused` trace event,
  then blocks on `workflow.Await(ctx, func() bool { return tick.Gen > *lastSeenGen })`. Each caller
  (the main path, and each fork branch independently) keeps its own `lastSeenGen` cursor, seeded from
  the fork node's own cursor at the moment branches are spawned — so a signal that released the fork
  node itself does not also silently free-ride the first node of every branch.
- Bumping `tick.Gen` once is a real broadcast: every blocked `workflow.Await` condition across every
  goroutine is re-evaluated by the SDK's dispatcher in the same tick, so N paused branches all wake
  together from one counter change — this is the actual mechanism, `workflow.Await` conditions are
  polled on every coroutine yield point, not tied to a single channel consumer. `nil` on `stepTick`
  (i.e. `StepMode=false`, Run-All) makes `stepGate` a no-op — one nil-check per node, zero behavior
  change to today's Run-All path.
- New signal constant `AppFlowSignalStep = "appflow_step"` next to `AppFlowSignalHILApproval`.
- New route `POST /admin/applications/{id}/debug/{run_id}/step` (`internal/admin/appflow_debug.go`,
  `AppFlowDebugHandler.Step`) — mirrors `HILApprovalsHandler`'s existing signal-sending pattern
  exactly: `dal.GetRun(tenantID, runID)` verifies the caller's own tenant actually owns the run before
  ever signaling (same cross-tenant-IDOR-safe pattern the HIL approval routes and the Phase 5
  `/ws/dashboard` fix both already established — never trust a URL param alone), then
  `TemporalSignaler.SignalNamedWorkflow(WorkflowIDForRun(tenant, run), AppFlowSignalStep, nil)`.
  Best-effort: a run that already completed or hit its debug lifetime ceiling surfaces as 409, not a
  crash. `debugStartBody.StepMode bool` (`step_mode` in the JSON body) threads through
  `AppFlowDebugService.Start` into `AppFlowWorkflowInput.StepMode`.
- New trace event `node_paused` — added to `ws/handler.go`'s and `sse/handler.go`'s existing
  `node_start`/`node_done`/`node_error` forwarding switch (same `{type, run_id, node_id, kind,
  detail?}` wire shape, generic `ev.Type` passthrough — no new wire-format work needed). Not
  persisted to `them.run_steps` — it's a transient "waiting" signal, not a lifecycle state
  `persistTrace`'s insert-then-update model has a slot for; only the live Redis Stream publish in
  `emitTrace` fires for it.

**Proven against a real Temporal workflow test environment, not just unit mocks** — the whole point
of this phase is concurrency behavior that a plain unit test can't exercise. Three new tests in
`internal/appflow/workflow_temporal_test.go` (extends the existing `AppFlowTraceWorkflowTestSuite`):
1. `TestStepMode_NoSignalSent_WorkflowNeverCompletes` — with zero signals sent, the workflow is
   genuinely blocked at the first node (proven via the trace events captured before the test
   environment's own internal stuck-workflow timeout fires), not running through as Run-All would.
2. `TestStepMode_TwoSignals_AdvancesOneNodeAtATime` — a 3-node sequential chain, 3 signals sent via
   `env.RegisterDelayedCallback`, proving each signal advances exactly one node, in order (including
   the 3rd node, a plain pass-through `orchestrator` kind — every node kind pauses in Step mode, not
   just the ones with interesting business logic).
3. `TestStepMode_ForkedBranches_OneSignalReleasesBothInLockstep` — the core Phase 6 guarantee: a fork
   into 2 branches, first signal releases the fork node, both branches independently reach their own
   `stepGate` and pause (proven via `node_paused` events for both), then a SECOND signal — sent once,
   not twice — releases both branches simultaneously. This is the test that would have caught the
   naive "one signal per branch" design being wrong.

8 new tests total (3 workflow-level above, 1 service-level proving `StepMode` propagates into
`AppFlowWorkflowInput`, 4 handler-level for the new route's HTTP mechanics including the IDOR-safe
404 path) — `go/TEST_INDEX.md` S1-163, S1 total 1475→1483. `go test ./...` 0 failures, full suite.
`go test -race ./internal/appflow/... ./internal/admin/...` — the only race found across 5 repeated
runs is the same pre-existing, already-documented `TestForkJoin_EmitsTraceForAllNodes` flake (Temporal
SDK test-harness internals, unrelated to this phase's code — see `TEST_INDEX.md`'s existing "flaky
(pre-existing)" row); none of the 6 new step-mode/lockstep tests ever appeared as the failing subtest
across any of the 5 runs.

**Frontend** (`frontend/src/app/admin/applications/`):
- `hooks/useAppFlowDebugSession.ts` — new `stepMode` setup field (locked once a run starts, same
  pattern as `entryPointSlug`/`userMessage`), `step()` action (POSTs the new route, all real state
  transitions arrive asynchronously via WS events same as everything else in this hook), new
  `'paused'` case in the WS message switch.
- `types.ts` — `AppFlowDebugNodeState` gains `'paused'`, distinct from `'pending'`: pending means
  "not reached yet" (a UI guess), paused means "the backend has genuinely stopped here" (an observed
  state, carried by a real `node_paused` event).
- `components/AppFlowDebugPanel.tsx` — Step Mode checkbox next to the setup fields; a Step button
  (only rendered once a step-mode run has started) whose enabled state is derived from
  `nodeStates` containing at least one `'paused'` entry — not a separately tracked flag, since
  "paused" is already the real signal.
- `components/CanvasNodes.tsx` — `paused` added to both node components' debug-accent/glow color
  maps (violet, distinct from `pending`'s amber) and a "⏸ paused" label line, mirroring the existing
  `running`/`done` treatment.
- **New `components/AppFlowDebugInspector.tsx`** — click-to-inspect, per this session's explicit
  choice of a side panel over a canvas popover (a popover would compete for space with
  `CanvasNodes.tsx`'s existing per-node Ports popover). Renders in place of
  `CanvasNodePropertiesPanel` in `CanvasBuilderView.tsx`'s existing right-hand panel slot whenever a
  debug session is active — the two panels serve mutually exclusive purposes (editing canvas config
  vs. inspecting a live/finished debug run), so no need to reconcile them into the same space.
  Reads `_debug` off whichever node `onNodeClick` (pre-existing, `CanvasInner.tsx`) last selected —
  no new click-handling wiring needed, since `debugDecoratedNodes` (not raw `nodes`) is what's
  already passed to `<ReactFlow>`, so `_debug` is already present on the clicked node by the time
  `onNodeClick` fires.

`npx tsc --noEmit` — 0 errors, run twice.

**Not done / known limitation, not a regression:** no live browser click-through was performed (same
standing limitation as every phase of this plan — no browser-automation tool, no headless Chromium
system libraries available in this environment without interactive sudo). The Temporal
signal/lockstep mechanism is proven against a real (non-mocked) Temporal workflow test environment;
the HTTP route is proven via handler tests with a fake DB/signaler; the frontend is proven via
`tsc` only. Recommend a manual logged-in walkthrough before trusting the Step button/panel UI
rendering itself: start a step-mode debug run on a flow with a fork, click Step repeatedly, confirm
both branches visibly pause and advance together on the canvas, and confirm the inspector panel
shows the right per-node detail when clicking a paused/done node. Also unchanged from Phase 5: a
draft canvas containing agent nodes still needs to have been published at least once before it can
be debugged at all (`unresolved_agent`) — orthogonal to Step controls, not addressed here.

---

## Phase 6 — review fix: `mainLastSeenGen` staleness across a fork (2026-09-24)

**A real bug, found by the user's review, not caught by Phase 6's own tests:** after a fork's
branches converge (`wg.Wait(ctx)` returns), `tick.Gen` has already advanced from whatever signals
released those branches — but the main loop's own step cursor, `mainLastSeenGen`, was never updated
to match. The very next `stepGate` call (for the node immediately after the join) then saw
`tick.Gen` already ahead of its stale cursor and ran **immediately, for free, without its own Step
click** — silently skipping a pause point right after every fork/join in Step mode. Phase 6's
original lockstep test (`TestStepMode_ForkedBranches_OneSignalReleasesBothInLockstep`) didn't catch
this because its spec had nothing executable after the join to observe the free-ride with.

**The fix:** sync `mainLastSeenGen = tick.Gen` (guarded `tick != nil`, since `tick` is nil when
`StepMode` is false) immediately after `wg.Wait(ctx)` in the fork case, `go/internal/appflow/
workflow.go`. One line, no interface changes, no test infrastructure changes.

**Regression test:** `TestStepMode_NodeAfterJoin_RequiresItsOwnSeparateStepClick`
(`internal/appflow/workflow_temporal_test.go`, AF-STEP-04) — a new `forkJoinThenNodeSpec` (fork → 2
condition branches → join → a 5th real node → end) sends exactly 4 signals and asserts the
post-join node does not complete before its own, separate 4th signal. Confirmed failing before the
fix (workflow completed on only 3 signals, the post-join node ran unreleased) and passing after —
verified both ways, not assumed. `go/TEST_INDEX.md` S1-164, S1 total 1483→1484. `go test ./...` 0
failures, full suite. `go test -race -count=3 ./internal/appflow/...` — only the same pre-existing,
already-documented `TestForkJoin_EmitsTraceForAllNodes` flake appears across all 3 runs; the new
regression test and every other Phase 6 test are race-clean.

**Not yet rebuilt/redeployed** on this box's live containers — same as Phase 6's original
completion note, this fix has not been exercised through a real running Temporal worker, only the
test-environment proof above.

---

**Plan status: all 6 phases complete**, including this post-completion review fix. No further
phases planned on this thread unless new requirements surface.

---

## Post-completion follow-up: agent-node debug visibility + step-run UX (2026-09-24)

Found live during Platform-as-Tenant Phase 6's re-verification walkthrough — the first real
browser click-through this whole plan ever got (every prior phase's "not live-verified" note is
why these went unnoticed until now).

**Bug: agent-kind nodes never received any `_debug` decoration at all.** `useAppFlowDebugSession.
ts`'s `decorateNodes` only applied `_debug` to `type === 'inline'` or `type === 'flowControl'`
canvas nodes — `type === 'agent'` was missing from that list since the function was first written
(Phase 5). A real debug run's agent node completed successfully on the backend (confirmed via
`them.run_steps` — real output, correct timing) but the canvas showed **no state change
whatsoever**: no border color, no glow, no output text, nothing — `AgentNode` (`CanvasNodes.tsx`)
didn't even have a rendering path for `_debug` to begin with. Fixed both: `decorateNodes` now
includes `type === 'agent'`, and `AgentNode` got the same border/glow/label overlay
`FlowControlNode`/`InlineNode` already had.

**UX gap 1: no visual indication of which wire was actually taken.** The user asked for this
directly after watching a condition branch resolve with no sense of which path the run actually
followed. Added `decorateEdges` (`useAppFlowDebugSession.ts`) — highlights (animated, green) any
edge whose source node has run, matching the taken branch label (`sourceHandle`/`data.label`
against the source node's own `node_done` detail, e.g. `branch=true`) for condition/router edges,
or unconditionally for a plain single-target edge. Wired into `CanvasBuilderView.tsx` alongside
the existing node decoration.

**UX gap 2: no clear "the flow is done" signal.** A completion state already existed (`debug.done`
→ small "✓ Run complete" text in the toolbar row) but was easy to miss, especially for a fast run
(an `a2a_echo` agent completes in single-digit milliseconds — nothing to visually track mid-flight,
so the run appears to finish "instantly" with no clear before/after). Replaced with a full-width
banner (`AppFlowDebugPanel.tsx`) that's actually hard to miss.

**Verified:** `npx tsc --noEmit` — 0 errors. No Go files touched, no Go test run needed. Not yet
re-verified live in a browser click-through after these specific changes (the bug they fix WAS
found via a live click-through; these fixes themselves inherit the same "recommend a follow-up
browser check" caveat every phase of this plan has carried). See `docs/LESSONS.md` for the
agent-node decoration bug's own write-up.
