# AppFlow Named Data Ports — Plan
# Status: PLANNED, phased. Phase 0 + open questions + Phases 1-4 COMPLETE (Phase 4's visible
# handles later reverted per live user feedback — see Phase 4 entry below). Phase 5 spec
# confirmed with user 2026-09-25, NOT YET IMPLEMENTED — start here next session.
# Owner: platform
# Last updated: 2026-09-25

---

## Goal

Bring App Canvas / AppFlow's data-flow visibility up to the same standard the agent builder
already has: when a wire is dragged from one node's output to another's input, the target node's
input section should clearly show `source.<port_name>` — a real, named, renamable port — not a
silently-implicit shared variable name the user has to guess by reading an upstream node's config.

**Long-term context (not part of this plan's scope, but the reason it matters):** the user's
stated direction is that App Canvas may eventually become capable enough to replace the agent
builder entirely. Prefer reusing/porting the agent builder's mature patterns over inventing
AppFlow-specific ones, so the two systems don't diverge further while this direction is still
open. See the user memory entries `project_appflow_vs_agentbuilder` / `feedback_appflow_port_naming`
(in the auto-memory system) for the exact framing.

**UI bar to hit, explicitly stated by the user, not just "technically correct":** whatever gets
built must be genuinely convenient and clear for the end user — this was called out as its own
requirement, separate from functional correctness.

---

## Origin

Found live, same session as Platform-as-Tenant Phase 6 / App Canvas Debug Mode follow-ups
(2026-09-24): while live-testing a Condition node's debug panel, the user found it shows a
free-text expression textarea with 3 generic hardcoded examples, but zero visibility into what
variables are actually available or where they came from (e.g. `.sentiment`, written by an
upstream LLM node's `output_var` field — nothing in the Condition panel surfaces this connection
at all). The user explicitly rejected a shortcut fix ("just show a reads list") in favor of the
real mechanism, after being told the agent builder already has exactly this: "think about both -
i want both, it can be as complex, do it right, maybe worth adding it to NodeRef and do it
properly."

A Plan agent was dispatched to research both systems and produce a real phased implementation
plan (not a summary) — full detail in this doc's "Phase 0" section below.

---

## Current-state map (researched 2026-09-24)

**Agent builder** (`frontend/src/app/admin/agents/builder/`) already has a complete, working
version of this:
- `nodeVars.ts` — `extractNodeVars()` (static per-step-type reads/writes), `upstreamVarSources()`
  (walks edges backward to map var name → {label, step_type})
- `hooks/useSkillPipeline.ts` — `onPipeConnectStart`/`onPipeConnect` — on drag-connect, default
  port name = source's output var name; collision resolved with numeric suffix (`varName_2`,
  `varName_3`, ...); commits `{[portID]: {from_step, from_port}}` into `node.data.inputs`
- `components/StepDataFlowSection.tsx` — renders a "READS" section per node, each var shown as a
  `{{.varname}}` chip with "from <emoji> <SourceNodeLabel>" when resolved, red warning if not.
  `PortAliasField` (lines 9-51) lets the user rename a committed port inline.
- `components/PortsPopover.tsx` — visualizes committed named ports as colored dots + labels on
  the node itself, via `resolveInputPorts()` in `frontend/src/lib/nodeRegistry.ts:162-206`

**App Canvas / AppFlow** (`frontend/src/app/admin/applications/components/`) has none of this for
its `llm`/`condition` node kinds (or any other kind):
- `cbv/panels/InlineNodePanel.tsx` — LLM panel: free-text prompts + `output_var` text input, no
  upstream visibility. Condition panel (~lines 148-209): free-text `expression` + 3 hardcoded
  examples (`CONDITION_EXAMPLES`), no enumeration of available vars, no source labels. Has one
  asymmetric helper, `branchTarget()` (~lines 151-158), which walks edges FORWARD (downstream) —
  nothing walks backward.
- `CanvasHelpers.ts` (`docToCanvas`/`canvasToDoc`) — the only serialization boundary; no
  var-tracking logic exists here today.
- `CanvasNodes.tsx` — only `control_output_ports` (condition's true/false branch pips) are
  rendered via `resolveOutputPorts()`; no data-in/data-out handles exist on any AppFlow node kind
  today, only control-flow `target`/`source` handles.
- Backend runtime (**already correct, not the gap**): `go/internal/appflow/inline.go` defines
  `FlowVars map[string]string` — a flat, shared-per-run variable bag, functionally equivalent to
  agentgen's PipelineVars. `InlineLLMConfig.OutputVar` is the field a user types into the LLM
  panel's "Output Variable" box; at runtime `workflow.go` (Temporal path) / `graph.go` (non-Temporal
  path) do `vars[outVar] = llmOut.ResponseText`. Condition's `expression` (Go template) reads
  `vars` directly. **The runtime mechanism already works exactly like agentgen's — only the
  frontend visibility/UX is missing.**
- No static "unresolved input var" compile-time check exists for AppFlow (`validate.go` has no
  equivalent of agentgen's `StepContract`/`UNRESOLVED_INPUT`).
- Confirmed via repo-wide search: `resolveInputPorts`/`PortsPopover` are never imported anywhere
  under `applications/components/` — these registry fields are effectively vestigial for the
  AppFlow family today (only used by agentgen).

---

## Phase 0 — Research + Planning — COMPLETE (2026-09-24)

Two research/plan passes run this session (a research fork + a dedicated Plan agent) converged
on the same conclusions independently. Full findings:

### Scope decision — confirmed conclusion, needs the user's explicit re-confirmation before Phase 1

**Only `llm` and `condition` get real named ports.** Reasoning, kind by kind, based on what each
kind's `workflow.go`/`graph.go` case actually does at runtime (not assumed — read directly):

- **`llm`** — yes, both an output port (aliases the existing `output_var` field) and input ports.
  Already the only kind that *writes* to `FlowVars`.
- **`condition`** — yes, input ports only (no output data port — its two outputs are the
  true/false control branches, not a data value). Already reads `FlowVars` via its Go-template
  `expression`.
- **`router`, `hil`, `fork`, `join`, `agent`, `orchestrator`, `middleware`, `entryPoint`** — no.
  None of these touch `FlowVars` at runtime today — they only ever read/write `accumulated` (a
  single relayed string) or are pure topology (fork fans out, join merges with a newline
  separator). Giving any of them a "named port" would render a connection point with nothing real
  behind it — exactly the kind of shortcut/fake-capability the user explicitly said they don't
  want ("do it right").

This directly means: **dragging a wire to create a named port only makes sense between two `llm`
nodes, or from `llm` into `condition`** — not from every node kind, which narrows the user's
original "every node that accepts input" framing. **This narrowing must be explicitly
re-confirmed with the user before Phase 4/5 implementation starts** — it is the single
highest-leverage open question from this research.

**Explicitly and deliberately NOT touched by this plan:** the pre-existing, separately-found
`AgentNode`-has-no-outgoing-`Handle`-at-all bug (found and worked around, not fixed, earlier the
same session — see `docs/LESSONS.md`'s "AgentNode has no outgoing connection point" entry). Since
`agent` nodes never touch `FlowVars`, adding a data port to them is out of scope here. If the user
later wants an agent's reply assignable to a named variable (so a downstream node can reference it
explicitly instead of via the implicit `accumulated` relay), that is *new backend runtime
behavior* (`workflow.go`'s `case "agent"` would need to write to `vars`) — a distinct, separate
future feature, not something to bundle into "adding ports."

### Data model conclusion — this is a UI/registry layer, NOT new backend data-plumbing

`FlowVars` already works exactly like agentgen's shared var bag — the "port" concept for
AppFlow's `llm`/`condition` cannot mean a new per-edge binding mechanism the way agentgen's
`{from_step, from_port}` does, because AppFlow has no compile-time contract enforcing anything
(unlike agentgen's `StepContract`), and any downstream node can already see any var by name
regardless of edges. So:

- **LLM output port** = literal alias for the existing `output_var` field. No new data.
- **LLM/Condition input ports** = a new, additive, purely-cosmetic mapping recording which
  upstream var each `{{.varname}}` reference is "meant" to come from, plus a user-facing alias.
  Proposed to live inside the existing `components[].config` object (round-trips through
  `docToCanvas`/`canvasToDoc` with zero new top-level wire-format keys) as a new field, tentatively
  named `input_aliases: {alias: underlying_flowvars_key}`.
- Renaming an alias rewrites the actual `{{.oldAlias}}` → `{{.newAlias}}` occurrences in the
  node's prompt/expression text in place, so the alias and the template text never drift apart —
  this is the one real structural difference from agentgen's ports (which persist independently
  of the text typed into any field).
- **No backend Go struct changes needed.** `InlineLLMConfig`/`InlineConditionConfig` don't need a
  new named field — the runtime already ignores unknown JSON config keys. Only
  `go/internal/appflow/noderegistry.go` (static metadata: which kinds have ports, port
  labels/colors) changes on the backend for Phases 1-5. `validate.go` only changes if the
  optional Phase 6 is approved.

### Registry changes (`go/internal/appflow/noderegistry.go`)

Already has the right shape via `nodedefs.Meta`'s existing `InputPorts`/`OutputPorts`/
`ControlOutputPorts` fields (`go/internal/nodedefs/nodedefs.go`) and `AcceptsDynamicInputs`/
`DynamicOutputs` — no new Go type needed, only new data in the existing registry entries:
- `llm`: add `OutputPorts: []nodedefs.PortDef{{ID: "output", Label: "Output", TypeHint: "text"}}`;
  confirm `AcceptsDynamicInputs: true` (frontend derives the live output port label from
  `cfg.output_var`, not a static array field).
- `condition`: no `OutputPorts` (control branches only, via existing `ControlOutputPorts`).
- Every other kind: no change (confirms their `AcceptsDynamicInputs`/`OutputPorts` correctly stay
  absent/false).

### Frontend changes — reuse vs. divergence

**Extract to shared `frontend/src/lib/` (used by both agent builder and AppFlow):**
- `extractTemplateVars()` (pure regex over `{{.x}}`) — move from `nodeVars.ts` to
  `frontend/src/lib/templateVars.ts`; agentgen re-exports for zero behavior change there.
- The collision-safe naming algorithm (`varName`, `varName_2`, `varName_3`...) from
  `onPipeConnectStart` — extract to a pure helper, both systems call it.
- `reachablePredecessors`/`reachableSuccessors` graph-walk helpers — generic over `Node[]`/
  `Edge[]`, move to `frontend/src/lib/graphWalk.ts`.
- `PortsPopover.tsx` rendering — reuse directly, unmodified (already generic over `NodeDef`/
  `ResolvedPort`; AppFlow just needs to pass `family: 'appflow'` into `getNodeDef`, already
  supported).

**AppFlow-specific (new files, not shared — genuinely different data model from agentgen's
binding-record approach):**
- `frontend/src/app/admin/applications/components/cbv/appFlowVars.ts` — `extractInlineNodeVars`
  (llm/condition only) + `upstreamAppFlowVarSources` (composes the shared graph walk with this
  extractor).
- `frontend/src/app/admin/applications/components/cbv/useInlinePortWiring.ts` — drag-connect
  logic; writes `input_aliases` + rewrites template text (not a `{from_step, from_port}` binding
  record like agentgen — see Data model section above for why).
- `frontend/src/app/admin/applications/components/cbv/panels/InlinePortsSection.tsx` — the
  "READS" panel section, mounted inside `InlineNodePanel.tsx` for both `llm` and `condition`.

**Modified:** `frontend/src/lib/nodeRegistry.ts` (new collision-naming helper), `CanvasNodes.tsx`
(`InlineNode` — add data-in/data-out handles, gated on `node_type === 'llm' || 'condition'`;
needs careful handle-layout math alongside the existing control-port spreading — read agent
builder's `StepNode.tsx` rail-offset logic before implementing, not yet fully explored),
`InlineNodePanel.tsx` (mount the new section + wire rename/delete callbacks). `CanvasHelpers.ts`
likely needs **zero** changes — `input_aliases` lives inside the already-opaque `config` object,
confirm this with a round-trip test before assuming.

### Phasing (each independently shippable, low-risk, testable — per this project's mandatory
plan/implement/test/commit cycle)

1. **Backend registry metadata only** — populate `OutputPorts`/confirm `AcceptsDynamicInputs` for
   `llm` in `noderegistry.go`. Zero runtime behavior change. New `AF-NR-xx` test(s). **DONE
   2026-09-24** — `llm` got `OutputPorts: [{ID: "output", ...}]`; new
   `TestAllAppCanvasNodeInfos_LLMHasOutputPort` in `noderegistry_test.go`; full `go test ./...`
   (1326 tests) and `go build ./...` both clean.
2. **Shared frontend extraction** — move `extractTemplateVars`/`reachablePredecessors`/
   `reachableSuccessors` to `frontend/src/lib/`; agentgen re-exports, zero behavior change there
   (verify against agent builder's existing tests/smoke). **DONE 2026-09-24** — new
   `frontend/src/lib/templateVars.ts` and `frontend/src/lib/graphWalk.ts`; `nodeVars.ts` now
   re-exports both (`export { extractTemplateVars }` / `export { reachablePredecessors,
   reachableSuccessors }`) so every existing call site (`StepNode.tsx`,
   `StepDataFlowSection.tsx`) needed zero edits. New test files
   `frontend/src/lib/__tests__/templateVars.test.js` (7 tests) and `graphWalk.test.js` (8 tests),
   following the existing plain-`node`/`assert` convention (no test runner configured in this
   frontend yet). Verified: pre-existing `nodeVars.test.js` still 26/26 passing (its inlined copy
   is unaffected by the re-export, confirming behavior didn't drift), `npx tsc --noEmit` clean
   with zero errors project-wide, and a full `npx next build` production build succeeded.
3. **"What can I read, and where from" panel** (the smaller, high-value half) — build
   `appFlowVars.ts` + `InlinePortsSection.tsx`, mount read-only inside `InlineNodePanel.tsx` for
   `llm`/`condition`. No drag-to-connect, no handle changes, no registry-shape risk. **Good
   natural pause point to demo to the user before committing to Phases 4/5's bigger surface.**
   **DONE 2026-09-25** — new `frontend/src/app/admin/applications/components/cbv/appFlowVars.ts`
   (`extractInlineNodeVars` for `llm`/`condition` only, confirmed empty reads/writes for every
   other kind; `upstreamAppFlowVarSources` composing `src/lib/graphWalk.ts`'s
   `reachablePredecessors`) and `cbv/panels/InlinePortsSection.tsx` (read-only "Reads" chip list,
   ported from the agent builder's `StepDataFlowSection.tsx` minus `PortAliasField`/rename/delete,
   which stay out of scope until Phase 5). Mounted inside `InlineNodePanel.tsx` right after the
   display-name field, in both the `llm` and `condition` branches. New test file
   `cbv/__tests__/appFlowVars.test.js` (11 tests, plain-`node`/`assert` convention matching Phase
   2's tests). Verified: `tsc --noEmit` clean project-wide, all 52 existing+new frontend tests
   passing (26 nodeVars + 7 templateVars + 8 graphWalk + 11 appFlowVars), full `next build`
   production build succeeds, and the live dev container (bind-mounted, hot-reloading) compiled
   the touched builder route with no errors after the edit — **not** visually verified in a
   browser (no browser-automation tool available in this session); a manual click-through by the
   user is recommended before treating Phase 3 as fully done from a UX standpoint.
4. **Data-port handles on the canvas node** — add data-in/data-out `<Handle>` elements to
   `InlineNode`, gated on kind. No wiring logic yet — handles exist and are visually inspectable
   via reused `PortsPopover`, but dragging onto them doesn't yet do anything beyond a plain edge.
   Isolates the trickiest layout math from state-mutation logic. **DONE 2026-09-25** — research
   found `InlineNode` has no fixed card height (unlike agent builder's `StepNode.tsx`, which grows
   its card to fit a pixel-based port rail), so handles use the existing percentage-spread pattern
   (same idea as `controlPorts`'s `100/(N+1)*(i+1)` formula) instead, offset into a 55-95% band so
   they never collide with `llm`'s single anonymous control-out at 50% (verified: only `llm` has
   `OutputPorts` per Phase 1, so `dataOutPorts` is non-empty only for `llm`; `dataInPorts` is
   empty for both kinds today since `input_aliases` don't exist until Phase 5 — the code path
   exists but currently renders nothing, exactly matching "no wiring yet"). **Real gap found and
   closed in the same phase**: `validateConnection` (`CanvasInner.tsx`) never inspected handle IDs
   at all — it would have silently accepted a drag onto/from any new `data-*` handle as an
   ordinary control edge, which is wrong (a data port is not a "run after" relationship). Added a
   guard rejecting any connection where either handle ID starts with `data-`, with message "Named
   data port wiring isn't available yet"; `handleConnect` (`CanvasBuilderView.tsx`) now also passes
   `conn.targetHandle` through (previously never used). New test file
   `components/__tests__/validateConnection.test.js` (6 tests). Verified: `tsc --noEmit` clean, all
   64 frontend tests passing (58 prior + 6 new), full `next build` succeeds. Not visually verified
   in a browser — confirm the new dot(s) render sensibly on an `llm` node's card and that dragging
   onto them shows the rejection message, not a silent connection.

   **CORRECTED 2026-09-25, live in the browser** — the visible 3rd dot from this phase was wrong.
   User's original ask was never "add a separate always-visible dot per data port" — it was one
   wire per connection, same as today's single in/out dot pair, with a picker menu handling
   multiplicity (see Phase 5 below). The extra `dataInPorts`/`dataOutPorts` `<Handle>` elements
   added to `InlineNode` were **reverted** (commit `8e6785fc`) — `llm`/`condition` nodes are back
   to exactly 2 handles, matching every other node kind. `validateConnection`'s `data-` prefix
   guard was **kept** (harmless no-op today, becomes load-bearing again in Phase 5 if any code
   path ever produces a literal `data-*` handle ID — it currently doesn't, since there's no
   `<Handle id="data-...">` anywhere anymore). **Lesson for Phase 5**: do not add new always-on
   `<Handle>` elements per named port. The 2 existing handles (plain control in/out) are also the
   drag points for data-port wiring — see Phase 5's revised design below, which reuses them rather
   than adding new ones.

5. **Drag-to-connect wiring + rename/delete** (the bigger, real half) — **REVISED SPEC,
   2026-09-25, confirmed with user, NOT YET IMPLEMENTED, this is the next task:**

   **UX, confirmed with user:**
   - No new handles. The user drags from the *existing* single output dot on an `llm` node (same
     dot already used for control-flow wiring) toward another `llm`/`condition` node.
   - **Source side has no picker** — `llm` only ever has one output var (`output_var`), so there's
     nothing to choose on the source end. Skip straight to the target.
   - **Target side**: on drop, if the target has more than one nameable field (`llm`: system
     prompt + user prompt; `condition`: only `expression`, so never ambiguous — auto-bind
     immediately, no picker), show a small popover at the drop point listing the candidate fields
     by label. User picks one; the binding commits to that field.
   - This is a asymmetric version of the plan's original Q2 answer (Section "Open questions",
     item 2) — the source-side half of that answer is now moot given LLM nodes never have more
     than one output var, so only the target-side popover is actually needed. Re-confirmed
     explicitly with the user 2026-09-25 rather than assumed.
   - The existing wire visually looks identical to a control-flow wire (same line, no new handle
     shape) — per user's "one wire, one dot per side" correction above. **Open follow-up, not yet
     resolved:** the user separately described wanting a small clickable count badge on a wire
     when it carries N port mappings (e.g. "5"), to inspect the mapping without opening the panel.
     Not yet designed in detail — flag this to the user again before Phase 5 implementation if it
     wasn't explicitly re-confirmed as in/out of Phase 5's first cut.

   **Data model** (unchanged from the original Phase 0 research, still correct): `input_aliases:
   {alias: underlying_flowvars_key}` inside the node's `config` object. Confirmed via direct code
   read (2026-09-25 research pass) that `config: Record<string, unknown>` is genuinely untyped at
   the TS level today (`types.ts`'s `InlineNodeData`/`FlowControlNodeData`), and both
   `canvasToDoc`/`docToCanvas` (`CanvasHelpers.ts` lines ~146, ~210-215) spread/pass the whole
   `config` object through with no field allowlist — so adding `input_aliases` inside it needs
   zero serialization-layer changes, confirming the plan's original claim.

   **Mechanics to build:**
   - `frontend/src/app/admin/applications/components/cbv/useInlinePortWiring.ts` (new): owns the
     connect-commit logic — writing `input_aliases` into the target node's `config`, and (per the
     plan's original alias semantics) rewriting `{{.oldName}}` occurrences in the target's
     prompt/expression text to the chosen alias if the user later renames it. Rename/delete reuse
     the "find/replace in text" pattern from `PortAliasField`'s caller side in the agent builder
     (NOT `PortAliasField` itself — that component is generically reusable for the rename-input UI
     piece, per Phase 0 research, but the binding-record wiring around it is agentgen-specific and
     was correctly not ported in Phase 3).
   - **No `onConnectStart` exists in AppFlow today** (confirmed via grep, 2026-09-25) — only a
     plain `onConnect` (`CanvasInner.tsx` prop, wired from `CanvasBuilderView.tsx:572`). Phase 5
     needs to add `onConnectStart`/`onConnectEnd` (or intercept inside the existing `onConnect`
     handler in `CanvasBuilderView.tsx:414`) to detect "this connection touches an `llm`/`condition`
     target with >1 nameable field" and open the popover before committing the edge — likely by
     deferring `addEdge`/`setEdges` until the popover's choice is made, rather than committing
     synchronously inside `handleConnect` as today.
   - **Coordinates**: React Flow's connect lifecycle does hand back a raw mouse/touch event, but
     nothing in this codebase captures `clientX/clientY` from it today (confirmed: agent builder's
     `onPipeConnectStart` discards its event arg entirely). Phase 5 will need to read the event
     manually to position the popover at the actual drop point.
   - **Relax `validateConnection`'s Phase-4 guard**: the `data-` prefix rejection
     (`CanvasInner.tsx` lines ~37-43) is currently a no-op (nothing produces a `data-*` handle ID
     since Phase 4's handles were reverted) — Phase 5 does not need to "relax" it since it was
     never blocking anything real to begin with. Leave it in place as a defensive fallback unless
     it becomes genuinely obsolete.
   - **No Go/backend changes needed** — confirmed with the user 2026-09-25. `input_aliases` lives
     entirely inside the frontend-owned, backend-opaque `config` object; the Go runtime already
     ignores unknown config keys (per Phase 0's original research).
6. **(Optional, propose but do not build without explicit separate approval)** — a warning-level
   `ValidationError` in `validate.go` for an `llm`/`condition` node referencing a var nothing
   upstream ever writes. Not part of the user's original ask (UI visibility, not a new compiler
   error class) — call this out explicitly before touching it if it comes up later.

### Open questions — RESOLVED 2026-09-24

1. **Scope-narrowing — CONFIRMED.** Only `llm`/`condition` get real named ports. User asked for
   the reasoning per excluded kind before signing off; verified directly against
   `go/internal/appflow/workflow.go` (lines ~425-610) rather than re-asserting the plan's own
   summary:
   - **`router`** — user accepted exclusion without needing the runtime check (skipped).
   - **`hil`** (425-438) — pure approve/reject gate. Only reads `approved`/`comment` from an
     activity result; never touches `vars` (FlowVars). No config field like `output_var` or an
     expression exists to alias. A port here would represent nothing real.
   - **`fork`** (548-602) — accepts no explicit input value; it's pure topology (fans out over
     `outEdgesBySource`). Never touches `vars`. Its "outputs" are the same `accumulated` context
     replayed down N branches, not distinct data values — so even calling them separate outputs is
     cosmetic.
   - **`join`** (604-610, merge logic in `graph.go`'s `walkBranch`/`mergeBranchResults`) — merges
     branch results via string concatenation into `accumulated`, never `vars`. The bare
     `case "join"` in workflow.go is just a pass-through if reached without a preceding fork.
   - Conclusion: all three (plus `agent`/`orchestrator`/`middleware`/`entryPoint`, unchanged from
     original research) are confirmed cosmetic-only for named ports. Only `llm`/`condition` read
     or write `FlowVars`. Scope stands as originally proposed.

2. **Drag-drop target — CONFIRMED: contextual popover at the drop point.** User explicitly wanted
   "something modern and clean," rejecting both a silent fixed-default field and a
   panel-must-be-open requirement. Final interaction model:
   - User drags a wire from an upstream output toward the `llm`/`condition` node — panel does
     **not** need to be pre-open.
   - On drop over the node card: if the target has more than one nameable field (`llm` has
     system prompt + user prompt), a small popover opens at the drop point listing the candidate
     fields by label; user picks one and the binding commits to that field.
   - If the target has only one nameable field (`condition`'s single expression), skip the
     popover and bind immediately — no extra click for the common case.
   - This reuses the drop-point screen coordinates already available from the drag gesture (no
     new positioning logic needed) and follows the common React Flow "connection line + drop
     menu" pattern — not a bespoke invention.
   - Implementation note for Phase 5: this changes the shape of the connect-commit handler in
     `useInlinePortWiring.ts` slightly from the original sketch — `onConnect` cannot commit the
     `input_aliases` write synchronously in all cases now; when the popover appears, the commit is
     deferred until the field choice is made. Design the handler as
     `resolveDropTarget(node) -> field | 'ambiguous'`, and only open the popover in the
     `'ambiguous'` branch.

3. **Output ports are symmetric with input ports** (confirmed with the user 2026-09-24 — not
   input-only, matching agentgen's symmetric treatment).
4. **`input_aliases` rename-rewrite risk** — renaming an alias does a find/replace over free-text
   prose (system/user prompts), not a structured field. If a user's prompt happens to reference
   the same var twice for genuinely different reasons, a rename replaces both — technically
   correct per Go template semantics (both really are the same var), but worth one line of UI
   copy warning the user ("renaming affects every use of `{{.x}}` in this node's fields").
5. **Phase 4's handle-layout math is under-researched** — read agent builder's `StepNode.tsx`
   rail-offset system in full before starting Phase 4, since `InlineNode` already has bespoke
   handle-spreading code for control ports (condition's true/false pips) that a naive addition of
   data-port handles could visually collide with.

---

## Explicitly out of scope

- Adding named ports to any kind other than `llm`/`condition` (see scope decision above) — unless
  the user later asks for a specific kind after seeing why it was excluded.
- Fixing the pre-existing `AgentNode`-has-no-outgoing-handle gap (tracked separately in
  `docs/LESSONS.md`) — not part of this plan, even though it's topically related.
- The optional Phase 6 static-validation pass, unless separately approved after Phase 5 ships.
- Any change to `docs/APP_CANVAS_DEBUG_PLAN.md`'s or `docs/PLATFORM_AS_TENANT_PLAN.md`'s own
  scope — this is a new, independent thread found *during* that work, not a continuation of it.
