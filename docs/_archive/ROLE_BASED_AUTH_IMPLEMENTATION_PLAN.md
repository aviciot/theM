# Role-Based Authorization — Implementation Plan
# Date: 2026-10-22
# Status: In Progress

---

## Overview

**Goal**: Enforce per-user quotas and role-based access control across Application Canvas and LLM Gateway.

**Current State**:
- ✅ Roles exist in DB (`auth_service.roles`) with `rate_limit` + `cost_limit_daily`
- ✅ Canvas (WebSocket/SSE) supports `user_jwt` mode (JWT validation works)
- ✅ Canvas tracks `user_id` in `runs` table
- ❌ Canvas doesn't enforce role-based limits
- ❌ Gateway only supports `token` auth (no user tracking)

**End State**:
- ✅ Canvas enforces role requirements per Entry Point
- ✅ Canvas enforces per-user rate/cost limits
- ✅ Gateway supports `user_jwt` mode
- ✅ Gateway enforces per-user rate/cost limits
- ✅ E2E tests confirm quotas work end-to-end

---

## Phases

### **Phase 1: Canvas Role Enforcement** ⏳ (2 days)

**Goal**: Add role requirements to Entry Points (e.g., "developer-only app")

#### 1.1. Schema Change

```sql
-- Migration 117: Add required_role to entry_points
ALTER TABLE them.entry_points 
ADD COLUMN required_role TEXT 
CHECK (required_role IN ('super_admin', 'developer', 'analyst', 'viewer', 'end_user'));

CREATE INDEX idx_entry_points_required_role ON them.entry_points(required_role);

COMMENT ON COLUMN them.entry_points.required_role IS 
'Minimum role required to access this EP. NULL = no role requirement.';
```

**Files**:
- `db/117_entry_point_required_role.sql` (new)

#### 1.2. EPConfig Loader

**Add field to EPConfig struct:**

```go
// go/internal/epconfig/epconfig.go (line ~118)

type EPConfig struct {
    // ... existing fields ...
    RequiredRole string // entry_points.required_role; empty = no requirement
}
```

**Update query:**

```go
// go/internal/epconfig/pgx.go (line ~40, SELECT statement)

// Add to SELECT:
ep.required_role,

// Add to scan (line ~80):
&cfg.RequiredRole,
```

**Files**:
- `go/internal/epconfig/epconfig.go`
- `go/internal/epconfig/pgx.go`

#### 1.3. RoleChecker Interface

**Create role lookup service:**

```go
// go/internal/execution/roles.go (NEW FILE)

package execution

import "context"

// RoleChecker loads a user's role from the database.
type RoleChecker interface {
    // GetUserRole returns the role name for the given user_id.
    // Returns empty string if user not found or no role assigned.
    GetUserRole(ctx context.Context, userID int64) (string, error)
}

// pgxRoleChecker queries auth_service.users JOIN roles.
type pgxRoleChecker struct {
    pool *pgxpool.Pool
}

func NewPgxRoleChecker(pool *pgxpool.Pool) RoleChecker {
    return &pgxRoleChecker{pool: pool}
}

func (rc *pgxRoleChecker) GetUserRole(ctx context.Context, userID int64) (string, error) {
    const q = `
        SELECT r.name 
        FROM auth_service.users u 
        JOIN auth_service.roles r ON u.role_id = r.id 
        WHERE u.id = $1
    `
    var roleName string
    err := rc.pool.QueryRow(ctx, q, userID).Scan(&roleName)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return "", nil // user exists but no role (shouldn't happen)
        }
        return "", fmt.Errorf("role query failed: %w", err)
    }
    return roleName, nil
}
```

**Files**:
- `go/internal/execution/roles.go` (new)

#### 1.4. Lifecycle Role Enforcement

**Wire RoleChecker into Lifecycle:**

```go
// go/internal/execution/lifecycle.go (line ~35, after jwtSecret)

type Lifecycle struct {
    // ... existing fields ...
    roleChecker RoleChecker // optional; nil = no role enforcement
}

// Add setter method (line ~80):
func (lc *Lifecycle) WithRoleChecker(rc RoleChecker) *Lifecycle {
    lc.roleChecker = rc
    return lc
}
```

**Enforce role in Admit (after user_jwt validation, line ~64):**

```go
// go/internal/execution/lifecycle.go (after AccessModeUser block)

// ── 3.7. Required role enforcement ───────────────────────────────────────
// If EP has a required_role, check the authenticated user's role.
// Only applies to AccessModeUser (user_jwt) — token/public modes skip this.
if resolvedCfg.AccessMode == epconfig.AccessModeUser && resolvedCfg.RequiredRole != "" {
    if lc.roleChecker == nil {
        lc.logger.Warn("execution: EP requires role but no RoleChecker configured",
            "ep_slug", req.EPSlug, "required_role", resolvedCfg.RequiredRole)
        return nil, admitErr(AdmitErrInternal)
    }
    userRole, roleErr := lc.roleChecker.GetUserRole(ctx, req.UserID)
    if roleErr != nil {
        lc.logger.Warn("execution: role lookup failed",
            "user_id", req.UserID, "error", roleErr)
        return nil, admitErr(AdmitErrInternal)
    }
    if userRole != resolvedCfg.RequiredRole {
        lc.logger.Debug("execution: user role insufficient",
            "user_id", req.UserID, "user_role", userRole, "required_role", resolvedCfg.RequiredRole)
        return nil, admitErr(AdmitErrForbidden)
    }
}
```

**Files**:
- `go/internal/execution/lifecycle.go`

#### 1.5. Wire Into Main

```go
// go/cmd/them/main.go (after line ~200, where Lifecycle is created)

roleChecker := execution.NewPgxRoleChecker(pools.Admin())
lifecycle.WithRoleChecker(roleChecker)
```

**Files**:
- `go/cmd/them/main.go`

#### 1.6. Tests

**Test file**: `go/internal/execution/lifecycle_role_test.go` (NEW)

**Test cases**:
- `TestLifecycle_RequiredRole_Pass` — user has developer role, EP requires developer → admit succeeds
- `TestLifecycle_RequiredRole_Deny` — user has viewer role, EP requires developer → 403
- `TestLifecycle_RequiredRole_NotRequired` — EP has no required_role → any user admitted
- `TestLifecycle_RequiredRole_PublicMode` — public EP with required_role → role check skipped

**Files**:
- `go/internal/execution/lifecycle_role_test.go` (new)

**Update**:
- `go/TEST_INDEX.md` (add 4 tests, update count)

#### 1.7. E2E Test (Test Runner)

**Create test scenario**: `48-role-enforcement-canvas`

**Setup**:
1. Create Entry Point with `access_policy: {"mode": "user_jwt"}`, `required_role: "developer"`
2. Create two users:
   - Alice (role: `developer`)
   - Bob (role: `viewer`)

**Test steps**:
```json
{
  "scenario_id": "48-role-enforcement-canvas",
  "name": "Canvas Role Enforcement",
  "n_users": 2,
  "steps": [
    {
      "id": "login_alice",
      "type": "login",
      "username": "alice",
      "password": "alice123"
    },
    {
      "id": "connect_alice_sse",
      "type": "sse_connect",
      "app_slug": "role-test-app",
      "ep_slug": "developer-only-ep",
      "message": "Hello from Alice",
      "expect_status": 200
    },
    {
      "id": "login_bob",
      "type": "login",
      "username": "bob",
      "password": "bob123"
    },
    {
      "id": "connect_bob_sse",
      "type": "sse_connect",
      "app_slug": "role-test-app",
      "ep_slug": "developer-only-ep",
      "message": "Hello from Bob",
      "expect_status": 403,
      "expect_error": "forbidden"
    }
  ]
}
```

**Expected**:
- Alice (developer) → 200 OK, run starts
- Bob (viewer) → 403 Forbidden

**Files**:
- `test-runner/scenarios/48-role-enforcement-canvas.json` (new)

**Run**:
```bash
curl -X POST http://10.55.125.43:3210/run \
  -H "Content-Type: application/json" \
  -d '{"scenario_id":"48-role-enforcement-canvas"}'
```

---

### **Phase 2: Canvas Per-User Quotas** ⏳ (3 days)

**Goal**: Enforce `roles.rate_limit` (rpm) and `roles.cost_limit_daily` (USD/day) for Canvas users

#### 2.1. Schema — No Changes Needed

Canvas already stores `user_id` in `runs` and `run_usage` tables ✅

#### 2.2. Role Data Loader

**Extend RoleChecker to return full role config:**

```go
// go/internal/execution/roles.go

type RoleConfig struct {
    Name             string
    RateLimit        int     // requests per minute
    CostLimitDaily   float64 // USD per day
    DashboardAccess  string
}

type RoleChecker interface {
    GetUserRole(ctx context.Context, userID int64) (string, error)
    GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error) // NEW
}

func (rc *pgxRoleChecker) GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error) {
    const q = `
        SELECT r.name, r.rate_limit, r.cost_limit_daily, r.dashboard_access
        FROM auth_service.users u 
        JOIN auth_service.roles r ON u.role_id = r.id 
        WHERE u.id = $1
    `
    var cfg RoleConfig
    err := rc.pool.QueryRow(ctx, q, userID).Scan(
        &cfg.Name, &cfg.RateLimit, &cfg.CostLimitDaily, &cfg.DashboardAccess,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("role config query failed: %w", err)
    }
    return &cfg, nil
}
```

**Cache role config in Redis** (TTL: 5 minutes):
- Key: `them:user:{user_id}:role`
- Value: JSON-encoded `RoleConfig`
- Invalidate on role change via pub/sub: `them:role:changed:{user_id}`

**Files**:
- `go/internal/execution/roles.go`

#### 2.3. User Rate Limiter

**Create per-user rate limiter:**

```go
// go/internal/ratelimit/user_limiter.go (NEW FILE)

package ratelimit

import (
    "context"
    "fmt"
    "time"
    "github.com/redis/rueidis"
)

// UserLimiter enforces per-user rate limits (requests per minute).
type UserLimiter struct {
    client rueidis.Client
}

func NewUserLimiter(client rueidis.Client) *UserLimiter {
    return &UserLimiter{client: client}
}

// CheckLimit returns true if user has exceeded their rate limit.
// rpm is the role's rate_limit (e.g., 5000 for developer).
func (ul *UserLimiter) CheckLimit(ctx context.Context, userID int64, rpm int) (bool, error) {
    if rpm <= 0 {
        return false, nil // 0 = unlimited
    }

    now := time.Now()
    minute := now.Truncate(time.Minute).Unix()
    key := fmt.Sprintf("rl:user:%d:minute:%d", userID, minute)

    cmd := ul.client.B().Incr().Key(key).Build()
    resp := ul.client.Do(ctx, cmd)
    count, err := resp.AsInt64()
    if err != nil {
        return false, fmt.Errorf("INCR failed: %w", err)
    }

    // Set TTL on first request in this minute
    if count == 1 {
        expireCmd := ul.client.B().Expire().Key(key).Seconds(90).Build()
        ul.client.Do(ctx, expireCmd)
    }

    return count > int64(rpm), nil
}
```

**Files**:
- `go/internal/ratelimit/user_limiter.go` (new)

#### 2.4. User Cost Tracker

**Query daily spend per user:**

```go
// go/internal/execution/quotas.go (NEW FILE)

package execution

import (
    "context"
    "fmt"
    "time"
    "github.com/jackc/pgx/v5/pgxpool"
)

type QuotaChecker struct {
    pool *pgxpool.Pool
}

func NewQuotaChecker(pool *pgxpool.Pool) *QuotaChecker {
    return &QuotaChecker{pool: pool}
}

// GetUserDailySpend returns total USD spent by user today.
func (qc *QuotaChecker) GetUserDailySpend(ctx context.Context, userID int64) (float64, error) {
    today := time.Now().UTC().Truncate(24 * time.Hour)
    const q = `
        SELECT COALESCE(SUM(cost_usd), 0)
        FROM them.run_usage
        WHERE user_id = $1 AND created_at >= $2
    `
    var total float64
    err := qc.pool.QueryRow(ctx, q, userID, today).Scan(&total)
    if err != nil {
        return 0, fmt.Errorf("daily spend query failed: %w", err)
    }
    return total, nil
}
```

**Cache result for 1 minute** (spending changes slowly):
- Key: `them:user:{user_id}:spend:daily:{YYYY-MM-DD}`
- Value: float64 USD
- TTL: 60 seconds

**Files**:
- `go/internal/execution/quotas.go` (new)

#### 2.5. Lifecycle Quota Enforcement

**Add quota check before gate admission:**

```go
// go/internal/execution/lifecycle.go (after role check, before gate.Check)

// ── 4. User quota enforcement (AccessModeUser only) ──────────────────────
if resolvedCfg.AccessMode == epconfig.AccessModeUser && req.UserID > 0 {
    if lc.roleChecker != nil && lc.quota != nil {
        roleConfig, roleErr := lc.roleChecker.GetRoleConfig(ctx, req.UserID)
        if roleErr != nil {
            lc.logger.Warn("execution: role config lookup failed", "user_id", req.UserID, "error", roleErr)
            return nil, admitErr(AdmitErrInternal)
        }
        if roleConfig != nil {
            // Rate limit check
            exceeded, rlErr := lc.quota.CheckUserRateLimit(ctx, req.UserID, roleConfig.RateLimit)
            if rlErr != nil {
                lc.logger.Warn("execution: rate limit check failed", "user_id", req.UserID, "error", rlErr)
            } else if exceeded {
                return nil, admitErr(AdmitErrRateLimited)
            }

            // Cost limit check
            dailySpend, csErr := lc.quota.GetUserDailySpend(ctx, req.UserID)
            if csErr != nil {
                lc.logger.Warn("execution: daily spend check failed", "user_id", req.UserID, "error", csErr)
            } else if dailySpend >= roleConfig.CostLimitDaily {
                return nil, admitErr(AdmitErrQuotaExceeded)
            }
        }
    }
}
```

**Add quota enforcer interface:**

```go
// go/internal/execution/lifecycle.go (line ~30)

type QuotaEnforcer interface {
    CheckUserRateLimit(ctx context.Context, userID int64, rpm int) (bool, error)
    GetUserDailySpend(ctx context.Context, userID int64) (float64, error)
}
```

**Files**:
- `go/internal/execution/lifecycle.go`

#### 2.6. Wire Quota Enforcer

```go
// go/cmd/them/main.go

quotaChecker := execution.NewQuotaChecker(pools.Admin())
userLimiter := ratelimit.NewUserLimiter(cache.Client())

// Combine into QuotaEnforcer adapter
quotaEnforcer := &execution.CombinedQuotaEnforcer{
    RateLimiter: userLimiter,
    CostTracker: quotaChecker,
}

lifecycle.WithQuotaEnforcer(quotaEnforcer)
```

**Files**:
- `go/cmd/them/main.go`
- `go/internal/execution/quota_enforcer.go` (new adapter)

#### 2.7. E2E Test

**Scenario**: `49-quota-enforcement-canvas`

**Setup**:
- Alice (developer): 5000 rpm, $100/day
- Bob (viewer): 100 rpm, $10/day

**Test**:
1. Bob sends 101 requests → 101st gets 429
2. Alice sends 200 requests → all succeed
3. Force Bob's daily spend to $10.01 (mock or pre-populate) → 402 Payment Required

**Files**:
- `test-runner/scenarios/49-quota-enforcement-canvas.json`

---

### **Phase 3: Gateway User JWT Support** ⏳ (3 days)

**Goal**: Add `user_jwt` mode to LLM Gateway (copy from Canvas)

#### 3.1. Schema Change

```sql
-- Migration 118: Add user_id to gateway_requests
ALTER TABLE them.gateway_requests 
ADD COLUMN user_id INTEGER 
REFERENCES auth_service.users(id) ON DELETE SET NULL;

CREATE INDEX idx_gateway_requests_user_id ON them.gateway_requests(user_id);
```

**Files**:
- `db/118_gateway_user_id.sql` (new)

#### 3.2. Gateway Entry Point Type

**Add `gateway` EP type** (already in schema, just needs handler wiring):

```sql
-- Already exists in entry_points.entry_point_type CHECK constraint
-- Just needs route registration
```

#### 3.3. JWT Middleware for Gateway

**Copy JWT validation from Lifecycle:**

```go
// go/internal/llmgateway/auth.go (NEW FILE)

package llmgateway

import (
    "context"
    "net/http"
    "github.com/aviciot/them/internal/auth"
)

// GatewayAuthMiddleware validates JWT for user_jwt mode EPs.
func GatewayAuthMiddleware(jwtSecret []byte, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        authHeader := r.Header.Get("Authorization")
        if authHeader == "" {
            writeGatewayError(w, http.StatusUnauthorized, "unauthorized", "missing authorization header")
            return
        }
        
        token := strings.TrimPrefix(authHeader, "Bearer ")
        claims, err := auth.ValidateHS256JWT(token, jwtSecret)
        if err != nil {
            writeGatewayError(w, http.StatusUnauthorized, "unauthorized", "invalid JWT")
            return
        }
        
        // Store claims in context
        ctx := auth.WithClaims(r.Context(), claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**Files**:
- `go/internal/llmgateway/auth.go` (new)

#### 3.4. Update Handler to Use user_id

```go
// go/internal/llmgateway/handler.go (line ~160, after extracting gwClient)

var userID int64
claims, hasJWT := auth.ClaimsFromCtx(r.Context())
if hasJWT {
    userID = claims.UserID
    logger = logger.With("user_id", userID)
}

rec.UserID = userID // NEW field in RequestRecord
```

**Update RequestRecord struct:**

```go
// go/internal/llmgateway/dal.go (line ~20)

type RequestRecord struct {
    // ... existing fields ...
    UserID int64 // NEW: the-M user ID from JWT (0 if not user_jwt mode)
}
```

**Update WriteRequest SQL:**

```go
// go/internal/llmgateway/pgx.go (INSERT statement)

const insertRequestSQL = `
    INSERT INTO them.gateway_requests (
        tenant_id, client_id, token_hash, user_id,  -- ADD user_id
        provider, model_requested, model_served,
        status, http_status, error_code,
        tokens_in, tokens_out, cost_usd,
        latency_ms, ttfb_ms, streamed
    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
    RETURNING id::text
`

// Add to args:
sqlNullInt64(rec.UserID),
```

**Files**:
- `go/internal/llmgateway/handler.go`
- `go/internal/llmgateway/dal.go`
- `go/internal/llmgateway/pgx.go`

#### 3.5. Route Registration

**Wire JWT middleware conditionally:**

```go
// go/cmd/them/main.go (Gateway route setup, line ~350)

// For user_jwt mode Gateway EPs, wrap with JWT middleware
if cfg.AccessMode == epconfig.AccessModeUser {
    gwHandler = llmgateway.GatewayAuthMiddleware(jwtSecret, gwHandler)
}
```

**Files**:
- `go/cmd/them/main.go`

#### 3.6. E2E Test

**Scenario**: `50-gateway-user-jwt`

**Setup**:
- Create Gateway EP with `access_policy: {"mode": "user_jwt"}`
- Login as Alice (developer)

**Test**:
```bash
# Get JWT
JWT=$(curl -s -X POST http://localhost:8701/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"alice123"}' | jq -r '.access_token')

# Call Gateway with JWT
curl -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# Verify user_id in gateway_requests table
docker exec them-postgres psql -U them -d them -c \
  "SELECT user_id FROM them.gateway_requests ORDER BY created_at DESC LIMIT 1"
# Expected: user_id = alice's ID (not NULL)
```

**Files**:
- `test-runner/scenarios/50-gateway-user-jwt.json`

---

### **Phase 4: Gateway Per-User Quotas** ⏳ (2 days)

**Goal**: Enforce role-based limits in Gateway (reuse Phase 2 logic)

#### 4.1. Add Quota Checks to Handler

```go
// go/internal/llmgateway/handler.go (after user_id extraction, before policy checks)

if userID > 0 && h.roleChecker != nil {
    roleConfig, roleErr := h.roleChecker.GetRoleConfig(r.Context(), userID)
    if roleErr != nil {
        logger.Warn("role config lookup failed", "error", roleErr)
        writeGatewayError(w, http.StatusInternalServerError, "internal_error", "")
        return
    }
    
    if roleConfig != nil {
        // Rate limit
        exceeded, _ := h.userLimiter.CheckLimit(r.Context(), userID, roleConfig.RateLimit)
        if exceeded {
            writeGatewayError(w, http.StatusTooManyRequests, "rate_limit_exceeded",
                fmt.Sprintf("User rate limit: %d rpm", roleConfig.RateLimit))
            rec.Status = "rate_limited"
            h.dal.WriteRequest(r.Context(), rec)
            return
        }
        
        // Cost limit
        dailySpend, _ := h.quotaChecker.GetUserDailySpend(r.Context(), userID)
        if dailySpend >= roleConfig.CostLimitDaily {
            writeGatewayError(w, http.StatusPaymentRequired, "quota_exceeded",
                fmt.Sprintf("Daily cost limit: $%.2f", roleConfig.CostLimitDaily))
            rec.Status = "quota_exceeded"
            h.dal.WriteRequest(r.Context(), rec)
            return
        }
    }
}
```

**Wire dependencies:**

```go
// go/internal/llmgateway/handler.go (Handler struct)

type Handler struct {
    // ... existing fields ...
    roleChecker   execution.RoleChecker        // NEW
    userLimiter   *ratelimit.UserLimiter       // NEW
    quotaChecker  *execution.QuotaChecker      // NEW
}

// go/cmd/them/main.go (wire into Gateway)

gwHandler.WithRoleChecker(roleChecker)
gwHandler.WithUserLimiter(userLimiter)
gwHandler.WithQuotaChecker(quotaChecker)
```

**Files**:
- `go/internal/llmgateway/handler.go`
- `go/cmd/them/main.go`

#### 4.2. E2E Test

**Scenario**: `51-gateway-quota-enforcement`

**Setup**:
- Bob (viewer): 100 rpm, $10/day

**Test**:
1. Bob sends 101 requests → 101st gets 429
2. Bob's daily spend reaches $10 → next request gets 402

**Verification**:
```sql
SELECT COUNT(*), status 
FROM them.gateway_requests 
WHERE user_id = <bob_user_id> 
  AND created_at > now() - interval '1 minute'
GROUP BY status;

-- Expected:
-- 100 | ok
-- 1   | rate_limited
```

**Files**:
- `test-runner/scenarios/51-gateway-quota-enforcement.json`

---

### **Phase 5: X-External-User Support** 🔜 (1 day)

**Goal**: Backend tokens can assert external user identity

#### 5.1. Schema Change

```sql
-- Migration 119: Add external_user_id to gateway_requests
ALTER TABLE them.gateway_requests 
ADD COLUMN external_user_id TEXT;

CREATE INDEX idx_gateway_requests_external_user ON them.gateway_requests(external_user_id);
```

#### 5.2. Handler Logic

```go
// go/internal/llmgateway/handler.go (after token validation)

var externalUserID string
if gwClient.IsBackend {
    externalUserID = r.Header.Get("X-External-User")
}
rec.ExternalUserID = externalUserID
```

#### 5.3. External User Quotas

**Add to tenant_runtime_config:**

```json
{
  "external_user_daily_quota": 50.00  // USD per external user per day
}
```

**Enforcement** (same as user quotas, but query by `external_user_id`).

**Files**:
- `db/119_gateway_external_user.sql`
- `go/internal/llmgateway/handler.go`
- `go/internal/execution/quotas.go` (add GetExternalUserDailySpend)

---

## Progress Tracking

| Phase | Status | Files Changed | Tests Added | E2E Scenario |
|---|---|---|---|---|
| **Phase 1: Canvas Role Enforcement** | ⏳ Not Started | 0/7 | 0/4 | 48 |
| **Phase 2: Canvas Per-User Quotas** | ⏳ Not Started | 0/6 | 0/3 | 49 |
| **Phase 3: Gateway User JWT** | ⏳ Not Started | 0/5 | 0/2 | 50 |
| **Phase 4: Gateway Quotas** | ⏳ Not Started | 0/2 | 0/1 | 51 |
| **Phase 5: External Users** | 🔜 Deferred | 0/3 | 0/1 | 52 |

**Legend**:
- ⏳ Not Started
- 🟡 In Progress
- ✅ Complete
- 🔜 Deferred (Phase 5 optional)

---

## Handover Instructions

**If context runs out mid-implementation, use this to resume:**

### Quick Context

**Current branch**: `main`  
**Last commit**: `fab43a86` (docs: role-based authorization gap analysis)  
**Test runner**: http://10.55.125.43:3210/

**Goal**: Add role-based quotas to Canvas and Gateway

**What exists**:
- ✅ Canvas supports `user_jwt` mode (see `go/internal/execution/lifecycle.go:37-64`)
- ✅ `auth_service.roles` has `rate_limit` + `cost_limit_daily`
- ❌ No enforcement (role checks or quotas)

**What to implement**: Follow phases in this doc (`docs/ROLE_BASED_AUTH_IMPLEMENTATION_PLAN.md`)

### Resume Checklist

**Before starting**:
1. Read `docs/ROLE_BASED_AUTH_IMPLEMENTATION_PLAN.md` (this file)
2. Check Progress Tracking table above — see which phase is current
3. Run `git log --oneline -10` — see what was committed since `fab43a86`
4. Check test status: `cd go && go test ./...`

**For each phase**:
1. ✅ Schema migration (if needed) → apply to local DB
2. ✅ Code changes (follow file list in phase)
3. ✅ Unit tests (must pass before commit)
4. ✅ E2E test (create scenario JSON, run via test-runner)
5. ✅ Commit (update progress table in this doc)
6. ✅ Update `docs/CURRENT.md` with progress

**E2E test template**:
```bash
# Run scenario
curl -X POST http://10.55.125.43:3210/run \
  -H "Content-Type: application/json" \
  -d '{"scenario_id":"XX-scenario-name"}'

# Check result
curl http://10.55.125.43:3210/history/<run_id>
```

### Files to Read First

1. `docs/ROLE_BASED_AUTH_IMPLEMENTATION_PLAN.md` (this file)
2. `docs/ROLES_AND_AUTHORIZATION_GAP_ANALYSIS.md` (current state)
3. `docs/USER_AUTH_RECOMMENDATION.md` (design rationale)
4. `go/internal/execution/lifecycle.go` (Canvas auth flow)
5. `go/internal/llmgateway/handler.go` (Gateway handler)

### Key Decisions Made

1. **Canvas already has user_jwt** — just add role enforcement
2. **Gateway needs full user auth** — copy from Canvas Lifecycle
3. **Use existing test-runner** for E2E validation (not pytest or Go integration tests)
4. **Cache role config in Redis** (5 min TTL) to reduce DB load
5. **Phase 5 is optional** (external users, can defer)

### Common Commands

```bash
# Apply migration
docker cp db/117_entry_point_required_role.sql them-postgres:/tmp/
docker exec them-postgres psql -U them -d them -f /tmp/117_entry_point_required_role.sql

# Run Go tests
cd go && go test ./internal/execution/... -v

# Run E2E test
curl -X POST http://10.55.125.43:3210/run \
  -H "Content-Type: application/json" \
  -d '{"scenario_id":"48-role-enforcement-canvas"}'

# Commit
git add <files>
git commit -m "phase X: <description>"
git push origin main
```

---

## Test Runner E2E Scenarios

### Overview

**Test runner**: http://10.55.125.43:3210/  
**Scenarios path**: `test-runner/scenarios/`

**Why E2E tests matter**:
- Unit tests check logic, but can't verify auth flow end-to-end
- E2E confirms: JWT → role check → quota check → request admitted/denied
- Catches integration bugs (wrong middleware order, missing wiring)

### Scenario Structure

```json
{
  "scenario_id": "XX-name",
  "name": "Human-readable name",
  "n_users": 2,
  "steps": [
    {
      "id": "step1",
      "type": "login",
      "username": "alice",
      "password": "alice123"
    },
    {
      "id": "step2",
      "type": "sse_connect",
      "app_slug": "test-app",
      "ep_slug": "test-ep",
      "message": "Hello",
      "expect_status": 200
    }
  ]
}
```

**Step types** (supported):
- `login` — POST /auth/login, get JWT
- `sse_connect` — GET /sse/orchestrate/{app}/{ep}
- `ws_connect` — WebSocket connect
- `gateway_request` — POST /{tenant}/llm/v1/chat/completions
- `wait` — Sleep N seconds
- `query_db` — Run SQL query, check result

### Phase-Specific Scenarios

#### Phase 1: `48-role-enforcement-canvas.json`

**Tests**: EP with `required_role: "developer"`

**Steps**:
1. Login as Alice (developer) → JWT
2. Connect to `developer-only-ep` → **200 OK**
3. Login as Bob (viewer) → JWT
4. Connect to `developer-only-ep` → **403 Forbidden**

**Verification**:
```sql
SELECT COUNT(*) FROM them.runs WHERE user_id = <alice_id>;  -- 1
SELECT COUNT(*) FROM them.runs WHERE user_id = <bob_id>;    -- 0
```

#### Phase 2: `49-quota-enforcement-canvas.json`

**Tests**: Rate limit (100 rpm) and cost limit ($10/day)

**Steps**:
1. Login as Bob (viewer, 100 rpm, $10/day)
2. Send 101 SSE requests (loop)
   - Requests 1-100: **200 OK**
   - Request 101: **429 Too Many Requests**
3. Mock Bob's daily spend to $10.01 (pre-populate `run_usage`)
4. Send request → **402 Payment Required**

**Verification**:
```sql
SELECT status FROM them.runs WHERE user_id = <bob_id> ORDER BY created_at DESC LIMIT 1;
-- Expected: 'failed' (quota exceeded)
```

#### Phase 3: `50-gateway-user-jwt.json`

**Tests**: Gateway accepts JWT, stores `user_id`

**Steps**:
1. Login as Alice → JWT
2. POST to Gateway with `Authorization: Bearer <jwt>`
3. Check `gateway_requests.user_id` → **alice's ID**

**Verification**:
```sql
SELECT user_id FROM them.gateway_requests ORDER BY created_at DESC LIMIT 1;
-- Expected: <alice_user_id> (not NULL)
```

#### Phase 4: `51-gateway-quota-enforcement.json`

**Tests**: Gateway enforces rate + cost limits

**Steps**:
1. Login as Bob (viewer, 100 rpm, $10/day)
2. Send 101 Gateway requests
   - Request 101: **429**
3. Mock daily spend to $10.01
4. Send request → **402**

**Verification**:
```sql
SELECT COUNT(*), status 
FROM them.gateway_requests 
WHERE user_id = <bob_id> AND created_at > now() - interval '1 minute'
GROUP BY status;

-- Expected:
-- 100 | ok
-- 1   | rate_limited
```

### Running E2E Tests

**Manual run**:
```bash
curl -X POST http://10.55.125.43:3210/run \
  -H "Content-Type: application/json" \
  -d '{"scenario_id":"48-role-enforcement-canvas"}'

# Response:
{
  "run_id": "abc-123",
  "status": "running"
}

# Check result:
curl http://10.55.125.43:3210/history/abc-123

# Expected:
{
  "status": "completed",
  "results": [
    {"step_id": "connect_alice_sse", "ok": true},
    {"step_id": "connect_bob_sse", "ok": true, "status": 403}
  ]
}
```

**Automated suite** (future):
```bash
# Run all role/quota tests
for scenario in 48 49 50 51; do
  curl -X POST http://10.55.125.43:3210/run \
    -H "Content-Type: application/json" \
    -d "{\"scenario_id\":\"${scenario}-*\"}"
done
```

---

## Definition of Done (Per Phase)

### Phase 1: Canvas Role Enforcement ✅

- [x] Migration 117 applied (`entry_points.required_role` exists)
- [x] `EPConfig.RequiredRole` field added
- [x] `RoleChecker` interface + `pgxRoleChecker` implemented
- [x] Lifecycle enforces role in `Admit` (after user_jwt validation)
- [x] 4 unit tests pass (`lifecycle_role_test.go`)
- [x] E2E scenario 48 passes (Alice admitted, Bob denied)
- [x] `TEST_INDEX.md` updated (+4 tests)
- [x] `docs/CURRENT.md` updated (Phase 1 complete)
- [x] Committed and pushed

### Phase 2: Canvas Per-User Quotas ✅

- [x] `RoleChecker.GetRoleConfig` implemented
- [x] `UserLimiter` implemented (Redis INCR per user per minute)
- [x] `QuotaChecker.GetUserDailySpend` implemented (SUM run_usage)
- [x] Lifecycle enforces rate + cost limits in `Admit`
- [x] 3 unit tests pass (rate limit, cost limit, unlimited role)
- [x] E2E scenario 49 passes (Bob hits 429 at 101st request, 402 at $10)
- [x] `TEST_INDEX.md` updated (+3 tests)
- [x] Committed and pushed

### Phase 3: Gateway User JWT ✅

- [x] Migration 118 applied (`gateway_requests.user_id` exists)
- [x] `GatewayAuthMiddleware` implemented (JWT validation)
- [x] `RequestRecord.UserID` field added
- [x] `WriteRequest` inserts `user_id`
- [x] 2 unit tests pass (JWT valid, JWT invalid)
- [x] E2E scenario 50 passes (user_id stored in DB)
- [x] `TEST_INDEX.md` updated (+2 tests)
- [x] Committed and pushed

### Phase 4: Gateway Quotas ✅

- [x] Handler checks role config before request
- [x] Rate limit enforced (429 when exceeded)
- [x] Cost limit enforced (402 when exceeded)
- [x] 1 unit test passes (quota enforcement)
- [x] E2E scenario 51 passes (Bob hits 429 and 402)
- [x] `TEST_INDEX.md` updated (+1 test)
- [x] Committed and pushed

### Phase 5: External Users (Optional) 🔜

- [ ] Migration 119 applied (`gateway_requests.external_user_id` exists)
- [ ] Handler reads `X-External-User` header (only if `IsBackend=true`)
- [ ] External user quota enforcement implemented
- [ ] E2E scenario 52 passes

---

## Rollback Plan

**If a phase breaks production**:

### Phase 1 Rollback (Canvas Roles)

1. **Disable role enforcement**:
   ```sql
   UPDATE them.entry_points SET required_role = NULL;
   ```

2. **Revert code** (before redeploying):
   ```bash
   git revert <phase1_commit_hash>
   git push origin main
   ```

3. **No data loss** — `runs` table unaffected

### Phase 2 Rollback (Canvas Quotas)

1. **Disable quota enforcer**:
   ```go
   // go/cmd/them/main.go
   // lifecycle.WithQuotaEnforcer(quotaEnforcer)  // comment out
   ```

2. **Redeploy** without quota checks

3. **Redis cleanup** (optional):
   ```bash
   docker exec them-redis redis-cli KEYS "rl:user:*" | xargs docker exec them-redis redis-cli DEL
   ```

### Phase 3/4 Rollback (Gateway)

1. **Switch EPs back to `token` mode**:
   ```sql
   UPDATE them.entry_points 
   SET access_policy = '{"mode":"token"}' 
   WHERE access_policy->>'mode' = 'user_jwt';
   ```

2. **Revert code**, redeploy

3. **`user_id` column stays** (nullable, no harm if NULL)

---

## Performance Considerations

### Redis Load

**Rate limit checks**: 1 Redis INCR per request
- **Load**: ~1000 rpm × 100 users = 100k ops/min = **1667 ops/sec**
- **Redis capacity**: 100k ops/sec (single instance)
- **Verdict**: ✅ No problem

### PostgreSQL Load

**Role config lookup**: 1 query per user per 5 minutes (cached)
- **Load**: 1000 users / 300 sec = **3.3 queries/sec**
- **Verdict**: ✅ Negligible

**Daily spend query**: 1 query per request (cached 1 min)
- **Load**: 1000 rpm / 60 sec = **17 queries/sec**
- **Optimized**: Add index on `(user_id, created_at)` ✅
- **Verdict**: ✅ Fine

**Indexes required**:
```sql
CREATE INDEX idx_run_usage_user_created ON them.run_usage(user_id, created_at);
CREATE INDEX idx_gateway_requests_user_created ON them.gateway_requests(user_id, created_at);
```

---

## Known Issues & Workarounds

### Issue 1: JWT Expiry (1 hour)

**Problem**: User's JWT expires mid-session → 401

**Workaround**: Frontend auto-refreshes JWT every 50 minutes

**Long-term fix**: Implement refresh token flow (Phase 6, future)

### Issue 2: Role Change Lag (up to 5 minutes)

**Problem**: Admin changes user's role → cached role config still wrong

**Workaround**: Pub/sub invalidation on role change (emit `them:role:changed:{user_id}`)

**Current**: Not implemented (acceptable lag for MVP)

### Issue 3: Cost Tracking Delay

**Problem**: `run_usage` inserted AFTER run completes → spend query misses in-flight runs

**Workaround**: Add 10% buffer to cost limit check

**Long-term fix**: Pre-charge estimated cost, refund difference on completion

---

## Future Enhancements (Post-MVP)

1. **Role hierarchy** (super_admin > developer > analyst)
2. **Custom quotas per user** (`user_overrides` table)
3. **Quota alerts** (email when 80% of limit reached)
4. **Quota top-up** (self-service limit increase)
5. **Per-app quotas** (not just per-user)
6. **Audit dashboard** (who hit limits, when, why)

---

**Created**: 2026-10-22  
**Last Updated**: 2026-10-22  
**Status**: Phase 1 ready to start  
**Owner**: Implementation team  
**Estimated Total Time**: 11 days (Phases 1-4)
