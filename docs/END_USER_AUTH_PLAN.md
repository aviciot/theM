# End-User Authentication at Runtime Entry Points
# Status: Design v4 — three security points verified, gaps documented
# Last updated: 2026-09-08

---

## The Problem

The-M needs to support two deployment patterns simultaneously:

- **Platform-as-infrastructure**: A bank deploys the-M. Bank employees manage it. Bank customers (Alice, Bob) use agentic apps without ever knowing the-M exists.
- **Platform-as-product**: End users sign up directly on the-M. They log in with their own credentials and use agents through a branded interface.

These two patterns require different identity flows, different history isolation, and different permission models — but they must share the same runtime pipeline without one path degrading security for the other.

---

## What the Code Actually Does (verified)

### JWT claims (auth/jwt.go, authserver/jwt.go)

The HS256 access token issued by `them-auth-go` contains:
```json
{ "sub": "42", "username": "avi", "name": "Avi Cohen",
  "role": "admin", "tenant_id": "...", "exp": ... }
```

- `role` is the **tenant membership role** (`admin`/`member`/`viewer`), not the platform role.
- The bridge reads this as `Claims.Roles = []string{role}` (normalised from single string to slice).
- `RequireSuperAdmin` checks `claims.Roles` for `"super_admin"`.
- `RequireTenantAdmin` checks for `"admin"` or `"super_admin"`.

### The two-role system (service.go:233–236, SCHEMA.sql)

There are **two separate role axes**:

| Axis | Where stored | Values | What it gates |
|---|---|---|---|
| Platform role | `auth_service.roles` (global) | `super_admin`, `developer`, `analyst`, `viewer` | `dashboard_access` field — `'admin'`/`'view'`/`'none'` |
| Membership role | `auth_service.tenant_memberships.role` | `admin`, `member`, `viewer` | JWT `role` claim; enforced by `RequireTenantAdmin` |

At login (`service.go:236`): the **membership role wins** — it is what goes into the JWT. The platform role governs `dashboard_access` (whether login is allowed at all) but does not appear in the JWT or at runtime.

OIDC users (`oidc_store.go:128`): always get platform role `"viewer"` (hard-coded). Their membership role comes from group mappings and defaults to `"viewer"`.

### Dashboard access gate (service.go:103–105)

```go
if user.DashboardAccess == "" || user.DashboardAccess == "none" {
    return nil, ErrDashboardAccessDenied  // → 403
}
```

`dashboard_access` is on `auth_service.roles`. `viewer` role has `'view'` (not `'none'`), so viewers **can log in** and get a JWT. They are blocked from admin routes by `RequireTenantAdmin`, which rejects `viewer`.

**Important gap:** There is no role that has `dashboard_access = 'none'` today. A pure end-user role (runtime-only, no dashboard at all) does not exist.

### Runtime entry point authentication (epconfig, lifecycle.go)

Two access modes:
- `AccessModePublic` — no token required.
- `AccessModeToken` — requires a valid opaque bearer token from `them.access_tokens`.

The HS256 dashboard JWT **cannot be used at a WS/SSE entry point today**. The token cache (`auth/token_cache.go`) validates opaque bearer tokens from the DB only. The JWT validation path (`auth/jwt.go:ValidateHS256JWT`) is used only by admin middleware — never by the WS/SSE admission pipeline.

### History isolation (history/pgx.go)

`LoadHistory` filters by `context_id + tenant_id + external_user_id`. No `user_id` filter. `context_id` is caller-supplied. Internal-user history is **not** per-user isolated — two users in the same tenant sharing a `context_id` share history. This is the current accepted model for internal team use.

### Managed apps (migration 055)

`them.applications.app_type` is `'tenant'` | `'managed'`. `managed_app_bindings` links a managed (platform-owned) app to a consuming tenant. **Nothing in the current runtime uses `app_type`** — managed apps are provisioned differently but execute identically. Runtime data (runs, tasks, history) is always scoped to the entry point's `tenant_id`, which for a managed app binding would be the **consuming tenant**, not the platform tenant.

---

## Three Concerns, Separated

### 1. Application ownership

Who created and controls the application definition.

| `app_type` | Owner tenant | Description |
|---|---|---|
| `'tenant'` | Any tenant | Self-managed app. Tenant builds and owns it. |
| `'managed'` | Platform (`default` tenant) | Platform-provided template. Tenants bind to it via `managed_app_bindings`. |

The `app_type` column already exists. Runtime enforcement of this boundary is not yet implemented.

### 2. Permission to invoke

Who may call the entry point at runtime.

Three distinct credential types are in scope:

| Credential | Issued by | Validates via | `user_id` source | `external_user_id` source |
|---|---|---|---|---|
| **Opaque bearer token** (`is_backend=false`) | the-M admin UI | `them.access_tokens` DB lookup | `access_tokens.user_id` | never (not trusted) |
| **Backend service token** (`is_backend=true`) | the-M admin UI | `them.access_tokens` DB lookup | `access_tokens.user_id` | `X-External-User` header (trusted) |
| **the-M user JWT** | `them-auth-go` login/OIDC | `ValidateHS256JWT` signature check | JWT `sub` claim | none (the user IS the identity) |
| **Bank-issued JWT** (Phase 3) | Bank's own IdP | JWKS signature check | `0` (no the-M account) | JWT `sub` claim |

The-M user JWT path does not exist at entry points today. It must be added as a new `AccessModeUser` that runs the JWT validator instead of the token cache lookup.

### 3. Runtime data ownership

Every run and task must be scoped so that analytics, billing, and history can be attributed correctly.

| Field | Populated from | Purpose |
|---|---|---|
| `runs.tenant_id` | `EPConfig.TenantID` (server) | Tenant billing, RLS isolation |
| `runs.entry_point_slug` | URL path (server) | App attribution |
| `runs.user_id` | *(not stored today)* | Internal-user attribution (gap) |
| `runs.external_user_id` | `X-External-User` / JWKS `sub` | End-user attribution |
| `tasks.external_user_id` | same | History ownership filter |

For managed apps, `tenant_id` on the run is the **consuming tenant** (from the entry point's `tenant_id` field, which is set at binding time). The platform tenant never appears on runtime data.

---

## Consistent Runtime Identity Model

All three credential paths must produce a `RuntimeIdentity` with the same fields:

```
RuntimeIdentity {
    TenantID:       string   // always set; from token/JWT claim; never from request
    UserID:         int64    // the-M internal user ID; 0 for external-only identities
    ExternalUserID: string   // end-user identity; empty for pure internal sessions
    IsBackend:      bool     // true = backend service token; may trust X-External-User
    SessionID:      string   // server-assigned
    RunID:          string   // server-assigned
}
```

How each path populates it:

| Path | TenantID | UserID | ExternalUserID |
|---|---|---|---|
| Opaque token (is_backend=false) | `access_tokens.tenant_id` | `access_tokens.user_id` | *(empty)* |
| Backend service token (is_backend=true) | `access_tokens.tenant_id` | `access_tokens.user_id` | `X-External-User` header |
| the-M user JWT (new) | JWT `tenant_id` claim | JWT `sub` claim | *(empty — user IS the identity)* |
| Bank JWT / JWKS (Phase 3) | EP URL path tenant | `0` | JWT `sub` claim |

History is always filtered by `context_id + tenant_id + external_user_id`. For the JWT path, `user_id` will also be stored on runs (for analytics), but history ownership uses `external_user_id` uniformly across all paths — this avoids a split filter path.

---

## Application-Level Authorization (separate from authentication)

Authentication answers "who are you?". Authorization answers "are you allowed to use this entry point?".

Today `CheckAccess` (`epconfig/epconfig.go:457`) enforces:
- EP enabled/disabled
- App enabled/disabled
- Token-level blocklist
- User-level blocklist

It does **not** enforce principal type (internal vs external vs end-user).

The missing layer is an `allowed_principals` field on entry points:

| Value | Meaning |
|---|---|
| `'internal'` | Only opaque bearer tokens and the-M user JWTs. End-user / bank JWTs rejected. |
| `'external'` | Only end-user / bank JWTs and backend service tokens with `X-External-User`. Internal team blocked. |
| `'both'` | All principal types allowed. |

Custom **application-level roles** (e.g. "premium vs free") are handled separately via the `Permissions []string` field already on `TokenInfo`. Phase 3 maps JWKS group claims to permissions. `CheckAccess` can then enforce permission requirements defined on the EP.

---

## Managed App — Platform vs Consuming Tenant

For a managed app:
- **Application definition** lives under the platform's `default` tenant. The platform team manages agents, orchestrators, and EPs.
- **Binding** (`managed_app_bindings`) attaches it to a consuming tenant with optional config overrides.
- **Entry point `tenant_id`** is set to the **consuming tenant** at binding time.
- **Every run** gets `tenant_id = consuming_tenant_id`. Quota, RLS, history, and billing are all scoped to the consuming tenant.
- The platform tenant never appears on any runtime data row.

Authorization for a managed EP follows the same `allowed_principals` model. The consuming tenant controls which principal types may call their binding — the platform tenant has no say at runtime.

---

## Security Verification — Three Questions (answered 2026-09-08)

### Q1: Where is permission to invoke a specific application enforced?

**Verified:** `CheckAccess` in `epconfig/epconfig.go:457` enforces:
- EP enabled/disabled
- App enabled/disabled
- Token-level blocklist (`them.access_tokens.blocked`)
- User-level blocklist (`them.users.blocked`)

**Gap confirmed:** There is NO check of principal type (internal vs external vs end-user) against any EP-level policy. Any credential type that passes authentication can reach any EP that accepts its access mode. `allowed_principals` does not exist. This is the Phase 3 gap — intentionally deferred, not overlooked.

**Safe for Phase 2?** Yes — Phase 2 only adds `AccessModeUser` for the-M user JWTs. Since Phase 2 EPs will be explicitly configured to `AccessModeUser`, a bearer-token caller cannot reach them (access mode mismatch rejects at `Lifecycle.Admit`). The inverse (JWT caller reaching `AccessModeToken` EPs) is also blocked by access mode. The principal-type guard (`allowed_principals`) becomes necessary only when Phase 4 bank JWTs are active and a single EP might accept both internal and external callers.

---

### Q2: How does history avoid collisions between identity namespaces?

**Verified:** `history/pgx.go:LoadHistory` filter:
```sql
AND ($3 = '' OR t.external_user_id = $3)
```

When `externalUserID = ""` (internal sessions), the filter is **skipped** — all tasks for `context_id + tenant_id` are visible. This is intentional: internal team members share history across a context.

**Collision analysis:**
- `user_id`-based and `external_user_id`-based identities are **different columns on `them.tasks`**. They cannot collide at the SQL level.
- `user_id = 42` (internal) and `external_user_id = "42"` (end-user) within the same tenant **will share history** if they use the same `context_id` and the internal call arrives with `externalUserID = ""`.
- For Phase 4 (bank JWTs), cross-issuer collision is bounded by `tenant_id` — a tenant can only configure one JWKS issuer, so two different issuers cannot produce the same `external_user_id` within one tenant.

**Remaining gap (Phase 2):** Internal sessions with `externalUserID = ""` return the full context history, which would include end-user turns if an internal user reuses an end-user's `context_id`. The fix (Phase 2): write `user_id` to `them.tasks` and add a `user_id` filter for internal sessions, matching the `external_user_id` pattern. This prevents internal sessions from accidentally reading end-user history.

---

### Q3: How is the authenticated tenant checked against the entry point's consuming tenant, including managed apps?

**Verified:** `epconfig/pgx.go:epConfigQuery`:
```sql
WHERE ep.tenant_id = $1::uuid AND a.slug = $2 AND ep.slug = $3
```

`tenantID` comes from: (1) URL slug → DB lookup, (2) bearer token `TenantID` claim, (3) bootstrap fallback. The DB query enforces the boundary at fetch time — a mismatched tenant gets a 404/401 before any execution starts.

**Managed app gap confirmed:** `managed_app_bindings` links `(app_id, tenant_id)` but has no entry_point column. The entry_points for a managed app live under the **platform tenant**'s `tenant_id`. A consuming tenant's URL slug resolves to their own `tenant_id`, which cannot match the platform's `ep.tenant_id`. Therefore:

> **Managed apps are not reachable at runtime today.** The binding exists in DB but there is no URL routing mechanism that lets a consuming tenant call a managed app's entry point.

The fix requires either: (a) provisioning replicated entry_point rows per binding with the consuming tenant's `tenant_id`, or (b) extending `epConfigQuery` to JOIN `managed_app_bindings` when `app_type = 'managed'` and the requesting tenant has an active binding.

Option (b) is smaller. This is a **Phase 5** gap — out of scope for Phases 1–4.

---

## Gaps vs Existing Functionality

| Capability | Exists today | Gap | Phase |
|---|---|---|---|
| Tenant isolation on runs/tasks | ✅ `tenant_id` enforced via `epConfigQuery` | — | done |
| External-user history isolation | ✅ Phase 1 (`external_user_id` filter) | — | 1 |
| Internal-user history isolation | ✅ Phase 2 (`user_id` on tasks/runs; dual-column SQL filter) | — | 2 |
| Dashboard access gate | ✅ `dashboard_access` field blocks `'none'` | — | 2 |
| Runtime-only user role | ✅ Phase 2 (`end_user` role seeded, migration 087) | — | 2 |
| The-M user JWT at WS/SSE entry points | ✅ Phase 2 (`AccessModeUser = "user_jwt"`, lifecycle step 3.5) | **Authorization rule:** any authenticated member of the EP's tenant can invoke any `AccessModeUser` EP. `allowed_principals` (Phase 3) is the scheduled fix for per-EP principal restrictions. | 2 |
| Bank JWT / JWKS validation | ❌ | `JWKSAuthenticator` not implemented | 4 |
| Principal type guard on EP | ❌ | `allowed_principals` not implemented | 3 |
| Managed app runtime routing | ❌ | `epConfigQuery` uses `ep.tenant_id` — managed EP not reachable by consuming tenant | 5 |

---

## Implementation Phases

### Phase 1 — Already complete (commit 1caef69)

- `external_user_id` on `them.tasks` and `them.runs`
- `is_backend` on `them.access_tokens`
- `LoadHistory` / `resolveRootTaskID` filter by `external_user_id`
- `X-External-User` header trusted only from `is_backend=true` tokens

---

### Phase 2 — The-M user JWT at entry points + runtime-only role + internal history isolation ✅ COMPLETE (2026-09-08)

**What it enables:**
1. Direct end users with a the-M account can call WS/SSE entry points using their dashboard JWT — no bearer token needed.
2. A runtime-only `end_user` role prevents these users from reaching the dashboard.
3. Internal sessions (empty `externalUserID`) are isolated from end-user history by storing `user_id` on tasks and filtering it.

**Schema changes:**
```sql
-- New role with no dashboard access
INSERT INTO auth_service.roles (name, description, dashboard_access, rate_limit, cost_limit_daily, token_expiry)
VALUES ('end_user', 'Runtime-only access, no dashboard', 'none', 1000, 10.00, 3600)
ON CONFLICT (name) DO NOTHING;

-- user_id on runs for analytics attribution
ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS user_id INTEGER;

-- user_id on tasks for history isolation (mirrors external_user_id pattern)
ALTER TABLE them.tasks ADD COLUMN IF NOT EXISTS user_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_tasks_user ON them.tasks(user_id) WHERE user_id IS NOT NULL;
```

**Go changes:**
- `epconfig`: add `AccessModeUser = "user_jwt"` constant.
- `Lifecycle.Admit` (WS/SSE handlers): when `AccessMode == AccessModeUser`, validate bearer as HS256 JWT via `auth.ValidateHS256JWT`; populate `RuntimeIdentity.UserID` from `claims.UserID`; set `TenantID` from `claims.TenantID`.
- `runrecorder.CreateRun`: write `user_id` when set.
- `history/pgx.go:LoadHistory`: add `userID int64` param; when `userID != 0`, add `AND t.user_id = $N` filter. When both `userID` and `externalUserID` are non-empty, only `externalUserID` is used (JWKS callers do not have a the-M user ID).
- `history/pgx.go:resolveRootTaskID`: INSERT now includes `user_id` when set.
- Service `Login`: the `ErrDashboardAccessDenied` check blocks `dashboard_access='none'` users from logging in via `/auth/login`. For runtime-only users, a separate `/auth/runtime-login` endpoint issues a JWT without the `dashboard_access` gate.

**Dashboard protection:** `RequireTenantAdmin` already rejects `viewer` — an `end_user` JWT cannot reach any admin route.

**Identity isolation invariant after Phase 2:**

| Session type | `user_id` filter | `external_user_id` filter | Effect |
|---|---|---|---|
| Internal (bearer token, no end-user) | `user_id = N` | empty (skip) | Sees only that user's turns |
| Backend-asserted end user | 0 (skip) | `external_user_id = X` | Sees only that end-user's turns |
| The-M user JWT | `user_id = N` | empty (skip) | Sees only that user's turns |

**Effort:** ~1.5 days. Low risk — additive, no existing path broken.

**Acceptance tests:**
- `TestAccessModeUser_ValidJWT_Admitted` — valid HS256 JWT → 101 WS upgrade.
- `TestAccessModeUser_InvalidJWT_Rejected` — tampered JWT → 401.
- `TestAccessModeUser_EndUserRole_DashboardDenied` — `end_user` role JWT → 403 on `/auth/login`.
- `TestAccessModeUser_ViewerJWT_AdminRoute_Rejected` — viewer JWT → 403 on admin route.
- `TestAccessModeUser_UserIDStoredOnRun` — run row has correct `user_id`.
- `TestHistory_InternalSession_CannotReadEndUserTurns` — internal `user_id=42` with shared `context_id` cannot read tasks written by `external_user_id="alice"`.
- `TestHistory_EndUser_CannotReadInternalTurns` — `external_user_id="alice"` cannot read tasks written by `user_id=42`.
- `TestTenantIsolation_MismatchedTenantRejects401` — bearer token from tenant A cannot reach EP in tenant B.

---

### Phase 3 — Principal type guard on entry points

**What it enables:** An EP can be restricted to internal users only, external end-users only, or both. Prevents internal team tokens from accidentally hitting customer-facing EPs and vice versa.

**Schema:**
```sql
ALTER TABLE them.entry_points
  ADD COLUMN IF NOT EXISTS allowed_principals TEXT NOT NULL DEFAULT 'internal'
  CHECK (allowed_principals IN ('internal', 'external', 'both'));
```

**Go changes:**
- `EPConfig`: add `AllowedPrincipals string`.
- `Lifecycle.Admit` / `CheckAccess`: check principal type against `AllowedPrincipals`:
  - `internal` — opaque bearer token or the-M user JWT allowed; bank JWT / backend-asserted `external_user_id` rejected.
  - `external` — only backend service tokens with `X-External-User` or bank JWTs (Phase 4) allowed.
  - `both` — no restriction.

**Effort:** ~0.5 day.

**Acceptance tests:**
- `TestAllowedPrincipals_Internal_RejectsExternalUser`
- `TestAllowedPrincipals_External_RejectsInternalToken`
- `TestAllowedPrincipals_Both_AcceptsEither`

---

### Phase 4 — Bank JWT / JWKS validation

**What it enables:** A bank customer's JWT (issued by the bank's own IdP — Keycloak, Auth0, etc.) is validated directly at the entry point. No bearer token from the-M needed. `external_user_id` is extracted from `sub` server-side — no header trust.

**Schema:**
```sql
CREATE TABLE them.tenant_runtime_config (
    tenant_id      UUID PRIMARY KEY REFERENCES them.tenants(id) ON DELETE CASCADE,
    jwks_uri       TEXT NOT NULL,
    issuer         TEXT NOT NULL,
    audience       TEXT NOT NULL,
    claim_mappings JSONB NOT NULL DEFAULT '{}',  -- {"group-name": "permission-name"}
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Go changes:**
- New `JWKSAuthenticator` implementing `transport.Authenticator` interface.
- Fetches JWKS from tenant config (per-tenant, cached, auto-refreshed on key rotation).
- Validates `iss`, `aud`, `exp`. Extracts `sub` → `ExternalUserID`. Maps group claims → `Permissions`.
- Sets `UserID = 0` (no the-M account). `TenantID` from EP URL path, not JWT.
- `CompositeAuthenticator`: tries JWKS validation when the tenant has `runtime_config`; falls back to opaque token cache.
- New `AccessModeExternal = "external_jwt"` EP mode.

**Effort:** ~2 days. Highest complexity — JWKS caching, key rotation, claim mapping.

**Acceptance tests (Keycloak test plan):**

| # | Test | Validates |
|---|---|---|
| T1 | `avi1` (premium group) hits `premium-only` EP | Permission enforced from JWT group claim |
| T2 | `avi2` (free group) hits `premium-only` EP | Lower-tier blocked |
| T3 | `avi1` runs agent; check DB | `external_user_id = avi1.sub` on run + task (sourced from JWT, not header) |
| T4 | `avi2` supplies `avi1`'s `context_id` | Empty history (different external_user_id) |
| T5 | Caller sends `X-External-User: avi1` while using `avi2` JWT | Run attributed to `avi2`'s sub (header ignored) |
| T6 | `avi1` token used at EP in different tenant | 403 — tenant isolation |

---

### Phase 5 — Managed app runtime routing

**What it enables:** A consuming tenant can call a managed (platform-owned) app's entry point. Today this is not possible because `epConfigQuery` resolves by `ep.tenant_id` which is the platform tenant's ID, not the consuming tenant's.

**Root cause:** `managed_app_bindings(app_id, tenant_id)` has no entry_point column. Entry points are always owned by the application's tenant. A consuming tenant has no entry_point rows for a managed app.

**Proposed fix (option B — smaller):** Extend `epconfig/pgx.go:epConfigQuery` to fall back to a managed-app JOIN when the primary tenant lookup yields no row:

```sql
-- After primary lookup fails, try managed app path:
SELECT ep.*, a.*
FROM them.entry_points ep
JOIN them.applications a ON ep.app_id = a.id
JOIN them.managed_app_bindings mab ON mab.app_id = a.id
WHERE a.app_type = 'managed'
  AND mab.tenant_id = $1::uuid     -- consuming tenant's ID
  AND mab.enabled = true
  AND a.slug = $2
  AND ep.slug = $3
```

The `TenantID` on the returned `EPConfig` must be set to the **consuming tenant's** ID, not the platform's.

**Effort:** ~1 day. Moderate risk — changes the core EP resolution path; needs integration tests against a real managed app binding.

**Out of scope for Phases 1–4.** Managed app routing is a separate concern from end-user authentication.

---

## Build Order and Dependencies

```
Phase 1 (done)  — external_user_id + is_backend + history isolation
Phase 2         — the-M user JWT at entry points + runtime-only role + internal history isolation
Phase 3         — allowed_principals guard on EPs
Phase 4         — Bank JWT / JWKS validation
Phase 5         — Managed app runtime routing (epConfigQuery JOIN on managed_app_bindings)
```

Phase 2 and 3 are independent and can be done in either order.
Phase 4 depends on Phase 3 (needs `allowed_principals = 'external'` to safely restrict JWKS-validated EPs).
Phase 5 is independent — it touches only `epconfig/pgx.go` and has no dependency on Phases 2–4.

Phase 2 also unblocks the "platform-as-product" pattern immediately — direct end users can sign up and use agents with their own account, fully isolated history, and no dashboard access.
