# End-User Authentication at Runtime Entry Points
# Status: Design proposal — security-reviewed v2, not yet implemented
# Last updated: 2026-09-08

---

## The Problem

The-M currently supports two principals at WS/SSE entry points:

1. **Service API token** — opaque bearer token scoped to an orchestrator (not an application). Any authenticated caller in a tenant can use it at any entry point. The-M sees the token but not the individual customer behind it.
2. **Internal JWT** — issued after login (local or SSO), used by the internal team in the dashboard.

Neither supports **end-user identity at runtime** — the bank's retail customers who interact with the agentic app. Today the-M cannot attribute a run to a specific customer, enforce per-customer limits, or isolate their history.

---

## What Exists Today (code-verified)

### History loading (`history/pgx.go:80–87`)

```sql
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
ORDER BY tm.id DESC LIMIT $3
```

Filter: **`context_id` + `tenant_id` only.** No `user_id`, no session ownership check.

- `context_id` is **client-supplied** (`ws/handler.go:319`). Any authenticated caller in a tenant can supply any UUID.
- `tenant_id` prevents cross-tenant access (correct).
- There is no per-user or per-session ownership check on history.

### History for different principal types today

| Principal | History scoping today |
|---|---|
| Internal user (JWT) | `context_id + tenant_id`. No user_id filter. User A can read user B's history by supplying B's context_id. |
| Service token | Same. No per-user scoping. All callers sharing a service token share a flat history namespace keyed by context_id. |
| End-user JWT (Approach B) | Does not exist yet. |

### Service token scope (`them.access_tokens` schema)

```sql
CREATE TABLE IF NOT EXISTS them.access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    orchestrator_id UUID REFERENCES them.orchestrators(id) ON DELETE CASCADE,
    ...
);
```

**`application_id` does not exist on `access_tokens`** — not in schema or any migration. `TokenInfo.AppID` in Go code is always `0`. The previous plan's claim of a cross-app token gap was incorrect: service tokens are scoped to an orchestrator, not an application. Cross-app reuse via service token is not currently possible because the EP resolves the app from the URL path and the token carries no app claim to conflict with.

**Correction to prior plan:** The `AppID` check proposed in Q2 is premature. No `application_id` column exists to check. This gap should only be added if/when tokens are explicitly scoped to applications.

### `context_id` model

- `them.tasks.context_id UUID NOT NULL` — client-supplied, not server-generated.
- `resolveRootTaskID` (`history/pgx.go:228`) finds-or-creates a task row keyed on `(context_id, run_id, tenant_id)`.
- `context_id` is the **history namespace**. Two runs with the same `context_id` share history (intended for multi-turn conversations).
- No `session_id` on tasks. `them.runs.session_id` exists but is not threaded through to history loading.

### Token exchange vs passthrough (corrected)

The prior plan incorrectly stated that token exchange "requires user accounts or DB rows." That is wrong. OAuth2 token exchange (RFC 8693) issues a new short-lived token for the caller — the AS (the-M) can issue a stateless signed JWT with no DB row. The real trade-offs are:

| | Passthrough (validate bank JWT at EP) | Exchange (issue the-M JWT) |
|---|---|---|
| Latency | JWKS fetch on first use (cached) | Extra pre-connect round-trip |
| DB rows | Zero | Zero if stateless JWT issued |
| Revocation | JWT expiry only | The-M can revoke issued token |
| Trust boundary | The-M trusts bank's JWKS directly | The-M independently re-signs; cleaner |
| Caller complexity | Bank JWT passed as Bearer on every request | Bank exchanges once, uses the-M JWT |
| `external_user_id` sourcing | From bank JWT `sub` at every request | Embedded in the-M JWT at exchange time |

**Recommendation: Passthrough.** The `Authenticator` interface is injectable — `JWKSAuthenticator` drops in without changing WS/SSE handlers. Exchange adds a round-trip and complexity for no security gain when the-M already validates the bank's JWKS. Short-lived bank tokens (5–15 min, refreshed by the bank app) make revocation moot.

---

## Security Findings (v2, code-verified)

### Q1. Can a caller spoof `external_user_id` or roles?

**Today:** No `external_user_id` exists anywhere. Existing identity fields (TenantID, AppID) are server-sourced and well-hardened.

**Risk when building:** The original plan proposed accepting `external_user_id` as a header or param — that's an exploit. Must be extracted from the validated JWT `sub` claim inside `JWKSAuthenticator` only, never from any caller-supplied field.

---

### Q2. Is the token verified for the specific tenant and application?

**Today:** `TenantID` is properly enforced. `access_tokens` has no `application_id` — `TokenInfo.AppID` is always `0`. Service tokens are scoped to an orchestrator via `orchestrator_id`. Cross-app token reuse is not possible with the current schema.

**Risk:** If `application_id` is added to tokens in the future, a check must be added simultaneously. Not needed now.

---

### Q3. Is there a runtime role concept separate from dashboard roles?

**Today:** `TokenInfo.Permissions []string` exists but is never checked in the admission pipeline. No EP-level principal guard. Dashboard roles (admin/viewer) are not checked at WS/SSE either.

**Risk:** An end-user JWT (Approach B) arriving at the same EP as internal team JWTs with no guard. An `allowed_principals` flag on entry points is needed before shipping Approach B.

---

### Q4. History ownership: internal users, external users, and service tokens

**Today:**
- History is keyed by `context_id + tenant_id` only.
- `context_id` is client-supplied — any authenticated caller in a tenant can load any other caller's history.
- This is acceptable for internal team use (trust boundary is the tenant) but **not acceptable for end users** (bank customers must not read each other's history).

**History model per principal type:**

| Principal | Correct history model | Change needed |
|---|---|---|
| Internal user (JWT) | Shared context within tenant. Caller controls `context_id`. Acceptable — team trusts each other. | None for now. |
| Service token (bank app) | Shared context keyed by whatever `context_id` the bank app passes. No per-customer isolation today. | Add `external_user_id` filter so history is scoped to the specific customer the bank app identifies. |
| External end-user JWT (Approach B) | `external_user_id = JWT.sub`. History scoped by `context_id + tenant_id + external_user_id`. Caller cannot access another user's history even with correct `context_id`. | `external_user_id` on `them.tasks` + filter in `LoadHistory`. |

**A shared service token cannot establish per-customer history isolation without the bank app explicitly passing an `external_user_id`.**
Options:
1. Service token + `external_user_id` header (trusted because the bank's service token authenticates the bank, and the bank asserts the customer ID — acceptable trust model for B2B).
2. Per-customer JWTs (Approach B) — server extracts `sub` from each user's JWT, no header trust needed.

Option 1 is acceptable if the service token is scoped to the tenant (which it is today). The bank is a trusted caller; the service token authenticates the bank; the bank asserts the customer ID. This is the same model as Stripe's `Stripe-Account` header or `on_behalf_of`.

---

### Q5. Token exchange vs passthrough (see table above)

**Recommendation: Passthrough**, not because exchange requires DB rows (it doesn't), but because it's simpler, the `Authenticator` interface already supports it, and there is no revocation requirement given short-lived tokens.

---

## Keycloak Test Plan — Two Users, Six Cases

**Setup:**
- `avi1` in Keycloak group `bank-premium` → the-M permission: `premium`
- `avi2` in Keycloak group `bank-free` → the-M permission: `free`
- Keycloak client `bank-runtime` emits `groups` claim
- avi-test tenant `runtime_config`: JWKS URI + claim mappings `{bank-premium: premium, bank-free: free}`

| # | Test | Expected | Validates |
|---|---|---|---|
| T1 | avi1 (premium) hits `premium-only` EP | 200 admitted | Q3: runtime permission enforced |
| T2 | avi2 (free) hits `premium-only` EP | 403 forbidden | Q3: lower-tier blocked |
| T3 | avi1 runs agent; check DB | `external_user_id = avi1.sub` on run + task | Q1: sourced from JWT, not header |
| T4 | avi2 supplies avi1's `context_id` | Empty history (different external_user_id) | Q4: ownership enforced |
| T5 | Caller sends `X-External-User: avi1` header while using avi2 JWT | Run attributed to avi2's sub | Q1: header ignored |
| T6 | avi1 token used at EP in different tenant | 403 | Tenant isolation |

**Regression tests to add (before implementation):**
- `TestHistory_CrossUser_Denied` — two users, same tenant, same `context_id`; user B gets empty history.
- `TestHistory_ServiceToken_ExternalUserIsolation` — service token + two different `external_user_id` values; histories don't bleed.

---

## Implementation Phase — Bounded Scope

### Phase 1 — History ownership (prerequisite for any end-user work)

**Schema:**
```sql
ALTER TABLE them.tasks ADD COLUMN external_user_id TEXT;
CREATE INDEX idx_tasks_external_user ON them.tasks(external_user_id) WHERE external_user_id IS NOT NULL;
```

**Go changes:**
- `LoadHistory` / `LoadSummary`: add `AND ($3 = '' OR t.external_user_id = $3)` filter.
- `resolveRootTaskID` / `WriteMessage`: accept and store `external_user_id`.
- `RuntimeIdentity`: add `ExternalUserID string` (empty for internal users and un-scoped service tokens).
- `domain.Run`: add `ExternalUserID string`.
- `recorder.CreateRun`: write `external_user_id` when present.

**Source of `external_user_id`:**
- Internal user JWT: empty string (no per-user history enforcement, consistent with today).
- Service token: from a trusted `X-External-User` header (bank authenticates with service token; bank asserts customer ID — acceptable trust model).
- Approach B JWT: from `sub` claim extracted by `JWKSAuthenticator` (no header trust needed).

**Acceptance criteria:**
- `TestHistory_CrossUser_Denied` passes.
- `TestHistory_ServiceToken_ExternalUserIsolation` passes.
- Existing history tests unchanged.

---

### Phase 2 — Entry point principal guard

**Schema:**
```sql
ALTER TABLE them.entry_points ADD COLUMN allowed_principals TEXT NOT NULL DEFAULT 'internal'
  CHECK (allowed_principals IN ('internal', 'external', 'both'));
```

**Go changes:**
- `epconfig.EPConfig`: add `AllowedPrincipals string`.
- `Admit`: if `AllowedPrincipals == 'internal'` and `tokenInfo.IsExternal → 403`; vice versa.

**Acceptance criteria:**
- Internal JWT rejected at `external`-only EP.
- External JWT rejected at `internal`-only EP.
- `both` EP accepts either.

---

### Phase 3 — JWKSAuthenticator (Approach B)

**Schema:**
```sql
CREATE TABLE them.tenant_runtime_config (
    tenant_id    UUID PRIMARY KEY REFERENCES them.tenants(id) ON DELETE CASCADE,
    jwks_uri     TEXT NOT NULL,
    issuer       TEXT NOT NULL,
    audience     TEXT NOT NULL,
    claim_mappings JSONB NOT NULL DEFAULT '{}',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Go changes:**
- New `JWKSAuthenticator` implementing `transport.Authenticator`.
- Fetches JWKS from tenant config (cached, refreshed on key rotation).
- Validates `iss`, `aud`, `exp`. Extracts `sub` → `ExternalUserID`. Maps group claims → `Permissions`.
- Sets `UserID = 0` sentinel. `TenantID` from EP URL path, not JWT.
- Wired in alongside `auth.Cache` via a dispatching `CompositeAuthenticator` that tries JWKS first when the tenant has `runtime_config`.

**Acceptance criteria (Keycloak test plan T1–T6 all pass).**

---

## Build Order

```
Phase 1: external_user_id on tasks/runs + history ownership filter   (closes Q4)
Phase 2: allowed_principals on entry_points                           (closes Q3)
Phase 3: JWKSAuthenticator + tenant_runtime_config                   (enables Approach B end-to-end)
```

Phases 1 and 2 improve the existing system and can ship before Phase 3.
Phase 3 is the full Approach B implementation.
