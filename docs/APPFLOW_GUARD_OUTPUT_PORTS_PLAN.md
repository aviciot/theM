# AppFlow Guard Output Ports Plan
# Status: Phase 0 DONE (2026-09-28, both code paths — see below). Phases
# 1-4 (the actual output-ports feature) not started; do not begin without
# explicit user confirmation first.
# Owner: platform
# Last updated: 2026-09-28

---

## Goal

When a node (`agent` or `llm`) has a Guard enabled (File Guard, PII Guard,
Prompt-Injection Guard), give that node an extra output data port carrying
the guard's outcome (clean / flagged / blocked, plus what was found) — so a
user can drag a wire from that port into a `condition` node and branch on
it (e.g. "if PII was found → route to HIL approval; else → continue
normally").

Confirmed with the user 2026-09-27 while building the PII Guard demo app:
today, a guard's real result IS computed correctly (proven live — see
`docs/APPFLOW_TEXT_GUARDS_PLAN.md`), but it is only ever surfaced as a
human-readable trace string (`"... — Text Guard: output: pii_redact:flagged"`).
There is no way for the FLOW ITSELF to see or act on that result. This plan
closes that gap.

---

## What already exists — confirmed by reading the real code, not assumed

This is mostly an **extension of the existing named data ports system**
(`docs/APPFLOW_NAMED_PORTS_PLAN.md`, Phases 0-5, already shipped), not a
rebuild. What's reusable, verbatim:

- **`PortDef`** (`go/internal/nodedefs/nodedefs.go`) — the port declaration
  shape (`ID`, `Label`, `Required`, `TypeHint`, …) already exists and is
  used for `llm`'s real `output` port today.
- **Frontend port resolution/rendering** — `nodeRegistry.ts`'s
  `resolveOutputPorts`/`resolveInputPorts`, `CanvasNodes.tsx`'s handle
  rendering, `InlinePortsSection.tsx`'s "Reads" panel, `PortBindingPopover.tsx`,
  `useInlinePortWiring.ts`'s alias/binding model — all of this is generic
  port-wiring UI that doesn't care what a port represents. No new frontend
  plumbing needed to LET a user drag a wire from a new port; only new *data*
  to feed it.
- **A per-instance ("dynamic") port mechanism already exists** —
  `nodedefs.go`'s doc comment on `PortDef` and a `dynamic_output_source`
  concept (already implemented and used by `agentgen`'s `transform` node
  kind: a port that only exists depending on that specific node instance's
  own config, not a blanket port on every node of that kind). This is
  exactly the shape needed here (a guard port should only exist when THAT
  node's wiring has a guard enabled) — it just isn't wired into `appflow`
  yet.
- **The guard result data itself already exists** — `TextGateCheckOutput`
  (`Blocked bool`, `Categories string`) and `FileGateCheckOutput`
  (`ScanStatus string`) are already computed correctly by
  `TextGateActivity`/`FileGateActivity`, per node, per run. Nothing new to
  compute — only new to SURFACE.

## What's genuinely new — the real work, not registry metadata

1. **A real scope change from the named-ports plan.** That plan explicitly
   put `agent` nodes out of scope for data ports ("agent nodes never touch
   FlowVars, adding a data port to them is out of scope here... a distinct,
   separate future feature" — confirmed verbatim in
   `docs/APPFLOW_NAMED_PORTS_PLAN.md`). This feature's main target IS agent
   nodes with File/PII Guards attached, since that's where guards are used
   most today. Needs re-confirming as a deliberate, informed scope
   expansion, not a silent scope-creep.
2. **`workflow.go` must actually WRITE the guard outcome into `vars`.**
   Today, `case "agent"`'s Text/File Guard checks discard their result into
   a trace-only `guardNotes []string` (see `docs/APPFLOW_TEXT_GUARDS_PLAN.md`
   Phase 3/4's `workflow.go` changes) — never into `FlowVars`, the same map
   `condition`'s expression already reads. `case "llm"` does the same. This
   is a small, well-precedented change (mirrors the existing
   `vars[outVar] = llmOut.ResponseText` pattern exactly) but it IS new
   runtime behavior, not just registry metadata.
3. **A normalized outcome value**, not a free-text trace string. Today's
   `Categories` (e.g. `"pii_redact:flagged"`) was designed for a human
   reading a log, not for a `condition` node's Go-template expression to
   branch on cleanly. Needs a real, documented value shape — e.g.
   `vars["<nodeID>_guard_status"] = "clean" | "flagged" | "blocked"` (one
   var) plus maybe `vars["<nodeID>_guard_categories"] = "email,phone"` (a
   second var, only if the user wants to branch on WHICH category, not just
   whether one fired) — exact shape needs deciding, see Open Questions.
4. **Conditional port existence.** A guard port must only show up on a node
   whose wiring actually has a guard enabled — not a blanket port on every
   `agent`/`llm` node. This needs the dynamic-port mechanism extended to
   `appflow`'s registry (today only `agentgen`'s `transform` kind uses it),
   gated on "does this specific node_id have an enabled `middleware_wirings`
   row" — a live DB lookup, not a static registry fact, which is a new kind
   of question the port-resolution code hasn't had to answer before.

---

## Open questions — resolved with the user 2026-09-27

1. **Exact var shape — one var per guard kind, not combined.** A node with
   both File Guard and PII Guard enabled gets two independent status vars
   — the user wants to route PII differently from a bad file (e.g. HIL for
   PII, straight rejection for a virus), so collapsing them into one
   "anything flagged" value would lose exactly the distinction this
   feature exists to expose.
2. **File Guard's async timing — being closed for real, not skipped.** See
   Phase 0 above — confirmed with the user rather than deferring the gap.
3. **Multi-guard nodes — one port per enabled guard**, same reasoning as
   (1): a node with 2 guards enabled gets 2 ports, not 1 combined one.
4. **Port appears automatically** when a guard is toggled on in the Guards
   section — no separate explicit "expose as port" step to forget.

One question remains genuinely open, deferred to Phase 2's own design
work rather than blocking the start of Phase 0:

- The exact FlowVar naming scheme (e.g. `<nodeID>_file_guard_status` vs
  `<nodeID>_pii_redact_status` vs some other convention) — needs to stay
  collision-free across multiple guards on one node and multiple nodes in
  one flow; pick this concretely when Phase 1 is actually implemented, not
  guessed here.

---

## Rough phasing (not started, not estimated in detail — sizing depends on
## the open questions above)

### Phase 0 — make File Guard's scan synchronous from AppFlow's point of view

**DONE 2026-09-28.** Implemented for BOTH File Guard code paths — the plan
below only described the URL-based path when written; a second, separate
raw-bytes inline path (a2a-stream's zip, docu_writer's PDF — scanned inside
`pgxAgentA2ACaller.InvokeByID`, never round-tripped through Temporal
activity history) was found live to still show stale `"pending"` and
needed its own independent wait, using the same `RedisScanSubscriber`
underneath. See `go/TEST_INDEX.md`'s S1-177 for the full file/test list.
Net effect either way: an `"infected"` verdict now fails the run with the
threat name instead of the file silently passing through as "pending";
`"clean"`/`"error"` surface the real terminal status; a wait that doesn't
resolve in time fails open (`"timeout"`), matching the classic
Orchestrator's own precedent — this was flagged as a real question in the
original phase text below and resolved by keeping AppFlow consistent with
the orchestrator rather than diverging into fail-closed.

**Live-verified 2026-09-28** against `verify-step6-ws` (raw-bytes path,
via the user's own live run, not a synthetic test): run `ced5c9d9-...`
shows `File Guard: clean` in `run_steps.output`, and
`middleware_jobs.updated_at` (06:45:42.374) lands before the run's own
`ended_at` (06:45:42.427) — proof the wait genuinely blocked until the
real scan finished, rather than racing ahead of it. Two earlier "still
shows pending" live runs on the same day were a deploy problem, not a
logic bug: `docker compose restart` does not pick up a freshly built
image (only `up -d`/`--force-recreate` does) — see `docs/LESSONS.md`'s
2026-09-28 entry.

Original phase plan (kept for context):

**Researched 2026-09-27, before writing this phase**: a real, working
completion-wait mechanism ALREADY EXISTS in this codebase —
`internal/orchestrator/scan_subscriber.go`'s `RedisScanSubscriber.
WaitForScanResult(ctx, runID, artifactID, timeout)` — but it's only wired
into the classic Orchestrator's event-bus path (`cmd/worker/main.go`,
`orchestrator/tools.go`'s `waitAndEmitScanResult`), never into AppFlow's
Temporal workflow/activity code. This phase connects the two, rather than
inventing a new wait mechanism — a DB poll-loop was considered and
rejected: it doesn't exist anywhere in this codebase today, and would
duplicate the Redis pub/sub mechanism that already does this exact job
(`them-middleware-worker`'s `main.go` already publishes to
`them:run:<runID>` on job completion — `PublishFinalResult`).

- New Temporal activity (e.g. `AppFlowFileGateWaitActivity`) wrapping
  `RedisScanSubscriber.WaitForScanResult` — called synchronously by
  `workflow.go`'s `case "agent"`, right after the existing
  `FileGateActivity` enqueues the scan, using the `ArtifactID`
  `FileGateActivity` already returns.
- **Bounded timeout, explicitly shorter than the classic orchestrator's
  5-minute precedent** — real scans are sub-second to a few seconds for
  realistic file sizes (confirmed: `total_ms` values seen live this
  session were 19-36ms, but for small test fixtures; no large-file
  benchmark exists in this repo yet — pick a real timeout, e.g. 30-60s,
  conservatively above worst-case, not copy the orchestrator's 5-minute
  value verbatim, since a user-facing synchronous wait needs a much
  tighter bound than a background async path did).
- **Explicit `RetryPolicy{MaximumAttempts: 1}` on this activity** — found
  during research: Temporal's default activity retry policy (already used
  elsewhere in this codebase, `workflow.go`'s `ao`/`shortAO` options) would
  silently re-run a timed-out wait up to 3 times by default, each
  restarting its own internal timeout window and multiplying the
  user-visible wait by the attempt count. A scan timeout must fail once,
  fast, and let the WORKFLOW decide fail-open vs. fail-closed — not let
  Temporal's retry machinery silently stack multiple full timeout windows.
- **Fail-open on timeout, matching the classic orchestrator's own existing
  precedent** (`tools.go`'s `waitAndEmitScanResult`: on `!ok`, treats the
  file as clean rather than blocking indefinitely) — a scan that hasn't
  finished in time should not hang the run forever; needs a real decision
  with the user on whether AppFlow's fail-open matches or should differ
  (e.g. maybe AppFlow should fail-closed by default, given guards are
  security-relevant — flag this explicitly when Phase 0 starts, don't
  silently copy the orchestrator's choice).
- Tests: a fake `RedisScanSubscriber`-shaped interface (matching this
  session's existing `FileGateChecker`/`TextGateChecker` small-interface
  pattern) proving (a) a fast scan result unblocks the wait immediately,
  (b) a timeout fails the activity once, not 3 times, (c) fail-open
  behavior on timeout is correct and deliberate, not accidental.

### Phase 1 — backend: write guard outcomes into FlowVars
- `workflow.go`'s `case "agent"`/`case "llm"`: after each Text/File Guard
  check, write a normalized status var (exact shape per Open Question 1)
  into `vars`, not just the existing trace-only `guardNotes` string.
- Tests: workflow-level (real Temporal test environment, matching this
  session's `AppFlowTraceWorkflowTestSuite` pattern) proving the var is
  set correctly for clean/flagged/blocked cases, and that a downstream
  `condition` node can actually read and branch on it.

### Phase 2 — conditional port resolution
- Extend the dynamic-port mechanism to `appflow`'s node registry, gated on
  a live `middleware_wirings` lookup for that specific `node_id` (does
  this node have an enabled guard wiring at all, and which kind).
- This is the part most likely to need real design iteration — the
  existing dynamic-port mechanism was built for `agentgen`'s `transform`
  node, which resolves its ports from the node's OWN config (fully local,
  no DB call). This is resolving a port's EXISTENCE from a DB lookup
  (guard wiring state) — a new pattern, not a copy-paste of the existing
  one.

### Phase 3 — frontend: render + wire the new port
- Reuse `resolveOutputPorts`/`PortBindingPopover`/`InlinePortsSection`
  wholesale — confirm live that a guard-status port genuinely needs zero
  new frontend port-wiring code, only a new port SOURCE to feed those
  existing components.
- Guards section UI: decide (Open Question 4) whether the port appears
  automatically when a guard is toggled on, or needs a separate toggle.

### Phase 4 — condition node UX for the new var
- Confirm the `condition` node's existing expression editor and example
  presets (`CONDITION_EXAMPLES` in `InlineNodePanel.tsx`) work naturally
  with the new var without any special-casing — e.g.
  `{{eq .agent_intake_guard_status "flagged"}}`.

---

## Explicitly out of scope for this plan

- Any change to the guard detection logic itself (regex quality, a real
  PII library swap) — separate concern, unrelated to ports.
- Guarding the LLM node's raw input before any guard even runs (a
  DIFFERENT feature — this plan is about exposing an EXISTING guard's
  result as a port, not adding new guard capability).
- Making the classic Orchestrator's existing event-bus File Guard path
  (already synchronous-feeling via its own `waitAndEmitScanResult`) use
  the new Phase 0 activity — that path already works, untouched by this
  plan; Phase 0 only adds the equivalent for AppFlow's Temporal path.
