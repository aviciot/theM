# AppFlow Runtime Params — declared node runtime parameters, starting with Debug

# Status: IMPLEMENTED (Debug-panel phase). Runtime settings screen migration still deferred.
# Date: 2026-09-23 (planned, revised after review, then implemented and live-verified same day)

---

## Goal

Today the App Canvas Debug Mode's setup panel (`useAppFlowDebugSession.ts`, Phase 5 of
`docs/APP_CANVAS_DEBUG_PLAN.md`) only asks for an entry point + a test message. It has no way to
ask "this canvas has LLM nodes — which provider/model/key should each one use for this debug run?"
— the agent builder's own debug panel does something similar for its nodes (a generic `app_params`
mechanism for HTTP nodes, a hardcoded special case for LLM nodes — see the design-decisions
research below), but AppFlow's node registry (`appflow.AppCanvasNodeInfo`) has no equivalent field
at all, and the agent builder's LLM handling is global-per-session, not per-node — not a pattern to
copy here.

**The ask, from the user:** don't special-case LLM nodes in the debug panel, and don't merge
multiple LLM nodes' settings into one global selection. Let a node *declare*, in its registry
definition, what runtime parameters it needs and whether each is required — the same node property
could later drive the Runtime settings screen too (out of scope this phase, explicitly deferred —
see below). The debug panel scans whatever nodes are on the canvas and renders **one field set per
node that needs one**, not one shared field set for the whole canvas.

**This phase's scope: Debug panel only.** Do not touch the existing `flow-llm-nodes` Runtime
settings screen (`GET/PUT /admin/applications/{id}/flow-llm-nodes`) — migrating it to read the same
declaration is an explicit future phase. Do not change production (non-debug) run behavior.

### Revision (same day): six requirements added after review

A review of the first draft added these hard requirements, which reshape §3/§4 below:

1. **Per-node, not global.** Each LLM node on the canvas gets its own provider/model/key setting in
   the debug panel — no dedup-by-key merge across nodes.
2. General (saved tenant key) and Custom (one-off key for this run) — unchanged from the first
   draft.
3. **Storage must be scoped to tenant + initiating user + run + node**, and concurrent users/runs
   must never overwrite or read each other's settings.
4. Settings apply **only to that debug run** — the app's saved Runtime settings (`flow-llm-nodes`)
   must never be touched by a debug run, in either direction.
5. **Credentials must survive retries** for the run's lifetime, then be cleaned up. **A missing/
   unavailable requested credential must fail clearly, never silently substitute another key.**
6. Secret values must never enter Temporal history or logs — this was already a hard constraint in
   the first draft (AF-WF-14) and is reconfirmed, now extended to "logs" explicitly.

---

## What already exists (confirmed by code research)

**Two different mechanisms in the agent builder, not one:**
1. **Generic `app_params`** — a node kind declares `AppParamDecl[]` (`key`, `label`, `description`,
   `type: secret|string|url|int|bool`, `required`) on its `NodeDef` (Go:
   `go/internal/agentgen/spec.go:40-47`, `NodeDef.AppParams` at `noderegistry.go:50`; frontend:
   `NodeDef.app_params?: AppParamDecl[]` at `frontend/src/lib/nodeRegistry.ts:82` — **already
   exists on the frontend type, just always `undefined` for appflow-family nodes today**). An
   instance picks one declared key via its own config (`config.app_param_key`,
   `go/internal/agentgen/spec.go:225-227`). `buildDebugParamSpecs()`
   (`frontend/src/app/admin/agents/builder/hooks/useDebugSession.ts:64-81`) scans nodes, reads each
   instance's `app_param_key`, looks up that key's declared metadata, and builds a UI field — one
   per distinct `app_param_key` value, deduped *because HTTP nodes legitimately share one named
   secret across many call sites* (e.g. "the API's bearer token"). **That dedup assumption does not
   hold for LLM nodes** — two LLM nodes on one canvas are not "the same secret used twice," they are
   two independent choices a debugging user may reasonably want to set differently (e.g. testing a
   cheap model on one node and a different provider on another). Confirmed as the reason requirement
   #1 above rejects the first draft's dedup-by-key design.
2. **Hardcoded LLM special case** — `useDebugSession.ts:37-62` just checks
   `step_type === 'llm'` by literal string and unconditionally shows three fixed, globally-scoped
   fields (`__debug_provider`/`__debug_model`/`__debug_api_key`) applying to every LLM node in the
   session. Not generic, not reusable, and exactly the "merge into one global selection" pattern
   this plan was told explicitly not to copy.

**AppFlow has neither.** `appflow.AppCanvasNodeInfo` (`go/internal/appflow/noderegistry.go:20-29`)
has no params field of any kind. Its `llm` kind's provider/model are configured once per app via a
**third**, separate mechanism: `GET/PUT /admin/applications/{id}/flow-llm-nodes`
(`go/internal/admin/applications.go:507-522`) — a design-time per-app override, storing only
`{provider, model}` (`go/internal/admin/service/appflow_llm_nodes.go:66-71`) with **no key field at
all**. Not touched by this plan (requirement #4).

**Production key resolution has no per-run override hook today.** `InlineLLMActivityInput`
(`go/internal/appflow/activities.go:106-133`) deliberately carries no key — its own doc comment
(activities.go:104-105): *"No API key is ever present — the activity resolves it from the DB at
execution time so it never enters Temporal workflow history."* Enforced by `AF-WF-14` /
`TestInlineLLMActivity_NoKeyInInput` (`go/internal/appflow/workflow_test.go:365`, reflection-based:
fails if any field name looks like a secret — the guard is specifically about secret-*shaped*
fields, not IDs, so adding `RunID`/`NodeID` to a request struct is safe under this guard).
`dbLLMCaller.Complete` (`go/cmd/dag-worker/main.go:688-699`) resolves the key by calling
`resolveKey` → `internal/llmresolve.Resolver.ResolveProvider(ctx, applicationID, tenantID,
providerName)` (`llmresolve.go:93-123`) — app-key → tenant-key precedence, **platform never as a
fallback**, reading from `them.llm_providers`' single default key column. **This resolver does not
know about `them.llm_provider_keys`'s named multi-key system at all** — that's a separate
resolution path, already built for `resolveSystemAgentRole`
(`go/internal/admin/system_agent_resolve.go`), which the Tenant LLM Provider Keys plan added for
Classifier/Synthesizer's General/Custom modes. **Reused for General mode below**, not
re-implemented.

**Consequence:** a debug-run key override **cannot** be added as a field on
`InlineLLMActivityInput`/`InlineLLMRequest` (that would break AF-WF-14's invariant). It must live
**out-of-band**, resolved server-side at the same point `resolveKey` already runs, keyed for lookup
by IDs only (`run_id` + `node_id`, both already ID-shaped fields already permitted through the
activity boundary) — never the secret value itself.

**Retry behavior — confirmed, matters for requirement #5.** `InlineLLMActivity` runs under
`ActivityOptions` with `RetryPolicy.MaximumAttempts` defaulting to `2`
(`go/internal/appflow/workflow.go:262-284`, overridable via `TemporalCfg.RetryMaxAttempts`).
Temporal has no partial-resume for a failed attempt — **each retry re-invokes the activity function
from scratch**, so any key lookup inside it runs again on every attempt. A credential store that
only survives one lookup (e.g. a value consumed/deleted on first read) would silently break on
retry #2. The store must support repeated reads for the run's full lifetime. The existing
non-idempotency comment right next to this code (`activities.go:400-404`) already warns that a
retried LLM call re-bills the provider — consistent with "retries are expected and must not lose
their credential."

**Identity/uniqueness already available at `debug/start` time.** `AppFlowDebugHandler.Start`
(`go/internal/admin/appflow_debug.go:60-64`) already has `tenantID` from
`tenantctx.MustTenantIDFromCtx` (server-derived from the JWT, not client-supplied — closes a
tenant-spoofing angle) and `userID` from `auth.ClaimsFromCtx`. `AdmitDebug`
(`go/internal/execution/lifecycle.go:602-628`) generates `runID := newRunID()` —
`uuid.New().String()` (`lifecycle.go:907`), a fresh UUIDv4 **per call** — so two concurrent
debug/start invocations, even by the same user on the same app, always get distinct `run_id`s.
`run_id` is already a collision-proof scoping anchor on its own; combined with `node_id` (also
already known, one canvas node's `instance_id`) this directly satisfies requirement #3's
"tenant + user + run + node" scoping without inventing a new identifier — `tenant_id`/`user_id` are
recorded alongside for authorization/audit (see §4), while `run_id`+`node_id` form the lookup key
(uniqueness is already guaranteed by `run_id` alone; `tenant_id` is still checked on every
read/write as a defense-in-depth authorization gate, not because it's needed for uniqueness).

**Tenant LLM key APIs already exist and are sufficient for the picker:**
`themApi.listMyLLMProviders()` → `GET /admin/my/llm-providers` → `LLMProviderOut[]`
(`{name, enabled, allowed_models, api_key_set}`, `frontend/src/lib/apiTypes.ts:1200-1212`).
`themApi.listProviderKeys(providerName)` → `GET /admin/my/llm-providers/{name}/keys` →
`LLMProviderKeyOut[]` (`{id, name, masked, is_default}`, `apiTypes.ts:1222-1229`). Same data
`TenantRoleCard.tsx` already uses for Classifier/Synthesizer's General mode.

---

## Design

### 1. New declared-params field on `AppCanvasNodeInfo` (Go) — unchanged from first draft

Add `RuntimeParams []nodedefs.RuntimeParamDecl` to `go/internal/appflow/noderegistry.go`'s
`AppCanvasNodeInfo` struct (placement: `go/internal/nodedefs`, since `AppCanvasNodeInfo` already
embeds `nodedefs.Meta` — avoids a new cross-package dependency between `agentgen` and `appflow`).
Shape:

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
input.

The `llm` node kind's entry in `noderegistry.go` (lines 38-59 today) gains:
```go
RuntimeParams: []RuntimeParamDecl{
    {Key: "llm_key", Label: "LLM API Key", Type: "llm_credential", Required: true,
     Description: "Provider + key this node uses for this debug run"},
},
```
This declaration is still one param *per node kind* (static metadata — "an llm node needs a
credential"). What changed from the first draft is entirely in how the debug panel *instantiates*
it per canvas (§3) and how the backend *scopes* it (§4) — not in this registry shape.

This flows to the wire automatically: `GET /admin/node-types` already merges `AllAppCanvasNodeInfos()`
into the response (`go/internal/admin/node_types.go:38`) — no handler change needed, only the struct
field + the `llm` entry's population.

### 2. Frontend: extend `AppParamDecl.type` union — unchanged from first draft

`frontend/src/lib/nodeRegistry.ts:82`'s `NodeDef.app_params?: AppParamDecl[]` already exists and
already flows through `getNodeDef(type, 'appflow')` once the Go side populates it. Add
`'llm_credential'` to `AppParamDecl.type`'s union (currently `'secret'|'string'|'url'|'int'|'bool'`).

### 3. Debug panel: per-node scan, no dedup — **changed from first draft**

New scan function in `useAppFlowDebugSession.ts` (sibling to the existing `entryPointOptions` scan,
same style):

```ts
const runtimeParamSpecs = nodes.flatMap(n => {
  const nodeType = (n.data as { node_type?: string }).node_type;
  if (!nodeType) return [];
  const decl = getNodeDef(nodeType, 'appflow');
  return (decl.app_params ?? []).map(p => ({
    ...p,
    // key is now composite — one entry PER NODE, never deduped across nodes.
    specKey: `${n.id}:${p.key}`,
    nodeId: n.id,
    nodeLabel: (n.data as { display_name?: string }).display_name,
  }));
});
```

**No dedup by `p.key` across nodes** — this is the change requirement #1 forces. Two `llm` nodes on
one canvas produce two independent spec entries, each keyed by `${nodeId}:${paramKey}`
(e.g. `llm_1:llm_key`, `llm_true:llm_key`), each with its own independent debug-panel state.

New type in `frontend/src/app/admin/applications/types.ts` (alongside the existing
`AppFlowDebugNodeState`/`AppFlowNodeDebugInfo`):
```ts
export interface AppFlowRuntimeParamSpec {
  specKey: string;   // `${nodeId}:${paramKey}` — unique per node, not just per param key
  key: string;        // the declared param key itself (e.g. "llm_key")
  label: string; description: string; type: string; required: boolean;
  nodeId: string; nodeLabel?: string;
}
```

New component `AppFlowLLMCredentialField.tsx` (sibling to `AppFlowDebugPanel.tsx`) — renders for
any spec with `type === 'llm_credential'`, **one instance per spec, one per node**:
- **General mode** (default): provider dropdown from `themApi.listMyLLMProviders()` (filtered
  `enabled`) → key dropdown from `themApi.listProviderKeys(provider)` ("use default key" as the
  no-selection option) — same UX as `TenantRoleCard.tsx`'s General mode, but one picker instance per
  LLM node, each independently selectable.
- **Custom mode**: provider + model + API key text fields, same shape as `TenantRoleCard.tsx`'s
  Custom mode, independently settable per node.

`AppFlowDebugPanel.tsx` renders the node's display name as a section header above each
`AppFlowLLMCredentialField` instance (so it's visually obvious which node each picker belongs to),
before the Run All button is enabled — same "setup must be complete" gating the agent builder's
`debugCommitSetup` uses, extended to require every `required: true` spec to have a value.

### 4. Backend: per-run, per-node, tenant+user-scoped credential store — **changed from first draft**

`POST /admin/applications/{id}/debug/start`'s request body (`go/internal/admin/appflow_debug.go:34-37`,
`debugStartBody`) gains a map keyed by node ID, not a single override:
```go
type debugStartBody struct {
    EntryPointSlug string                       `json:"entry_point_slug"`
    UserMessage    string                       `json:"user_message"`
    LLMOverrides   map[string]LLMOverrideInput  `json:"llm_overrides,omitempty"` // node_id -> override
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

`AppFlowDebugService.Start` (`go/internal/admin/service/appflow_debug.go:56-125`), once it has
`run_id` from `AdmitDebug` (which also gives back the **server-derived** `tenantID` via
`resolvedCfg.TenantID` — never trust a client-supplied tenant for this write), resolves each
`node_id → LLMOverrideInput` entry into a plaintext credential (General mode: reuse the same
`them.llm_provider_keys` lookup `resolveSystemAgentRole` already does, via a small new shared
helper — not copy-pasted; Custom mode: the literal `api_key` from the request, never touching the
DB) and writes **one store entry per node**, not one per run.

**Storage choice: Redis, with the DB explicitly rejected for this.** Requirement #3 asks for
"Redis or the DB" — Redis is the right choice here specifically because:
- The data is inherently short-lived (bounded by one debug run's lifetime, not permanent app
  config) — a DB row would need its own cleanup job; Redis TTL is the cleanup job.
- It must never leak into any durable, replayable log (Temporal history already excluded per
  AF-WF-14; a DB table is itself a durable, queryable log of past debug credentials, which is a
  worse fit for "keep secret values out of logs" than an expiring cache entry).
- No other debug-run state is DB-persisted either (debug runs skip `session.Register`/
  `gate.Confirm` — see `docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 5 backend section); a DB table here
  would be the only piece of debug-run state that outlives the run, which is exactly the opposite of
  what's wanted.

**Key shape** (satisfies requirement #3's tenant+user+run+node scoping):
```
them:debug:{tenant_id}:{run_id}:{node_id}:llm_override
```
Value: JSON `{user_id, provider, model, api_key, base_url}` — `user_id` stored in the value (not
just implied by who wrote it) so a read-time check can confirm the reader matches, as a
defense-in-depth double-check beyond the key's own tenant scoping.
TTL: matches the debug run's expected maximum lifetime (see open question #3 for the exact value)
— **not consumed/deleted on first read**, since requirement #5 needs it to survive a Temporal
retry's second lookup. Deleted proactively on run completion (terminal event — reuse
`internal/runstream`'s `isStreamTerminal` detection, called from wherever `AdmitDebug`'s caller
already observes run completion) as the primary cleanup path; TTL is the fallback for runs that
never reach a terminal event (e.g. a debug session abandoned mid-flight).

**Concurrency (requirement #3):** because `run_id` is a fresh UUIDv4 per `debug/start` call
(confirmed above — no reuse, no collision), two concurrent debug runs — same user, same app, same
node IDs even — always write to different Redis keys (different `run_id` segment). Two different
tenants can never collide either (different `tenant_id` segment) even in the pathological case of
somehow-colliding `run_id`s. No additional locking or generation-counter needed — the key shape
itself is the concurrency guarantee.

**Read side — `dbLLMCaller.resolveKey`/`Complete` gain the lookup, plus new plumbing for
`RunID`/`NodeID`:** confirmed by direct trace that `InlineLLMRequest` (activities.go:219-228) does
**not** currently carry `RunID`/`NodeID`, and `InlineLLMActivity` (activities.go:340-408) builds the
request literal (activities.go:373-382) without passing them through, even though both are already
present on `InlineLLMActivityInput`. This plan adds:
```go
type InlineLLMRequest struct {
    // ...existing fields unchanged...
    RunID  string // new — for the debug-override lookup key
    NodeID string // new — for the debug-override lookup key
}
```
and threads `input.RunID`/`input.NodeID` into the literal at activities.go:373-382. **Safe under
AF-WF-14** — the guard test only flags secret-*shaped* field names (checked by
`TestInlineLLMActivity_NoKeyInInput`'s reflection over `InlineLLMActivityInput`, not
`InlineLLMRequest`, and `RunID`/`NodeID` are IDs, not secrets, so they'd pass the same check even if
it ran against this struct too).

`dbLLMCaller.resolveKey` (`go/cmd/dag-worker/main.go:669-676`) gains a first check: look up
`them:debug:{tenantID}:{req.RunID}:{req.NodeID}:llm_override` in Redis (only when `RunID`/`NodeID`
are non-empty — production, non-debug calls never populate them, so this is a no-op there; confirm
at implementation time whether every `InlineLLMRequest` call site can cheaply supply both, or
whether a nil/empty check is the only gate needed). If present, use its provider/model/key/base_url
directly, **skip `llmresolve.Resolver.ResolveProvider` entirely** for that call.

**Requirement #5's "fail clearly, never silently substitute" — the concrete mechanism:** this is
why the override check must be a **hard branch**, not a soft fallback merged into the existing
resolution chain. If the debug-start request declared an override for this node (i.e. the frontend
sent an `llm_overrides` entry for it) but the Redis lookup at execution time comes back empty (TTL
already expired, Redis unavailable, or any other reason), `resolveKey`/`Complete` must return an
explicit error — *not* fall through to `llmresolve.Resolver.ResolveProvider` and silently use the
app's default/tenant/platform key instead. Today's code has no way to distinguish "no override was
ever requested for this node" (correctly falls through to normal resolution) from "an override was
requested but is now unavailable" (must fail loud) — this plan needs an explicit marker for that
distinction. Simplest approach: `AppFlowDebugService.Start` writes a Redis key for **every** node
with a declared `llm_credential` param that was part of the debug run's compiled spec, even if the
frontend's per-node picker was left at "use tenant default" — so "the key exists with a default-key
marker" and "the key is simply absent" are the only two states, and absence always means "was
requested but is gone," never "was never requested." (Non-debug/production calls never write this
key at all, so they're unaffected — the absence-means-missing rule only applies when `RunID` is a
real debug run ID, which the caller already knows.)

### 5. Security note on Custom-mode key storage — unchanged from first draft

Custom-mode keys are sent once in the `debug/start` request body, resolved server-side into the
per-node Redis entry described above, and never written back to any client-side persistent storage
(unlike the agent builder's own debug secrets, which use browser `sessionStorage` —
`useDebugSession.ts:22-23`). This is a stricter posture than the agent builder's existing
precedent, consistent with requirement #6 (secrets out of logs — client-side persistent storage is
itself a kind of durable log outside this plan's control).

---

## Explicitly out of scope for this plan

- **Runtime settings screen migration** (`flow-llm-nodes` reading from the same `RuntimeParams`
  declaration) — deferred to a future phase. The existing `flow-llm-nodes` API/UI, and the app's
  saved Runtime settings it manages, are completely untouched by this plan (requirement #4).
- **Any change to production (non-debug) LLM node resolution** — the Redis-override check added to
  `dbLLMCaller` only ever fires when `RunID`/`NodeID` correspond to a debug run that actually wrote
  an override key; production calls are unaffected.
- **Step controls** (Phase 6 of `docs/APP_CANVAS_DEBUG_PLAN.md`) — unrelated, separate plan.

---

## Open questions for implementation time (not blocking the plan, but flag before coding)

1. Exact package placement for the shared `RuntimeParamDecl` type — `go/internal/nodedefs` vs. a
   same-shape duplicate in `appflow`. Lean toward `nodedefs` since `AppCanvasNodeInfo` already
   embeds `nodedefs.Meta`.
2. Whether every `InlineLLMRequest`-building call site can cheaply supply non-empty `RunID`/
   `NodeID` for debug calls and empty for production calls, or whether a separate bool flag is
   clearer than relying on emptiness — needs a look at all call sites, not just the one traced this
   session.
3. Exact TTL value for the Redis override key, and the exact hook point for proactive
   delete-on-terminal-event (which component observes a debug run's terminal state today, and can
   it cheaply also fire N Redis deletes — one per node that had an override — at that point).
4. Whether `AppFlowDebugService.Start` should reject the whole debug/start call if any *required*
   `llm_credential` param has no override supplied and the node's canvas config also has no
   design-time default — or whether "falls through to the app's flow-llm-nodes provider/model with
   the tenant's default key" is an acceptable no-override behavior for a required-but-unset param.
   The first draft implicitly assumed the latter; this revision's requirement #5 (fail clearly on a
   *requested-but-missing* credential) does not by itself answer what should happen for a
   *never-requested* required param — that's a UX/validation question for the debug panel's "setup
   complete" gate (§3), not a backend resolution question, but worth confirming before implementing
   the gate's exact rule.

---

## Implementation — COMPLETE (2026-09-23)

All open questions above were resolved during implementation:

1. `RuntimeParamDecl` lives in `appflow` itself (`go/internal/appflow/noderegistry.go`), not
   `nodedefs` — matching `nodedefs`'s own stated boundary ("param-declaration types are typed to
   each runtime's own compiler/resolution model, not shared metadata"), the same reason
   `agentgen.AppParamDecl` lives in `agentgen` rather than the shared package. JSON tag `app_params`
   (not a new field name) so the frontend's existing `NodeDef.app_params?: AppParamDecl[]` needed
   zero structural changes — only a new `'llm_credential'` value added to its `type` union.
2. `InlineLLMRequest` gained `RunID`/`NodeID` fields, populated unconditionally at both call sites
   (`workflow.go`, `graph.go`) from the activity input's own `RunID`/`NodeID` — always non-empty,
   since every AppFlow run has these. `Debug bool` (also added) is what actually gates the
   debug-override check, not emptiness of the IDs.
3. TTL is `debugcred.TTL = 10 * time.Minute` (`go/internal/debugcred/store.go`). Proactive
   delete-on-terminal-event was **not** implemented this pass — TTL-only cleanup, per the plan's
   own "TTL is the fallback for runs that never reach a terminal event" framing generalized to the
   only cleanup path for now. Flagged as a possible follow-up, not a regression: a debug run's
   credentials linger in Redis for up to 10 minutes after the run finishes rather than being deleted
   immediately.
4. Resolved: a required `llm_credential` param with **no** override entry in the request fails the
   whole `debug/start` call with 422 before `AdmitDebug` is ever invoked — there is no "falls
   through to Runtime settings" behavior for AppFlow debug runs. Confirmed live: a request with zero
   `llm_overrides` against a 3-LLM-node draft returned `422 node "llm_1" requires an llm_overrides
   entry...` without admitting a run.

**What was built, exactly per the six requirements from the review:**

1. **Per-node, not global** — `useAppFlowDebugSession.ts`'s `runtimeParamSpecs` scan produces one
   spec per `${nodeId}:${paramKey}`, never deduped across nodes. `AppFlowDebugPanel.tsx` renders one
   `AppFlowLLMCredentialField.tsx` instance per node, each with its own independent General/Custom
   state. Verified live: a 3-LLM-node draft with 3 different custom keys produced 3 distinct Redis
   entries with 3 distinct `api_key` values (confirmed via direct `GET` against `them-redis`).
2. **General + Custom** — `AppFlowLLMCredentialField.tsx` mirrors `TenantRoleCard.tsx`'s
   provider-dropdown → key-dropdown (General) / provider+model+key text fields (Custom) pattern, at
   node scope. General mode resolves via `resolveLLMOverride` (new
   `go/internal/admin/service/appflow_debug_credentials.go`), reusing the same
   `them.llm_provider_keys` DAL calls `resolveSystemAgentRole` already uses — never re-implemented.
3. **Storage scoped to tenant + run + node** (user recorded in the value, not the key) —
   `go/internal/debugcred.Store`, key shape `them:debug:{tenant_id}:{run_id}:{node_id}:llm_override`.
   Concurrency safety comes from `run_id` already being a fresh UUIDv4 per `debug/start` call — no
   locking needed, proven by 5 integration tests against live Redis (`TestIntegration_Store_*`,
   including `TenantScoping_DifferentTenantsDoNotCollide`).
4. **Applies only to that debug run** — the override check in `dbLLMCaller.Complete`
   (`cmd/dag-worker/main.go`) only fires when `req.Debug == true`; production (non-debug) calls
   never look at `debugcred.Store` at all, and nothing in this change touches
   `GetAppFlowLLMNodes`/`PutAppFlowLLMOverride` (the Runtime settings API) or its underlying table.
5. **Survives retries, fails clearly on missing credential** — `debugcred.Store.Get` is a plain
   non-destructive Redis `GET` (proven by `TestIntegration_Store_GetSurvivesRepeatedReads`, 3
   consecutive reads all succeed). `dbLLMCaller.Complete`'s debug branch returns an explicit error
   — never falls through to `resolveKey`/`llmresolve` — on: lookup error, override absent
   (`"debug credential unavailable for this run (expired or evicted)"`), or `debugStore` unconfigured
   at all (`"debug run but no debug credential store configured on this worker"` — a real bug this
   session's own tests caught before shipping: this case was originally falling through to normal
   resolution and nil-panicking, fixed in the same commit as the test that found it).
6. **Secrets never in Temporal history or logs** — the override value never crosses the workflow/
   activity boundary; only `RunID`/`NodeID` (IDs, not secrets) were added to `InlineLLMRequest`,
   confirmed safe under the existing `AF-WF-14` reflection guard test (which checks
   `InlineLLMActivityInput`, and these same names would pass that check too). Custom-mode keys are
   sent once in the `debug/start` request body and never written to browser-side persistent storage
   (stricter than the agent builder's own `sessionStorage` precedent for its debug secrets).

**Also fixed in the same session, found while live-testing this feature (not originally part of
this plan, but blocking real verification of it):**
- **Cross-tenant IDOR on `/ws/dashboard` `run:*` subscriptions** — `IsValidChannel` validated
  channel-name shape only, never whether the caller's tenant owned the run. Fixed with a new
  `dashboard.RunOwnershipChecker` (`go/internal/dashboard/run_ownership.go`), checked before
  `tailRunChannel` runs; fails closed (refuses) on a nil checker, an error, or `owns=false`. 3 new
  tests prove this (`TestDashboard_RunChannel_WrongTenantRefused`,
  `OwnershipCheckErrorRefusesNotTails`, `NilOwnerRefusesEntirely`).
- **Frontend was silently dropping every real trace event** — `useAppFlowDebugSession.ts`'s
  `ws.onmessage` gated on a top-level `msg.type`, but real events arrive as `{"channel":...,
  "event":{"type":"node_start",...}}` with no top-level `type` at all (only `ping`/`subscribed`/a
  WS-protocol `error` have one). The shipped Phase 5 hook had never actually displayed a single live
  event in a real browser — this bug predates this session's other work and was only caught now.

**Tests:** 13 new unit tests across 4 packages (`internal/appflow` +1, `internal/admin/service` +4,
`internal/dashboard` +3, `cmd/dag-worker` +5 — `dbLLMCaller` had zero direct tests before this
change) plus 5 new integration tests (`internal/debugcred`, run for real against live Redis this
session). `go/TEST_INDEX.md` S1 total 1448→1461, S2 total 73→78. `go test ./...` 0 failures, full
suite. `go test -race` on touched packages found one **pre-existing, unrelated** flaky race
(`internal/appflow.TestForkJoin_EmitsTraceForAllNodes`, inside the Temporal SDK's own test-harness
concurrency, nowhere near this session's one-line-per-file edits to `workflow.go`/`graph.go`) —
flagged in `TEST_INDEX.md`, not fixed, same category as the already-documented
`internal/llmgateway.TestHandler_Stream_200` flake. `npx tsc --noEmit` 0 errors.

**Verified live end-to-end, not just unit tests:** rebuilt and restarted `them-go-bridge`,
`them-dag-worker`, `them-dag-worker-2`, `them-dag-worker-debug` (all healthy, all workers polling
correctly post-restart); `them-frontend` hot-reloaded with 0 compile errors. Ran a Node script
inside the live `them-frontend` container (no browser-automation tool available in this
environment) that: created a real throwaway app + 3-LLM-node draft + entry point, called
`debug/start` with no overrides (confirmed 422, no run admitted), then called it again with 3
distinct per-node custom credentials (confirmed 200, real run executed on the debug worker pool,
full `node_start`/`node_done`/`done` sequence arrived live over `/ws/dashboard`), then confirmed via
direct `redis-cli GET` that 3 separate Redis keys existed with 3 distinct `api_key` values scoped
correctly by tenant+run+node. The throwaway application was deleted from the live DB after testing.

**Not done — no actual logged-in browser click-through of the new per-node picker UI itself** (see
above for why, and what was done instead to compensate). The full backend contract (validation,
per-node isolation, retry-safe storage, fail-loud-on-missing, tenant ownership) is proven live;
the picker component's rendering/interaction (dropdowns populating, mode toggle, etc.) has not been
visually confirmed in a real browser. Recommend a manual pass through the Debug button before fully
trusting the UI layer specifically.

**Explicitly still deferred, unchanged from the plan:** Runtime settings screen migration
(`flow-llm-nodes` reading from the same `RuntimeParams` declaration) — a separate future phase, not
started.
