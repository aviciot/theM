# AppFlow A2A Response Kinds + File Guard Plan
# Status: Phase 1 COMPLETE. Phases 2-3 not yet started.
# Owner: platform
# Last updated: 2026-09-26

---

## Goal

App Canvas's `agent` node (a canvas-built A2A agent, called directly via
`pgxAgentA2ACaller.InvokeByID` in `go/cmd/dag-worker/main.go`) today only
ever reads the **text** content of an agent's A2A response. If the agent
returns anything else — a file, structured data, or raw bytes — it is
silently dropped with no error, no log, no trace event. This was found live
this session while investigating whether File Guard (virus-scanning file
artifacts) could be wired into App Canvas.

Two real gaps, found in order, each confirmed by reading the actual code
(not assumed):

1. **AppFlow doesn't recognize non-text A2A response parts at all.** The
   real A2A protocol has 4 part kinds (confirmed via the vendored SDK,
   `go/vendor/github.com/a2aproject/a2a-go/v2/a2a/core.go`): `Text`, `URL`
   (file), `Data` (structured), `Raw` (bytes). `pgxAgentA2ACaller`'s response
   parsing (`go/cmd/dag-worker/main.go` ~line 880) uses a hand-rolled
   anonymous struct with only a `Text string` field — a file/data/raw part
   in the real response silently decodes to an empty string, no error.
2. **File Guard has real, working detection logic (ClamAV scan, quarantine,
   `middleware_wirings` config) but is never called from AppFlow at all.**
   Its only real call site today (`go/internal/a2a/executor.go` line 193) is
   on the classic Orchestrator's event-bus path, a completely different
   execution model AppFlow doesn't share.

**PII/Prompt-Injection Guard is explicitly OUT of scope for this plan** —
confirmed by reading `go/internal/middleware/config.go`: `PIIRedactConfig`/
`PromptInjectConfig` are empty struct shapes with zero references anywhere
in `pipeline.go`/`gate.go`. No real detection logic exists for it at all —
building App Canvas UI for it now would let a user configure something with
zero runtime effect. That's a separate, bigger, not-yet-scoped feature
(writing the actual PII/prompt-injection detection logic), not part of this
plan.

**Confirmed with the user 2026-09-26**: the LLM node is NOT affected by any
of this — confirmed via `go/internal/llm/provider.go`'s `StreamEvent` type,
which only has 4 event kinds (`text_delta`, `tool_calls`, `stop`, `error`),
none of which carry a file. Anthropic/OpenAI calls in this codebase can only
ever produce text. This entire plan only concerns `agent`-kind (A2A) nodes.

---

## Current-state map (researched 2026-09-26)

**A2A protocol part kinds** (`go/vendor/github.com/a2aproject/a2a-go/v2/a2a/core.go`):
- `Part.Content` is one of `Text` / `URL` / `Data` / `Raw` (a real Go
  interface with 4 implementations, decoded correctly by the SDK's own
  `Part.UnmarshalJSON`, line 717).
- Accessor methods already exist and just need to be used:
  `Part.Text()`, `Part.URL()`, `Part.Data()`, `Part.Raw()`.
- `Task.Artifacts []*Artifact`, `Artifact.Parts []*Part` — the full response
  shape is already modeled by real SDK types; nothing needs to be invented.

**The old, working file-handling path** (`go/internal/a2a/executor.go`,
lines ~155-204) is a completely different execution model: the classic
Orchestrator publishes typed events (`"token"`, `"file"`, `"done"`) onto an
in-process event bus as a workflow runs, and `executor.go` just switches on
`ev.Type` — the "this is a file" classification happened upstream, before
this code ever runs. **AppFlow has no equivalent event bus for agent calls**
— `InvokeAgentActivity` makes one blocking HTTP call via
`pgxAgentA2ACaller.InvokeByID` and gets back a single already-collapsed
`string` (`AgentInvokeActivityOutput.ResponseText`). There is no reusable
plumbing here; the recognition has to be added at the point where the raw
JSON-RPC response is first decoded.

**File Guard's real, working logic** (`go/internal/middleware/gate.go`):
- `FileGate.Intercept(ctx, GateInput) (GateResult, error)` — takes a
  `DownloadURL`/`FileName`/`ContentType`/`AgentSlug` (for per-agent
  `middleware_wirings` config resolution) plus tenant/app/run/session IDs.
  Downloads the file, quarantines it in MinIO, inserts a
  `quarantine_artifacts` row, enqueues a `middleware_job` (async ClamAV
  scan). Returns an `ArtifactID` the caller can use instead of the raw URL.
- `resolveSecCfg`/`loadWiringCfg` (lines 246, 259): checks
  `middleware_wirings` for a per-agent config first, falls back to
  `them.applications.security_config` — this precedence already exists and
  needs no change.
- This logic is untouched by this plan — fully reusable as-is.

**`middleware_wirings` table** (`go/internal/admin/dal/middleware_wirings.go`):
already has `agent_id`, `def_id` (FK to `middleware_defs`), `node_id`
(optional, for per-canvas-instance scoping), `config_override`, `enabled`,
`position`. Already supports exactly what's needed — nothing new required
in this table.

**`middleware_defs` row for File Guard** (`slug: "file-guard"`) already has
real `config_fields` (confirmed via `GET /admin/node-types`, `family:
"middleware"`): `enabled`, `mode` (`"block"`/`"warn"`), `max_file_size_mb`,
`allowed_types`, `blocked_types`, `notify_on_fail`. The config shape for the
UI popup already exists — nothing new to design there either.

**What's genuinely missing, in order:**
1. `pgxAgentA2ACaller.InvokeByID` doesn't recognize non-text parts.
2. `AgentInvokeActivityOutput`/`AgentInvokeActivityInput` have no field to
   carry a recognized file (or data) part through the workflow.
3. `appflow`'s `case "agent"` in `workflow.go` never calls `FileGate.Intercept`
   for a recognized file part.
4. App Canvas's frontend has no UI to create/edit a `middleware_wirings` row
   from the canvas at all — `middleware` is a separate node family with zero
   canvas-side creation flow today (confirmed: `middleware_wirings.go`'s
   handlers exist, but nothing in `frontend/src/app/admin/applications/`
   calls them).

---

## Scope decision — confirmed with the user 2026-09-26

- **File Guard only.** PII/Prompt-Injection guard is out of scope (no real
  detection logic exists anywhere to hook up).
- **Design shape confirmed**: middleware is NOT a new flow-graph node type
  (that would mean rethinking `middleware_wirings`' existing agent-scoped
  design from scratch). It stays an **attached configuration on an
  agent node** — clicking an `agent` node's canvas box opens its existing
  properties panel, which gains a new "Guards" section. That section is a
  real config form (not a plain toggle), rendering `middleware_defs.config_fields`
  the same way node config fields are already rendered elsewhere, and
  creates/updates a `middleware_wirings` row scoped to that specific canvas
  node instance (`node_id`).
- Confirmed the LLM node is entirely unaffected (see above) — this plan only
  touches the `agent` node's calling/response path.

---

## Phasing

### Phase 1 — teach AppFlow's agent caller to recognize all 4 A2A part kinds — DONE (2026-09-26)
- **SDK check performed 2026-09-26, before writing this phase**: confirmed
  the vendored SDK (`a2a-go/v2` v2.5.0 — already current, no version bump
  needed) has no outbound HTTP client at all (`a2asrv` is server-building
  machinery only, used by `internal/a2a/server.go` to SERVE an A2A endpoint,
  not to call one) and no full JSON-RPC envelope type
  (`{"jsonrpc":"2.0","result":...}` has to stay a small hand-rolled wrapper
  struct). So this phase does NOT eliminate `pgxAgentA2ACaller`'s manual
  HTTP request-building — that stays as-is. What the SDK DOES eliminate:
  the hand-rolled **decode** struct for the parts inside the response.
  `go/cmd/dag-worker/main.go`'s `pgxAgentA2ACaller.InvokeByID`: replace the
  hand-rolled anonymous struct (`Parts []struct{ Text string }`) with the
  real vendored SDK types (`a2a.Task`, `a2a.Artifact`, `[]*a2a.Part`) as the
  `Result` field's type inside a small envelope struct, so `Part.Text()`/
  `Part.URL()`/`Part.Data()`/`Part.Raw()` (real SDK methods, using `Part`'s
  own correct `UnmarshalJSON`, `core.go` line 717) become available instead
  of only ever reading a hand-rolled `.text` JSON field that can't recognize
  the other 3 kinds at all.
- `AgentInvokeActivityOutput` (in `go/internal/appflow/activities.go`) gains
  new optional fields to carry a recognized non-text part downstream — exact
  shape TBD at implementation time (likely a `Kind` string +
  `FileURL`/`FileName`/`ContentType` for the file case, since that's the
  only non-text kind File Guard cares about; `Data`/`Raw` can be traced but
  not specially handled yet, matching "don't build unused capability").
- `InvokeAgentActivity` (`go/internal/appflow/activities.go`): populate the
  new fields when a non-text part is recognized. Text-only responses
  (today's only real-world case, since every canvas-built agent this
  session only ever returned text) are completely unaffected — this is
  additive, not a behavior change to the existing path.
- New tests: at least one confirming a file-part response is now recognized
  and carried through (not silently dropped), and that a pure-text response
  is byte-for-byte unaffected (regression guard for the common case).

**Implemented exactly as specced above, one addition beyond the original
sketch**: `AgentInvoker.InvokeByID`'s return type widened from bare `string`
to a new `appflow.AgentInvokeResult` struct (`ResponseText`, `PartKind`,
`FileURL`, `FileName`, `FileContentType`) — the plan's "exact shape TBD"
turned out to need a named result type rather than growing the interface's
return arity, since `AgentInvoker` is implemented outside the `appflow`
package (`cmd/dag-worker`'s `pgxAgentA2ACaller`) and Go doesn't have a clean
way to add a second return value to an interface method without touching
every caller anyway — a struct is the additive-friendly shape if a 5th kind
is ever needed later.

`pgxAgentA2ACaller.InvokeByID`'s response-decode logic was extracted into a
standalone `decodeAgentSendMessageResponse(io.Reader) (appflow.AgentInvokeResult,
error)` function, specifically so it's unit-testable in isolation —
`InvokeByID` itself still needs a live Postgres pool + real HTTP call and
isn't practical to unit test as a whole. `pgxAgentA2ACaller`'s hand-rolled
parts struct (`Parts []struct{ Text string }`) was replaced with
`Parts []*a2a.Part` (the real vendored SDK type,
`github.com/a2aproject/a2a-go/v2/a2a`), whose own `UnmarshalJSON` correctly
recognizes all 4 real content kinds; `Part.Text()`/`.URL()`/`.Data()`/`.Raw()`
(also real SDK methods) are used to classify the first non-empty part,
priority order text > file > data > raw (text checked first so the
overwhelmingly common real case is unaffected by the added checks).

`InvokeAgentActivity` also gained a small trace-quality fix while wiring
this through: previously a response with only a recognized non-text part
would trace `node_done` with an empty detail string (the exact same class
of bug found and fixed for LLM/agent text responses earlier this session,
see `docs/CURRENT.md`'s bug #4) — now shows `[file response, no text]` (or
`[data response, no text]`/`[raw response, no text]`) instead of a blank
field.

8 new tests (1 in `internal/appflow/workflow_test.go` — `AF-WF-17`; 7 in new
`cmd/dag-worker/agent_response_kinds_test.go` — `S1-169`), following each
package's existing test conventions and ID schemes. `go build ./...`,
`go vet ./...`, and the full `go test ./...` (all 60+ packages) all clean —
run via a `golang:1.25` container against the real vendored module (no Go
toolchain installed directly in this environment), since this exact
combination is also what the Dockerfile's own build stage runs. `TEST_INDEX.md`
updated (new S1 total: 1492).

**Verified live against the real stack, not just unit tests**: rebuilt
`them-dag-worker`'s image (which itself runs the full test suite in-image,
confirmed 0 failures there too), force-recreated all 3 replicas
(`them-dag-worker`, `them-dag-worker-2`, `them-dag-worker-debug`) — logs
confirm all 3 started healthy and are polling their correct task queues
(`appflow-dag` ×2, `appflow-dag-debug` ×1). Re-ran `verify-step2-condition`'s
existing debug flow (a real agent text response, unrelated to file parts) to
confirm zero regression: `agent_false` still correctly echoed the exact same
mock text response as before this change, byte for byte.

**Not yet tested live**: no real agent in this environment currently returns
a file/data/raw part (every canvas-built test agent so far, including
`a2a_echo`, only ever returns text), so the new file-recognition path itself
has only been proven via the unit tests above, not a live end-to-end debug
run. Phase 2 (the File Guard hook) will need a way to actually produce a
file-returning test agent to verify itself, or a live one is a pre-existing
prerequisite worth resolving before Phase 2 starts.

### Phase 2 — hook FileGate into the agent node's execution path — DONE (2026-09-26)

**REVISED 2026-09-26 — real scoping bug found before writing code, and a
generality requirement confirmed with the user.**

**Scoping bug found**: `them.middleware_wirings` already has a real `node_id`
column with its own unique index (`uq_mw_wiring_app_node`, confirmed via
`\d them.middleware_wirings`) — the schema was clearly built to support one
wiring per specific canvas node instance. But `FileGate.loadWiringCfg`'s
actual runtime query (`go/internal/middleware/gate.go` line 259) only ever
looks up by `(application_id, agent.slug)` — it never reads `node_id` at
all. So today, if the same agent is dragged onto one canvas twice, both
instances would share (or collide on) one wiring, not get independent guard
configs — the data model's own intent is silently unenforceable. This phase
fixes `loadWiringCfg`'s query to also match on `node_id` when present
(falling back to slug-only for a wiring created before `node_id` existed).

**Generality requirement, confirmed with the user**: File Guard must be
buildable once and reused, not built agent-specific and redone later when
LLM nodes gain file-carrying responses (a separate, not-yet-scoped gap —
today's Anthropic/OpenAI client code in this repo only ever parses
text/tool_use/tool_result blocks, never an image/file block, even though the
real provider APIs can return one). Concretely: one shared helper —
"does this specific canvas node (`node_id`) have an enabled file-guard
wiring, and if so, intercept this file" — called from two places:
`workflow.go`'s `case "agent"` (real today, since agent responses can now
carry a file per Phase 1) and, later, whatever case handles an LLM
response once that separate gap is closed (not built in this phase, but the
shared helper is written so plugging it in there requires no redesign).

- `workflow.go`'s `case "agent"`: after `InvokeAgentActivity` returns, if the
  output carries a recognized file part, call the new shared helper (see
  below) with `node.ID` (not just the resolved agent's slug). Blocked files
  return a non-retryable failure or route to a "blocked" trace state — exact
  behavior (block vs. warn) already comes from the wiring's own `mode`
  config field, no new decision needed there.
- New shared activity (real I/O — `gate.go` cannot run inline in
  deterministic workflow code, same rule already documented for
  `renderFlowTemplate`/LLM calls) wrapping `FileGate.Intercept`, taking
  `NodeID` (not just `AgentSlug`) so the fixed `loadWiringCfg` query above
  can actually use it.
- Not part of this phase: PII guard, cache middleware, or the LLM-side call
  site itself (blocked on the separate LLM-file-recognition gap) —
  explicitly deferred per the scope decision above. Only the *shared helper*
  needs to be written generically now; wiring it into the LLM case is future
  work once that prerequisite exists.

**Implemented exactly as revised above:**
- `go/internal/middleware/gate.go`: `GateInput` gained `NodeID`;
  `resolveSecCfg`/`loadWiringCfg` widened to `(ctx, appID, nodeID, agentSlug)`
  — the SQL now matches `mw.node_id = $3 OR mw.node_id IS NULL OR mw.node_id
  = ''` with `ORDER BY (mw.node_id = $3) DESC` so an exact node-scoped
  wiring is preferred when one exists, while a legacy slug-only wiring
  (`node_id IS NULL`) still resolves correctly. New test
  `TestFileGate_NodeIDScoping` proves two canvas instances of the same agent
  (same `AgentSlug`, different `NodeID`) now genuinely resolve independent
  configs — one enabled, one disabled, neither leaking into the other.
- `go/internal/appflow/activities.go`: new `FileGateChecker` interface (same
  small-local-interface pattern as `AgentInvoker`/`InlineLLMCaller`, so this
  package doesn't need to import `internal/middleware`'s MinIO dependency),
  new `FileGateCheckInput`/`FileGateCheckOutput` types, new
  `AppFlowActivities.FileGate` field (nil-safe — a nil gate is a documented
  no-op, matching every other optional dependency on this struct), new
  `FileGateActivity` method. 3 new tests (`AF-WF-18/19/20`): nil-gate no-op,
  real delegation with `NodeID` passed through unchanged, and error
  propagation (a scan-infrastructure failure must not silently look like
  "scanning disabled").
- `go/internal/appflow/workflow.go`: new `AppFlowFileGateActivityName`
  constant; `case "agent"` now calls the new activity, scoped by `node.ID`,
  only when `agentOut.PartKind == "file"` — a text-only response (the
  overwhelmingly common case today) never even reaches this code path. A
  gate error fails the run non-retryably; the file's own URL/name aren't
  used to change `accumulated` in this phase (that's a future decision, not
  needed for the guard check itself to work).
- `go/cmd/dag-worker/main.go`: new `appFlowFileGateAdapter` bridging
  `middleware.FileGate` to `appflow.FileGateChecker` (same bridging pattern
  `cmd/them/main.go`'s existing `fileGateAdapter` already uses for the A2A
  server's `FileInterceptor` interface) — `dag-worker` never constructed a
  `FileGate` at all before this phase. Same fail-open construction as
  `cmd/them/main.go`: no `THE_M_S3_ENDPOINT` configured → nil storage client
  → File Guard scanning disabled, never blocks a run. New activity
  registered on the AppFlow worker.
- `docker-compose.dev.yml`: added the 5 `THE_M_S3_*` env vars (same
  `them-minio`-style defaults `them-go-bridge` already uses) to all 3
  dag-worker containers (`them-dag-worker`, `-2`, `-debug`) — confirmed
  missing before this phase, which would have silently kept File Guard
  fail-open in dev even after all the Go code above shipped.

**Real, pre-existing, unrelated gap found and deliberately NOT touched**:
`docker-compose.hetzner.yml` has **zero** S3/MinIO configuration anywhere —
not just for `dag-worker`, but for `them-go-bridge` too (the binary whose
File Guard code for the classic Orchestrator path has existed for a while).
File Guard has apparently never been wired up for the actual Hetzner
production deployment at all. Confirmed with the user not to guess
production secrets/endpoints — left the hetzner compose file untouched.
**This is a real, standing gap, worth a dedicated follow-up** whenever File
Guard needs to actually run in production, not something this session's
scope should silently paper over with fabricated defaults.

4 new tests total (1 in `internal/middleware` — `TestFileGate_NodeIDScoping`;
3 in `internal/appflow`'s activity tests — `AF-WF-18/19/20`), plus Phase 1's
8, so 12 new tests across both phases combined this session. `go build
./...`, `go vet ./...`, and the full `go test ./...` all clean via the same
`golang:1.25` container method Phase 1 used.

**Verified live**: `them-dag-worker` rebuilt (own Dockerfile build stage
re-ran the full suite, 0 failures) and all 3 replicas
(`them-dag-worker`/`-2`/`-debug`) force-recreated. Logs confirm all 3
started healthy and — for the first time — `them-dag-worker`'s own log line
`"storage client initialised for File Guard" endpoint="http://them-minio:9000"`,
proving the new S3/MinIO env vars actually took effect (previously this
binary never even attempted to connect). Re-ran an existing app's debug flow
(a real text-only agent response) to confirm zero regression: completes
exactly as before, the new File Guard check correctly never fires for a
text response (only `PartKind == "file"` triggers it).

**Not yet tested live**: same standing gap as Phase 1 — no real agent in
this environment currently returns an actual file, so the File Guard hook
itself (the `case "agent"` → `FileGateActivity` → `FileGate.Intercept` →
ClamAV scan path) has only been proven via unit tests with fakes, not a real
end-to-end debug run with a genuine file artifact. Building a
file-returning test agent (or reusing an existing one, if one exists
somewhere in this repo's test fixtures) would be needed to close this gap
before fully trusting Phase 2 in front of a real user.

### Phase 3 — frontend: attach a File Guard wiring to an agent node
- New "Guards" section in the `agent` node's properties panel
  (`frontend/src/app/admin/applications/components/cbv/panels/` — exact
  file TBD, likely a new `AgentGuardsSection.tsx` mounted alongside the
  agent panel's existing content).
- Renders `file-guard`'s `config_fields` (fetched live via `GET
  /admin/node-types`, matching this session's confirmed self-documenting
  pattern — no new frontend-hardcoded field list) as a real form: enabled
  toggle, mode (block/warn), max file size, allowed/blocked types, notify
  toggle.
- Wired to the existing `middleware_wirings` CRUD routes
  (`POST`/`PUT`/`DELETE /admin/applications/{id}/middleware-wirings...`),
  scoped to the selected agent node's `node_id` — no new backend route
  needed, this is a pure frontend addition reusing existing APIs.
- New tests: at minimum the same plain-node/assert convention used
  throughout this session's other AppFlow frontend work.

---

## Explicitly out of scope

- PII/Prompt-Injection guard (`guard_default`) — no real detection logic
  exists anywhere in the codebase; a separate, larger, not-yet-scoped
  feature.
- Cache middleware (`cache_default`) — not requested, not investigated this
  session.
- Any change to the LLM node's calling path — confirmed unaffected; LLM
  responses can never carry a file in this codebase's provider model.
- Any change to the classic Orchestrator / `internal/a2a/executor.go`'s
  existing event-bus file handling — that path already works correctly and
  is untouched by this plan.
- Handling `Data`/`Raw` A2A part kinds beyond "recognize and trace them" —
  only the file case gets real downstream handling (File Guard); building
  UI/logic for structured-data or raw-byte responses is not requested and
  not scoped here.
