# Canvas Builder — Live Debug Mode
# Status: IN PROGRESS — Phase 1 building
# Last updated: 2026-08-20

---

## Overview

Live debug mode lets users test a skill pipeline directly in the builder without publishing or binding to an application. Two execution modes: **Run All** (execute then replay the data flow visually on the canvas) and **Step Through** (execute one step at a time, inspect and override values between steps).

No backend changes required. All execution is client-side: LLM calls go directly to the Anthropic API using a debug API key the user provides.

---

## UI Layout

```
┌─────────────────────────────────────────────────────────────┐
│ TOOLBAR  [Back] [Agent Name]  ············  [🐛 Debug] [Save]│
└─────────────────────────────────────────────────────────────┘
┌──────────┬──────────────────────────────────┬───────────────┐
│          │                                  │               │
│  LEFT    │   📥 ──"hello"──→ 🧠 ──"Paris"──→ 📤            │
│  PANEL   │   [hello]        [Paris]   [Paris]│  PROPERTIES   │
│  (hidden │                                  │  shows full   │
│  in dbg) │                                  │  in/out when  │
│          │                                  │  node clicked │
│          ├──────────────────────────────────┤               │
│          │  DEBUG BAR                       │               │
│          │  [▶ Run All] [⏭ Step] [⏹ Reset] │               │
│          │  Input: [________________]       │               │
│          │  API Key: [••••••••••••••]       │               │
│          │  ● idle / ▶ running / ✓ done     │               │
└──────────┴──────────────────────────────────┴───────────────┘
```

---

## Where Users See Return Values

### On the canvas (primary)
- **Each edge** shows a value bubble after data flows through it: `"hello"` on Input→LLM edge, `"Paris"` on LLM→Response edge
- **Each node** shows a small output box below its emoji: the actual value it produced
- Data stays visible on the canvas until Reset is pressed

### In the properties panel (detail)
- Clicking any node after a run shows two sections: **Debug Input** and **Debug Output** — the full values the step received and produced, not truncated

### Debug bar (summary)
- Status indicator: idle / running / done / error
- No JSON blob — the canvas is the visualization

---

## Manual Data Insertion

Two levels of manual control:

### 1. Entry point (Input step)
User types the test message in the debug bar Input field before running. This becomes the value of the Input step's variable.

### 2. Per-step override (Step Through mode)
Before each step executes, the user sees what values it will receive and can edit them:

```
⏭ Step pressed →
  Panel shows: "LLM will receive:"
               {{input}} = [hello        ] ← editable
  ⏭ Step pressed again → LLM runs with edited value
```

This lets users test edge cases without re-running from the start. Like a debugger's "set variable" feature.

---

## Node Visual States

| State | Visual |
|---|---|
| `idle` | no change |
| `pending` | pulsing amber border — next to execute |
| `running` | pulsing blue border |
| `done` | solid green border + output value shown below emoji |
| `error` | solid red border + error message below emoji |

---

## Edge Visual States

| State | Visual |
|---|---|
| `idle` | default grey |
| `active` | animated cyan glow + value bubble label |
| `error` | red |

---

## State Shape

```typescript
type DebugNodeState = 'idle' | 'pending' | 'running' | 'done' | 'error';

interface DebugState {
  active: boolean;
  mode: 'run-all' | 'step' | null;
  testInput: string;
  apiKey: string;                              // session only, never persisted to server
  vars: Record<string, unknown>;              // live pipeline variables
  nodeStates: Record<string, DebugNodeState>;
  nodeOutputs: Record<string, string>;        // per node: display value shown on canvas
  nodeErrors: Record<string, string>;         // per node: error message if failed
  edgeValues: Record<string, string>;         // per edge: value label shown on edge
  executionOrder: string[];                   // topologically sorted node IDs
  currentStepIndex: number;                   // step-through cursor
  pendingVarOverrides: Record<string, string>; // step-through: user edits before step runs
  error: string | null;
}
```

---

## Execution Model

### Topological Sort
Pipeline graph sorted source-first following edges. Cycles detected and shown as errors before running.

### Variable Store
Single `vars` object passed through pipeline. Each step reads and writes it.

```
Initial:      vars = {}
After Input:  vars = { input: "hello" }
After LLM:    vars = { input: "hello", output: "Paris" }
After Response: pipeline complete, final = vars[from_var]
```

### Step Executors (client-side)

| Step | Executor |
|---|---|
| `input` | Writes `testInput` to `vars[bindings.text \|\| 'input']` |
| `llm` | Renders user_prompt template → POST to Anthropic API → writes to `vars[output_var \|\| 'output']` |
| `http` | fetch() with rendered URL/body → writes to `vars[output_var]` |
| `transform` | Template expression eval → writes vars |
| `response` | Reads `vars[from_var \|\| 'output']` → marks done |

### Template Rendering
`{{variable_name}}` → `String(vars['variable_name'] ?? '')` — applied before every LLM/HTTP call.

---

## Mode 1 — Run All

1. User fills Input + API Key → clicks ▶ Run All
2. Execute all steps sequentially (no pause)
3. On completion: animate data flow across canvas
   - Edges light up in topological order (150ms stagger)
   - Value bubble appears on each edge
   - Each node shows its output value below emoji
4. Properties panel shows full in/out for selected node

---

## Mode 2 — Step Through

1. User fills Input + API Key → clicks ⏭ Step
2. First pending step highlighted amber
3. Debug bar shows incoming vars for that step — user can edit values
4. ⏭ Step again → step executes with (possibly overridden) vars
5. Node turns green, edge lights up with value, next step turns amber
6. Repeat until pipeline complete

---

## Implementation Phases

### Phase 1 — Foundation ✓ (in progress)
- Debug state shape
- 🐛 Debug toggle in toolbar
- Debug bar at bottom of canvas (Input, API Key, buttons, status)

### Phase 2 — Execution Engine
- `topoSort(nodes, edges)`
- `renderTemplate(template, vars)`
- Step executors for: input, llm, response
- Run All flow
- Step Through flow with var override

### Phase 3 — Canvas Visualization
- Node debug overlay (output value + colored border)
- Custom edge type with value bubble label
- Animation sequencing for Run All

### Phase 4 — Properties Panel Debug Detail
- Debug Input / Debug Output sections when node selected in debug mode

### Phase 5 — Polish
- sessionStorage for API key (cleared on tab close)
- Cycle detection with clear error
- HTTP + Transform executors
- API key warning message

---

## Security
- Debug API key: React state + sessionStorage only
- Never sent to the-M backend
- Direct browser → Anthropic API calls
- Never logged
- Clear warning shown to user in UI

---

## Files Changed
| File | Change |
|---|---|
| `frontend/src/app/admin/agents/builder/page.tsx` | All debug state, bar, executors, canvas visualization |
| `docs/architecture-v2/CANVAS_DEBUG_MODE_PLAN.md` | This doc |
