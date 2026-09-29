# AppFlow Guard Output Ports Plan
# Status: Phase 0 DONE (2026-09-28). Phase 1 DONE (2026-09-29, flat status
# var per guard, real Temporal tests). Phase 1.5 DONE (2026-09-29,
# per-category detail plumbed through, flat key convention). Phase 2 IN
# PROGRESS (2026-09-29): (a)/(b) FlowVars-shape decided (b, flat storage +
# display translation), "Writes" panel DONE. Source-side port-picker
# popover (multi-output drag UI) NOT yet started. Phase 3 not started.
# Owner: platform
# Last updated: 2026-09-29

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

**DONE 2026-09-29.** `workflow.go`'s `case "agent"`/`case "llm"` now write a
normalized status var after every Text/File Guard check via two new helpers:
`writeTextGuardVars(vars, nodeID, categories)` (parses `TextGateCheckOutput.
Categories`'s space-separated `"defSlug:status"` pairs — confirmed exact
format from `middleware.TextGate.Check`'s `joinCategoryParts` — into one var
per guard that actually ran) and `writeFileGuardVar(vars, nodeID,
scanStatus)` (File Guard's own vocabulary, deliberately not normalized into
text guards' clean/flagged/blocked — see Open Question 1). Var name:
`guardStatusVarName(nodeID, defSlug) = nodeID + "_" + defSlug + "_status"`
— e.g. `agent1_pii_redact_status`, `llm_1_file_guard_status` (File Guard's
real `def_slug` is `file-guard` with a hyphen, not valid inside a Go
`text/template` reference — the var uses `file_guard` with an underscore
instead; the frontend must apply this same substitution, see Phase 2).
5 new real-Temporal-environment tests in `workflow_temporal_test.go`
(AF-TR-W16 through W19 plus the two-guards case) prove: a flagged/clean
status is readable by a downstream `condition` node and correctly decides
its branch; two guards on the same node write two independent vars neither
overwrites; File Guard's status uses its own vocabulary. `go build`/`go
vet`/`go test ./...` all clean.

**Superseded/expanded scope, decided live 2026-09-29** (see the redesign
below) — one flat status var per guard turned out to be too little: a real
PII Guard result needs to expose WHICH category matched (email vs ssn),
not just "something matched." Phase 1 as shipped is the right foundation
(the var-writing mechanism, the naming convention, the workflow call
sites) but Phase 1.5 below extends the DATA it carries before Phase 2's UI
work makes it visible to users.

### Phase 1.5 — carry per-category detail through, not just one flat status

**DONE (2026-09-29).**

**Real gap found reading the actual detector code**: `pii.Detector.Process`
(`internal/middleware/pii/pii.go`) already computes exactly which
categories matched, with counts — `found := map[string]int{}` — and stores
it as `Result.Detail: map[string]any{"categories": found}`. This detail is
computed correctly today but is **thrown away** before it reaches the
workflow: `Pipeline.Run` only forwards `Detail` to `PublishProgress` (File
Guard's async event stream) and a `"threat"` string extraction — never into
`TextGateCheckOutput`, which only ever carries a flat `Categories string`.

**Shipped:** `middleware.TextGateResult` gained `ResultsByGuard
map[string]Result` (keyed by defSlug), populated inside `TextGate.Check()`.
`appflow.TextGateCheckOutput` gained `GuardDetails []GuardCategoryDetail`
(`{DefSlug, Outcome string; Categories map[string]int}`). The real boundary
bug was `cmd/dag-worker/main.go`'s `appFlowTextGateAdapter.Check`, which
only ever read `Text`/`Blocked`/`Categories` off the `TextGate` result —
fixed via new `guardCategoryDetailsFrom(...)` helper (sorted by defSlug for
Temporal-replay determinism). `workflow.go` gained
`writeGuardCategoryVars(vars, nodeID, details)`, writing one flat var per
matched category: `nodeID_defSlug_category_status = "flagged"` (e.g.
`agent1_pii_redact_phone_status`) — same flat-key convention as Phase 1,
not yet the nested `{{pii_guard.email.status}}` display form (that's
Phase 2's (a)/(b) decision below, still not started). Tests: 1 new
`internal/middleware/textgate_test.go` case proving `ResultsByGuard` carries
the real category detail one layer below the boundary bug; 2 new real
Temporal-environment tests in `workflow_temporal_test.go`
(`TestAgentNode_GuardCategoryVar_ConditionBranchesOnSpecificCategory`,
`TestAgentNode_GuardCategoryVar_DoesNotMatchUnrelatedCategory`) proving a
condition node can branch on one specific category without matching an
unrelated one. `go build`/`go test ./...` (full suite) clean. See
`go/TEST_INDEX.md` S1-183.

### Phase 2 — nested guard output vars + Writes panel

**Redesigned 2026-09-29, replacing the original "one flat status var,
reuse the transform node's dynamic-port mechanism" sketch** — live design
discussion surfaced two real requirements the original sketch didn't
cover: (1) a guard's result isn't always just one status word — PII Guard
needs to expose per-category detail (`pii_guard.email.status`,
`pii_guard.ssn.status`), and File Guard may expose more than a bare status
too (e.g. threat name); (2) users need a dedicated place to SEE what a
node writes, mirroring the existing Reads panel, not just type a var name
from memory.

**Var naming — nested, not flat.** Supersedes Phase 1's flat
`<nodeID>_<defSlug>_status` for the *user-facing* naming users type/drag —
`{{pii_guard.email.status}}`, `{{pii_guard.ssn.status}}`,
`{{file_guard.status}}`, `{{file_guard.threat}}`. Go's `text/template`
syntax makes `{{.pii_guard.email.status}}` mean chained field access into a
Go value, not a flat map key with dots in it (confirmed this exact
footgun already once this session, in the named-ports plan's alias-naming
work) — so `FlowVars` (currently `map[string]string`) needs either (a) to
become `map[string]any` with nested `map[string]any` values so the dotted
template access actually resolves as real field access, or (b) a
lower-level flat storage key (`nodeID_defSlug_category_status`, matching
Phase 1's existing convention) with the DISPLAY/AUTOCOMPLETE layer showing
the nicer dotted form and translating it before rendering the template.
**Decided 2026-09-29: (b).** `FlowVars` stays `map[string]string`, flat
keys unchanged (`nodeID_defSlug_category_status`, matching Phase 1/1.5
exactly — zero backend risk). A display-layer translation shows the
dotted form (`{{pii_guard.email.status}}`) in the UI (Writes panel,
drag-to-wire) while the flat key is what's actually stored in
`PortBinding.source_var` and rendered by `text/template`. Trade-off
accepted: the on-screen "nice name" and the literal template string a
user could hand-type are not identical — acceptable since dragging (not
typing) is the primary UX, matching the existing Reads-panel/target-popover
convention where users drag rather than type.

**New "Writes" panel section — DONE (2026-09-29).** Mirrors
`InlinePortsSection.tsx`'s existing "Reads" section visually — shows every
var THIS node produces: its own real output (`output_var` for `llm`;
nothing static for `agent`, which never writes to FlowVars directly) plus
one entry per enabled guard, each showing its dotted display reference
(`{{pii_guard.email.status}}`). A "Guards" subsection inside Writes groups
guard-produced vars separately from the node's own primary output, per the
live design discussion. Populated by reading the SAME per-app
`listMiddlewareWirings(appId)` call the Guards section itself already
makes — confirmed no new backend endpoint needed, this part of the
original sketch was right.

Shipped as: new `guardWriteVarsForNode(nodeId, wirings)` in new
`frontend/.../cbv/guardWriteVars.ts` — pure function computing every
`GuardWriteVar{flatVar, displayRef, guardLabel}` an enabled wiring on a
node writes, kept in exact sync with `workflow.go`'s naming (flat
`nodeID_defSlug_status`, plus `nodeID_defSlug_category_status` for
`pii_redact` only — `prompt_inject` has no per-category detail today, per
Phase 1.5). One real footgun handled: File Guard's wiring `def_slug` is
`"file-guard"` (hyphen) but its FlowVar segment is the literal
`"file_guard"` (underscore, from `writeFileGuardVar`'s hardcoded string) —
`guardWriteVarsForNode` special-cases this rather than assuming
`def_slug` is always the var segment. New `WritesSection.tsx` (new file,
`frontend/.../cbv/panels/`) renders it, wired into `InlineNodePanel.tsx`
(llm, alongside its existing `output_var`) and `AgentNodePanel.tsx`
(agent, guard vars only). Read-only display only — no drag-to-wire yet,
that's the source-side popover below, still not started. New
`frontend/.../cbv/__tests__/guardWriteVars.test.js` (8 tests, plain
`node`-run, no framework — matches this repo's existing frontend test
convention): pins the exact flat-var strings for the no-categories-configured
(expands to all 4), restricted-categories, file-guard segment mismatch,
prompt_inject (no expansion), disabled-wiring (nothing), wrong-node
(nothing), and two-guards-same-node cases. `tsc --noEmit` clean; all 91
frontend tests (83 pre-existing + 8 new) pass.

**Source-side port-picker popover — new UI, decided live 2026-09-29.**
Today, `useInlinePortWiring.ts`'s drag-to-wire system has NO source-side
choice at all — `isBindableSource` only returns true for `llm` (the only
kind with a bindable output today), and `commitInlinePortBinding` always
reads exactly one var (`sourceCfg.output_var`). Once a node can have
multiple outputs (its own `output_var` AND N guard vars), dragging from
its output dot needs a NEW popover asking "which output are you sending?"
— a second popover, distinct from and in addition to the EXISTING
target-side popover (`resolveDropTarget`/`PortBindingPopover`, unchanged,
still asks "which field on the target"). Confirmed live: the same source
output must remain freely reusable across multiple target wires — no
"already connected, can't reuse" restriction; a status var is a read, not
a claim.

- Extend `isBindableSource` to also return true for any node with ≥1
  enabled guard wiring, not just `node_type === 'llm'`.
- New function (name TBD at implementation time, e.g. `resolveDragSource`)
  returning the list of this node's available outputs: its own
  `output_var` (if `llm`) plus one entry per guard var — mirroring
  `resolveDropTarget`'s existing `{kind: 'none' | 'auto' | 'ambiguous'}`
  shape for consistency.
- `commitInlinePortBinding` (or a new sibling) needs the CHOSEN source var
  threaded in, not hardcoded to `output_var` — the existing
  `PortBinding{source_node_id, source_var}` shape already supports an
  arbitrary `source_var` string, so this is additive, not a breaking
  change to the binding storage format.

### Phase 3 — condition node UX for nested guard vars
- Confirm the `condition` node's existing expression editor and example
  presets (`CONDITION_EXAMPLES` in `InlineNodePanel.tsx`) work with the
  final chosen var shape from Phase 2 (flat storage + dotted display, or
  real nested `FlowVars`) — add a guard-specific example once the shape
  is locked, e.g. `{{eq .pii_guard_email_status "flagged"}}` (flat) or
  `{{eq .pii_guard.email.status "flagged"}}` (nested, pending Phase 2's
  (a)/(b) decision).
- Confirm dragging a guard var into a `condition` node's Expression field
  (the only nameable field `condition` has today, so this already
  auto-binds via the EXISTING target-side popover, no new target-side
  code needed) produces a working, readable expression.

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
