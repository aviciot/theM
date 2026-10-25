---
title: A2A Agents & Agent Builder (agentgen, agent-runtime, inbound A2A)
type: architecture
status: current
owns:
  - go/internal/agentgen/
  - go/cmd/agent-runtime/
  - go/internal/a2a/
  - go/internal/agentregistry/
  - go/internal/temporal/canvas_workflow.go
  - go/internal/temporal/canvas_activities.go
  - go/internal/temporal/temporal_executor.go
  - go/internal/admin/canvas_tasks.go
  - go/internal/admin/service/agent_definitions_publish.go
last_verified: 2026-10-24   # by reading code; nothing run live this session
---

# A2A Agents & Agent Builder

## Purpose
Three related things that all speak the **A2A protocol** (JSON-RPC 2.0, via the official
`github.com/a2aproject/a2a-go/v2` SDK):
1. **Canvas agents** — agents drawn in the Agent Builder, compiled to an `AgentSpec`, and served by one generic binary, `them-agent-runtime`.
2. **Outbound calls** — `agentregistry` lets orchestrators (and AppFlow agent nodes) call any registered agent over A2A.
3. **Inbound A2A** — `internal/a2a` exposes an application entry point as an A2A agent to outside callers.

## How it works

```
Agent Builder UI → them.agent_definitions (canvas JSON, drafts/revisions)
   │ validate / publish (admin/service/agent_definitions_publish.go → agentgen.Compile)
   ▼
them.agent_runtime_specs (AgentSpec)  +  them.component_definitions + them.agents (transport canvas_a2a,
   endpoint http://them-agent-runtime:9300/agents/{slug})   ── pub/sub them:agents:changed ──► registry caches
   ▼
caller (orchestrator / AppFlow agent node / a2a_call step)
   POST http://them-agent-runtime:9300/agents/{slug}   headers X-Them-Tenant-Id / -Application-Id / -Agent-Id / -Binding-Id
   ▼
agent-runtime.handle:  identity from headers → spec by (tenant, agent) (60s cache) → slug cross-check
   → binding (tenant+app+agent, definition_id pin) → load app provider keys, app params, agent params
   → a2asrv JSON-RPC handler → executeSkill → LocalExecutor (default) or TemporalExecutor
   ▼
events Submitted → Working → Artifact(text) → Completed|Failed; tasks kept in Redis
```

### Definition → spec (`agentgen/compiler*.go`, `plan_compiler.go`)
Canvas JSON `{agent_root, skills[]}` is validated and compiled (`Validate`, `CompileForPublish`, `Compile`) to `AgentSpec{ID, DefinitionID, Slug, TenantID, Card, Skills[], RequiredParams, ExecutionBackend,…}`; each skill is a topologically sorted list of `StepSpec` with declared inputs/outputs (contract), `Next`, branch arms. Issues carry severity (`error` blocks publish, `warning` advisory). Slugs are `^[a-z0-9_]{1,48}$`. **Publish writes `agent_runtime_specs`, `component_definitions` and `agents` with one shared UUID in one transaction** (`agents.id → component_definitions.id`), sets `transport='canvas_a2a'` and the runtime endpoint URL. The generic `CreateAgent` path does not do this.

### Node (step) types (`agentgen/noderegistry.go`, `nodes*.go`)
`input`, `llm`, `http`, `transform`, `response`, `branch`, `loop`, `parallel`/join (fan-out), `a2a_call`, `mcp_call`, `human_wait`, `stream_out`. Each is a registered `NodeDef` with metadata (served by `GET /admin/node-types`) and an `Execute`. Each step reads only its declared inputs and promotes only declared outputs (`ErrContractViolation` on a missing required input); steps with no contract fall back to the global vars. Templates are Go `text/template` over pipeline vars.
- `llm`: provider key from the app's `provider_keys`, per-node overrides in `config_overrides.llm_nodes`, else platform key.
- `http`: URL/body rendered from vars; auth injected from an app-global param (`app_param_ref`) or per-binding param (`app_param_key`) via `header|query|basic|custom_header`.
- `a2a_call`: resolves the target by `(tenant, application, slug)` through the registry, self-call rejected, depth capped at **3** via `X-Them-A2A-Depth`.
- `mcp_call`: `POST {MCP_SERVICE_URL}/internal/execute` with the application ID (see `mcp-service.md`).

### Execution backends
- **Local** (`local_executor.go`): in-process DAG runner with fan-out goroutines, join state, per-node retry policy, loops.
- **Temporal** (`TemporalExecutor`, `temporal/canvas_workflow.go`): when the spec says `execution_backend=temporal`. `CanvasAgentWorkflow` on queue `canvas-dag-nodes` (workflow ID `{tenant}:canvas:{agent}:{invocationID}`, timeout 12 min, 24 h with a `human_wait`); each node is an `ExecuteStepActivity` that rebuilds credentials from the DB (`ContextLoader`) so **no secrets enter workflow history**. Fails closed (task `failed`) if the spec wants Temporal but the runtime has none.
- **Human-in-the-loop**: a plan with `human_wait` is submitted asynchronously (detached 24 h context). Redis `them:hitl:{taskID}` stores workflow handle + wait token; `HITLRequestHandler` polls the workflow query `hitl_status` to emit `input-required`/`completed` on `GetTask`/`SubscribeToTask`. Humans answer through `POST /admin/canvas-tasks/{task_id}/signal` (tenant admin; tenant checked against the handle, optional `wait_token`, compare-and-set before signalling signal `human_input:{stepID}`).

### Runtime details (`cmd/agent-runtime`)
Routes: `GET /healthz`, `GET /agents/{slug}/.well-known/agent-card.json`, `POST /agents/{slug}` (SDK handler: message/send, message/stream, tasks/get|cancel|resubscribe, push-config methods). `message` parts: first `text` → `vars.input`; each key of a `data` part → a var. Skill chosen by `message.metadata.skill_id` else the first skill; binding policy `AllowedSkillIDs` is enforced. Output is a single text artifact. Tasks stored in Redis (`them:agent:a2atask:{id}`, 24 h); spec cache 60 s.

### Outbound registry (`agentregistry`)
Per-tenant L1 (in-process) + L2 Redis `them:agents:registry:{tenant}` (10 min), invalidated by `them:agents:changed`. Transports: `a2a_async`/`canvas_a2a` (A2A over HTTP, 3 min timeout; streaming agents use `SendStreamingMessage`), `http`, `mock`. For canvas agents it adds the `X-Them-*` headers from the app's binding.

### Inbound A2A (`internal/a2a`)
`POST /{tenant}/a2a/{app}/{ep}` and `GET …/.well-known/agent.json`. For `SendMessage`/`SendStreamingMessage`: buffer body (1 MiB), run the full `Lifecycle.Admit` (see `run-lifecycle.md`), then the SDK handler with an executor that starts the orchestration workflow and maps run-stream events to A2A task events. Task state is the shared Redis A2A task store.

## Key invariants
- Credentials never live in `AgentSpec`/definition JSON (slot/param **names** only); values are decrypted per request from `app_agent_bindings`/`applications`, never logged (`InvocationContext.String()` masks).
- Binding stale-check: a binding pinned to a `definition_id` that differs from the spec's means "re-publish the application" (409).
- URL slug must equal the spec's slug for the identity in the headers (prevents slug squatting).
- Canvas workflow activities carry only credential-free input; secrets are re-loaded inside the activity.
- Every DB lookup in the runtime carries `tenant_id` explicitly (the runtime uses the Admin pool, so RLS does not help).

## Failure modes
| What fails | Effect |
|---|---|
| Redis | Task store/HITL unavailable → message/send errors; spec cache still serves 60 s |
| Temporal down / not enabled on the runtime | `execution_backend=temporal` agents fail closed |
| Callee agent down | `a2a_call` / AppFlow agent node error → pipeline fails |
| Binding missing or stale | 404 / 409 from the runtime |
| Spec edited | Up to 60 s of the old spec (cache) |
| `human_wait` unanswered | Workflow times out after its `timeout_seconds` or 24 h; handle TTL 24 h |

## Known gaps (found while writing, 2026-10-24; from reading code; none reproduced live)
See `docs/plans/code-findings-backlog.md` F-67…F-77. Highlights:
1. **agent-runtime has no real authentication.** Identity is just the `X-Them-*` headers (comment: "Phase 3 will sign them"); any `Authorization` header is ignored. Anyone on `them-network` can run any tenant's agent with that app's stored credentials.
2. **Task access is not tenant-checked.** The live task store (`RedisA2ATaskStore`) keys by task ID only and never checks tenant/app/user; the inbound A2A server skips admission entirely for `GetTask`/`CancelTask`/push-config calls, so those need no token. The ownership-checking `RedisTaskStore` the older docs describe is not the one in use. HITL handles also carry a tenant that the runtime never compares.
3. **HTTP step SSRF and credential exfiltration:** URL templates can include caller data, there is no URL allow-list, redirects are followed, and the app credential is injected into whatever URL results.
4. Agent card endpoint on the runtime is unauthenticated and looks up the spec by slug only (not by tenant).
5. Inbound A2A: the bearer token's tenant silently overrides the URL slug's tenant (WS/SSE do the opposite); card path is `agent.json` while the runtime uses `agent-card.json`.
6. `.claude/skills/a2a.md` is stale (says hand-rolled dispatch, SDK not adopted, unimplemented step types, `a2a_async`, ownership-checked task store) — do not treat it as ground truth.

## Where to change things
| To… | Edit |
|---|---|
| Add a step type | `agentgen/spec.go` (type + config), `noderegistry.go`/`nodes*.go` (def + `Execute`), `compiler_validate.go`, `plan_compiler.go`; if Temporal-capable also `temporal/canvas_*.go` |
| Change publish | `admin/service/agent_definitions_publish.go`, `admin/dal/agent_definitions_publish.go` |
| Change runtime request handling | `cmd/agent-runtime/runtime.go`, `spec.go` |
| Change outbound calling/caching | `agentregistry/registry.go` |
| Change inbound A2A | `a2a/server.go`, `executor.go`, `card.go` |
| Change HITL | `agentgen/hitl_store.go`, `cmd/agent-runtime/hitl.go`, `admin/canvas_tasks.go`, `temporal/canvas_workflow.go` |

After editing `go/internal/agentgen/`: `go test ./internal/agentgen/...`; `go/cmd/agent-runtime/`: `go test ./...` + rebuild/restart `them-agent-runtime`; Temporal canvas code: restart **all** dag-workers (see `platform-overview.md`).

## Related
- `app-canvas-appflow.md` — AppFlow agent nodes (they call agents through the same endpoint)
- `run-lifecycle.md` — inbound admission; `mcp-service.md` — `mcp_call`
- `auth-and-roles.md`, `multi-tenancy-rls.md`
