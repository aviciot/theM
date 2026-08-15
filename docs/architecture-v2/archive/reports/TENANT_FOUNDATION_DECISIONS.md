# Tenant Foundation Decisions
# Date: 2026-07-26
# Status: DECISIONS MADE — Wave 7 Phase 3 may proceed with constraints

---

## Background and Purpose

The-M is evolving from a single-operator platform toward a multi-tenant SaaS. Before the Go
migration continues beyond Wave 7 Phase 2, the ownership model for each resource class must be
decided. Without these decisions, handlers built in Phase 3 may encode assumptions that require
destructive schema and API changes to fix later.

This document records the decisions, classifies every resource, and states the exact impact on
the current Wave 7 work.

---

## 1. Tenant Identity

### 1.1 What uniquely identifies a tenant

A tenant is a single organizational customer of the-M platform. The tenant boundary is defined
by a **Tenant record** (to be created; does not yet exist in the DB schema). Until that record
exists, the platform operates as a single implicit tenant — all resources belong to the one
operator instance.

**Decision:** The tenant identifier is a UUID (`tenant_id`). A new `them.tenants` table will be
introduced in a dedicated pre-migration wave before any tenant-scoped resource column is added.
No tenant column is added to any existing table until that wave is complete.

### 1.2 How tenant context is resolved

Tenant context resolution follows a strict priority chain. Context is **never** trusted solely
because it was supplied in a request field or header.

**Platform Admin portal:**
1. JWT claim `tenant_id` (scoped admin credential) — OR —
2. Explicit tenant switch using a privileged action (requires `role=platform_admin` claim).
   The switch logs to audit. No tenant selector is exposed to non-platform-admin users.

**Tenant Portal:**
1. JWT claim `tenant_id` — fixed at login; cannot be changed without re-authentication.
2. No `?tenant_id=` query parameter. No `X-Tenant-ID` header. No body field.
3. If the JWT has no `tenant_id` claim, access is denied.

**Runtime APIs (WS, SSE, A2A, WebRTC):**
1. Bearer token → `access_tokens.tenant_id` (tenant stored when token was issued).
2. Entry point slug → `entry_points` → `applications.tenant_id`.
3. Domain-based routing (future) → DNS-verified tenant mapping.
4. Any other source is untrusted and must not be used.

**Resolution rule:** the tenant_id derived from the authenticated identity (token or JWT) takes
precedence over any tenant identifier in the request payload. A mismatch is a 403, not a 404.

### 1.3 User multi-tenancy

**Decision:** Users belong to exactly one tenant. Cross-tenant access is not supported in the
initial multi-tenant design. Platform Admins are a special class outside tenant scoping.

A Platform Admin user has `role=platform_admin` in their JWT. They may operate in any tenant
context but must explicitly select or be granted a tenant context via a privileged operation.
Platform Admin actions that touch a specific tenant are recorded in the platform-level audit log
with both `platform_admin_id` and `tenant_id`.

### 1.4 Who may switch tenant context

Only Platform Admins with `role=platform_admin` may switch tenant context. Tenant Portal users
cannot switch tenants. Session tokens, access tokens, and run records are always fixed to the
tenant at creation time and cannot be re-scoped.

---

## 2. Resource Ownership Classification

### 2.1 Platform-global resources

These exist once across the entire platform. No `tenant_id` column. Managed by Platform Admins
only through the Platform Admin control plane.

| Resource | Table | Rationale |
|---|---|---|
| **LLM providers** | `them.llm_providers` | See §3 — platform-global by default, with planned tenant override path |
| **LLM routing config** | `them.config['llm_routing']` | Same as LLM providers — see §4 |
| **Monitoring config** | `them.config['monitoring']` | Platform-wide observability settings |
| **Middleware definitions** | `them.middleware_defs` | Shared reusable middleware catalog |
| Platform-level audit log | (future `them.platform_audit`) | Cross-tenant admin actions |

### 2.2 Tenant-owned resources

These belong to one tenant. All queries must include `WHERE tenant_id = $tenant_id`. Creating
a resource for a different tenant is a security violation, not a data error.

| Resource | Current table | `tenant_id` present? | Note |
|---|---|---|---|
| **Applications** | `them.applications` | No (to be added) | Core tenant boundary |
| **App orchestrators** | `them.app_orchestrators` | No (via `application_id`) | Inherits from application |
| **Entry points** | `them.entry_points` | No (via `application_id`) | Inherits from application |
| **Middleware wirings** | `them.middleware_wirings` | No (via `application_id`) | Inherits from application |
| **Shared orchestrators** | `them.orchestrators` | No (to be added) | Currently platform-global; intended as tenant-owned templates |
| **Shared agents** | `them.agents` | No (to be added) | Currently platform-global; intended as tenant-owned |
| **Access tokens** | `them.access_tokens` | No (to be added) | Token issuance is tenant-scoped |
| **Runs and steps** | `them.runs`, `them.run_steps` | No (via `orchestrator_id`) | Must be queryable by tenant |
| **Run usage** | `them.run_usage` | No (via `run_id`) | Billing is per-tenant |
| **Tasks and artifacts** | `them.tasks`, `them.artifacts` | No | Owned via run chain |
| **Users and roles** | `auth_service.*` | Depends on auth service design | Managed by auth service |
| Tenant-specific audit log | `them.audit_logs` | No (to be added) | Row-level tenant filter |

**Note on current state:** No `tenant_id` column exists in any of these tables today. Until
the tenants migration wave runs, the platform operates as a single-tenant instance. All existing
rows implicitly belong to tenant "default". The migration wave will backfill `tenant_id` on all
rows.

### 2.3 Application-owned resources

These are owned by a specific Application within a tenant. The tenant boundary is enforced
through the `application_id → applications.tenant_id` chain.

| Resource | Ownership chain |
|---|---|
| Entry points | `entry_points.application_id → applications.tenant_id` |
| App orchestrators | `app_orchestrators.application_id → applications.tenant_id` |
| Middleware wirings | `middleware_wirings.application_id → applications.tenant_id` |
| Sessions (Redis) | `them:session:{id}` → EP slug → `entry_points.application_id` |
| Temporal workflows | workflow ID encodes `runID`; run references `orchestrator_id` |

### 2.4 User/session-owned resources

| Resource | Owner | Scope |
|---|---|---|
| Sessions | User (bearer token or anonymous) + EP | Within one entry point |
| Runs | Initiated by session | Within one application |
| Run steps, task messages | Owned by run | Transient operational data |
| JWT (user session) | User | Expires; not stored in `them` schema |
| Redis session state | Session ID | Cleared on session end |

### 2.5 Global defaults with tenant override

These resources have platform-wide defaults that a tenant may override. The override path is
not yet implemented in the schema; the structure is defined here for design consistency.

| Resource | Platform default | Tenant override (planned) |
|---|---|---|
| LLM providers | `them.llm_providers` rows | `them.tenant_llm_providers` (future) |
| LLM routing config | `them.config['llm_routing']` | `them.config['llm_routing:{tenant_id}']` or separate table |
| Monitoring config | `them.config['monitoring']` | Per-tenant monitoring thresholds (future) |
| Rate limits | Config values | `applications.runtime_config.rate_limit_rpm` (already per-app) |

---

## 3. Wave 7 Impact — LLM Provider Ownership

### 3.1 Decision

**LLM providers are platform-global resources with a planned tenant override path.**

This means:
- The `them.llm_providers` table has no `tenant_id` column.
- All tenants share the same set of configured LLM providers.
- The Platform Admin configures providers once; they are available to all tenants.
- In the future, a tenant override table (`them.tenant_llm_providers`) may allow a tenant to
  supply their own API key for a given provider, or to restrict which providers their
  applications may use.
- For now: no `tenant_id` column is needed in `them.llm_providers`.

### 3.2 Rationale

Provider configuration (name, display_name, default_model, model_pricing, base_url, enabled)
is a platform infrastructure concern. The API key stored with a provider is the platform's
service credential for that LLM service. It is not a per-customer credential.

The current runtime path confirms this: `loaders.py:build_provider()` selects the provider by
name only — there is no tenant filter on `them.llm_providers`. The `load_model_pricing` query
also selects by `name` only. Introducing a tenant filter here without a migration plan would
break the runtime.

Per-tenant LLM API keys are already handled at the orchestrator level via
`app_orchestrators.llm_api_key_encrypted` — if a tenant wants to use their own Anthropic key
for a specific orchestrator, they set it there. The platform-level `llm_providers` row provides
the default fallback.

### 3.3 Uniqueness

The `UNIQUE(name)` constraint on `them.llm_providers.name` remains correct for a platform-global
table. If tenant-owned providers are added in a future wave, the uniqueness scope changes to
`UNIQUE(tenant_id, name)` on the override table, not on the platform table.

### 3.4 Authorization

LLM provider CRUD (list/get/create/update/delete) is a Platform Admin operation. It is mounted
under `/api/v1/admin/llm-providers` which requires `RequireSuperAdmin` (JWT `role=superadmin`).
No tenant-scoping needed in Wave 7.

### 3.5 Encryption ownership

`THE_M_SECRET_KEY` is the platform-level encryption root. All `api_key_encrypted` values in
`them.llm_providers` are encrypted with this key. This is correct for a platform-global table.
If per-tenant keys were needed, each tenant would require a separate derived key — a significant
key-management change outside Wave 7 scope.

### 3.6 UI

The LLM providers admin UI belongs in the **Platform Admin portal**, not the Tenant Portal.
It manages platform-wide LLM configuration and must not be visible to tenant administrators.

### 3.7 Runtime routing

The LLM routing config (`them.config['llm_routing']`) selects which provider is used by default
for orchestration. This is a platform default. Per-application routing is handled by
`app_orchestrators.llm_provider`. No tenant-scope changes needed for Wave 7.

### 3.8 Impact on Phase 2 implementation

**The existing Phase 2 implementation requires no changes.**

The DAL (`go/internal/admin/dal/llm_providers.go`) operates on the platform-global table with
no `tenant_id` column — correct.

The service layer (`go/internal/admin/service/llm_providers.go`) has no tenant parameter in any
method signature — correct for a platform-global resource.

The `UNIQUE(name)` constraint enforced by `dal.IsUniqueViolation` is correct for the
platform-global ownership model.

The encryption scheme (single `THE_M_SECRET_KEY` → Fernet) is correct for platform-global
credentials.

**Phase 2 may remain exactly as committed. No rework required.**

---

## 4. Wave 6 Impact — Routing Config and Monitoring Config

### 4.1 LLM routing config

**Decision: platform-global with a planned per-application override path.**

The current `them.config['llm_routing']` row holds the platform default: which provider name
and model are used when an orchestrator does not specify its own. This is a Platform Admin
concern.

**Current Go routes may remain temporarily global.** The `GET/PUT /api/v1/admin/llm-providers/routing/config`
routes (Wave 6, live) operate on this single platform row. This is correct today.

**What must change before multi-tenant production:**
- Add a per-application LLM routing override so tenant applications can choose a different
  default provider without Platform Admin intervention.
- The runtime path must check `app_orchestrators.llm_provider` (already done) before falling
  back to the platform routing config (already done via `loaders.py` fallback chain).
- No schema change to `them.config` is needed for the platform default row.
- The admin route itself does not need a tenant scope — it is Platform Admin only.

**No code changes required before Wave 7 Phase 3.**

### 4.2 Monitoring config

**Decision: platform-global for operational thresholds; per-application for session heatmaps.**

The `them.config['monitoring']` row holds platform-wide operational thresholds (heatmap
low/medium/high, stats window). This is an operator concern — a Platform Admin configures it
once for the whole platform.

Per-application session heatmap display (already in `SessionsView`) reads these global
thresholds and applies them to application-specific session counts. This design is correct:
the thresholds are platform policy, the data is application-scoped.

**Current Go route may remain temporarily global.**
`GET/PUT /api/v1/admin/monitoring-config` (Wave 6, live) operates on the platform row.

**What must change before multi-tenant production:**
- If different tenants need different monitoring thresholds, introduce
  `them.config['monitoring:{tenant_id}']` with fallback to `them.config['monitoring']`.
- The admin route would then require tenant context from the JWT.
- This is a future wave concern. Not required for Wave 7.

**No code changes required before Wave 7 Phase 3.**

---

## 5. Tenant Context Propagation

The following chain defines how tenant context must flow through every layer once multi-tenancy
is active. This is a planning specification, not a current implementation requirement.

```
Authentication (JWT or bearer token)
  ↓ jwt.Claims.TenantID  OR  access_tokens.tenant_id
Request middleware
  ↓ ctx.Value(tenantKey) — injected by auth middleware; never from request body/header
Application
  ↓ applications.tenant_id (verified against ctx tenant)
Session
  ↓ SessionInfo.TenantID (stored at session creation; not mutable)
Run
  ↓ runs.tenant_id (set at run creation; carried in PythonOrchestrationInput or native run)
Agent invocation
  ↓ task context carries tenant_id; A2A calls include tenant_id in metadata (future)
Tool / LLM call
  ↓ API key resolved from app_orchestrators (tenant's own key) or llm_providers (platform key)
  ↓ No tenant_id in the LLM API call itself — it uses whatever key was resolved
Logs / Audit
  ↓ slog fields always include tenant_id from ctx; audit_logs.tenant_id column
```

**Enforcement points:**
1. Auth middleware: resolves tenant from JWT/token; rejects mismatched or absent tenant.
2. DAL layer: every query on a tenant-scoped table includes `WHERE tenant_id = $tenant`.
   The DAL function signature must include `tenantID string` (or a tenant context) for
   tenant-scoped resources.
3. Gate/session: `SessionInfo` struct gains a `TenantID` field.
4. Run recorder: `CreateRun` gains a `tenant_id` parameter.
5. Audit: every mutation through the service layer emits an audit row with `tenant_id`.

**Platform-global resources (LLM providers, routing config, monitoring config) do NOT carry
tenant_id in their queries.** They are outside the propagation chain.

---

## 6. Isolation Foundation Tiers

These tiers define the isolation architecture the platform must support. Only the foundation
(logical shared isolation) is required for Wave 7. Higher tiers are post-Wave-7 planning.

### Tier 0 — Logical shared isolation (current + near-term)

Single PostgreSQL instance, single Redis, single Go binary, single Python worker pool.
Row-level isolation enforced by `tenant_id` filters on every query.
Rate limiting is per-application already (via `applications.runtime_config`).

**Required additions for Tier 0 multi-tenancy:**
- `tenant_id` column on: `applications`, `agents`, `orchestrators`, `access_tokens`, `runs`,
  `audit_logs`. Backfill existing rows with a `default` tenant UUID.
- Auth service: `users` table gains `tenant_id`; `teams` gains `tenant_id`.
- DAL: all admin queries gain `tenantID` parameter and `WHERE tenant_id = $n` clause.
- Middleware: JWT/token middleware resolves and validates tenant on every request.

### Tier 1 — Dedicated worker pools (future)

Per-tenant Temporal task queues. Worker configuration determines which task queue a tenant's
workflows execute on. Provides latency isolation and fair scheduling.

**Foundation required before Tier 1:**
- `runs.task_queue_name` column (or derived from tenant config).
- Temporal worker provisioning per tenant.

### Tier 2 — Dedicated Redis namespace (future)

Per-tenant Redis key prefix (`them:{tenant_id}:` instead of `them:`). Or separate Redis
database index. Provides memory isolation and per-tenant pub/sub namespacing.

**Foundation required before Tier 2:**
- All Redis key construction must go through a tenant-aware key builder.
- The `them:session:*`, `them:agents:*`, `them:orchestrators:*`, `them:bridge:*`, `them:dash:*`
  key prefixes documented in `docs/REDIS.md` all gain a `{tenant_id}:` segment.

### Tier 3 — Dedicated database schema/database (future)

Separate PostgreSQL schema or separate database instance per tenant. Provides the strongest
data isolation. Requires connection pooling aware of tenant routing.

### Tier 4 — Fully dedicated deployment (future)

Separate Docker/Kubernetes stack per tenant. No shared infrastructure.

### Tenant quotas and fairness

The existing per-application runtime controls (`runtime_config.rate_limit_rpm`,
`runtime_config.max_concurrent_sessions`, EP `queue_timeout_seconds`) already provide the
building blocks for Tier 0 quota enforcement. A tenant-level quota layer above these would:
- Cap total concurrent sessions across all applications owned by the tenant.
- Cap total runs per minute for the tenant.
- Enforce token budget caps per tenant per billing period.

These are Tier 0+ additions, not Wave 7 work.

---

## 7. Minimum UI Testability Requirements

### 7.1 Required separation

| Portal | Who uses it | Tenant context | LLM provider admin |
|---|---|---|---|
| Platform Admin | THEM operators only | Switches via privileged action | Yes — visible |
| Tenant Portal | Customer admins/users | Fixed at login; no selector | No — hidden |

The Platform Admin portal and Tenant Portal must be separate authenticated surfaces. They may
share a common component library but must not share a route namespace, a cookie/JWT scope, or
a capability set. A Tenant Portal user must not be able to discover, even by URL guessing, that
Platform Admin routes exist.

### 7.2 Test setup for isolation verification

Minimum required to test isolation:
1. Create two test tenants: `tenant-alpha` and `tenant-bravo`.
2. In each tenant, create: one Application, one Agent, one Entry Point — using identical names.
3. Create one Platform Admin user and one Tenant Portal user per tenant.

### 7.3 Cross-tenant leakage tests

| Test | Expected result |
|---|---|
| Tenant Alpha user requests Tenant Bravo's application list | 403 or empty (never Bravo's data) |
| Tenant Alpha session records appear in Tenant Bravo's session admin | Never (row-level filter) |
| Tenant Alpha run appears in Tenant Bravo's runs list | Never |
| Tenant Alpha and Bravo have agents with identical slugs | Both exist; no slug collision across tenants |
| Platform Admin lists all applications (no tenant filter) | Returns applications from ALL tenants |
| Platform Admin switches to Tenant Alpha context → lists applications | Returns only Alpha's applications |

### 7.4 Denied access behavior

- A tenant user attempting to access another tenant's resource: **403 Forbidden** (not 404).
  Returning 404 would reveal that the resource exists. 403 reveals only that access is denied.
- An unauthenticated user: **401 Unauthorized**.
- A Platform Admin operating outside any tenant context attempting to access a tenant resource
  without a tenant switch: **403** or require explicit tenant context selection.

### 7.5 API category classification

| API category | Classification | Who calls it |
|---|---|---|
| `GET/PUT /api/v1/admin/llm-providers` | Platform control-plane | Platform Admin only |
| `GET/PUT /api/v1/admin/llm-providers/routing/config` | Platform control-plane | Platform Admin only |
| `GET/PUT /api/v1/admin/monitoring-config` | Platform control-plane | Platform Admin only |
| `GET/POST/DELETE /api/v1/admin/agents` (CRUD) | Platform control-plane | Platform Admin only (current); Tenant control-plane (future, post tenant migration) |
| `GET/POST/DELETE /api/v1/admin/orchestrators` (CRUD) | Platform control-plane | Platform Admin only (current); Tenant control-plane (future) |
| `GET/POST/DELETE /api/v1/admin/applications` (CRUD) | Tenant control-plane | Tenant Portal admin |
| `GET/POST/DELETE /api/v1/admin/tokens` (CRUD) | Tenant control-plane | Tenant Portal admin |
| `GET /api/v1/admin/sessions` | Tenant control-plane | Tenant Portal admin |
| `POST /api/v1/admin/sessions/{id}/disconnect` | Tenant control-plane | Tenant Portal admin |
| `GET /api/v1/runs` | Tenant control-plane | Tenant Portal admin |
| `POST /api/v1/runs/{id}/signal` | Tenant control-plane | Tenant Portal admin |
| `/ws/{slug}`, `/sse/{slug}` | Runtime data-plane | Customer applications |
| `/apps/{slug}/ws`, `/apps/{slug}/sse` | Runtime data-plane | Customer applications |
| A2A `message/send` | Runtime data-plane | External agents |
| `/health/*`, `/metrics` | Platform control-plane (observability) | Monitoring systems |

---

## 8. Decisions Made Now

These decisions are confirmed and govern all subsequent Go migration work.

| # | Decision |
|---|---|
| D-01 | LLM providers are **platform-global**. No `tenant_id` column. Managed by Platform Admin only. |
| D-02 | LLM routing config (`them.config['llm_routing']`) is **platform-global**. Current Go route is correct. |
| D-03 | Monitoring config (`them.config['monitoring']`) is **platform-global**. Current Go route is correct. |
| D-04 | Applications are **tenant-owned**. A `tenant_id` column will be added in the tenant migration wave. |
| D-05 | Entry points, app orchestrators, middleware wirings are **application-owned** (tenant boundary through application). |
| D-06 | Agents and shared orchestrators are **tenant-owned** (currently platform-global; to be migrated). |
| D-07 | Access tokens are **tenant-owned** (to be migrated). |
| D-08 | Runs, sessions, tasks are **user/session-owned within an application** (tenant boundary through application). |
| D-09 | Tenant context must be resolved from authenticated identity only. Request fields and headers are untrusted. |
| D-10 | Users belong to exactly one tenant. Platform Admins are outside tenant scoping. |
| D-11 | A Platform Admin may switch tenant context only through a privileged action that is recorded in audit. |
| D-12 | Cross-tenant access requests return **403**, not 404. |
| D-13 | The Platform Admin portal and Tenant Portal are separate authenticated surfaces with no shared routes. |
| D-14 | The tenant migration will be a dedicated wave (pre-Wave N) that adds `tenant_id` columns and backfills. It will not be mixed with any other migration wave. |
| D-15 | Wave 7 handlers operate on platform-global resources. No tenant parameter is required in any Wave 7 handler. |

---

## 9. Open Decisions

These questions are not resolved here. They must be answered before the tenant migration wave begins.

| # | Question |
|---|---|
| O-01 | Should agents and shared orchestrators become tenant-owned, or remain platform-global templates that tenants override? If tenant-owned, slug uniqueness becomes per-tenant. |
| O-02 | Should `access_tokens` gain a `tenant_id` column, or should tenant scoping be inferred from `orchestrator_id → orchestrators.tenant_id`? |
| O-03 | What is the billing model? Per-run, per-token, per-session, or subscription? This drives whether `run_usage` needs a direct `tenant_id` or inherits through the run chain. |
| O-04 | How is the auth service (`auth_service.users`) adapted for multi-tenancy? Does each tenant get isolated user namespaces, or are users platform-global with tenant memberships? |
| O-05 | What is the Redis isolation strategy? Key-prefix-per-tenant (Tier 2) or shared with row-level DB enforcement only (Tier 0)? |
| O-06 | Should Tenant Portal be a separate deployment path (separate Next.js app, separate domain) or a filtered view of the existing admin UI? |
| O-07 | When should Tier 1 (dedicated Temporal worker pools) be required? At first paying customer? At N tenants? |
| O-08 | Should per-tenant LLM provider API key override be row-level in `them.llm_providers` (adding `tenant_id`) or via a separate `them.tenant_llm_providers` override table? |

---

## 10. Summary — Ownership Recommendations

### LLM providers
**Platform-global.** Managed by Platform Admin. No `tenant_id` column. The `UNIQUE(name)`
constraint is correct as-is. Per-tenant API key overrides are a future concern handled at the
`app_orchestrators.llm_api_key_encrypted` level, not in `them.llm_providers`.

### LLM routing config
**Platform-global.** The `them.config['llm_routing']` row is a platform default that tells the
runtime which provider to use when an orchestrator does not specify its own. Current Wave 6 Go
routes are correct and may remain unchanged.

### Monitoring config
**Platform-global.** The `them.config['monitoring']` row holds platform-wide operational
thresholds. Current Wave 6 Go routes are correct and may remain unchanged.

---

## 11. Impact on Wave 7 Phase 2

**No rework required. Phase 2 implementation is correct.**

The DAL operates on a platform-global table with no `tenant_id` — correct.
The service layer has no tenant parameter — correct for a platform-global resource.
The `UNIQUE(name)` uniqueness contract is correct for platform-global scope.
The encryption uses `THE_M_SECRET_KEY` (platform key) — correct for platform credentials.

---

## 12. Whether Wave 7 Phase 3 May Proceed

**Yes. Wave 7 Phase 3 may proceed without any prerequisites.**

The Phase 3 work (HTTP handlers + Traefik cutover for LLM provider CRUD) operates entirely on
a platform-global resource. The handlers:
- Do not need a `tenant_id` parameter.
- Do not need tenant context resolution.
- Are mounted under `RequireSuperAdmin` (Platform Admin only) — correct.
- Write to a table that will never gain a `tenant_id` column.

The only constraints that apply to Phase 3 from this document:

1. The 5 LLM provider routes must be documented as **Platform control-plane API** in any
   API catalogue, spec, or client SDK.
2. The Phase 3 handlers must not expose `api_key_encrypted` in any response — already
   enforced by the service layer.
3. The Traefik router for these routes (`them-go-llm-providers`) must sit behind the same
   `RequireSuperAdmin` JWT check that all other admin routes use — already enforced by the
   admin sub-router mount in `cmd/them/main.go`.
4. No cross-tenant isolation work is required or expected in Wave 7. That belongs to a
   dedicated tenant migration wave.

---

## 13. Exact Next Task

**Wave 7 Phase 3:** Implement HTTP handlers and Traefik cutover for LLM provider CRUD.

See `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` for the exact implementation steps and
first prompt.

The tenant foundation work (adding `tenant_id` columns, tenant middleware, Platform Admin /
Tenant Portal separation) is a separate dedicated wave to be planned before it is needed.
It must not be mixed into any Go migration wave.
