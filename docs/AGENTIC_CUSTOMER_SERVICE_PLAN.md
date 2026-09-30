# Agentic Customer Service — Research & Canvas Gap Analysis
# Last updated: 2026-09-30

---

## Completed

- **Router: dynamic label ports + llm_key debug param** — 2026-09-30
  - Router node now renders one spread output handle per `output_labels` entry (like condition's true/false)
  - Router declares `llm_key` RuntimeParam — credential picker appears in debug setup panel
  - `DynamicControlOutputSource="output_labels"` field added to registry; frontend reads it generically
  - Edge sourceHandle wiring in `docToCanvas` extended to cover `flow_control` nodes with dynamic ports
  - Tests updated + new `TestRouterDynamicControlOutputSource` added

---

## 1. ADK / LangGraph Primitives — What They Have

### Google ADK (adk.dev)

| Primitive | Description |
|---|---|
| **SequentialAgent** | Run sub-agents one after another in fixed order — deterministic, no AI routing |
| **LoopAgent** | Run sub-agents in a cycle until `escalate=true` or `max_iterations` reached; state passes via shared `output_key` context between iterations |
| **ParallelAgent** | Run multiple sub-agents concurrently; results collected when all complete |
| **LlmAgent (orchestrator)** | LLM decides at runtime which sub-agent to call next — non-deterministic routing |
| **Tool-as-agent** | Any callable (function, API) wrapped as an agent, composable with the above |
| **escalate signal** | Sub-agent raises `context.actions.escalate = true` to break a loop early — the loop's termination hook |
| **output_key / shared context** | Named slot in shared state; agents write to it, later iterations read from it — the cross-iteration memory model |

**Key ADK insight:** ADK separates *deterministic* orchestration (Sequential/Loop/Parallel — no LLM involved in routing) from *dynamic* orchestration (LlmAgent decides). We conflate these — our `router` node does LLM routing but we have no pure deterministic loop primitive.

### LangGraph

| Primitive | Description |
|---|---|
| **StateGraph** | Directed graph where nodes share a typed state object — all edges read/write the same state dict |
| **Node** | Any Python callable; receives state, returns state delta |
| **Edge** | Unconditional transition A → B |
| **Conditional edge** | `add_conditional_edges(node, fn)` — function returns next node name; enables cycles |
| **Cycle / loop** | Graph can have back-edges; a conditional edge pointing back to an earlier node creates a loop |
| **interrupt / human-in-the-loop** | `interrupt(value)` inside a node pauses execution; resumes when `Command(resume=value)` is sent — similar to our HIL signal |
| **Send API** | Dispatch to a node with custom state — enables dynamic fan-out where each branch gets different state (beyond our static Fork) |
| **Checkpointing** | Persistent state snapshot at each node; enables resume-after-failure and time-travel debugging |
| **Subgraph** | Nest one StateGraph inside another as a node — composable hierarchical workflows |

**Key LangGraph insight:** Cycles are first-class — any node can loop back. Our canvas is a strict DAG (no back-edges). This is the single biggest gap for real agentic apps.

---

## 2. Customer Service App Design

### Overview

A customer contacts support. The system classifies their intent, gathers context in parallel, attempts automated resolution, escalates to a human for high-value decisions, and loops for clarification when the request is ambiguous.

### ASCII Flow Diagram

```
[Entry Point: customer message]
          |
          v
    [router: classify]
    billing | refund | technical | unclear
       |         |         |         |
       |         |         |    [clarification loop]  ← LOOP (missing)
       |         |         |    ask → wait → re-classify
       |         |         |    (max 3 rounds, then escalate)
       |         |         |
       |     [condition:   |
       |      refund >$500?]
       |       true | false
       |         |      |
       |      [HIL:    [llm: auto-approve]
       |       human   small refund]
       |       review]
       |         |
       v         v
    [fork: gather context in parallel]
    /                    \
[agent: fetch           [agent: fetch
 account info]           order history]
    \                    /
     [join: merge context]
          |
          v
    [llm: compose resolution]
          |
     [condition: resolved?]
       true |  false
            |      |
       [done]  [hil: escalate to human agent]
                    |
               [llm: compose handoff summary]
                    |
               [done: ticket created]
```

### Node-by-Node Breakdown

| Step | Canvas Node | Config |
|---|---|---|
| Receive message | Entry Point (WS/SSE) | — |
| Classify intent | `router` | labels: billing, refund, technical, unclear |
| Clarification loop | **MISSING — needs `loop` node** | ask LLM, wait for reply, re-classify; max 3 |
| Refund amount check | `condition` | expression: `{{gt .refund_amount 500}}` |
| High-value HIL | `hil` | approver_role: "support_manager", prompt: "Approve refund of ${{.refund_amount}}?" |
| Auto-approve small refund | `llm` | generates approval message |
| Fetch account info | `agent` (A2A) | billing-lookup agent |
| Fetch order history | `agent` (A2A) | order-history agent |
| Parallel gather | `fork` / `join` | both agents run concurrently |
| Compose resolution | `llm` | system_prompt: "You are a support agent…", uses account + order vars |
| Resolved check | `condition` | expression: `{{eq .resolution_status "resolved"}}` |
| Escalate to human | `hil` | approver_role: "support_agent", prompt: "{{.resolution_draft}}" |
| Handoff summary | `llm` | generates ticket summary |

### What Works With Current Canvas

- Intent routing → `router` node handles this well
- Refund threshold check → `condition` node
- HIL for high-value approval → `hil` node (just built)
- Parallel account + order lookup → `fork` / `join`
- LLM resolution drafting → `llm` node
- Escalation HIL → second `hil` node

### What's Missing

1. **Loop / while node** — the clarification loop (ask → wait → re-classify, max N times) cannot be expressed. The canvas is a strict DAG; there's no way to route back to an earlier node.
2. **Wait-for-input node** — inside a clarification loop, the app needs to pause and receive another user message mid-flow (not just approve/reject, but arbitrary text). HIL only does approve/reject binary decisions.
3. **Counter / iteration state** — loops need to track how many times they've iterated (to enforce max 3). Flow vars exist but there's no increment primitive; an LLM node could do it but that's wasteful.
4. **Dynamic fan-out (Send API equivalent)** — for cases where you want to spawn N parallel branches based on runtime data (e.g. look up N order IDs). Fork requires knowing edge count at design time.

---

## 3. Canvas Gap Analysis

| Gap | Why existing nodes can't cover it | Complexity | Priority for demo |
|---|---|---|---|
| **Loop node** (while/for with back-edge) | Canvas is a strict DAG — no back-edges allowed in compiler or graph validator. Condition node routes forward only. | High — requires DAG validator changes, cycle detection, iteration counter in workflow state, max_iterations enforcement in Temporal | **Essential** — clarification loop is the most realistic agentic behaviour |
| **Wait-for-input node** (receive mid-flow user message) | HIL only sends a binary signal (approve/reject). Receiving free-text input mid-flow requires a new signal type and a way to surface it in the UI | Medium — new Temporal signal type, new debug UI widget, new API endpoint | **Essential** — without it, clarification loop has nothing to receive |
| **Iteration counter / loop vars** | No primitive for `counter += 1`. Could fake with an LLM node but fragile | Low — could add a `set_var` node or support simple math expressions in `condition` | Nice-to-have (workaround: encode counter in LLM output) |
| **Dynamic fan-out** (runtime-determined branch count) | Fork edge count is fixed at canvas design time | High — requires late-bound edge resolution, new graph execution model in workflow.go | Nice-to-have for demo; static fork covers the 2-agent parallel case |
| **Subgraph / nested flow** | No way to call one app canvas flow from another | High — requires inter-workflow Temporal calls or inline sub-execution | Nice-to-have |
| **Timeout on non-HIL nodes** | Only HIL has timeout_seconds. A wait-for-input node also needs timeout + fallback | Low — extend the same timeout pattern | Needed alongside wait-for-input |

---

## 4. Recommended Build Order

### Phase 1 — Build the demo with current canvas (no new node types)
Use what exists to build a partial but runnable customer service app:
- `router` → classify intent (billing / refund / technical)
- `condition` → refund > $500?
- `hil` → manager approval for large refund
- `fork` / `join` → parallel account + order lookup
- `llm` → compose resolution response
- `hil` → escalation handoff

**Skip the clarification loop for now** — handle "unclear" intent with a single LLM node that asks for clarification and ends the run (user re-sends). This is weaker but runnable today.

Deliverable: working app in the canvas, tests a realistic HIL + parallel + routing flow.

### Phase 2 — Loop node (highest value gap)
Add a **loop node** to the canvas:
- New node type `loop` in `noderegistry.go` with `max_iterations` config field
- Back-edge support in `compiler.go` (detect loop node's back-edge, don't reject as cycle)
- Iteration counter in Temporal workflow state
- `escalate` condition: sub-node sets a flow var (e.g. `loop_done = true`) that the loop checks

This unlocks the clarification loop and every other retry/refinement pattern.

### Phase 3 — Wait-for-input node
Add a **wait_input** node:
- Parks the flow like HIL but accepts free-text, not approve/reject
- New Temporal signal type; new API endpoint `POST /runs/{id}/input/{node_id}`
- Debug inspector: text input box + submit (parallel to HIL's approve/reject buttons)
- Required to make the clarification loop actually interactive

### Phase 4 — Polish
- Iteration counter expression support in condition node
- Timeout on wait_input node
- Dynamic fan-out (lower priority — static fork covers most cases)

---

## 5. Immediate Next Step

Build the **Phase 1 customer service demo app** using only existing node types. This gives a real working example of HIL + routing + parallel in one canvas, and surfaces any runtime bugs before investing in new node types.

Suggested app name: **"Smart Refund Handler"**
Flow: customer message → router (billing/refund/technical) → condition (>$500?) → HIL (large) or auto-approve (small) → fork (account+order lookup) → join → LLM (compose response) → done.
