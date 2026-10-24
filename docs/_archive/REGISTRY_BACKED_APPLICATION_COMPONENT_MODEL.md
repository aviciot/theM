# Registry-Backed Application Component Model
# Scope: definition/instance split, common component contract, component-definition storage model, current-table mapping, Wave 9 impact
# Date: 2026-08-03
# Builds on: APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md (55ad66e) — Option C (hybrid) three-layer model
# Governs: Wave 9 (Application Definition Layer) and all later waves

---

## 0. How to read this document

This review extends the Option C decision from `APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md`
(commit `55ad66e`). It does **not** relitigate any of the eight decisions carried in from that
review — Option C, the three-layer model, save/publish split, the versioning columns, the confirmed
Temporal execution model, the Wave 8 scope, and the Wave 9 definition-layer scope are all fixed
inputs. What this review answers is the **next** structural question: *should every reusable object
on the Canvas have its own stored definition, and should the Application Definition JSON reference
those definitions rather than embed their implementation?*

The proposal under evaluation is a **registry-backed component model**: reusable objects
(orchestrators, agents, middleware, entry-point protocols, and future tools/ADK agents) each have a
**Component Definition** stored in the platform database, and an Application Definition contains
component **instances** that reference those definitions by portable identity + version, carrying
only per-instance config overrides and secret bindings — never the implementation.

The current Python codebase is treated as **evidence, not target**. Where the current tables already
embody a definition/instance split (`orchestrators` vs `app_orchestrators`, `middleware_defs` vs
`middleware_wirings`) that is confirmatory evidence; where they do not (agents are catalog-only;
entry points are instance-only), this review decides whether a split is warranted.

Every decision below is argued. Where the proposal as stated is wrong, over-built, or incomplete,
this document says so and proposes a better design. Two hard constraints from the platform's runtime
are load-bearing throughout and must never be violated:

- **`app_orchestrators.name` is immutable and is the key by which Temporal workers resolve an
  orchestrator at run start.** Any component model that changes this lookup breaks in-flight and
  future workflows. The name must remain stable across application edits.
- **Go `epconfig` reads `entry_points` by `slug`** at connection time (30s in-process cache,
  invalidated by `PUBLISH them:ep:config:changed`). `slug` is the stable external identifier of an
  entry point. Any component model must preserve this read path unchanged.

A third invariant governs the whole design: **secret values never appear in any definition JSONB,
in exported artifacts, in logs, or in Temporal event history.** Only secret *references* live in
definitions; Fernet ciphertext lives only in projection rows.

---

## 1. Executive Summary

**The registry-backed component model is ACCEPTED, with modifications.** It is not a replacement for
Option C — it is a refinement of what lives inside Layer 1 (the Application Definition) and adds a
new sibling concept *above* Layer 1: a **Component Registry** of reusable Component Definitions. The
final model is therefore a **four-concept** model that still resolves to the same three runtime
layers: Component Definitions (registry, reusable, versioned) → Application Definition (instances +
connections + config overrides + secret bindings, per revision) → Compiled Runtime Projection (the
existing relational rows) → Per-Run Execution State (Temporal). The registry sits beside Layer 1 and
is consumed by the publish/compile step; it does not add a runtime hot-path read, because the
compiled projection still holds fully-resolved config exactly as it does today.

**The core idea is correct and the platform is already half-way there.** The current schema already
proves the pattern in two places: `orchestrators` (a reusable named definition) → `app_orchestrators`
(a per-app instance), and `middleware_defs` (a reusable type definition) → `middleware_wirings` (a
per-app instance with `config_override`). The proposal generalizes this existing, working split to
*all* component kinds and formalizes the reference as a portable `{kind, namespace, name, version}`
tuple instead of a raw UUID FK. This is a good generalization: it fixes export/import portability
(portable identity survives environment boundaries where UUIDs do not), it gives the Canvas a single
uniform way to render property forms (every definition carries a `configuration_schema`), and it
makes adding a new component kind a data operation rather than a schema migration.

**Where the proposal is modified.** (1) Agents are already effectively definitions (the `agents`
catalog); they do **not** get a second "agent definition" table — the catalog *is* the definition
registry for the agent kind, extended with the common contract columns. (2) Entry points do **not**
need per-row definition tables — the protocol type string (`websocket|sse|webrtc|a2a|voice`) is an
*implicit* builtin definition; adding EP-definition rows would be pure ceremony with no reuse payoff
and would risk the `epconfig` slug read path. (3) Application orchestrators are **application-native
instances of a builtin `llm-orchestrator` definition**, not instances of the legacy global
`orchestrators` table — that table is deprecated, not promoted, because its `name`-unique-per-cluster
constraint is exactly the coupling this review must dissolve. (4) The storage model is **Option C
(base + subtype)**, not the single generic table the proposal implicitly suggests, because
type-safe columns and the existing indexed catalog matter more than schema minimalism.

**What changes in Wave 9.** Wave 8 is untouched (runtime + bulk-delete only). Wave 9 — the
Application Definition Layer — absorbs the registry: it now also creates a `component_definitions`
base table, migrates `agents`/`middleware_defs` under it as subtypes (or adds the shared contract
columns and a compatibility view), adds portable-reference resolution to `CompileDefinition`, and
makes the Application Definition JSON store `components[]` (instances with `definition_ref`) +
`connections[]` instead of the `bindings[]/agent_refs[]/delegation[]/tool_grants[]` blocks sketched
in the 55ad66e example. The three-layer runtime and the Temporal model are unchanged. The
name-coupling and slug-coupling constraints are honored by carrying the immutable `name` and `slug`
on the *instance*, not the definition, and pinning them into the projection exactly as today.

---

## 2. Compatibility with Option C

**The registry-backed model EXTENDS Option C; it does not replace it.** Every Option C decision
survives verbatim:

| Option C decision (55ad66e) | Status under registry model |
|---|---|
| JSON Application Definition is the canonical *design* source of truth | **Unchanged.** The definition still owns design intent; now its component blocks are instances-with-refs instead of embedded implementations. |
| Relational rows are the compiled runtime projection | **Unchanged.** Projection shape is identical; `CompileDefinition` now resolves refs before writing. |
| Three-layer model (Definition → Projection → Temporal) | **Preserved.** The registry is a *fourth* concept that sits beside Layer 1, feeding the compile step. It adds no runtime layer. |
| Save (draft, cheap) vs Publish (validate+compile+stamp+flush) | **Unchanged.** Publish gains ref-resolution + config-merge + secret-resolution steps (Section 16); draft save stays cheap. |
| `application_definitions` table + `active_definition_id` + `runs.definition_id` | **Unchanged.** Component definitions are versioned independently in the registry; the app revision *pins* the component versions it resolved (Section 15). |
| Temporal execution model (workflow-per-context, Activity/Child-Workflow, gather, signal, cancel) | **Unaffected.** The worker still reads the projection, never the registry. |
| Wave 8 = runtime + bulk-delete only | **Unchanged. This review does not touch Wave 8.** |
| Wave 9 = Application Definition Layer | **Extended** to include the component registry (Section 18). |

**Is the three-layer model preserved?** Yes. The registry is not a runtime layer — nothing reads it
on the hot path. It is consumed only by (a) the Canvas, to render palette items and property forms,
and (b) the publish/compile step, to resolve `definition_ref` → concrete config and validate
instance overrides. At run time the Temporal worker still reads a single fully-resolved projection
row set, exactly as in Option C. The registry therefore lives *architecturally above* Layer 1
(design-time authority) and is invisible to Layers 2 and 3.

**Is the Temporal execution model affected?** No. The worker receives the same compiled projection
(`app_orchestrators` by immutable `name`, `allowed_agent_ids[]` adjacency, `entry_points` by `slug`,
`middleware_wirings` in position order). It performs **zero registry lookups** during execution.
Because config is pre-resolved and pinned at publish, replay determinism is preserved and a
mid-flight edit to a component definition cannot alter a running workflow (Section 15, Section 17).

**Net effect on Option C:** the registry-backed model **improves** Option C on portability
(cross-environment import by portable identity), Canvas ergonomics (uniform schema-driven forms),
and extensibility (new kinds without schema change), at the cost of one new base table and a
resolution step in the compiler. It does **not** complicate the runtime, which is the axis Option C
cared most about. The improvement is worth the cost; see Section 6.

---

## 3. Definition vs Instance — The Two Concepts

The registry model rests on a precise, class-vs-object distinction. The clearest analogy:

> A **Component Definition** is a **class**. A **Component Instance** is an **object** — a specific
> `new` of that class inside one application, with its constructor arguments (config overrides) and
> injected dependencies (secret bindings) filled in.

### 3.1 Component Definition (the class)

A reusable, versioned, tenant- or platform-scoped description of *a kind of component that can be
placed on the Canvas*. It is authored once and referenced many times across many applications.

**A Component Definition CONTAINS:**
- Portable identity: `kind`, `namespace`, `name`, `version` (+ a UUID `id` as a cache/optimization).
- Human metadata: `display_name`, `description`.
- Implementation binding: `implementation_type` (how the runtime executes it — e.g. `a2a_async`,
  `llm_loop`, `pii_redact`, `websocket`) and whatever endpoint/handle the implementation needs.
- `configuration_schema`: a JSON Schema describing the *shape and constraints* of the config an
  instance may override.
- `default_config`: the baseline config values (the class's field defaults).
- `capabilities`: what this component can *produce/answer* (used for connection compatibility).
- `input_schema` / `output_schema`: the contract of a single invocation (used to validate
  connections between components).
- `credential_schema`: which secrets an instance must bind (names + whether required), never values.
- Lifecycle: `enabled`, timestamps.

**A Component Definition DOES NOT contain:**
- Any per-application placement (canvas position, connections to other components).
- Any per-application config *override* — only defaults.
- Any secret *value* — only the `credential_schema` naming what secrets are needed.
- Any immutable per-app runtime identifier (`app_orchestrators.name`, `entry_points.slug`) — those
  are instance properties, because two instances of the same definition in the same app must have
  distinct names/slugs.
- Any tenant application's runtime limits (session caps, rate limits) — those are app/EP-instance
  concerns.

### 3.2 Component Instance (the object)

A specific placement of a definition inside one Application Definition. Many instances may reference
the same definition, each with different config and secrets.

**A Component Instance CONTAINS:**
- `instance_id`: canvas-stable identity, unique within the application (this is the successor of
  today's `node_id`).
- `definition_ref`: the portable reference `{kind, namespace, name, version}` (+ optional resolved
  `definition_id` UUID as a cache).
- `config`: per-instance overrides layered on top of `default_config` (validated against
  `configuration_schema`).
- `secret_bindings`: a map from `credential_schema` slot → secret *reference* (e.g.
  `secret://tenant/main-llm`). Never a value.
- Instance-native immutable identifiers where the kind requires them: `name` for orchestrator
  instances (Temporal lookup key), `slug` for entry-point instances (epconfig lookup key).
- Canvas placement metadata (position), stored in the `canvas` block, keyed by `instance_id`.

**A Component Instance DOES NOT contain:**
- The definition's implementation (endpoint, prompt template shape, `configuration_schema`,
  `default_config`) — those are resolved from the definition at publish time.
- Any secret value.
- Any config field the `configuration_schema` does not permit.

### 3.3 The relationship

```
Component Definition  (registry, versioned, reusable)
        ▲  referenced by definition_ref {kind, namespace, name, version}
        │
Component Instance    (inside one Application Definition revision)
        │  config (overrides) + secret_bindings (references) + instance_id + name/slug
        ▼  resolved + merged + pinned at PUBLISH
Compiled Projection Row  (app_orchestrators / entry_points / middleware_wirings)
        │  fully-resolved config, Fernet ciphertext, immutable name/slug, definition version pinned
        ▼  read at run start (one SELECT, no registry lookup)
Temporal Worker
```

`resolved_config(instance) = definition.default_config ⊕ tenant/env defaults ⊕ instance.config`,
computed once at publish and frozen into the projection (Section 12). The instance never sees the
definition's internals at runtime; the projection is self-contained.

---

## 4. Which Existing Types Need Definitions

For each current type: is it *already* a definition, *already* an instance, or *neither* — and what
does the registry model do with it.

| Type | Today it is… | …a definition of / instance of what | Registry-model verdict |
|---|---|---|---|
| **`agents`** | **Already a definition** (catalog). Each row is a reusable, named, endpoint-bearing description of an external A2A agent, referenced by many apps via `allowed_agent_ids[]`. | Defines an *agent component* (transport, endpoint, input_schema, skills, timeouts). | **Is a Component Definition of kind `agent`.** Add the common-contract columns (namespace, version, capabilities, credential_schema). It becomes an `agent`-subtype under `component_definitions`. No second table. |
| **`orchestrators`** | **Nominally a definition** (global named, reusable, `a2a_exposed` promotes it to an agent). In practice it is a *legacy* half-definition with a cluster-wide `name`-unique constraint. The Temporal loader still falls back to it by `name` (see below). | Defines a global orchestrator template. | **Deprecated, not promoted.** The builtin `llm-orchestrator` Component Definition replaces it as the "class" for all standard orchestrators. Its `name`-unique-per-cluster constraint is the exact coupling to dissolve. Retire after Wave 9 backfill (Section 8, Section 11). |
| **`middleware_defs`** | **Already a definition** (global type: slug, kind, base `config`, `is_builtin`). | Defines a *middleware component type*. | **Is a Component Definition of kind `middleware`.** Add common-contract columns; becomes the `middleware` subtype. Its existing `config` is the `default_config`. |
| **`entry_points`** | **Already an instance** (per-app: slug, type, access policy, limits, queue, binding FK). No EP-definition table exists. | Instance of an *implicit* protocol definition selected by the `entry_point_type` string. | **Instance stays. Definition is implicit** — the protocol type string *is* the definition (Section 10). No EP-definition rows. Projection unchanged (preserves `slug` read path). |
| **`app_orchestrators`** | **Already an instance** (per-app binding: immutable name, resolved config, Fernet keys, adjacency). | Instance of… nothing reliable today — `orchestrator_id` FK is nullable and usually NULL. | **Instance of the builtin `llm-orchestrator` Component Definition.** Remains the compiled projection row for orchestrator instances. Immutable `name` preserved (Section 11). |
| **`middleware_wirings`** | **Already an instance** (per-app: def_id FK, position, config_override, node_id). | Instance of a `middleware_defs` row. | **Instance of a `middleware` Component Definition.** Remains the compiled projection row for middleware instances. `def_id` now resolves via portable ref at publish. |
| **`applications`** | **Neither** — it is the tenant container. | The parent of a definition tree. | **Stays the container.** Gains no component role; holds `active_definition_id`, `runtime_config`, presentation. |

**Reading:** the platform already has definitions for agents and middleware, and instances for
entry points, app-orchestrators, and middleware-wirings. The registry model's real work is (a) give
agents and middleware the shared contract + portable identity, (b) provide the builtin
`llm-orchestrator` definition so app-orchestrator instances finally have a real "class", (c) leave
entry points as instances of an implicit protocol definition, and (d) deprecate the legacy global
`orchestrators` table whose naming constraint is incompatible with the target model.

---

## 5. Common Component Definition Contract

The proposal's suggested shared contract, evaluated field by field. **Verdict: accept the core,
modify three fields, add four.** This is the schema every definition kind shares.

| Field | Proposed | Verdict | Rationale |
|---|---|---|---|
| `id` | UUID | **Accept** | Primary key + fast-path resolution cache. Not the portable identity (Section 14). |
| `kind` | enum | **Accept** | Discriminator: `agent \| orchestrator \| middleware \| tool \| entry_point(builtin only)`. Drives subtype table + Canvas palette grouping. |
| `namespace` | string | **Accept** | Portability + collision avoidance. Conventions: `them.builtin`, `them.tenant.<tenant>`, `company.<product>` (Section 14). |
| `name` | string | **Accept** | Portable name within `(kind, namespace)`. Distinct from instance `name`/`slug`. |
| `version` | int | **Accept, define as integer revision** | Monotonic integer per `(kind, namespace, name)`. Not semver (Section 14/15). Unique key: `(kind, namespace, name, version)`. |
| `display_name` | string | **Accept** | Canvas label. |
| `implementation_type` | string | **Accept, rename semantics** | The runtime dispatcher key (e.g. `a2a_async`, `llm_loop`, `pii_redact`, `websocket`). This is *how the runtime executes it*, decoupled from `kind` (a `tool` kind could have `implementation_type: http` or `mcp`). |
| `configuration_schema` | JSON Schema | **Accept** | Drives Canvas forms + API validation + publish validation (Section 11). |
| `default_config` | JSON | **Accept** | Layer-1 of config resolution (Section 12). Must itself validate against `configuration_schema`. |
| `capabilities` | list | **Accept, modify to typed tags** | Structured capability tags (e.g. `["delegation.target","tool.callable"]`) used for connection compatibility (Section 16 step 5). Free-form list is too weak; use a controlled vocabulary. |
| `input_schema` | JSON Schema | **Accept** | Invocation input contract; validates connection source→target compatibility. |
| `output_schema` | JSON Schema | **Accept** | Invocation output contract; validates downstream consumers. |
| `credential_schema` | JSON | **Accept** | Names required/optional secret slots; values never here (Section 13). Each slot: `{name, required, description}`. |
| `enabled` | bool | **Accept** | Soft-disable a definition from the palette without deleting revisions. |

**Fields to ADD (missing from the proposal):**

| Added field | Why it is required |
|---|---|
| `scope` (`builtin \| tenant`) | Distinguishes platform-owned definitions (`them.builtin.*`, all-tenant) from tenant-authored ones. Governs edit permissions and namespace. |
| `tenant_id` (nullable) | NULL for builtin/global; set for tenant-scoped definitions. Required for tenant isolation (CLAUDE.md tenant rule). |
| `status` (`draft \| published \| deprecated`) | A definition version has its own lifecycle. Apps may only pin `published` versions; `deprecated` blocks *new* pins but keeps existing ones valid. |
| `content_hash` (sha256) | Reproducibility + drift detection, mirroring `application_definitions.definition_hash`. Lets a pinned app prove the exact definition bytes it compiled against. |

**Fields deliberately NOT in the shared contract** (they belong on the *instance*, not the
definition): `instance_id`, per-app `config` overrides, `secret_bindings` (values or bindings),
canvas position, the immutable `name`/`slug` that a runtime uses as a lookup key. Putting any of
these on the definition would break reuse (two instances of one definition must differ).

The full shared contract is therefore: `id, kind, namespace, name, version, display_name,
description, implementation_type, configuration_schema, default_config, capabilities, input_schema,
output_schema, credential_schema, scope, tenant_id, status, content_hash, enabled, created_at,
published_at`.

---

## 6. Option A / B / C / D Evaluation — Component Definition Storage

Options for *where the Component Definitions physically live*:

- **Option A — single generic `component_definitions` table** with a `kind` discriminator and
  JSONB `configuration_schema`/`default_config`/`capabilities`/`credential_schema` columns. One row
  per definition-version, all kinds together.
- **Option B — type-specific tables** with no shared base: keep `agents`, `middleware_defs`, add
  `orchestrator_defs`, `tool_defs`, etc. Each fully self-describing, no common table.
- **Option C — shared base table + kind-specific subtype tables.** `component_definitions` holds the
  common contract (Section 5); a kind that needs type-safe columns gets a subtype table
  (`agent_defs`, `middleware_def_details`, …) joined 1:1 by `id`. Kinds with no extra typed columns
  need no subtype.
- **Option D — hybrid registry view over the *existing* catalog tables.** Keep `agents` and
  `middleware_defs` physically as-is (add the shared contract columns to each), and expose a
  read-model `component_definitions` **view** that `UNION ALL`s them plus builtin orchestrator/EP
  rows. No physical base table; the "registry" is a view + the existing tables.

| Criterion | A — Single generic | B — Type-specific | C — Base + subtype | D — View over existing |
|---|---|---|---|---|
| **Conceptual simplicity** | Best (one table) | Poor (N unrelated tables, no unifying concept) | Good (one base + few subtypes; the concept is explicit) | Good conceptually, but the "registry" is virtual — two mental models (tables vs view) |
| **DB type safety** | Poor (everything JSONB; endpoint_url, timeout, transport all untyped) | Best (native typed columns per kind) | Best where it matters (typed columns in subtypes) | Best (existing typed columns retained) |
| **Ease of adding new kinds** | Best (insert rows, no DDL) | Worst (new table + new DAL + new handlers) | Good (base row always; subtype table only if typed columns needed — often none) | Poor (new physical table + extend the view + extend UNION) |
| **Validation** | Uniform via `configuration_schema`, but no DB-level constraints | DB constraints per kind; no uniform path | Uniform `configuration_schema` on base **and** DB constraints in subtypes | Uniform via view; constraints per existing table |
| **Canvas form generation** | Uniform (one query, one schema shape) | Non-uniform (different columns per kind) | Uniform (query the base for schema; subtype fetched lazily) | Uniform via view |
| **Querying / administration** | Simple single-table queries; but agent-specific admin (list by endpoint) needs JSONB probing | Native per-kind queries; cross-kind listing needs UNION | Base for cross-kind lists; subtype join for kind-specific admin | Native on existing tables; cross-kind via view |
| **Runtime performance** | Irrelevant — registry not on hot path. Publish reads it. | Same | Same | Same (view only materialized at design/publish time) |
| **Export/import portability** | Good (portable identity columns present) | Good (if each table adds portable identity) | Good (portable identity on base) | Good (portable identity on existing tables) |
| **Implementation complexity** | Low DDL; high JSONB-validation glue; loses `agents` typed columns | High (N tables, N DALs) | Moderate (one base DAL + thin subtype DALs; reuses `agents`/`middleware_defs`) | Low DDL but view maintenance + dual write-path complexity |
| **Migration from current tables** | Hard — must *move* `agents` (many FKs: `allowed_agent_ids`, `middleware_wirings.agent_id`) into a generic table, or dual-write | Medium — leave `agents` alone, add new tables | **Easiest that keeps type safety** — `agents`/`middleware_defs` *become* subtypes; add base rows + FK; existing FKs to `agents.id` keep working if `agents.id` == base `id` | None to existing tables, but the view is a new surface to keep correct |
| **ADK compatibility** | Good (new kind = rows) | Poor (new kind = new table) | Good (new kind = base rows; subtype only if typed columns) | Poor (new kind = new physical table + view edit) |

**Key migration fact that decides it:** `agents.id` is referenced by `app_orchestrators.allowed_agent_ids UUID[]`,
`middleware_wirings.agent_id`, and Temporal loaders. Any storage model that *relocates* agents into a
new generic table (Option A) forces rewriting those references — high blast radius for zero runtime
gain. Options C and D both keep `agents` physically intact.

---

## 7. Recommended Storage Model

**Decision: Option C — shared `component_definitions` base table + kind-specific subtype tables —
with `agents` and `middleware_defs` adopted as the `agent` and `middleware` subtypes so that a
subtype row's `id` equals the existing catalog row's `id`.** Option D (view-only) is the fallback if
Wave 9 must ship without any DDL on `agents`; it is rejected as the primary because a virtual
registry means two mental models and a fragile UNION that every new kind must edit.

Option C is chosen because it is the only option that simultaneously (a) keeps the existing typed,
indexed `agents` catalog and all FKs to `agents.id` working unchanged, (b) gives every kind one
uniform `configuration_schema`-driven contract for the Canvas and validation, and (c) makes adding a
new kind (tool, ADK) a base-row insert with a subtype table only when typed columns are actually
needed. It pays a small cost — a 1:1 join for kind-specific admin queries — that is irrelevant
because the registry is never on the runtime hot path.

### 7.1 Base table

```sql
-- Wave 9. Shared contract for every reusable Canvas component kind.
CREATE TABLE them.component_definitions (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind                 TEXT NOT NULL CHECK (kind IN ('agent','orchestrator','middleware','tool','entry_point')),
  namespace            TEXT NOT NULL,                    -- them.builtin | them.tenant.<id> | company.<product>
  name                 TEXT NOT NULL,
  version              INTEGER NOT NULL,                 -- monotonic per (kind, namespace, name)
  display_name         TEXT NOT NULL,
  description          TEXT,
  implementation_type  TEXT NOT NULL,                    -- runtime dispatcher key
  configuration_schema JSONB NOT NULL DEFAULT '{}'::jsonb,
  default_config       JSONB NOT NULL DEFAULT '{}'::jsonb,
  capabilities         JSONB NOT NULL DEFAULT '[]'::jsonb,
  input_schema         JSONB,
  output_schema        JSONB,
  credential_schema    JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{name, required, description}] — NAMES ONLY
  scope                TEXT NOT NULL CHECK (scope IN ('builtin','tenant')),
  tenant_id            UUID,                             -- NULL for builtin
  status               TEXT NOT NULL CHECK (status IN ('draft','published','deprecated')),
  content_hash         TEXT NOT NULL,
  enabled              BOOLEAN NOT NULL DEFAULT true,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at         TIMESTAMPTZ,
  UNIQUE (kind, namespace, name, version)
);
```

### 7.2 Which kinds get a subtype table

| Kind | Subtype table? | Rationale |
|---|---|---|
| `agent` | **Yes — the existing `them.agents` table becomes the subtype**, with `agents.id` = `component_definitions.id` (FK). Keeps `transport`, `endpoint_url`, `auth_token_encrypted`, `input_schema`, `skills`, `agent_card`, `supports_streaming` as typed columns. All existing FKs to `agents.id` keep working. | Preserves the indexed catalog and every reference; zero relocation. |
| `middleware` | **Yes — `them.middleware_defs` becomes the subtype**, `middleware_defs.id` = base `id`. Keeps `kind`, `is_builtin`, and its base `config` (mapped to `default_config`). Today `middleware_defs` is unversioned and identified by a globally-unique `slug`; the backfill maps `slug`→`name`, assigns `version=1`, and a `namespace` (`them.builtin` where `is_builtin`, else `them.tenant.<id>`). | Preserves existing middleware wiring FKs (`middleware_wirings.def_id`). |
| `orchestrator` | **No subtype needed initially.** The single builtin `llm-orchestrator` definition's config is fully expressed in `configuration_schema`/`default_config` (LLM provider/model, prompts, limits, voice/TTS/memory). | No typed columns unique to orchestrator definitions; instance config lands in `app_orchestrators` (the projection). |
| `entry_point` | **No table rows at all** — builtin/implicit only (Section 10). The protocol type string is the definition. | Avoids risking the `epconfig` slug path; no reuse payoff. |
| `tool` (future) | **Base row; subtype only if a tool kind needs typed columns** (e.g. MCP endpoint). | ADK/tool extensibility without DDL churn. |

**Adoption mechanic (agents/middleware).** Rather than moving data, add the base row and make the
existing table a subtype: `ALTER TABLE them.agents ADD COLUMN <shared-contract columns not already
present>`; ensure `agents.id` participates as `component_definitions(id)` via a backfilled base row
per agent (namespace `them.tenant.<id>` or `them.builtin`, version 1, `content_hash` computed). The
same for `middleware_defs`. Details and sequencing in Section 8 and Section 18. If the ALTER/backfill
proves too risky for Wave 9's window, fall back to Option D (view) for one wave and physicalize later
— but the target is Option C.

---

## 8. Current Table Mapping

| Table | Current role | New role | Action | Wave |
|---|---|---|---|---|
| `them.agents` | Catalog of external A2A agent endpoints | **Component Definition, kind `agent`** (subtype of `component_definitions`) | **Keep + extend.** Add shared-contract columns; backfill a base row per agent (`agents.id` = base `id`). No relocation; all FKs to `agents.id` unchanged. | Wave 9 |
| `them.orchestrators` | Global named orchestrators (`name` unique per cluster; `a2a_exposed` promotes to agent) | **Deprecated.** Replaced as the "class" by the builtin `llm-orchestrator` Component Definition. | **Deprecate.** Stop writing after Wave 9 backfill; keep read-only during the migration window for any `a2a_exposed` orchestrators-as-agents; retire in a cleanup wave. The `name`-unique-per-cluster constraint (the coupling this review dissolves) dies with it. | Wave 9 (deprecate), later (drop) |
| `them.app_orchestrators` | Per-app binding instances (immutable `name`, resolved config, Fernet keys, adjacency) | **Compiled projection of an orchestrator Component Instance** (instance of builtin `llm-orchestrator`) | **Keep as projection.** Unchanged shape. `name` stays immutable (Temporal key). Add `source_definition_id`/`source_definition_hash` stamps + resolved `component_definition_id`/`component_version` pin. `orchestrator_id` FK stays dead; `edges` JSONB deprecated. | Wave 9 |
| `them.applications` | Tenant application container | **Container** (unchanged) | **Keep + extend** (already extended in Option C: `active_definition_id`, `source_definition_hash`, `runtime_config`). No component role. | Wave 9 (Option C cols) |
| `them.entry_points` | Per-app protocol doors (slug, type, access, limits, queue, binding FK) | **Compiled projection of an entry-point Component Instance** whose definition is the *implicit* protocol type (Section 10) | **Keep as projection.** Unchanged shape; `slug` read path preserved. No EP-definition table. | Wave 9 |
| `them.middleware_defs` | Global middleware type defs (slug, kind, base config, is_builtin) | **Component Definition, kind `middleware`** (subtype) | **Absorb into `component_definitions`** as the `middleware` subtype. Add shared-contract columns; base `config` → `default_config`; backfill base rows. | Wave 9 |
| `them.middleware_wirings` | Per-app middleware instances (def_id FK, position, config_override, node_id) | **Compiled projection of a middleware Component Instance** | **Keep as projection.** `def_id` now resolves via portable ref at publish; store resolved `component_definition_id`/`version` pin. Shape otherwise unchanged. | Wave 9 |

**Survival summary.** All projection tables (`app_orchestrators`, `entry_points`,
`middleware_wirings`) **survive unchanged in shape** (plus stamp/pin columns) — this is what
preserves the Temporal name lookup and the epconfig slug lookup. The two existing definition tables
(`agents`, `middleware_defs`) **survive and are extended** into subtypes. Only `orchestrators` (the
legacy global table) is **deprecated**, because its cluster-wide `name` uniqueness is the coupling
the target model must remove; the builtin `llm-orchestrator` definition takes its place as the class.
`applications` is a container and gains only the Option C revision columns. No projection table is
retired in Wave 9; `orchestrators` and the dead `edges`/`orchestrator_id` columns are dropped in a
later cleanup wave after the definition layer stops writing them.

---

## 9. Application Definition JSON — Revised Format

Replaces the `bindings[]/agent_refs[]/delegation[]/tool_grants[]` blocks from the 55ad66e example
with `components[]` (instances with `definition_ref`) + `connections[]`. Comments (`//`) are
explanatory and are **not** valid JSON — strip before storing. This example has: two entry points
(ws + sse), two orchestrator instances (root + sub with different LLM config), three agent instances,
two middleware **definitions** used as **three** instances (PII middleware twice with different
config, audit middleware once), all connections, canvas positions, app runtime settings, and secret
**references** only.

```jsonc
{
  "schema_version": 2,                             // v2: registry-backed components[] + connections[]
  "definition_id": "e2b1...-uuid",
  "application_id": "a17c...-uuid",
  "revision": 5,
  "status": "published",
  "definition_hash": "sha256:...",

  "name": "Support Concierge",
  "presentation": { "color": "#4f46e5", "description": "Front-door support router" },

  // ── Component instances. Each references a Component Definition by portable identity. ──
  "components": [
    // ---- Orchestrator instances (instances of the builtin llm-orchestrator definition) ----
    {
      "instance_id": "orch_router",
      "name": "support_router",                    // IMMUTABLE runtime name — Temporal reads this
      "definition_ref": { "kind": "orchestrator", "namespace": "them.builtin",
                          "name": "llm-orchestrator", "version": 1 },
      "config": {
        "role": "root",
        "system_prompt": "You route the user to the right specialist.",
        "llm": { "provider": "anthropic", "model": "claude-sonnet" },
        "max_iterations": 10, "max_parallel_tools": 4, "history_window": 20
      },
      "secret_bindings": { "llm_api_key": "secret://tenant/router-llm" }
    },
    {
      "instance_id": "orch_payments",
      "name": "payments_orch",                     // IMMUTABLE runtime name
      "definition_ref": { "kind": "orchestrator", "namespace": "them.builtin",
                          "name": "llm-orchestrator", "version": 1 },
      "config": {
        "role": "sub",
        "system_prompt": "You handle payment and ledger questions.",
        "llm": { "provider": "anthropic", "model": "claude-opus" },   // different model from root
        "max_iterations": 8, "max_parallel_tools": 2, "history_window": 12, "budget_tokens": 50000
      },
      "secret_bindings": { "llm_api_key": "secret://tenant/payments-llm" }
    },

    // ---- Agent instances (instances of catalog agent Component Definitions) ----
    {
      "instance_id": "agent_fraud",
      "definition_ref": { "kind": "agent", "namespace": "company.risk",
                          "name": "fraud-agent", "version": 2 },
      "config": { "timeout_seconds": 30 }
    },
    {
      "instance_id": "agent_ledger",
      "definition_ref": { "kind": "agent", "namespace": "company.payments",
                          "name": "ledger-agent", "version": 3 },
      "config": { "timeout_seconds": 45 },
      "secret_bindings": { "api_token": "secret://tenant/ledger-token" }
    },
    {
      "instance_id": "agent_kb",
      "definition_ref": { "kind": "agent", "namespace": "them.builtin",
                          "name": "knowledge-base", "version": 1 },
      "config": {}
    },

    // ---- Middleware instances: PII definition used TWICE, Audit definition used ONCE ----
    {
      "instance_id": "mw_pii_kb",                  // PII middleware instance #1 (in front of KB)
      "definition_ref": { "kind": "middleware", "namespace": "them.builtin",
                          "name": "pii-redact", "version": 1 },
      "config": { "redact": ["ssn", "card_number"] }
    },
    {
      "instance_id": "mw_pii_ledger",              // PII middleware instance #2 (in front of Ledger)
      "definition_ref": { "kind": "middleware", "namespace": "them.builtin",
                          "name": "pii-redact", "version": 1 },
      "config": { "redact": ["ssn"] }              // different override from instance #1
    },
    {
      "instance_id": "mw_audit_ledger",            // Audit middleware instance (in front of Ledger)
      "definition_ref": { "kind": "middleware", "namespace": "them.builtin",
                          "name": "audit-log", "version": 2 },
      "config": { "level": "full" }
    }
  ],

  // ── Entry point instances (instances of implicit protocol definitions — Section 10) ──
  "entry_points": [
    {
      "instance_id": "ep_ws",
      "slug": "support-ws",                        // IMMUTABLE external id — epconfig reads this
      "protocol": "websocket",                     // the implicit definition_ref
      "root": "orch_router",                       // EP→binding connection (see connections[])
      "access_policy": { "mode": "token" },
      "limits": { "conversation_token_limit": null, "max_concurrent_sessions": 50,
                  "queue_timeout_seconds": 30, "queue_message": "Holding your place…" }
    },
    {
      "instance_id": "ep_sse",
      "slug": "support-sse",                       // IMMUTABLE external id
      "protocol": "sse",
      "root": "orch_router",
      "access_policy": { "mode": "token" },
      "limits": { "conversation_token_limit": 200000, "max_concurrent_sessions": null,
                  "queue_timeout_seconds": null, "queue_message": null }
    }
  ],

  // ── Connections. Typed edges between instances. ──
  "connections": [
    { "source": "ep_ws",       "target": "orch_router",   "type": "entry" },
    { "source": "ep_sse",      "target": "orch_router",   "type": "entry" },
    { "source": "orch_router", "target": "orch_payments", "type": "delegation" },  // sets delegatable
    { "source": "orch_router", "target": "agent_fraud",   "type": "tool" },        // direct tool grant
    { "source": "orch_router", "target": "agent_kb",      "type": "tool",
      "via": ["mw_pii_kb"] },                                                       // through PII mw
    { "source": "orch_payments","target": "agent_ledger", "type": "tool",
      "via": ["mw_pii_ledger", "mw_audit_ledger"] }                                // chained: PII→Audit→Ledger
  ],

  // ── Application-level runtime settings (compiled into applications.runtime_config) ──
  "runtime_config": {
    "max_concurrent_sessions": 200, "rate_limit_rpm": 600,
    "blocked_tokens": [], "blocked_user_ids": []
  },

  // ── Execution policy (engine reads these instead of hardcoded constants) ──
  "policies": {
    "max_delegation_depth": 3, "max_agent_calls": 200,
    "timeout_policy": { "default_agent_seconds": 30, "run_seconds": 900 },
    "parallelism_policy": { "default_max_parallel": 4, "join": "all" },
    "memory_policy": { "summarize_after_messages": 200 }
  },

  // ── Canvas layout — design-only, never compiled. Keyed by instance_id. ──
  "canvas": {
    "viewport": { "x": 0, "y": 0, "zoom": 1 },
    "layout": {
      "ep_ws":         { "x":  20, "y":  40 }, "ep_sse":        { "x":  20, "y": 200 },
      "orch_router":   { "x": 260, "y": 120 }, "orch_payments": { "x": 520, "y": 260 },
      "agent_fraud":   { "x": 520, "y":  40 }, "agent_kb":      { "x": 520, "y": 140 },
      "agent_ledger":  { "x": 780, "y": 300 },
      "mw_pii_kb":     { "x": 400, "y": 140 }, "mw_pii_ledger": { "x": 640, "y": 280 },
      "mw_audit_ledger":{ "x": 710, "y": 280 }
    }
  }
}
```

Notes:
- **PII middleware appears as two distinct instances** (`mw_pii_kb`, `mw_pii_ledger`) of one
  definition (`them.builtin/pii-redact@1`) with **different `config`** — this is the multi-instance
  requirement (Section 9 answers Q9).
- **The Ledger chain** (`mw_pii_ledger` → `mw_audit_ledger` → `agent_ledger`) shows two middleware
  instances of two *different* definitions chained on one tool grant via `via[]`.
- **Immutable identifiers live on the instance**: orchestrator `name`, entry point `slug`. Two
  orchestrator instances of the same definition therefore have distinct Temporal names.
- **Secrets are references only** (`secret://tenant/...`), never values (Section 13).
- **`connections[].type`** replaces the four separate edge blocks with one typed-edge list;
  `entry|delegation|tool` cover today's semantics; `via[]` preserves middleware chains losslessly.

---

## 10. Entry Point Definition/Instance Split

**Decision: entry points are INSTANCES of an IMPLICIT, builtin protocol definition. THEM does NOT
need entry-point definition rows in the database.** The `entry_point_type` string
(`websocket|sse|webrtc|a2a|voice`) *is* the definition identity.

**What an EP "definition" would describe** (conceptually, as builtin metadata — not a DB row):
- Protocol type (`websocket`, `sse`, `webrtc`, `a2a`, `voice`).
- Supported auth modes (token, none, mTLS) — a `configuration_schema` for `access_policy`.
- Config schema for limits (token limit, max concurrent sessions, queue timeout/message).
- Capabilities (streaming, bidirectional, binary) — used to validate that a protocol supports what
  the app asks of it.

**What an EP instance inside an app describes** (the actual `entry_points` projection row):
- `slug` (immutable external identifier — epconfig lookup key).
- `protocol` (the implicit definition_ref).
- `access_policy` (the resolved config for the chosen auth mode).
- Per-EP limits: `conversation_token_limit`, `max_concurrent_sessions`, `queue_timeout_seconds`,
  `queue_message`.
- The `root` connection: which orchestrator instance answers this door
  (`entry_points.app_orchestrator_id`).

**Why implicit, not a DB table.** Three reasons:
1. **No reuse payoff.** Unlike agents and middleware (authored once, referenced by many apps), the
   protocol set is a small, fixed, platform-owned enum. There is no user-authored "entry point type"
   to store and version. A table would hold five near-static rows and buy nothing.
2. **Slug read path risk.** `epconfig` reads `entry_points` by `slug` with a 30s in-process cache
   (Go, `sync.Mutex map[slug]`), invalidated by `PUBLISH them:ep:config:changed`. Introducing an
   EP-definition FK into that hot query, or moving protocol config into a joined definition table,
   would complicate the single stable read the runtime depends on. Keeping the protocol as a string
   column on the instance keeps that read exactly as it is today.
3. **The config schema is small and builtin.** The `configuration_schema` for each protocol
   (which auth modes, which limits apply) is platform code, best shipped as a builtin registry entry
   (a `component_definitions` row with `kind='entry_point'`, `scope='builtin'`, one per protocol) if
   we want the Canvas to render EP forms uniformly — but the app-side stays an instance keyed by
   `slug`. The five builtin `entry_point` rows are *optional* palette metadata, not a runtime
   dependency; the projection never joins to them.

**Net:** EP instance stays exactly as `entry_points` is today (preserving the slug read path);
the "definition" is the protocol string, optionally backed by five builtin `component_definitions`
rows purely so the Canvas can render property forms the same way it renders every other component.

---

## 11. Orchestrator Definition/Instance Split

Three sub-options, per the prompt:
- **(a)** Every app orchestrator is a standalone instance with embedded config (no reusable
  definition) — today's `app_orchestrators` with no real class.
- **(b)** App orchestrators optionally reference a reusable orchestrator *template* definition that
  another app authored.
- **(c)** A single builtin `llm-orchestrator` Component Definition serves as the implicit class for
  all standard orchestrators; each app orchestrator is an instance of it.

**Decision: (c) as the baseline, with (b) available later as tenant-authored templates — and
explicitly NOT the legacy `orchestrators` table for either.**

**Why (c).** Every standard orchestrator instance runs the same engine (`llm_loop`
`implementation_type`): plan→act→observe with an LLM, bounded by `max_iterations`/`max_parallel_tools`,
optionally voice/TTS/memory. The *shape* of its config (provider, model, prompt, limits, voice) is
identical across instances; only the *values* differ. That is exactly a class (the
`llm-orchestrator` definition holds the `configuration_schema` + `default_config`) with many objects
(the `app_orchestrators` projection rows hold the per-instance values). This gives the Canvas a
uniform property form for orchestrators, gives publish a schema to validate orchestrator config
against, and gives ADK/other engines a clean extension point (`kind='orchestrator'`,
`implementation_type='adk'` is a *different definition*, not a special case).

**Why not (a).** "Standalone instance, no definition" is the status quo and is exactly what makes the
Canvas orchestrator form bespoke code and makes validation ad hoc. It also leaves no place to hang a
future non-LLM orchestrator engine.

**Why not the legacy `orchestrators` table.** That table is cluster-global with a `name`-unique
constraint and is read by the deprecated `GET(WS) /ws/orchestrate/{name}` path and by `a2a_exposed`
promotion. Promoting it to "the orchestrator definition" would carry its cluster-wide naming
coupling into the new model — the very coupling this review must dissolve. Instead, the builtin
`llm-orchestrator` definition is a single platform-owned row in `component_definitions`; app
orchestrators are its instances; the legacy table is deprecated (Section 8).

**Why (b) later.** Some tenants will want to author a reusable orchestrator template (a canned
"triage bot" with a fixed prompt + tool set) and drop it into many apps. That is a tenant-scoped
Component Definition of `kind='orchestrator'` (namespace `them.tenant.<id>`, `implementation_type`
still `llm_loop`) whose `default_config` carries the canned prompt/limits; an app instance overrides
only what it must. This needs no new mechanism — it is just another registry row — so (b) is
"available for free" once (c)'s registry exists. It is deferred as a feature (no builtin templates
ship in Wave 9) but not blocked.

**The name-coupling resolution (critical).** The immutable Temporal lookup key stays on the
**instance**, never the definition:
- `app_orchestrators.name` remains immutable and remains the Temporal lookup key — unchanged. It is
  allocated once at compile (`_generate_orch_name`) and never rewritten; `compile_graph` keys the
  upsert by `node_id` (the canvas identity, successor: `instance_id`), so editing the canvas never
  churns the runtime `name`. This behavior is preserved exactly.
- The Temporal loader (`load_orchestrator_row(name, db)`) resolves **`app_orchestrators` by `name`
  first**, then **falls back to the global `orchestrators` table by `name`** (for seeded/playground
  templates). The registry model keeps the primary path intact; the fallback to `orchestrators`
  is deprecated alongside that table (Section 8) — once the builtin `llm-orchestrator` definition
  and its instances cover the seeded templates, the fallback is removed, not repointed. Until then
  the fallback stays, so no seeded orchestrator breaks mid-migration.
- The definition (`llm-orchestrator`) is referenced by portable identity and has its own `name`
  (`"llm-orchestrator"`) which is **the definition's name, not the runtime lookup name** — the two
  namespaces are distinct.
- `runs.orchestrator_name` is a denormalized snapshot written alongside `orchestrator_id` at run
  start; it continues to record the immutable instance `name`, so the audit trail is unaffected.
- The fragile `agent__orch__<name>` double-prefix coupling (loader `removeprefix` chain) is replaced
  by carrying an explicit `ref_kind`/`transport` field from the projection into the loader, resolved
  at compile from the instance's `definition_ref.kind`. This removes the string-parsing coupling
  without changing the immutable `name` the worker keys on.

---

## 12. Configuration Resolution

The precise precedence chain, from lowest to highest priority (later layers win on a per-key basis;
merge is a deep merge over JSON objects, replace over scalars/arrays unless a schema marks a field
`mergeStrategy: append`):

| Layer | Source | Who applies it | When | Where stored | Go type |
|---|---|---|---|---|---|
| **1 — definition defaults** | `component_definitions.default_config` | `CompileDefinition` reads it | Publish | Not stored alone; input to merge | `map[string]any` (decoded from JSONB) |
| **2 — tenant/environment defaults** | (optional) tenant-scoped or environment-scoped override map | `CompileDefinition` reads tenant/env config if present | Publish | Not stored alone; input to merge | `map[string]any` |
| **3 — instance overrides** | `components[].config` in the Application Definition | `CompileDefinition` reads from the draft/published definition | Publish | Source of truth in `application_definitions.definition` | `map[string]any` |
| **4 — compiled projection** | `resolved = L1 ⊕ L2 ⊕ L3`, validated against `configuration_schema`, secrets resolved to ciphertext | `CompileDefinition` writes it | Publish (in the compile txn) | `app_orchestrators` / `entry_points` / `middleware_wirings` columns + Fernet ciphertext | Typed struct (`AppOrchestrator`, `EntryPoint`, `MiddlewareWiring`) — the runtime reads *this*, not the merge inputs |

**Precise precedence:** `resolved_config = default_config ⊕ tenant_env_defaults ⊕ instance_config`,
where `⊕` is a deep merge with the right-hand side winning per key. A field absent at Layer 3 falls
through to Layer 2, then Layer 1. A field present at Layer 3 must satisfy `configuration_schema` or
publish fails (422). Layer 2 is **optional** — if no tenant/environment override map exists, the
chain is simply `default_config ⊕ instance_config` (matching today's behavior where there is no L2).

**Where the result lives and what the runtime reads.** The merged, validated, secret-resolved config
is written once into the projection columns at publish. **The runtime reads only Layer 4** — it never
re-runs the merge, never reads the definition, never reads the instance overrides. This is the
guarantee that keeps the registry off the hot path and keeps Temporal replay deterministic: the
worker's config is a frozen projection row.

---

## 13. Secrets Model

The airtight secret design. Invariants first, then the flow.

**Security invariants (non-negotiable):**
1. Secret **values** never appear in any `application_definitions.definition` JSONB.
2. Secret values never appear in exports.
3. Secret values never appear in logs (redact `secret://` refs and never log resolved plaintext).
4. Secret values never appear in Temporal event history (the worker reads *ciphertext* from the
   projection and decrypts in-activity, in-memory; the decrypted value is never a workflow argument).
5. The only durable home of a secret value is Fernet ciphertext in a projection row
   (`app_orchestrators.*_api_key_encrypted`, agent `auth_token_encrypted`, and equivalent
   middleware columns).

**What can appear in the Application Definition JSON:** only secret **references**. Format:
**`secret://<scope>/<name>`** (string URI), e.g. `secret://tenant/router-llm`. Scope is `tenant`
(tenant secret store), `app` (app-scoped), or `env` (environment). The reference names *where the
value lives*; it never carries the value. A `secret_bindings` map on each instance binds a
`credential_schema` slot to a reference: `"secret_bindings": { "llm_api_key": "secret://tenant/router-llm" }`.

**Where secret values live at runtime.** Fernet ciphertext in the projection column that the
corresponding runtime reader already expects (`app_orchestrators.llm_api_key_encrypted`, etc.). The
runtime path is unchanged — the worker/loader decrypts the column value exactly as today.

**How secrets are resolved at Publish time.** During `CompileDefinition`: for each instance
`secret_bindings` entry, (1) validate the referenced slot exists in the definition's
`credential_schema` and is required-or-provided, (2) look up the value from the secret store by the
`secret://` reference, (3) Fernet-encrypt it, (4) write the ciphertext to the projection column. The
plaintext exists only transiently in the compile process memory and is never persisted to the
definition, logged, or passed to Temporal.

**What happens at Import time.** The imported definition carries `secret_bindings` **references but
no values** (values were never in the artifact). Import resolves references against the *target*
environment's secret store. If a referenced secret does not exist in the target, the instance is
flagged `missing_secret` and the app cannot be published until the operator binds the value (creates
the secret) or rebinds the reference. Import never fabricates or carries secret material across
environments.

**What happens at Export time.** References are preserved verbatim; values are omitted (they were
never in the definition to begin with). An export is safe to commit to a repo or move between
environments because it is provably value-free — the invariant that nothing but references live in
the JSONB makes this automatic.

**Drift/rotation.** Rotating a secret updates the secret store; the *reference* is unchanged, so the
definition is unchanged. The projection ciphertext is stale until the next publish (or an explicit
`resolve-secrets` op that re-encrypts from the store without a full recompile) — a deliberate,
auditable action, never a silent live mutation of a running workflow.

---

## 14. Portable References

**Format:** `{ kind, namespace, name, version }`.

| Field | Meaning |
|---|---|
| `kind` | Component kind: `agent \| orchestrator \| middleware \| tool \| entry_point`. Selects the subtype/resolver. |
| `namespace` | Ownership + collision domain. Conventions: `them.builtin` (platform-owned), `them.tenant.<tenant_id>` (tenant-authored), `company.<product>` (external/vendor). |
| `name` | Human-stable name unique within `(kind, namespace)`. |
| `version` | **Integer revision** — monotonic per `(kind, namespace, name)`. Not semver. See Section 15. |

**UUID as optimization, portable identity as the stable key.** Each instance MAY carry a resolved
`definition_id` UUID as a cache. But the **portable tuple is the source of truth for identity**,
because UUIDs do not survive an environment boundary: exporting from staging and importing to prod
means the same logical `company.payments/ledger-agent@3` has a *different* UUID in each environment.
Resolution therefore proceeds:
1. **UUID first (fast path):** if `definition_id` is present and resolves in this environment, use
   it — cheap, no lookup by tuple.
2. **Portable identity fallback:** if the UUID is absent or does not resolve (cross-env import), look
   up by `(kind, namespace, name, version)` and re-cache the resolved UUID.

**What "version" means:** an integer revision of a definition. Editing a definition creates a new
integer revision; existing rows are immutable. This is simpler than semver, matches the
`application_definitions.revision` model already adopted in Option C, and makes pinning unambiguous
(Section 15). If semver-style compatibility ranges are ever needed, they can be layered as an
optional `version_constraint` field later without a format break — but v2 pins integers.

**Import resolution behavior (Q14):**

| Situation at import | Behavior |
|---|---|
| Referenced definition does not exist (unknown `kind/namespace/name`) | Import **fails validation** for that instance; report `unresolved_definition` with the tuple. No silent creation. |
| Definition exists but the pinned `version` does not | Import **fails**, reporting `version_not_found`; operator may re-pin to an available version (with a diff warning) or import the missing definition first. |
| Definition + version exist but instance `config` is incompatible with its `configuration_schema` | Import **fails** with `config_invalid` and the schema errors. The definition may have tightened its schema across versions. |
| Required secret binding missing in target env | Import **succeeds as draft** but marks the instance `missing_secret`; publish is blocked until bound. Import never carries secret values. |

Import is thus **fail-closed on identity and config**, **fail-open-to-draft on secrets** (because
secrets are legitimately environment-local and must be bound after transfer).

---

## 15. Version Pinning

**Decision: PIN EXACT VERSION at publish time, for every component kind.** A published Application
revision records, per instance, the resolved `definition_id` + integer `version` it compiled
against, stamped into both the definition JSON (`definition_ref.version`) and the projection row
(`component_definition_id` + `component_version`). No component kind uses "latest" or a floating
range in a published revision.

| Kind | Pin strategy | Rationale |
|---|---|---|
| `agent` | Exact version | An agent definition edit (endpoint change, schema tightening) must not silently alter a published app's behavior. |
| `orchestrator` (`llm-orchestrator`) | Exact version | Prompt/limit schema changes in the builtin definition must not retroactively change live apps. |
| `middleware` | Exact version | A redaction-rule change in a middleware definition is a behavior change; it must be an explicit re-publish. |
| `entry_point` (implicit) | N/A — protocol string, versioned with the platform | Protocols are platform code; no per-app pin. |

**Why exact, not range or latest.** The whole point of Option C's revision + hash model is
**reproducibility**: given a `run_id → definition_id → definition_hash`, you can reconstruct the exact
topology and config that authorized every call. "Latest" breaks this — a definition edit would change
what a published, unedited app does, silently, and would make historical runs irreproducible. A
compatible range (semver) is a half-measure that reintroduces silent change for "patch" edits, which
for prompts/redaction rules are still behavior changes. Exact pin is the only choice consistent with
the reproducibility invariant.

**Operator workflow — "I edited a component definition; how do apps get it?"**
- **Only new publishes get it (default):** editing a definition creates a new version. Existing
  published apps keep their pinned version and are unaffected. The next time an app is published, the
  Canvas surfaces "newer version available" and the operator opts in by re-pinning + re-publishing.
- **All apps must get it (fleet update):** a deliberate, auditable "bump-and-republish" operation
  iterates the apps pinned to the old version, re-pins them to the new version, re-validates each
  (config still satisfies the new `configuration_schema`; else flag), and re-publishes. This is an
  explicit fleet operation, never an implicit consequence of editing a definition. Apps whose config
  no longer validates against the new version are reported and left on the old pin for manual fixup.
- **Deprecating a version:** setting a definition version `status='deprecated'` blocks *new* pins to
  it while leaving *existing* pins valid — a soft migration lever.

---

## 16. Publish Process — Step by Step

`CompileDefinition` in the registry-backed model. Runs inside one transaction; either the whole
projection is replaced atomically or nothing changes.

1. **Load the draft Application Definition** (`application_definitions` where status=draft, or the
   named revision for activate/rollback).
2. **Resolve each `definition_ref`** — UUID fast path, else portable-identity lookup
   (Section 14). Fail closed on `unresolved_definition` / `version_not_found`.
3. **Pin** the resolved `definition_id` + integer `version` onto each instance for the compiled
   snapshot (Section 15). Record the definition `content_hash` for reproducibility.
4. **Validate each instance `config`** against its definition's `configuration_schema` (JSON Schema).
   Fail with `config_invalid` + per-field errors on mismatch.
5. **Validate connection compatibility** — for each `connections[]` edge, check the source's
   `capabilities`/`output_schema` against the target's `input_schema`/required `capabilities`
   (e.g. a `delegation` edge requires the target to have capability `delegation.target`; a `tool`
   edge requires the target to be `tool.callable`; a `via[]` middleware must accept the upstream
   output shape). Fail with `connection_incompatible`.
6. **Check secret reference resolution** — every required `credential_schema` slot must have a
   `secret_bindings` reference that resolves in this environment; missing required → fail; missing
   optional → warn (Section 13).
7. **Merge the config resolution chain** — `default_config ⊕ tenant/env ⊕ instance.config` per
   instance (Section 12); the merged result is the projection payload.
8. **Write relational projection rows** — upsert `app_orchestrators` keyed by immutable `name`
   (from the instance), `entry_points` keyed by immutable `slug`, replace `middleware_wirings` in
   `via[]` position order. Derive `allowed_agent_ids[]` from `tool` connections and `delegatable`
   from `delegation` connections (as `compile_graph` does today), now driven by typed
   `connections[]` instead of inferred edges. Carry `component_definition_id` + `component_version`
   pins onto each projection row.
9. **Encrypt secret values** — for each resolved binding, look up the value, Fernet-encrypt, write
   to the projection ciphertext column. Plaintext is transient and never persisted elsewhere.
10. **Stamp `definition_hash`** on `applications.source_definition_hash` and (optionally) each
    projection row's `source_definition_hash`.
11. **Set `applications.active_definition_id`** to the newly published revision; flip the definition
    `status` draft→published.
12. **Flush caches** — `DEL them:app:{app_id}:orch:{name}` (per name), `DEL them:orch:loc:{name}`,
    `DEL them:agents:registry`, then `PUBLISH them:ep:config:changed {app_id}` so `epconfig`
    invalidates by app UUID.

Steps 2–3 and 5 are the **new** registry work versus today's `compile_graph`; steps 8–12 are the
existing compile/flush behavior, unchanged in effect. The projection written is byte-for-byte the
same *shape* the runtime reads today — this is what makes the registry model additive.

---

## 17. Worker Runtime Package

What the Temporal worker receives at run start, and what it deliberately does **not** do.

**Reads at workflow start — one projection load, no registry:**
- `load_orchestration_context_activity` (or its Go equivalent) issues **one** SELECT set against the
  **projection**: the `app_orchestrators` row by immutable `name`, its `allowed_agent_ids[]`
  adjacency, resolved LLM/voice/TTS/memory config, decrypt-ready Fernet ciphertext, and the
  `middleware_wirings` for any `via[]` chains. Entry-point resolution already happened at connection
  time via `epconfig(slug)`.
- **It does NOT read `component_definitions`.** The definition registry is never touched during
  execution — all config was resolved and pinned at publish (Section 12, Section 16).

**Cached in workflow history:** the immutable resolved config snapshot returned by the load activity.
Because it is captured in Event History, replay is deterministic and a mid-run publish (which
rewrites the projection for *new* runs) cannot alter the in-flight workflow. The run also records
`definition_id` (+ optionally the pinned component versions) for audit reproducibility.

**Does NOT do during execution:** no registry lookup, no config re-merge, no secret re-resolution
against the store (it decrypts the ciphertext it already loaded, in-activity, in-memory), no schema
validation. All of that is publish-time work.

**The Go struct the worker receives** (illustrative — resolved, self-contained):

```go
type ResolvedOrchestrator struct {
    Name              string            // immutable Temporal lookup key (app_orchestrators.name)
    ComponentDefID    uuid.UUID         // pinned definition (audit only; NOT looked up at runtime)
    ComponentVersion  int               // pinned version (audit only)
    Kind              string            // "standard" | "adk" | ... (drives dispatch, not a lookup)
    SystemPrompt      string
    LLM               ResolvedLLM       // provider, model, decrypted-at-use api key ciphertext ref
    Limits            OrchestratorLimits// max_iterations, max_parallel_tools, history_window, budget
    AllowedAgents     []ResolvedAgent   // adjacency: id, transport, endpoint, timeout, ciphertext
    Delegatable       []ResolvedSubOrch // delegation targets (by immutable name)
    Middleware        []ResolvedMiddleware // position-ordered chains, resolved config
    Policies          ExecutionPolicies // max_delegation_depth, join, timeouts (from definition.policies)
}
```

Every field is a *value*, resolved at publish. The worker is a pure consumer of Layer 2; the registry
is invisible to it.

---

## 18. Impact on Wave 9

Wave 8 is **unchanged** (runtime + bulk-delete only). Wave 9 — the Application Definition Layer —
**absorbs the component registry**. Concrete deltas:

**New tables (Wave 9):**
- `them.component_definitions` (base contract, Section 7.1).
- (No new subtype tables initially — `agents` and `middleware_defs` *become* the `agent`/`middleware`
  subtypes via ALTER + base-row backfill.)
- `them.application_definitions` (already in Option C's Wave 9 scope).

**Existing tables modified (Wave 9):**
- `them.agents`: add shared-contract columns not already present (`namespace`, `version`,
  `capabilities`, `credential_schema`, `scope`, `status`, `content_hash`, `implementation_type`
  aligned from `transport`); backfill a `component_definitions` base row per agent
  (`agents.id` = base `id`).
- `them.middleware_defs`: same shared-contract columns; `config` → `default_config`; backfill base
  rows.
- `them.app_orchestrators`, `them.entry_points`, `them.middleware_wirings`: add
  `component_definition_id` + `component_version` pin columns + `source_definition_hash` stamp.
  **Shapes otherwise unchanged** (name/slug read paths preserved).
- `them.applications`: Option C columns (`active_definition_id`, `source_definition_hash`).
- `them.runs`: `definition_id` (Option C).
- Seed the builtin `llm-orchestrator` (`kind=orchestrator`) definition and, optionally, five builtin
  `entry_point` palette rows.

**Python migrations needed first** (before Go writers own the projection): the schema DDL above and
the backfills — a one-time job that (1) synthesizes revision-1 `application_definitions` from each
app's current projection (Option C backfill), (2) inserts a `component_definitions` base row per
existing agent and middleware_def, (3) stamps existing projection rows with their resolved
`component_definition_id`/`version` and the definition hash.

**New APIs (Wave 9):**
- Component CRUD: `GET/POST/PUT /api/v1/admin/component-definitions` (+ `?kind=`, `?namespace=`,
  version list), `POST .../{id}/publish`, `PATCH .../{id}/deprecate`. Read for the Canvas palette;
  write for definition authoring.
- Application definition APIs (already in Option C's Wave 9): `PUT /{id}/definition/draft`,
  `POST /{id}/validate`, `POST /{id}/publish`, `POST /{id}/activate`, `GET /{id}/export`,
  `POST /import`, `PUT /{id}/restore`, `POST /{id}/clone`, `POST /{id}/rollback` — now operating on
  the `components[]/connections[]` v2 format.
- Fleet ops (deferred within Wave 9 or to a follow-up): `POST /component-definitions/{ref}/rebump`
  (fleet re-pin, Section 15).

**Go packages to build (Wave 9):**
- `go/internal/admin/registry/` — component-definition resolver (UUID + portable identity),
  `configuration_schema` validator, capability/connection compatibility checker.
- `go/internal/admin/definition/` — `CompileDefinition` (Section 16), draft/publish/validate,
  export/import/restore/clone/rollback over the v2 format. Consumes `registry` + `go/internal/crypto/fernet.go`.
- `go/internal/admin/dal/component_definitions.go` — base + subtype CRUD.
- `go/internal/admin/dal/definitions.go` — `application_definitions` CRUD + projection writers for
  `app_orchestrators`, `entry_points`, `middleware_wirings` (none exist in Go today).

**Sequencing within Wave 9:** (1) DDL + backfills (Python migration); (2) registry DAL + resolver +
schema validator; (3) `CompileDefinition` + publish pipeline; (4) draft/validate/publish/activate;
(5) export/import/restore/clone/rollback over v2; (6) component-definition CRUD APIs + Canvas
palette. Fleet re-pin and tenant-authored orchestrator templates are deferred within/after Wave 9.

---

## 19. Impact on the Migration Roadmap (Waves 8–15)

Same table as 55ad66e §17. **What changed:** Wave 9 now explicitly includes the component registry
(new `component_definitions` table, agent/middleware subtype adoption, portable-reference resolution,
v2 `components[]/connections[]` definition format). **What stayed the same:** every wave's *domain*
and *category*; Wave 8 scope; the definition-layer-as-linchpin sequencing. **What is deferred:**
tenant-authored orchestrator templates, fleet re-pin UX, semver constraints, physical `agents`/
`middleware_defs`→subtype physicalization if Option D fallback is used for a wave.

| Wave | Domain | Scope | Depends on | Category | Change vs 55ad66e |
|---|---|---|---|---|---|
| **8** | App runtime special-ops (pure) | `PUT /{id}/runtime`, `POST /bulk-delete` | epconfig pub/sub | **M** | **Unchanged.** |
| **9** | **Application Definition + Component Registry Layer** | `component_definitions` base + agent/mw subtype adoption + backfill; `application_definitions` + backfill; portable-ref resolver + schema validator; `CompileDefinition` (v2 components/connections); draft/publish/validate/activate; export/import/restore/clone/rollback; component-definition CRUD; `runs.definition_id` | Wave 8; Fernet | **R** | **Extended** with the registry (this review). |
| **10** | Runs read/audit tail | stats, contexts, tasks, artifacts, bulk-delete, delete | run recorder | **M** | Unchanged. |
| **11** | Run control (Temporal) | cancel + signal delivery | Go temporal signaler | **M** | Unchanged. |
| **12** | Apps runtime surface | `/apps`, `/apps/{slug}` REST, tasks | execution pkg | **M** | Unchanged. |
| **13** | A2A server + agent card | agent-card, `/a2a`, `/a2a/push` | Go `a2a` pkg | **R** | Unchanged (invoke `/a2a`). |
| **14** | Admin agent/orch/middleware/system-agent ops | test/discover/security-scan; middleware-defs CRUD now = component-def CRUD; system-agents; middleware-wirings write (definition-driven); per-AO test-llm/voice/tts | Wave 9 registry | **R** | **Extended:** middleware-defs CRUD folds into component-definition CRUD; `orchestrators` legacy table deprecation begins here. |
| **15** | Voice + legacy deprecation + Python removal | voice/webrtc; deprecate legacy `orchestrators/{name}` voice + `ws/orchestrate/{name}`; **drop legacy `orchestrators` table**, dead `edges`/`orchestrator_id` columns; remove Python | Waves 8–14 | **M**+**X** | **Extended:** legacy `orchestrators` table + dead columns dropped here. |

Blocked-by-Wave-9: import, restore, clone, rollback, export, middleware-wirings writes, component
authoring, portable references, config resolution, secret resolution.

---

## 20. Summary Decision Table

| Question | Decision |
|---|---|
| **Compatible with Option C?** | **Yes — extends, not replaces.** Registry is a fourth *design-time* concept beside Layer 1; three runtime layers and the Temporal model are unchanged. |
| **Does it improve or complicate Option C?** | **Improves** portability (portable identity survives env boundaries), Canvas ergonomics (uniform schema forms), extensibility (new kinds = data, not DDL). Cost: one base table + a resolve step in compile. Runtime unchanged. |
| **Common definition contract?** | **Accept** the proposed fields; **add** `scope`, `tenant_id`, `status`, `content_hash`; **modify** `capabilities` to typed tags and `version` to integer revision. Instance-only fields (name/slug, config overrides, secret bindings, position) stay off the definition. |
| **Storage model (A/B/C/D)?** | **Option C — base `component_definitions` + kind subtypes.** `agents`/`middleware_defs` *become* the agent/middleware subtypes (id-shared), preserving all FKs. Option D (view) is the fallback if Wave 9 can't ALTER `agents`. |
| **Which kinds get subtype tables?** | `agent` (existing `agents`), `middleware` (existing `middleware_defs`). `orchestrator`, `entry_point`, `tool` need no subtype initially. |
| **agents mapping** | Component Definition, kind `agent`. Keep + extend into subtype. No relocation. |
| **orchestrators (global) mapping** | **Deprecated.** Builtin `llm-orchestrator` definition replaces it; retire after backfill. Its `name`-unique coupling dies with it. |
| **app_orchestrators mapping** | Compiled projection of an orchestrator instance. Keep, immutable `name` preserved, add pin/stamp columns. |
| **middleware_defs mapping** | Component Definition, kind `middleware`. Absorb as subtype. |
| **middleware_wirings mapping** | Compiled projection of a middleware instance. Keep; `def_id` resolved via portable ref at publish. |
| **entry_points mapping** | Compiled projection of an EP instance of an *implicit* protocol definition. Keep; `slug` read path preserved; no EP-definition table. |
| **applications mapping** | Container; keep + Option C revision columns. |
| **Entry Point definition/instance split?** | Instance in DB; definition is **implicit** (the protocol type string). Optional 5 builtin palette rows for Canvas forms; projection never joins to them. |
| **Orchestrator definition/instance split?** | **(c)** builtin `llm-orchestrator` definition as the class for all standard orchestrators; app orchestrators are its instances. **(b)** tenant-authored templates available later for free. **Not** the legacy `orchestrators` table. |
| **Same definition, multiple instances?** | Each instance has its own `instance_id`, `config`, `secret_bindings`, and (where required) immutable `name`/`slug`. Example: PII middleware used twice with different `redact` config (Section 9). |
| **Config resolution precedence?** | `default_config ⊕ tenant/env defaults ⊕ instance.config`, deep-merge right-wins, validated vs `configuration_schema`, frozen into the projection at publish. Runtime reads only the projection. |
| **configuration_schema usage?** | Canvas property forms, API validation, publish validation (step 4), and the merge/validate in compile. Not read at runtime. |
| **Secrets?** | References only in JSON (`secret://scope/name`); Fernet ciphertext only in projection; resolved+encrypted at publish; never in JSONB, exports, logs, or Temporal history. |
| **Portable references?** | `{kind, namespace, name, version}`; UUID is a cache; portable tuple is the stable key; version = integer revision; UUID-first then tuple-fallback resolution. |
| **Import behavior (missing def / version / config / secret)?** | Fail-closed on unresolved definition, missing version, invalid config; fail-open-to-draft (flagged `missing_secret`) on unbound secrets. |
| **Version pinning?** | **Exact version pinned at publish** for all kinds. No latest/range. Definition edits affect only new publishes; fleet update is an explicit, auditable re-pin+republish. |
| **Publish process?** | Load draft → resolve refs → pin → validate config → validate connections → check secrets → merge config → write projection → encrypt secrets → stamp hash → set active → flush caches (Section 16). |
| **Worker runtime package?** | One projection load at run start; immutable snapshot in history; **zero registry lookups** during execution; resolved `ResolvedOrchestrator` Go struct. |
| **Wave 8 scope?** | **Unchanged — runtime + bulk-delete only.** This review does not touch Wave 8. |
| **Name-coupling (app_orchestrators.name)?** | Immutable `name` stays on the **instance** and remains the Temporal lookup key; definition `name` is a separate namespace; `agent__orch__` prefix coupling replaced by explicit `ref_kind`. |
| **Slug-coupling (epconfig)?** | `slug` stays on the EP **instance**; `entry_points` projection + `epconfig(slug)` read path unchanged; no EP-definition join added. |
| **Simplest stable architecture recommendation?** | Option C storage + implicit EP definitions + builtin `llm-orchestrator` + exact version pinning + secret-references-only + portable identity with UUID cache. Additive to Option C; runtime untouched; new kinds are data. |

---

## 21. Simplest Stable Architecture — Recommendation

The simplest architecture that is *also* stable under the platform's known future (multi-instance
components, cross-environment import, ADK/tool kinds, reproducible runs) is:

1. **One base `component_definitions` table (Option C)** with the shared contract, `agents` and
   `middleware_defs` adopted as id-shared subtypes so nothing relocates and no FK breaks.
2. **Entry points stay instance-only**, their definition implicit in the protocol string, so the
   `epconfig(slug)` hot path is untouched.
3. **A single builtin `llm-orchestrator` definition** as the class for all standard orchestrators;
   the legacy global `orchestrators` table is deprecated so its cluster-wide `name` coupling is
   removed rather than propagated.
4. **Application Definition v2** stores `components[]` (instances with portable `definition_ref`,
   `config`, `secret_bindings`) + typed `connections[]`; immutable `name`/`slug` live on instances.
5. **Exact version pinning at publish** for reproducibility; definition edits never silently change
   a published app.
6. **Secret references only** in JSON; Fernet ciphertext only in projection; resolution at publish.
7. **Portable `{kind, namespace, name, version}` identity**, UUID as a resolution cache.
8. **The runtime is unchanged** — one projection load per run, zero registry lookups, deterministic
   replay.

This is *additive to Option C*, honors both hard constraints (immutable Temporal `name`, epconfig
`slug`), keeps the secret model airtight, and makes every future component kind a data operation.
It is the recommendation.
