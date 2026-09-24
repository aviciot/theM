# AppFlow Named Data Ports — Plan
# Status: PLANNED, phased. Phase 0 (research + planning) COMPLETE. Phase 1 NEXT.
# Owner: platform
# Last updated: 2026-09-24

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
   `llm` in `noderegistry.go`. Zero runtime behavior change. New `AF-NR-xx` test(s).
2. **Shared frontend extraction** — move `extractTemplateVars`/`reachablePredecessors`/
   `reachableSuccessors` to `frontend/src/lib/`; agentgen re-exports, zero behavior change there
   (verify against agent builder's existing tests/smoke).
3. **"What can I read, and where from" panel** (the smaller, high-value half) — build
   `appFlowVars.ts` + `InlinePortsSection.tsx`, mount read-only inside `InlineNodePanel.tsx` for
   `llm`/`condition`. No drag-to-connect, no handle changes, no registry-shape risk. **Good
   natural pause point to demo to the user before committing to Phases 4/5's bigger surface.**
4. **Data-port handles on the canvas node** — add data-in/data-out `<Handle>` elements to
   `InlineNode`, gated on kind. No wiring logic yet — handles exist and are visually inspectable
   via reused `PortsPopover`, but dragging onto them doesn't yet do anything beyond a plain edge.
   Isolates the trickiest layout math from state-mutation logic.
5. **Drag-to-connect wiring + rename/delete** (the bigger, real half) — `useInlinePortWiring.ts`:
   connect-start ghost-port preview, connect-commit (rewrite template text + `input_aliases`),
   rename (find/replace in text), delete. Needs to locate and hook into whatever currently owns
   `onConnect` for the App Canvas (not yet located precisely — quick grep at implementation time).
6. **(Optional, propose but do not build without explicit separate approval)** — a warning-level
   `ValidationError` in `validate.go` for an `llm`/`condition` node referencing a var nothing
   upstream ever writes. Not part of the user's original ask (UI visibility, not a new compiler
   error class) — call this out explicitly before touching it if it comes up later.

### Open questions requiring the user's decision before implementation starts

1. **Scope-narrowing confirmation** (see above) — only `llm`/`condition` get real ports; every
   other kind gets none, because they don't touch `FlowVars`. Needs explicit sign-off since it
   narrows "every node" from the user's first framing of the request.
2. **Drag-drop target ambiguity** — when dragging a wire onto an `llm`/`condition` node to create
   a named port, does the drop need to land directly on the node card itself (like agent
   builder), or does it need to land inside the specific prompt/expression text field inside the
   open side panel? AppFlow's `llm` node has two text fields (system + user prompt) it could feed
   — dropping on the card alone is ambiguous about which field receives it, unlike agentgen's
   steps which (per this research) don't have this same multi-field ambiguity. Two live options:
   (a) card-drop targets a fixed default field (e.g. always user_prompt) — simple, one rule to
   learn; (b) drop zones live inside the open panel next to each specific field — unambiguous,
   but requires the panel already open, which is a real interaction-model change from agent
   builder's flow. **This is the single biggest open UX question and should be resolved with the
   user before Phase 4/5 implementation begins — it changes the interaction model, not just the
   data model.**
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
