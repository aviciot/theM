# AI Workflow Advisor
# Last updated: 2026-07-12

The AI Workflow Advisor is a built-in analysis assistant in the Application Builder canvas. It reads your workflow graph (entry points, orchestrators, agents, connections) and streams back an advisory — what's broken, what's missing, what could be improved — and can **apply changes directly to the database** with a single click.

---

## Where it lives

| Component | Location |
|---|---|
| Open button | Top-right toolbar in the Application Builder (`AI Advisor`) |
| Panel | Slides in as a 380px right sidebar alongside the canvas |
| Agent code | `agents/workflow_advisor/` (`main.py`, `advisor.py`) |
| Orchestrator | `them.orchestrators` row with `name = 'workflow_advisor'` |
| Agent DB row | `them.agents` row with `slug = 'workflow_advisor'` |
| Frontend | `frontend/src/app/admin/applications/page.tsx` — `AdvisorPanel`, `ProposalCard`, `parseAdvisorBuffer`, `applyProposal` |

---

## End-to-end data flow

```
User opens AI Advisor
  │
  ├── Scan animation: nodes light up sequentially (visual only)
  │
  ├── serializeWorkflow() — reads canvas nodes + full orchestrator/agent lists
  │     Produces JSON with per-orchestrator:
  │       id (DB UUID), name, systemPrompt (≤800 chars), model,
  │       maxParallelTools, maxIterations, historyWindow, memoryEnabled,
  │       assignedAgents: [{id, slug}]  ← resolved from DB, not raw UUIDs
  │     Per agent:
  │       id (DB UUID), slug, description, transport, hasAuthToken,
  │       scanResult: {score, risk, summary}
  │
  ├── advisorSend(null, isInitial=true)
  │     Wraps JSON: "Analyze this workflow:\n\n<json>"
  │     Opens WebSocket: /ws/orchestrate/workflow_advisor?token=...
  │     Sends: { type: "message", content: "...", context_id: "..." }
  │
  ├── Bridge WS → workflow_advisor orchestrator (LLM)
  │     System prompt: thin pass-through — calls agent__workflow_advisor
  │     immediately, returns response verbatim
  │
  ├── A2A adapter → them-workflow-advisor:9600 (A2A agent)
  │     main.py: _extract_message() → _extract_workflow() → stream_analysis()
  │     advisor.py: _build_analysis_prompt() converts graph dict → structured
  │     text prompt → Claude (claude-sonnet-4-6) streams response
  │
  ├── Bridge streams tokens back via WS msg.type = "token"
  │
  ├── Frontend: parseAdvisorBuffer(buf)
  │     Scans for ```them-proposal ... ``` fenced blocks in the live buffer
  │     Closed blocks → parsed into Proposal objects, stripped from display text
  │     Open blocks (not yet closed) → shown as "_Preparing suggestion…_" chip
  │     Prose passes through unchanged
  │
  └── Renders:
        Text bubble (advisor prose)
        ProposalCard(s) below the bubble — one per proposal
        "Apply all (N)" button if ≥ 2 pending proposals
```

---

## What the advisor knows about the-M

The system prompt in `agents/workflow_advisor/advisor.py` (`_SYSTEM_PROMPT`) encodes the full orchestration mental model:

- **Routing mechanism** — the LLM picks agents using only the `description` field; vague or overlapping descriptions cause wrong routing
- **Orchestrator system prompt quality** — must name agents and explain when to use each; empty or boilerplate prompts = guesswork
- **`maxIterations`** — if the workflow has 4 agents, it may need ≥ 6 iterations; the advisor flags configs that look too low
- **`historyWindow`** — `null` / 0 = every turn starts fresh; multi-turn workflows need ≥ 5
- **`maxParallelTools`** — 1 = sequential only; flag for workflows with many independent agents
- **`memoryEnabled`** — useful for long conversations but adds latency
- **Structural issues** — isolated nodes, missing entry points, zero-agent orchestrators
- **Security** — surfaces per-agent scan results (score/100, risk, summary) in the analysis

---

## Proposal protocol — them-proposal blocks

When the advisor recommends a concrete change, it emits a fenced block immediately after the prose sentence that motivates it:

````
```them-proposal
{
  "id": "p1",
  "type": "update_prompt",
  "targetType": "orchestrator",
  "targetId": "<DB UUID of the orchestrator>",
  "targetName": "support_router",
  "field": "system_prompt",
  "current": "You are a helpful assistant.",
  "suggested": "You are the Support Router for Acme Corp...\n(full text)",
  "reason": "Current prompt names no agents; the LLM has no routing signal."
}
```
````

### Valid type/field pairs

| type | field | targetType | value type |
|---|---|---|---|
| `update_prompt` | `system_prompt` | `orchestrator` | string |
| `update_description` | `description` | `agent` | string |
| `update_display_name` | `display_name` | `orchestrator` or `agent` | string |
| `update_config` | `max_iterations` | `orchestrator` | integer |
| `update_config` | `history_window` | `orchestrator` | integer |
| `update_config` | `max_parallel_tools` | `orchestrator` | integer |

### Parser rules (frontend)

- `parseAdvisorBuffer(buf)` is called on every incoming token — it re-scans the accumulator from scratch.
- A block is extracted only once its closing ` ``` ` (on its own line) has arrived. Until then, the text from the opening fence onward is replaced with "Preparing suggestion…".
- JSON inside a closed block is parsed; if invalid or `field` is not in the allowlist (`PROPOSAL_ALLOWED_FIELDS`), the block is silently dropped.
- `mergeProposals(existing, incoming)` preserves `status` and `error` on already-seen proposal ids so an "Applied ✓" card doesn't flip back to pending on the next token.
- Proposal ids (`p1`, `p2`, …) are unique **per response turn**. React keys use `messageIndex + proposalId` to avoid cross-turn collisions.

---

## Apply flow

```
User clicks "Apply" on a ProposalCard
  │
  ├── setProposalStatus(msgIndex, id, 'applying')   — button shows spinner
  │
  ├── body = { [proposal.field]: proposal.suggested }
  │
  ├── if targetType === 'orchestrator':
  │     themApi.updateOrchestrator(targetId, body)
  │     → PATCH /api/v1/admin/orchestrators/{id}
  │     → bridge handler calls _invalidate(name)
  │       → publishes to Redis _CHANGE_CHANNEL
  │       → all replicas bust their in-process cache
  │       → busts them:orchestrators:{name} key
  │
  │   if targetType === 'agent':
  │     themApi.updateAgent(targetId, body)
  │     → PATCH /api/v1/admin/agents/{id}
  │     → bridge handler calls invalidate_registry()
  │       → busts them:agents:registry key
  │
  ├── reflectProposalOnCanvas(proposal, updatedRow)
  │     Updates the parent-level orchestrators/agents arrays
  │     (so a re-analyze sees the new value in serializeWorkflow)
  │     Updates canvas node data for display_name, description,
  │     max_parallel_tools (fields that live on the node itself)
  │
  ├── setProposalStatus(msgIndex, id, 'applied')    — button shows "Applied ✓"
  └── showToast("Applied: <field> on <target>", ok)
```

**Cache invalidation is automatic** — the existing PATCH handlers on the bridge bust Redis immediately. No extra endpoint is needed.

---

## Multi-turn follow-up

After the initial analysis, the user can ask follow-up questions in the chat input:

- "Suggest a better prompt for the research orchestrator"
- "Why do descriptions need to be distinct?"
- "Rewrite the agent_evidence description"

Each follow-up opens a new WebSocket to `workflow_advisor` with the same `context_id`. The orchestrator's `history_window = 10` keeps the prior exchange in context, so the advisor can reference earlier analysis and emit new proposal blocks in follow-up turns.

---

## Re-analyze

The **↻** button in the advisor header clears all messages and `context_id`, then triggers a fresh scan. After applying proposals, re-analyzing shows the updated values in the new advisory.

---

## Stale proposals

If the canvas changes (or proposals are applied) since a proposal was generated, the proposal may be stale — its `current` value no longer matches reality. The frontend detects this on Apply by comparing `proposal.current` against the live value in the orchestrators/agents list. If they differ, the card shows "Canvas changed since analysis" in amber and still allows applying (the suggested value is still valid even if current changed).

---

## Container and config

| Item | Value |
|---|---|
| Container | `them-workflow-advisor` |
| Port (internal) | 9600 |
| Source | `agents/workflow_advisor/` |
| LLM model | `claude-sonnet-4-6` (via `ANTHROPIC_MODEL` env var) |
| A2A endpoint | `http://them-workflow-advisor:9600` |
| Orchestrator name | `workflow_advisor` |
| Agent slug | `workflow_advisor` |
| Docker profile | core (always running) |

Rebuild after code change:
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml build them-workflow-advisor
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d them-workflow-advisor
```

---

## Key source locations

| Concern | File | Notes |
|---|---|---|
| LLM system prompt + orchestration knowledge | `agents/workflow_advisor/advisor.py` `_SYSTEM_PROMPT` | Edit here to improve advisory quality |
| Workflow graph serialization | `agents/workflow_advisor/advisor.py` `_build_analysis_prompt()` | Converts graph dict → structured text |
| Streaming LLM call | `agents/workflow_advisor/advisor.py` `stream_analysis()` | Async generator; runs in thread via `asyncio.to_thread` |
| A2A executor | `agents/workflow_advisor/main.py` `WorkflowAdvisorExecutor` | Emits task state events + artifact chunks |
| Canvas serialization | `frontend/…/applications/page.tsx` `serializeWorkflow()` | Resolves agent IDs to slugs; includes maxIterations, historyWindow |
| Proposal parser | `frontend/…/applications/page.tsx` `parseAdvisorBuffer()` | Pure function; safe to unit-test |
| Proposal merge | `frontend/…/applications/page.tsx` `mergeProposals()` | Preserves applied/failed status across re-renders |
| Apply handler | `frontend/…/applications/page.tsx` `applyProposal()` | PATCH → refresh lists → reflect on canvas |
| Apply all | `frontend/…/applications/page.tsx` `applyAll()` | Sequential (avoids concurrent writes to same orchestrator) |
| Canvas/list refresh | `frontend/…/applications/page.tsx` `reflectProposalOnCanvas()` | Updates parent state + node data |
| Proposal UI | `frontend/…/applications/page.tsx` `ProposalCard` | Status machine: pending → applying → applied/failed/stale |
| Panel UI | `frontend/…/applications/page.tsx` `AdvisorPanel` | Chat + proposal cards + Apply All button |

---

## Extending the advisor

**To improve advisory quality** — edit `_SYSTEM_PROMPT` in `advisor.py`. The prompt is the single source of truth for what the advisor knows. Add new implications, examples, or patterns. Rebuild the container.

**To add a new proposal type** — add the `field` to `PROPOSAL_ALLOWED_FIELDS` (frontend, line ~73), add it to `FIELD_LABEL` / `FIELD_ICON`, handle the canvas reflect case in `reflectProposalOnCanvas`, and update the system prompt's valid type/field table.

**To add a new field to the serialized graph** — update `serializeWorkflow()` in the frontend and `_build_analysis_prompt()` in the advisor. They must stay in sync.
