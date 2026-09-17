# Inline Nodes Design Brief
# the-M — App Canvas Inline Execution Nodes
# For: Opus design session
# Date: 2026-09-17

---

## Context & Goal

the-M is a multi-agent orchestration platform. The app canvas today lets users wire
together pre-built agents into a flow (via A2A calls). The goal is to add **inline nodes**
— LLM calls, tool calls, conditions — that run directly inside the flow without needing
a separate agent. This makes the app canvas capable enough to eventually replace the
separate agent builder.

**Execution model decision (already made):**
- Simple linear flows → local (in-process inside `them-go-bridge`)
- DAG / parallel / durable flows → Temporal (`them-dag-worker`)
- Inline nodes must work in **both** modes
- Do NOT build a custom DAG execution engine — Temporal handles that

---

## Current State

### Backend — compiler (`go/internal/appflow/compiler.go`)

`AppFlowNode.Kind` values today:
- `"agent"` — calls an external A2A agent
- `"middleware"` — pre/post filter (file guard etc.)
- `"router"` — LLM-judged routing between branches
- `"hil"` — human-in-the-loop pause
- `"fork"` — split into parallel branches
- `"join"` — merge parallel branches
- `"orchestrator"` — legacy local loop orchestrator

`AppFlowNode.Config json.RawMessage` already exists — ready to carry inline node config.

**Missing kinds:** `"llm"`, `"tool"`, `"condition"`, `"transform"`

### Backend — workflow (`go/internal/appflow/workflow.go`)

- Switch on node kind → dispatches to Temporal activity
- Each node: `*ActivityInput` / `*ActivityOutput` pair, registered by name string
- `accumulated string` is the data bus between nodes (current output passed forward)
- `walkBranch()` handles fork branches — currently only agent/orchestrator/middleware
- Unknown kind → non-retryable error (good — fails fast)

### Backend — dag-worker (`go/cmd/dag-worker/main.go`)

Already wired:
- `multiLLMFactory` supporting: mock, anthropic, openai, groq, ollama, vllm, lmstudio
- `AppFlowActivities` has `LLMCaller`, `DB`, `StatusUpdater`, `StreamPub`, `AgentInvoker`
- Key resolution infrastructure already in place (per-app encrypted keys)

**The LLM calling infrastructure is already there — inline LLM nodes can reuse it.**

### Frontend — types (`frontend/src/app/admin/applications/types.ts`)

`CanvasNodeData` union today:
- `OrchNodeData | AgentNodeData | MwNodeData | EpNodeData | FlowControlNodeData`

Missing: `LlmNodeData`, `ToolNodeData`, `ConditionNodeData`

### Frontend — API types (`frontend/src/lib/apiTypes.ts`)

`ComponentDefinitionSummary.kind`: `'orchestrator' | 'agent' | 'middleware' | 'entry_point' | 'tool'`
`ConnectionDef.type`: `'entry' | 'delegation' | 'tool' | 'middleware' | 'flow_control'`

Both need extending for inline nodes.

`AppDefinitionDoc.execution_backend`: `'local' | 'temporal'` — already exists.

---

## What Needs Designing

### 1. Node type catalogue — what inline nodes to add first?

Candidates (priority order):
1. **LLM node** — call an LLM with a prompt template, output goes to next node
2. **Condition node** — branch based on expression or LLM judge on previous output
3. **Tool/MCP node** — call a specific MCP tool inline
4. **Transform node** — reshape/extract data (JSONPath, regex, template)

Questions for Opus:
- Which of these to build in Phase 1?
- Should Condition replace/extend Router, or coexist?
- What is the input/output contract for each node? (just `string`, or structured?)

### 2. Data model — how does config flow from canvas → compiler → workflow?

Today: `AppFlowNode.Config json.RawMessage` exists but is only used by Router.

For an LLM node, config needs:
- `provider` (or "inherit from app keys")
- `model`
- `system_prompt`
- `prompt_template` (can reference `{{input}}` = previous node output)
- `max_tokens`, `temperature`

For a Condition node:
- `expression` (e.g. `input contains "yes"`) or `llm_judge: true`
- `true_edge` label, `false_edge` label

Questions for Opus:
- Should inline node config be stored in `ComponentInstance.config` in the definition JSON?
- Or should inline nodes NOT be `ComponentDefinitionSummary` references at all — just raw nodes in the definition?
- How does the canvas serialise inline nodes that have no agent/orchestrator backing them?

### 3. Execution — local vs Temporal

**Temporal mode (dag-worker):**
- Each inline node = a new Temporal activity
- LLM node → `InlineLLMActivity` registered in dag-worker
- Condition node → `InlineConditionActivity`
- dag-worker already has LLM factory wired — this is straightforward

**Local mode (in-bridge):**
- Need a local runner that walks the compiled graph and executes inline nodes in-process
- Streaming works naturally here
- Currently `them-go-bridge` has no graph walker — it submits to Temporal or runs via orchestrator
- **This needs a new local execution path in the bridge**

Questions for Opus:
- Should local mode use the same `AppFlowNode` compiled graph as Temporal?
- Or separate compilation for local?
- Where exactly does the local runner live in `go/`? New package? Extension of existing?
- How does local mode handle streaming output from an LLM node to the WS/SSE client?

### 4. Canvas UI — what does an inline node look like?

Today: nodes are either agents (reference to a registered agent) or flow control (fork/join/router/hil).

Inline nodes have no external reference — they are self-contained config on the canvas.

Questions for Opus:
- Should inline nodes appear in the node library as a new section ("Inline / Logic")?
- What's the visual distinction from agent nodes?
- How does the user configure the prompt template / expression — inline panel or side drawer?
- Should the canvas validate that inline node config is complete before allowing publish?

### 5. Validation & publish

Today: `Validate()` in compiler checks edge counts for fork/join, cycles, orphan nodes.

New validation needed:
- LLM node: must have `provider` set (or app must have that provider key configured)
- Condition node: must have exactly 2 outgoing edges labelled `true` / `false`
- Tool node: must reference a valid MCP server attached to the app

### 6. Connection type

Today `ConnectionDef.type` has `'flow_control'` for router/hil/fork/join edges.

Inline node connections: should they be `'inline'`? Or just `'delegation'`?
This affects canvas rendering (edge style) and compiler routing.

---

## Key Files to Read Before Designing

| File | Why |
|---|---|
| `go/internal/appflow/compiler.go` | Full compiler — node kinds, Config usage |
| `go/internal/appflow/workflow.go` | Activity dispatch pattern — how to add new node kinds |
| `go/cmd/dag-worker/main.go` | LLM factory, activity registration |
| `go/internal/admin/dal/router_llm.go` | Existing LLM-judge for Router — reuse pattern |
| `frontend/src/app/admin/applications/types.ts` | Canvas node data model |
| `frontend/src/app/admin/applications/components/CanvasNodes.tsx` | Node rendering |
| `frontend/src/app/admin/applications/components/CanvasHelpers.ts` | canvasToDoc / docToCanvas serialisation |
| `frontend/src/lib/apiTypes.ts` | AppDefinitionDoc, ComponentDefinitionSummary |
| `docs/CURRENT.md` | Current HEAD and deployment state |
| `docs/SCHEMA.md` | DB schema |
| `go/CLAUDE.md` | Go coding rules |

---

## Constraints

- Follow `CLAUDE.md` and `go/CLAUDE.md` at all times
- No new DB tables unless absolutely necessary for inline node config storage
- Inline node config lives in the definition JSON (already a jsonb column) — not a new table
- Brand: the-M / them / THE_M_ — never odin
- Every Go change needs tests; `go/TEST_INDEX.md` must be updated
- Keep files under 400 lines — split by responsibility

---

## Recommended Design Output

A plan covering:
1. Exactly which node kinds to implement in Phase 1 (suggest: LLM + Condition)
2. Data model for inline node config in the definition JSON
3. Compiler changes (new `compileNode` cases)
4. Temporal activity design (signatures, input/output)
5. Local execution path design (where it lives, how streaming works)
6. Frontend: new node types, canvas serialisation, properties panel
7. Validation rules
8. Test plan

Write the plan to `docs/INLINE_NODES_PLAN.md` before touching any code.
