# Gateway Profile Capabilities Plan
# Last updated: 2026-10-15

---

## What Is a Gateway Profile?

A **gateway profile** is an App Canvas app linked to a Gateway Client.
When an agent calls `POST /{tenant}/llm/v1/chat/completions`, the-M intercepts
the request, runs it through the app's AppFlow (transform → LLM → etc.), and
returns the result. The agent changes only its `base_url` and API key.

```
Agent (OpenAI SDK)
  → base_url: http://the-M/{tenant}/llm/v1
  → api_key: <gateway token>
       ↓
  the-M Gateway (auth + policy)
       ↓
  Profile App (AppFlow: transforms, LLM nodes, conditions…)
       ↓
  LLM Provider (Anthropic / OpenAI)
```

---

## Current Capabilities

### Request Path (Agent → LLM) ✅

| Capability | Status | How |
|---|---|---|
| Auth (gateway token) | ✅ Done | `gateway_clients` table + SHA256 hash |
| Model allowlist | ✅ Done | `gateway_policies.allowed_models` |
| Model aliases | ✅ Done | `gateway_policies.model_aliases` |
| Per-request token ceiling | ✅ Done | `gateway_policies.max_tokens_per_request` |
| Monthly USD budget cap | ✅ Done | `gateway_policies.monthly_budget_usd` |
| Rate limiting | ✅ Done | Redis INCR per token + per app |
| PII masking (request) | ✅ Done | Transform node in profile app (regex_replace) |
| Route through AppFlow profile | ✅ Done | `dispatchAppFlowSync/Stream` |

### Response Path (LLM → Agent) ⚠️

| Capability | Status | How |
|---|---|---|
| Return LLM output to agent | ✅ Done | `output.FinalText` returned as OpenAI JSON |
| PII masking (response) | ❌ Missing | No response transform node yet |
| Content policy on response | ❌ Missing | No post-LLM node in profile app |
| Tool call capture | ❌ Missing | See note below |

### Audit & Observability

| Capability | Status | What Is Stored |
|---|---|---|
| Request metadata log | ✅ Done | `gateway_requests` table: tokens, cost, model, status, latency |
| Raw request content | ❌ Missing | Message text is NOT stored |
| Raw response content | ❌ Missing | LLM output is NOT stored |
| Tool calls / tool results | ❌ Missing | Not captured anywhere |
| Per-run step trace | ✅ Partial | `run_steps` stores node execution, not message content |

---

## What the-M CANNOT Do at Gateway Level

**The LLM never sends binary files through chat completions.** The response is
always JSON with a text `content` field. If a user asks for a PDF/ZIP/image, the
LLM returns its content as text (raw bytes as a string, base64 characters, etc.).
The-M sees this as plain text — there is no file type to detect.

**Tool calls**: if the agent uses function calling, the tool result is returned
to the LLM as text/JSON. The tool itself runs on the agent side — the-M never
sees the binary execution. We can log the tool call/result text, but not
intercept execution.

---

## Planned Capabilities

### Phase 1 — Response Transform (high value, low complexity)

Add a **response transform node** to AppFlow that runs after the LLM node.
Same mechanism as the existing request transform — regex_replace steps — but
applied to the LLM output before returning to the agent.

**Enables:**
- PII masking on LLM output (e.g. LLM echoes back a card number)
- Content redaction (remove URLs, phone numbers, etc. from responses)
- Response rewriting

**How to enable:** add a second transform node after the LLM node in the
profile app and connect it. No new node type needed — transform already exists.
The gateway currently only passes `FinalText` through — we need to confirm
the transform node's output flows correctly as `FinalText`.

**Gap today:** the profile app definition has no "response" concept — the AppFlow
runs linearly (entry → transform → LLM → output). The LLM node output IS
`FinalText`. So adding a post-LLM transform node would naturally work.
**This may already work** — needs a test.

---

### Phase 2 — Full Message Content Audit Log (medium complexity)

Store the actual request messages and LLM response text per gateway call.

**New DB column(s) on `gateway_requests`:**
```sql
request_messages  jsonb   -- full messages[] array sent to LLM
response_text     text    -- full LLM response text
tool_calls        jsonb   -- tool_calls from LLM response (if any)
tool_results      jsonb   -- tool results returned to LLM (if any)
```

**Audit levels (configurable per Gateway Client or per Policy):**
| Level | What is stored |
|---|---|
| `none` | Nothing (current default) |
| `metadata` | Tokens, cost, model, status (current behavior) |
| `response` | Metadata + LLM response text |
| `full` | Metadata + request messages + response text + tool calls |

**How to enable:** add `audit_level` column to `gateway_policies` or
`gateway_clients`. The handler reads it and stores accordingly.

**Privacy note:** `full` audit stores user message content — must be
behind explicit opt-in and may have compliance implications.

---

### Phase 3 — Tool Call Capture (medium complexity)

If the agent uses function calling, capture the tool calls and results.

**What we can capture:**
- Tool call name + arguments (from LLM response `tool_calls` field)
- Tool result content (from agent's next message with `role: "tool"`)

**What we cannot capture:**
- The actual tool execution (happens on the agent side)
- Binary output of tools

**How to enable:** inspect `ChatRequest.Messages` for `role: "tool"` entries
and the LLM response JSON for `tool_calls` — both are already in the request/
response body that passes through the handler. Store in `tool_calls` /
`tool_results` columns (Phase 2).

---

### Phase 4 — Content Policy Node (higher complexity)

A new AppFlow node type: **policy** (or **guardrail**). Runs after LLM output,
evaluates the response against a policy (keyword blocklist, regex patterns,
semantic classification via a second LLM call), and either passes or blocks.

**Enables:**
- Block responses containing specific content categories
- Flag responses for human review (HIL node integration)
- Custom rejection messages to the agent

**How to enable:** new node kind in AppFlow (`kind: "policy"`), new node type
registered in the canvas. Profile app connects: LLM → policy → output.

---

## How to Enable Each Capability Today vs Future

| What you want | Today | Future |
|---|---|---|
| Mask PII in request | Add transform node before LLM in profile app | Same |
| Mask PII in response | Add transform node after LLM (**test if works**) | Phase 1 confirms |
| See what was sent/received | Not possible | Phase 2: audit log |
| Capture tool calls | Not possible | Phase 3 |
| Block harmful response content | Not possible | Phase 4: policy node |
| Scan "files" in response | Not meaningful — LLM returns text only | N/A |

---

## Recommended Build Order

1. **Test response transform** — check if a post-LLM transform node in the
   profile app already works. If yes, document it. Low effort, high value.
2. **Phase 2: audit log** — add `audit_level` + store request/response content.
   Gives 100% visibility into what transits the gateway.
3. **Phase 3: tool call capture** — piggybacks on Phase 2 infrastructure.
4. **Phase 4: policy node** — new node type, larger scoping needed.
