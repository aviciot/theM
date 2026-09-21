# Node Registry — Unify App Canvas, Agent Builder and Middleware
# Status: Phases 1-5 COMPLETE.
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
| 2 — Extract shared registry | ✅ COMPLETE | (pending commit) |
| 3 — Register app canvas nodes | ✅ COMPLETE | (pending commit) |
| 4 — App canvas renders from registry | ✅ COMPLETE | (pending commit) |
| 5 — Middleware adopts node contract | ✅ COMPLETE | (pending commit) |

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

**STATUS: COMPLETE (2026-09-19).** `go test ./...` 0 failures (all packages, cached+fresh run);
`go vet ./...` clean except pre-existing unrelated `internal/llm` context-leak warnings (not
touched this phase); `tsc --noEmit` 0 errors (no frontend files changed — backend-only phase).

What was built:
- New package `go/internal/nodedefs/nodedefs.go` — `ConfigFieldDoc`, `NodeExample`, `PortDef`,
  `EdgeRules`, and `Meta` (the portable node metadata struct: `Label`, `Description`, `Emoji`,
  `Color`, `BgColor`, `Edges`, `InputPorts`, `OutputPorts`, `ControlOutputPorts`, `ConfigFields`,
  `UsageNotes`, `Examples`). No dependency on `agentgen` or its interpreter.
- `go/internal/agentgen/noderegistry.go` — `ConfigFieldDoc`/`NodeExample`/`PortDef`/`EdgeRules`/`Meta`
  are now Go type aliases (`type X = nodedefs.X`) so every existing reference in `agentgen`
  compiles unchanged. `NodeDef` and `NodeTypeInfo` both embed `nodedefs.Meta` (anonymous field) —
  its JSON fields serialise inline, same shape as before. `Execute`, `DeriveInputs`,
  `DeriveOutputs`, `DefaultPolicy`, `MaxPolicy` stayed on `NodeDef` (typed to the agentgen
  interpreter/compiler, per the plan).
- `go/internal/agentgen/nodes.go` — all 12 `RegisterNode(NodeDef{...})` literals updated: the
  fields now owned by `Meta` (`Label`, `Description`, `Emoji`, `Color`, `BgColor`, `Edges`,
  `InputPorts`, `OutputPorts`, `ControlOutputPorts`, `ConfigFields`, `UsageNotes`, `Examples`) are
  nested under a `Meta: nodedefs.Meta{...}` field; every value is unchanged, only the literal
  shape changed (struct-literal field promotion doesn't apply to embedded structs in Go, unlike
  method/field access on values).
- Test files updated to match (literal shape only, no behavior change):
  `go/internal/agentgen/local_executor_test.go` (49 `NodeDef{}` literals), `go/internal/temporal/canvas_workflow_test.go` (8 literals).

**Gate result:** `/admin/node-types` payload verified **semantically identical** before/after via
a throwaway `cmd/dumpnodetypes` helper (built, run, diffed, then deleted — not part of the
shippable change). Byte-for-byte the JSON key order changed (Go serialises embedded-struct fields
before the outer struct's own fields), but parsed-JSON equality holds: same 12 node types, same
keys, same values, verified with a Python dict-equality check. No frontend code depends on JSON
key order (nothing does; `JSON.parse` results are consumed as objects), so this is behaviour-neutral.

**Known deviation from the plan's stated gate:** the plan says "byte-identical" — the actual result
is "semantically identical, key order differs." Flagged here rather than silently reinterpreting
the gate.

---

### Phase 3 — Register app canvas nodes

**STATUS: COMPLETE (2026-09-19).** `go test ./...` 0 failures (full suite, `-count=1` fresh run,
1718 sub-test `--- PASS`, 0 `--- FAIL`); `go build ./...` clean. No frontend files touched this
phase (frontend consumption is Phase 4) — `tsc --noEmit` therefore unaffected, not re-run.

What was built:
- New file `go/internal/appflow/noderegistry.go` — `AppCanvasNodeInfo` struct (field-for-field
  shape match with `agentgen.NodeTypeInfo`: embeds `nodedefs.Meta` plus `type`, `version`,
  `output_arity`, `is_source`, `is_sink`, `single_input`, `accepts_dynamic_inputs`,
  `dynamic_outputs`, `executable`) and a static `appCanvasNodeRegistry` with all 6 kinds:
  `llm`, `condition`, `router`, `hil`, `fork`, `join`. Metadata (label/emoji/color) matches
  `CanvasNodes.tsx`'s `INLINE_META`/`FC_META` values exactly; `config_fields` sourced from
  `RouterConfig`/`HILConfig`/`InlineLLMConfig`/`InlineConditionConfig` (compiler.go/inline.go);
  `edges` degree rules sourced from `validate.go`'s structural checks (condition: exactly 2 out;
  router: ≥1 out; fork: ≥2 out; join: ≥2 in). `condition`'s `control_output_ports` carries the
  true/false handles that were previously hand-written JSX only
  (`CanvasNodes.tsx:384-391`). `AllAppCanvasNodeInfos()` returns a defensive copy.
- `go/internal/appflow/noderegistry_test.go` — 4 new tests: all 6 kinds present with non-empty
  label/color/executable, condition's true/false ports + exactly-2-edge rule, fork/join degree
  rules matching `validate.go`, and a copy-not-shared-slice mutation guard.
- `go/internal/admin/node_types.go` — `NodeTypesHandler.ServeHTTP` now merges
  `agentgen.AllNodeTypeInfos()` (12 entries) with `appflow.AllAppCanvasNodeInfos()` (6 entries)
  into one JSON array, marshalling each family separately (preserving their exact independent
  byte shape) then sorting the merged raw JSON by `type` for deterministic output. No new Go type
  wraps both families — `agentgen.NodeTypeInfo.Type` is `agentgen.StepType`, `appflow`'s is a bare
  `string`; merging at the `json.RawMessage` level avoids a shared-type coupling neither package
  needs.
- `go/internal/admin/node_types_test.go` — `TestNodeTypesHandler_ReturnsAllTypes` updated: the
  expected count is now `len(agentgen.KnownStepTypes()) + len(appflow.AllAppCanvasNodeInfos())`
  (12+6=18) instead of 12. New `TestNodeTypesHandler_IncludesAppCanvasKinds` asserts the 5
  appflow-only kinds are present with a label and `executable=true`, that exactly 2 entries are
  typed `"llm"` (agentgen's own StepLLM step + appflow's app-canvas llm node — same name,
  different family, both legitimately present), and that condition's 2 `control_output_ports`
  survive the merge.
- `go/TEST_INDEX.md`: S1-123, S1-124 added; S1 total 1310 → 1315.

**Deliberately NOT done this phase (Phase 4's job):** `CanvasNodes.tsx`'s `INLINE_META`/`FC_META`,
`constants.ts`'s `NODE_PORTS`, condition's hand-written true/false `Handle` JSX, and
`InlineNodePanel.tsx`/`FlowControlNodePanel.tsx`'s hardcoded config fields are all still in place
and still what the frontend actually renders from — nothing on the frontend reads
`/admin/node-types` for these 6 kinds yet. This phase only makes the backend registry exist and
be servable; Phase 4 is the frontend cutover.

**Correction found during this phase, not yet acted on:** `fork` and `join` have **zero config
fields today** — `FlowControlNodePanel.tsx` has no `isFork`/`isJoin` branch, only `isRouter`/
`isHIL` (falls through to a generic "Flow control node: {type}" placeholder text for both). The
new registry entries for `fork`/`join` correctly have an empty `config_fields` list (there is
nothing to configure — merge behaviour is fixed, not user-configurable), so this is not a
registry bug, just worth knowing before Phase 4 assumes every kind needs a form.

**Gate result:** `appflow` validation and execution code (`compiler.go`, `workflow.go`,
`validate.go`, `graph.go`) — completely untouched this phase; only new metadata files plus the
`/admin/node-types` merge changed. Full Go suite green. `test_42_appflow_inline_nodes.py` was
**not re-run live** (no live stack access in this session) — safe to infer unaffected given zero
changes to the files it exercises, but flagging rather than claiming verified-live.

---

### Phase 4 — App canvas renders from the registry

**STATUS: COMPLETE (2026-09-21).** `go test ./...` 0 failures (full suite, fresh Docker run);
`tsc --noEmit` 0 errors; `test_42_appflow_inline_nodes.py` 29/29 (unchanged — this phase touched
no execution code), verified live after rebuild + force-recreate of `them-go-bridge` and
`them-frontend`.

**Bug found and fixed before Phase 4 could safely proceed:** Phase 3 merged the `agentgen` and
`appflow` families into one `/admin/node-types` array with two legitimate entries typed `"llm"`
(agentgen's `StepLLM`, appflow's app-canvas llm node) — confirmed by Phase 3's own test. But the
frontend's shared cache (`frontend/src/lib/nodeRegistry.ts`) indexed entries by bare `type` in a
flat map (`_byType[d.type] = d`), so loading both families on one page would let one `"llm"` entry
silently clobber the other, and `getNodeDef("llm")` would return whichever landed last. This was
latent until Phase 4 became the first frontend consumer of the merged array from the app-canvas
side. Fixed by:
- `go/internal/admin/node_types.go`: `withFamily()` stamps a `"family": "agentgen"|"appflow"` tag
  onto each entry at the JSON-merge boundary — additive, touches neither `agentgen` nor `appflow`'s
  structs (keeping them decoupled, per Phase 3's stated design). New test
  `TestNodeTypesHandler_FamilyDisambiguatesDuplicateType`.
- `frontend/src/lib/nodeRegistry.ts`: cache is now keyed by `"family:type"`
  (`_byFamilyType`, `familyTypeKey()`); `getNodeDef(type, family = 'agentgen')` — every existing
  agent-builder call site is unchanged (they never pass `family` and get `agentgen` as before);
  app-canvas call sites pass `'appflow'` explicitly. New `getCachedNodeTypesByFamily(family)`.

**What was built (the actual Phase 4 cutover):**
- `frontend/src/app/admin/applications/components/CanvasNodes.tsx`: `FC_META`/`INLINE_META`
  hardcoded maps deleted. `FlowControlNode`/`InlineNode` call `getNodeDef(data.node_type,
  'appflow')` for emoji/color/label. `InlineNode`'s hardcoded `data.node_type === 'condition'`
  branch (two hand-written `Handle id="true"`/`id="false"`) replaced by a generic loop over
  `resolveOutputPorts(nodeDef, cfg)`'s control ports — a new branching appflow kind needs a
  `control_output_ports` entry in the Go registry and zero frontend changes.
- `frontend/src/app/admin/applications/components/CanvasHelpers.ts`: `docToCanvas`'s hardcoded
  `defaultDisplayName`/`defaultName` maps replaced by `getNodeDef(nodeType, 'appflow').label`.
  Condition-branch `sourceHandle` restoration generalized from `d.node_type === 'condition'` to
  "any inline node type whose registry entry has `control_output_ports`".
- `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx`: added a `useEffect`
  fetching `/admin/node-types` once (mirrors `useDefinitionLifecycle.ts`'s pattern), caches via
  `setCachedNodeTypes`. Palette's two hardcoded JSX arrays (Flow Control section, Inline/Logic
  section) replaced by `flowControlPalette`/`inlinePalette` — `appflowNodeTypes` filtered through
  a small local `APPFLOW_NODE_COMPONENT` map (`node_type` → `'inline' | 'flow_control'`, i.e. which
  RF node component/palette section it renders as). This split is a frontend/UI concern with no
  backend equivalent — agentgen has no analogous grouping either — so it stays as a small local
  map rather than inventing a new backend field for it. Drop-handler defaults
  (`APPFLOW_NODE_DEFAULTS`) similarly kept local (design-time seed config, not portable metadata).
- `frontend/src/app/admin/applications/types.ts`: `FlowControlNodeData.node_type` and
  `InlineNodeData.node_type` relaxed from hardcoded literal unions (`'router'|'hil'|'fork'|'join'`,
  `'llm'|'condition'`) to `string` — a new appflow kind needs zero frontend type-file edits.
- `frontend/src/app/admin/applications/components/cbv/panels/FlowControlNodePanel.tsx` +
  `InlineNodePanel.tsx`: config forms stay hardcoded per-`node_type` — **matches the agent
  builder's actual precedent, not just the plan's stated intent**: research this session confirmed
  `StepConfigSection.tsx` has no `config_fields`-driven generic form renderer anywhere in the
  codebase either; only visuals/handles/ports/policy are registry-driven on that side. Router/HIL
  forms use curated dropdowns and array editors that a generic renderer would regress into plain
  text inputs — not attempted. The one real improvement taken: panel headers/descriptions now read
  `getNodeDef(type, 'appflow').label`/`.description` instead of hardcoded duplicate strings, so
  backend copy changes propagate without a frontend edit.
- **Bug fixed as a direct consequence of the handle-id convention change** (see below):
  `InlineNodePanel.tsx`'s `branchTarget()` compared `e.sourceHandle === branch` against the bare
  `'true'`/`'false'` string; after the convention change below it would have permanently shown
  "not connected" for both branches. Fixed to compare against `` `ctrl-out-${branch}` ``.

**Handle-ID convention unified with the agent builder** (explicitly authorized by the user this
session — existing app-canvas flows are test data, fine to lose/recreate): condition branch
handles are now `ctrl-out-true`/`ctrl-out-false` in React Flow (matching `nodeRegistry.ts`'s
`resolveOutputPorts` convention: `` `ctrl-out-${port.id}` ``), not the bare `true`/`false` used
before. **The wire format (`ConnectionDef.label` sent to/from the backend) is unchanged** — still
the literal string `"true"`/`"false"`, because `appflow/validate.go:158` matches
`strings.EqualFold(e.Label, "true")` and cannot be touched without a backend change (out of scope).
`CanvasHelpers.ts`'s `canvasToDoc` strips the `ctrl-out-` prefix before sending; `docToCanvas`
re-adds it when restoring `sourceHandle` from a loaded definition.

**Deliberately not done, flagged rather than silently skipped:**
- No live browser click-through was performed (no browser available in this session's
  environment) — the plan's gate asks for "a manual round-trip (save → reload → condition still
  wired true/false)" done by a human in the UI. What *was* verified: `tsc --noEmit` clean, the
  running `them-frontend` container confirmed to be serving the edited source
  (`docker exec ... grep getNodeDef`), and `test_42`'s API-level publish/WS/condition-routing round
  trip (29/29) — which exercises the same `canvasToDoc`/`docToCanvas`-shaped JSON path but via
  direct API calls, not via drag-and-drop in a browser. **Recommend a human does the actual
  click-through before trusting this in production.**
- `NODE_PORTS` in `constants.ts` was left untouched — it encodes coarse cross-node-type wiring
  rules (what can connect to `entryPoint`/`orchestrator`/`agent`/`middleware`/`flowControl`/
  `inline`), not per-fine-kind edge arity. It was already keyed by RF node type, not `node_type`,
  before this phase, and the registry's `edges` field is per-fine-kind — they solve different
  problems. Not in scope.
- `FlowControlNodePanel.tsx`/`InlineNodePanel.tsx` were not converted to a `config_fields`-driven
  generic renderer — see above; this is genuinely more scope than "mirror the agent builder" implies
  once you check what the agent builder actually does today.

**Gate result:** `tsc --noEmit` 0 errors. `go test ./...` 0 failures (full suite). `test_42` 29/29
live after rebuild+force-recreate of `them-go-bridge` and `them-frontend`. Manual browser
click-through **not performed** — flagged above, recommended before production trust.

---

### Phase 5 — Middleware adopts the node contract

**STATUS: COMPLETE (2026-09-21).** `go test ./...` 0 failures (full suite); `test_42` 29/29 live
after rebuild + force-recreate of `them-go-bridge`. Backend-only by explicit user decision — no
frontend files touched this phase (see below).

**What was built:**
- `db/102_middleware_defs_node_contract.sql` — adds 4 nullable columns to `them.middleware_defs`:
  `edges`, `input_ports`, `output_ports`, `config_fields` (all JSONB). Existing rows with no
  seeded data keep working — same fail-open precedent as `097_middleware_defs_visual.sql`. Also
  seeds File Guard's real shape: `edges = {min_in:1, max_in:1, min_out:1, max_out:1}` (it sits
  inline on an `orchestrator → middleware → agent` chain — one edge in, one out, not branching)
  and `config_fields` mirroring `MiddlewareNodePanel.tsx`'s 6 real fields exactly (`enabled`,
  `mode`, `max_file_size_mb`, `allowed_types`, `blocked_types`, `notify_on_fail`). Applied to the
  live DB. The two other builtin rows (`cache_default`, `guard_default`) were left unseeded —
  confirmed live to degrade to zero-value edges + empty config_fields, not an error.
- `go/internal/admin/dal/middleware_wirings.go`: `MiddlewareDefSummary` gets 4 new
  `json.RawMessage` fields (`Edges`, `InputPorts`, `OutputPorts`, `ConfigFields` — raw, not typed
  as `nodedefs.EdgeRules`/`[]nodedefs.PortDef`, since this package has no dependency on
  `internal/nodedefs` and node_types.go is the only consumer). `ListMiddlewareDefs` query/scan
  extended; nil DB columns simply leave the field unset (`omitempty`).
- `go/internal/admin/node_types.go`: **`NodeTypesHandler` gained a DB dependency for the first
  time** — it was a zero-dependency `struct{}` through Phases 3-4. New `NewNodeTypesHandler(db
  DBQuerier) NodeTypesHandler` constructor; `db` may be `nil` (degrades to "no middleware entries",
  doesn't panic — every pre-existing agentgen/appflow-only test passes `nil`). New
  `middlewareNodeInfo(d dal.MiddlewareDefSummary) appflow.AppCanvasNodeInfo` shapes a DB row into
  the same JSON shape agentgen/appflow entries use (reused `appflow.AppCanvasNodeInfo` rather than
  inventing a third struct — field-for-field match already existed). `Executable` is hardcoded
  `false` for every middleware entry — `workflow.go`'s `case "middleware"` is still a pass-through
  no-op (verified unchanged this phase), so "executable" would be a lie otherwise. `ServeHTTP`'s
  merge loop gained a third pass over `dal.ListMiddlewareDefs`, tagged `withFamily(b,
  "middleware")` — same mechanism Phase 4 added for `agentgen`/`appflow`. A middleware DB read
  error fails open (agentgen/appflow entries still return) rather than 500ing the whole endpoint.
- `go/internal/admin/router.go`: route registration changed from `NodeTypesHandler{}.ServeHTTP` to
  `NewNodeTypesHandler(dbq).ServeHTTP` — `dbq` was already in scope in `BuildRouter`, no new
  wiring needed. Route stays public/unauthenticated (unchanged) — middleware rows returned here
  are the global/builtin catalog, same trust level as the two static Go registries.
- Tests: `TestNodeTypesHandler_MergesMiddlewareFamily` (seeded row → `family="middleware"`, edges
  and config_fields decode correctly, `executable=false`), `TestNodeTypesHandler_NilDBSkipsMiddlewareFamily`
  (nil db → no middleware entries, no panic). All 4 pre-existing `node_types_test.go` tests updated
  from `admin.NodeTypesHandler{}.ServeHTTP` to `admin.NewNodeTypesHandler(nil).ServeHTTP` —
  behaviour unchanged (nil db was always the implicit case before this phase existed).

**Verified live:** `GET /api/v1/admin/node-types` returns 21 entries (18 from Phases 3-4 + 3
middleware rows); `file-guard` entry has `family: "middleware"`, correct 1-in/1-out edges, all 6
config_fields, `executable: false`.

**Deliberately NOT done this phase, by explicit user decision (not an oversight):**
- **No frontend changes.** The canvas still fetches File Guard's visuals from the older, separate
  `GET /admin/middleware-defs` call (`CanvasBuilderView.tsx`'s `mwVisualById`/`listMiddlewareDefs`)
  — it does **not** yet read from the newly-merged `/admin/node-types` entry. Both endpoints now
  serve overlapping data; the frontend cutover (retiring the separate fetch, per the plan's
  "Dropped: middleware as a UI special case" goal) was explicitly deferred to a future session to
  avoid touching File Guard's UI — a real security feature — in the same pass as a backend
  architecture change.
- **Making middleware actually execute inside the app-canvas graph** — confirmed unchanged this
  phase: `internal/appflow/workflow.go`'s `case "middleware":` and `graph.go`'s `walkBranch`
  `case "orchestrator", "middleware":` are both still pure pass-throughs (follow the first
  outgoing edge, no side effects). File Guard still only runs via the orchestrator/A2A middleware
  gate (`internal/middleware/gate.go`), never inside an app-canvas graph run. This was always
  explicitly out of scope for Phase 5 (see "Deferred" below) — restated here as confirmed, not
  assumed.
- **`config_fields`-driven generic rendering of `MiddlewareNodePanel.tsx`** — same reasoning as
  Phase 4's equivalent note for `FlowControlNodePanel.tsx`/`InlineNodePanel.tsx`: the panel's
  curated form (toggle, dropdown, comma-separated-list inputs) would regress under a naive generic
  renderer, and no such renderer exists anywhere in this codebase yet. Out of scope.

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
