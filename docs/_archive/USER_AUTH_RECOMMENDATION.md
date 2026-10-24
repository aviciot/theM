# User Authentication & Role Enforcement — Recommendation
# Date: 2026-10-22
# Status: Design proposal

---

## Executive Summary

**Current gaps**:
1. **LLM Gateway**: No user tracking → can't enforce per-user quotas
2. **Application Canvas**: Has `access_policy` modes, but roles not enforced

**Recommendation**: Use **existing auth infrastructure** — no major changes needed, just wire up what's already built.

---

## What Already Works

### 1. Entry Points Have Four Auth Modes

**Location**: `entry_points.access_policy` (JSONB)

| Mode | Description | When to use | User tracking |
|---|---|---|---|
| `public` | No auth required | Public demos, webhooks | No |
| `token` | Bearer token (service) | Closed agents, backend services | No (unless `is_backend=true` + `X-External-User`) |
| `user_jwt` | the-M user JWT | Dashboard users, employee apps | **Yes** — `user_id` + `role` in JWT |
| `external_jwt` | Bank JWKS RS256 | Customer-facing SSO apps | **Yes** — `external_user_id` from token `sub` |

**Code**: `go/internal/epconfig/epconfig.go:60-79`

**Current support**:
- ✅ **WebSocket/SSE**: All 4 modes work (tested, in production)
- ❌ **LLM Gateway**: Only `token` mode supported (no JWT validation yet)

### 2. User Context Already Flows Through

**When using `user_jwt` mode**:
- JWT validated by `JWTMiddleware` → `Claims` in context
- `Claims.UserID` (int64), `Claims.Roles` ([]string), `Claims.TenantID` extracted
- Stored in `runs.user_id`, `run_usage.user_id` for audit

**When using `token` mode with `is_backend=true`**:
- Backend sends `X-External-User: alice@example.com`
- Stored in `runs.external_user_id` for audit
- **Gateway doesn't support this yet** (SSE/WS do)

### 3. Roles Already in DB

**Table**: `auth_service.roles`

| Role | Rate Limit | Daily Cost | Dashboard |
|---|---|---|---|
| `super_admin` | 10k rpm | $1k/day | admin |
| `developer` | 5k rpm | $100/day | view |
| `analyst` | 1k rpm | $50/day | view |
| `viewer` | 100 rpm | $10/day | view |
| `end_user` | 1k rpm | $10/day | none |

**Users table** has `role_id` FK → enforcement just needs to read this.

---

## Recommendation by Use Case

### Use Case 1: LLM Gateway for Closed Agents (Service Tokens)

**Example**: Monitoring system calls Gateway to summarize logs.

**Current**: Works fine (bearer token, no user)

**Auth mode**: `token` (default)

**Change needed**: **None** — this is the right pattern for service-to-service.

**Quota enforcement**: Per-token or per-app (not per-user)

---

### Use Case 2: LLM Gateway for Dashboard Users (Human Users)

**Example**: Dashboard "Ask AI" button where Alice asks a question.

**Current problem**: Dashboard uses a shared service token → no user tracking.

**Recommendation**: **Switch to `user_jwt` mode**

#### Implementation (Option A — Recommended)

1. **Entry point**: Create Gateway EP with `access_policy: {"mode": "user_jwt"}`
2. **Frontend**: Send user's JWT as `Authorization: Bearer <jwt>` to Gateway
3. **Gateway handler**: Add JWT middleware before Gateway handler
   - `JWTMiddleware` → validates JWT → extracts `Claims`
   - `Claims.UserID` → load user's role from DB
   - Enforce `roles.rate_limit` and `roles.cost_limit_daily`

**Code changes needed**:
```go
// go/internal/llmgateway/handler.go

// After line 113 (Extract auth), add:
claims, hasJWT := auth.ClaimsFromCtx(r.Context())
if hasJWT {
    // User JWT mode — load role and enforce per-user limits
    userID := claims.UserID
    role := h.loadUserRole(ctx, userID) // NEW: load from users table
    
    // Check per-user rate limit (roles.rate_limit)
    if h.rateLimiter.CheckUserLimit(ctx, userID, role.RateLimit) {
        writeGatewayError(w, http.StatusTooManyRequests, "rate_limit_exceeded", 
            fmt.Sprintf("User rate limit: %d rpm", role.RateLimit))
        return
    }
    
    // Check per-user daily cost (roles.cost_limit_daily)
    dailySpend := h.dal.SumUserDailySpend(ctx, userID)
    if dailySpend >= role.CostLimitDaily {
        writeGatewayError(w, http.StatusPaymentRequired, "quota_exceeded",
            fmt.Sprintf("Daily cost limit: $%.2f", role.CostLimitDaily))
        return
    }
    
    // Add user_id to request record for audit
    rec.UserID = userID
}
```

**Pros**:
- ✅ Per-user quotas work
- ✅ Audit trail: "alice spent $45 today"
- ✅ Role-based limits enforced
- ✅ No breaking changes (existing service tokens keep working)

**Cons**:
- ⚠️ Frontend must proxy user JWT (security review needed)
- ⚠️ JWT expires in 1 hour (needs refresh flow)

---

### Use Case 3: LLM Gateway for External Users (SSO)

**Example**: Bank customer logs in via Okta, calls Gateway.

**Current**: External JWT validation exists for SSE/WS, not Gateway.

**Recommendation**: **Add `external_jwt` mode to Gateway**

#### Implementation

1. **Entry point**: `access_policy: {"mode": "external_jwt"}`
2. **Frontend**: Send bank-issued JWT as `Authorization: Bearer <jwt>`
3. **Gateway handler**: Validate JWT via JWKS
   - Extract `sub` claim → `external_user_id`
   - Store in `gateway_requests.external_user_id` (NEW column)
   - Apply quota from `tenant_runtime_config` (not role-based — external users don't have the-M roles)

**Code**: Reuse `go/internal/auth/jwks.go` (already exists for SSE)

---

### Use Case 4: Application Canvas Entry Points

**Current**: Entry points have `access_policy`, but **no role enforcement**.

**Problem**: Anyone with a valid JWT can access any app — no "developer-only" apps.

**Recommendation**: **Add role requirements to Entry Points**

#### Schema Change

```sql
-- Migration XXX: Add required_role to entry_points
ALTER TABLE them.entry_points 
ADD COLUMN required_role TEXT;

-- Optional: FK to roles table (would require moving roles to them schema)
-- For now: free text, match against JWT Claims.Roles[]
```

#### Enforcement (WebSocket/SSE handlers)

```go
// go/internal/sse/handler.go (line ~150, after JWT validation)

if cfg.RequiredRole != "" {
    claims, _ := auth.ClaimsFromCtx(r.Context())
    if !contains(claims.Roles, cfg.RequiredRole) {
        // User doesn't have required role
        writeError(w, http.StatusForbidden, "forbidden", 
            fmt.Sprintf("This app requires role: %s", cfg.RequiredRole))
        return
    }
}
```

**Use cases**:
- "Production LLM Gateway" app requires `developer` role
- "Read-only Analytics" app allows `analyst` or higher
- "Public Demo" app has no requirement (anyone can access)

---

## What About Shared Tokens?

**Problem**: Frontend app has ONE service token, used by 10 users.

**Current**: All 10 users share the token's quota (can't distinguish Alice from Bob).

**Solution**: Use **`X-External-User` header** (already supported in SSE/WS)

### How It Works

1. **Frontend backend** (trusted service) calls Gateway with:
   - `Authorization: Bearer <service_token>` (has `is_backend=true`)
   - `X-External-User: alice@example.com` (asserts end-user identity)

2. **Gateway** checks `token.IsBackend`:
   - If `true` → accept `X-External-User` header
   - If `false` → ignore header (security: untrusted clients can't forge user)

3. **Track per-user**: Store `external_user_id` in `gateway_requests`

4. **Enforce per-user quotas**:
   - Query: `SUM(cost_usd) FROM gateway_requests WHERE external_user_id='alice@example.com' AND date=today()`
   - Compare to limit from `tenant_runtime_config.user_quotas` (new JSONB field)

**Code changes**:
```go
// go/internal/llmgateway/handler.go (after line 158)

var externalUserID string
if gwClient.IsBackend {
    // Trusted backend token can assert external user
    externalUserID = r.Header.Get("X-External-User")
}

rec.ExternalUserID = externalUserID // NEW field in RequestRecord

// Enforce per-external-user quota (if policy defines it)
if externalUserID != "" && policy.ExternalUserQuota > 0 {
    dailySpend := h.dal.SumExternalUserDailySpend(ctx, tenantID, externalUserID)
    if dailySpend >= policy.ExternalUserQuota {
        writeGatewayError(w, http.StatusPaymentRequired, "user_quota_exceeded", 
            "External user daily limit exceeded")
        return
    }
}
```

---

## Summary Table

| Use Case | Auth Mode | User Tracking | Quota Scope | Code Changes |
|---|---|---|---|---|
| **Closed agent** (monitoring system) | `token` | No | Per-token or per-app | None (already works) |
| **Dashboard user** (employee) | `user_jwt` | Yes (user_id + role) | Per-user (role-based) | Add JWT middleware to Gateway |
| **SSO user** (bank customer) | `external_jwt` | Yes (external_user_id) | Per-external-user (tenant config) | Add JWKS validation to Gateway |
| **Shared token + backend** (frontend proxy) | `token` (is_backend=true) | Yes (X-External-User header) | Per-external-user | Add X-External-User support to Gateway |
| **Role-restricted app** | Any mode + `required_role` | Depends on mode | Depends on mode | Add role check to WS/SSE handlers |

---

## Implementation Plan

### Phase 1: LLM Gateway — User JWT Support (1-2 days)

1. **Schema**: Add `gateway_requests.user_id` (nullable)
   ```sql
   ALTER TABLE them.gateway_requests ADD COLUMN user_id INTEGER 
       REFERENCES auth_service.users(id) ON DELETE SET NULL;
   CREATE INDEX idx_gateway_requests_user_id ON them.gateway_requests(user_id);
   ```

2. **Code**: Wire JWT middleware into Gateway handler
   - Location: `go/cmd/them/main.go` (route setup)
   - Conditionally apply `JWTMiddleware` based on EP `access_policy.mode`

3. **Handler**: Read `Claims` from context, populate `user_id`
   - Location: `go/internal/llmgateway/handler.go:159`

4. **Test**: Create Gateway EP with `user_jwt` mode, call with JWT

5. **Verify**: `gateway_requests.user_id` populated in DB

### Phase 2: LLM Gateway — Role-Based Limits (2-3 days)

6. **Load role**: Query `users JOIN roles` at request time
   - Cache in Redis (key: `them:user:{id}:role`, TTL: 5 min)
   - Invalidate on role change (pub/sub: `them:role:changed:{user_id}`)

7. **Rate limit**: Implement `RateLimiter.CheckUserLimit(userID, rpm)`
   - Redis key: `rl:user:{user_id}:minute:{timestamp}`
   - INCR + EXPIRE pattern (same as token rate limit)

8. **Cost limit**: Implement `SumUserDailySpend(userID)`
   - Query: `SELECT SUM(cost_usd) FROM gateway_requests WHERE user_id=? AND created_at >= today()`
   - Cache result for 1 minute (spending changes slowly)

9. **Enforce**: Return 429 (rate) or 402 (cost) when limit exceeded

10. **Test**: User with `viewer` role (100 rpm, $10/day) hits limit

### Phase 3: LLM Gateway — X-External-User Support (1 day)

11. **Schema**: Add `gateway_requests.external_user_id TEXT`
    ```sql
    ALTER TABLE them.gateway_requests ADD COLUMN external_user_id TEXT;
    CREATE INDEX idx_gateway_requests_external_user ON them.gateway_requests(external_user_id);
    ```

12. **Handler**: Check `IsBackend`, read header, enforce quota
    - Location: `go/internal/llmgateway/handler.go:158`

13. **Policy**: Add `tenant_runtime_config.external_user_daily_quota` (JSONB)

14. **Test**: Backend token + `X-External-User` header → user tracked

### Phase 4: Entry Points — Required Role (1 day)

15. **Schema**: Add `entry_points.required_role TEXT`

16. **Loader**: Include in `EPConfig` struct
    - Location: `go/internal/epconfig/epconfig.go:118`

17. **Handlers**: Check role before allowing WS/SSE connection
    - Location: `go/internal/sse/handler.go:150`

18. **Test**: Create app with `required_role: "developer"`, verify `analyst` blocked

### Phase 5: Documentation & Migration (1 day)

19. **Docs**: Update `docs/GOVERNANCE_POLICY_DESIGN.md` with user-level policies

20. **Migration guide**: How to switch existing apps from `token` to `user_jwt`

21. **Admin UI**: Add "Required Role" field to Entry Point editor

22. **Dashboard**: Show user's role + quota usage (e.g. "$45 / $100 used today")

---

## Decision Required

**Choose one**:

### Option A: Full Implementation (Recommended)
- Do all 5 phases
- Total time: ~7-9 days
- Result: Complete per-user quotas + role-based access

### Option B: Minimal (Gateway user tracking only)
- Do Phase 1 only
- Total time: 1-2 days
- Result: Audit trail (know which user made request), no enforcement

### Option C: App-level only (no Gateway changes)
- Do Phase 4 only
- Total time: 1 day
- Result: Role-restricted apps, but Gateway still token-based

---

## Risks & Mitigations

### Risk 1: JWT in Frontend
**Problem**: Proxying user JWT to Gateway exposes session token to client

**Mitigation**: 
- Use short-lived JWTs (1 hour, already default)
- Rotate on refresh
- Monitor for token leakage (unexpected IP/user-agent)

### Risk 2: Token Refresh UX
**Problem**: JWT expires → user gets 401 mid-session

**Mitigation**:
- Frontend auto-refreshes JWT before expiry
- Gateway returns `Retry-After` header on 401

### Risk 3: Role Change Lag
**Problem**: Admin changes user's role → cached quota still wrong

**Mitigation**:
- Pub/sub invalidation (already used for token revocation)
- Max cache TTL: 5 minutes (acceptable lag)

### Risk 4: Migration Breaking Existing Integrations
**Problem**: Switching existing apps to `user_jwt` breaks current callers

**Mitigation**:
- Make it **opt-in** per Entry Point
- Default stays `token` (backward compatible)
- Only new apps use `user_jwt`

---

## Open Questions

1. **Should role limits be per-tenant or global?**
   - Example: Developer at Tenant A has separate quota from Developer at Tenant B?
   - Recommendation: Per-tenant (users are tenant-scoped already)

2. **What if external user doesn't have a the-M role?**
   - External JWT users don't exist in `auth_service.users`
   - Recommendation: Apply default quota from `tenant_runtime_config`, not role-based

3. **Should we enforce role hierarchy?** (super_admin > developer > analyst)
   - Recommendation: Not yet — keep it simple (exact match only)

4. **What about API key authentication?** (users.api_key_hash)
   - Recommendation: Treat same as JWT (load role, enforce limits)

---

**Created**: 2026-10-22  
**Status**: Design proposal  
**Next**: Choose Option A/B/C, start Phase 1
