# Node Registry — Unify App Canvas, Agent Builder and Middleware
# Status: Phase 1 COMPLETE. Phases 2-5 not started.
# Date: 2026-09-19

---

## Goal

**A node is defined once. Every canvas reads that definition. Nothing is hardcoded.**

Today the same concept is expressed three different ways:

| Kind | Defined where | Canvas reads it how |
|---|---|---|
| Agent builder nodes | code registry (`agentgen/noderegistry.go`) | generically, from `/admin/node-types` |
| App canvas nodes | hardcoded in 6 places | hardcoded again in the frontend |
| Middleware (File Guard, PII Guard) | DB table `them.middleware_defs` | special-cased everywhere |

Three systems for one idea. They will drift apart permanently unless unified.

**After this plan:** one endpoint returns every node type — whether it came from code or from the
DB — in one shape. The canvas draws them all identically and does not care where they came from.

---

## Progress

| Phase | Status | Commit |
|---|---|---|
| 1 — Runtime config split | ✅ COMPLETE | (pending commit) |
| 2 — Extract shared registry | ⬜ NOT STARTED ← **next** | — |
| 3 — Register app canvas nodes | ⬜ NOT STARTED | — |
| 4 — App canvas renders from registry | ⬜ NOT STARTED | — |
| 5 — Middleware adopts node contract | ⬜ NOT STARTED | — |

**Update this table at the end of every session.** One phase per session.

Baseline at plan creation: HEAD `dd8f3e0f`, `go test ./...` 0 failures (S1 1254, S2 59),
`tsc --noEmit` 0 errors, `test_42_appflow_inline_nodes.py` 21/21.

---

## Rules (non-negotiable)

1. **One phase per session.** Do not start the next phase — hand over instead.
2. **STOP RULE: no new app-canvas node types** (Tool, Transform) until Phase 3 lands.
   Adding one today costs six hardcoded edits, then the same work again during migration.
3. **Gates must pass before every commit:**
   - `go test ./...` — zero failures
   - `go/TEST_INDEX.md` updated in the **same commit** as any new/changed Go test
   - frontend: `tsc --noEmit` — zero errors
   - `scripts/tests/test_42_appflow_inline_nodes.py` — still passing
4. **Never delete a test to make the suite pass.**
5. **Commit only files relevant to the phase** — no `git add .` / `git add -A`.
6. **Update the Progress table + `docs/CURRENT.md`** before handing over.

---

## Environment (read before running anything)

**There is no local Go toolchain on this box.** Run Go through Docker:

```bash
docker run --rm \
  -v /opt/docker/them/go:/src \
  -v /tmp/gocache/mod:/go/pkg/mod \
  -v /tmp/gocache/build:/root/.cache/go-build \
  -w /src golang:1.25-alpine go test ./...
```
Mount the two cache volumes or every run re-downloads all dependencies (~3 min vs ~10 s).

**Frontend:** `cd frontend && ./node_modules/.bin/tsc --noEmit -p tsconfig.json`

**After any Go change, rebuild AND force-recreate** — `build` + `restart` does NOT pick up a
new image (see `docs/LESSONS.md`):
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d --build --force-recreate them-go-bridge them-dag-worker
```
Frontend changes need `them-frontend` rebuilt the same way — it was missed once already and the
UI silently ran a stale image.

**Known pre-existing conditions — do not "fix" these:**
- `internal/appflow/compiler.go`, `cmd/dag-worker/main.go` and ~45 other files are gofmt-unclean
  at baseline. Do not mass-reformat; it buries real diffs.
- The repo has no eslint config — `npx eslint` fails by design.

---

## Recommendations

- **Model: Sonnet 5.** These phases are mechanical — move code, register definitions, copy an
  existing frontend pattern. Managed settings pin Sonnet 4.6 on restart, so run `/model sonnet`
  at the start of each session.
- **Phase 1 is independent of 2–4** and can run in parallel in a separate session.
- **Phase 2's gate is the strongest in the plan:** capture the `GET /admin/node-types` JSON
  before the extraction and diff it after. Identical payload = the refactor is provably
  behaviour-neutral, in one command instead of a debugging session. Use the same technique for
  any "pure refactor" step here.
- **Verify refactors by executing, not by reading.** Three silent data-loss bugs in the
  inline-nodes work all looked correct on inspection and only showed up in an executed
  round-trip test.
- **Do not trust a subagent's "done" report** — re-run its gate yourself.

---

## Scope

**In:** how a node is *described* (label, colour, handles, edge rules, config fields) and how the
canvas renders and configures it.

**Out:** how a node *executes*. The agent canvas runs in-process (`agentgen` interpreter); the app
canvas runs on Temporal (`appflow`). That difference is real and stays. Only the description is
shared.

---

## Current state — verified, not assumed

- `NodeDef` (`internal/agentgen/noderegistry.go`) has ~25 fields. All carry a `json` tag **except
  `Execute` / `DeriveInputs` / `DeriveOutputs`**, which are `json:"-"`. That line is already the
  portable/non-portable seam — the metadata was designed to be serialisable.
- The agent builder frontend already renders generically: `StepNode.tsx:125` draws handles from
  `control_output_ports` rather than hardcoding them. This is the working reference for Phase 4.
- `GET /admin/node-types` already exists (`internal/admin/router.go:273`), served by
  `them-go-bridge`. No new endpoint is needed.
- `them.middleware_defs` already has `emoji`, `color`, `bg_color`, `display_name`, `config`,
  `enabled`, `scope`, `version`. It is **missing** edges, ports and typed config fields.
- **In AppFlow, `middleware` nodes are a no-op pass-through** (`workflow.go:417`,
  `graph.go:144`). The real File Guard runs in the orchestrator and A2A paths
  (`internal/middleware/gate.go`), never in the app canvas graph. Phase 5 is what makes a
  middleware node on the app canvas actually do something.

---

## Services (for context)

| Service | Role |
|---|---|
| `them-go-bridge` | builds, validates, publishes canvases; serves `/admin/node-types` and the UI |
| `them-agent-runtime` | executes agent canvases locally (in-process) |
| `them-dag-worker` | executes both canvases on Temporal (different queues/workflows) |

`agentgen` is compiled into bridge, agent-runtime and dag-worker — so a registry change reaches
all three. `appflow` is separate, which is why it must be wired in explicitly (Phase 3).

---

## Phases

Each phase is independently shippable and ends green. One phase per session (`CLAUDE.md`).

### Phase 1 — Runtime config split (independent, do first)

Move `provider` / `model` off the app-canvas inline LLM node into the Runtime screen, mirroring
the agent builder's existing pattern (`GET|PUT /admin/applications/{id}/agents/{agent_id}/llm-nodes`,
`internal/admin/agent_bindings.go`).

- Compiler emits an `llm_nodes` declaration on the AppFlowSpec (mirror `collectLLMNodes`)
- Overrides stored per-app **outside** the published definition
- Workflow prefers the override over the compiled value
- Canvas panel shows provider/model read-only with a "configured in Runtime" hint
- Prompt, `output_var`, expression **stay on the canvas** — those are design-time

**Why first:** a real defect today. Inline nodes are invisible in the Runtime screen, and changing
a model forces a canvas edit + re-publish. Independent of the registry work.

**Gate:** `test_42` updated (it currently sets `provider:"mock"` in canvas config) and passing.

**STATUS: COMPLETE (2026-09-19).** `go test ./...` 1310 tests 0 failures (S1-122, 12 new tests);
`tsc --noEmit` 0 errors; `test_42_appflow_inline_nodes.py` 29/29 (6 new checks) — verified against
the live stack after rebuild + force-recreate of `them-go-bridge` and `them-dag-worker`.

What was built:
- `db/101_app_flow_llm_overrides.sql` — new table `them.app_flow_llm_overrides
  (application_id, node_id, provider, model, updated_at)`, PK `(application_id, node_id)`, FK
  cascade, no RLS (same precedent as `app_temporal_config`). Applied to the live DB.
- `internal/appflow/compiler.go` — `AppFlowSpec.LLMNodes []AppFlowLLMNodeSpec`, populated by
  `collectLLMNodes` (mirrors `agentgen.collectLLMNodes`). `ApplyLLMOverrides(spec, overrides)` —
  pure function, rewrites `Provider`/`Model` inside a matching `kind:"llm"` node's `Config`,
  preserving every other field (prompts, `output_var`, etc).
- `internal/appflow/llm_override_loader.go` — `PgxAppFlowLLMOverrideLoader` (DB-backed, mirrors
  `PgxTemporalConfigLoader`; no unit test, consistent with that sibling — DB-loader tests in this
  codebase are integration-only).
- `internal/admin/dal/appflow_llm_nodes.go` — `GetActiveDefinitionJSON`, `ListAppFlowLLMOverrides`,
  `UpsertAppFlowLLMOverride`.
- `internal/admin/service/appflow_llm_nodes.go` — `AppService.GetAppFlowLLMNodes` (compiles active
  definition, merges stored overrides) + `PutAppFlowLLMOverride` (validates non-empty provider+model).
- `internal/admin/applications.go` — `GET|PUT /admin/applications/{id}/flow-llm-nodes[/{node_id}]`,
  TenantTx-scoped, same pattern as `PutRuntime`.
- `internal/execution/lifecycle.go` — `AppFlowLLMOverrideLoader` interface +
  `WithAppFlowLLMOverrideLoader`; `StartAppFlow` applies overrides to `input.Spec` before
  `ExecuteWorkflow` (fail-open: DB error or nil loader → no overrides, canvas-compiled value used).
  Wired in `cmd/them/main.go`.
- Frontend: `InlineNodePanel.tsx` LLM section — provider/model are now a read-only display with a
  "configured in Runtime" hint; prompt/`output_var`/temperature stay canvas-editable (design-time).
  New `RuntimeAppFlowLLMSection.tsx` (copies `CanvasAgentsSection`'s per-node override UI, flat —
  no per-agent grouping since these nodes sit directly on the app canvas) wired into
  `RuntimeView.tsx`'s General tab.
- `scripts/tests/test_42_appflow_inline_nodes.py` — new step [7b]: GET lists the compiled node with
  no override, PUT sets an override, GET confirms it merged without touching the compiled value,
  then resets to `mock` so the existing WS run (steps 8-10) is unaffected.

**Known pre-existing issues found, not fixed (out of scope for this phase):**
- `RuntimeView.tsx` was already over the 400-line file-size guideline (594 lines) before this
  change; now 618 after wiring in the new section. A split was not attempted — flagged, not fixed.
- `go/TEST_INDEX.md`'s bottom-line `go test ./...` total was already stale/out of sync with the S1
  subtotal before this change (1254 vs S1's 1298). Bumped to 1310 with a note rather than doing a
  full reconciliation, which is a separate cleanup.
- A stray uncommitted, non-compiling `internal/admin/dal/gateway.go` was found mid-session (from a
  parallel session's in-progress work on the same repo) — it was not touched; the parallel session
  later committed a fix (`8e2082c7`) that also patched the shared `service.Dal` test fakes this
  phase's interface change required.

---

### Phase 2 — Extract the shared registry

New package `internal/nodedefs`. Move the portable half of `NodeDef` into it: `Label`,
`Description`, `Emoji`, `Color`, `BgColor`, `Edges`, `InputPorts`, `OutputPorts`,
`ControlOutputPorts`, `ConfigFields`, `Examples`, `UsageNotes`.

`Execute`, `DeriveInputs`, `DeriveOutputs`, `DefaultPolicy` stay in `agentgen` — they are typed to
its interpreter.

`agentgen` imports `nodedefs` and keeps working exactly as before.

**Gate:** agent builder behaviour byte-identical. Verify the way the step-6 split was verified —
compare the served `/admin/node-types` payload before and after; it must be unchanged.

---

### Phase 3 — Register app canvas nodes

Register the 6 app-canvas node types in `nodedefs`: `llm`, `condition`, `router`, `hil`, `fork`,
`join`. Real definitions replace the hardcoded copies:

| Hardcoded today | Comes from the registry after |
|---|---|
| `INLINE_META` / `FC_META` (`CanvasNodes.tsx`) | `label`, `emoji`, `color` |
| `NODE_PORTS` (`constants.ts`) | `edges`, ports |
| condition's two handles (hand-written JSX) | `control_output_ports` |
| `InlineNodePanel` form fields | `config_fields` |

`/admin/node-types` returns both families.

**Gate:** `appflow` validation and execution unchanged; full Go suite + `test_42` green.

---

### Phase 4 — App canvas renders from the registry

Frontend stops hardcoding. Palette, node rendering, handles and the properties form are all
driven by the registry payload — copying `StepNode.tsx` / `StepConfigSection.tsx`, which already
do exactly this for the agent builder.

**Gate:** `tsc` clean, `test_42` green, and a manual round-trip (save → reload → condition still
wired true/false). Registry-driven handles are exactly where a regression would hide.

---

### Phase 5 — Middleware adopts the node contract

**Middleware stays in the DB.** It gains the columns a node needs, so it can be returned in the
same shape as a code node.

Migration on `them.middleware_defs`, all nullable so existing rows keep working:

| Column | Purpose |
|---|---|
| `edges JSONB` | in/out degree rules |
| `input_ports JSONB` / `output_ports JSONB` | handles |
| `config_fields JSONB` | typed field list so the properties panel renders itself |

`/admin/node-types` merges code nodes + DB middleware into one list.

**Kept:** per-tenant enable/disable, and adding a new guard by seeding a row — no deploy.
**Dropped:** middleware as a UI special case.
**Trade-off, stated plainly:** config-field definitions become JSONB data rather than
compile-checked Go. Acceptable because it is what buys no-deploy seeding.

**Separate and NOT in this phase:** making a middleware node actually execute inside the app
canvas graph. Today `case "middleware"` is a pass-through that does nothing (`workflow.go:417`);
File Guard runs only in the orchestrator and A2A paths. Wiring real execution is its own slice —
see "Deferred" below. Phase 5 makes middleware *look and configure* like a node; it does not
change what runs.

---

## Stop rule

**No new app-canvas node types (Tool, Transform) until Phase 3 lands.** Adding one today costs six
hardcoded edits, and then the same work again during migration. That is the specific waste this
plan exists to stop.

---

## Sequencing and cost

| Phase | Sessions | Depends on |
|---|---|---|
| 1 — Runtime split | 1 | — |
| 2 — Extract registry | 1 | — |
| 3 — Register app nodes | 1 | 2 |
| 4 — Frontend generic | 1 | 3 |
| 5 — Middleware contract | 1 | 3 |

~5 sessions. Phase 1 can run in parallel with 2–4; it touches different code.

---

## Deferred (explicitly out of scope)

1. **Middleware execution inside the app canvas graph** — the pass-through at `workflow.go:417`
   made real. Needs its own design: guards are pre/post filters, not sequential nodes.
2. **App canvas local execution** — "local" today means "run the bound orchestrator", not "run the
   graph in-process". There is no local AppFlow walker.
3. **Merging the two canvases.** The design brief states the app canvas should *eventually replace*
   the agent builder. This plan deliberately does not do that — it removes the duplication that
   would make such a decision expensive, and leaves the decision open.

---

## Honest notes

- This is a **refactor of working code**. Users see nothing new. The payoff is that the next node
  type costs one registry entry instead of six edits, and the two canvases stop drifting.
- The duplication being fixed here was introduced by the inline-nodes work (steps 7–11): the
  existing Router/HIL/Fork style was mirrored rather than the better `agentgen` pattern sitting in
  the same binary. Phases 2–4 are paying that back.
- Deferring is defensible if other work is more urgent — the cost is real but not yet painful.
  What is **not** defensible is adding more hardcoded node types in the meantime (see Stop rule).
