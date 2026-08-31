# Go ADK Runtime Evaluation
# the-M Agent Runtime — Architecture Investigation
# Date: 2026-08-24
# Author: Engineering (Claude-assisted)

---

## Summary

This document evaluates whether Google's Agent Development Kit Go SDK (`google/adk-go`) could replace or optionally augment the current the-M agent runtime, with specific focus on Temporal integration.

**Verdict: Not a short-term adoption target.** Critical blockers exist for every layer of the current stack. The A2A wire format is the one area of immediate, zero-cost interoperability.

---

## What We Have Today

The current the-M agent runtime is a custom, spec-driven execution engine:

| Layer | Implementation |
|---|---|
| Agent authoring | JSON `AgentSpec` — skills, steps, edges |
| Step node types | `input`, `llm`, `transform`, `http`, `branch`, `response` |
| Interpreter | Go step-graph walker (`go/internal/agentgen/interpreter.go`) |
| Transform DSL | 30+ functions: `strip_fences`, `json_path`, `upper`, `concat`, `coalesce`, etc. |
| Auth injection | Per-node `app_param_key` → `inject_mode` (header/query/basic/custom) |
| Durable execution | Temporal workflow wrapping the interpreter |
| Entry points | WebSocket, SSE, A2A (JSON-RPC 2.0) |
| Session storage | Postgres + Redis |
| LLM provider | Anthropic (Claude), configurable per-node |

Key property: **agents are data, not code**. A user can build, validate, publish, and deploy an agent by editing JSON in the canvas UI — no code recompile required.

---

## What Go ADK Is

**Repo:** `github.com/google/adk-go`
**Module:** `google.golang.org/adk/v2`
**Version:** v2.2.0 GA (August 10, 2026), Apache 2.0
**Origin:** Google, parallel to Python ADK (Python was first; Go has feature lag)

Go ADK is a **code-first** framework for building LLM-powered agents optimized for Google Cloud (Gemini, Vertex AI, Agent Engine, Cloud Run). It is not spec-driven — agents are defined in Go code or YAML, not importable JSON.

### Agent types

| Type | Description |
|---|---|
| `LlmAgent` | LLM with tools, instructions, callbacks |
| `SequentialAgent` | Linear sub-agent pipeline |
| `LoopAgent` | Iterative with exit-loop tool |
| `ParallelAgent` | Concurrent sub-agents |
| `RemoteAgent` (A2A) | Proxies to A2A-compliant remote agent |
| Custom | Implements `agent.Agent` interface |

### Workflow graph node types (v2.0+)

| Node | Description |
|---|---|
| `FunctionNode` | Typed Go function; schema inferred via generics/reflection |
| `EmittingFunctionNode` | FunctionNode that streams events or triggers HITL pauses |
| `AgentNode` | Embeds any `agent.Agent` |
| `ToolNode` | Promotes a tool to a graph node |
| `JoinNode` | Fan-in barrier for parallel paths |
| `DynamicNode` | Imperative orchestration via Go code |
| `WorkflowNode` | Embeds another workflow as a single node |

---

## Temporal Integration — The Key Question

**Go ADK has no first-party Temporal integration.**

The official ADK integration docs (`adk.dev/integrations/temporal/`) explicitly state: *"Supported in ADK Python."* The Go SDK is absent from the support matrix.

The Python integration (`temporalio.contrib.google_adk_agents`, Temporal Python SDK 1.24.0+) is itself **experimental**. Architecture:
- ADK agent loop runs inside a `@workflow.defn` Temporal Workflow
- LLM calls run as Temporal Activities (non-deterministic ops isolated)
- HITL via Temporal Signals

For Go: there are community-contributed samples wiring ADK agents into Temporal manually, but no ADK scaffolding. This is the same amount of work we already do in `go/internal/temporal/`.

---

## Gap Analysis — Go ADK vs Current Stack

| Our component | ADK equivalent | Gap |
|---|---|---|
| JSON `AgentSpec` (agent authoring) | YAML config or Go code | **ADK cannot import our JSON format** — no bridge |
| Step-graph interpreter | `workflow` graph engine | Different node model; not spec-driven |
| `llm` node | `AgentNode` wrapping `LlmAgent` | Equivalent capability |
| `http` node with auth injection | `FunctionNode` (custom) | **No built-in HTTP step** — must implement from scratch |
| `transform` node + 30 functions | `FunctionNode` (custom) | **No transform DSL** — all functions need re-implementation |
| `branch` node | `StringRoute`/`BoolRoute` edges | Equivalent, different syntax |
| Temporal (durable execution) | **Not provided in Go ADK** | Wire Temporal yourself — same work as today |
| WebSocket entry point | **Not supported** | ADK exposes REST/SSE/A2A only |
| SSE entry point | `launcher/web/api` streaming | Supported |
| A2A serving | `launcher/web/a2a` | **Fully supported**, same protocol |
| A2A consuming | `remoteagent.NewA2A` | **Fully supported** |
| Claude/Anthropic provider | **Not built in** | Must implement `model.Model` interface |
| Postgres session storage | Custom `session.Service` impl | ADK backends are GCS/Vertex AI — Postgres requires writing the interface |

---

## What Adoption Would Actually Require

Full adoption of Go ADK as the runtime engine, while keeping the JSON spec as the authoring format, requires:

1. **Spec compiler** — A bridge that reads `AgentSpec` JSON at startup and emits ADK `workflow.Graph` constructs. Highest-risk piece: our node types don't map 1:1 to ADK nodes.

2. **Transform re-implementation** — All 30+ functions (`strip_fences`, `json_path`, `upper`, `concat`, `coalesce`, etc.) reimplemented as Go `FunctionNode` callables. Net-new code, not a migration.

3. **HTTP step FunctionNode** — Full `http` node semantics: URL templating, auth param injection (header/query/basic/custom), response variable binding.

4. **Temporal wiring** — Wrap ADK `runner` invocations in Temporal Workflow + Activity shell. Same work as today, different target.

5. **WebSocket transport** — Keep our existing WebSocket layer; translate between WS and ADK's REST/SSE runner API. ADK cannot natively serve WebSocket.

6. **Anthropic model provider** — Implement `model.Model` interface for Claude API. Well-defined interface but non-trivial work.

7. **Postgres session backend** — Implement `session.Service` over Postgres. ADK ships GCS and Vertex AI backends only.

**Conclusion:** The migration recreates most of what we already have, wrapped in ADK conventions. The spec-as-data property (agents editable as JSON, deployable without recompile) is structurally incompatible with ADK's code-first model.

---

## What Go ADK Does Better Than Us

Being honest about the real benefits:

| Benefit | Notes |
|---|---|
| HITL (Human-in-the-loop) | `EmittingFunctionNode` with pause/resume — we'd need to build this |
| Fan-out / fan-in | `ParallelAgent` + `JoinNode` — our interpreter is sequential only |
| Built-in A2A serving + consuming | Fully supported; we built this ourselves |
| OpenTelemetry traces | Built in; we have basic slog logging |
| Agent Engine / Cloud Run deployment | Out-of-scope for our on-prem stack |
| YAML-driven agent config | Simpler for code-first agents; we prefer JSON spec |

---

## Where We Are Interoperable Today — Zero Cost

**A2A wire format.** Our agentgen agents serve `/a2a` (JSON-RPC 2.0) with `.well-known/agent.json` agent cards. Any ADK agent using `remoteagent.NewA2A` can call our agents as remote skills today, with no changes required on either side.

This is the one area of immediate value: ADK-built agents (by us or third parties) can be registered as skills in our runtime, and our published agents can be consumed by ADK-powered orchestrators.

---

## Recommended Decision

### Do not adopt Go ADK as the runtime engine (now or near-term)

Blockers that are not workaroundable:
- No first-party Temporal + Go ADK integration
- No WebSocket transport
- No Anthropic model provider
- No JSON spec importer — incompatible with our spec-as-data model
- Breaking change cadence (v2.0 changed imports, session API, event construction)

### Keep our native runtime

The current interpreter (`go/internal/agentgen/interpreter.go`) + Temporal wrapper is the right foundation. It is spec-driven, supports Claude, supports WebSocket/SSE/A2A, and runs on-prem without Google Cloud dependencies.

### Use A2A interoperability where it adds value

Register ADK-built agents as external skills in our runtime via A2A. This gives us access to ADK's richer agent types (parallel, loop, HITL) without migrating the runtime.

### Revisit if any of these change

| Trigger | Re-evaluate |
|---|---|
| Official Go ADK Temporal integration reaches GA | Temporal migration effort drops significantly |
| ADK Go adds WebSocket transport | Entry point migration becomes possible |
| ADK Go adds Anthropic model provider | LLM layer migration becomes possible |
| We decide to abandon spec-as-data in favor of code-first agents | Full ADK adoption becomes reasonable |

---

## Optional Path — Dual Runtime (Future)

If we ever want to offer Go ADK as an alternative execution backend while keeping the JSON spec authoring format, the architecture would be:

```
AgentSpec (JSON)
      │
      ▼
  Compiler
      │
  ┌───┴──────────┐
  │              │
  ▼              ▼
Native       ADK Graph
Interpreter  (compiled from spec)
  │              │
  └──────┬───────┘
         │
     Temporal
    Workflow
```

The compiler (`go/internal/agentgen/compiler.go`) would gain an ADK backend alongside the current native interpreter backend. Agents opt in via a `runtime: adk` field in the spec.

This is a multi-sprint effort. It makes sense only after the ADK Temporal + Anthropic gaps are closed upstream.

---

## Files Referenced

| File | Role |
|---|---|
| `go/internal/agentgen/interpreter.go` | Current step-graph interpreter |
| `go/internal/agentgen/transform/functions.go` | Transform function library |
| `go/internal/agentgen/spec.go` | `AgentSpec` JSON schema types |
| `go/internal/agentgen/compiler.go` | Spec → compiled pipeline |
| `go/internal/temporal/workflow.go` | Temporal workflow wrapper |
| `go/internal/a2a/server.go` | A2A JSON-RPC server (interop point) |
