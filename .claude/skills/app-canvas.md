---
name: app-canvas
description: App Canvas build + debug guide for the-M platform — how to construct an app definition JSON, create it via the real API, and run/verify it through Debug Mode. Invoke before building or debugging any App Canvas application (not the agent builder — that's a separate system).
---

# App Canvas — Build & Debug Reference
# Ground truth as of 2026-09-26. Node-level rules below are LIVE-QUERYABLE
# (see §1) — always re-fetch, never trust a cached copy of the node list.
# App-level rules (§2-5) are NOT exposed by any endpoint — this file is the
# only record of them. If they ever seem wrong, re-verify against the Go
# source cited inline before trusting this doc.

---

## 0. Two different systems — do not confuse them

- **Agent builder** (`frontend/src/app/admin/agents/builder/`) — builds a
  single reusable *agent* (`agentgen` family nodes: `llm`, `http`, `transform`,
  `branch`, `a2a_call`, `mcp_call`, `input`, `response`, `loop`, `parallel`,
  `human_wait`, `stream_out`). Compiles to `AgentSpec`, served by
  `them-agent-runtime`. See `.claude/skills/a2a.md` for that system.
- **App Canvas** (`frontend/src/app/admin/applications/`) — builds a whole
  *application*: entry point(s) → orchestrator/flow → agents. This skill
  covers App Canvas's own node family (`appflow`: `llm`, `condition`, `fork`,
  `join`, `router`, `hil`) plus agent/middleware/orchestrator components
  referenced by slug, not built inline.

This skill is for **App Canvas only**.

---

## 1. Node type rules — ALWAYS fetch live, never hardcode

```
GET /api/v1/admin/node-types
```
(requires an admin JWT — see §4 for login)

Returns all 21 node types across both families, each with: `label`,
`description`, `config_fields` (key/type/required/description/**example**),
`edges` (`min_in`/`max_in`/`min_out`/`max_out`), `control_output_ports` (for
branching nodes like `condition`), `output_ports`, `usage_notes` (real
gotchas — e.g. condition's "missing var renders false, not an error"),
`family` (`appflow` vs `agentgen`), and `app_params` (for `llm`'s debug
credential requirement).

**This is genuinely self-documenting — read it fresh before building
anything with an unfamiliar node kind. Do not guess a node's config shape
from a prior example JSON; confirm against this endpoint.** A prior example
may be stale or may have been built before a field was added/renamed.

Confirmed appflow-family node kinds and their real behavior (from this
endpoint + `go/internal/appflow/workflow.go` cross-check, 2026-09-26):
- **`llm`** — the only appflow kind that writes a FlowVar (`output_var`,
  defaults to `"output"`). `user_prompt`/`system_prompt` support
  `{{.varname}}` interpolation. Provider/model are NOT set on the node —
  set once per app in the Runtime screen; Debug runs override per-node via
  `llm_overrides` (see §4).
- **`condition`** — exactly 2 outgoing edges required, labelled `"true"`/
  `"false"` (case-insensitive). Expression is a Go template over flow vars;
  a missing/unset var renders as `""` → takes the **false** branch, not an
  error. **Gotcha confirmed by reading workflow.go directly**: `.input`
  inside a condition's expression is NOT the original entry-point message —
  it's whatever the immediately-preceding node (usually an `llm`) wrote to
  `accumulated`. Don't try to steer a condition by the raw user message if
  an LLM sits between the entry point and the condition.
- **`fork`** — needs ≥2 outgoing edges, all run concurrently, merge at a
  paired `join`.
- **`join`** — needs ≥2 incoming edges; results merged with a newline
  separator.
- **`router`** — LLM-based intent classifier; `output_labels` must match
  edge labels exactly, or the run fails (unless there's exactly 1 outgoing
  edge, which is always taken as a fallback).
- **`hil`** — pauses for human approval; a rejection ends the run with
  status `"rejected"`, no false-path edge like condition has.

Agent/middleware/orchestrator components are NOT node types — they're
looked up via `GET /api/v1/admin/component-definitions` (separate endpoint,
not checked in as much detail this session — confirm its shape before
assuming).

---

## 2. App definition JSON shape — NOT exposed by any endpoint (written down here, may go stale)

Top-level shape (`schema_version: 2`):
```json
{
  "name": "my-app",
  "schema_version": 2,
  "execution_backend": "temporal",
  "components": [ /* nodes, see below */ ],
  "entry_points": [ /* see §3 — NOT the same as the them.entry_points table row */ ],
  "connections": [ /* edges, see below */ ]
}
```

**Component shape** (one per node on the canvas):
```json
{
  "instance_id": "llm_1",
  "definition_ref": { "kind": "inline", "name": "llm", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "llm", "display_name": "...", /* fields per §1's config_fields */ }
}
```
- `kind: "inline"` for `llm`/`condition`/`fork`/`join`/`router`/`hil` (appflow
  family) — `namespace` is always `"builtin"`.
- `kind: "agent"` for an agent component, e.g.:
  ```json
  { "instance_id": "agent_1",
    "definition_ref": { "kind": "agent", "name": "a2a_echo", "version": 1,
                         "namespace": "them.tenant.00000000-0000-0000-0000-000000000001" },
    "config": { "display_name": "..." } }
  ```
  The namespace is `them.tenant.<tenant_uuid>` — the bootstrap tenant is
  `00000000-0000-0000-0000-000000000001`. Confirm the right tenant UUID
  before reusing this for a non-bootstrap-tenant app.
- `definition_id` is not required in the JSON — resolved server-side by
  `(kind, name, namespace)` lookup.

**Connections** (edges): `{"type": "flow_control", "source": "<instance_id>", "target": "<instance_id>"}`.
A labelled edge (condition's true/false) adds `"label": "true"` or `"false"`.

---

## 3. Entry points — a REAL GOTCHA, easy to miss

The definition JSON's `entry_points` array is **only a description that gets
projected into the real `them.entry_points` table at Publish time**
(confirmed: `DefinitionsHandler.Publish` "compiles projections" —
`go/internal/admin/definitions.go`). Saving a draft definition does **NOT**
create the table row.

`debug/start` looks up the entry point by slug against
`app.EntryPoints` from `GetApplication` — which reads the **table**, not the
JSON blob (confirmed: `AppFlowDebugService.Start`,
`go/internal/admin/service/appflow_debug.go` ~line 112-121). If the table row
doesn't exist, `debug/start` returns a bare **404** with no explanation —
easy to misread as "app doesn't exist."

**So: after creating the draft definition, you must ALSO explicitly create
the entry point** via its own API call:
```
POST /api/v1/admin/applications/{app_id}/entry-points
{ "slug": "my-app-ws", "entry_point_type": "websocket", "enabled": true }
```
Use the **same slug** you put in the definition JSON's `entry_points[].slug`
so the two stay consistent (not enforced by the API — your responsibility).

EP has no self-documenting endpoint like §1's node-types — this section is
the only record of its rules. Re-verify against
`go/internal/admin/applications.go`'s `CreateEntryPoint` /
`go/internal/appflow/workflow.go`'s `EntryPointSlug` handling if this ever
seems wrong. **Flagged to the user 2026-09-26: worth adding a live
self-documenting endpoint for EP/app-shape rules later, matching the
node-types pattern — not done yet, deliberately deferred as low-priority
(EP's shape is small and rarely changes) in favor of writing it down here
first.**

---

## 4. Full build + debug sequence (verified live, 2026-09-26)

1. **Login** — `POST /api/v1/auth/login` on `them-auth-go` (NOT `them-go-bridge`),
   body `{"username": "admin", "password": "admin123"}` (documented test
   credential, `CLAUDE.md`). Returns `{"access_token": "..."}`. Use as
   `Authorization: Bearer <token>` on everything else.
2. **Create the application** — `POST /api/v1/admin/applications` on
   `them-go-bridge:8002`, body `{"name": "...", "slug": "...", "enabled": true}`.
   Returns `{"id": "<app_uuid>"}`.
3. **Create the draft definition** — `POST /api/v1/admin/applications/{app_id}/definitions`,
   body `{"definition": <the full JSON from §2>}`. Returns `{"id": "<def_uuid>", "revision": N}`.
4. **Create the entry point row** — see §3. Do not skip this.
5. **Start a debug run** — `POST /api/v1/admin/applications/{app_id}/debug/start`:
   ```json
   {
     "entry_point_slug": "my-app-ws",
     "user_message": "the input text",
     "llm_overrides": {
       "llm_1": { "mode": "general", "provider": "anthropic", "key_id": 143 }
     }
   }
   ```
   Every `llm`-kind component in the definition needs an entry in
   `llm_overrides`, keyed by its `instance_id`, or the run is rejected before
   admission (422). Two modes:
   - `"general"` — uses a real saved tenant key. `key_id` is the
     `them.llm_provider_keys.id` row (query it, or ask the user — don't
     guess/hardcode across sessions, key IDs are per-environment).
   - `"custom"` — a one-off key for this run only:
     `{"mode": "custom", "provider": "mock", "model": "mock", "api_key": "unused"}`.
     **Known inconsistency**: `provider: "mock"` still requires a non-empty
     `api_key` string in custom mode even though the mock provider never
     actually uses it (`resolveLLMOverride` vs `multiLLMFactory.newProvider`
     disagree on this) — pass any placeholder string to work around it.
   Returns `{"run_id": "...", "expires_at": "...", "workflow_id": "..."}`.
6. **Poll the result** — `GET /api/v1/admin/applications/{app_id}/debug/{run_id}/result`.
   **No "done"/"complete" field exists on this response** — only each
   node's own `status` (`completed`/`running`/etc) inside `node_results`.
   There is no reliable "stop polling now" signal via this endpoint; the
   real frontend avoids this entirely by consuming `/ws/dashboard`'s live
   event stream instead of polling. When scripting a check, poll a fixed
   number of times over a fixed total wait and print the final snapshot —
   don't try to detect "done" cleverly, there's nothing to detect it with.

All of the above was run for real against the live stack this session (not
assumed) — see `docs/APP_CANVAS_VERIFICATION_PLAN.md` for the actual apps
built and their results.

---

## 5. Practical tips

- Run everything from inside `them-frontend` (`docker exec them-frontend node <script>`)
  — it's on the same Docker network as `them-go-bridge`/`them-auth-go`, no
  port-forwarding needed, and `node`'s built-in `fetch` is available.
- Never hardcode a `key_id` across sessions — provider keys are
  environment-specific. Query `them.llm_provider_keys` or ask the user.
- `a2a_echo` (used in this session's test apps) genuinely echoes its input
  unchanged — if an agent's output looks suspiciously identical to the
  upstream LLM's output, that's correct behavior for this specific test
  agent, not a bug.
- Before trusting an old/previously-built test app's Condition logic,
  actually read its `expression` — a condition that's structurally valid can
  still be logically wrong (e.g. `{{gt (len .sentiment) 0}}` checks "is there
  any text" not "is it positive," and is therefore always true regardless of
  input).
