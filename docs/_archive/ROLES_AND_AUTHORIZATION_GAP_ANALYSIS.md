# Roles and Authorization — Gap Analysis
# Date: 2026-10-22
# Status: Current implementation vs. requirements

---

## Executive Summary

**Current state**: The-M has a role system (`auth_service.roles`) with per-role limits (rate_limit, cost_limit_daily, token_expiry), but **roles are NOT enforced** in the LLM Gateway or Application Canvas. Users are identified by **token only** — no user context, no role-based access control, no per-user usage limits.

**Gap**: You cannot enforce per-user LLM usage limits, role-based app access, or user-specific quotas because the system doesn't track which **user** is making the request — only which **token**.

**Required for**: Per-user usage tracking, role-based application access, SSO integration with role mapping, audit trails by user.

---

## Current Architecture

### 1. Role Schema (auth_service.roles)

**Table**: `auth_service.roles`

| Field | Type | Purpose | Example |
|---|---|---|---|
| `id` | INTEGER | Primary key | 1 |
| `name` | VARCHAR(50) | Role name | `developer`, `analyst`, `end_user` |
| `description` | TEXT | Human description | "Developer with full access" |
| `mcp_access` | TEXT[] | Allowed MCP servers | `{github,slack}` |
| `tool_restrictions` | JSONB | Tool access rules | `{"allowed": ["web_search"]}` |
| `dashboard_access` | VARCHAR(20) | UI permission level | `admin`, `view`, `none` |
| `rate_limit` | INTEGER | Requests/min | 5000 |
| `cost_limit_daily` | NUMERIC(10,2) | USD/day cap | 100.00 |
| `token_expiry` | INTEGER | JWT TTL (seconds) | 3600 |

**Existing roles** (seeded in DB):
- `super_admin` — 10k rpm, $1k/day, admin UI
- `developer` — 5k rpm, $100/day, view UI
- `analyst` — 1k rpm, $50/day, view UI
- `viewer` — 100 rpm, $10/day, view UI
- `end_user` — 1k rpm, $10/day, no UI

### 2. User → Role Mapping (auth_service.users)

**Each user has ONE role**:
```sql
SELECT id, username, email, role_id FROM auth_service.users;
```

**Foreign key**: `users.role_id → roles.id`

### 3. JWT Claims (UI users)

**Access tokens issued by them-auth-go** include role:

```json
{
  "sub": "42",
  "username": "alice",
  "name": "Alice Developer",
  "role": "developer",
  "tenant_id": "uuid-tenant",
  "exp": 1735000000,
  "iat": 1734996400,
  "type": "access"
}
```

**Go bridge reads this JWT** and extracts:
- `Claims.UserID` (from sub)
- `Claims.Username`
- `Claims.Roles` (normalized from `role` string → `[]string`)
- `Claims.TenantID`

**Location**: `go/internal/auth/jwt.go:50` (`Claims` struct)

**Middleware**: `JWTMiddleware` validates JWT → stores `*Claims` in context

**Context retrieval**: `auth.ClaimsFromCtx(ctx)` → `(*Claims, bool)`

### 4. Bearer Tokens (API clients — LLM Gateway)

**Table**: `them.access_tokens`

**Columns**:
- `id` (serial PK)
- `token` (UNIQUE, sha256 hash of actual token)
- `tenant_id` (UUID, application owner)
- `application_id` (FK to `applications.id`, optional)
- `permissions` (TEXT[], e.g. `{llm_gateway}`)
- `is_backend` (BOOLEAN, trusted server)
- `user_id` (FK to `auth_service.users.id`, **can be NULL**)
- `created_at`, `expires_at`, `revoked`, `last_used_at`

**Token validation** (Gateway):
1. Extract bearer token from `Authorization` header
2. Hash token → lookup in cache (L1 in-process, L2 Redis, L3 PostgreSQL)
3. Store `TokenInfo` in context (no user_id, no role)

**TokenInfo struct** (`go/internal/auth/token_cache.go:23`):
```go
type TokenInfo struct {
    TokenID     int64    `json:"token_id"`
    AppID       int64    `json:"app_id,omitempty"`
    TenantID    string   `json:"tenant_id,omitempty"`
    Permissions []string `json:"permissions"`
    IsBackend   bool     `json:"is_backend,omitempty"`
    CreatedAt   int64    `json:"created_at"`
    ExpiresAt   int64    `json:"expires_at,omitempty"`
}
```

**NO `user_id` or `role` in TokenInfo** — the token is the identity.

---

## Where Roles Are Used (Current)

### ✅ Used

1. **JWT token expiry** — `auth_service.roles.token_expiry` controls access token TTL
   - Location: `go/internal/authserver/jwt.go:99` (`IssueAccessToken`)
   - Applied at login time

2. **Dashboard access enforcement** — `roles.dashboard_access` (admin/view/none)
   - Location: Frontend UI (Next.js, reads JWT claims)
   - Controls what UI pages are visible

3. **MCP server access** (planned) — `roles.mcp_access` (array of allowed servers)
   - Not yet enforced in runtime (MCP supervisor doesn't check this)

### ❌ NOT Used (Gap)

1. **LLM Gateway rate limiting** — NOT per-role, NOT per-user
   - Current: Per-token only (no user context)
   - Gap: Cannot enforce `roles.rate_limit` (5000 rpm for developer, 100 rpm for viewer)

2. **LLM Gateway cost limits** — NOT per-role, NOT per-user
   - Current: No daily spend limit enforcement
   - Gap: Cannot enforce `roles.cost_limit_daily` ($100/day for developer, $10/day for end_user)

3. **Application Canvas access control** — NOT role-based
   - Current: Anyone with a valid JWT can create/run apps
   - Gap: Cannot restrict app creation to `developer` role, prevent `end_user` from admin actions

4. **Run usage tracking by user** — NOT tracked
   - Current: Runs linked to `application_id` (tenant boundary), NOT `user_id`
   - Gap: Cannot answer "how much did alice spend today?" or "which user hit the quota?"

5. **Audit logs by user** — Partially tracked
   - `them.audit_logs` has `user_id` FK, but LLM Gateway requests don't populate it
   - Gap: Cannot audit which user made which Gateway request

---

## The Token vs. User Problem

### Current Gateway Auth Flow

```
1. Client sends: Authorization: Bearer <token>
2. Middleware hashes token → lookup access_tokens table
3. Context gets TokenInfo (token_id, tenant_id, permissions)
4. Handler uses tenant_id + client_id (derived from token)
5. Request recorded in gateway_requests (tenant_id, client_id, token_hash)
6. NO user_id, NO role checked
```

**What's missing**:
- No `user_id` in the request context
- No role-based rate limiting
- No per-user spend tracking

### Why Tokens Don't Have Users

**Design choice**: API tokens (bearer) are **not user-scoped** — they represent an **application** or **service**, not a person.

**Example**: A frontend app has ONE token shared by all users hitting its backend.

**Problem**: If 10 users share one token, you cannot:
- Apply different rate limits per user (developer vs. end_user)
- Track which user exceeded their daily spend
- Audit which user made the request

---

## SSO Integration Gap

### How SSO Works (OIDC)

1. User logs in via external IdP (Google, Okta, Azure AD)
2. IdP returns ID token with user claims (email, name, groups)
3. Auth service maps IdP groups → the-M roles
   - Location: `go/internal/authserver/oidc.go` (UpsertOIDCUser)
4. JWT issued with `role` claim (e.g. `developer`)

**Current state**: SSO login works, JWT contains role.

**Gap**: After login, if user calls LLM Gateway with a **bearer token** (not JWT), role is lost — because bearer tokens don't carry user context.

---

## Solutions (Decision Required)

### Option 1: Add user_id to bearer tokens (recommended)

**Change**:
1. When creating a token in UI, require `user_id` (current user who clicked "Generate Token")
2. Store `user_id` in `access_tokens.user_id` (currently nullable)
3. Add `user_id` to `TokenInfo` struct
4. Load user's role from DB at token validation time (JOIN users → roles)
5. Enforce role-based limits in Gateway handler

**Pros**:
- Enables per-user quotas even with shared tokens
- Audit trail: "user X made request Y with token Z"
- Role-based access control in Gateway

**Cons**:
- Token now represents a user, not just an app — breaks "service token" pattern
- If token is shared, all users share one quota (Alice's $100/day = Bob's $100/day if they share token)

### Option 2: Require JWT for LLM Gateway (breaking change)

**Change**:
1. Deprecate bearer tokens for Gateway
2. Require JWT (user session token) instead
3. Read user_id + role from JWT claims
4. Enforce per-user limits

**Pros**:
- Clean separation: JWT = user, bearer = service
- No DB lookup for role (it's in the JWT)

**Cons**:
- **BREAKING**: All existing Gateway clients must switch to JWT auth
- Frontend must proxy user JWT to Gateway (exposes user session to client)
- Short-lived JWTs (1 hour) = token refresh burden

### Option 3: Hybrid — support both, enforce differently

**Change**:
1. If request has JWT → use user_id + role from claims → apply per-user limits
2. If request has bearer → use token-level limits (no per-user tracking)
3. Admin can choose: "this app requires user auth" (JWT only) or "service token" (bearer only)

**Pros**:
- Backward compatible
- Flexible: closed agents use bearer, user-facing apps use JWT

**Cons**:
- Complex: two auth paths, two limit models
- Confusing: "why is my rate limit different when I use a token vs. login?"

### Option 4: Scoped tokens with user claim (OAuth2 pattern)

**Change**:
1. New token type: `user_token` (bearer token scoped to a user)
2. Created by user action ("Generate my personal token")
3. Carries `user_id` + `role` in DB
4. Separate from `service_token` (no user, higher quota, backend-only)

**Pros**:
- Clear distinction: service vs. user tokens
- Supports both use cases (closed agents + user apps)

**Cons**:
- More code: two token validation paths
- Migration: existing tokens need classification (service or user?)

---

## Recommendations

### Immediate (Phase 1)

1. **Add `user_id` to TokenInfo** (Option 1 lite):
   - Modify `access_tokens.user_id` to be **REQUIRED** for new tokens
   - Update `TokenInfo` struct to include `UserID int64`
   - Load `user_id` from DB at token validation (already querying `access_tokens`)
   - **Do NOT enforce limits yet** — just track user_id in requests

2. **Track user_id in gateway_requests**:
   - Add `user_id` column to `gateway_requests` table (nullable for now)
   - Populate from `TokenInfo.UserID` in Gateway handler
   - Enables audit: "user X made Y requests today"

3. **Document the gap**:
   - Update `docs/GOVERNANCE_POLICY_DESIGN.md` with user-level policy requirements
   - Add to `docs/STATUS.md`: "Per-user quotas not enforced (tracked but not gated)"

### Next (Phase 2)

4. **Load role at token validation time**:
   - Modify `TokenQuerier.QueryToken` to JOIN `users → roles`
   - Add `Role string` to `TokenInfo`
   - Cache role in Redis L2 (invalidate on role change via pub/sub)

5. **Enforce role-based rate limits**:
   - New middleware: `RoleBasedRateLimitMiddleware`
   - Check `roles.rate_limit` against Redis counter (per user_id, not token)
   - Return 429 if exceeded

6. **Enforce daily cost limits**:
   - Query: `SUM(cost_usd) FROM gateway_requests WHERE user_id = ? AND created_at > today()`
   - Compare to `roles.cost_limit_daily`
   - Return 402 Payment Required if exceeded

### Future (Phase 3)

7. **Application-level role requirements**:
   - Add `applications.required_role` (e.g. "developer")
   - Check at app run time: if `user.role < required_role` → deny
   - Example: "Production LLM app requires developer role, not end_user"

8. **SSO group → role auto-assignment**:
   - Already implemented in OIDC handler (`oidc.go:UpsertOIDCUser`)
   - Document the mapping in admin UI

9. **MCP server access enforcement**:
   - Check `roles.mcp_access` in MCP supervisor before allowing server attach
   - Example: "analyst role cannot use github MCP server"

---

## Current Usage Limit Enforcement (What Works Today)

### ✅ Enforced (Tenant-level)

1. **Application caps** (`applications.max_concurrent_sessions`)
   - Location: `go/internal/gate/gate.go` (admission gate)
   - Prevents exceeding per-app concurrency limit

2. **Token-based rate limiting** (per-token, not per-user)
   - Location: `go/internal/ratelimit/limiter.go`
   - Redis INCR counter per token hash

3. **Tenant-level spend tracking** (monthly budget)
   - Location: `go/internal/llmgateway/handler.go` (LoadPolicy → SumMonthlySpend)
   - Checks total tenant spend vs. policy.monthly_budget

### ❌ NOT Enforced (User-level)

1. **Per-user rate limit** (`roles.rate_limit`)
   - Would require: user_id in request context + Redis counter per user_id

2. **Per-user daily cost limit** (`roles.cost_limit_daily`)
   - Would require: `SUM(cost_usd) WHERE user_id = ? AND date = today()`

3. **Role-based app access** (e.g. only `developer` can create apps)
   - Would require: `users.role_id` check in `/admin/applications` POST handler

---

## Questions to Answer

### 1. Should bearer tokens represent users or services?

**Current**: Ambiguous (can be either, but no user tracking)

**Recommendation**: **Both** — introduce typed tokens:
- `service_token` — no user_id, high quota, backend-only (existing pattern)
- `user_token` — has user_id, role-based quota, user-facing apps

### 2. How do we enforce per-user limits with shared tokens?

**Problem**: If 5 users share one token (frontend app), one user's usage counts toward all.

**Options**:
- **A**: Require `X-External-User` header (trusted backend asserts user identity)
  - Already supported for `is_backend=true` tokens
  - Gateway reads header → overrides user_id from token
- **B**: Deprecate shared tokens, require per-user tokens
  - Breaking change for existing integrations
- **C**: Accept limitation — shared tokens = shared quota
  - Document clearly: "if you share a token, you share the limit"

**Recommendation**: **A** (X-External-User) for backend services, **user_token** for direct user access

### 3. What happens with SSO users who never create a token?

**Current**: SSO login → JWT → can use Dashboard

**Gap**: If they call LLM Gateway, they must:
- Either: Send JWT as `Authorization: Bearer <jwt>` (works, but JWT expires in 1h)
- Or: Generate a personal token (not currently linked to their user_id)

**Recommendation**: Support JWT auth in Gateway (Option 2 or 3)

---

## Next Steps

1. **Decision required**: Which option (1/2/3/4) for user-scoped limits?
2. **Schema change**: Add `gateway_requests.user_id` (nullable initially)
3. **Code change**: Populate `TokenInfo.UserID` from DB
4. **Test**: Verify user_id appears in `gateway_requests` table
5. **Document**: Update `docs/GOVERNANCE_POLICY_DESIGN.md` with chosen approach

---

**Created**: 2026-10-22  
**Status**: Analysis complete, decision pending  
**Next**: Choose token model + implement Phase 1
