# App Canvas — Debug Mode (real execution, not simulated)
# Status: PLANNED, phased. Phase 1 COMPLETE. Phase 2 NEXT.
# Date: 2026-09-22

---

## Progress

| Phase | What | Status |
|---|---|---|
| 1 — Debug worker pool + routing | New Temporal queue, worker container(s), per-run routing | ✅ COMPLETE (2026-09-22) |
| 2 — Per-node trace instrumentation | AppFlow activities emit node_start/node_done/node_error | ⬜ NEXT |
| 3 — Durable trace storage | Extend `them.run_steps`; make existing Flow tree populate for Graph-mode runs | ⬜ NOT STARTED |
| 4 — Runtime log-verbosity setting | Per-app off/status/full config, gates persistence in Phase 3 | ⬜ NOT STARTED |
| 5 — Debug UI: setup + Run All | Dynamic param-spec scan (mirrors agent builder), Run All button, WS/SSE consumer | ⬜ NOT STARTED |
| 6 — Debug UI: Step controls | Step button, lockstep multi-branch pause/resume, canvas node highlighting | ⬜ NOT STARTED |

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

### 2. Per-node trace events — the new instrumentation every activity needs

Each AppFlow activity (`InlineLLMActivity`, router/HIL/fork/join execution, `InvokeAgentActivity`,
etc. — everything dispatched from `internal/appflow/workflow.go`'s main loop) needs to publish a
start and a done/error event, mirroring the existing `token`/`done` `XAdd` pattern already used
for LLM streaming:
- `node_start {run_id, node_id, kind, started_at}`
- `node_done {run_id, node_id, kind, output_snapshot, latency_ms}` /
  `node_error {run_id, node_id, kind, error, latency_ms}`

**Open design question (not yet decided):** should this instrumentation be unconditional (every
run, debug or not, emits these events — cheap, and closes the `run_steps` gap for ALL runs, not
just debug ones) or gated behind the debug flag (less always-on overhead, but means a production
run still has zero forensic trace if something goes wrong and debug wasn't on)? Leaning toward
**unconditional emission, gated recording** — always emit the events (cheap pub/sub), but only
persist them to a durable table when the run is a debug run, keeping decision #1 (debug is opt-in
for overhead) intact while still allowing a future decision to persist for all runs without
re-touching the activities. Needs explicit confirmation before implementation, not decided here.

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

## Remaining open question

1. **Unconditional vs debug-gated event emission** (architecture section 2) — should the
   `node_start`/`node_done`/`node_error` events always be published (cheap pub/sub) regardless of
   the app's configured log level, with the log-level setting only controlling what gets
   *persisted*? Or should emission itself be skipped entirely when log level is `off`? Leaning
   toward "always emit, level controls persistence" (simpler code path, one less conditional
   deep inside the workflow/activity hot path) but not yet explicitly confirmed.
