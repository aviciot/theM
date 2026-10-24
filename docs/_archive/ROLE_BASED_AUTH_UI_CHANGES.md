# Role-Based Authorization — UI Changes
# Date: 2026-10-22
# Status: Planning

---

## Overview

**Goal**: Add UI support for role-based authorization features.

**Current UI State**:
- ✅ User login shows role in `/me` response
- ✅ Dashboard shows user info (username, email)
- ❌ No role display in UI
- ❌ No quota usage display
- ❌ No "Required Role" field in Entry Point editor
- ❌ No quota enforcement errors shown to user

**Required UI Changes**:
1. Show user's role and quota limits in Dashboard header
2. Add "Required Role" field to Entry Point editor
3. Show quota usage (rate limit + cost limit) in user profile
4. Handle quota exceeded errors (429/402) with clear messages

---

## Phase 1: Entry Point Editor — Required Role Field

**Location**: `frontend/src/components/EntryPoints/EntryPointForm.tsx` (or similar)

**Current Entry Point form fields**:
- Slug
- Type (websocket/sse/gateway)
- Access Policy (public/token/user_jwt/external_jwt)
- Max Concurrent Sessions
- Queue Timeout
- Enabled

**Add field**:
```tsx
// Required Role dropdown (only visible when access_policy.mode = "user_jwt")

{accessMode === 'user_jwt' && (
  <FormControl fullWidth margin="normal">
    <InputLabel>Required Role</InputLabel>
    <Select
      name="required_role"
      value={formData.required_role || ''}
      onChange={handleChange}
      label="Required Role"
    >
      <MenuItem value="">None (any authenticated user)</MenuItem>
      <MenuItem value="super_admin">Super Admin</MenuItem>
      <MenuItem value="developer">Developer</MenuItem>
      <MenuItem value="analyst">Analyst</MenuItem>
      <MenuItem value="viewer">Viewer</MenuItem>
      <MenuItem value="end_user">End User</MenuItem>
    </Select>
    <FormHelperText>
      Only users with this role (or higher) can access this entry point
    </FormHelperText>
  </FormControl>
)}
```

**API changes needed**:
```typescript
// POST /admin/entry-points
// PUT /admin/entry-points/:id

interface EntryPointCreateRequest {
  slug: string;
  entry_point_type: string;
  access_policy: {
    mode: 'public' | 'token' | 'user_jwt' | 'external_jwt';
  };
  required_role?: string; // NEW FIELD
  max_concurrent_sessions?: number;
  queue_timeout_seconds?: number;
  enabled: boolean;
}
```

**Backend changes**:
```go
// go/internal/admin/entry_points.go

type CreateEntryPointRequest struct {
    Slug                  string      `json:"slug"`
    EntryPointType        string      `json:"entry_point_type"`
    AccessPolicy          interface{} `json:"access_policy"`
    RequiredRole          *string     `json:"required_role"` // NEW
    MaxConcurrentSessions *int        `json:"max_concurrent_sessions"`
    QueueTimeoutSeconds   *int        `json:"queue_timeout_seconds"`
    Enabled               bool        `json:"enabled"`
}

// SQL INSERT
const insertEPSQL = `
    INSERT INTO them.entry_points (
        application_id, slug, entry_point_type, access_policy, 
        required_role,  -- NEW
        max_concurrent_sessions, queue_timeout_seconds, enabled
    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    RETURNING id
`
```

**Files**:
- `frontend/src/components/EntryPoints/EntryPointForm.tsx` (UI)
- `go/internal/admin/entry_points.go` (API handler)
- `go/internal/admin/dal/entry_points.go` (SQL)

---

## Phase 2: User Profile — Show Role & Quota Limits

**Location**: `frontend/src/components/UserProfile.tsx` (or Dashboard header)

**Current `/me` response**:
```json
{
  "user_id": 42,
  "username": "alice",
  "email": "alice@example.com",
  "tenant_id": "uuid-tenant"
}
```

**Enhanced `/me` response** (backend change needed):
```json
{
  "user_id": 42,
  "username": "alice",
  "email": "alice@example.com",
  "tenant_id": "uuid-tenant",
  "role": {
    "name": "developer",
    "rate_limit": 5000,
    "cost_limit_daily": 100.00,
    "dashboard_access": "view"
  }
}
```

**UI Display**:
```tsx
// Dashboard header (top-right corner)

<Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
  <Chip 
    label={user.role.name.toUpperCase()} 
    color="primary" 
    size="small" 
  />
  <Typography variant="body2" color="text.secondary">
    {user.username}
  </Typography>
  <IconButton onClick={handleProfileClick}>
    <AccountCircleIcon />
  </IconButton>
</Box>

// User Profile Dialog
<Dialog open={profileOpen} onClose={handleClose}>
  <DialogTitle>User Profile</DialogTitle>
  <DialogContent>
    <Stack spacing={2}>
      <TextField label="Username" value={user.username} disabled />
      <TextField label="Email" value={user.email} disabled />
      <TextField label="Role" value={user.role.name} disabled />
      <Divider />
      <Typography variant="subtitle2">Quota Limits</Typography>
      <TextField 
        label="Rate Limit" 
        value={`${user.role.rate_limit} requests/minute`} 
        disabled 
      />
      <TextField 
        label="Daily Cost Limit" 
        value={`$${user.role.cost_limit_daily}/day`} 
        disabled 
      />
    </Stack>
  </DialogContent>
</Dialog>
```

**Backend changes**:
```go
// go/internal/authserver/handlers.go (GET /auth/me)

type MeResponse struct {
    UserID   int64       `json:"user_id"`
    Username string      `json:"username"`
    Email    string      `json:"email"`
    TenantID string      `json:"tenant_id"`
    Role     *RoleInfo   `json:"role,omitempty"` // NEW
}

type RoleInfo struct {
    Name            string  `json:"name"`
    RateLimit       int     `json:"rate_limit"`
    CostLimitDaily  float64 `json:"cost_limit_daily"`
    DashboardAccess string  `json:"dashboard_access"`
}

// SQL query (join users → roles)
const meQuerySQL = `
    SELECT u.id, u.username, u.email, u.tenant_id,
           r.name, r.rate_limit, r.cost_limit_daily, r.dashboard_access
    FROM auth_service.users u
    LEFT JOIN auth_service.roles r ON u.role_id = r.id
    WHERE u.id = $1
`
```

**Files**:
- `frontend/src/components/UserProfile.tsx` (new or existing)
- `frontend/src/components/DashboardHeader.tsx` (add role chip)
- `go/internal/authserver/handlers.go` (enhance /me endpoint)

---

## Phase 3: Quota Usage Display (Real-Time)

**Location**: Dashboard header or User Profile dialog

**Goal**: Show "You've used X of Y rpm" and "You've spent $X of $Y today"

**New API endpoint**: `GET /auth/quota-usage`

**Response**:
```json
{
  "user_id": 42,
  "rate_limit": {
    "limit": 5000,
    "current": 230,
    "window": "2026-10-22T14:00:00Z"
  },
  "cost_limit": {
    "limit": 100.00,
    "current": 45.32,
    "date": "2026-10-22"
  }
}
```

**UI Display**:
```tsx
// User Profile Dialog (add to Phase 2 dialog)

<Stack spacing={2}>
  <Typography variant="subtitle2">Current Usage</Typography>
  
  {/* Rate Limit Progress */}
  <Box>
    <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: 1 }}>
      <Typography variant="body2">Rate Limit</Typography>
      <Typography variant="body2" color="text.secondary">
        {quotaUsage.rate_limit.current} / {quotaUsage.rate_limit.limit} rpm
      </Typography>
    </Box>
    <LinearProgress 
      variant="determinate" 
      value={(quotaUsage.rate_limit.current / quotaUsage.rate_limit.limit) * 100}
      color={quotaUsage.rate_limit.current > quotaUsage.rate_limit.limit * 0.9 ? 'warning' : 'primary'}
    />
  </Box>

  {/* Cost Limit Progress */}
  <Box>
    <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: 1 }}>
      <Typography variant="body2">Daily Spend</Typography>
      <Typography variant="body2" color="text.secondary">
        ${quotaUsage.cost_limit.current.toFixed(2)} / ${quotaUsage.cost_limit.limit.toFixed(2)}
      </Typography>
    </Box>
    <LinearProgress 
      variant="determinate" 
      value={(quotaUsage.cost_limit.current / quotaUsage.cost_limit.limit) * 100}
      color={quotaUsage.cost_limit.current > quotaUsage.cost_limit.limit * 0.9 ? 'error' : 'success'}
    />
  </Box>
</Stack>
```

**Backend changes**:
```go
// go/internal/authserver/quota.go (NEW FILE)

type QuotaUsageResponse struct {
    UserID    int64           `json:"user_id"`
    RateLimit RateLimitUsage  `json:"rate_limit"`
    CostLimit CostLimitUsage  `json:"cost_limit"`
}

type RateLimitUsage struct {
    Limit   int       `json:"limit"`
    Current int       `json:"current"`
    Window  time.Time `json:"window"`
}

type CostLimitUsage struct {
    Limit   float64 `json:"limit"`
    Current float64 `json:"current"`
    Date    string  `json:"date"`
}

func (h *Handlers) GetQuotaUsage(w http.ResponseWriter, r *http.Request) {
    claims := auth.ClaimsFromContext(r.Context())
    
    // Get role limits
    role := h.store.GetUserRole(r.Context(), claims.UserID)
    
    // Get current rate limit usage (from Redis)
    minute := time.Now().Truncate(time.Minute)
    rateKey := fmt.Sprintf("rl:user:%d:minute:%d", claims.UserID, minute.Unix())
    current := h.redis.Get(r.Context(), rateKey) // INCR count
    
    // Get current daily spend (from PostgreSQL)
    today := time.Now().UTC().Truncate(24 * time.Hour)
    dailySpend := h.quotaChecker.GetUserDailySpend(r.Context(), claims.UserID)
    
    resp := QuotaUsageResponse{
        UserID: claims.UserID,
        RateLimit: RateLimitUsage{
            Limit:   role.RateLimit,
            Current: current,
            Window:  minute,
        },
        CostLimit: CostLimitUsage{
            Limit:   role.CostLimitDaily,
            Current: dailySpend,
            Date:    today.Format("2006-01-02"),
        },
    }
    
    json.NewEncoder(w).Encode(resp)
}
```

**Files**:
- `frontend/src/components/UserProfile.tsx` (add quota usage)
- `go/internal/authserver/quota.go` (new endpoint)
- `go/cmd/auth-server/main.go` (register route)

---

## Phase 4: Error Handling — Quota Exceeded UI

**Goal**: Show user-friendly error when 429/402 occurs

**Current error handling** (generic):
```tsx
// When API call fails:
catch (error) {
  showNotification('Request failed', 'error');
}
```

**Enhanced error handling**:
```tsx
// Intercept 429 (Rate Limited)
if (error.response?.status === 429) {
  const data = error.response.data;
  showDialog({
    title: 'Rate Limit Exceeded',
    message: `You've exceeded your ${data.limit} requests/minute limit. Please wait a moment and try again.`,
    severity: 'warning',
    actions: [
      { label: 'Upgrade Plan', onClick: () => navigate('/billing') },
      { label: 'OK', onClick: closeDialog }
    ]
  });
  return;
}

// Intercept 402 (Payment Required / Quota Exceeded)
if (error.response?.status === 402) {
  const data = error.response.data;
  showDialog({
    title: 'Daily Quota Exceeded',
    message: `You've reached your $${data.limit}/day spending limit. Your quota will reset tomorrow at midnight UTC.`,
    severity: 'error',
    actions: [
      { label: 'Request Increase', onClick: () => navigate('/quota-request') },
      { label: 'OK', onClick: closeDialog }
    ]
  });
  return;
}
```

**Gateway error response format** (standardize):
```json
// 429 Too Many Requests
{
  "error": {
    "type": "rate_limit_exceeded",
    "message": "User rate limit: 5000 rpm",
    "code": "rate_limit_exceeded",
    "limit": 5000,
    "current": 5001,
    "retry_after": 60
  }
}

// 402 Payment Required
{
  "error": {
    "type": "quota_exceeded",
    "message": "Daily cost limit: $100.00",
    "code": "quota_exceeded",
    "limit": 100.00,
    "current": 100.15,
    "reset_at": "2026-10-23T00:00:00Z"
  }
}
```

**Files**:
- `frontend/src/utils/errorHandler.ts` (centralized error handling)
- `frontend/src/components/ErrorDialog.tsx` (quota error dialog)
- `go/internal/llmgateway/handler.go` (standardize error response)

---

## Phase 5: Admin UI — Role Management

**Goal**: Allow admins to view/edit role limits

**New page**: `/admin/roles`

**Features**:
- List all roles (table view)
- Edit role limits (rate_limit, cost_limit_daily)
- Assign users to roles

**Table view**:
```tsx
<TableContainer>
  <Table>
    <TableHead>
      <TableRow>
        <TableCell>Role Name</TableCell>
        <TableCell>Rate Limit (rpm)</TableCell>
        <TableCell>Daily Cost Limit</TableCell>
        <TableCell>Dashboard Access</TableCell>
        <TableCell>Users</TableCell>
        <TableCell>Actions</TableCell>
      </TableRow>
    </TableHead>
    <TableBody>
      {roles.map(role => (
        <TableRow key={role.id}>
          <TableCell>{role.name}</TableCell>
          <TableCell>{role.rate_limit}</TableCell>
          <TableCell>${role.cost_limit_daily}</TableCell>
          <TableCell>{role.dashboard_access}</TableCell>
          <TableCell>{role.user_count}</TableCell>
          <TableCell>
            <IconButton onClick={() => handleEdit(role)}>
              <EditIcon />
            </IconButton>
          </TableCell>
        </TableRow>
      ))}
    </TableBody>
  </Table>
</TableContainer>
```

**Edit dialog**:
```tsx
<Dialog open={editOpen} onClose={handleClose}>
  <DialogTitle>Edit Role: {selectedRole.name}</DialogTitle>
  <DialogContent>
    <Stack spacing={2}>
      <TextField 
        label="Rate Limit (requests/minute)" 
        type="number"
        value={formData.rate_limit}
        onChange={(e) => setFormData({...formData, rate_limit: e.target.value})}
      />
      <TextField 
        label="Daily Cost Limit (USD)" 
        type="number"
        step="0.01"
        value={formData.cost_limit_daily}
        onChange={(e) => setFormData({...formData, cost_limit_daily: e.target.value})}
      />
      <FormControl>
        <InputLabel>Dashboard Access</InputLabel>
        <Select
          value={formData.dashboard_access}
          onChange={(e) => setFormData({...formData, dashboard_access: e.target.value})}
        >
          <MenuItem value="none">None</MenuItem>
          <MenuItem value="view">View Only</MenuItem>
          <MenuItem value="admin">Admin</MenuItem>
        </Select>
      </FormControl>
    </Stack>
  </DialogContent>
  <DialogActions>
    <Button onClick={handleClose}>Cancel</Button>
    <Button onClick={handleSave} variant="contained">Save</Button>
  </DialogActions>
</Dialog>
```

**Backend API**:
```go
// GET /admin/roles
type ListRolesResponse struct {
    Roles []RoleWithCount `json:"roles"`
}

type RoleWithCount struct {
    ID              int     `json:"id"`
    Name            string  `json:"name"`
    RateLimit       int     `json:"rate_limit"`
    CostLimitDaily  float64 `json:"cost_limit_daily"`
    DashboardAccess string  `json:"dashboard_access"`
    UserCount       int     `json:"user_count"` // COUNT(*) from users
}

// PUT /admin/roles/:id
type UpdateRoleRequest struct {
    RateLimit       int     `json:"rate_limit"`
    CostLimitDaily  float64 `json:"cost_limit_daily"`
    DashboardAccess string  `json:"dashboard_access"`
}
```

**Files**:
- `frontend/src/pages/Admin/Roles.tsx` (new page)
- `go/internal/admin/roles.go` (new handler)
- `go/internal/admin/dal/roles.go` (SQL)

---

## Phase 6: User Assignment — Change User Role

**Location**: `/admin/users` (existing page, enhance)

**Add column**: "Role" (dropdown to change user's role)

**Current users table**:
```tsx
<TableRow>
  <TableCell>{user.username}</TableCell>
  <TableCell>{user.email}</TableCell>
  <TableCell>{user.active ? 'Active' : 'Inactive'}</TableCell>
  <TableCell>
    <IconButton onClick={() => handleEdit(user)}>
      <EditIcon />
    </IconButton>
  </TableCell>
</TableRow>
```

**Enhanced table** (add Role column):
```tsx
<TableRow>
  <TableCell>{user.username}</TableCell>
  <TableCell>{user.email}</TableCell>
  <TableCell>
    <Select
      value={user.role_id}
      onChange={(e) => handleRoleChange(user.id, e.target.value)}
      size="small"
    >
      {roles.map(role => (
        <MenuItem key={role.id} value={role.id}>
          {role.name}
        </MenuItem>
      ))}
    </Select>
  </TableCell>
  <TableCell>{user.active ? 'Active' : 'Inactive'}</TableCell>
  <TableCell>
    <IconButton onClick={() => handleEdit(user)}>
      <EditIcon />
    </IconButton>
  </TableCell>
</TableRow>
```

**Backend API**:
```go
// PUT /admin/users/:id/role
type UpdateUserRoleRequest struct {
    RoleID int `json:"role_id"`
}

func (h *AdminHandlers) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
    userID := chi.URLParam(r, "id")
    var req UpdateUserRoleRequest
    json.NewDecoder(r.Body).Decode(&req)
    
    // Update DB
    const q = `UPDATE auth_service.users SET role_id = $1 WHERE id = $2`
    h.pool.Exec(r.Context(), q, req.RoleID, userID)
    
    // Invalidate role cache (pub/sub)
    h.redis.Publish(r.Context(), fmt.Sprintf("them:role:changed:%s", userID), "")
    
    w.WriteHeader(http.StatusOK)
}
```

**Files**:
- `frontend/src/pages/Admin/Users.tsx` (add role dropdown)
- `go/internal/admin/users.go` (add UpdateUserRole endpoint)

---

## Summary of UI Changes

| Phase | Component | Change | API Needed | Files |
|---|---|---|---|---|
| **1** | Entry Point Form | Add "Required Role" dropdown | PUT /admin/entry-points | EntryPointForm.tsx |
| **2** | User Profile | Show role + quota limits | GET /auth/me (enhanced) | UserProfile.tsx, DashboardHeader.tsx |
| **3** | User Profile | Show quota usage (progress bars) | GET /auth/quota-usage (NEW) | UserProfile.tsx, quota.go |
| **4** | Error Dialog | Handle 429/402 with clear messages | Standardize error format | ErrorDialog.tsx, errorHandler.ts |
| **5** | Admin Roles | View/edit role limits | GET /admin/roles, PUT /admin/roles/:id | Admin/Roles.tsx, admin/roles.go |
| **6** | Admin Users | Change user role (dropdown) | PUT /admin/users/:id/role | Admin/Users.tsx, admin/users.go |

---

## Implementation Order (Tied to Backend Phases)

### **After Backend Phase 1** (Canvas Role Enforcement)
✅ **UI Phase 1**: Add "Required Role" field to Entry Point editor

### **After Backend Phase 2** (Canvas Quotas)
✅ **UI Phase 2**: Show role info in user profile  
✅ **UI Phase 3**: Show quota usage (real-time)  
✅ **UI Phase 4**: Handle 429/402 errors

### **After Backend Phase 4** (Gateway Quotas)
✅ **UI Phase 4**: Quota errors work for Gateway too (no additional UI changes)

### **Optional (Admin Features)**
🔜 **UI Phase 5**: Admin role management page  
🔜 **UI Phase 6**: User role assignment

---

## Wireframes (ASCII Mockups)

### User Profile Dialog (Phase 2 + 3)

```
┌─────────────────────────────────────────────┐
│ User Profile                            [X] │
├─────────────────────────────────────────────┤
│                                             │
│ Username:  alice                            │
│ Email:     alice@example.com                │
│ Role:      Developer                        │
│                                             │
│ ───────────────────────────────────────     │
│                                             │
│ Quota Limits                                │
│                                             │
│ Rate Limit:    5000 requests/minute         │
│ Cost Limit:    $100.00/day                  │
│                                             │
│ ───────────────────────────────────────     │
│                                             │
│ Current Usage                               │
│                                             │
│ Rate Limit        230 / 5000 rpm            │
│ [████░░░░░░░░░░░░░░░░░░░░░░] 4.6%          │
│                                             │
│ Daily Spend      $45.32 / $100.00           │
│ [████████████░░░░░░░░░░░░░] 45.3%          │
│                                             │
│                                             │
│                          [Close]            │
└─────────────────────────────────────────────┘
```

### Entry Point Form (Phase 1)

```
┌─────────────────────────────────────────────┐
│ Create Entry Point                          │
├─────────────────────────────────────────────┤
│                                             │
│ Slug:  [my-endpoint_________]               │
│                                             │
│ Type:  [WebSocket ▼]                        │
│                                             │
│ Access Policy:  [User JWT ▼]                │
│                                             │
│ ┌─────────────────────────────────────┐     │
│ │ Required Role:  [Developer ▼]       │     │
│ │                                     │     │
│ │ Only users with this role (or       │     │
│ │ higher) can access this EP          │     │
│ └─────────────────────────────────────┘     │
│                                             │
│ Max Concurrent:  [100________]              │
│                                             │
│ Queue Timeout:   [30_________] seconds      │
│                                             │
│ [✓] Enabled                                 │
│                                             │
│                    [Cancel]  [Create]       │
└─────────────────────────────────────────────┘
```

### Quota Exceeded Error (Phase 4)

```
┌─────────────────────────────────────────────┐
│ ⚠️  Rate Limit Exceeded                      │
├─────────────────────────────────────────────┤
│                                             │
│ You've exceeded your 5000 requests/minute   │
│ limit. Please wait a moment and try again.  │
│                                             │
│ Retry after: 42 seconds                     │
│                                             │
│                                             │
│              [Upgrade Plan]  [OK]           │
└─────────────────────────────────────────────┘
```

---

## Testing UI Changes

### Manual Testing Checklist

**Phase 1** (Required Role field):
- [ ] Create EP with `user_jwt` mode → "Required Role" dropdown appears
- [ ] Create EP with `token` mode → "Required Role" field hidden
- [ ] Set required_role to "developer" → save → reload → value persists
- [ ] Set required_role to "None" → save → DB stores NULL

**Phase 2** (User profile):
- [ ] Login as Alice (developer) → profile shows "Developer" role
- [ ] Profile shows "Rate Limit: 5000 rpm"
- [ ] Profile shows "Cost Limit: $100.00/day"

**Phase 3** (Quota usage):
- [ ] Make 10 requests → quota usage updates
- [ ] Progress bar shows correct percentage
- [ ] Progress bar turns orange at 90% threshold
- [ ] Progress bar turns red at cost limit threshold

**Phase 4** (Error handling):
- [ ] Hit rate limit → see "Rate Limit Exceeded" dialog
- [ ] Hit cost limit → see "Daily Quota Exceeded" dialog
- [ ] Dialog shows retry_after time
- [ ] "Upgrade Plan" button works (navigates to billing)

---

## Files Summary

**New Files** (6):
- `frontend/src/components/UserProfile.tsx` (user profile dialog)
- `frontend/src/components/ErrorDialog.tsx` (quota error dialog)
- `frontend/src/pages/Admin/Roles.tsx` (admin role management)
- `go/internal/authserver/quota.go` (quota usage endpoint)
- `go/internal/admin/roles.go` (role CRUD API)
- `go/internal/admin/dal/roles.go` (role SQL queries)

**Modified Files** (5):
- `frontend/src/components/EntryPoints/EntryPointForm.tsx` (add required_role)
- `frontend/src/components/DashboardHeader.tsx` (show role chip)
- `frontend/src/pages/Admin/Users.tsx` (add role dropdown)
- `frontend/src/utils/errorHandler.ts` (handle 429/402)
- `go/internal/authserver/handlers.go` (enhance /me response)

---

**Created**: 2026-10-22  
**Status**: Planning  
**Depends On**: Backend phases (see ROLE_BASED_AUTH_IMPLEMENTATION_PLAN.md)  
**Total Estimate**: 4-5 days UI work (can overlap with backend)
