# End-User Authentication at Runtime Entry Points
# Status: Design proposal — not yet implemented
# Last updated: 2026-09-08

---

## The Problem

The-M currently supports two kinds of principals at WS/SSE entry points:

1. **Service API token** — opaque bearer token tied to a platform user, used by the tenant's backend app. The-M sees the token but not the individual customer behind it.
2. **Internal JWT** — issued after login (local or SSO), used by the internal team in the dashboard.

Neither supports **end-user identity at runtime** — the bank's retail customers who interact with the agentic app. Today the-M cannot attribute a run to a specific customer, enforce per-customer limits, or isolate their history.

---

## Two Approaches Evaluated

### Approach A — Reuse OIDC Foundation

End users authenticate via the tenant's IdP (same Keycloak/Okta flow as internal team members). The-M issues them an internal JWT. They hit WS/SSE with it.

**What works today:** Full OIDC flow is proven end-to-end. Group mapping → role assignment exists. Tenant membership enforcement exists.

**Critical gaps:**
- Every OIDC user gets a persistent `auth_service.users` row + membership row. 100k bank customers = 100k rows in the platform DB. Not viable at scale.
- `OIDCCallback` always redirects to `/` (the-M dashboard). End users would land on the admin UI. Post-auth redirect is hardcoded.
- OIDC flow sets HttpOnly cookies — the bank's app cannot receive a bearer token programmatically.
- No per-user rate limiting. No `external_user_id` on runs. No history isolation by user.

**Verdict:** Workable for small trusted teams. Not viable for large end-user populations.

---

### Approach B — Bank-Issued Token Passthrough / Exchange

The bank authenticates its own customers and presents a bank-issued JWT to the-M at the entry point. The-M validates it (via the tenant's JWKS URI) and maps it to a runtime identity — no the-M account created.

**What works today:** Bearer token extraction at WS/SSE already exists. `Authenticator` interface is injectable — a new implementation can be dropped in without restructuring the handlers.

**Critical gaps:**
- No JWKS-based token validator exists. `auth.Cache` only validates against internal `them.access_tokens`.
- Nowhere to store per-tenant JWKS URI / audience config (`idp_config` stores OIDC client config, not token validation config).
- `RuntimeIdentity.UserID` is typed `int64` referencing `auth_service.users.id`. A foreign JWT has no such ID — a sentinel value (0) or nullable field is needed.
- No `external_user_id` column on `them.runs` — per-customer attribution is impossible.
- `TenantID` is sourced from the JWT claim today; a bank JWT carries none — must come from the EP path.

**Verdict:** Cleaner architecture for end users. Higher build effort. Avoids DB bloat entirely.

---

## Recommendation: Hybrid

Use **Approach B as the target** for end users, with **Approach A optionally available** for trusted small teams who want their users to have dashboard access.

The two paths share the same runtime entry point — the difference is which `Authenticator` validates the token.

---

## Minimum Missing Pieces (in priority order)

### 1. `external_user_id` on runs (1–2 days)
Add `external_user_id TEXT` (nullable) to `them.runs`. Flow it through `domain.Run`, `recorder.CreateRun`, and `RuntimeIdentity`. The tenant app passes it as a session header or query param — the-M stores it on every run.

This alone unlocks: per-customer usage reports, audit trail, history isolation (filter by external_user_id).

**Schema change:** `ALTER TABLE them.runs ADD COLUMN external_user_id TEXT;`

---

### 2. Per-tenant JWKS validator (3–4 days)
New `Authenticator` implementation: `JWKSAuthenticator`. Configured per tenant with:
- `jwks_uri` — where to fetch the public keys
- `audience` — expected `aud` claim
- `issuer` — expected `iss` claim

Config stored in a new `them.tenant_runtime_config` JSONB column or table.

`JWKSAuthenticator` validates the bank JWT, extracts `sub` as `external_user_id`, uses `tenant_id` from the EP path, sets `RuntimeIdentity.UserID = 0` (sentinel for "external user, no platform account").

---

### 3. Management access guard at EP level (1 day)
Today an internal JWT with `role=viewer` can hit a WS/SSE entry point. An end-user JWT (if Approach A is used) would do the same. The EP itself should enforce: "this entry point accepts end-user tokens only" or "internal team only" via an EP-level policy flag.

Add `allowed_principals ENUM ('internal', 'external', 'both')` to `them.entry_points`.

---

### 4. Per-user rate limiting (2–3 days)
Extend the gate to key rate limits on `external_user_id` when present, in addition to the existing per-token and per-EP caps. Config: `max_runs_per_user_per_minute` on `them.tenant_quotas`.

---

### 5. History isolation (1 day — mostly policy)
`context_id` is already caller-supplied. Convention: the tenant app sets `context_id = hash(external_user_id + session_id)`. The-M enforces no cross-context leakage (already true today — context_id is a filter, not a shared namespace).

Document this as a required convention. No code change needed unless we want the-M to enforce it.

---

## What We Deliberately Do NOT Build

- No the-M accounts for end users (Approach B avoids `auth_service.users` rows entirely)
- No dashboard access for end users (gate: `UserID=0` sentinel → no JWT issuance → no cookie)
- No SSO redirect flow for end users (they authenticate at the bank, not at the-M)
- No token revocation beyond JWT expiry (bank controls token lifetime; short-lived tokens are sufficient)

---

## Open Questions Before Starting

1. **Who supplies `external_user_id`?** The tenant app in a header/param, or derived from the bank JWT `sub` claim automatically? (Recommend: JWT `sub` when Approach B; explicit header when service token.)
2. **Should `external_user_id` be opaque or meaningful?** Opaque hash is safer (no PII in the-M DB). Meaningful (email) is easier to debug. Tenant's choice — the-M stores whatever is passed.
3. **Short-lived tokens vs long-lived with revocation?** For Approach B, short-lived JWTs (5–15 min, refreshed by the bank's app) avoids building a revocation path.
4. **One JWKS config per tenant or per entry point?** Per-tenant is simpler; per-EP allows a tenant to accept tokens from different IdPs on different entry points.

---

## Build Order

```
1. external_user_id on runs          ← unblocks attribution immediately, no auth change
2. Per-tenant JWKS validator          ← core of Approach B
3. Management access guard at EP      ← safety before opening to end users
4. Per-user rate limiting             ← after validator is working
5. History isolation convention       ← document + optional enforcement
```

Step 1 can ship independently and is useful even if only service tokens are used today.
Steps 2–4 are a single cohesive wave.
