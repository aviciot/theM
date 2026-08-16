# R6 — Tenant Architecture Review
**Date:** 2026-08-16
**Review SHA:** ca29acd
**Status:** RESEARCH ONLY — no code changes
**Author:** Architecture Review (Claude Code)

---

## Purpose

This document is a comprehensive audit of the current multi-tenant architecture in the-M platform. It identifies where tenant isolation is present, where it is absent or inconsistent, what risks exist today, and what must be done before Wave 9 (multi-tenant runtime) can be safely implemented. It is the authoritative reference for all Wave 9 planning and for the resolution of the diverged `026_tenant_foundation.sql` migration.

---

## Table of Contents

1. [Current Tenant Architecture — What Is and Is Not Scoped](#1-current-tenant-architecture)
2. [Problems and Inconsistencies Found](#2-problems-and-inconsistencies)
3. [Target Tenancy Principles](#3-target-tenancy-principles)
4. [DB Ownership Matrix — Every Table](#4-db-ownership-matrix)
5. [Component Registry Scope Model](#5-component-registry-scope-model)
6. [API Tenant Model](#6-api-tenant-model)
7. [Application Definition Tenant Model](#7-application-definition-tenant-model)
8. [Import / Export Tenant Behavior](#8-import--export-tenant-behavior)
9. [Secrets Model](#9-secrets-model)
10. [Redis / Cache Changes Needed](#10-redis--cache-changes-needed)
11. [Temporal Changes Needed](#11-temporal-changes-needed)
12. [026 Compatibility Decision](#12-026-compatibility-decision)
13. [Revised Wave 9 DB Proposal](#13-revised-wave-9-db-proposal)
14. [Security Risks — Ranked](#14-security-risks)
15. [Exact Implementation Order](#15-exact-implementation-order)
16. [Safe to Implement Wave 9 — Conclusion](#16-safe-to-implement-wave-9--conclusion)

---

## 1. Current Tenant Architecture

### 1.1 What Is Scoped to a Tenant Today

The following tables have a `tenant_id` column that is NOT NULL, carries a FK to `them.tenants`, and defaults to the bootstrap UUID `00000000-0000-0000-0000-000000000001`:

| Table | tenant_id | Unique constraint |
|---|---|---|
| `access_tokens` | NOT NULL, FK, default bootstrap | — |
| `agents` | NOT NULL, FK, default bootstrap | UNIQUE(tenant_id, slug) |
| `applications` | NOT NULL, FK, default bootstrap | — |
| `orchestrators` | NOT NULL, FK, default bootstrap | UNIQUE(tenant_id, name) |
| `run_artifacts` | NOT NULL, FK, default bootstrap | — |
| `runs` | NOT NULL, FK, default bootstrap | — |

These tables are correctly prepared for multi-tenancy at the DB layer. All rows existing before the tenant columns were added were retroactively assigned to the bootstrap tenant, so there are no orphaned rows.

Additionally, `runs` has an `application_id` nullable column that provides a secondary scoping dimension — run records can be associated with a specific application within a tenant.

### 1.2 What Is Partially Scoped

| Table | How it is scoped | Gap |
|---|---|---|
| `audit_logs` | tenant_id nullable, no FK | Nullable means rows can have NULL; FK missing means referential integrity not enforced |
| `entry_points` | No tenant_id; has application_id FK to applications | Scoped transitively through applications.tenant_id, but no direct tenant column; GLOBAL UNIQUE(slug) prevents same slug in two tenants |
| `middleware_wirings` | No tenant_id; has application_id FK to applications | Same transitive scoping as entry_points |
| `app_orchestrators` | No tenant_id; has application_id FK to applications (via UNIQUE(application_id, node_id)) but also GLOBAL UNIQUE(name) | Transitive scoping only; global unique on name prevents same orchestrator name across tenants |

### 1.3 What Is Explicitly Global (No Tenancy)

| Table | Reason global |
|---|---|
| `config` | Platform-level settings, intentionally global |
| `llm_providers` | Platform-level provider registry, intentionally global |
| `middleware_defs` | Global definition library; GLOBAL UNIQUE(slug) |
| `tenants` | The tenant registry itself |

### 1.4 Bootstrap Tenant

The bootstrap tenant is the implicit tenant for all pre-tenant data and for all super_admin UI users:

```
id:            00000000-0000-0000-0000-000000000001
slug:          default
display_name:  Default Tenant
enabled:       true
```

The Go layer's `AdminTenantMiddleware` automatically injects this ID into the request context when the JWT's `tenant_id` claim is empty. This means all existing admin UI sessions transparently operate in the bootstrap tenant without any migration of credentials or UI changes.

### 1.5 How Tenant Identity Flows Through the Go Layer

The `Claims` struct carries the tenant identity from the JWT:

```go
type Claims struct {
    // ...
    TenantID string `json:"tenant_id,omitempty"`
}
```

Key design constraints:
- `TenantID` NEVER comes from the request body or URL path — only from the verified JWT payload
- `AdminTenantMiddleware`: empty `TenantID` → uses bootstrap UUID (supports super_admin users)
- `BearerTenantMiddleware`: empty `TenantID` → 403 (bearer tokens must be explicitly tenant-scoped)
- `tenantctx` package: no fallback logic; returns `ErrNoTenant` or `ErrInvalidTenant` if the context value is absent — middleware must populate it first

This layered approach is sound. The weak point is that the middleware chain must be correctly applied to every route group, and the current audit reveals some route handlers bypass this.

### 1.6 EPConfig Cache and Entry Point Scoping

The `epconfig.EPConfig` struct carries a `TenantID` field sourced from `applications.tenant_id`. However, the EPConfig cache is keyed by `ep_slug` only:

```
cache key: them:ep:{ep_slug}:config   (inferred — no tenant in key)
```

Because `entry_points` has `GLOBAL UNIQUE(slug)`, EP slugs cannot collide across tenants today. This means the cache is accidentally safe — but only because of the global uniqueness constraint. If that constraint is ever relaxed, the cache becomes a cross-tenant data leak vector.

---

## 2. Problems and Inconsistencies

This section enumerates every confirmed inconsistency found during the review, ordered roughly by severity.

### P-01: `app_orchestrators` Has No `tenant_id` Column (CRITICAL)

`app_orchestrators` is the table that wires named orchestrators to applications. It has:
- `UNIQUE(name)` — globally unique orchestrator name
- `UNIQUE(application_id, node_id)` — unique within an application
- No `tenant_id` column

**Effect:** Two tenants cannot have orchestrators with the same name, even if they are in completely separate applications. In a multi-tenant deployment, this is a namespace collision waiting to happen. The name constraint must become `UNIQUE(tenant_id, name)` once `tenant_id` is added.

**Resolution required:** Add `tenant_id NOT NULL` to `app_orchestrators`, drop `UNIQUE(name)`, create `UNIQUE(tenant_id, name)`. The 026 migration file partially addresses this (adds column and creates the unique index) but contains bugs (see Section 12).

### P-02: `entry_points` Has `GLOBAL UNIQUE(slug)` (HIGH)

Entry point slugs are globally unique across all tenants. This means:
- Two tenants cannot use the same application slug (e.g., `customer-support`)
- The EP slug is used as a routing token in URLs: `/apps/{slug}/ws` — this implicitly assumes global uniqueness
- The Go EPConfig cache uses EP slug as the cache key with no tenant namespace

**This is acceptable for a single-tenant-at-a-time deployment**, but it is an architectural blocker for true multi-tenancy. Resolution requires:
1. Adding `tenant_id` to `entry_points`
2. Changing `UNIQUE(slug)` to `UNIQUE(tenant_id, slug)`
3. Adding tenant to EPConfig cache keys
4. Adding tenant discrimination to URL routing (e.g., tenant subdomain or `/t/{tenant_slug}/apps/{slug}/ws`)

**Do not attempt this in Wave 9.** It is a breaking URL change. Document it as Wave 10.

### P-03: Temporal Orchestrator Resolution Is Globally Namespaced (CRITICAL)

The Python loader resolves orchestrators by name with no tenant filter:

```sql
SELECT * FROM app_orchestrators WHERE name = $1 AND enabled = true
```

There is no `tenant_id` or `application_id` filter. If two tenants have orchestrators named `support-bot`, the query returns whichever row happens to be first. This is a cross-tenant data access bug.

The Redis locator exacerbates this:

```
them:orch:loc:{name}   → stores "app:{app_id}" or "tmpl"
them:orch:tmpl:{name}  → template orchestrator cache
```

Both keys are globally namespaced by name. A cache write from Tenant A's `support-bot` will be read by Tenant B's `support-bot` resolution. This is a **cross-tenant cache poisoning** vulnerability.

**Resolution:** Orchestrator resolution must filter by tenant_id. Redis keys must include tenant_id prefix.

### P-04: Go `/apps/{slug}/ws` Handler Passes EP Slug as Orchestrator Name (HIGH)

The Go WS route `/apps/{slug}/ws` uses the EP slug as the `OrchestratorName` passed to Temporal:

```
OrchestratorName = ep_slug
```

The Python path does it correctly:
```
EP slug → load entry_point.app_orchestrator → use app_orchestrator.name
```

These are semantically different. An entry point's slug and its wired orchestrator's name are not required to be equal. When they differ, the Go path will fail to find the orchestrator or will find the wrong one. This is a routing correctness bug independent of tenancy, but it becomes a security issue in multi-tenant context because the wrong orchestrator might belong to a different tenant.

**Resolution:** The Go handler must look up the EP record, retrieve `app_orchestrator_id`, then use `app_orchestrators.name` — the same logic as Python.

### P-05: Redis `them:agents:registry` Has No Tenant Prefix (HIGH)

The agent registry cache key:
```
them:agents:registry
```

This stores agent configurations globally. In a multi-tenant deployment, agents are scoped per tenant (agents table has tenant_id). But the cache does not reflect this. A cache hit from one tenant's registry write will serve data to another tenant's agent lookup.

**Resolution:** Key must become `them:agents:registry:{tenant_id}` or `them:t:{tenant_id}:agents:registry`.

### P-06: `audit_logs.tenant_id` Is Nullable With No FK (MEDIUM)

Audit logs are a compliance concern. If tenant_id can be NULL, audit records cannot be reliably attributed to a tenant. Additionally, the absence of a FK means a tenant deletion would orphan audit records without cascade or block.

The 026 migration attempts to add `NOT NULL` to audit_logs.tenant_id, but this will fail if any existing rows have NULL values. Given that audit_logs records system events that predate the tenant column, there are almost certainly NULL rows.

**Resolution options:**
1. Backfill all NULL audit_log rows to bootstrap tenant, then set NOT NULL (safe for single-tenant)
2. Keep nullable but enforce non-null at application layer for all new writes (pragmatic)
3. Add a FK without NOT NULL constraint (referential integrity without forcing attribution)

Option 2 is recommended for Wave 9. Option 1 can follow once backfill is confirmed safe.

### P-07: `middleware_defs` Has Global Slug Namespace (MEDIUM)

`middleware_defs` has `GLOBAL UNIQUE(slug)`. Middleware definitions are shared across tenants — they are a library of reusable logic blocks. This is intentional global behavior. However, it means tenants cannot have private middleware with names that overlap with other tenants' middleware names.

In the current single-tenant model this is fine. In a multi-tenant SaaS model, tenants should be able to define private middleware. This requires adding `tenant_id` to `middleware_defs` and making the unique constraint `UNIQUE(tenant_id, slug)`, with a sentinel `tenant_id = NULL` or `tenant_id = 00000000-...` for platform-global middleware.

**This is Wave 10 work.** Do not attempt in Wave 9.

### P-08: `026_tenant_foundation.sql` Is Diverged From Live Schema (CRITICAL)

The file `db/026_tenant_foundation.sql` exists on disk but has NOT been applied (not present in `schema_migrations`). The tenant work was applied directly (outside of migrations), creating a schema divergence:

1. Live `tenants` table has `enabled` column; 026 defines `is_bootstrap` column — schema mismatch
2. Live `agents.tenant_id` is NOT NULL; 026 would try to ADD COLUMN IF NOT EXISTS (safe, no-op) then SET NOT NULL (would fail if any NULLs, but none exist — so safe)
3. Live `audit_logs.tenant_id` is nullable; 026 step 6 would SET NOT NULL — **WILL FAIL** if any NULL rows exist
4. `app_orchestrators.tenant_id` does NOT exist in live schema; 026 would ADD COLUMN — **this is the only net-new DDL 026 would execute**

See Section 12 for the full compatibility table and recommended resolution.

### P-09: Temporal Workflow Input Has No `application_id` or `tenant_id` (HIGH)

The Temporal workflow is launched with `orchestrator_name` as the primary routing key. Neither `application_id` nor `tenant_id` is included in workflow input. This means:
- The workflow cannot enforce tenant isolation independently
- The workflow cannot attribute runs to an application without a separate lookup
- Cross-tenant orchestrator name collisions (P-01) directly affect which workflow runs

**Resolution:** Add `tenant_id` and `application_id` to Temporal workflow input. The Temporal activities that load orchestrator configuration must filter by these values.

### P-10: EPConfig Cache Key Has No Tenant Dimension (MEDIUM)

As noted in Section 1.6, the EPConfig cache key is `{ep_slug}` only. This is currently safe because EP slugs are globally unique. But this is a hidden coupling — if the global uniqueness constraint is ever relaxed, the cache silently becomes a cross-tenant data leak.

**Resolution (deferred to Wave 10):** When `entry_points` gains `tenant_id` and the unique constraint changes to `UNIQUE(tenant_id, slug)`, the EPConfig cache key MUST simultaneously change to include tenant_id.

---

## 3. Target Tenancy Principles

The following principles must govern all Wave 9 and later work. They are non-negotiable design constraints.

### P1 — Tenant Identity from JWT Only

Tenant identity MUST come from the verified JWT payload (`tenant_id` claim). It MUST NOT come from:
- Request body parameters
- URL path parameters (except as a hint to be verified against JWT)
- Request headers set by the client
- Database lookups using client-supplied tenant hints

**Rationale:** Any path that allows a client to assert its own tenant_id enables horizontal privilege escalation.

### P2 — Tenant Isolation at the Query Layer

Every DB query that accesses tenant-scoped data MUST include a `tenant_id` filter. DAL functions must accept `tenantID string` as a required parameter and include it in the WHERE clause. No DAL function may return data across tenant boundaries.

**Rationale:** Defense in depth — if the API layer fails to scope correctly, the DB layer must still prevent cross-tenant reads.

### P3 — Cache Keys Include Tenant Prefix

Every Redis key that stores tenant-scoped data MUST include `tenant_id` (or a tenant-scoped surrogate like `application_id`) in the key. Global cache keys are only permitted for explicitly global data (llm_providers, config, middleware_defs in platform-global scope).

**Rationale:** A cache miss causing a DB query is recoverable. A cache hit serving another tenant's data is a silent data breach.

### P4 — No Global Name Uniqueness for Tenant-Scoped Resources

Orchestrators, agents, applications, entry points, and middleware must NOT have globally unique name constraints. Uniqueness must be scoped to `(tenant_id, name/slug)`. Platform-global resources (config, llm_providers) may retain global uniqueness.

### P5 — Bootstrap Tenant Is a Real Tenant

The bootstrap tenant `00000000-0000-0000-0000-000000000001` is not a special case — it is the first and currently only real tenant. All code paths that resolve tenant identity MUST work correctly when the tenant is the bootstrap tenant. No special-casing of the bootstrap UUID in query logic is permitted.

**Rationale:** Special-casing the bootstrap tenant creates dead code paths that will be untested in production when real tenants are added.

### P6 — Tenant Deletion Must Be Safe

When a tenant is deleted (or disabled), the platform MUST either:
- Block deletion if the tenant has live sessions or active runs
- Cascade-delete or soft-delete all tenant-scoped data in a safe transaction

The `tenants.enabled` column is the correct mechanism for soft-disabling. Hard deletion is Wave 11 work.

### P7 — Application ID Flows Through Temporal

The Temporal workflow input must include `application_id` and `tenant_id`. Temporal activities that load configurations must use these values to scope their queries. The orchestrator_name alone is insufficient.

### P8 — Entry Point Slugs Become Tenant-Scoped in Wave 10

Changing EP slug uniqueness is a breaking URL change. It is deferred to Wave 10. Wave 9 must not make any assumptions that EP slugs are globally unique going forward — all new code should be written to accept tenant-scoped EP slugs even if the constraint is not yet enforced.

---

## 4. DB Ownership Matrix — Every Table

This matrix lists every table in the `them` schema, its tenancy model, the current state, the target state, and the wave where any gap is resolved.

| Table | Tenancy Model | Current State | Target State | Gap | Resolution Wave |
|---|---|---|---|---|---|
| `tenants` | Platform registry | OK — has id, slug, enabled | OK | Schema divergence with 026 (enabled vs is_bootstrap) | Wave 9 (fix 026) |
| `config` | Platform global | OK — no tenant_id | OK — stays global | None | — |
| `llm_providers` | Platform global | OK — no tenant_id | OK — stays global | None | — |
| `agents` | Tenant-scoped | OK — tenant_id NOT NULL, UNIQUE(tenant_id, slug) | OK | None | — |
| `orchestrators` | Tenant-scoped | OK — tenant_id NOT NULL, UNIQUE(tenant_id, name) | OK | None | — |
| `access_tokens` | Tenant-scoped | OK — tenant_id NOT NULL | OK | None | — |
| `applications` | Tenant-scoped | OK — tenant_id NOT NULL | OK | None | — |
| `runs` | Tenant-scoped | OK — tenant_id NOT NULL, application_id nullable | OK | application_id should be NOT NULL eventually | Wave 10 |
| `run_artifacts` | Tenant-scoped | OK — tenant_id NOT NULL | OK | None | — |
| `audit_logs` | Tenant-scoped | PARTIAL — tenant_id nullable, no FK | tenant_id NOT NULL, FK | NULL rows prevent NOT NULL migration | Wave 9 (backfill + constraint) |
| `entry_points` | App-scoped (transitive) | PARTIAL — no tenant_id, GLOBAL UNIQUE(slug) | tenant_id NOT NULL, UNIQUE(tenant_id, slug) | Breaking URL change required | Wave 10 |
| `app_orchestrators` | App-scoped (transitive) | MISSING tenant_id, GLOBAL UNIQUE(name) | tenant_id NOT NULL, UNIQUE(tenant_id, name) | Must add tenant_id, fix uniqueness | Wave 9 |
| `middleware_defs` | Platform global (currently) | No tenant_id, GLOBAL UNIQUE(slug) | tenant_id nullable (NULL = platform), UNIQUE(tenant_id, slug) | Breaking change | Wave 10 |
| `middleware_wirings` | App-scoped (transitive) | No tenant_id, has application_id FK | tenant_id NOT NULL (via application) | Low risk — transitive scoping OK | Wave 10 |

### Notes on Transitive Scoping

Tables that lack a direct `tenant_id` but reference `application_id → applications.tenant_id` are transitively tenant-scoped. This is acceptable when:
1. Queries always join through `applications` to filter by tenant
2. The FK chain is intact and enforced

For `entry_points` and `middleware_wirings`, the transitive chain is valid today. The gap is that queries that don't join through `applications` can accidentally return cross-tenant data.

---

## 5. Component Registry Scope Model

### 5.1 Agents Registry

Agents are tenant-scoped resources. The `agents` table has `tenant_id NOT NULL` with `UNIQUE(tenant_id, slug)`. This is correct.

**Current Gap — Redis cache:**
```
them:agents:registry    ← WRONG: no tenant prefix
```

**Target key pattern:**
```
them:t:{tenant_id}:agents:registry
```

The agent registry cache must be keyed by tenant. Cache invalidation on agent CRUD must invalidate only the affected tenant's cache, not the global cache.

### 5.2 Orchestrators Registry

Orchestrators are tenant-scoped resources. The `orchestrators` table has `tenant_id NOT NULL` with `UNIQUE(tenant_id, name)`. This is correct.

**Current Gap — Redis cache:**
```
them:orch:loc:{name}    ← WRONG: globally namespaced
them:orch:tmpl:{name}   ← WRONG: globally namespaced
```

**Target key patterns:**
```
them:t:{tenant_id}:orch:loc:{name}
them:t:{tenant_id}:orch:tmpl:{name}
```

### 5.3 App Orchestrators Registry

App orchestrators link orchestrators to applications. They are scoped through `application_id` but lack a direct `tenant_id`. The global `UNIQUE(name)` constraint is a critical flaw.

**Target:** Add `tenant_id NOT NULL` column. Change `UNIQUE(name)` to `UNIQUE(tenant_id, name)`.

### 5.4 LLM Providers Registry

LLM providers are platform-global. No tenant scoping is needed or desired. Multiple tenants share the same LLM provider configurations. This is correct.

If per-tenant LLM provider overrides are needed in the future, the pattern should be an `llm_provider_overrides` table with `tenant_id`, not modifications to `llm_providers`.

### 5.5 Middleware Definitions Registry

Middleware definitions are currently platform-global. For Wave 9, they remain global. Wave 10 should introduce tenant-private middleware via `tenant_id nullable` with `UNIQUE(COALESCE(tenant_id, '00000000-...'), slug)` or a similar construct.

---

## 6. API Tenant Model

### 6.1 Admin API Routes

Admin routes are served by both the Python bridge and the Go gateway. The tenant model for admin routes:

- All admin JWT tokens for super_admin users carry empty `tenant_id`
- `AdminTenantMiddleware` fills in bootstrap UUID
- All DAL queries include `tenant_id` filter using the context value
- Result: super_admin users see only bootstrap tenant data (correct for single-tenant; in multi-tenant, super_admin would need a separate tenant-selection mechanism)

**Gap:** There is no mechanism for a super_admin to act on behalf of a non-bootstrap tenant without getting a JWT for that tenant. This is acceptable for Wave 9 (single active tenant) but must be addressed before multiple tenants are provisioned.

### 6.2 Bearer Token Routes (Runtime API)

Bearer tokens carry `tenant_id` explicitly. `BearerTenantMiddleware` enforces non-empty tenant_id (403 if absent). This is the correct model for runtime API calls.

All new runtime routes MUST use `BearerTenantMiddleware` and MUST pass the resulting tenant_id to all DAL queries.

### 6.3 WebSocket Routes

The WS routes `/apps/{slug}/ws` and `/ws/orchestrate/{app_slug}/{ep_slug}` are the primary runtime entry points. These routes must:
1. Validate the bearer token and extract tenant_id via `BearerTenantMiddleware`
2. Look up the entry point record using `ep_slug` AND verify `applications.tenant_id = token.tenant_id`
3. Look up the app_orchestrator record using `app_orchestrator_id` from the entry point (NOT ep_slug as orchestrator name)
4. Pass `tenant_id` and `application_id` to Temporal workflow input

Current implementation in Go `/apps/{slug}/ws` fails at step 3: it passes `ep_slug` as `OrchestratorName`. This is Bug P-04.

### 6.4 SSE Routes

SSE routes follow the same model as WS routes. The same tenant_id flow applies. No specific SSE-related tenant gaps were identified beyond the general Temporal input issue (P-09).

### 6.5 Tenant Provisioning API

There is currently no API for creating or managing tenants. Tenants are bootstrapped directly in the DB. Wave 9 should introduce a minimal tenant provisioning API under admin routes:

```
POST   /admin/tenants          — create tenant
GET    /admin/tenants          — list tenants
GET    /admin/tenants/{id}     — get tenant
PATCH  /admin/tenants/{id}     — update (enable/disable)
```

Tenant creation must:
- Generate a UUID (not accept client-supplied UUIDs)
- Validate slug uniqueness
- Set `enabled = true` by default
- NOT create any child resources (applications, agents, etc.) — those come later

---

## 7. Application Definition Tenant Model

### 7.1 Application → Entry Point → App Orchestrator Chain

The application definition chain is:

```
tenants (tenant_id)
  └── applications (tenant_id FK, app_id)
        └── entry_points (application_id FK, ep_slug)
        │     └── epconfig (TenantID from applications.tenant_id)
        └── app_orchestrators (application_id FK, name)
              └── orchestrators (tenant_id FK, name)
```

This chain is logically correct but has structural gaps:
- `entry_points` and `app_orchestrators` have no direct `tenant_id`
- The chain can be traversed correctly in SQL but requires multi-table joins
- The global `UNIQUE` constraints on `entry_points.slug` and `app_orchestrators.name` prevent the chain from working across multiple tenants with overlapping names

### 7.2 Application Compiler

The application compiler (`app/services/app_compiler.py`) generates the runtime configuration graph from the application definition. It must:
- Always scope its queries by `tenant_id` (through `application_id`)
- Include `tenant_id` in the compiled EPConfig output
- Reject compilation requests where the requester's tenant_id does not match the application's tenant_id

Currently, the compiler is called from admin routes that already have `tenant_id` in context, so the scoping is implicit. Making it explicit (passing tenant_id to `compile_graph`) is a Wave 9 hardening task.

### 7.3 Graph Export / Import

Application graph export and import must handle tenant identity carefully:

- **Export:** Strip `tenant_id` from exported graph. The export is a portable definition, not a tenant-bound artifact.
- **Import:** Always assign the importing tenant's `tenant_id` to all imported resources. Never trust `tenant_id` values in the imported payload.
- **Cross-tenant copy:** Not supported in Wave 9. Wave 10.

---

## 8. Import / Export Tenant Behavior

### 8.1 What Gets Exported

When an application definition is exported (graph export), the following are included:
- Application definition (name, description, configuration)
- Entry point definitions (slugs, configuration)
- App orchestrator node definitions (names, wiring)
- Agent references (by slug — NOT by tenant-scoped ID)
- Orchestrator references (by name — NOT by tenant-scoped ID)

### 8.2 What Must Be Stripped from Exports

The following MUST be removed from export payloads before they leave the API:
- `tenant_id` values on any resource
- `application_id` values (these are internal UUIDs)
- `entry_point_id` values
- Database primary keys (except stable slug/name identifiers)
- Redis cache keys
- Access token values

### 8.3 Import Security Rules

On import, the platform MUST:
1. Assign the current request's `tenant_id` to ALL imported resources — never trust tenant_id in payload
2. Validate all slug/name uniqueness within the target tenant's namespace (not globally)
3. Validate agent slugs exist in the target tenant's agent registry (or in the platform-global registry)
4. Validate orchestrator names exist in the target tenant's orchestrator registry
5. Return a conflict error if any slug/name collides within the tenant namespace

### 8.4 Template Application Definitions

Platform-provided template applications are a special case. They are associated with the bootstrap tenant or a dedicated `platform` tenant, and can be copied to any other tenant via the import mechanism. The copy operation must:
- Assign the target tenant_id to all copied resources
- Create new UUIDs for all records
- Preserve slug/name values from the template

---

## 9. Secrets Model

### 9.1 Current Secrets Architecture

Secrets in the platform are stored as:
- LLM provider API keys: in `llm_providers` table (platform-global)
- Agent credentials: in `agents` table (tenant-scoped)
- Access tokens: in `access_tokens` table (tenant-scoped, hashed)
- Per-application runtime secrets: not yet implemented

### 9.2 Tenant Isolation of Secrets

LLM provider credentials are platform-global. This means all tenants share the same LLM provider keys. In a multi-tenant SaaS model, this creates a billing attribution problem: LLM spend cannot be attributed to individual tenants without an LLM proxy layer.

**Wave 9 requirement:** The existing model is acceptable for Wave 9 (single tenant, self-hosted). Document clearly that LLM provider isolation is a Wave 10 prerequisite for multi-tenant SaaS.

### 9.3 Access Token Scoping

Access tokens carry `tenant_id`. The `BearerTenantMiddleware` validates that the token's tenant matches the JWT claim. This is correct.

One risk: if a token is created in Tenant A and somehow presented to a Tenant B endpoint, the middleware should reject it (tenant mismatch). Verify that this check is explicit in `BearerTenantMiddleware` — checking that `token.tenant_id == jwt.tenant_id` is not sufficient if the route itself accepts any valid token; the token's tenant_id must be checked against the route's tenant context.

### 9.4 Secrets for Agent-to-Platform Communication

A2A agents authenticate using bearer tokens. These tokens are tenant-scoped in the access_tokens table. Agent credentials must never be shared across tenants.

### 9.5 Environment-Level Secrets

The `secrets.local` / `generate-env.sh` mechanism for generating `.env` values is platform-level (not tenant-level). Per-tenant secrets (e.g., tenant-specific encryption keys, custom OAuth client IDs) are out of scope for Wave 9.

---

## 10. Redis / Cache Changes Needed

### 10.1 Keys That Must Change

The following Redis key patterns are globally namespaced but store tenant-scoped data. They MUST be updated before enabling multi-tenant runtime.

| Current Key Pattern | Problem | Target Key Pattern | Priority |
|---|---|---|---|
| `them:agents:registry` | Global; stores per-tenant agent list | `them:t:{tenant_id}:agents:registry` | Wave 9 |
| `them:orch:loc:{name}` | Global by name; same name across tenants collides | `them:t:{tenant_id}:orch:loc:{name}` | Wave 9 |
| `them:orch:tmpl:{name}` | Global by name; same name across tenants collides | `them:t:{tenant_id}:orch:tmpl:{name}` | Wave 9 |
| `them:app:{app_id}:orch:{name}` | app_id provides implicit tenant scoping — acceptable | No change needed (app_id is globally unique UUID) | — |
| `them:ep:{ep_slug}:sessions` | Global by slug — safe only while GLOBAL UNIQUE(slug) holds | `them:t:{tenant_id}:ep:{ep_slug}:sessions` in Wave 10 | Wave 10 |

### 10.2 Keys That Are Already Correct

| Key Pattern | Why It Is Correct |
|---|---|
| `them:app:{app_id}:sessions` | application UUID is globally unique — implicit tenant scoping |
| `them:sess:{session_id}` | session UUID is globally unique |
| `rl:them:app:{app_id}:{hour}` | app_id globally unique |
| `them:scan:state:{agent_id}` | agent_id globally unique |
| `them:dash:agent:{agent_id}` | agent_id globally unique |

### 10.3 Cache Invalidation Rules After Key Change

When the agent registry key changes to `them:t:{tenant_id}:agents:registry`:
- Agent CREATE, UPDATE, DELETE must invalidate `them:t:{tenant_id}:agents:registry` (not the old global key)
- There must be no code path that reads the old global key after the migration
- Old global key should be DEL'd during migration (or will expire naturally if TTL-based)

When orchestrator locator keys change:
- Orchestrator resolution code in the Go gateway and Python bridge must use tenant_id to construct the key
- The Temporal activity that resolves orchestrators must receive tenant_id as input to build the key

### 10.4 Cache Warming Strategy for Multi-Tenant

In a multi-tenant deployment, warming all tenant caches on startup is not feasible. The caches must be lazy-populated (read-through) per tenant. The current warm-on-startup pattern for the single-tenant agent registry must be changed to warm-on-first-access per tenant.

### 10.5 Redis Key Namespace Summary

After Wave 9 changes, the Redis key taxonomy should be:

```
Platform-global keys:
  them:config:{key}
  them:llm:{provider_id}:{...}

Tenant-scoped keys:
  them:t:{tenant_id}:agents:registry
  them:t:{tenant_id}:orch:loc:{name}
  them:t:{tenant_id}:orch:tmpl:{name}

Application-scoped keys (tenant implicit via app_id UUID):
  them:app:{app_id}:orch:{name}
  them:app:{app_id}:sessions
  rl:them:app:{app_id}:{hour}

Session-scoped keys:
  them:sess:{session_id}
  them:ep:{ep_slug}:sessions   ← move to tenant-scoped in Wave 10

Agent-scoped keys:
  them:scan:state:{agent_id}
  them:dash:agent:{agent_id}
```

---

## 11. Temporal Changes Needed

### 11.1 Current Workflow Input

The Temporal workflow is launched with orchestrator_name as the primary routing key. The workflow input struct (as inferred from Python bridge code) does not include `application_id` or `tenant_id`.

This is a critical gap. The Temporal worker that executes the workflow runs in a shared environment. Without tenant identity in the workflow input, the worker cannot enforce tenant isolation during execution.

### 11.2 Required Workflow Input Changes

The Temporal workflow input must be extended to include:

```python
@dataclass
class WorkflowInput:
    session_id: str
    orchestrator_name: str
    tenant_id: str           # ADD — required for all queries in activities
    application_id: str      # ADD — required for run attribution and app config lookup
    entry_point_id: str      # ADD — for EP config and billing attribution
    # ... existing fields
```

Go equivalent:
```go
type WorkflowInput struct {
    SessionID        string `json:"session_id"`
    OrchestratorName string `json:"orchestrator_name"`
    TenantID         string `json:"tenant_id"`
    ApplicationID    string `json:"application_id"`
    EntryPointID     string `json:"entry_point_id"`
    // ...
}
```

### 11.3 Required Activity Changes

Every Temporal activity that queries the database must be updated to:
1. Accept `tenant_id` and `application_id` from the workflow input
2. Include `tenant_id` (directly or via application join) in all WHERE clauses
3. Reject or error if tenant_id is empty

Specifically:
- `load_orchestrator_config` activity: add `WHERE app_orchestrators.tenant_id = $tenant_id AND name = $name`
- `load_agent_config` activity: add `WHERE agents.tenant_id = $tenant_id AND slug = $slug`
- `record_run` activity: include `tenant_id` and `application_id` in INSERT
- `load_entry_point_config` activity: verify `applications.tenant_id = $tenant_id`

### 11.4 Orchestrator Resolution Fix

The Python loader:
```sql
SELECT * FROM app_orchestrators WHERE name = $1 AND enabled = true
```

Must become:
```sql
SELECT ao.*
FROM app_orchestrators ao
JOIN applications a ON ao.application_id = a.id
WHERE ao.name = $1
  AND a.tenant_id = $2
  AND ao.enabled = true
```

Once `app_orchestrators.tenant_id` is added (Wave 9), this simplifies to:
```sql
SELECT * FROM app_orchestrators
WHERE name = $1 AND tenant_id = $2 AND enabled = true
```

### 11.5 Go WS Handler Fix (P-04)

The Go `/apps/{slug}/ws` handler must be corrected to resolve orchestrator name from the EP record rather than using ep_slug as orchestrator name:

```go
// WRONG (current):
OrchestratorName = epSlug

// CORRECT:
ep, err := dal.GetEntryPointBySlug(ctx, epSlug, tenantID)
if err != nil { ... }
appOrch, err := dal.GetAppOrchestratorByID(ctx, ep.AppOrchestratorID, tenantID)
if err != nil { ... }
OrchestratorName = appOrch.Name
```

### 11.6 Temporal Worker Restart Protocol

After any changes to `activities.py`, `workflows.py`, or shared workflow structs, the Temporal worker MUST be restarted:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-worker
docker logs them-worker --tail 5   # confirm "temporal_worker: polling"
```

The worker caches activity registrations at startup. Failing to restart means new parameters receive `None` at runtime — a silent data corruption bug.

---

## 12. 026 Compatibility Decision

### 12.1 Current State

- `db/026_tenant_foundation.sql` exists on disk
- `026_tenant_foundation` is NOT in `schema_migrations`
- The tenant work it describes was applied directly to the DB outside of migrations
- The live schema has diverged from what 026 expects

### 12.2 Step-by-Step Compatibility Analysis

| 026 Step | Operation | Live DB State | Expected Outcome | Safe? |
|---|---|---|---|---|
| 1 | `CREATE TABLE IF NOT EXISTS them.tenants` with `is_bootstrap` column | Table exists with `enabled` column (not `is_bootstrap`) | IF NOT EXISTS → SKIPPED | Safe (skipped) but schema mismatch remains |
| 2 | `INSERT` bootstrap tenant `ON CONFLICT DO NOTHING` | Row `00000000-0000-0000-0000-000000000001` exists | ON CONFLICT → no-op | Safe |
| 3a | `ADD COLUMN IF NOT EXISTS tenant_id` on `agents` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 3b | `ADD COLUMN IF NOT EXISTS tenant_id` on `orchestrators` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 3c | `ADD COLUMN IF NOT EXISTS tenant_id` on `access_tokens` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 3d | `ADD COLUMN IF NOT EXISTS tenant_id` on `applications` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 3e | `ADD COLUMN IF NOT EXISTS tenant_id` on `runs` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 3f | `ADD COLUMN IF NOT EXISTS tenant_id` on `audit_logs` | Column EXISTS NULLABLE | IF NOT EXISTS → SKIPPED | Safe |
| 3g | `ADD COLUMN IF NOT EXISTS tenant_id` on `app_orchestrators` | Column DOES NOT EXIST | ADD COLUMN runs | Safe — net new DDL |
| 4 | `UPDATE ... SET tenant_id = bootstrap` backfill | Existing rows in app_orchestrators have NULL (just added) | Backfill runs | Safe |
| 5 | DO block: validate no NULL tenant_id rows | `audit_logs` likely has NULL rows (pre-tenant records) | **VALIDATION FAILS** on audit_logs | **UNSAFE** |
| 6 | `ALTER TABLE audit_logs ALTER COLUMN tenant_id SET NOT NULL` | NULL rows exist | **ALTER TABLE FAILS** | **UNSAFE** |
| 7a | `DROP INDEX IF EXISTS agents_slug_key` | Index already dropped (uq_agents_tenant_slug exists) | IF EXISTS → SKIPPED | Safe |
| 7b | `CREATE UNIQUE INDEX IF NOT EXISTS uq_agents_tenant_slug` | Index EXISTS | IF NOT EXISTS → SKIPPED | Safe |
| 7c | `DROP INDEX IF EXISTS orchestrators_name_key` | Index already dropped | IF EXISTS → SKIPPED | Safe |
| 7d | `CREATE UNIQUE INDEX IF NOT EXISTS uq_orchestrators_tenant_name` | Index EXISTS | IF NOT EXISTS → SKIPPED | Safe |
| 7e | `DROP CONSTRAINT IF EXISTS app_orchestrators_name_key` | Constraint EXISTS | Drops constraint | Safe |
| 7f | `CREATE UNIQUE INDEX IF NOT EXISTS uq_app_orchestrators_tenant_name` | Index DOES NOT EXIST | Creates index | Safe — but only works after tenant_id column is added (step 3g) |
| 8 | Create various indexes with IF NOT EXISTS | Most already exist | No-ops for existing | Safe |
| 9 | `ADD COLUMN IF NOT EXISTS tenant_id` on `run_artifacts` | Column EXISTS NOT NULL | IF NOT EXISTS → SKIPPED | Safe |
| 10 | Validate run_artifacts tenant_id | All rows have bootstrap value | Passes | Safe |
| 11 | `INSERT INTO schema_migrations` ON CONFLICT DO NOTHING | Row does not exist | Inserts record | Safe |

### 12.3 Decision

**026 CANNOT be applied as-is.** It will fail at step 5 (DO block validation) due to NULL `audit_logs.tenant_id` rows.

### 12.4 Recommended Resolution

Do NOT attempt to run `026_tenant_foundation.sql` directly. Instead, create a replacement migration `027_tenant_cleanup.sql` that:

1. **Addresses only the gaps** — everything 026 would do that hasn't already been done:
   - `ADD COLUMN IF NOT EXISTS tenant_id` on `app_orchestrators` (the only net-new column)
   - `DROP CONSTRAINT IF EXISTS app_orchestrators_name_key`
   - `CREATE UNIQUE INDEX IF NOT EXISTS uq_app_orchestrators_tenant_name ON app_orchestrators(tenant_id, name)`
   - Backfill `app_orchestrators.tenant_id` to bootstrap UUID
   - Set `app_orchestrators.tenant_id NOT NULL`

2. **Handles `audit_logs` correctly** — not by forcing NOT NULL, but by:
   - `UPDATE them.audit_logs SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL`
   - Then optionally `ALTER TABLE them.audit_logs ALTER COLUMN tenant_id SET NOT NULL`
   - Only after confirming zero NULLs remain

3. **Marks BOTH 026 and 027 as applied** — to prevent any future attempt to run 026:
   ```sql
   INSERT INTO schema_migrations(version) VALUES ('026_tenant_foundation') ON CONFLICT DO NOTHING;
   INSERT INTO schema_migrations(version) VALUES ('027_tenant_cleanup') ON CONFLICT DO NOTHING;
   ```

4. **Reconciles the tenants table schema** — add `is_bootstrap` column if any code references it, or document that the column is `enabled` in the live schema.

### 12.5 Schema Divergence: `tenants.enabled` vs `tenants.is_bootstrap`

The live schema has `tenants.enabled` (boolean, controls whether the tenant is active).
The 026 file defines `tenants.is_bootstrap` (boolean, marks the special bootstrap row).

These are semantically different:
- `enabled` = can this tenant authenticate and use the platform?
- `is_bootstrap` = is this the special fallback tenant?

**Recommendation:** Keep `enabled`. Add `is_bootstrap boolean NOT NULL DEFAULT false` as a separate column in `027_tenant_cleanup.sql`. Set `is_bootstrap = true` on the `00000000-...` row. This allows code to query `WHERE is_bootstrap = true` to find the bootstrap tenant without hardcoding the UUID.

---

## 13. Revised Wave 9 DB Proposal

This section provides concrete DDL for the Wave 9 migration, replacing the flawed 026 approach.

### 13.1 New Migration File: `027_tenant_cleanup.sql`

```sql
-- 027_tenant_cleanup.sql
-- Resolves gaps left by out-of-band tenant work applied before 026.
-- Safe to run against current live schema (migration 025 applied, 026 NOT applied).
-- Created: 2026-08-16

BEGIN;

-- ── Step 1: Mark 026 as applied (it was applied out-of-band, partially) ──────
INSERT INTO schema_migrations(version)
VALUES ('026_tenant_foundation')
ON CONFLICT DO NOTHING;

-- ── Step 2: Add is_bootstrap to tenants table ─────────────────────────────────
ALTER TABLE them.tenants
    ADD COLUMN IF NOT EXISTS is_bootstrap BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE them.tenants
SET is_bootstrap = TRUE
WHERE id = '00000000-0000-0000-0000-000000000001';

-- ── Step 3: Add tenant_id to app_orchestrators ────────────────────────────────
ALTER TABLE them.app_orchestrators
    ADD COLUMN IF NOT EXISTS tenant_id UUID
        REFERENCES them.tenants(id)
        ON DELETE RESTRICT;

-- ── Step 4: Backfill app_orchestrators.tenant_id ──────────────────────────────
UPDATE them.app_orchestrators ao
SET tenant_id = a.tenant_id
FROM them.applications a
WHERE ao.application_id = a.id
  AND ao.tenant_id IS NULL;

-- Fallback: any remaining NULLs get bootstrap tenant
UPDATE them.app_orchestrators
SET tenant_id = '00000000-0000-0000-0000-000000000001'
WHERE tenant_id IS NULL;

-- ── Step 5: Enforce NOT NULL on app_orchestrators.tenant_id ──────────────────
ALTER TABLE them.app_orchestrators
    ALTER COLUMN tenant_id SET NOT NULL;

-- ── Step 6: Fix app_orchestrators unique constraints ──────────────────────────
ALTER TABLE them.app_orchestrators
    DROP CONSTRAINT IF EXISTS app_orchestrators_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_app_orchestrators_tenant_name
    ON them.app_orchestrators(tenant_id, name);

-- ── Step 7: Backfill audit_logs.tenant_id NULLs ──────────────────────────────
UPDATE them.audit_logs
SET tenant_id = '00000000-0000-0000-0000-000000000001'
WHERE tenant_id IS NULL;

-- ── Step 8: Optionally enforce NOT NULL on audit_logs.tenant_id ──────────────
-- Only uncomment after confirming zero NULLs in audit_logs:
-- ALTER TABLE them.audit_logs ALTER COLUMN tenant_id SET NOT NULL;
-- ALTER TABLE them.audit_logs
--     ADD CONSTRAINT audit_logs_tenant_id_fk
--     FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE RESTRICT;

-- ── Step 9: Add tenant_id FK to audit_logs (nullable FK is safe) ──────────────
-- Adding FK without NOT NULL: referential integrity without blocking NULL
ALTER TABLE them.audit_logs
    DROP CONSTRAINT IF EXISTS audit_logs_tenant_id_fk;

ALTER TABLE them.audit_logs
    ADD CONSTRAINT audit_logs_tenant_id_fk
    FOREIGN KEY (tenant_id) REFERENCES them.tenants(id)
    ON DELETE SET NULL;   -- soft: if tenant deleted, audit log stays with NULL

-- ── Step 10: Verify final state ───────────────────────────────────────────────
DO $$
DECLARE
    v_null_ao_count INTEGER;
    v_null_audit_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO v_null_ao_count
    FROM them.app_orchestrators WHERE tenant_id IS NULL;

    SELECT COUNT(*) INTO v_null_audit_count
    FROM them.audit_logs WHERE tenant_id IS NULL;

    IF v_null_ao_count > 0 THEN
        RAISE EXCEPTION '027: % app_orchestrators rows still have NULL tenant_id', v_null_ao_count;
    END IF;

    RAISE NOTICE '027: audit_logs NULL tenant_id rows: % (acceptable if FK allows NULL)', v_null_audit_count;
END$$;

-- ── Step 11: Record migration ─────────────────────────────────────────────────
INSERT INTO schema_migrations(version)
VALUES ('027_tenant_cleanup')
ON CONFLICT DO NOTHING;

COMMIT;
```

### 13.2 Application Layer Changes (Wave 9)

In addition to the DB migration, Wave 9 requires these application-layer changes:

#### Go DAL Changes

File: `go/internal/admin/dal/app_orchestrators.go`

Add `tenant_id` to all INSERT and SELECT queries:

```go
// GetAppOrchestratorByName — add tenant filter
func GetAppOrchestratorByName(ctx context.Context, db *pgxpool.Pool, name string, tenantID string) (*AppOrchestrator, error) {
    row := db.QueryRow(ctx,
        `SELECT id, tenant_id, application_id, name, node_id, enabled
         FROM them.app_orchestrators
         WHERE name = $1 AND tenant_id = $2 AND enabled = true`,
        name, tenantID,
    )
    // ...
}
```

#### Redis Key Changes

Files: `go/internal/cache/orchestrator.go` (or equivalent)

```go
// BEFORE:
key := fmt.Sprintf("them:orch:loc:%s", name)

// AFTER:
key := fmt.Sprintf("them:t:%s:orch:loc:%s", tenantID, name)
```

```go
// BEFORE:
key := "them:agents:registry"

// AFTER:
key := fmt.Sprintf("them:t:%s:agents:registry", tenantID)
```

#### Temporal Workflow Input

Files: `app/temporal/workflows.py`, `go/internal/temporal/client.go` (if applicable)

Add `tenant_id` and `application_id` to workflow input dataclass/struct (see Section 11.2).

#### Go WS Handler Fix

File: `go/internal/ws/handler.go` (or equivalent)

Fix orchestrator name resolution (see Section 11.5).

### 13.3 Post-Wave-9 Schema State

After `027_tenant_cleanup.sql` is applied, the DB state will be:

| Table | tenant_id | Constraint | Status |
|---|---|---|---|
| tenants | N/A (is the tenant table) | UNIQUE(slug) | OK + is_bootstrap column added |
| agents | NOT NULL, FK | UNIQUE(tenant_id, slug) | OK |
| orchestrators | NOT NULL, FK | UNIQUE(tenant_id, name) | OK |
| access_tokens | NOT NULL, FK | — | OK |
| applications | NOT NULL, FK | — | OK |
| runs | NOT NULL, FK | — | OK |
| run_artifacts | NOT NULL, FK | — | OK |
| app_orchestrators | NOT NULL, FK | UNIQUE(tenant_id, name) | FIXED by 027 |
| audit_logs | NULLABLE, FK (SET NULL on delete) | — | IMPROVED — NULL FK added |
| entry_points | NONE (Wave 10) | GLOBAL UNIQUE(slug) | DEFERRED |
| middleware_defs | NONE (Wave 10) | GLOBAL UNIQUE(slug) | DEFERRED |
| middleware_wirings | NONE (Wave 10) | via application_id | DEFERRED |

---

## 14. Security Risks — Ranked

### CRITICAL

#### SEC-01: Cross-Tenant Orchestrator Name Collision
**Risk:** Two tenants can have orchestrators with the same name. The Temporal loader selects by name with no tenant filter, returning the first match. This can cause Tenant A's session to execute Tenant B's orchestrator configuration.
**Impact:** Full cross-tenant execution — Tenant A's users run in Tenant B's orchestrator, potentially with Tenant B's agent access, LLM budgets, and run records.
**Mitigation:** Add `tenant_id` to `app_orchestrators`. Filter all orchestrator queries by `tenant_id`. Fix Temporal activity.
**Status:** UNMITIGATED. Must be fixed before enabling multi-tenant.

#### SEC-02: Redis Cache Cross-Tenant Poisoning (Orchestrator Locator)
**Risk:** `them:orch:loc:{name}` and `them:orch:tmpl:{name}` are globally keyed by orchestrator name. A cache write from one tenant's orchestrator resolution poisons the cache for any other tenant with the same orchestrator name.
**Impact:** Cross-tenant cache poisoning leading to incorrect orchestrator routing — same severity as SEC-01 but via the cache layer rather than the DB layer.
**Mitigation:** Change cache keys to include `tenant_id` prefix.
**Status:** UNMITIGATED. Must be fixed before enabling multi-tenant.

#### SEC-03: Agent Registry Cache Has No Tenant Isolation
**Risk:** `them:agents:registry` stores all agents globally. A tenant lookup hits the global cache, which may contain agents from other tenants.
**Impact:** Cross-tenant agent configuration exposure. A tenant could inadvertently invoke another tenant's agents if their registry cache is merged.
**Mitigation:** Change cache key to `them:t:{tenant_id}:agents:registry`.
**Status:** UNMITIGATED. Must be fixed before enabling multi-tenant.

#### SEC-04: Go WS Handler Passes EP Slug as Orchestrator Name
**Risk:** The Go `/apps/{slug}/ws` handler uses `ep_slug` as `OrchestratorName`. If EP slug and orchestrator name differ, the handler resolves the wrong orchestrator — potentially one belonging to a different tenant in a multi-tenant deployment.
**Impact:** Incorrect orchestrator routing; in multi-tenant context, potential cross-tenant execution.
**Mitigation:** Fix the Go handler to look up orchestrator name from the app_orchestrator record (see Section 11.5).
**Status:** UNMITIGATED. Bug exists today even in single-tenant context.

### HIGH

#### SEC-05: Temporal Workflow Has No Tenant Identity
**Risk:** Temporal workflow input contains no `tenant_id` or `application_id`. Temporal activities cannot independently verify they are operating on behalf of the correct tenant.
**Impact:** If orchestrator name resolution is bypassed or tampered with, the workflow has no secondary defense. All tenant isolation depends on the caller correctly setting up the workflow.
**Mitigation:** Add `tenant_id` and `application_id` to workflow input. Update all activities to filter by these values.
**Status:** UNMITIGATED. Deep fix required.

#### SEC-06: `audit_logs` Not Reliably Attributed to a Tenant
**Risk:** `audit_logs.tenant_id` is nullable. Security events (unauthorized access attempts, admin actions) may have no tenant attribution.
**Impact:** Compliance failure — cannot produce a per-tenant audit trail. In a security incident, cannot determine which tenant was affected.
**Mitigation:** Backfill NULL rows. Enforce non-null at application layer for all new writes. Add FK.
**Status:** PARTIALLY MITIGATED (027 adds FK with SET NULL). Application-layer enforcement is still needed.

#### SEC-07: Bearer Token Tenant Not Verified Against EP Tenant
**Risk:** A bearer token for Tenant A could be presented to an endpoint that serves Tenant B applications. If the middleware only checks that the token is valid (not that its tenant matches the application's tenant), cross-tenant API calls succeed.
**Impact:** Tenant A user accesses Tenant B application resources.
**Mitigation:** WS/SSE handlers must verify `token.tenant_id == application.tenant_id` explicitly, not just that the token is valid.
**Status:** NEEDS VERIFICATION. The middleware structure looks correct but the handler-level check requires code audit.

#### SEC-08: Super Admin Can Only Access Bootstrap Tenant
**Risk:** `AdminTenantMiddleware` assigns bootstrap tenant to all super_admin JWT tokens. A super_admin cannot manage non-bootstrap tenant resources without a tenant-specific JWT.
**Impact:** Operations risk — super_admin cannot diagnose or manage non-bootstrap tenants. Could result in admin bypassing the intended tenant model by directly accessing the DB.
**Mitigation:** Add a tenant impersonation mechanism for super_admin (admin-scoped, audit-logged).
**Status:** ACCEPTED RISK for Wave 9 (single tenant). Must be addressed before Wave 10.

### MEDIUM

#### SEC-09: `entry_points` GLOBAL UNIQUE(slug) Is a Tenant Namespace Conflict
**Risk:** Two tenants cannot have entry points with the same slug. Tenant A registering `customer-support` blocks Tenant B from using the same name.
**Impact:** Tenant namespace collision; first-mover advantage on slug names.
**Mitigation:** Change to `UNIQUE(tenant_id, slug)`. Requires URL routing changes (Wave 10).
**Status:** ACCEPTED RISK for Wave 9. Document clearly.

#### SEC-10: EPConfig Cache Not Tenant-Keyed
**Risk:** EPConfig cache key is ep_slug only. Safe while GLOBAL UNIQUE(slug) holds but is a latent cross-tenant leak vector.
**Impact:** If global slug uniqueness is relaxed without updating the cache key, cross-tenant EPConfig leaks.
**Mitigation:** Change cache key to include tenant_id when GLOBAL UNIQUE(slug) is relaxed.
**Status:** DEFERRED to Wave 10. Safe today.

#### SEC-11: `middleware_defs` GLOBAL UNIQUE(slug) Is a Tenant Namespace Conflict
**Risk:** Same issue as SEC-09 but for middleware definitions.
**Impact:** Tenant namespace conflict on middleware names.
**Mitigation:** Wave 10 — add tenant_id to middleware_defs.
**Status:** ACCEPTED RISK for Wave 9.

#### SEC-12: LLM Provider Credentials Are Shared Across Tenants
**Risk:** All tenants use the same LLM provider API keys. One tenant's excessive usage can exhaust rate limits or quotas affecting other tenants.
**Impact:** Availability degradation for other tenants; billing attribution is impossible.
**Mitigation:** Per-tenant LLM provider configuration or LLM proxy with per-tenant rate limiting. Wave 10.
**Status:** ACCEPTED RISK for Wave 9 (single active tenant). Not a multi-tenant SaaS blocker until Wave 10.

### LOW

#### SEC-13: `app_orchestrators` Name Backfill Uses application_id Join
**Risk:** The 027 backfill uses `UPDATE ao SET tenant_id = a.tenant_id FROM applications a WHERE ao.application_id = a.id`. If any `app_orchestrators` row has a NULL `application_id`, it falls through to the bootstrap fallback.
**Impact:** Orphaned app_orchestrators rows assigned to bootstrap tenant instead of their actual tenant.
**Mitigation:** Validate no NULL `application_id` rows exist before running 027. Add NOT NULL constraint to `app_orchestrators.application_id` if not already present.
**Status:** LOW — verify before running 027.

#### SEC-14: `026_tenant_foundation.sql` Left on Disk but Unapplied
**Risk:** A future operator might attempt to run 026 directly, causing audit_logs to fail and potentially partially applying the migration.
**Impact:** Partial migration leaving the DB in an inconsistent state.
**Mitigation:** The 027 migration marks 026 as applied in schema_migrations, preventing re-application. Additionally, add a comment to 026 noting it was superseded by 027.
**Status:** MITIGATED by 027 approach.

---

## 15. Exact Implementation Order

The following is the precise implementation sequence for Wave 9 tenant hardening. Each step has a clear definition of done.

### Phase 1 — DB Migration (Zero Downtime)

**Step 1.1:** Verify prerequisites
```bash
# Confirm 026 is NOT in schema_migrations
docker exec -it them-postgres psql -U them -d them -c \
  "SELECT version FROM schema_migrations WHERE version LIKE '%026%';"
# Expected: 0 rows

# Confirm app_orchestrators has no tenant_id column yet
docker exec -it them-postgres psql -U them -d them -c \
  "\d them.app_orchestrators"

# Confirm audit_logs NULL count
docker exec -it them-postgres psql -U them -d them -c \
  "SELECT COUNT(*) FROM them.audit_logs WHERE tenant_id IS NULL;"
```

**Step 1.2:** Create `db/027_tenant_cleanup.sql` with the DDL from Section 13.1

**Step 1.3:** Apply migration in a transaction
```bash
docker cp db/027_tenant_cleanup.sql them-postgres:/tmp/027_tenant_cleanup.sql
docker exec them-postgres psql -U them -d them -f /tmp/027_tenant_cleanup.sql
```

**Step 1.4:** Verify migration
```bash
docker exec -it them-postgres psql -U them -d them -c \
  "SELECT version FROM schema_migrations ORDER BY applied_at DESC LIMIT 5;"
docker exec -it them-postgres psql -U them -d them -c \
  "\d them.app_orchestrators"
# Verify: tenant_id column present, NOT NULL, FK to tenants
```

**Definition of Done:** `027_tenant_cleanup` in schema_migrations, `app_orchestrators.tenant_id` NOT NULL, `uq_app_orchestrators_tenant_name` index exists.

---

### Phase 2 — Redis Key Migration (Requires Code Deploy)

**Step 2.1:** Update agent registry cache key
- File: Go cache package (wherever `them:agents:registry` is written/read)
- Change key pattern to `them:t:{tenant_id}:agents:registry`
- Ensure cache population passes tenantID parameter
- Ensure cache invalidation uses tenantID

**Step 2.2:** Update orchestrator locator cache keys
- File: Go/Python orchestrator resolution code
- Change `them:orch:loc:{name}` to `them:t:{tenant_id}:orch:loc:{name}`
- Change `them:orch:tmpl:{name}` to `them:t:{tenant_id}:orch:tmpl:{name}`

**Step 2.3:** Flush stale global cache entries after deploy
```bash
docker exec -it them-redis redis-cli KEYS "them:agents:registry" | xargs -r docker exec them-redis redis-cli DEL
docker exec -it them-redis redis-cli KEYS "them:orch:loc:*" | xargs -r docker exec them-redis redis-cli DEL
docker exec -it them-redis redis-cli KEYS "them:orch:tmpl:*" | xargs -r docker exec them-redis redis-cli DEL
```

**Definition of Done:** No stale global orchestrator/agent cache keys. All new cache writes use tenant-prefixed keys.

---

### Phase 3 — Temporal Workflow Input Fix (Requires Worker Restart)

**Step 3.1:** Update workflow input struct/dataclass to include `tenant_id`, `application_id`, `entry_point_id`

**Step 3.2:** Update all callers (WS handler, SSE handler, bridge client) to populate these fields from the authenticated request context

**Step 3.3:** Update Temporal activities:
- `load_orchestrator_config`: add `tenant_id` filter
- `load_agent_config`: add `tenant_id` filter
- `record_run`: include `tenant_id` and `application_id`
- `load_entry_point_config`: verify tenant match

**Step 3.4:** Restart Temporal worker (MANDATORY after activity changes)
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-worker
docker logs them-worker --tail 10
```

**Definition of Done:** Worker logs show "temporal_worker: polling". Workflow input for new sessions includes tenant_id and application_id. Activities filter by tenant_id.

---

### Phase 4 — Go Handler Fix (P-04)

**Step 4.1:** Fix `/apps/{slug}/ws` handler orchestrator name resolution
- Look up EP record by slug + tenantID
- Retrieve `app_orchestrator_id` from EP record
- Look up `app_orchestrators` by ID + tenantID
- Use `app_orchestrators.name` as `OrchestratorName`

**Step 4.2:** Write/update test: verify WS handler uses app_orchestrator.name, not ep_slug

**Definition of Done:** Integration test passes for case where ep_slug ≠ orchestrator name. Handler correctly routes to the orchestrator wired in the DB.

---

### Phase 5 — Python Orchestrator Resolution Fix

**Step 5.1:** Update `load_orchestrator_row` in Python to filter by tenant_id:
```python
# Add tenant_id parameter and include in WHERE clause
WHERE name = $1 AND tenant_id = $2 AND enabled = true
```

**Step 5.2:** Propagate tenant_id from request context through the Python bridge call chain

**Definition of Done:** Python orchestrator resolution query includes tenant_id filter. Cross-tenant name collision returns not-found instead of wrong orchestrator.

---

### Phase 6 — Audit Logging Hardening

**Step 6.1:** Audit all `audit_logs` INSERT paths in Go and Python

**Step 6.2:** Add non-null tenant_id enforcement at the application layer for all new audit log writes:
```python
# Python: enforce non-null before INSERT
assert tenant_id is not None and tenant_id != "", "audit_logs requires tenant_id"
```

```go
// Go: enforce non-null before INSERT
if tenantID == "" {
    return fmt.Errorf("audit_logs: tenant_id is required")
}
```

**Step 6.3:** After verifying all NULL rows have been backfilled (by 027 step 7), uncomment the NOT NULL constraint in 027 or create 028:
```sql
ALTER TABLE them.audit_logs ALTER COLUMN tenant_id SET NOT NULL;
```

**Definition of Done:** No new NULL tenant_id rows in audit_logs. Application layer rejects writes with empty tenant_id.

---

### Phase 7 — Testing

**Step 7.1:** Run Python test suite:
```bash
python3.12 scripts/tests/run_tests.py
```
Expected: zero new failures.

**Step 7.2:** Run Go test suite:
```bash
cd /opt/docker/them/go && go test ./...
```

**Step 7.3:** Run integration test for cross-tenant isolation:
- Create two test sessions with different application configurations
- Verify session A uses orchestrator A's config
- Verify session B uses orchestrator B's config
- Verify neither session can access the other's run records

**Step 7.4:** Run compose health check:
```bash
python3.12 scripts/tests/run_tests.py 15
```

**Definition of Done:** All existing tests pass. New cross-tenant isolation tests pass. Compose is healthy.

---

### Phase 8 — Documentation Updates

**Step 8.1:** Update `docs/architecture-v2/CURRENT.md` with Wave 9 status

**Step 8.2:** Update `docs/REDIS.md` with new tenant-prefixed key patterns

**Step 8.3:** Update `docs/SCHEMA.md` with `app_orchestrators.tenant_id` column and `tenants.is_bootstrap`

**Step 8.4:** Update `go/TEST_INDEX.md` with any new Go tests added

**Step 8.5:** Update `scripts/tests/INDEX.md` with any new Python tests added

---

## 16. Safe to Implement Wave 9 — Conclusion

### Original Verdict (review date: 2026-08-15): **NO**

**Updated Verdict (implementation date: 2026-08-16): YES — Wave 9 pre-requisites complete**

---

### What Changed Since the Review

The review identified five blockers: P-08, SEC-01, SEC-02, SEC-03, SEC-04.

**Architecture Decision (2026-08-16): Python is permanently retired.**

`them-bridge` and `them-worker` are legacy-only containers that must remain OFF. This decision has irreversible implications for the blocker list:

#### SEC-01 — LEGACY PYTHON PATH — RETIRED (not an implementation blocker)

**Original finding:** Python `loaders.py` resolved orchestrators globally by name with no tenant filter.

**Resolution:** Python worker is permanently retired. `them-worker` must remain OFF. This code path will never execute again. No Python patches required or permitted. The Go Temporal worker, when built, MUST resolve orchestrators by `app_orchestrator_id` UUID (from `WorkflowInput.AppOrchestratorID`), never by name globally.

Status: **DOCUMENTED DEFERRED — DEAD PATH. NOT AN ACTIVE BLOCKER.**

---

#### SEC-02 — LEGACY PYTHON PATH — RETIRED (not an implementation blocker)

**Original finding:** Python `loaders.py` used globally-namespaced Redis keys `them:orch:loc:{name}` and `them:orch:tmpl:{name}` with no tenant prefix.

**Resolution:** Python worker is permanently retired. These Redis keys are dead. No new code writes to them. No patches required or permitted.

Status: **DOCUMENTED DEFERRED — DEAD PATH. NOT AN ACTIVE BLOCKER.**

---

#### SEC-03 — IMPLEMENTED ✓

**Original finding:** Go agent registry used global key `them:agents:registry` — all tenants shared one cache slot.

**Implementation:**
- Redis key changed from `them:agents:registry` → `them:agents:registry:{tenant_id}`
- L1 in-process key changed from `slug` → `"{tenantID}:{slug}"`
- `DBReader.QueryAgentsByTenant(ctx, tenantID)` — SQL filters by `tenant_id`
- `Invoke(ctx, tenantID, slug, input)` — tenantID always from server context, never client payload
- Pub/sub invalidation: payload = tenantID UUID; empty payload = no-op (no global eviction possible)
- 9 new tenant-isolation tests verify cross-tenant cache non-contamination

Affected files: `go/internal/agentregistry/registry.go`, `pgx_querier.go`, `registry_test.go`, `go/internal/admin/service/agents.go`, `go/internal/orchestrator/orchestrator.go`, `go/internal/temporal/activities.go`

Status: **COMPLETE**

---

#### SEC-04 — IMPLEMENTED ✓

**Original finding:** Go WS/SSE/A2A handlers used EP slug as `OrchestratorName` in `WorkflowInput`. Correct resolution requires a JOIN to `app_orchestrators`.

**Implementation:**
- `EPConfig` now carries `AppOrchestratorID` and `OrchestratorName` (loaded via LEFT JOIN `app_orchestrators ON ao.id = ep.app_orchestrator_id AND ao.application_id = ep.application_id`)
- NULL binding is a hard error — WS/SSE/A2A return 503 "entry point has no orchestrator configured" rather than silently routing to wrong orchestrator
- `WorkflowInput.AppOrchestratorID` added for Go Temporal worker (will use UUID for resolution, not name)
- All three transport handlers (WS, SSE, A2A) updated
- `execution.Lifecycle` updated to use `resolvedCfg.OrchestratorName`

Affected files: `go/internal/epconfig/epconfig.go`, `pgx.go`, `go/internal/ws/handler.go`, `go/internal/sse/handler.go`, `go/internal/a2a/server.go`, `go/internal/execution/lifecycle.go`, `go/internal/temporal/workflow.go`

Status: **COMPLETE**

---

#### P-08 — IMPLEMENTED ✓

**Original finding:** `app_orchestrators` had `UNIQUE(name)` — a global uniqueness constraint that prevented two tenants from having an orchestrator with the same name.

**Implementation:**
- Migration `db/027_app_orchestrators_uniqueness.sql` — drops `app_orchestrators_name_key`, adds `uq_app_orchestrators_app_name UNIQUE(application_id, name)`
- Applied to live DB and verified: same-name orchestrators in different applications succeed; duplicate in same application blocked
- Idempotent (IF NOT EXISTS / DROP CONSTRAINT IF EXISTS)

Status: **COMPLETE**

---

### Final Verdicts

| Finding | Status | Notes |
|---|---|---|
| P-08 | ✓ COMPLETE | `UNIQUE(application_id, name)` in place, migration applied |
| SEC-01 | RETIRED | Legacy Python path — permanently retired, will not be reactivated |
| SEC-02 | RETIRED | Legacy Python path — permanently retired, will not be reactivated |
| SEC-03 | ✓ COMPLETE | Per-tenant Redis key + L1 key + SQL filter + 9 isolation tests |
| SEC-04 | ✓ COMPLETE | EPConfig resolves OrchestratorName via JOIN; NULL = hard error |

**SAFE TO DEVELOP WAVE 9: YES**

**SAFE FOR GO-ONLY MULTI-TENANT EXECUTION TODAY: YES** — subject to the following permanent constraint:

> **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID (from `WorkflowInput.AppOrchestratorID`), never by name globally. `WorkflowInput.OrchestratorName` is for reference only — the authoritative field is `AppOrchestratorID`.**

### Remaining Wave 10 Gaps (do not block Wave 9)

- `entry_points.tenant_id` and `UNIQUE(tenant_id, slug)` — Wave 10 (breaking URL change)
- `middleware_defs` tenant scoping — Wave 10
- LLM provider per-tenant isolation — Wave 10
- Super admin tenant impersonation — Wave 10
- `middleware_wirings` direct tenant_id — Wave 10

---

*End of R6 Tenant Architecture Review*
*Original review SHA: ca29acd — 2026-08-15*
*Implementation complete SHA: see CURRENT.md — 2026-08-16*
