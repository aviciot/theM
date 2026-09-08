# End-User Authentication at Runtime Entry Points
# Status: Design proposal — security-reviewed, not yet implemented
# Last updated: 2026-09-08

---

## The Problem

The-M currently supports two principals at WS/SSE entry points:

1. **Service API token** — opaque bearer token tied to a platform user. The-M sees the token but not the individual customer behind it.
2. **Internal JWT** — issued after login (local or SSO), used by the internal team in the dashboard.

Neither supports **end-user identity at runtime** — the bank's retail customers who interact with the agentic app. Today the-M cannot attribute a run to a specific customer, enforce per-customer limits, or isolate their history.

---

## Security Questions — Findings Against Code

### Q1. Can a caller spoof `external_user_id` or roles?

**What exists today:**
- `transport.go:79` comment explicitly states: "All fields come from trusted authentication state; none may be supplied by the client in a request header or body."
- `ws/handler.go:213` — only `Authorization: Bearer <token>` or `?token=` query param are accepted. No identity headers are read.
- `lifecycle.go:337–349` — `TenantID` and `ApplicationID` in `domain.Run` come exclusively from `resolvedCfg` (server-side DB). The comment reads: "Never from request data — enforced by not reading those fields from req."
- `lifecycle.go:388–394` — `Start()` overwrites `input.TenantID`, `input.ApplicationID`, `input.RunID`, `input.ContextID` from the handle (server-resolved). Client-supplied values are discarded.
- `middleware.go:162` — `BearerTenantMiddleware` comment: "TenantID is NEVER read from request headers or query parameters."

**What is missing / exploitable:**
- `external_user_id` does not exist yet. The plan proposed accepting it as a header or param (plan section 1, "The tenant app passes it as a session header or query param"). **This is the exploit**: if `external_user_id` is accepted from a caller-supplied header, any caller can claim to be any user. It must be extracted exclusively from the validated bank JWT (`sub` claim), never from headers or params.
- No `external_user_id` field exists in `RuntimeIdentity`, `domain.Run`, `session.SessionInfo`, or any DB column today — so nothing is exploitable yet, but the design must enforce server-side sourcing before shipping.

**Fix required:** `external_user_id` must be extracted from the validated JWT `sub` claim inside the new `JWKSAuthenticator` — never accepted as a caller-supplied value.

---

### Q2. Is the token verified for the specific tenant and application?

**What exists today:**
- `TokenInfo` (`token_cache.go:23`) carries `AppID int64` sourced from `them.access_tokens.application_id` (DB lookup, trusted). This is the application the token was created for.
- `lifecycle.go:184` — `epLoader.Load(ctx, req.TenantID, req.AppSlug, req.EPSlug)` resolves the entry point config server-side. The `AppID` in `resolvedCfg` comes from the DB row matching the tenant+app+EP path.
- `epconfig.CheckAccess` (`lifecycle.go:209`) checks blocked tokens and blocked users, but does **not** verify that `tokenInfo.AppID == resolvedCfg.AppID`. A service token created for App A can currently be used at App B's entry point within the same tenant.

**What is missing / exploitable:**
- No explicit check that the bearer token's `app_id` matches the entry point's `app_id`. A tenant admin who creates a token for App A can use it at App B if both are in the same tenant. This is a **current gap** for service tokens.
- For Approach B (bank JWT), `TenantID` is not in the bank's JWT — it must be sourced from the EP URL path (`/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws`) which is already how `ws/handler.go:173` resolves it via `slugResolver`. That path is trusted (URL, not header).

**Fix required:**
1. Add `AppID` check in `CheckAccess` or `Admit`: if `tokenInfo.AppID != 0 && tokenInfo.AppID != resolvedCfg.AppID → 403`.
2. For Approach B, validate that the JWKS config belongs to the tenant resolved from the EP path, not from any claim in the bank JWT.

---

### Q3. Is there a runtime role concept separate from dashboard roles?

**What exists today:**
- `Claims` (`jwt.go:50`) carries `Roles []string` — these are dashboard roles (admin/member/viewer/super_admin).
- `TokenInfo` (`token_cache.go:23`) carries `Permissions []string` — this is the service token permissions array, stored in `them.access_tokens`. Currently empty/unused in the Lifecycle path.
- `RuntimeIdentity` (`transport.go:79`) has no `Role` or `Permission` field.
- `epconfig.CheckAccess` does not check any role — it only checks blocked token hashes and blocked user IDs.
- No EP-level role requirement exists. An end user with `role=viewer` (from OIDC) can call any WS/SSE EP in their tenant.

**What is missing / exploitable:**
- Dashboard roles (admin/member/viewer) and runtime permissions are completely separate today, but neither is enforced at the EP level. The `Permissions` field on `TokenInfo` exists but is never checked in the admission pipeline.
- For Approach B, the bank JWT will carry business roles (`premium`, `free`, `enterprise`). These have no mapping to the-M concepts today. There is no per-EP role requirement mechanism.

**Fix required:**
- Add `allowed_principals` (internal/external/both) to `them.entry_points` and check it in `Admit`.
- Add optional `required_permission` to `them.entry_points` — checked against `tokenInfo.Permissions` at admission. This is how the bank can lock a "premium-only" EP.
- Business role claims from the bank JWT (e.g. `premium`) should map to `permissions` in `TokenInfo` via a tenant-configured claim mapping — analogous to group mappings for OIDC.

---

### Q4. What prevents user A from accessing user B's history via `context_id`?

**What exists today:**
- `history/pgx.go:79–87` — `LoadHistory` filters by both `context_id` AND `tenant_id`: `WHERE t.context_id = $1::uuid AND ($2 = '' OR t.tenant_id = $2::uuid)`. Tenant isolation exists.
- `context_id` is client-supplied (`ws/handler.go:319`): "if clientContextID != '' { handle.ContextID = clientContextID }". Any authenticated caller can supply any UUID as `context_id`.
- The history query filters only by `context_id + tenant_id`. There is **no user ownership check** — no `user_id` or `external_user_id` filter on `task_messages` or `tasks`.
- `them.tasks.tenant_id` is set on write. No `user_id` column exists on `them.tasks` or `them.task_messages`.

**What is missing / exploitable:**
- **Active exploit:** User A (authenticated, same tenant) can supply user B's `context_id` in the first WS message and load B's entire conversation history. The only guard is that A must know B's `context_id` UUID — but if B's `context_id` is deterministic (e.g. `hash(user_id)`), A can derive it.
- The plan's proposed mitigation ("caller sets `context_id = hash(external_user_id + session_id)`") is a convention, not an enforcement. It relies on the bank's app generating unique context IDs — the server never validates ownership.

**Fix required:**
- Add `external_user_id TEXT` to `them.tasks` (alongside `tenant_id`). Set it from the validated JWT `sub` on `WriteMessage`/`CreateTask`.
- Extend `LoadHistory` to filter by `external_user_id` when present: `AND ($3 = '' OR t.external_user_id = $3)`.
- This makes cross-user history access impossible even if A knows B's `context_id`.

---

### Q5. Token validation (passthrough) vs token exchange — which does the plan propose?

**What exists today:**
- The existing OIDC callback (`oidc.go:269`) is **token exchange**: it receives a short-lived authorization `code` from Keycloak, exchanges it for an `id_token` (server-to-server call to Keycloak's token endpoint), validates the `id_token`, then issues a new the-M HS256 JWT. The bank's token never touches the runtime.
- `auth.Cache.Validate` is **passthrough validation**: it validates the opaque service token against the internal DB (L1→L2→DB). No new token is issued.
- The plan's "Approach B" proposes **runtime passthrough validation**: the bank JWT is validated at the WS/SSE entry point on each request using the tenant's JWKS URI. No the-M JWT is issued.

**Comparison:**

| | Passthrough (validate at EP) | Exchange (issue the-M JWT first) |
|---|---|---|
| Latency | JWKS fetch on first use (cached) | Extra round-trip before WS connect |
| Token lifetime | Bank controls expiry | The-M controls expiry |
| Revocation | Only via JWT expiry (short-lived recommended) | The-M can revoke immediately |
| DB rows created | Zero (no the-M account) | Requires the-M session/token row |
| User attribution | `sub` claim → `external_user_id` | Must carry `external_user_id` in the-M JWT |
| Trust boundary | The-M trusts bank's JWKS | The-M independently validates and re-signs |

**Recommendation: Passthrough** for Approach B.
Exchange adds latency, a pre-connect HTTP round-trip, and requires the-M to create a token row — defeating the "no DB bloat" goal. Short-lived bank JWTs (5–15 min, refreshed by the bank's app) make revocation unnecessary. The `Authenticator` interface is already injectable — a `JWKSAuthenticator` drops in without changing the WS/SSE handlers.

---

## Keycloak Test Plan (two users, covers all 5 questions)

### Setup

Use existing Keycloak `them` realm. Create two users with different roles:

```
User: avi1  / group: bank-premium  → the-M runtime permission: "premium"
User: avi2  / group: bank-free     → the-M runtime permission: "free"
```

Create a Keycloak client `bank-runtime` with:
- Protocol: `openid-connect`
- Access type: `confidential`
- Groups mapper: emits `groups` claim in ID token

Configure avi-test tenant with a `runtime_config`:
```json
{
  "jwks_uri": "http://them-keycloak:8080/auth/keycloak/realms/them/protocol/openid-connect/certs",
  "issuer": "http://localhost:8088/auth/keycloak/realms/them",
  "audience": "bank-runtime",
  "claim_mappings": { "bank-premium": "premium", "bank-free": "free" }
}
```

### Test Cases

| # | Test | Expected result | Validates |
|---|---|---|---|
| T1 | avi1 (premium) hits `premium-only` EP | 200 admitted | Q2: token scoped to correct app; Q3: runtime role enforced |
| T2 | avi2 (free) hits `premium-only` EP | 403 forbidden | Q3: runtime role blocks lower-tier users |
| T3 | avi1 runs agent; verify run attributed to avi1's `sub` | `external_user_id = avi1_sub` in DB | Q1: no spoofing; sub from JWT not header |
| T4 | avi2 supplies avi1's `context_id` in WS message | Sees empty history (different external_user_id) | Q4: history ownership enforced |
| T5 | Caller sends `X-External-User: avi1` header while using avi2's JWT | Run attributed to avi2's sub | Q1: header ignored, JWT sub wins |
| T6 | avi1 JWT used at App B's EP (token issued for App A) | 403 forbidden | Q2: app-scoped token check |

---

## Updated Missing Pieces (security-hardened)

### 1. `external_user_id` — server-side only (1–2 days)
- Add `external_user_id TEXT` to `them.runs`, `them.tasks`.
- Source: exclusively from validated JWT `sub` claim in `JWKSAuthenticator`. Never from headers/params.
- Flow through `domain.Run`, `recorder.CreateRun`, `session.SessionInfo`, `LoadHistory` filter.

### 2. Per-tenant JWKS validator (3–4 days)
- New `JWKSAuthenticator` implementing `transport.Authenticator`.
- Config: `jwks_uri`, `issuer`, `audience`, `claim_mappings` stored per tenant in `them.tenant_runtime_config`.
- Extracts `sub` → `external_user_id`; maps group claims → `Permissions` in `TokenInfo`.
- Sets `UserID = 0` (sentinel); `TenantID` from EP path, not JWT.

### 3. App-scoped token check in `CheckAccess` (0.5 days)
- If `tokenInfo.AppID != 0 && tokenInfo.AppID != resolvedCfg.AppID` → 403.
- Closes the cross-app token reuse gap for existing service tokens too.

### 4. `allowed_principals` on entry points (1 day)
- Add `allowed_principals TEXT DEFAULT 'internal'` to `them.entry_points`.
- Checked in `Admit`: `external` EPs reject internal JWTs; `internal` EPs reject external JWTs.

### 5. History ownership via `external_user_id` (1 day)
- Add `external_user_id TEXT` to `them.tasks`.
- `LoadHistory` filters by `external_user_id` when present.
- Closes the context_id cross-user history exploit.

### 6. Per-user rate limiting (2–3 days)
- Gate keys on `external_user_id` when present.
- Config: `max_runs_per_user_per_minute` on `them.tenant_quotas`.

---

## Build Order

```
1. external_user_id on runs+tasks (server-side from JWT sub)  ← closes Q1 + Q4
2. App-scoped token check in CheckAccess                       ← closes Q2
3. allowed_principals on entry_points                          ← closes Q3
4. JWKSAuthenticator + tenant runtime config                   ← enables Approach B
5. Per-user rate limiting                                      ← after validator works
```

Steps 1–3 improve security for the existing service-token path and can ship before Approach B is built.
