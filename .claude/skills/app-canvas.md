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
- **`router`** — LLM-based intent classifier; `output_labels` (array) drives
  both the edge labels AND the port handles on the node — one spread handle
  per label appears automatically. Edge labels must match `output_labels`
  entries exactly (case-insensitive), or the run fails. **Debug requires an
  `llm_key` param** (same picker as the `llm` node) — the credential field
  appears in the debug setup panel because the router makes a real LLM call.
  Optional `classifier_prompt` lets you guide the LLM's classification logic.
- **`hil`** — pauses for human approval. Has two optional outgoing edge
  labels: **`"approved"`** (green port, taken when approved) and
  **`"rejected"`** (red port, taken when rejected). If no `"rejected"` edge
  is wired, rejection ends the run with status `"rejected"`. If a `"rejected"`
  edge is wired, the flow continues down that branch instead of terminating —
  use this to compose a rejection-response LLM node. An unlabelled edge is
  treated as the approval path (same as labelling it `"approved"`). Writes
  `hil_decision` (`"approved"`/`"rejected"`) and `hil_comment` (approver's
  text) as FlowVars. **No pre-run debug params** — approve/reject happens
  mid-run: click the HIL node in the
  debug inspector panel while it is in `running` state to see the
  Approve/Reject buttons.

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
- `kind: "inline"` for `llm`/`condition` ONLY. `kind: "flow_control"` for
  `fork`/`join`/`router`/`hil` — a DIFFERENT `definition_ref.kind`, not
  `"inline"` (confirmed 2026-09-26 the hard way: using `"inline"` for a
  `fork`/`join` node fails validation with `unknown_inline_node` —
  `go/internal/appflow/compiler.go`'s `compileNode` switches on
  `DefinitionRef.Kind` first, and only `"inline"` name-switches into
  `llm`/`condition`; `"flow_control"` is the separate branch that
  name-switches into `router`/`hil`/`fork`/`join`). `namespace` is always
  `"builtin"` for both cases.
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

## 2b. Cycle nodes — ground truth (read compiler.go + workflow.go, 2026-10-03)

A `cycle` node is a loop container. It is `kind: "flow_control"`, `name: "cycle"`.
All nodes **inside** the loop body get `"parent_instance_id": "<cycle_instance_id>"` on their component.
The cycle node itself has NO `parent_instance_id`.

### Cycle component config fields (CycleConfig in compiler.go)
```json
{
  "node_type": "cycle",
  "display_name": "My Loop",
  "break_when_var": "done",
  "break_when_op": "eq",
  "break_when_val": "true",
  "max_iterations": 10,
  "entry_node_id": "first_body_node_id"
}
```
- `break_when_op`: `"eq"` | `"neq"` | `"truthy"`
- `break_when_val`: omit or `""` when `break_when_op` is `"truthy"`
- `entry_node_id`: instance_id of the **first** body node to run. **Required** — the compiler
  falls back to auto-detect (zero-in-count), but explicit is always correct.
- `break_when_var`: the flow variable the loop checks after each iteration.

### How body nodes are defined
```json
{
  "instance_id": "cond_inside",
  "parent_instance_id": "cycle_1",         ← REQUIRED — links to the cycle
  "definition_ref": { "kind": "inline", "name": "condition", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "condition", "expression": "{{numgt .refund_amount \"0\"}}" }
}
```
Every body node must have `"parent_instance_id"` set to the cycle's `instance_id`.
Use the correct `definition_ref.kind` just like top-level nodes: `"inline"` for llm/condition,
`"flow_control"` for hil/fork/join/router/wait_for_input.

### How body edges are defined
Body-internal edges go in the top-level `connections` array — same as any other edge.
The compiler extracts them into `CycleConfig.BodyEdges` automatically (it selects edges
whose source AND target both have `parent_instance_id == cycleInstID`).

```json
{ "type": "flow_control", "source": "cond_inside", "target": "llm_set_flag", "label": "true" }
```

Loop-back edges (e.g. last body node → first body node) are also in `connections`:
```json
{ "type": "flow_control", "source": "llm_reextract", "target": "cond_inside" }
```

### Outer connections (FLOW IN / FLOW OUT)
The cycle node is treated as a single opaque node in the outer graph:
```json
{ "type": "flow_control", "source": "llm_before", "target": "cycle_1" }      ← FLOW IN
{ "type": "flow_control", "source": "cycle_1", "target": "next_node" }        ← FLOW OUT
```
These are plain top-level connections. No special label needed.

### Rejected/escape edges from body nodes to the outer graph
If a body node (e.g. `hil` inside the cycle) routes to a node **outside** the cycle on reject,
that edge source is inside the cycle but target is outside. The compiler's `compileCycleBody`
only collects edges where BOTH source and target are children — so the outer edge goes in
`connections` and is handled at the outer EPFlow level, not in BodyEdges:
```json
{ "type": "flow_control", "source": "hil_approval", "target": "llm_refund_rejected", "label": "rejected" }
```
Note: `llm_refund_rejected` must NOT have a `parent_instance_id` — it lives at the root level.

### wait_for_input node (used inside cycle bodies or standalone)
```json
{
  "instance_id": "wait_user_amount",
  "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "flow_control", "name": "wait_for_input", "version": 1, "namespace": "builtin" },
  "config": {
    "node_type": "wait_for_input",
    "display_name": "Wait for Corrected Amount",
    "prompt": "{{.rephrase_prompt}}",
    "output_var": "input",
    "timeout_seconds": 300
  }
}
```
`output_var` defaults to `"input"` — the next user message is stored in that flow variable.

### Complete minimal cycle example (validate-amount pattern)
```json
// Components (at root level):
{ "instance_id": "cycle_1",
  "definition_ref": { "kind": "flow_control", "name": "cycle", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "cycle", "display_name": "Validate Amount", "break_when_var": "amount_valid",
              "break_when_op": "truthy", "break_when_val": "", "max_iterations": 3,
              "entry_node_id": "cond_valid" } }

// Body nodes (parent_instance_id = "cycle_1"):
{ "instance_id": "cond_valid", "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "inline", "name": "condition", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "condition", "expression": "{{numgt .amount \"0\"}}" } }

{ "instance_id": "llm_set_flag", "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "inline", "name": "llm", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "llm", "display_name": "Mark Valid", "output_var": "amount_valid",
              "user_prompt": "true", "system_prompt": "Output only the word true." } }

{ "instance_id": "llm_ask", "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "inline", "name": "llm", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "llm", "display_name": "Ask User", "output_var": "ask_prompt",
              "user_prompt": "Ask them to re-enter the amount as a number.", "system_prompt": "You are helpful." } }

{ "instance_id": "wait_1", "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "flow_control", "name": "wait_for_input", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "wait_for_input", "display_name": "Wait for Amount",
              "prompt": "{{.ask_prompt}}", "output_var": "input", "timeout_seconds": 300 } }

{ "instance_id": "llm_reextract", "parent_instance_id": "cycle_1",
  "definition_ref": { "kind": "inline", "name": "llm", "version": 1, "namespace": "builtin" },
  "config": { "node_type": "llm", "display_name": "Re-Extract", "output_var": "amount",
              "user_prompt": "{{.input}}", "system_prompt": "Extract only the number. Reply with digits only." } }

// Connections:
{ "type": "flow_control", "source": "llm_before_cycle", "target": "cycle_1" }       // FLOW IN
{ "type": "flow_control", "source": "cycle_1", "target": "next_after_cycle" }        // FLOW OUT
{ "type": "flow_control", "source": "cond_valid", "target": "llm_set_flag", "label": "true" }
{ "type": "flow_control", "source": "cond_valid", "target": "llm_ask", "label": "false" }
{ "type": "flow_control", "source": "llm_ask", "target": "wait_1" }
{ "type": "flow_control", "source": "wait_1", "target": "llm_reextract" }
{ "type": "flow_control", "source": "llm_reextract", "target": "cond_valid" }        // loop-back
```

### Common mistakes
1. **Forgetting `parent_instance_id`** on body nodes → compiler treats them as outer nodes → cycle has empty body.
2. **Putting cycle node itself inside another cycle** without intending nesting — check no `parent_instance_id` on the cycle component itself (unless intentionally nested).
3. **Adding an edge from a body node to the cycle's FLOW OUT target** — wrong. Body nodes connect to `cycle_1`, not to the outer target. The FLOW OUT edge is `cycle_1 → next_node`.
4. **Omitting `entry_node_id`** — auto-detect works only if one body node has zero incoming body edges. If the body has a loop-back (which all real cycles do), auto-detect will find zero-in-count nodes correctly only if the loop-back arrives at a non-entry node. Always set `entry_node_id` explicitly.
5. **Using `"kind": "inline"` for `wait_for_input`** — it must be `"flow_control"`.

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
     **`provider: "mock"` now works in General mode too** (fixed
     2026-09-26, user request) — the bootstrap tenant has a `mock` provider
     row (id 18) with a default key ("MockKey", id 265, placeholder
     `api_key`, since mock never actually uses one). `resolveLLMOverride`
     (`go/internal/admin/service/appflow_debug_credentials.go`) always looks
     up a real saved key row regardless of provider name — there is no
     special-case for `mock`, it just needed a row to find, same as any
     other provider. If this fix is missing in a different environment,
     recreate it via `POST /admin/my/llm-providers/mock/keys` with any
     placeholder `api_key` — not a code change, just missing seed data.
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

- **After patching a published definition directly in the DB** (e.g. adding a
  connection), also update any existing draft definitions for that app — the
  canvas always loads the latest draft, not the published version. Drafts are
  auto-created by seeding from the published definition the first time the app
  is opened, so any draft created before the patch will be stale. Fix:
  ```sql
  UPDATE them.application_definitions
  SET definition = (SELECT definition FROM them.application_definitions WHERE id = '<published_id>')
  WHERE application_id = '<app_id>' AND status = 'draft';
  ```

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
