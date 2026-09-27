# AppFlow LLM Node File Output Plan
# Status: NOT STARTED. Scoping only.
# Owner: platform
# Last updated: 2026-09-27

---

## Goal

Today, an App Canvas `llm` node (and agent builder's equivalent LLM node —
same underlying `internal/llm` package) can only ever produce text. If a
real LLM call (Anthropic/OpenAI) returns an image or file part, it is never
even decoded — not just discarded after the fact, the struct fields to hold
it don't exist anywhere in this codebase.

This gap was found while discussing whether an LLM node could generate a
PDF/JPG directly (e.g. "call Opus to generate a PDF"), as a generalization
of the File Guard work done in `docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md`
(which only covers `agent`-kind/A2A nodes, explicitly out of scope for LLM).

**Today's real workaround, which already works with no new code:** build an
A2A agent that calls the LLM internally and returns a file part (exactly
what `docu_writer` already does — Claude → Markdown → fpdf2 → PDF bytes),
then use App Canvas's existing `agent` node to call it. File Guard already
covers this path end to end (tested live 2026-09-27 against `a2a_stream`).
**This plan is only for making an `llm` node itself do this directly** —
not required unless there's a real product need for every LLM node to
support files, not just agent nodes.

---

## Why this is NOT a small follow-up to the A2A work

The A2A File Guard fix was small because the file-recognition plumbing
already existed in the A2A protocol/SDK (`a2a.Part.URL()`/`.Raw()`, etc.) —
the fix only had to read fields that were already there. For the LLM path,
none of the equivalent plumbing exists at any layer:

| Layer | A2A path (already done) | LLM path (today) |
|---|---|---|
| Wire format decode | Real SDK types, `Part.URL()`/`.Raw()` already exist | Anthropic SSE decoder has no field for an image/document content block at all (not discarded — never decoded); same for OpenAI's multimodal content-array shape |
| Result type | `AgentInvokeResult` already had `PartKind`/`FileURL`/`FileName` fields, just unused | `domain.ContentPart` has a doc comment claiming `"image"` is valid, but no field to hold image bytes/URL/mimetype — aspirational only, zero constructors set it |
| Event/stream type | N/A (single blocking call) | `llm.StreamEvent` only has `text_delta`/`tool_calls`/`stop`/`error` — a new kind needs adding in **5 switch-statement call sites across 4 files** (`internal/orchestrator/orchestrator.go`, `cmd/dag-worker/main.go` ×2, `cmd/agent-runtime/llm.go` ×2) |
| Node-level call signature | `AgentInvoker.InvokeByID` already returned a struct | `InlineLLMCaller.Complete` returns a bare `(string, error)` — structurally cannot carry a file even if one were decoded; this is a breaking interface change, not additive |
| File Guard hook | New activity call, ~1 day | Confirmed already generic enough to reuse as-is (`FileGateChecker`/`InterceptInline` in `internal/appflow/activities.go`) — genuinely the one small piece here |

(Researched live 2026-09-27 — see conversation history for the full
file/line citations if this doc goes stale and needs re-verifying.)

---

## Rough phasing (not started, not estimated in detail yet)

### Phase 1 — provider-level decode
- `internal/llm/anthropic.go`: decode `image`/`document` content blocks from
  the real Anthropic streaming SSE shape (base64 `source.data` + mimetype).
- `internal/llm/openai.go`: decode OpenAI's multimodal content-array output
  shape (differs from Anthropic's — each provider needs its own parsing,
  the `Provider` interface itself stays generic).
- New `StreamEvent` kind (e.g. `"file_delta"` or similar) carrying the
  decoded bytes/URL + mimetype.

### Phase 2 — carry the file through the type system
- `domain.ContentPart` gains real fields for an image/file type (not just
  the existing aspirational comment).
- Update the 5 `switch ev.Type` call sites (orchestrator, dag-worker ×2,
  agent-runtime ×2) to handle the new kind — at minimum, don't drop it;
  most will just need to pass it through to whatever already carries
  `ResponseText` in that call site's own result type.
- `InlineLLMCaller.Complete`'s return signature changes from `(string,
  error)` to a result struct (breaking change — every real caller + test
  double needs updating, same shape of change `AgentInvoker.InvokeByID`
  went through in the A2A plan's Phase 1).

### Phase 3 — wire into AppFlow + File Guard
- `internal/appflow/workflow.go`'s `case "llm"`: recognize a file-kind
  result, call the existing `FileGateChecker`/`InterceptInline` helper —
  this part is genuinely small, the helper needs no changes (confirmed).
- Agent builder / `cmd/agent-runtime`'s equivalent LLM node: same wiring,
  separate call site (not shared with appflow's workflow code).

### Phase 4 — App Canvas UI
- Decide how a file produced by an `llm` node is surfaced/downloadable in
  the canvas debug panel / artifacts view (agent nodes already have this
  via the existing A2A artifact pipeline — reuse, don't rebuild, if the
  shape matches closely enough once Phase 1-3 land).

---

## Estimate

Multi-week, not a few days — every layer between "provider API" and
"canvas node" needs a new field/case built from scratch, not just a new
call added to existing plumbing (contrast with the A2A plan, which was
same-session-sized). Recommend re-scoping each phase in detail (open
questions, exact structs, test plan) only once there's a real product
decision to build this — don't start implementation from this doc alone.

## Explicitly out of scope until this plan is picked up

- Everything already covered by `docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md`
  (agent-node file handling + File Guard) — already done, unaffected by
  this plan either way.
- Any UI/UX design for how a file-producing LLM node looks on the canvas —
  not designed yet, deliberately deferred to Phase 4 above.
- A2A protocol changes, Google ADK, or any other agent framework — not
  relevant to this gap; the blocker is entirely inside this codebase's own
  `internal/llm` provider parsing, not the A2A wire protocol.
