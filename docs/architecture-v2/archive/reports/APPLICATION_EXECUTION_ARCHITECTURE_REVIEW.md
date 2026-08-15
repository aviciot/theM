# Application Execution Architecture Review
# Scope: definitive three-layer model — Application Definition, Compiled Runtime Model, Per-Run Execution State
# Source-of-truth decision, execution model decision, revised migration roadmap (Waves 8–15)
# Date: 2026-08-02
# Supersedes on source-of-truth question: APPLICATION_MODEL_ARCHITECTURE_REVIEW.md (2026-08-02)

---

## 0. How to read this document

This review governs implementation decisions for Waves 8 through 15. It treats the current
Python implementation as **evidence, not target**. Where the previous review
(`APPLICATION_MODEL_ARCHITECTURE_REVIEW.md`) concluded that relational rows are the canonical
source of truth, this review **re-opens that question**, evaluates three options against a
concrete future-workload scenario, and lands a **revised** position. Sections 6, 16, 17, and 18
change decisions from the previous review. Everything else is confirmed or extended.

The previous review answered "how do we migrate the five special-op endpoints". This review
answers the larger question the platform now faces: **is the Application model correctly
layered so that complex dynamic agentic execution (parallel calls, AND/OR joins, clarification
loops, nested delegation, retries, cancellation, future ADK agents) can be added without
re-architecting the persistence layer.**

---

## 1. Executive Summary

**Source-of-truth decision — REVISED to Option C (hybrid), with a staged adoption path.**
The previous review declared relational rows the single source of truth and the graph a
transient edit payload. That conclusion is correct *for the runtime* but wrong *for the design
artifact*. This review adopts **Option C**: an immutable, revisioned JSON **Application
Definition** becomes the canonical *design* source of truth, and the existing relational rows
(`app_orchestrators`, `entry_points`, `middleware_wirings`) become the **compiled runtime
projection** derived from a published definition. The definition is authoritative for what the
application *is*; the relational projection is authoritative for what the runtime *reads at
connection time*. Drift is prevented because the projection is only ever written by the compile
step and is stamped with the `definition_id` + `definition_hash` it was produced from. This is a
superset of the current design — the current tables survive unchanged as the projection; we add a
definition table above them. **No runtime code changes to adopt the definition layer** — the
runtime keeps reading the same relational rows.

**Execution model decision — CONFIRM the existing Temporal model; it is already correct.**
Reconnaissance of `app/temporal/workflows.py` and `go/internal/temporal/` shows the platform
already runs the target dynamic execution engine: one `OrchestrationWorkflow` per conversation
`context_id`; each agent/tool call is a Temporal **Activity** (`invoke_agent_activity`); each
orchestrator→orchestrator delegation is a **Child Workflow** (`execute_child_workflow`) bounded
at `_MAX_SUB_ORCH_DEPTH = 3`; parallel fan-out is `asyncio.gather` over concurrently-scheduled
activities bounded by an `asyncio.Semaphore(max_parallel_tools)`; clarification is a
`wait_condition` pause resumed by the `submit_human_response` signal; retries are per-activity
`RetryPolicy`; cancellation is Temporal-native `handle.cancel()`. The example scenario in this
prompt (parallel Payments+Fraud, ambiguous result, clarification re-call, nested Ledger call) is
**already expressible** with these primitives. The design work is therefore *not* to rebuild the
engine — it is to (a) let the Application Definition declare the **policies** the engine reads
(max depth, parallelism, timeout, AND/OR join intent) instead of hard-coding `= 3` / `= 4`, and
(b) fix the current AND-only join and the fragile `agent__orch__` name coupling.

**Key changes vs the previous review.** (1) Source of truth becomes the JSON definition, not the
relational rows — the rows are demoted to a compiled projection. (2) `schema_version` grows into a
proper `Application Definition v1` object with a `definition_id`, `revision`, and content
`definition_hash`; revision metadata **is added now** (three columns + one table), even though
full multi-version history and rollback UI are deferred. (3) **Save and Publish become distinct
operations**: Canvas edits a *draft* definition (cheap, no compile, no cache flush); Publish
validates, compiles the projection, stamps the hash, and flushes caches. (4) The
`app_orchestrators` concept is **kept but reframed** as a compiled projection row and renamed in
documentation to *agent binding* — no table rename in Waves 8–11. (5) Wave 8 scope is **reduced**
— export moves out of Wave 8 because export must now serialize a *definition*, not reconstruct a
graph from rows; export is re-scoped into the definition-layer wave.

**Migration roadmap changes.** The roadmap is re-sequenced around one gating decision: *the
Application Definition layer must land before import/restore/clone/rollback are migrated, and
before any Temporal policy is made definition-driven.* Wave 8 shrinks to the two pure-runtime
endpoints (`PUT /{id}/runtime`, `POST /bulk-delete`) that touch no graph semantics. A new
**Wave 9 — Application Definition Layer** introduces the `application_definitions` table, the
draft/publish split, and the definition-based export/import/restore/clone. Compile-graph porting
moves into Wave 9 as `CompileDefinition`. Waves 10–15 then migrate the remaining product-critical
runtime, A2A, and voice surfaces and finally retire Python. The full re-sequenced plan is in
Section 17.

---

## 2. Current-State Diagram

```
CURRENT PYTHON EDIT/COMPILE FLOW  (canvas → compile → relational rows → export → graph)

  React Flow Canvas (browser)
    graph = { nodes:[{id,type,data}], edges:[{source,target}], canvas:{viewport,layout} }
        │
        │  POST /applications           (create)
        │  PATCH /applications/{id}      (update)
        │  POST /import                  (create-from-snapshot)
        │  PUT  /applications/{id}/restore (overwrite-from-snapshot)
        ▼
  app/routers/admin_applications.py
    validate_graph(graph)          ── pure structural check (~60 lines, no DB)
    compile_graph(db, app_id, ...)  ── graph → relational rows (~265 lines, DB I/O)
        │    · slug-conflict + agent-existence checks (cross-table)
        │    · upsert app_orchestrators  keyed by node_id (canvas-stable identity)
        │    · derive allowed_agent_ids[]  from Orch→Agent edges  (NOT from user input)
        │    · derive delegatable         from Orch→Orch edges    (NOT a checkbox)
        │    · Fernet-encrypt llm/tts/transcription API keys
        │    · upsert entry_points keyed by slug; replace middleware_wirings
        │    returns touched_orch_names[]
    _flush_orch_caches(app_id, names)
        │    DEL them:app:{app_id}:orch:{name}   (per name)
        │    DEL them:orch:loc:{name}            (per name)
        │    DEL them:agents:registry
        │    PUBLISH them:ep:config:changed {app_id}
        ▼
  PostgreSQL  them schema  (SOURCE OF TRUTH TODAY — graph is discarded after compile)
    applications      : id, tenant_id, name, presentation(JSONB), canvas(JSONB),
                        runtime_config(JSONB), enabled          ← canvas stored, graph NOT
    app_orchestrators : id, application_id, tenant_id, node_id, name(immutable, unique),
                        kind, delegatable, allowed_agent_ids UUID[], system_prompt,
                        llm_*/voice_*/tts_*, *_api_key_encrypted(Fernet),
                        max_iterations, max_parallel_tools, history_window, budget_tokens,
                        edges(JSONB)
    entry_points      : id, application_id, app_orchestrator_id(FK), slug(unique),
                        entry_point_type, access_policy(JSONB), conversation_token_limit,
                        max_concurrent_sessions, queue_timeout_seconds, queue_message
    middleware_wirings: id, application_id, agent_id, def_id, position,
                        config_override(JSONB), node_id

  EXPORT PATH (Python only):
    export_graph(entry_points, ao_list, mw_wirings, canvas)  ── pure rows → nodes+edges JSON
        · reconstructs orchestrator/agent/entryPoint/middleware nodes
        · reconstructs EP→Orch and Orch→Agent edges
        · does NOT reconstruct middleware→agent edges (known fidelity gap)
        · does NOT emit encrypted keys (must be re-entered after import)


WHAT GO CURRENTLY DOES  (read path + partial admin write path)

  Client → Traefik → them-go-bridge
    internal/epconfig  (30s in-process cache, sync.Mutex map[slug]*EPConfig)
        SELECT ep.id, ep.slug, ep.entry_point_type, ep.enabled,
               ep.max_concurrent_sessions, ep.queue_timeout_seconds, ep.access_policy,
               a.id, a.tenant_id, a.enabled, a.runtime_config
        FROM them.entry_points ep JOIN them.applications a ...
        WHERE ep.slug=$1
        invalidated by: SUBSCRIBE them:ep:config:changed  (slug OR app-uuid payload)

    internal/admin/applications.go   (Handler→Service→DAL)
        GET/POST/PUT/PATCH/DELETE applications  — writes ONLY name, enabled
        POST/PUT/PATCH/DELETE entry-points      — writes ONLY slug, entry_point_type, enabled
        DELETE = soft delete (enabled=false)
        NO runtime_config write, NO bulk-delete, NO export/import/restore
        NO compile_graph, NO export_graph, NO app_orchestrators DAL, NO middleware DAL

    internal/temporal  (them-orchestration-go queue, gated by TEMPORAL_ENABLED)
        OrchestrationWorkflow → RunOrchestratorActivity (loop)
        HITL via GetSignalChannel("submit_human_response")
        R-4d execution boundary: rejects empty tenant_id/application_id/run_id
```

**Reading of the current state.** The graph is a *write-only* edit payload: it enters, is
compiled to rows, and is thrown away. The only way to see a graph again is to *reconstruct* it
from rows via `export_graph`. This is why export has fidelity gaps (middleware→agent edges, key
material) — reconstruction can never be lossless because information was destroyed at compile
time. That single fact is the strongest argument for the source-of-truth change in Section 6.

---

## 3. Target-State Diagram

```
TARGET THREE-LAYER ARCHITECTURE

┌── LAYER 1 · APPLICATION DEFINITION (canonical design source of truth) ───────────────┐
│                                                                                       │
│  Canvas (React Flow) edits a DRAFT definition                                        │
│      PUT /applications/{id}/definition/draft   → upsert draft (cheap, NO compile)     │
│                                                                                       │
│  PostgreSQL  them.application_definitions                                             │
│    id (definition_id), application_id, tenant_id, revision INT,                       │
│    status ('draft'|'published'|'archived'), schema_version INT,                       │
│    definition JSONB   ← the portable, versioned Application Definition v1             │
│    definition_hash TEXT (sha256 of canonical JSON), canvas JSONB,                     │
│    created_at, published_at                                                           │
│                                                                                       │
│  · Portable, versioned, declarative. Describes WHAT EXISTS and WHAT IS PERMITTED.     │
│  · Export = serialize the published definition verbatim (lossless).                  │
│  · Import/Clone = insert a new definition; Rollback = re-publish an older revision.   │
└───────────────────────────────────────────────────────────────────────────────────────┘
        │   POST /applications/{id}/publish
        │     1. validate_definition()   — pure structural + policy check
        │     2. CompileDefinition()      — definition → relational projection (in a txn)
        │     3. stamp projection rows with definition_id + definition_hash
        │     4. flip definition.status = published; set applications.active_definition_id
        │     5. flush caches + PUBLISH them:ep:config:changed
        ▼
┌── LAYER 2 · COMPILED RUNTIME MODEL (runtime projection — what the engine reads) ──────┐
│                                                                                       │
│  PostgreSQL  them schema  (UNCHANGED tables, now demoted to projection)              │
│    applications.active_definition_id → the currently-live definition revision        │
│    app_orchestrators   ← agent bindings: resolved refs, allowed_agent_ids[] adjacency │
│    entry_points        ← resolved EP→binding bindings, per-EP limits/queue            │
│    middleware_wirings  ← resolved middleware chains                                    │
│    (each projection row carries source_definition_id + source_definition_hash)        │
│                                                                                       │
│  · Read by: epconfig gate, Temporal loaders, run recorder, rate limiter.             │
│  · Written ONLY by CompileDefinition. Never edited directly.                          │
│  · Policy snapshot: max_parallel_tools, max_iterations, budget, delegation adjacency, │
│    (future) max_delegation_depth, timeout policy, join policy.                        │
└───────────────────────────────────────────────────────────────────────────────────────┘
        │   connection time: epconfig(slug) → resolves EP → binding → policy snapshot
        │   run start:      Temporal loader reads app_orchestrator by name + allowed_agent_ids
        ▼
┌── LAYER 3 · PER-RUN EXECUTION STATE (owned by Temporal — never in Postgres as truth) ─┐
│                                                                                       │
│  Temporal  OrchestrationWorkflow  (one per context_id, id = ctx-{context_id})        │
│    · current plan/decision            (self.messages, plan_result.tool_calls)        │
│    · pending / parallel branches      (asyncio.gather over invoke_agent_activity)    │
│    · completed branches + outputs     (InvokeAgentResult list)                       │
│    · clarification / input-required   (wait_condition + submit_human_response signal) │
│    · nested delegation                (execute_child_workflow, depth+1, cap 3)        │
│    · retries                          (per-activity RetryPolicy)                      │
│    · cancellation                     (native handle.cancel())                       │
│    · durable history                  (Temporal Event History, continue_as_new bound) │
│                                                                                       │
│  Postgres (audit projection, not truth): runs (parent_run_id), run_steps, run_usage,  │
│                                          tasks, task_messages, artifacts             │
│  Redis (transient side-channels): them:dash:run:{run_id}:tokens, session hashes,     │
│                                   gate reservations, rate-limit counters             │
└───────────────────────────────────────────────────────────────────────────────────────┘

TEMPORAL BOUNDARY: everything above the run-start arrow is definition/config (durable in PG,
cached in Redis, immutable per revision). Everything below is execution state (durable in
Temporal history, projected to PG for audit). The two never share a mutable store.
```

---

## 4. Three-Layer Analysis

| Aspect | Layer 1 — Application Definition | Layer 2 — Compiled Runtime Model | Layer 3 — Per-Run Execution State |
|---|---|---|---|
| **Contains** | Metadata, entry points, root binding ref, available agent refs, permitted delegation edges, tools/integrations, runtime limits (depth/calls/timeout/parallelism/memory/security), middleware bindings, canvas layout, deployment refs | Resolved agent refs, resolved EP bindings, delegation adjacency map, policy snapshot, per-EP limits, middleware/tool bindings, definition hash stamp | Current plan, pending/parallel/completed branches, agent outputs, retries, clarification requests, nested delegation tree, durable event history |
| **Stored in** | `them.application_definitions.definition` (JSONB), one row per revision | `them.app_orchestrators` + `them.entry_points` + `them.middleware_wirings` (+ `applications.active_definition_id`) | Temporal Event History (truth); `them.runs`/`run_steps`/`run_usage`/`tasks`/`task_messages` (audit projection); Redis (transient stream/session/gate) |
| **Written by** | Canvas draft save; import; clone; rollback (all produce a new definition row) | `CompileDefinition` only, inside the publish transaction | Temporal worker (activities + workflow); run recorder mirrors to PG |
| **Read by** | Admin UI (canvas load, export), publish pipeline, diff/audit tooling | epconfig gate, Temporal loaders, run recorder, rate limiter, session cap enforcement | The workflow itself; dashboard WS/SSE streaming; runs API for audit |
| **Lifetime** | Immutable per revision; retained indefinitely (revisions accumulate) | Overwritten atomically on each publish; only the active revision's projection is live | Per run/context; bounded by `continue_as_new`; finalized to PG on completion/cancel |
| **Authoritative for** | *What the application is* (design intent) | *What the runtime executes* (connection-time + run-start config) | *What actually happened* (execution reality) |
| **Relationship** | Compiles down into Layer 2 on publish; Layer 2 rows carry back-pointer `source_definition_id`/`hash` | Feeds Layer 3 at run start (loader reads binding + adjacency); never mutated by Layer 3 | Reads Layer 2 as immutable config; writes nothing back to Layer 1/2 except audit rows keyed by run |

**The critical invariant:** information only ever flows *down* between layers within a lifecycle
(definition → projection → run), and back *up* only as immutable audit references (a run row
records `orchestrator_name` and could record `definition_id`). No layer mutates the layer above
it. This is what makes the system debuggable: given a `run_id` you can find its `definition_hash`,
fetch the exact definition revision that produced it, and reconstruct the permitted topology as it
was at run time — even after later edits.

---

## 5. Option A / B / C Evaluation

Definitions restated:

- **Option A — Relational source of truth (current).** Canvas graph compiles into normalized
  tables; export reconstructs JSON from rows. No stored definition.
- **Option B — JSON definition source of truth.** Store the complete versioned definition as
  JSONB; compile into relational/runtime projections. The relational rows exist *only* as a cache.
- **Option C — Hybrid.** Store an immutable/revisioned JSON definition as the design source of
  truth, plus relational compiled projections for querying and runtime. Definition carries
  revision + hash. Temporal receives the compiled immutable revision at run start. Canvas edits a
  draft definition; Publish validates and compiles; execution state stays only in Temporal.

| Criterion | A — Relational (current) | B — JSON only | C — Hybrid (recommended) |
|---|---|---|---|
| **Correctness** | OK for runtime; *lossy* for design (compile destroys graph info) | Correct design record; runtime must parse JSON on every hot path or maintain its own cache anyway | Correct on both axes — definition lossless, projection purpose-built for runtime |
| **Simplicity** | Simplest today (one path) | Simple model, but forces re-plumbing every runtime reader (epconfig, loaders, rate limiter) to read JSON | Adds one table + publish step; runtime readers unchanged |
| **Runtime performance** | Fast — indexed columns, no JSON parse on hot path | Slower or forces a de-facto projection cache (reinvents A) | Fast — identical to A at runtime (reads same columns) |
| **Consistency risk** | Low internally, but no way to detect divergence between "what UI showed" and "what runs" | Low (single store) but every reader must agree on JSON parsing rules | Managed: projection stamped with `definition_hash`; mismatch is detectable and repairable |
| **Migration effort** | None (already built) | High — rewrite all runtime readers; risky big-bang | Moderate — additive; new table + publish endpoint + `CompileDefinition`; readers untouched |
| **Canvas usability** | Save is heavy (always compiles + flushes cache); no cheap draft; no undo | Cheap draft/save; but canvas layout + runtime config now interleaved in one blob | Cheap draft save (no compile); explicit publish; clean draft-vs-live distinction |
| **Export/import fidelity** | Lossy (reconstruction gaps: middleware edges, keys, unpublished intent) | Lossless (export = the JSON) | Lossless (export = published definition verbatim) |
| **Versioning** | Impossible without schema change; PATCH overwrites | Natural — each definition is a value; revisions trivial | Natural — revision + hash columns; history deferred but not blocked |
| **Temporal integration** | Loader reads live rows; a mid-run edit can change the topology under a running workflow | Loader must parse JSON at run start (extra work on a hot path) | Loader reads immutable projection; run can pin `definition_id` so a mid-run publish never mutates an in-flight run |
| **ADK compatibility** | Agent UUID FK works, but no place to declare ADK sub-graph structure declaratively | JSON can hold arbitrary ADK structure — risk of ADK leaking into the core schema | JSON definition holds an *opaque, versioned* agent-ref block; projection stays a UUID FK — clean boundary (Section 12) |
| **Operational debugging** | Given a run, cannot recover the exact topology at run time | Can recover definition, but must re-derive adjacency to reason about runtime | Best — run → `definition_hash` → exact revision → exact adjacency; projection is queryable |

**Verdict.** Option A wins only on "already built" and "nothing to do", and it fails the two
criteria that matter for the platform's stated future: **export/import fidelity** and
**definition versioning / reproducibility**. Option B fixes those but pays for them by dragging
JSON parsing onto every runtime hot path or silently reinventing a projection cache — at which
point it *is* Option C with worse ergonomics and a riskier migration. Option C keeps everything A
does well at runtime, adds the lossless design record B does well, and does so **additively** on
top of the existing tables. Option C is chosen.

---

## 6. Recommended Architecture — Option C (Hybrid)

**Decision: adopt Option C. The JSON Application Definition is the canonical design source of
truth. The existing relational rows become the compiled runtime projection.** This overturns the
previous review's "relational rows are the source of truth" conclusion *for the design artifact*,
while confirming it *for the runtime*.

### 6.1 What the JSON definition contains (authoritative for design)

Everything a human or the canvas expresses as intent, and nothing that is a runtime-only
derivation:

- Application metadata: `name`, `presentation`, `schema_version`.
- `entry_points[]`: slug, type, access policy, per-EP limits (token limit, max concurrent
  sessions, queue timeout + message), and the id of the binding that answers the EP.
- `bindings[]` (formerly `app_orchestrators`): each an agentic node — its stable `node_id`,
  immutable `name`, `kind`, LLM/voice/TTS config, prompts, and runtime limits
  (`max_iterations`, `max_parallel_tools`, `history_window`, `budget_tokens`).
- `agent_refs[]`: catalog agent UUIDs the app uses (may be A2A agents or, later, ADK agents).
- `delegation[]`: explicit *allowed* binding→binding delegation edges (declares `delegatable`).
- `tool_grants[]`: binding→agent edges (declares `allowed_agent_ids`) and binding→middleware→agent
  chains (declares middleware wirings *including the edges the current export loses*).
- `policies`: application-level limits — `max_delegation_depth`, `max_agent_calls`,
  `timeout_policy`, `parallelism_policy`, `memory_policy`, `security_policy`. These are the knobs
  that today are hard-coded constants in the workflow.
- `canvas`: React Flow layout/viewport (design-only; never compiled).
- `runtime_config`: blocked tokens/users, app rate limit, soft session cap.
- Secret material is **referenced, never embedded** — the definition stores provider + a flag that
  a key exists, not the key. Fernet ciphertext lives only in the projection.

### 6.2 What the relational projection contains (authoritative for runtime)

Exactly the current tables, unchanged in shape, plus a `source_definition_id UUID` and
`source_definition_hash TEXT` stamp on `applications` (and optionally on each child table). The
projection holds *resolved, indexed, decrypt-ready* forms: `allowed_agent_ids UUID[]` adjacency,
`entry_points.app_orchestrator_id` bindings, `*_api_key_encrypted` Fernet ciphertext,
per-EP integer limits. Runtime readers (`epconfig`, Temporal loaders, rate limiter) are unchanged.

### 6.3 Which is authoritative, and how drift is prevented

- **Design authority:** the definition. If the definition and the projection disagree about
  *what the app should be*, the definition wins and the projection is recompiled.
- **Runtime authority:** the projection. If a request arrives, the runtime reads the projection —
  it never parses the definition on the hot path.
- **Drift prevention (three mechanisms):**
  1. **Write monopoly.** The projection is written *only* by `CompileDefinition` inside the
     publish transaction. No admin endpoint edits projection rows directly once the definition
     layer is live. (During migration, legacy Python `compile_graph` is the temporary writer; see
     Section 15 on the compatibility window.)
  2. **Hash stamping.** Every publish writes `applications.active_definition_id` and
     `source_definition_hash`. A background integrity check (or a `GET /{id}/verify`) recomputes
     the projection from the active definition and compares to the stored hash; mismatch =
     detectable, repairable by re-publish.
  3. **Run pinning.** A run records the `definition_id` it started under. A mid-run `publish`
     replaces the projection for *new* runs; in-flight Temporal workflows already hold their loaded
     config in workflow history and are unaffected — so a publish can never corrupt a running
     conversation.

### 6.4 Why not simply keep A

Because A cannot answer three questions the platform must answer: *"show me exactly what this app
looked like at 14:03 when run X executed"*, *"export this app and import it into staging with 100%
fidelity"*, and *"roll back yesterday's bad publish"*. All three require a retained, lossless,
revisioned definition. A destroys the graph at compile time and can only approximate it on
export. C retains it.

---

## 7. Application Definition v1 — Concrete JSON Example

A two-binding application (a router orchestrator that may delegate to a payments orchestrator),
two entry points (a websocket and an SSE door), three agents, one middleware. Comments (`//`) are
explanatory only and are **not** valid JSON — strip before storing.

```jsonc
{
  "schema_version": 1,                          // integer; bump only on breaking format change
  "definition_id": "e2b1...-uuid",              // stable id of THIS revision (set by server)
  "application_id": "a17c...-uuid",             // the parent application this revision belongs to
  "revision": 4,                                // monotonic per application
  "status": "published",                        // draft | published | archived
  "definition_hash": "sha256:9f2c...",          // canonical-JSON hash (server-computed)

  "name": "Support Concierge",
  "presentation": { "color": "#4f46e5", "description": "Front-door support router" },

  // ── Agentic nodes (compiled into app_orchestrators; documented as "bindings") ──
  "bindings": [
    {
      "node_id": "orch_router",                 // canvas-stable identity (immutable per node)
      "name": "support_router",                 // immutable runtime name (Temporal reads this)
      "kind": "standard",
      "role": "root",                           // exactly one binding per reachable EP is root
      "display_name": "Router",
      "system_prompt": "You route the user to the right specialist.",
      "llm": { "provider": "anthropic", "model": "claude-sonnet", "has_api_key": true },
      "voice": { "transcription_provider": null, "transcription_model": null },
      "tts": { "provider": null, "voice": null },
      "limits": {
        "max_iterations": 10,
        "max_parallel_tools": 4,                // engine reads this instead of the hardcoded 4
        "history_window": 20,
        "budget_tokens": null
      }
    },
    {
      "node_id": "orch_payments",
      "name": "payments_orch",
      "kind": "standard",
      "role": "sub",
      "display_name": "Payments",
      "system_prompt": "You handle payment and ledger questions.",
      "llm": { "provider": "anthropic", "model": "claude-sonnet", "has_api_key": true },
      "limits": { "max_iterations": 8, "max_parallel_tools": 2, "history_window": 12,
                  "budget_tokens": 50000 }
    }
  ],

  // ── Catalog agent references (compiled into allowed_agent_ids adjacency) ──
  "agent_refs": [
    { "node_id": "agent_fraud",   "agent_id": "f1a2...-uuid", "kind": "a2a" },
    { "node_id": "agent_ledger",  "agent_id": "1ed6...-uuid", "kind": "a2a" },
    { "node_id": "agent_kb",      "agent_id": "9b0c...-uuid", "kind": "a2a" }
    // A future ADK agent would appear here as { kind: "adk", agent_id: "<uuid>" } — Section 12.
  ],

  // ── Permitted delegation (binding→binding). Presence of an edge == delegatable=true. ──
  "delegation": [
    { "from": "orch_router",   "to": "orch_payments" }
  ],

  // ── Tool grants (binding→agent, and binding→middleware→agent chains). ──
  "tool_grants": [
    { "from": "orch_router",   "to": "agent_fraud" },   // router may call Fraud directly
    { "from": "orch_router",   "to": "agent_kb", "via": ["mw_pii"] }, // through PII middleware
    { "from": "orch_payments", "to": "agent_ledger" }   // payments may call Ledger
  ],

  // ── Middleware nodes (compiled into middleware_wirings; edges now PRESERVED via tool_grants.via)
  "middleware": [
    { "node_id": "mw_pii", "def_id": "c0ff...-uuid", "config_override": { "redact": ["ssn"] },
      "enabled": true }
  ],

  // ── Entry points (compiled into entry_points). Each names the binding that answers it. ──
  "entry_points": [
    {
      "node_id": "ep_ws",
      "slug": "support-ws",
      "type": "websocket",
      "binding": "orch_router",
      "access_policy": { "mode": "token" },
      "limits": {
        "conversation_token_limit": null,
        "max_concurrent_sessions": 50,
        "queue_timeout_seconds": 30,
        "queue_message": "All agents are busy, holding your place…"
      }
    },
    {
      "node_id": "ep_sse",
      "slug": "support-sse",
      "type": "sse",
      "binding": "orch_router",
      "access_policy": { "mode": "token" },
      "limits": { "conversation_token_limit": 200000, "max_concurrent_sessions": null,
                  "queue_timeout_seconds": null, "queue_message": null }
    }
  ],

  // ── Application-level runtime policy (compiled into applications.runtime_config) ──
  "runtime_config": {
    "max_concurrent_sessions": 200,
    "rate_limit_rpm": 600,
    "blocked_tokens": [],
    "blocked_user_ids": [],
    "session_timeout_minutes": null            // accepted + persisted, not enforced (see prev review)
  },

  // ── Execution policy — the knobs the Temporal engine should read, not hardcode ──
  "policies": {
    "max_delegation_depth": 3,                 // today hardcoded _MAX_SUB_ORCH_DEPTH = 3
    "max_agent_calls": 200,                    // guard against runaway fan-out
    "timeout_policy": { "default_agent_seconds": 30, "run_seconds": 900 },
    "parallelism_policy": { "default_max_parallel": 4, "join": "all" }, // all | any | quorum:N
    "memory_policy": { "summarize_after_messages": 200 },
    "security_policy": { "require_tenant_scoped_run": true }
  },

  // ── Canvas layout — design-only, never compiled ──
  "canvas": { "viewport": { "x": 0, "y": 0, "zoom": 1 }, "layout": { "orch_router": {"x":100,"y":80} } },

  // ── Deployment configuration references (opaque; resolved by ops tooling, not the compiler) ──
  "deployment": { "environment_ref": null, "traefik_priority_hint": null }
}
```

Notes on the format:
- **`tool_grants[].via`** is the fix for the current middleware-edge fidelity gap: the chain is
  declared, not reconstructed, so export/import round-trips 100% including middleware topology.
- **`policies`** is new and is the home for every value the workflow hardcodes today
  (`_MAX_SUB_ORCH_DEPTH`, the default `max_parallel_tools` fallback of 4, the 10-minute HITL
  timeout, the 200-message `continue_as_new` bound). Migrating these to the definition is deferred
  until the engine reads them (a later wave) but the *slot* exists in v1 so no format break is
  needed later.
- Secrets are never in the definition (`has_api_key: true`, not the key).

---

## 8. Edge Semantics

The current canvas has one undifferentiated edge (`{source, target}`) whose meaning is inferred
from the node types it connects. This overloading is the root cause of both the derived-field
magic (`delegatable`, `allowed_agent_ids`) and the middleware-edge fidelity gap. Definition v1
makes the meaning **explicit** by putting each edge kind in its own block. Four distinct meanings:

| Canvas edge (by endpoints) | Meaning | Definition representation | How the runtime uses it |
|---|---|---|---|
| **EP → binding** | *Entry binding*: which agentic node answers this door | `entry_points[].binding = node_id` | epconfig resolves slug → binding; Temporal loads that orchestrator by name at run start |
| **binding → binding** | *Allowed delegation*: this orchestrator MAY delegate to that one (capability, not a forced call) | `delegation[]` entry; sets `delegatable=true` on the target | Loader exposes the target as a `sub_orchestrator` pseudo-agent; the planner decides at runtime whether to call it (`execute_child_workflow`) |
| **binding → agent** (optionally `via` middleware) | *Tool availability*: this orchestrator MAY call that agent (capability) | `tool_grants[]` (with `via[]` for middleware chains) → `allowed_agent_ids[]` + `middleware_wirings` | Loader builds the agent's NeutralTool list; planner may call it via `invoke_agent_activity` |
| **binding → middleware → agent** | *Interception chain*: calls to the agent pass through middleware | `tool_grants[].via` + `middleware[]` nodes | Compile writes `middleware_wirings` (position-ordered); runtime applies interceptors around the call |

**What each edge is NOT.** None of these edges is a *fixed workflow transition* (a mandatory
"do A then B") and none is a pure *data dependency*. The current platform has **no static
edge type** — every edge is a *capability grant* (allowed delegation / tool availability), and the
*actual* execution order is decided dynamically by the LLM planner at runtime. This is the correct
default for agentic systems and it is why the same topology can produce the parallel-then-
clarify-then-nested-delegate behavior in the example scenario without any static graph.

Section 9 addresses whether/how to add explicit *static* edges for deterministic workflows.

---

## 9. Static vs Dynamic Execution

**Today: 100% dynamic.** Every edge is a capability grant; the LLM planner (`plan_turn_activity`)
decides each turn which granted agents to call, in what combination, and whether to delegate. There
is no representation for "always call A, then B, then C" or "if X then A else B". The scenario in
this prompt is fully dynamic and is already supported.

**Recommendation: keep dynamic as the default; add *optional, declarative* static blocks in a
later definition minor version — do not build a workflow engine now.** The reasoning:

- The four "shapes" (sequence, parallel, condition, loop) already have runtime homes:
  - **Parallel** is the platform's native mode — `asyncio.gather` over fanned-out activities.
  - **Sequence** emerges naturally from the plan→act→observe loop (the planner emits one call,
    sees the result, emits the next).
  - **Loop** is the agentic loop itself, bounded by `max_iterations`.
  - **Condition** is what the LLM planner *does* — it chooses the branch.
- What is *missing* is the ability to make any of these **deterministic** (non-LLM) when a use
  case demands guaranteed order (e.g. compliance: "always run the fraud check before the payment").

**Division of responsibility:**

| Semantic | Expressed in Application Definition? | Or delegated to agent/framework? |
|---|---|---|
| Allowed delegation topology | **Yes** — `delegation[]` | — |
| Tool availability | **Yes** — `tool_grants[]` | — |
| Parallelism *bounds* (max, join policy) | **Yes** — `policies.parallelism_policy` | Actual fan-out chosen by planner |
| Delegation depth / call limits / timeouts | **Yes** — `policies` | Enforced by the engine |
| *Which* agents to call this turn | **No** | LLM planner (dynamic) |
| *Order* of calls (dynamic) | **No** | LLM planner |
| *Guaranteed* order (static compliance flow) | **Future** — optional `flows[]` block (sequence/condition/loop DSL) | Engine executes the static flow deterministically |
| Nested sub-agent structure of an ADK agent | **No** | Inside the ADK agent (opaque — Section 12) |

**Deferred design (not v1):** a `flows[]` block that lets a binding declare a deterministic
sub-workflow (`{ "type":"sequence", "steps":[...] }`, `{ "type":"condition", "when":..., "then":...}`).
When present, the engine runs the static flow; when absent (the default and today's behavior), the
engine runs the dynamic agentic loop. Adding this later needs no format break because `flows[]` is
simply a new optional key. **v1 ships without it** — the platform's demand today is dynamic.

---

## 10. Temporal Execution Semantics — the example scenario, step by step

Scenario: Main may call Payments and Fraud; Payments may call Ledger. Runtime: Main calls
Payments AND Fraud in parallel → waits both → Payments returns ambiguous → Main re-calls Payments
with a clarification → Payments calls Ledger → Main combines the clarified result with Fraud's and
returns.

This maps **directly** onto the primitives confirmed in `app/temporal/workflows.py`. Where each
piece of state lives:

| Scenario step | Mechanism (confirmed in current code) | State location |
|---|---|---|
| Run starts for `context_id` | `OrchestrationWorkflow.run`, id `ctx-{context_id}` | Temporal workflow, one instance |
| Load topology (Main's allowed agents = {Payments, Fraud}, adjacency) | `load_orchestration_context_activity` reads the **projection** (`app_orchestrators.allowed_agent_ids`, delegatable bindings) | Read from PG projection; result held in workflow history |
| Main plans "call Payments and Fraud" | `plan_turn_activity` (LLM) returns `tool_calls=[Payments, Fraud]` | `plan_result.tool_calls` in workflow history |
| **Parallel fan-out** of Payments + Fraud | `invoke_coros = [_invoke_one(tc) …]; await asyncio.gather(*invoke_coros)`, bounded by `asyncio.Semaphore(max_parallel_tools)` | Two concurrent `invoke_agent_activity` (Fraud) and one `execute_child_workflow` (Payments, since Payments is a sub-orchestrator) scheduled from the parent workflow |
| **Wait for both** (AND-all join) | `asyncio.gather` returns only when *all* coros complete; results ordered by list position | Parent workflow blocks in history until both activities/child resolve |
| Payments (child workflow) runs its own loop | Child `OrchestrationWorkflow` with `depth+1`, fresh `context_id`, `parent_run_id=run_id` | Separate Temporal workflow; its steps in its own history; `runs.parent_run_id` links audit rows |
| Payments returns ambiguous | Child returns `{status, final_answer}` → mapped to `InvokeAgentResult` | Returned up to parent's `gather` result list |
| Main evaluates "incomplete → clarify" | Next `plan_turn_activity` turn — LLM sees the ambiguous result in `self.messages`, emits a new `tool_calls=[Payments(clarify=…)]` | Decision in workflow history; **this is a normal next iteration**, not a special primitive |
| **Re-call Payments** with clarification | Another `execute_child_workflow` (new child, `depth+1`) | New child workflow instance |
| Payments calls Ledger (nested delegation) | Inside the child: `_invoke_one(Ledger)` → `invoke_agent_activity` (Ledger is an agent, so Activity) | Child workflow's history; `run_steps` row under the child run |
| **Nested state preserved** across the re-call | Each child is a durable Temporal workflow; `parent_run_id` chains them; depth guarded by `_MAX_SUB_ORCH_DEPTH` | Temporal history per workflow + `runs.parent_run_id` chain in PG |
| Main combines clarified Payments + Fraud, returns | Final `plan_turn` → `finalize_run_activity` | `runs.final_output`, `run_status=completed` |

**Activity vs Child Workflow rule (confirmed and endorsed):**
- **Leaf agent call → Activity** (`invoke_agent_activity`). Cheap, retry-policy-scoped, heartbeated.
- **Orchestrator→orchestrator delegation → Child Workflow** (`execute_child_workflow`). Gets its
  own durable history, its own retry/cancel scope, its own audit run, and independent
  `continue_as_new` bounding. This is the right unit for nested delegation because a sub-
  orchestrator is itself a full agentic loop.

**How the root waits.** Via `asyncio.gather` inside the workflow method — deterministic because
Temporal replays the schedule order and results are consumed by list index, not completion order.

**How clarification works.** Two paths exist and both are correct:
1. *Agent-initiated* HITL: an agent returns `status="input-required"`; the workflow pauses on
   `workflow.wait_condition(lambda: self._human_response is not None, timeout=10m)` and resumes on
   the `submit_human_response` signal. (Human clarification.)
2. *Orchestrator-initiated* clarification (the scenario): the LLM simply issues another tool call
   to the same sub-orchestrator on the next loop iteration. No special primitive — it is a normal
   turn. This is why the design "just works".

**How cancellation propagates.** `cancel_workflow(id)` → `handle.cancel()`. Temporal cancels the
parent; cancellation propagates to in-flight child workflows and activities; `finalize_run_activity`
runs in a shielded scope so every run (parent and children) is finalized to `canceled` in PG.

**How the audit trail is reconstructed.** The real runtime tree is rebuilt from PG audit rows:
`runs` (linked by `parent_run_id` — parent run → Payments child run → its Ledger step),
`run_steps` (one row per agent call with `iteration`, `agent_slug`, `tool_call_id`, `status`,
`latency_ms`), and `task_messages`. Adding `runs.definition_id` (Section 15) lets the audit tree
also point at the exact topology revision that authorized each call.

**The one gap to fix (later wave, not now):** the join is always **AND-all** (`gather` waits for
everything). The scenario doesn't need OR/quorum, but `policies.parallelism_policy.join` in the
definition reserves the slot; the engine change (first-completed / quorum via
`workflow.wait` with `return_when`) is a future wave. Also, the `agent__orch__<name>` double-prefix
name coupling (`removeprefix("agent__")` then `removeprefix("orch__")`) is fragile and should be
replaced by an explicit `transport`/`ref_kind` field carried from the projection.

---

## 11. Data Ownership Table

| Data | PostgreSQL | Temporal / history | Redis |
|---|---|---|---|
| **Application Definition (design truth)** | `application_definitions.definition` (JSONB, per revision) | — | — |
| **Compiled runtime config (projection)** | `app_orchestrators`, `entry_points`, `middleware_wirings`, `applications.active_definition_id` | Loaded into workflow history at run start (immutable snapshot per run) | — |
| **Per-EP config (limits, access, queue)** | `entry_points` columns | — | `epconfig` 30s in-process cache (Go); invalidated via pub/sub |
| **Orchestrator config cache** | `app_orchestrators` (source) | — | `them:app:{app_id}:orch:{name}`, `them:orch:loc:{name}`, `them:agents:registry` |
| **Per-run decision state (plan, messages)** | mirrored to `task_messages` (audit) | **Truth**: `self.messages`, `plan_result` in Event History | — |
| **Parallel branch state (pending/done)** | mirrored to `run_steps` (audit) | **Truth**: in-flight `gather` / scheduled activities in Event History | — |
| **Agent call results** | `run_steps.output`, `tasks`, `artifacts` (audit) | **Truth**: `InvokeAgentResult` in Event History | `them:dash:run:{run_id}:tokens` (transient stream only) |
| **Retry state** | `run_steps.status` reflects final outcome (audit) | **Truth**: Temporal `RetryPolicy` attempt counter in Event History | — |
| **Cancellation signal** | `runs.status='canceled'` (audit result) | **Truth**: native `handle.cancel()` propagation in Event History | — |
| **Audit trail (runtime tree)** | **Truth for audit**: `runs` (`parent_run_id`), `run_steps`, `run_usage`, `tasks`, `task_messages` | Source that the recorder mirrors from | dashboard event fan-out (`them:dash:run:{run_id}`, `them:dash:runs`) — transient |
| **Active sessions** | — | — | **Truth**: session Redis Hash + Lua shadow-TTL; gate reservations; rate-limit counters (`rl:them:app:…`) |

Principle: **Postgres owns durable config truth and audit projection; Temporal owns execution
truth; Redis owns transient/ephemeral coordination.** No datum has two mutable homes.

---

## 12. ADK Compatibility

**Boundary rule: an ADK agent is referenced by catalog UUID exactly like any other agent; its
internal sub-agent structure is opaque to the Application Definition.** This keeps ADK-specific
constructs (its own sub-agent trees, its own tool schemas, its planner) entirely inside the agent
and out of the core schema.

An ADK agent is therefore **an agent reference resolved by the runtime**, not a nested application
component and not an opaque executable node in the *application* graph. It appears in the
definition's `agent_refs[]` with `kind: "adk"` and a UUID; the projection stores that UUID in
`allowed_agent_ids[]` unchanged. The runtime resolves the UUID to a `them.agents` row whose
`adapter_type = "adk"`, and an ADK adapter (new file under `app/adapters/` / `go/internal/…`)
handles transport — identical to how A2A agents are dispatched today via `invoke_agent_activity`.

**Interface an ADK agent must expose to be usable as an agent reference:**
1. A resolvable catalog identity (`them.agents` row: id, name/slug, `adapter_type='adk'`,
   endpoint/handle, optional `input_schema`).
2. A **single invoke contract**: accept `(tool_input, injected_context)` → return
   `{status ∈ {completed, failed, input-required}, result_text, file_parts[], error}` — precisely
   the `InvokeAgentResult` shape the engine already consumes. Whatever multi-agent orchestration
   ADK does internally is hidden behind this call.
3. Cancellation/timeout cooperation: honor the activity's `timeout_seconds` and cancellation so the
   engine's existing retry/timeout/cancel machinery applies unchanged.

Consequences: **no schema change** to the Application Definition or the projection for ADK. If an
ADK agent must itself *appear as an orchestrator that can delegate* to other THEM bindings, it is
modeled as a binding with `kind:"adk"` and its allowed delegations are still declared in
`delegation[]` — the ADK internals remain opaque; only the *permitted outward edges* are declared.
This is the clean boundary Option C's JSON layer buys us: arbitrary future agent shapes live behind
a stable UUID + invoke contract, never leaking into the core tables.

---

## 13. `app_orchestrators` Evaluation

**Decision: KEEP the table; REFRAME it as a compiled projection; RENAME it in documentation and
API vocabulary to *agent binding* (conceptual), but DO NOT rename the physical table in Waves
8–11.** A physical rename is a coordinated live-DB migration touching Temporal loaders, run
recorder, epconfig, and every DAL — high risk, zero functional gain during migration.

Rationale:
- The row is genuinely useful: it is the resolved, indexed, decrypt-ready runtime form of a
  definition binding, and Temporal reads it by immutable `name`. That is exactly what Layer 2
  needs. Replacing it with a "generic agent binding" table now would be a rename with no behavior
  change and large blast radius.
- The *name* `app_orchestrators` is misleading in the target model (these are not the global
  `orchestrators` catalog; they are per-app bindings that may be plain agents, sub-orchestrators,
  or later ADK agents). So the **concept** is renamed to *binding* in the definition
  (`bindings[]`), in docs, and in new Go type names — while the physical table keeps its name.
- The dead `app_orchestrators.orchestrator_id` FK and `applications.orchestrator_id` remain dead
  (confirm not-populated; do not join). The `edges` JSONB column on `app_orchestrators` becomes
  redundant once the definition holds edges — mark it deprecated, stop writing it after the
  definition layer lands, drop it in the cleanup wave.

**Migration path if a physical rename is ever wanted (deferred, optional):** add a view
`them.agent_bindings` over `app_orchestrators`, migrate readers to the view, then rename the base
table underneath the view in a single migration. Not scheduled.

---

## 14. Lifecycle Operation Definitions

`Δproj` = triggers the compile step (writes the relational projection). `Δcache` = flush orch
caches + `PUBLISH them:ep:config:changed`.

| Operation | What it does | DB state produced | Compile? | Cache? | HTTP contract |
|---|---|---|---|---|---|
| **create** | New application + its first draft definition (revision 1) | insert `applications` (enabled, no `active_definition_id` yet); insert `application_definitions` (revision 1, status=draft) | No | No | `POST /applications` → 201 `{application_id, definition_id, revision, status:"draft"}` |
| **update (draft)** | Canvas save; overwrite the working draft (or create a new draft revision) | update/insert `application_definitions` where status=draft | **No** | **No** | `PUT /applications/{id}/definition/draft` → 200 `{definition_id, revision, definition_hash}` |
| **validate** | Pure structural + policy check of a draft; no writes | none | No | No | `POST /applications/{id}/validate` (body: draft or ref) → 200 `{valid:true}` / 422 `{errors[]}` |
| **publish** | Validate + compile draft into projection + stamp hash + flip active | flip draft→published; write projection rows (`app_orchestrators`/`entry_points`/`middleware_wirings`); set `applications.active_definition_id`, `source_definition_hash` | **Yes** | **Yes** | `POST /applications/{id}/publish` → 200 `{active_definition_id, revision, definition_hash}` |
| **activate** | Switch `active_definition_id` to an already-published revision (fast rollout without recompiling from a draft) | recompile that revision's projection (idempotent); update `active_definition_id` | **Yes** (idempotent) | **Yes** | `POST /applications/{id}/activate` `{definition_id}` → 200 |
| **export** | Serialize the published (or a named) definition verbatim | none (read) | No | No | `GET /applications/{id}/export[?definition_id=]` → 200 Application Definition v1 JSON |
| **import** | Create a new application from an exported definition (new ids, re-check agent UUIDs) | insert `applications` + `application_definitions` (draft or auto-published per flag); if published, compile | Yes if `publish=true` | Yes if published | `POST /applications/import` (body: v1 JSON) → 201 `{application_id, definition_id}` |
| **restore** | Overwrite an existing app's definition from a snapshot (new revision), then publish | insert new `application_definitions` revision; publish it | Yes | Yes | `PUT /applications/{id}/restore` (body: v1 JSON) → 200 |
| **clone** | Copy an app's published definition into a brand-new application | insert new `applications` + copied `application_definitions` (draft) | No (until published) | No | `POST /applications/{id}/clone` → 201 `{application_id, definition_id}` |
| **rollback** | Re-activate a prior published revision | = activate on an older `definition_id` | Yes (idempotent) | Yes | `POST /applications/{id}/rollback` `{definition_id}` → 200 |

The key behavioral change from today: **save (draft) is cheap and side-effect-free**;
**publish/activate/restore/rollback are the only compile+cache operations.** This eliminates the
current problem where every PATCH recompiles and flushes caches even for a layout nudge.

---

## 15. Versioning Decision

**Decision: add revision metadata NOW. Defer full history *retention policy*, diff UI, and
automated rollback UX — but not the columns.** Adding the columns later would require backfilling
existing applications with a synthetic revision-1 definition and is strictly harder than adding
them at the same time the definition table is introduced.

**Schema added in Wave 9 (definition layer):**

```sql
CREATE TABLE them.application_definitions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- definition_id
  application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  tenant_id      UUID NOT NULL,
  revision        INTEGER NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('draft','published','archived')),
  schema_version  INTEGER NOT NULL DEFAULT 1,
  definition      JSONB NOT NULL,
  definition_hash TEXT NOT NULL,
  canvas          JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at    TIMESTAMPTZ,
  UNIQUE (application_id, revision)
);
ALTER TABLE them.applications
  ADD COLUMN active_definition_id UUID REFERENCES them.application_definitions(id),
  ADD COLUMN source_definition_hash TEXT;
ALTER TABLE them.runs
  ADD COLUMN definition_id UUID;   -- audit: which topology authorized this run
```

**Fields:** `revision` (monotonic per app), `status` (draft/published/archived), `definition_hash`
(sha256 of canonical JSON — reproducibility + drift detection), `active_definition_id` on the
application (the live revision), `runs.definition_id` (audit linkage).

**Migration for existing apps:** a one-time backfill runs `export_graph`-equivalent on each
existing application's projection to synthesize a revision-1 published definition, computes its
hash, inserts the `application_definitions` row, and sets `active_definition_id`. After backfill,
every app has a definition and the projection is stamped.

**What is deferred (and why it is not blocked):** retention limits (how many revisions to keep),
a visual diff between revisions, and one-click rollback UX. None require format changes — they are
UI/policy on top of the revision rows. Because the revision + hash + status columns exist from day
one, all of this can be added later without touching stored data.

---

## 16. Export / Import / Restore Contract

**Current Python contract:** `{ schema_version, name, presentation, graph:{nodes,edges,canvas}, canvas }`
— where `graph` is *reconstructed* from relational rows by `export_graph` (lossy: middleware→agent
edges dropped, keys omitted).

**Decision: REPLACE with the Application Definition v1 object (Section 7).** Not "adapt" — the
graph-reconstruction format is fundamentally lossy and cannot represent middleware chains, policies,
or revision identity. The v1 definition is the export payload verbatim.

**New contract:**
- `GET /applications/{id}/export` → the published Application Definition v1 JSON (Section 7),
  serialized directly from `application_definitions.definition` (no reconstruction, lossless).
- `POST /applications/import` → accepts v1 JSON; re-validates agent UUIDs against the target
  environment; creates a new application + definition; publishes if requested.
- `PUT /applications/{id}/restore` → accepts v1 JSON; writes a new revision; publishes.

**Backward compatibility during migration.** The importer accepts *both* forms for one release
window: if the payload has `graph.nodes`/`graph.edges` (old form) it is up-converted to a v1
definition (nodes+edges → `bindings`/`agent_refs`/`delegation`/`tool_grants`) on ingest; if it has
`bindings`/`entry_points` (new form) it is used directly. `schema_version` disambiguates:
old exports had `schema_version:1` on the *graph* wrapper; v1 definitions have `schema_version:1`
with a `bindings` array — presence of `bindings` selects the new path. The old export endpoint is
**deprecated** once the definition layer ships and removed when Python is removed.

---

## 17. Updated Migration Roadmap (Waves 8–15)

Legend — Category: **M**=migrate as-is, **R**=redesign-before-migrate, **D**=defer,
**X**=deprecate/remove. "Blocked by def layer" = cannot correctly migrate until Wave 9 lands.

| Wave | Domain | Routes / scope | Depends on | Difficulty | Category | Arch decision needed first? |
|---|---|---|---|---|---|---|
| **8** | App runtime special-ops (pure) | `PUT /{id}/runtime`, `POST /bulk-delete` | epconfig pub/sub (exists) | Low | **M** | No — no graph semantics. Export moved OUT of Wave 8. |
| **9** | **Application Definition Layer** | new `application_definitions` table + backfill; `CompileDefinition` (Go port of compile_graph); draft/publish/validate/activate; `GET /export`, `POST /import`, `PUT /restore`, `POST /clone`, `POST /rollback`; `runs.definition_id` | Wave 8; Fernet (exists) | High | **R** | **Yes — this review is that decision.** Gates 10–14. |
| **10** | Runs read/audit tail | `GET /runs/stats`, `/runs/contexts`, `/runs/{id}/tasks`, `/artifacts`, `/context/{cid}/artifacts`, `POST /runs/bulk-delete`, `DELETE /runs/{id}` | run recorder (Go exists) | Medium | **M** | No |
| **11** | Run control (Temporal cancel/signal) | `PATCH /runs/{id}/cancel`, signal delivery | Go temporal signaler (exists) | Medium | **M** | No |
| **12** | Apps runtime surface (product-critical) | `GET/POST /apps`, `/apps/{slug}` REST, `/apps/{slug}/tasks/{task_id}`, WS/SSE already Go | execution pkg (exists) | Medium | **M** | No |
| **13** | A2A server + agent card | `/.well-known/agent-card.json`, `POST /a2a`, `/a2a/push/{task_id}` | Go `a2a` pkg (exists) | High | **R** | Partially — invoke `/a2a` skill; align typed parts |
| **14** | Admin agent/orch ops + middleware-defs + system-agents | agents test/discover/security-scan, orch test-llm/voice/tts, `middleware-defs` CRUD, `system-agents`, `middleware-wirings` write, per-AO test-llm/voice/tts | Wave 9 (middleware in def) | High | **R** | Yes — middleware wirings become definition-driven |
| **15** | Voice + legacy deprecation + Python removal | `/voice/transcribe`, `/voice/tts`, `webrtc/token`; **deprecate** `POST /orchestrators/{name}/transcribe|tts`, `GET(WS) /ws/orchestrate/{name}`; remove Python | Waves 8–14 | High | **M** + **X** | No |

**Blocked-by-definition-layer:** import, restore, clone, rollback, export (all Wave 9);
middleware-wirings write and any definition-driven admin op (Wave 14); making Temporal policies
definition-driven (post-15 engine work). **Product-critical Python routes:** `/apps` runtime
surface (12), A2A (13), runs read+control (10–11), voice (15). **Legacy / deprecation candidates:**
`POST /orchestrators/{name}/transcribe`, `POST /orchestrators/{name}/tts`, `GET(WS)
/ws/orchestrate/{name}` — all superseded by the Application/EP path; deprecate in 15.

**Sequencing rationale.** Wave 9 is the linchpin: until the definition layer exists, migrating
import/restore just re-ports a lossy format into Go and cements the wrong source of truth. By
landing 8 (trivial, unblocks nothing else) then 9 (the architecture), every later wave migrates
against the correct model.

---

## 18. Exact Next Implementation Wave — Wave 8 (re-scoped)

**Change from the previous review:** the previous Wave 8 was `runtime + bulk-delete + export`.
This review **removes export from Wave 8** and moves it into Wave 9, because export must now emit
an *Application Definition* (Layer 1), which does not exist until Wave 9 builds it. Porting the old
lossy `export_graph` into Go in Wave 8 would produce a format we intend to replace one wave later —
wasted work that ships a deprecated contract. Wave 8 therefore migrates only the two endpoints that
touch pure runtime config and no graph semantics.

**Wave 8 scope (approved):**
1. `PUT /api/v1/admin/applications/{id}/runtime` — JSONB `runtime_config` UPDATE + cache flush.
2. `POST /api/v1/admin/applications/bulk-delete` — hard-delete + cache flush.

**Implementation steps:**
1. Traefik: add `them-go-apps-subroutes` rule for `/{id}/…` write sub-paths (exact rule already
   specified in the previous review §4a; apply to `docker-compose.yml` and
   `docker-compose.traefik.yml`). Do **not** otherwise touch Traefik.
2. Go cache: add `FlushApplicationOrchCaches(ctx, appID, orchNames)` — `DEL
   them:app:{app_id}:orch:{name}`, `DEL them:orch:loc:{name}`, `DEL them:agents:registry`,
   `PUBLISH them:ep:config:changed {app_id}`.
3. DAL (`go/internal/admin/dal/applications.go`): `UpdateRuntimeConfig`,
   `ListAppOrchestratorNames`, `BulkDeleteApplications` (tenant-scoped, `RETURNING id`).
4. Handlers (`go/internal/admin/applications.go`): `PutRuntime`, `BulkDelete`. Runtime struct =
   five fields, pointer types for nullable ints, `session_timeout_minutes` accepted+persisted, not
   enforced (per prior review §Q10). Bulk-delete: pre-fetch orch names, hard-delete, then flush.
5. Wire routes in `go/internal/admin/router.go`.

**Test strategy:** unit tests per handler with a mock DAL; assert cache-flush call order
(delete-then-publish; flush *after* delete for bulk-delete). Update `go/TEST_INDEX.md` in the same
commit. `go test ./...` must pass with zero new failures. Smoke-test through Traefik with the go
profile up; run `scripts/tests/test_routing_fix_contracts.py` from inside `them-bridge` and confirm
the application-write routing tests pass (not skip). Update `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`.

**Commit scope:** the two handlers, their DAL + cache additions, the Traefik rule in both compose
files, the tests, `TEST_INDEX.md`, and the route inventory — one commit, staged file-by-file (no
`git add -A`). Do **not** port `compile_graph`, `export_graph`, or any definition-layer code in
Wave 8.

---

## 19. What Is Required Before Python Can Be Removed

Every prerequisite, grouped by kind.

**Go packages that must exist (new):**
- `go/internal/admin/definition/` — `CompileDefinition` (port of `compile_graph`), `validate`,
  and definition export/serialize (Wave 9). Consumes the existing `go/internal/crypto/fernet.go`.
- `go/internal/admin/dal/definitions.go` — CRUD for `application_definitions`; upsert/delete for
  `app_orchestrators`, `entry_points`, `middleware_wirings` (projection writers).
- `go/internal/admin/dal/` additions for `app_orchestrators` and `middleware_wirings` (neither
  exists in Go today).
- A2A server package parity for `/.well-known/agent-card.json`, `POST /a2a`, `/a2a/push/{task_id}`
  (Wave 13) — extend existing `go/internal/a2a`.
- Voice/transcribe/tts + webrtc-token handlers (Wave 15) — new, or explicit deprecation of the
  legacy `orchestrators/{name}` voice routes.
- Admin agent ops: test/discover/security-scan, orch test-llm/voice/tts, system-agents,
  middleware-defs CRUD (Wave 14) — new handlers + DAL.

**Go packages that already exist and are sufficient:** `epconfig`, `temporal` (worker + signaler +
reconciler on `them-orchestration-go`), `execution`, `gate`, `ratelimit`, `session`, `runrecorder`,
`ws`, `sse`, `crypto`, `auth`, `agentregistry`.

**DB migrations that must run:**
- `application_definitions` table + `applications.active_definition_id` +
  `applications.source_definition_hash` + `runs.definition_id` (Wave 9), with the revision-1
  backfill for existing apps.
- Confirm/kill the dead `applications.orchestrator_id NOT NULL` FK and
  `app_orchestrators.orchestrator_id` before final removal (verify default/nullability on the live
  DB first — see prior review Risk 2). Drop the deprecated `app_orchestrators.edges` JSONB after
  the definition layer stops writing it.
- Fix Python `Application`/`AppOrchestrator` missing `tenant_id` mapping *or* ensure the definition
  layer's Go writers always stamp `tenant_id` (so no NULL-tenant rows are produced) before Python
  write paths are retired.

**Traefik rules that must change:**
- Add `them-go-apps-subroutes` (Wave 8).
- Add rules routing `POST /applications/import`, `POST /applications/{id}/publish|clone|rollback|
  activate|validate`, `PUT /applications/{id}/definition/draft|restore`, and `GET
  /applications/{id}/export` to Go (Wave 9).
- Route `/runs/*` audit + control sub-paths to Go (Waves 10–11).
- Route `/apps/*`, `/a2a`, `/.well-known/agent-card.json`, `/voice/*`, `/apps/{slug}/webrtc/token`
  to Go (Waves 12, 13, 15).
- Remove Python-target rules and the Python bridge service from compose only after every route
  above is Go-owned and validated (Wave 15).

**Behavioral prerequisites (non-route):**
- Go must own the projection write monopoly (`CompileDefinition`) so no Python `compile_graph`
  path remains that could write NULL-tenant or unstamped projection rows.
- The Temporal worker parity must be confirmed for delegation/child-workflow/HITL on the Go queue
  (the Go `OrchestrationWorkflow` exists; its sub-orchestrator child-workflow path must reach parity
  with the Python `_MAX_SUB_ORCH_DEPTH` delegation before the Python worker is retired).
- All `scripts/tests` suites plus `go test ./...` green; E2E (test 14) green; multi-turn + Temporal
  workflow tests green; routing contract tests pass (not skip) in a seeded environment.

Only when every package exists, every migration has run, every Traefik rule points at Go, and the
full test matrix is green can the Python `app/` bridge and its Traefik service be removed (Wave 15).

---

## 20. Summary of Decisions

| Question | Decision |
|---|---|
| Three-layer separation correct? | Yes — Definition / Compiled Projection / Per-Run Execution State. Confirmed. |
| Canonical source of truth | **Design:** JSON Application Definition. **Runtime:** relational projection. (Option C) |
| Definition stored as JSON, relational, or both? | Both. JSON authoritative for design; relational authoritative for runtime; projection stamped with definition hash. |
| Drift prevention | Compile-only write monopoly + hash stamping + run pinning to `definition_id`. |
| Save vs publish separate? | **Yes.** Draft save = cheap, no compile, no cache flush. Publish = validate+compile+stamp+flush. |
| Compile output | Relational projection rows + `active_definition_id` + `source_definition_hash`. |
| Edge meanings | EP→binding (entry), binding→binding (allowed delegation), binding→agent (tool availability), binding→mw→agent (interception chain). No static transition edge in v1. |
| Static vs dynamic | Dynamic default (planner decides). Bounds/policies in definition; guaranteed static flows deferred to optional `flows[]`. |
| Temporal interaction | Confirmed correct: workflow-per-context, Activity per leaf agent, Child Workflow per delegation, gather for parallel, signal for HITL, native cancel. |
| Agent call unit | Leaf agent → Activity; orchestrator delegation → Child Workflow. |
| Nested delegation | Child workflows, `depth+1`, capped (`_MAX_SUB_ORCH_DEPTH=3`, to become `policies.max_delegation_depth`). |
| Clarification | Orchestrator re-call = normal next turn; human clarification = `input-required` + `submit_human_response` signal. |
| Failure/retry/timeout/cancel | Per-activity RetryPolicy; native cancel with shielded finalize. |
| Audit reconstruction | `runs.parent_run_id` chain + `run_steps` + (new) `runs.definition_id`. |
| Data ownership | PG = config truth + audit; Temporal = execution truth; Redis = transient coordination. |
| ADK reference model | Catalog UUID + `InvokeAgentResult` contract; internals opaque. No schema change. |
| `app_orchestrators` | Keep table; reframe as compiled *binding* projection; conceptual rename only (no physical rename in Waves 8–11). |
| Versioning now? | **Yes** — revision + status + hash columns + `application_definitions` table now; history UI/rollback UX deferred. |
| Export/import/restore | **Replace** with Application Definition v1 (lossless); accept old form during one migration window; deprecate old export. |
| Wave 8 scope | **runtime + bulk-delete only.** Export moved to Wave 9 (definition-based). |
| Previous review's "relational = source of truth" | **Confirmed for runtime, overturned for design.** Definition is the design source of truth (Option C). |
```
