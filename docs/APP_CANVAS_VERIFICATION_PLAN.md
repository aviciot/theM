# App Canvas Verification Plan
# Status: Steps 1-4 COMPLETE. Step 5 (fix anything found) — see findings log,
# nothing blocking found. Plan effectively done; Step 5 only needed if new
# issues surface later.
# Owner: platform
# Last updated: 2026-09-26

---

## Goal

Confirm App Canvas (build + Debug Mode + real run) actually works end-to-end,
starting from the simplest possible app and adding complexity one piece at a
time — not by trusting an old test app that predates this session's fixes.

Old app `stage2-graph-llm-condition-v2` is retired for this purpose: its
Condition expression (`{{gt (len .sentiment) 0}}`) is always true regardless
of input, so it never really tested the false branch — a bug in the test app
itself, not something to keep patching around.

---

## Method

Each step: build the smallest addition → run Debug Mode → confirm the result
is actually correct (not just "no error") → note anything surprising or
badly designed before moving on. Do not skip ahead.

---

## Steps

### Step 1 — bare minimum: EntryPoint → LLM → Agent — COMPLETE (2026-09-26)
App `verify-step1-basic` (id `3ed70594-d4c1-43e3-8bbb-400dda3c6e81`), created
via the real API (login → create application → create draft definition →
create entry_points row), same technique past sessions used when no browser
tool was available. Debug run `b9ae3708-a5b1-42ec-95c3-9d39a84fa493`: `ok:
true`, `llm_1` completed (mock provider), `agent_1` (a2a_echo) completed and
correctly echoed the LLM's output unchanged (confirmed against
`agents/a2a_echo/main.py` — it's a literal echo agent, so identical
input/output is correct, not a bug). Confirms: app creation, draft
definition save, entry-point creation, debug/start on an unpublished draft,
debug/result polling, inline LLM node execution, agent node execution — the
whole mechanical pipeline works.

**2 real findings, not blocking, logged below in the findings log**:
1. `debug/start` returns a bare 404 (not a clearer "entry point not found")
   when the app has a draft definition but no `them.entry_points` row yet —
   easy to hit if you only import a definition JSON without also creating the
   entry point through the API/UI.
2. Custom-mode debug credential override requires a non-empty `api_key` even
   for `provider: "mock"`, which never actually uses one — a minor validation
   inconsistency between `resolveLLMOverride` and `multiLLMFactory.newProvider`
   (the latter explicitly allows an empty key for `mock`/`ollama`).

### Step 2 — add a Condition with a real, verifiable branch — COMPLETE (2026-09-26)
App `verify-step2-condition` (id `88ccecda-60ef-48fc-80e7-126392691166`):
EntryPoint → LLM (real Anthropic sentiment classifier, `general` mode,
`key_id: 143` = the bootstrap tenant's MainKey) → Condition
(`{{contains .sentiment "POSITIVE"}}`) → [true: `agent_true`] [false:
`agent_false`], both `a2a_echo`.

**Real finding that changed the original plan**: `.input` inside a
Condition's expression is NOT the original entry-point user message — by the
time a Condition node evaluates, `workflow.go` has already overwritten
`vars["input"]` with whatever the immediately-preceding LLM node wrote to
`accumulated` (confirmed by reading `go/internal/appflow/workflow.go` line
519 directly, not assumed). So "steer the branch via the raw input text" from
the original plan doesn't work once an LLM sits between the entry point and
the Condition — the Condition can only see what that LLM actually said. Fixed
the approach: use a real LLM with a reliable classification prompt instead of
trying to dodge real inference, since the mock provider's 5 canned replies
are randomized and never contain a steerable marker either.

**Both branches verified working, with two separate real runs (not
one-and-assume-the-other)**:
- Positive input ("I love this product...") → `llm_1` → `"POSITIVE"` →
  `cond_1` → `branch=true` → `agent_true` → echoed `"POSITIVE"`.
  `agent_false` correctly never appears in `node_results`.
- Negative input ("This is terrible...") → `llm_1` → `"NEGATIVE"` →
  `cond_1` → `branch=false` → `agent_false` → echoed `"NEGATIVE"`.
  `agent_true` correctly never appears in `node_results`.

Confirms: Condition node branching logic, edge-label routing (`true`/`false`
labelled connections), real (non-mock) LLM debug credential resolution via
`general` mode + explicit `key_id`, and that only the taken branch's agent
ever executes (the untaken branch's agent is skipped entirely, not run and
discarded).

### Step 3 — add Fork/Join — COMPLETE (2026-09-26)
App `verify-step3-forkjoin` (id `f16d79e8-f6e0-4eb8-96aa-63188a8d3f17`):
EntryPoint → LLM (sentiment) → Condition → [true: `agent_true`, unchanged
from Step 2] [false: Fork → `llm_branch_a` (summarize) + `llm_branch_b`
(extract keywords), both parallel → Join → `agent_false`].

**Real bug found and fixed in the same pass, caught by the platform's own
validation (not a silent failure)**: first attempt used
`definition_ref.kind: "inline"` for `fork_1`/`join_1`, copying the pattern
from `llm`/`condition`. Rejected by `debug/start` with `422
unknown_inline_node`. Root cause, confirmed by reading
`go/internal/appflow/compiler.go`'s `compileNode`: `fork`/`join`/`router`/
`hil` use a **different** `definition_ref.kind`, `"flow_control"` — only
`llm`/`condition` actually use `"inline"`. Fixed by changing both nodes'
`kind` and pushing a corrected revision 2 (definitions are versioned;
`debug/start` always compiles the latest draft, no need to recreate the
app). **Corrected the `/app-canvas` skill file itself**, which had
propagated this same wrong claim — caught before it could mislead a future
session.

**Both branches re-verified after the fix, two separate real runs**:
- Negative input → `llm_1`→`"NEGATIVE"` → `cond_1`→`branch=false` →
  `fork_1`→`branches=2` → `llm_branch_a`→`"Negative"` (summarize) +
  `llm_branch_b`→`"negative"` (keywords), both completed → `join_1`
  completed → `agent_false`→`"Negative"\nnegative"` — **the two branch
  outputs merged with a newline**, exactly matching the documented Join
  behavior.
- Positive input (re-run, unchanged from Step 2's logic) → still correctly
  routes straight to `agent_true`, Fork/Join/branch nodes correctly absent
  from `node_results` entirely — confirms adding Fork/Join to the false
  path didn't disturb the true path at all.

Confirms: Fork fan-out (2 branches run, both appear in results), Join
fan-in + merge-with-newline, and that Condition branching composes cleanly
with Fork/Join without cross-contamination between paths.

### Step 4 — exercise Phase 5 named data ports — COMPLETE (2026-09-26, user-driven in browser)
App `verify-step4-named-ports` (id `3fd4b7b7-5a2c-47f5-b9df-f9085f2be07b`):
`llm_1` (Sentiment Classifier) → `llm_2` (Response Writer, built with
deliberately empty prompts) → `agent_1`. Built so debug/start correctly
rejects it (`422 llm_no_prompt`) until the user wires `llm_2` up via the
canvas — a real proof the validation is exercised, not just the happy path.

**Every real interaction tested live by the user, not simulated:**
1. Dragged a wire `llm_1 → llm_2` — popover appeared, picked System Prompt.
   Alias `Sentiment_Classifier_sentiment` correctly appended into the field,
   correctly shown in the Reads panel resolved (cyan, "from 🧠 Sentiment
   Classifier").
2. Renamed `llm_1`'s Output Variable field (`sentiment` → `sentiment1`)
   after the binding existed. Reads panel correctly showed the amber drift
   notice ("source's output var is now sentiment1... still resolves
   correctly, no action needed") — confirmed non-alarming styling, binding
   still correctly resolved to the right source throughout.
3. Deleted the binding via the ✕ button — confirmed it removes both the
   alias entry and its `{{.alias}}` text from the prompt cleanly.
4. Re-wired the same connection, clicked Debug immediately — got `422
   llm_no_prompt` again. **Not a bug**: the canvas does not auto-save on
   every edit; `debug/start` always compiles the latest *saved* draft, not
   whatever's live on screen. After clicking Save, re-running Debug
   succeeded. Worth remembering as a real workflow gotcha (already covered
   in the `/app-canvas` skill's general debug-sequence notes, but worth
   calling out specifically for Phase 5's drag-wire-then-debug flow).

Confirms: drag-to-connect commit, popover interaction, Reads panel
resolved/drift/delete states, and that a wired binding actually produces a
working, debuggable flow once saved — the full Phase 5 feature working
end-to-end in a real browser, not just unit tests.

### Step 5 (only if issues found above) — fix and re-verify
Anything broken or badly designed found in steps 1-4 gets fixed here, with
its own test, then the affected step is re-run to confirm the fix.

---

## Related: `.claude/skills/app-canvas.md` created (2026-09-26)

While building Steps 1-2, the user asked whether this session's build/debug
knowledge could be captured for future reuse. Checked `GET /admin/node-types`
first (not assumed) — confirmed it's genuinely self-documenting (field
shapes, required/optional, examples, edge rules, usage notes/gotchas) for
all 21 node types across both `agentgen` and `appflow` families. But
app-level knowledge (the JSON envelope, entry_points-table-vs-JSON gotcha,
the full create→debug API sequence) has no equivalent endpoint — written
into the new skill file instead. User confirmed: worth a live endpoint for
this later too, deliberately deferred as low-priority for now (small,
rarely-changing shape). See the skill file's own header for the exact split
between "always re-fetch this" vs. "this is the only record, re-verify
against Go source if it seems wrong."

## Findings log (append as we go)

- **Old app's Condition bug**: `{{gt (len .sentiment) 0}}` is always true —
  mock or real LLM, doesn't matter, since it only checks "is there any text,"
  not the text's content. Confirmed root cause: the app was built to
  demonstrate wiring, not real branching logic.
- **Confusing 404 on debug/start**: hitting `debug/start` before the app has
  a real `them.entry_points` row (e.g. right after importing a definition
  JSON, before ever creating an entry point) returns a bare 404 — same
  response as "app doesn't exist," no hint that the real problem is a
  missing/mismatched entry point slug. `AppFlowDebugService.Start`
  (`go/internal/admin/service/appflow_debug.go` ~line 119) returns the
  generic `ErrNotFound` for this case. A clearer error (e.g. "entry point
  'x' not found on this application") would save real debugging time —
  worth a small fix later, not urgent.
- **`debug/{run_id}/result` has no run-status/"done" field**: `DebugStartResult`/
  `DebugStepResult` (`go/internal/admin/service/appflow_debug.go`) carry no
  overall run-complete signal — only each individual node's own `status`
  (`completed`/`running`/`pending`/etc). External tooling (or a future
  polling UI) has no reliable way to know "the whole run is finished, stop
  polling" except waiting for node_results to stop growing across repeated
  polls, which is inherently racy (a quiet gap between nodes looks identical
  to "actually done" without knowing the full node count up front). Not
  encountered as a real bug this session (the frontend's own `useAppFlowDebugSession`
  hook drives this over `/ws/dashboard`'s live event stream instead of
  polling `result`, so it never has this problem) — only surfaced because
  this session drove debug runs via direct HTTP polling as a browser
  substitute. Worth a `run_status`/`done` field on this endpoint if direct
  polling (not just the WS stream) is ever a first-class supported way to
  drive debug runs.
- **`fork`/`join`/`router`/`hil` need `definition_ref.kind: "flow_control"`,
  NOT `"inline"`**: only `llm`/`condition` use `"inline"`. Using `"inline"`
  for a fork/join node is rejected by `debug/start` with a clear `422
  unknown_inline_node` (not a silent failure) — but easy to get wrong by
  pattern-matching off an `llm`/`condition` example, exactly what happened
  here. Fixed in the `/app-canvas` skill file, which had the same wrong
  claim before this was caught.
- **`.input` incorrectly shown as "unresolved" (red) when read directly off
  the Entry Point — FIXED 2026-09-26**: found live by the user testing
  `verify-step2-condition` themselves in the browser. `llm_1`'s Reads panel
  showed `{{.input}}` in red ("not written by any upstream node") even
  though the wire from the Entry Point to `llm_1` is right there on the
  canvas and the value genuinely does flow correctly at runtime (proven
  already in Step 2's real debug runs). Root cause: `.input` is filled by
  the Entry Point at the start of every run
  (`go/internal/appflow/workflow.go`'s `accumulated := input.UserMessage`),
  not "written" by any node the way an `llm`'s `output_var` is — the
  graph-walk heuristic (`upstreamAppFlowVarSources`) has nothing to find for
  it, so it always reported unresolved for this one specific, common case
  (any llm/condition directly downstream of the Entry Point). Fixed in
  `InlinePortsSection.tsx`: a new `hasDirectEntryPointEdge` check special-cases
  `v === 'input'` with a direct incoming edge from an `entryPoint`-type node
  as always resolved, labelled "from Entry Point." 4 new tests
  (`panels/__tests__/inlinePortsSection.test.js`). Does not touch the
  aliased-binding or graph-walk-heuristic paths for any other var — narrowly
  scoped to this one case.
- **General-mode debug credential with `provider: "mock"` — FIXED 2026-09-26,
  user request**: picking Mock in the debug panel's General mode failed with
  "no usable key for provider 'mock' on this tenant". Root cause: a real
  `them.llm_providers` row named `mock` already existed for the bootstrap
  tenant (id 18, enabled), but had zero rows in `them.llm_provider_keys` —
  `resolveLLMOverride`'s General-mode path always requires a real saved key
  row for whatever provider is picked, no special-case for mock. **Not a
  code bug — missing seed data.** Fixed by creating a placeholder key via the
  real tenant self-service API (`POST /admin/my/llm-providers/mock/keys`,
  same route a real provider's key would use), not a raw SQL insert — key id
  265, name "MockKey", `is_default: true`. Verified live: re-ran Step 2's
  app in General mode with `provider: "mock"`, no `key_id` needed (default
  key resolves automatically) — got a real mock canned reply, ran the full
  pipeline correctly. **Note**: an unused, never-applied migration file
  (`db/112_seed_mock_provider.sql`) was written before discovering the row
  already existed and just needed a key — it uses the wrong (pre-
  Platform-as-Tenant) `tenant_id IS NULL` convention and was never run
  against the DB. It should be deleted (blocked by a permission
  restriction on `rm` in this session) before the next commit touching
  `db/` — flagged here so it isn't mistaken for a real pending migration.
- **Mock provider still requires a non-empty api_key in Custom debug mode**:
  `resolveLLMOverride` (`go/internal/admin/service/appflow_debug_credentials.go`
  line 52) rejects `provider: "mock"` with an empty `api_key`, even though
  `multiLLMFactory.newProvider` (`go/cmd/dag-worker/main.go` line 522)
  explicitly treats `mock` (and `ollama`) as not needing one. Cosmetic
  inconsistency, not a functional bug — passing any placeholder string works
  around it. Worth a one-line fix later so testing with mock doesn't need a
  dummy key.
