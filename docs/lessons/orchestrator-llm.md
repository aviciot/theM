---
title: Lessons — Orchestrator & LLM
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-09-07 — Worker uses EP LLM settings but config is on orchestrator row

**Symptom:** Canvas UI allows setting LLM provider/model at EP level, but the worker loads LLM config only from `app_orchestrators.llm_model/llm_provider` — which are NULL if the user only configured the EP. Results in "messages: Input should be a valid array" from Anthropic (empty model name → empty messages array).
**Fix:** `workerconfig/loader.go` now falls back to `entry_points.llm_provider/llm_model` when the orchestrator row has NULL values.
**Watch for:** The EP query in the loader must select `ep.llm_provider, ep.llm_model` — verify when adding new EP columns that affect runtime.

---

---

## 2026-07-07 — Multi-turn: user message must be written to task_messages at task creation, not reconstructed on demand

**Symptom:** When building prior-turn history in `_load_context_history`, tasks that had no `task_messages` (e.g. if the agentic loop was interrupted before any messages were persisted) produced empty history, silently dropping the user's prior message from LLM context.

**Fix:** Save the user message as `task_message seq=0` immediately after root task creation, before entering the agentic loop. This makes the user message DB-durable at creation time, independent of whether the loop completes. Future turns always find it via `_load_context_history`.

**Watch for:** Any new "initial" data that subsequent turns need (system metadata, injected context, etc.) should also be stored in `task_messages` at task creation time — don't rely on `task.input_message` being available to future turns, as `_load_context_history` only reads `task_messages`.

---

---

## 2026-07-11 — Debate flow slow (264s) — three compounding root causes

**Symptom:** debate_flow run takes 264s and fails with "activity Heartbeat timeout" on iteration 3.

**Root causes (by impact):**

### 1. Context explosion from large tool results (biggest impact)
Iteration 2 plan call received 14,981 input tokens vs 1,038 for iteration 1. The 3 debate agents each returned ~4,000 tokens of detailed argument text. These were passed verbatim as `tool_result` blocks into the LLM context. The LLM had to process 13k tokens of new content just to compose the judge tool call.

**Fix:** JSON-aware compaction in `_compact_tool_result()` — keeps only routing fields (`_COMPACT_JSON_KEEP`: agent, main_point, confidence, approach, round, winner, etc.) from JSON tool results. Falls back to `_MAX_TOOL_RESULT_CHARS = 2000` char slice for non-JSON. Full results are persisted in DB/artifacts. `_slim_tool_use_inputs()` also strips heavy `arguments`/`opponent_arguments` fields from tool_use blocks stored in `self.messages`.

### 2. `plan_turn heartbeat_timeout=30s` too short for large contexts
When context reaches 15k+ tokens, TTFT (time-to-first-token) from Anthropic can exceed 30s. The activity heartbeats on every token — if the first token arrives after >30s, Temporal kills the activity.

**Fix:** Raised `heartbeat_timeout` for `plan_turn_activity` to 90s.

### 3. System prompt routing debate outputs through full argument text
Original system prompt instructed the orchestrator to forward complete agent arguments. R3 update changed it to route only summary fields (main_point, confidence, approach, round) and keep full arguments inside the A2A task artifacts.

**Architecture note:** Full argument text lives in `them.artifacts`. Orchestrator LLM context holds only routing fields. Agents receive only the prior round's summary, not full transcripts.

**Watch for:** Any orchestrator calling agents that produce large outputs (>2k chars each) will experience context growth proportional to `n_agents × output_size × n_rounds`. Always cap tool results in the LLM context; full content lives in artifacts.

---

---

## 2026-07-11 — tool_use/tool_result mismatch on resumed conversations (two root causes)

**Symptom:** After resuming a conversation (same context_id), next message fails with Anthropic API HTTP 400: "tool_use ids were found without tool_result blocks immediately after".

**Root cause A — tool_results never persisted:** The agentic loop persisted assistant turns (tool_use blocks) but never persisted the corresponding tool_result messages to `task_messages`. When the next turn loaded history via `_load_context_history`, it found assistant messages containing tool_use IDs with no matching tool_result rows → invalid conversation.

**Fix A:** Added `record_tool_results_activity` — a new Temporal activity that persists the tool_result message (role='user', content=[{type:tool_result, ...}]) to `task_messages` immediately after all agents complete and before the next iteration. This is idempotent (seq-keyed) and runs with retry_policy.maximum_attempts=3.

**Root cause B — _sanitize_history only checked tail:** Even with new runs, old DB rows from before the fix had orphaned tool_use IDs in the middle of history. `_sanitize_history` only stripped the tail (last assistant turn if dangling) — it missed orphaned pairs in earlier turns.

**Fix B:** Full-pass `_sanitize_history` — (1) collect all `tool_result` IDs from the entire history in one pass, (2) rebuild history dropping any assistant message whose tool_use IDs have no matching result anywhere in history, (3) drop the orphaned tool_result message immediately following any dropped assistant turn.

**Watch for:**
- Every assistant turn that includes tool_use blocks MUST be followed immediately by a user message containing one `tool_result` block per tool_use. This is an Anthropic API hard requirement.
- Always persist tool_results to DB before the next planning turn — if the bridge crashes between agents completing and the next LLM call, the DB must have enough data to reconstruct a valid history.
- `_sanitize_history` is a last-resort safety net; it should never need to drop rows in a healthy run.

---

---

## 2026-08-25 — Anthropic 400: tool_use.input must be an object (not null)

**Symptom:** All runs using MCP tools (or any tool with no parameters) fail with `messages.N.content.1.tool_use.input: Input should be an object` from the Anthropic API.
**Root cause:** Two bugs in `go/internal/llm/anthropic.go`:
1. At stream parse time, `json.Unmarshal(buf.Bytes(), &input)` on an empty or `{}` JSON buffer leaves `input` as a nil map. A nil map marshals to `null`, not `{}`.
2. At history round-trip time, if `ToolInput` in a stored `ContentPart` is nil or the literal JSON `null`, it is forwarded verbatim to Anthropic — which rejects anything that is not a JSON object.
**Fix:** At parse time, set `input = map[string]any{}` when unmarshal leaves a nil map. At serialization time in `domainPartsToAnthropicContent`, replace nil/`"null"` `ToolInput` with `json.RawMessage("{}")`.
**Watch for:** Any tool that takes zero parameters will trigger this. The fix is purely in the LLM layer; no changes to tool definitions or agent code are needed.

---

---

## 2026-09-05 — Orchestrator Delete is soft (UPDATE enabled=false), not hard

**Symptom:** E2E test 14 fails with "Orchestrator created: resource already exists" on repeated runs. The test's cleanup via `DELETE /admin/orchestrators/{name}` succeeds (HTTP 200) but leaves the row in the DB with `enabled=false`. Re-creating with the same name fails with 23505 (unique constraint on `name` + `tenant_id`).

**Root cause:** `DeleteOrchestrator` in `dal/orchestrators.go` does `UPDATE ... SET enabled=false`, not `DELETE`. This is a legacy choice that differs from agents (hard DELETE + component_definitions cleanup) and applications (hard DELETE with CASCADE). Soft-delete preserves the name slot, blocking reuse.

**Fix:** Change `DeleteOrchestrator` to a hard DELETE. No DB schema change needed — there are no tables with FKs pointing at `orchestrators.id` that require cascade behavior (app_orchestrators are a separate table with their own lifecycle). This is Step 29 candidate 1.

**Watch for:** Never assume admin "delete" endpoints free the resource name. Always verify in the DAL whether it's soft or hard delete.

---
