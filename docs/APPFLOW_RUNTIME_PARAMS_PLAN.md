# AppFlow Runtime Params — declared node runtime parameters, starting with Debug

# Status: PLANNED, phased. Phase 1 NEXT.
# Date: 2026-09-23

---

## Goal

Today the App Canvas Debug Mode's setup panel (`useAppFlowDebugSession.ts`, Phase 5 of
`docs/APP_CANVAS_DEBUG_PLAN.md`) only asks for an entry point + a test message. It has no way to
ask "this canvas has an LLM node — which provider/model/key should this debug run use?" — the
agent builder's own debug panel does exactly this for its nodes (a generic `app_params` mechanism
for HTTP nodes, a hardcoded special case for LLM nodes — see the design-decisions research below),
but AppFlow's node registry (`appflow.AppCanvasNodeInfo`) has no equivalent field at all.

**The ask, from the user:** don't special-case LLM nodes in the debug panel. Instead, let a node
*declare*, in its registry definition, what runtime parameters it needs and whether each is
required — the same node property could later drive the Runtime settings screen too (out of scope
this phase, explicitly deferred — see below). The debug panel then just scans whatever nodes are on
the canvas and renders a field for whatever they declare, generically.

**This phase's scope: Debug panel only.** Do not touch the existing `flow-llm-nodes` Runtime
settings screen (`GET/PUT /admin/applications/{id}/flow-llm-nodes`) — migrating it to read the same
declaration is an explicit future phase. Do not change production (non-debug) run behavior.

---

## What already exists (confirmed by code research this session)

**Two different mechanisms in the agent builder, not one:**
1. **Generic `app_params`** — a node kind declares `AppParamDecl[]` (`key`, `label`, `description`,
   `type: secret|string|url|int|bool`, `required`) on its `NodeDef` (Go:
   `go/internal/agentgen/spec.go:40-47`, `NodeDef.AppParams` at `noderegistry.go:50`; frontend:
   `NodeDef.app_params?: AppParamDecl[]` at `frontend/src/lib/nodeRegistry.ts:82` — **already
   exists on the frontend type, just always `undefined` for appflow-family nodes today**). An
   instance picks one declared key via its own config (`config.app_param_key`,
   `go/internal/agentgen/spec.go:225-227`). `buildDebugParamSpecs()`
   (`frontend/src/app/admin/agents/builder/hooks/useDebugSession.ts:64-81`) scans nodes, reads each
   instance's `app_param_key`, looks up that key's declared metadata, and builds a UI field. Today
   only the agent builder's `http` node kind uses this (`go/internal/agentgen/nodes.go:232-247`).
2. **Hardcoded LLM special case** — `useDebugSession.ts:37-62` just checks
   `step_type === 'llm'` by literal string and unconditionally shows three fixed, globally-scoped
   fields (`__debug_provider`/`__debug_model`/`__debug_api_key`). Not generic, not reusable, and
   scoped to "all LLM nodes in this session" rather than per-node.

**AppFlow has neither.** `appflow.AppCanvasNodeInfo` (`go/internal/appflow/noderegistry.go:20-29`)
has no params field of any kind. Its `llm` kind's provider/model are configured once per app via a
**third**, separate mechanism: `GET/PUT /admin/applications/{id}/flow-llm-nodes`
(`go/internal/admin/applications.go:507-522`) — a design-time per-app override, storing only
`{provider, model}` (`go/internal/admin/service/appflow_llm_nodes.go:66-71`) with **no key field at
all**. Not touched by this plan.

**Production key resolution has no per-run override hook today.** `InlineLLMActivityInput`
(`go/internal/appflow/activities.go:106-133`) deliberately carries no key — its own doc comment
(activities.go:104-105): *"No API key is ever present — the activity resolves it from the DB at
execution time so it never enters Temporal workflow history."* This is enforced by a dedicated
test, `AF-WF-14` / `TestInlineLLMActivity_NoKeyInInput`
(`go/internal/appflow/workflow_test.go:365`, reflection-based: fails if any field name looks like a
secret). `dbLLMCaller.Complete` (`go/cmd/dag-worker/main.go:688-699`) resolves the key by calling
`resolveKey` → `internal/llmresolve.Resolver.ResolveProvider(ctx, applicationID, tenantID,
providerName)` (`llmresolve.go:93-123`) — app-key → tenant-key precedence, **platform never as a
fallback**, reading from `them.llm_providers`' single default key column. **This resolver does not
know about `them.llm_provider_keys`'s named multi-key system at all** — that's a separate
resolution path, already built for `resolveSystemAgentRole`
(`go/internal/admin/system_agent_resolve.go`), which the Tenant LLM Provider Keys plan added for
Classifier/Synthesizer's General/Custom modes.

**Consequence for this plan:** a debug-run key override **cannot** be added as a field on
`InlineLLMActivityInput`/`InlineLLMRequest` — that would break the AF-WF-14 invariant it exists to
protect (secrets must never enter Temporal workflow history, which is durable and replayed).
It needs to live **out-of-band**, resolved server-side by `run_id` at the same point `resolveKey`
already runs, never passed through the workflow/activity input.

**Tenant LLM key APIs already exist and are sufficient for the picker:**
`themApi.listMyLLMProviders()` → `GET /admin/my/llm-providers` → `LLMProviderOut[]`
(`{name, enabled, allowed_models, api_key_set}`, `frontend/src/lib/apiTypes.ts:1200-1212`).
`themApi.listProviderKeys(providerName)` → `GET /admin/my/llm-providers/{name}/keys` →
`LLMProviderKeyOut[]` (`{id, name, masked, is_default}`, `apiTypes.ts:1222-1229`). Same data
`TenantRoleCard.tsx` already uses for Classifier/Synthesizer's General mode.

---

## Design

### 1. New declared-params field on `AppCanvasNodeInfo` (Go)

Add `RuntimeParams []nodedefs.RuntimeParamDecl` (or reuse `agentgen.AppParamDecl`'s shape under a
new name in a neutral package both `agentgen` and `appflow` can import without a cross-dependency —
candidate: `go/internal/nodedefs`, which `appflow.AppCanvasNodeInfo` already embeds `Meta` from) to
`go/internal/appflow/noderegistry.go`'s `AppCanvasNodeInfo` struct. Shape:

```go
type RuntimeParamDecl struct {
    Key         string // e.g. "llm_credential"
    Label       string
    Description string
    Type        string // "llm_credential" | "secret" | "string" | "url" | "int" | "bool"
    Required    bool
}
```

`type: "llm_credential"` is a new type value beyond the agent builder's existing set — the frontend
param-spec renderer uses it to know "render the tenant-key General/Custom picker" instead of a plain
input. Everything else (`secret`/`string`/`url`/`int`/`bool`) renders like the agent builder's
existing types, for future non-LLM AppFlow node kinds that might declare a plain param.

The `llm` node kind's entry in `noderegistry.go` (lines 38-59 today) gains:
```go
RuntimeParams: []RuntimeParamDecl{
    {Key: "llm_key", Label: "LLM API Key", Type: "llm_credential", Required: true,
     Description: "Provider + key this debug run uses for every LLM node on the canvas"},
},
```
One declared param covers provider+model+key together (a single `llm_credential`-typed field is a
compound picker, not three separate params) — matches how `TenantRoleCard.tsx` already presents
General/Custom as one unit, not three independent fields.

This flows to the wire automatically: `GET /admin/node-types` already merges `AllAppCanvasNodeInfos()`
into the response (`go/internal/admin/node_types.go:38`) — no handler change needed, only the struct
field + the `llm` entry's population.

### 2. Frontend: `NodeDef.app_params` already typed — extend the `AppParamDecl.type` union

`frontend/src/lib/nodeRegistry.ts:82`'s `NodeDef.app_params?: AppParamDecl[]` already exists and
already flows through `getNodeDef(type, 'appflow')` once the Go side populates it. Add
`'llm_credential'` to `AppParamDecl.type`'s union (currently `'secret'|'string'|'url'|'int'|'bool'`,
`nodeRegistry.ts` — exact line TBD at implementation time, find via the type definition).

### 3. Debug panel: generic param scan + LLM-credential picker

New scan function in `useAppFlowDebugSession.ts` (sibling to the existing `entryPointOptions`
scan, same style — plain `.filter()`/`.map()` at hook top level, not memoized, matching the
existing pattern at lines 46-49):

```ts
const runtimeParamSpecs = nodes.flatMap(n => {
  const nodeType = (n.data as { node_type?: string }).node_type;
  if (!nodeType) return [];
  const decl = getNodeDef(nodeType, 'appflow');
  return (decl.app_params ?? []).map(p => ({ ...p, nodeId: n.id, nodeLabel: (n.data as any).display_name }));
});
```

Dedupe by `key` the same way the agent builder's HTTP scan does (one canvas can have multiple LLM
nodes; the debug session needs at most one "which key" answer applying to all of them, per the
user's framing that the current per-node override problem was never asked for beyond "expose what
each node needs").

New type in `frontend/src/app/admin/applications/types.ts` (alongside the existing
`AppFlowDebugNodeState`/`AppFlowNodeDebugInfo`):
```ts
export interface AppFlowRuntimeParamSpec {
  key: string; label: string; description: string; type: string; required: boolean;
  nodeId: string; nodeLabel?: string;
}
```

New component `AppFlowLLMCredentialField.tsx` (sibling to `AppFlowDebugPanel.tsx`) — renders for
any spec with `type === 'llm_credential'`:
- **General mode** (default): provider dropdown from `themApi.listMyLLMProviders()` (filtered
  `enabled`) → key dropdown from `themApi.listProviderKeys(provider)` ("use default key" as the
  no-selection option) — identical UX to `TenantRoleCard.tsx`'s General mode.
- **Custom mode**: provider + model + API key text fields, same shape as `TenantRoleCard.tsx`'s
  Custom mode, stored in browser state only (never persisted to `sessionStorage` the way the agent
  builder's secrets are — see the security note below on why this is intentionally more ephemeral).

`AppFlowDebugPanel.tsx` renders one `AppFlowLLMCredentialField` per deduped `llm_credential` spec,
alongside the existing entry-point/test-message fields, before the Run All button is enabled (same
"setup must be complete before running" gating the agent builder's `debugCommitSetup` uses).

### 4. Backend: out-of-band debug key override, never through Temporal

`POST /admin/applications/{id}/debug/start`'s request body
(`go/internal/admin/appflow_debug.go:34-37`, `debugStartBody`) gains an optional field:
```go
type debugStartBody struct {
    EntryPointSlug string `json:"entry_point_slug"`
    UserMessage    string `json:"user_message"`
    LLMOverride    *LLMOverrideInput `json:"llm_override,omitempty"` // new
}
type LLMOverrideInput struct {
    Mode     string `json:"mode"`      // "general" | "custom"
    Provider string `json:"provider,omitempty"`
    KeyID    *int64 `json:"key_id,omitempty"`    // general mode, optional (nil = tenant default key)
    Model    string `json:"model,omitempty"`     // custom mode
    APIKey   string `json:"api_key,omitempty"`   // custom mode
    BaseURL  string `json:"base_url,omitempty"`  // custom mode
}
```

`AppFlowDebugService.Start` (`go/internal/admin/service/appflow_debug.go:56-125`), once it has a
`run_id` from `AdmitDebug`, resolves the override into a plaintext key (General mode: same
`them.llm_provider_keys` lookup `resolveSystemAgentRole` already does, via a small new shared
helper rather than copy-pasting its DAL calls; Custom mode: the literal `api_key` from the request,
never touching the DB) and **writes it to Redis** under a short-TTL key
(`them:debug:run:{run_id}:llm_override`, TTL matching the debug run's expected max lifetime, e.g.
10 minutes — long enough for any debug run, short enough that a stale entry can't outlive the run
by much) as a small JSON blob `{provider, model, api_key, base_url}`. **This key is never included
in `AppFlowWorkflowInput` or any activity input** — it does not cross the Temporal boundary at all,
so AF-WF-14 is untouched and needs no changes.

`dbLLMCaller.resolveKey` (`go/cmd/dag-worker/main.go:669-676`) and `dbLLMCaller.Complete`
(`main.go:688-699`) gain a first check: given `RunID` (already present on `InlineLLMActivityInput`
and threaded into `InlineLLMRequest` — confirm at implementation time whether `InlineLLMRequest`
needs a `RunID` field added; `InlineLLMActivityInput.RunID` already exists at activities.go:107,
the question is only whether it's currently passed down to the `Complete` call site), look up
`them:debug:run:{run_id}:llm_override` in Redis. If present, use its provider/model/key/base_url
directly, skipping `llmresolve.Resolver.ResolveProvider` entirely for that call. If absent
(the normal, non-debug case), fall through to today's unchanged behavior. Delete the Redis key once
the run reaches a terminal state (reuse the existing terminal-event detection already in
`internal/runstream` — `isStreamTerminal`), or just let the TTL expire; a debug run is inherently
short-lived and low-volume, so a TTL-only cleanup is acceptable and avoids adding a new write path
just for deletion.

### 5. Security note on Custom-mode key storage

The agent builder's own debug secrets (`__debug_api_key` etc.) are stored in browser
`sessionStorage` (`useDebugSession.ts:22-23`, `ssSet`/`ssGet`) so they survive a page reload within
the same tab session. This plan's Custom-mode key deliberately does **not** follow that pattern —
it is sent once in the `debug/start` request body, resolved server-side into a short-TTL Redis
entry, and never written back to any client-side persistent storage. This is a stricter posture
than the agent builder's existing precedent (worth flagging, not itself a blocker): the debug/start
request body already goes over HTTPS/JWT-auth like every other admin route, and the Redis entry's
short TTL bounds exposure. If asked to match the agent builder's sessionStorage convenience later,
that would need explicit confirmation given it's a step down in secret-handling rigor.

---

## Explicitly out of scope for this plan

- **Runtime settings screen migration** (`flow-llm-nodes` reading from the same `RuntimeParams`
  declaration) — deferred to a future phase, per explicit agreement with the user. The existing
  `flow-llm-nodes` API/UI is completely untouched by this plan.
- **Per-node-instance key overrides within one debug run** (e.g. two different LLM nodes on the
  same canvas using two different keys in the same debug session) — this phase's dedupe-by-key
  design gives one override applying to every LLM node in the run, matching the agent builder's
  own "one set of debug LLM params for the whole session" precedent. A per-instance version would
  need per-node Redis keys and a more complex UI; not requested, not built here.
- **Any change to production (non-debug) LLM node resolution** — the Redis-override check added to
  `dbLLMCaller` is purely additive (falls through unchanged when no override key exists for that
  `run_id`), and only debug/start ever writes that key.
- **Step controls** (Phase 6 of `docs/APP_CANVAS_DEBUG_PLAN.md`) — unrelated, separate plan.

---

## Open questions for implementation time (not blocking the plan, but flag before coding)

1. Exact package placement for the shared `RuntimeParamDecl` type — a new `go/internal/nodedefs`
   addition (since `appflow.AppCanvasNodeInfo` already embeds `nodedefs.Meta`) vs. a same-shape
   duplicate in `appflow` to avoid `agentgen` importing something `appflow`-shaped or vice versa.
   Lean toward `nodedefs` since both packages already depend on it.
2. Whether `InlineLLMRequest` needs a new `RunID` field, or whether the activity already has access
   to it some other way before calling `Complete` — needs a direct read of the full call chain from
   `InlineLLMActivity` (activities.go:340) through to where `Complete` is invoked, to confirm.
3. Exact TTL value for the Redis override key — 10 minutes suggested above, not yet confirmed
   against any existing debug-run timeout constant elsewhere in the codebase.
