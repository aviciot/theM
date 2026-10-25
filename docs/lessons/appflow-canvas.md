---
title: Lessons — App Canvas / AppFlow & Frontend
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-10-15 — `"gateway"` missing from `validProtocols` in publish.go

**Symptom:** Creating a gateway profile app via the API — validate returns `invalid_protocol "gateway"` even though gateway apps exist in the DB and the UI creates them fine.
**Root cause:** `go/internal/admin/service/publish.go`'s `validProtocols` map listed `websocket`, `sse`, `voice`, `webrtc`, `a2a` but not `"gateway"`. Existing apps were inserted via older path before this validation was added.
**Fix:** Added `"gateway": {}` to `validProtocols`. Rebuild + `docker compose up -d --force-recreate them-go-bridge`.
**Watch for:** Any new EP protocol (e.g. `voice` expansion) — add it to this map or validate/publish will silently reject it.

---

---

## 2026-09-26 — A dotted default value silently breaks Go text/template, caught only by reading the runtime source

**Symptom:** none in production — caught before shipping, during Phase 5 of `docs/_archive/APPFLOW_NAMED_PORTS_PLAN.md` (AppFlow named data-port wiring). A first-cut fix generated default port-binding alias names like `LLM2.output` (readable, traces back to the source node).
**Root cause:** `{{.aliasName}}` in AppFlow's `llm`/`condition` node text is executed by real Go `text/template` (`go/internal/appflow/inline.go`'s `renderFlowTemplate`) against `FlowVars`, a flat `map[string]string`. A literal `.` inside the alias is NOT "the map key that happens to contain a dot" — Go's template dotted-field syntax parses `{{.A.B}}` as chained field access ("field `B` of field `A`"), which does not resolve against a flat string map. A dotted alias would have silently mis-resolved (or errored) the first time a user actually used it, not at write time.
**Fix:** the alias-name sanitizer (both the auto-generated default in `useInlinePortWiring.ts` and the manual rename field in `PortAliasField.tsx`) folds a literal `.` to `_` alongside whitespace/braces/quotes. Default alias naming uses `<SourceDisplayName>_<var>` (underscore joiner), never dotted. A dedicated regression test (`generated alias never contains a literal dot...`) pins this down so it can't silently regress.
**Watch for:** any time a frontend feature generates a string that gets interpolated into a Go `text/template` string (here: AppFlow's `{{.x}}` FlowVars templates; also true of agentgen's equivalent), **read the actual template-execution code** (`Parse`/`Execute` call site + the data type it executes against) before assuming a "just a string key" model — don't reason about Go template syntax from the `{{.x}}` shape alone. A `map[string]string` and a nested struct look identical in `{{.x}}` prose but behave completely differently the moment the key contains a `.`.

---

---

## 2026-07-14 — Entry point save 500: diff keyed on id that the frontend lost

**Symptom:** Saving an existing application in the canvas builder returns 500 with `UniqueViolationError: duplicate key value violates unique constraint "entry_points_slug_key"`.

**Root cause:** `_apply_entry_point_diff` matched entry points by their database `id`. The frontend stored this id in `_epId` on each canvas node. If `_epId` was missing (lost due to any canvas interaction), the server treated the existing EP as a new insert, hit the slug unique constraint, and crashed.

**Fix:** Changed the diff to key on `slug` instead of `id`. Slug is already globally unique (DB constraint) and immutable by design (it's a URL — renaming is a breaking change). The server owns identity: existing slug → UPDATE, new slug → CREATE, missing slug → DELETE. Frontend never needs to track or send `id`.

**Watch for:** Slug is now the stable identity for entry points. If a user wants to "rename" a slug they must delete the old EP and create a new one — this is intentional, as renaming a slug breaks external clients pointing at the old URL.

---

---

## Canvas save: graph-centric model prevents "3 EPs → 3 orchs" duplication (2026-07-15)

**What burned us:** When 3 entry points all wired to the same orchestrator node were saved, the old EP-centric save code created a separate `app_orchestrators` row per EP. On reload it looked like 3 independent orchestrators. Root cause: upsert keyed by orchestrator *name* alone — multiple rows with the same name could coexist when the loop ran per-EP.

**Fix:** DB unique index `(application_id, node_id)` on `app_orchestrators`. Frontend sends the full `graph: {nodes, edges}` from React Flow state — the compiler walks edges once and upserts one AO row per orch node, regardless of how many EPs point to it.

**Corollary:** Canvas positions must be keyed by the React Flow **node id** that was actually used when saving. The load function (`buildNodesFromApp`) generates its own node ids (`ep_<slug>`, `orch_<ao_id>`, `agent_<agent_id>`). After a save, positions are stored under those ids — the load function must look them up by the same id it just generated, with legacy-prefix fallback for old saves.

**Watch for:** If you add a new node type to the canvas, make sure `handleSave` includes its id in `canvasLayout` and `buildNodesFromApp` uses `pos(nodeId, legacyKey)` to restore its position.

---

---

## Publish/validate skips the DB registry via a hardcoded kind check (2026-09-17)

`ValidateDefinition` and `PublishDefinition` resolve every canvas component against
`them.component_definitions`. Components implemented in code have no such row, and the
exemption was a hardcoded string compare in **two** places in
`go/internal/admin/service/publish.go` — `!= "flow_control"` in the validate loop and again in
the publish projection loop.

**Consequence:** adding a new `definition_ref.kind` (we added `"inline"` for LLM/Condition nodes)
makes every publish containing that kind fail with `component_not_found`, even though the node
is perfectly valid. The failure is confusing because it points at the registry, not at the
missing exemption.

**Fix:** both sites now call one helper, `isBuiltinKind(kind string) bool`
(`flow_control` | `inline`), so the exemption list has a single home.

**Watch for:** any new canvas node family implemented in Go rather than registered in the DB.
Add its kind to `isBuiltinKind` in the same commit, and note that the exemption must NOT bypass
structural checks — `version: 0` on a builtin still has to fail `missing_version` (test PUB-IN-02).

---

## Canvas topology errors were invisible until the first connection (2026-09-17)

`appflow.Compile` / `appflow.Validate` were called from exactly two places, both at request
time (`internal/ws/handler.go`, `internal/sse/handler.go`). The admin validate and publish
endpoints never ran them. `internal/admin/` imported `appflow` only in `hil_approvals.go`.

**Consequence:** a canvas with a 1-branch fork, a router with no outgoing edges, or (now) a
condition node missing its true/false edges would validate clean, publish clean, and then fail
at the first user connection — surfacing to the end user as "failed to start appflow workflow"
rather than to the builder who could fix it.

**Fix:** `validateDoc` now runs `appflow.Compile` + `appflow.Validate` and merges the codes into
the report, which the canvas already maps onto the offending node via `instance_id`
(`_error` / `_errorMsg` → red ring + tooltip). Two deliberate constraints:
- **Gated on `execution_backend == "temporal"`.** A local-backend app does not execute the graph
  at all, so graph rules must not block it (test PUB-IN-04 pins this with the same fixture).
- **`unresolved_agent` is filtered.** `_resolved_agent_ids` is stamped *during* publish
  (`dal/publish.go`), so it is absent on a draft and every agent node would report unresolved.
  Agent identity is already covered by the registry resolution loop.

**Watch for:** adding a validation code to `appflow.Validate` now changes publish behaviour for
existing Temporal-backend apps. That is usually what you want, but it can reject a canvas that
published fine yesterday — call it out in the deploy notes.

---

## React Flow multi-handle nodes: three silent round-trip losses (2026-09-19)

Adding a canvas node with **two named source handles** (an inline Condition node with `true` /
`false` branches) surfaced three bugs that all typecheck cleanly and fail only at runtime, after
a save/reload cycle. Anything else with named handles will hit the same three.

1. **`docToCanvas` never set `sourceHandle`.** Edges were rebuilt from `conn.label` but the handle
   id was dropped, so a reloaded canvas drew both branches from the same handle — and because
   `canvasToDoc` derives the label FROM `sourceHandle`, the next save wrote both connections with
   no label at all. A working gate silently became unroutable, one save after it looked fine.
2. **Edge ids collided.** Ids were `e_${source}_${target}`. A condition whose true and false
   branches both point at the same node produced two edges with identical ids, and React Flow kept
   one. Fix: append the label.
3. **`validateConnection` ignored `sourceHandle`.** Its duplicate guard compared
   `(source, target)` only, so the second branch of "log either way, then continue" was refused
   with a misleading "These nodes are already connected". Fix: compare
   `(source, sourceHandle, target)`, normalising with `?? null` so every existing single-handle
   node type behaves identically.

**Watch for:** any new node type with more than one source handle. The pair
`canvasToDoc` (handle → label) and `docToCanvas` (label → handle) must be symmetric, or the data
degrades one save at a time.

**Test it by executing a round trip, not by reading the diff.** These were caught by building an
in-memory graph, running `canvasToDoc` then `docToCanvas`, and asserting labels, handles and edge
ids survive — including the case where both branches share one target. Reading the code was not
enough; all three looked correct.

**Also verified while here:** `@xyflow`'s `addEdge` compares `sourceHandle` alongside
source/target, so it does NOT dedupe a condition's two branches landing on the same node.

---

## Dead UI component + 3 dead config fields survived a view merge unnoticed (found 2026-09-23)

`frontend/src/app/admin/applications/components/SessionsView.tsx` (528 lines) had zero imports
anywhere in the repo — a code comment in `AppCard.tsx` line 331 read "Monitor (unified — replaces
separate Sessions + Monitor)", confirming `MonitorView.tsx` absorbed its functionality at some
point, but the old file itself was never deleted. Tracing its only real dependency —
`MonitoringConfig` (the "Settings → Monitoring" panel's 8-field JSONB blob in `them.config`) —
found `heatmap_low`, `panel_max_sessions`, and `stats_window_seconds` were only ever read by the
now-dead `SessionsView.tsx`; `MonitorView.tsx` (the surviving, actually-mounted view) never
consumed them, despite all three still having live sliders in the Settings UI and full backend
plumbing (Go struct fields, DB round-trip, `go/internal/admin/service/config_test.go` coverage).

**Why it wasn't caught earlier:** deleting the losing side of a UI merge is easy to skip — the app
still builds and works with the dead file present, so nothing forces cleanup. Once the file was
orphaned, the config fields it alone consumed kept looking "used" to anyone grepping only within
`MonitoringConfig`'s own type/defaults/handler chain — you have to trace forward to the actual
runtime consumer(s), not just confirm the field is threaded through the type system end-to-end.

**Fix:** deleted `SessionsView.tsx`; removed `heatmap_low`, `panel_max_sessions`,
`stats_window_seconds` from the Go `MonitoringConfig` struct/defaults/validation, the TS
`MonitoringConfig` type, both default-value constants (`MONITORING_DEFAULTS`, `MON_DEFAULTS`), and
their 3 sliders in `MonitoringPanel.tsx`; updated the now-medium<high-only heatmap validation and
all affected Go tests + `TEST_INDEX.md`.

**Watch for:** after any "component A absorbs component B" refactor, grep the repo for imports of
B before considering the merge done — an unimported `.tsx`/`.go` file left behind will keep its
own dependencies (config fields, API params, feature flags) looking falsely alive to anyone tracing
usage only as far as "is this threaded through the type/schema," rather than "does any mounted
component actually read it at runtime."

---

## A TTL was sized against a remembered constant instead of the constant's actual current value (found 2026-09-23)

While re-deriving `debugcred.TTL` for a bounded debug-run lifetime
(`docs/_archive/APPFLOW_RUNTIME_PARAMS_PLAN.md`), the previous round's 30-minute TTL turned out to have been
sized against an assumption that `appFlowActivityTimeout` (`go/internal/appflow/workflow.go`) was
120 seconds. Re-reading the actual constant mid-implementation this round found it is
`10 * time.Minute` — a 5x discrepancy. The true worst case for even one node exhausting all retries
is ≈20.2 minutes (`appFlowActivityTimeout` × `retryMax` + backoff), not the ~4 minutes the old
30-minute number implicitly assumed it covered with margin. Nothing tested this — there was no
assertion anywhere pinning `debugcred.TTL`'s value against `appFlowActivityTimeout`'s actual value,
so the wrong assumption shipped silently.

**Why it wasn't caught earlier:** the 120s figure was carried forward across several rounds of the
same multi-round review cycle as a remembered fact rather than re-verified against the source each
time it was used in a new calculation — easy to do when a constant lives in a different package
than the one being edited, and the number "sounds right" for an LLM call timeout.

**Fix:** re-read `go/internal/appflow/workflow.go` directly instead of trusting the carried-forward
number; introduced `appflow.DebugRunMaxLifetime` as the single source of truth for the debug-run
wall-clock ceiling (documented derivation from the *actual* `appFlowActivityTimeout` value), wired
it into `Lifecycle.StartAppFlow`'s `WorkflowRunTimeout` (previously unset for debug runs entirely),
and made `debugcred.TTL = appflow.DebugRunMaxLifetime + CleanupMargin` an explicit derivation rather
than an independent literal — plus a test (`TestTTL_DerivedFromDebugRunMaxLifetimePlusCleanupMargin`)
that pins the exact resulting value so a future edit to either constant can't silently desync them.

**Watch for:** before reusing any timeout/limit constant in a new calculation — especially across a
multi-round review session where the same numbers get carried forward in conversation — re-read the
constant from its source file rather than trusting a value stated earlier in the same session or a
prior round's summary.
component actually read it at runtime."

---

---

## Agent-node resolution being tied to publish time was a real design gap, not just an inconvenience (found 2026-09-24)

**Symptom:** `stage2-graph-llm-condition-v2`'s Debug button failed with `validate: [unresolved_agent]
agent node has no resolved agent_id` — already a documented "known limitation" in
`docs/_archive/APP_CANVAS_DEBUG_PLAN.md` (an agent-kind canvas node's real `agents.id` is only stamped into
the definition JSON's `_resolved_agent_ids` map by `PublishDefinition`, at publish time). The
existing framing treated this as an acceptable, if annoying, side effect of "debug runs the draft
directly, no publish required" being the Phase 5 design decision. The user pushed back hard on
that framing: requiring a real publish (which makes the canvas the actual live version real users
hit) just to test a draft is not an acceptable trade-off, regardless of how the limitation is
labeled in a doc.

**Root cause:** the only code path that ever resolved an agent's component-registry name/version
tuple into its real `agents.id` UUID was `PublishDefinition`'s own resolution loop
(`RegistryResolver.ResolveForPublish` + `AgentExists`, `internal/admin/service/publish.go`).
Nothing else in the codebase ever called that same lookup at any other time — so any code path
that runs a draft without going through publish first (debug being the only one that existed) had
no way to fill in an agent node's ID.

**Fix:** added `AppFlowDebugService.resolveDraftAgentIDs`
(`internal/admin/service/appflow_debug.go`), which calls the *exact same* `RegistryResolver.
ResolveForPublish` + `AgentExists` pair `PublishDefinition` already uses, just invoked at
debug-start time for any agent-kind component the publish-time stamp didn't already cover. Same
server-side, tamper-proof lookup (the client never supplies the ID) — just triggered at a second
point in time, not a new or weaker guarantee.

**Watch for:** when a limitation is framed as "known and documented," that's not the same as
"acceptable" — a doc entry recording a gap is not a decision that the gap should stay. If a
workaround for a known limitation would require a genuinely risky or confusing action from the
user (here: publish something not meant to go live yet, just to test it), that is itself a signal
the underlying gap needs a real fix, not just better documentation of the workaround. Also: when a
one-time resolution step (like a name→ID lookup) is meaningful in more than one workflow (publish
AND debug), write it so any caller can invoke it — encoding "call this on your way through publish"
as literally the only way to run it is a design smell even if it works for the one caller that
exists today.

---

---

## LLM/agent activities computed the real output, then threw it away when tracing (found 2026-09-24)

**Symptom:** After the agent-resolution fix above unblocked debugging
`stage2-graph-llm-condition-v2`, the live debug run completed successfully — but the App Canvas
Debug Mode inspector showed, for the LLM node: "Sentiment Classifier · llm · llm_1 · Done · No
output captured for this node yet." State was correctly `done`; the actual output was just never
there to show.

**Root cause:** `InlineLLMActivity` and `InvokeAgentActivity`
(`go/internal/appflow/activities.go`) both call `a.emitTrace(ctx, ..., "node_done", "", ...)` —
the detail argument is a **hardcoded empty string**, even though both activities already computed
the real response (`responseText`/`text`) a few lines earlier and use it for other purposes right
after (token streaming, the activity's own return value). Every other node kind that reports a
real result — router (`"label="+l`), condition, fork/join — already puts that result into
`detail`. LLM and agent were the only two kinds silently discarding theirs. A code comment on the
original tests (`workflow_test.go`, AF-TR-01) even documented this as deliberate: "Phase 2 keeps
LLM node detail empty — no prompt/response content is captured at this stage" — a real decision,
just one that turned out to defeat the actual purpose of a debug inspector (showing you what a
node produced).

**Fix:** pass `responseText`/`text` instead of `""` on both `node_done` calls. No new plumbing —
`emitTrace`'s `detail` parameter, the Redis Stream payload, the `them.run_steps.output` column,
and the frontend's `AppFlowDebugInspector.tsx` rendering path all already existed and already
worked correctly for other node kinds; LLM/agent just weren't using them. The existing
`verbosity` gate (`"off"`/`"status"`/`"full"`) still applies unchanged — this fix doesn't bypass or
weaken that control, it just stops throwing away data before the gate even sees it.

**Watch for:** when an activity computes a value it's clearly proud of (uses it for streaming,
returns it as the actual output), grep whether every other sibling call in the same file that
reports a similar result is passing something non-trivial to the same parameter — a lone `""`
where every neighbor passes real data is a strong signal something got dropped, not that this one
case is special. Also: a comment stating "we deliberately don't capture X here" is worth
re-examining the first time a feature (like a debug inspector) is actually built around needing X
— a decision that made sense in isolation can become the wrong one once its consumer exists.

---

---

## A canvas node type was missing from a decoration function's allowlist since it was first written (found 2026-09-24)

**Symptom:** continuing the same live App Canvas Debug Mode walkthrough (right after the previous
two fixes), the user stepped through a real debug run and reported "no response from agent in
debug" — an agent-kind canvas node showed absolutely no visual change at all: no border color, no
glow, no output, nothing, even though the backend had genuinely executed it (confirmed via
`them.run_steps`: real output, correct timing, `status: completed`).

**Root cause:** `useAppFlowDebugSession.ts`'s `decorateNodes` — the function that attaches live
`_debug` state onto canvas nodes for `CanvasNodes.tsx` to render — filtered on `n.type !==
'inline' && n.type !== 'flowControl'`, silently skipping everything else. Agent-kind nodes use
`type === 'agent'`, a third node type that was simply never added to that allowlist, going all the
way back to when this function was first written (Phase 5). On top of that, `AgentNode` itself
had no rendering branch for `_debug` at all — even if the data had reached it, there was nothing
to draw. Two bugs stacked in the same direction (data never attached, and no renderer to use it if
it had been), so the node looked completely inert regardless of whether the run reached it.

**Why it went unnoticed until now:** every one of this plan's — and the parent App Canvas Debug
Mode plan's — prior phases explicitly recorded "not live-verified, no browser-automation tool
available" as a standing limitation. This was the first real interactive click-through session
either plan ever got. A positive/negative allowlist check (`type !== X && type !== Y`) silently
drops any future type C without ever raising an error — it just quietly does nothing for it,
which is exactly the shape of bug that only surfaces the first time someone actually looks at type
C's behavior with their own eyes.

**Fix:** added `'agent'` to `decorateNodes`'s allowlist; added the same border/glow/state-label
overlay `FlowControlNode`/`InlineNode` already had to `AgentNode` (`CanvasNodes.tsx`) — reusing
the existing `debugAccent`/`debugGlow` maps rather than inventing new styling.

**Watch for:** an explicit allowlist of type strings (`type === 'a' || type === 'b'`) is a good
pattern for "only these specific kinds get this treatment" — but it is only as complete as
whoever wrote it remembered every kind that existed *at the time*. When a new node/component type
is added to a canvas system later, grep every existing allowlist-style check across the type's
sibling handling (decoration, validation, serialization) rather than assuming "it'll just fall
through safely" — a silent no-op is much harder to notice than an error would have been.

---

---

## AgentNode has no outgoing connection point — agents were only ever designed as canvas leaves (found 2026-09-24)

**Symptom:** extended `stage2-graph-llm-condition-v2` with fork/join on the "false" branch,
wired as `agent_false → fork_1 → ...` (chaining fork after the existing terminal agent node). The
backend ran this correctly end-to-end — verified live, real output at every node — but the user
reported the canvas rendered it as two disconnected trees: the original
`websocket → llm → condition → agents` chain, and a separate floating
`fork → branch A / branch B → join` island with no visible wire connecting them.

**Root cause:** `AgentNode` (`frontend/src/app/admin/applications/components/CanvasNodes.tsx`)
renders only a `target` `Handle` — no `source` `Handle` at all. `FlowControlNode` and `InlineNode`
both render target *and* source handles; `AgentNode` never got the source one, because every
existing use of an agent node in this canvas was as a genuine dead end (an orchestrator's tool
target, or — until this session — the last node on a branch). React Flow needs a source handle
on the *originating* node to anchor an edge visually; with none present, the edge data existed
and executed correctly, but had nothing to visually attach to on the `agent_false` end.

**Not a data bug, not a backend bug** — `InvokeAgentActivity`'s output flows into `accumulated`
exactly like any other node kind's; the workflow engine has no rule against continuing past an
agent node. This is purely a frontend rendering gap: agent nodes can act as a mid-flow step today
(the backend already supports it), the canvas editor just never gave them the handle to draw that
wire.

**Fix applied this session:** worked around it, did not fix the underlying gap — rewired so the
new fork/join sits between `cond_1` (source-capable) and `agent_false` (target-only, as
designed): `cond_1 --false--> fork_1 → ... → join_1 → agent_false`. User explicitly chose the
workaround over fixing `AgentNode` itself in this pass, to keep the scope to "extend the test
app," not "fix a canvas rendering gap." The real gap — agent nodes can't be a source of an
outgoing edge in the UI, even though the runtime has no such restriction — is unfixed, tracked
here for a future session.

**Fixed for real, 2026-09-27**: hit again while building a PII Guard demo app (`agent_intake` →
`cond_pii_check`, an agent node genuinely used mid-flow) — same symptom, same root cause. This
time fixed properly instead of worked around: `AgentNode` (`CanvasNodes.tsx`) gained a `sourcePos`
derivation and a `<Handle type="source" .../>`, copied verbatim from `MiddlewareNode`'s existing
working pattern (both target and source handles, direction-aware via `useAppLayoutDir()`). No
backend change needed — the runtime never had this restriction, confirmed again. `tsc --noEmit`
and all 79 existing frontend tests (including `validateConnection.test.js`, whose fixture already
listed `agent: { emits: ['result'] }` — the validation *logic* layer already allowed this, only
the visual `Handle` was missing) pass unchanged.

**Watch for:** when a node type's canvas component was written before that node type was ever
used in a particular position (source vs. leaf), don't assume the missing capability is a design
decision — check whether the *runtime* actually enforces that restriction (`go/internal/appflow`
in this case: it doesn't) before treating "the UI won't let me draw this wire" as authoritative.
A UI gap and a real architectural constraint look identical from the outside until you check the
execution engine directly.

---
