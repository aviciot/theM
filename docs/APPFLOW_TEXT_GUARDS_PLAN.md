# AppFlow Text Guards Plan (PII Redaction + Prompt-Injection Detection)
# Status: ALL 4 PHASES DONE. Verified live end-to-end via a real debug run
# (input-phase block). Not yet live-BROWSER-verified (no browser-automation
# tool in this environment, same standing limitation as every other
# frontend phase this session) — recommend a real click-through: select an
# llm node, confirm the Guards section shows PII Guard + Prompt-Injection
# Guard (no File Guard), toggle one on, save, reload, confirm it persists.
# Owner: platform
# Last updated: 2026-09-27

---

## Goal

Add real PII redaction and prompt-injection detection for text produced by
an `llm` node or an `agent` node in App Canvas, using the existing
`them-middleware-worker` container (the same one that already runs the
File Guard antivirus scan) — no new service.

This closes a real, pre-existing gap: `PIIRedactConfig`/`PromptInjectConfig`
(`go/internal/middleware/config.go`) and their pipeline-ordering support
have existed since File Guard's original design, but **no actual detection
logic was ever implemented for them** (confirmed 2026-09-27: only `av_scan`
is registered in `cmd/middleware-worker/main.go`'s `Registry`). Confirmed
by reading `go/internal/middleware/processor.go`: the `Part{Kind: "text",
Text: "..."}` shape and `Result{Modified: *Part}` (for in-place redaction)
were already designed to support this — this plan implements against an
existing, well-thought-out shape, it does not need to invent one.

---

## Where this attaches (confirmed against the File Guard precedent)

- **Not a draggable canvas node.** Same design decision File Guard already
  made and the user confirmed again this session: it's a "Guards" config
  section attached to an existing node (like File Guard's section on an
  `agent` node), not a new node type in the flow graph.
- **Applies to both `llm` and `agent` node kinds.** Reuses the same Guards
  UI pattern (`AgentGuardsSection.tsx`) generalized to a second node kind,
  not rebuilt per node kind.
- **Direction is a config choice, not hardcoded.** Confirmed with the user
  2026-09-27: rather than picking one direction, each guard gets a
  `direction` config field — `"output"` (scan what the LLM/agent
  produced, same direction File Guard already works in — the default) or
  `"both"` (also scan the incoming user message / prompt before it's sent,
  a second, separate scan call). `"input"`-only is not a needed third
  option (if you're paying the cost to scan input, you always want output
  scanned too) — 2 real choices, not 3. This is a NEW field on
  `PIIRedactConfig`/`PromptInjectConfig` (neither has one today — check at
  implementation time and add it, don't assume it exists).
  Input-side scanning, when enabled, runs BEFORE the LLM/agent call
  (blocking a malicious prompt from ever reaching the model); output-side
  runs after, same as File Guard's existing timing.
- **A2A is unaffected.** This scans the agent's response TEXT inside our
  own workflow code, entirely after the A2A call already returned — no A2A
  wire-format change, no new part kind, nothing added to the protocol.
  Exactly the same non-invasive relationship File Guard has to A2A today.

---

## A real architectural difference from File Guard — must not copy its async path verbatim

`FileGate.Intercept`/`InterceptInline` (`go/internal/middleware/gate.go`)
quarantines bytes to MinIO, inserts a `quarantine_artifacts` row, and
**enqueues an async job** — the caller gets back `ScanStatus: "pending"`
immediately, and the real clean/infected verdict lands later via
`them-middleware-worker`'s poll loop. That is correct for a file: the
canvas run doesn't need to change its own text based on the file's
contents, so "verdict arrives later" is an acceptable design.

**Text redaction cannot work that way.** If a `pii_redact` guard is
supposed to strip an email address out of the LLM's own response before
that response is used by the next node (or returned to the end user), the
redaction must happen **synchronously, inline**, before the workflow
continues — not queued for a worker to get to eventually. By the time an
async job result arrived, the unredacted text would already have been
used.

**Design decision for this plan**: text guards run **synchronously inline**
inside the calling activity (`InvokeAgentActivity` / the LLM node's
activity) — not via `middleware_jobs`/`quarantine_artifacts` at all. This
means:
- A new, small synchronous entry point is needed — NOT `FileGate`, a
  parallel one (e.g. `TextGate.Check(ctx, in, text) (TextGateResult, err)`)
  that calls the same `Processor.Process` interface `av_scan` already
  implements, but resolves + returns the result immediately, in-process,
  no queue.
- `pii_redact`/`prompt_inject` processors run **inside the same binary that
  calls them** (dag-worker, via the activity), not inside
  `them-middleware-worker` at all, despite this doc's opening line saying
  "same container that runs File Guard" — that statement is about REUSING
  the processor plugin architecture (`middleware.Processor` interface,
  `Registry`), not about routing through the same async worker process.
  **This is a real, deliberate correction to how this plan opened above —
  flagging it explicitly rather than leaving a contradiction unresolved.**
- `av_scan` stays exactly as-is (async, file-only, via
  `them-middleware-worker`) — this plan does not change that path.

---

## Phasing (not started)

### Phase 1 — synchronous TextGate + processor interface reuse — DONE (2026-09-27)

**Implemented as specced, with one real deviation found during
implementation**: config resolution was NOT reused from
`FileGate.resolveSecCfg` as originally planned. `FileGate`'s resolver
parses `middleware_defs.config` as a nested `SecurityConfig{Processors:
map[string]json.RawMessage}` shape — that's how `file-guard`'s own config
happens to be stored, but `pii_redact`/`prompt_inject`'s config is a flat
processor-config object instead (`{"enabled":false,"mode":"redact",...}`,
matching `PIIRedactConfig`/`PromptInjectConfig`'s own flat struct shape
directly — see Phase 2's DB migration). Reusing the nested-shape resolver
against flat data would have silently returned an empty config (no
`"processors"` key to find), not an error — a real, silent
misconfiguration bug, not just a style mismatch. Confirmed with the user
2026-09-27 to write a small dedicated resolver
(`TextGate.loadTextWiringCfg`) instead of forcing the data into the nested
shape or the code into parsing two shapes generically.

`FileGate`'s own `resolveSecCfg`/`loadWiringCfg`/`loadSecCfg` WERE
generalized (parameterized by `defSlug` instead of hardcoding
`"file-guard"`) as `resolveSecCfgForDef`/`loadWiringCfgForDef`/`loadSecCfg`
in `gate.go` — zero behavior change for File Guard itself (its own methods
became thin wrappers), done so the *precedence logic* (node_id → agent
slug → app-level fallback) has one implementation, even though TextGate
ultimately needed its own separate config-shape handling on top.

`internal/middleware/pipeline.go`'s `Pipeline.Run`/`PipelineResult` (the
existing, already-generic processor-chaining engine `av_scan`'s async path
already uses) reused as-is, with one additive field:
`PipelineResult.FinalPart Part` — the redacted text a synchronous caller
needs back, which `av_scan`'s async caller never needed (it only checks
`FinalStatus`). `TextGate.Check` runs `pii_redact` then `prompt_inject` in
order, threading `PipelineResult.FinalPart` forward between them so a
prompt-injection check sees PII-redacted text, not the original.

6 new tests, `internal/middleware/textgate_test.go` (`middleware_test`
package, reusing `gate_test.go`'s existing `fakeWiringRows`/`fakeRow`/
`fakeRows` fixtures).

### Phase 2 — the two processors themselves — DONE (2026-09-27)

**Implemented as specced.** `internal/middleware/pii/pii.go`: regex-based
MVP — email, phone (3-3-4 grouped digits), credit-card-like 13-19 digit
sequences (no Luhn check — over-redacting a non-card-but-card-shaped number
is the safer failure mode for a redaction guard, not a bug), SSN-like
`###-##-####`. `mode: "redact"` masks matches in place via `Result.Modified`;
`"block"` sets `Result.Block`; `"warn"` flags without changing anything.
`LLMAssist` field exists on `PIIRedactConfig` but is NOT implemented — per
plan, left for a future phase.

`internal/middleware/promptguard/promptguard.go`: heuristic phrase-matching
MVP, each pattern tagged with a minimum `Sensitivity` level ("low" =
unambiguous override attempts always checked; "medium" adds role-override
attempts; "high" adds delimiter-escape patterns, which are more prone to
false positives on legitimate technical text). `mode: "block"`/`"warn"`
only — no `"redact"` (an injection attempt isn't a value to mask). Not an
LLM-judge call, per plan.

Both register into a `*middleware.Registry` — same type `av_scan` already
uses, but this registry instance will be constructed and used **inside
dag-worker** (per Phase 1's design), not inside
`cmd/middleware-worker/main.go`; that wiring is Phase 3's job, not done yet
as of this update.

**Real DB schema gap found and resolved, confirmed with the user
2026-09-27**: `middleware_defs` had exactly one pre-existing row for this
concern, `guard_default`, bundling BOTH pii and prompt-injection under one
combined config (`{"checks": ["pii", "prompt_injection"], ...}`) — a
different, older shape than every other guard (`av_scan`/`file-guard`,
1-def-per-processor). Confirmed with the user: split into 2 new
`middleware_defs` rows (`pii_redact`, `prompt_inject`), matching the
established pattern, rather than keep or extend the combined row.
`guard_default` is disabled (not deleted — a middleware_wirings row could
still reference it by FK) with a description pointing at its replacements.
`db/113_pii_prompt_guard_defs.sql`: each new def also needed a matching
`component_definitions` row under the same UUID first — `middleware_defs.id`
has a real FK into `component_definitions(id)` (`fk_mw_defs_base_def`),
confirmed live via `file-guard`'s own row existing in both tables — a gap
not mentioned in this plan's original draft, found by actually running the
migration and reading the FK violation error.

13 new tests: `internal/middleware/pii/pii_test.go` (7),
`internal/middleware/promptguard/promptguard_test.go` (9) — 2 real bugs in
the regex patterns themselves were caught and fixed by these tests before
they ever shipped (an RE2-unfriendly phone regex that silently never
matched; a role-override test whose sample text didn't actually contain the
article the pattern required) — exactly the kind of bug a test suite is
for, not a false alarm.

Combined Phase 1+2: 22 new tests (`S1-172`), `go build`/`go vet`/`go test
./...` all clean.

### Phase 3 — wire into AppFlow's `llm` and `agent` cases — DONE (2026-09-27)

**Implemented as specced.** New `AppFlowTextGateActivityName` constant +
`AppFlowActivities.TextGate`/`TextGateActivity` (mirrors
`FileGate`/`FileGateActivity`'s exact nil-safe pattern — a nil `TextGate`
means text passes through completely unguarded, same as a nil `FileGate`
never scanning a file). `workflow.go`'s `case "llm"` calls
`TextGateActivity` right after the LLM activity returns, BEFORE the text is
written into `vars[outVar]`/`accumulated` — a downstream node never sees
unredacted text. `case "agent"` calls it right after `InvokeAgentActivity`
returns and `accumulated` is set, running alongside (not instead of) the
existing File Guard file-part check below it — an agent response can carry
BOTH a text reply worth guarding AND a file part in the same response (the
a2a-stream multi-artifact case already proven earlier this session shows
both can coexist).

A blocked result (`TextGateCheckOutput.Blocked`) fails the run
non-retryably via `temporalerr.NewNonRetryableApplicationError`, same
pattern `case "condition"`'s error paths already use — includes
`Categories` in the error message (e.g. "prompt_inject:flagged") so the
failure is diagnosable, not a bare "blocked." A redacted (not blocked)
result replaces `agentOut.ResponseText`/`llmOut.ResponseText` in place
before it's written to `accumulated`/`vars`, so the guard is invisible to
every node downstream — they just see already-safe text.

5 new tests: 3 activity-level (`workflow_test.go`, AF-WF-21/22/23, same
`fakeFileGate`-style pattern as File Guard's own activity tests) plus 2
real end-to-end tests via the actual Temporal test-environment
(`workflow_temporal_test.go`'s `AppFlowTraceWorkflowTestSuite`, AF-TR-W10/
W11) — `TestLLMNode_TextGateBlocks_FailsWorkflowNonRetryably` proves a
block genuinely fails the whole workflow (via a single-node spec with
nothing downstream to silently swallow it), and
`TestAgentNode_TextGateRedacts_ReplacesAccumulatedText` proves the redacted
text actually lands in `AppFlowWorkflowOutput.FinalText`, not just the
activity's own return value.

`cmd/dag-worker/main.go`: `pii`/`promptguard` processors registered into a
new `*middleware.Registry` constructed and used inside dag-worker itself
(per Phase 1's design decision — NOT inside `cmd/middleware-worker`, which
only ever runs `av_scan`'s async file path); new `appFlowTextGateAdapter`
bridging `middleware.TextGate` to `appflow.TextGateChecker`, same bridging
pattern `appFlowFileGateAdapter` already uses for File Guard.

**Real deployment gotcha hit again, same lesson as earlier this session**:
all 3 dag-worker images (`them-dag-worker`, `-2`, `-debug`) had to be
rebuilt and restarted explicitly by name — see `docs/LESSONS.md`'s
2026-09-27 entry (3 separate compose services/image tags share one
Dockerfile; rebuilding only `them-dag-worker` silently leaves the other
two, including the debug-mode one, on stale code).

`go build`/`go vet`/`go test ./...` all clean.

**Not yet tested live** — no live debug run has been done against a real
app with a text-guard wiring attached and enabled (the DB rows exist as of
Phase 2, but no UI exists yet to attach a wiring to an `llm`/`agent` node —
that's Phase 4's job). Unlike File Guard's equivalent gap, this one can't
be closed via the admin API the way `verify-step6-fileguard`'s File Guard
wiring was manually seeded, because the Guards UI component doesn't
generalize to `llm` nodes yet — Phase 4 needs to land, or a wiring needs to
be manually inserted via SQL, before this can be proven end-to-end with a
real run.

### Phase 4 — App Canvas UI — DONE (2026-09-27)

**Implemented as specced, plus 2 real schema/query bugs found and fixed
along the way (see `docs/LESSONS.md`'s 2026-09-27 entries for the full
detail) and the `direction` field's runtime wiring closed in the same
session it shipped (it existed as a DB/UI field with zero code reading it
until this phase — flagged and fixed, not left as a silent lie):**

- `middleware_wirings.agent_id` made nullable (`db/114`) — an `llm` node
  has no `agents` row at all. `ListMiddlewareWirings`/`GetMiddlewareWiring`
  switched to `LEFT JOIN agents`; `CreateMiddlewareWiring`'s upsert re-keyed
  from `uq_mw_wiring_app_agent_pos` to `uq_mw_wiring_app_node` (the real
  identity for both node kinds); admin handler's Create validation loosened
  from "agent_id required" to "agent_id OR node_id required."
- `direction` ("output"/"both") actually wired in: `TextGate.Check` gained
  a `Phase` param; `workflow.go`'s `case "agent"`/`case "llm"` now call
  `TextGateActivity` twice (input phase before the call, output phase
  after) — a guard's own `direction` config decides whether either call
  does anything.
- `AgentGuardsSection.tsx` generalized from one hardcoded file-guard form
  into a component rendering every applicable guard for a node's kind:
  `agent` gets file-guard + pii_redact + prompt_inject; `llm` gets
  pii_redact + prompt_inject only (no file-guard — an llm response can
  never carry a file, confirmed via
  `docs/APPFLOW_LLM_FILE_OUTPUT_PLAN.md`). Wired into `InlineNodePanel.tsx`.

**Verified live, end-to-end, against a real debug run** (not just unit
tests): a `pii_redact` wiring on an `llm` node with `mode=block,
direction=both` correctly failed the run with
`text guard blocked input (pii_redact:flagged)` **before the LLM node ever
executed** — confirmed via `them.runs.error` and the fact that
`run_steps` had zero rows for that node (traceNode's own `node_start` never
even got a chance to fire, since the block happens before that node's
activities begin).

9 new tests across `internal/admin/dal` (4, real Postgres integration
tests), `internal/admin` (1 net-new + 1 renamed), `internal/middleware` (3),
`internal/appflow` (1, real Temporal test-environment). `go build`/`go
vet`/`go test ./...` all clean; frontend `tsc --noEmit` clean, all 79
existing JS tests pass.

**Original spec text below, kept for the "why" — the mode/redact-vs-block
design reasoning still applies as originally written:**
- Generalize `AgentGuardsSection.tsx` to also render for `llm` nodes (today
  it's agent-node-specific — `agentSlug`/`Agent[]` lookup logic needs to
  become conditional on node kind, not agent-only).
- **Mode field, confirmed with the user 2026-09-27**: each guard gets a
  `mode` dropdown, same `options`-array pattern as File Guard's `mode` fix
  this session (`ConfigFieldDoc.Options`, real `<select>`, not free text)
  — NOT the simpler on/off `BlockOnDetect` checkbox alone. PII gets
  `["block", "redact", "warn"]` (redact = strip the matched text in place,
  keep the rest); prompt-injection gets `["block", "warn"]` (nothing
  meaningful to redact — an injection attempt isn't a value to mask, it's
  a decision to accept or reject the whole text). `BlockOnDetect bool`
  becomes redundant once `mode` exists — remove it from both config
  structs rather than keep two ways to say the same thing (same
  "duplicate enabled checkbox" lesson from this session's Guards UX
  review — don't reintroduce that pattern here).
- **Direction field, confirmed with the user 2026-09-27**: `direction`
  dropdown, `["output", "both"]` (see above) — default `"output"`.

---

## Explicitly out of scope (flag before ever expanding into these)

- LLM-judge-based prompt-injection detection (`PromptInjectConfig` has no
  field for this yet; regex/heuristic only, this phase).
- `PIIRedactConfig.LLMAssist` — field exists, not implemented, not in this
  phase's scope.
- Any change to `av_scan`'s existing async file-scanning path — untouched
  by this plan.
- Any change to the A2A wire protocol — this scans text entirely inside
  our own workflow code, after an A2A call already returned.

---

## Estimate

Smaller than the LLM file-output plan (`docs/APPFLOW_LLM_FILE_OUTPUT_PLAN.md`)
because the plugin architecture, config shapes, and pipeline ordering
already exist and are well-designed — this is "write 2 processors + 1 new
synchronous gate + wire 2 call sites + extend 1 UI component," not
"invent new types across 4 files." Rough shape: a few days for Phases 1-3
(backend), a bit more for Phase 4 (UI + the mode/UX decision flagged
above) — but confirm Phase 4's exact UX decision with the user before
starting Phase 4's implementation, since it's a real open question, not
just polish.
