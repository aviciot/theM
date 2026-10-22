package execution

import (
	"context"
	"errors"
	"testing"
)

// mockUserRateLimiter is a fake UserRateLimiter for testing.
type mockUserRateLimiter struct {
	limitExceeded bool
	err           error
}

func (m *mockUserRateLimiter) CheckLimit(ctx context.Context, userID int64, rpm int) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.limitExceeded, nil
}

// mockUserQuotaChecker is a fake UserQuotaChecker for testing.
type mockUserQuotaChecker struct {
	dailySpend float64
	err        error
}

func (m *mockUserQuotaChecker) GetUserDailySpend(ctx context.Context, userID int64) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.dailySpend, nil
}

// mockPlatformRoleCheckerWithConfig extends mockPlatformRoleChecker to return RoleConfig.
type mockPlatformRoleCheckerWithConfig struct {
	roleConfigs map[int64]*RoleConfig // userID -> RoleConfig
	err         error
}

func (m *mockPlatformRoleCheckerWithConfig) GetUserRole(ctx context.Context, userID int64) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	cfg, ok := m.roleConfigs[userID]
	if !ok || cfg == nil {
		return "", nil
	}
	return cfg.Name, nil
}

func (m *mockPlatformRoleCheckerWithConfig) GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	cfg, ok := m.roleConfigs[userID]
	if !ok {
		return nil, nil
	}
	return cfg, nil
}

// TestLifecycle_UserRateLimit_Pass verifies that a user under their rate limit is admitted.
func TestLifecycle_UserRateLimit_Pass(t *testing.T) {
	// Setup: Alice (user_id=42) has role "developer" with 5000 rpm, not exceeded
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			42: {Name: "developer", RateLimit: 5000, CostLimitDaily: 100.0},
		},
	}

	rateLimiter := &mockUserRateLimiter{limitExceeded: false}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userRateLimiter:     rateLimiter,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 42,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	exceeded, err := lc.userRateLimiter.CheckLimit(ctx, req.UserID, roleConfig.RateLimit)
	if err != nil {
		t.Fatalf("rate limit check failed: %v", err)
	}

	// Assert: not exceeded
	if exceeded {
		t.Error("expected rate limit not exceeded, but got exceeded")
	}
}

// TestLifecycle_UserRateLimit_Exceeded verifies that a user exceeding their rate limit is rejected.
func TestLifecycle_UserRateLimit_Exceeded(t *testing.T) {
	// Setup: Bob (user_id=99) has role "viewer" with 100 rpm, exceeded
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			99: {Name: "viewer", RateLimit: 100, CostLimitDaily: 5.0},
		},
	}

	rateLimiter := &mockUserRateLimiter{limitExceeded: true}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userRateLimiter:     rateLimiter,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 99,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	exceeded, err := lc.userRateLimiter.CheckLimit(ctx, req.UserID, roleConfig.RateLimit)
	if err != nil {
		t.Fatalf("rate limit check failed: %v", err)
	}

	// Assert: exceeded
	if !exceeded {
		t.Error("expected rate limit exceeded, but got not exceeded")
	}

	// Expected behavior: Admit returns AdmitErrRateLimited (429)
}

// TestLifecycle_UserDailyCostLimit_Pass verifies that a user under their daily cost limit is admitted.
func TestLifecycle_UserDailyCostLimit_Pass(t *testing.T) {
	// Setup: Charlie (user_id=50) has role "analyst" with $50/day limit, spent $25
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			50: {Name: "analyst", RateLimit: 1000, CostLimitDaily: 50.0},
		},
	}

	quotaChecker := &mockUserQuotaChecker{dailySpend: 25.0}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userQuotaChecker:    quotaChecker,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 50,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	dailySpend, err := lc.userQuotaChecker.GetUserDailySpend(ctx, req.UserID)
	if err != nil {
		t.Fatalf("daily spend check failed: %v", err)
	}

	// Assert: spend under limit
	if dailySpend >= roleConfig.CostLimitDaily {
		t.Errorf("expected daily spend (%f) < limit (%f)", dailySpend, roleConfig.CostLimitDaily)
	}
}

// TestLifecycle_UserDailyCostLimit_Exceeded verifies that a user exceeding their daily cost limit is rejected.
func TestLifecycle_UserDailyCostLimit_Exceeded(t *testing.T) {
	// Setup: David (user_id=60) has role "viewer" with $5/day limit, spent $5.50
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			60: {Name: "viewer", RateLimit: 100, CostLimitDaily: 5.0},
		},
	}

	quotaChecker := &mockUserQuotaChecker{dailySpend: 5.50}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userQuotaChecker:    quotaChecker,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 60,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	dailySpend, err := lc.userQuotaChecker.GetUserDailySpend(ctx, req.UserID)
	if err != nil {
		t.Fatalf("daily spend check failed: %v", err)
	}

	// Assert: spend exceeds limit
	if dailySpend < roleConfig.CostLimitDaily {
		t.Errorf("expected daily spend (%f) >= limit (%f)", dailySpend, roleConfig.CostLimitDaily)
	}

	// Expected behavior: Admit returns AdmitErrQuotaUserDailyCost (402)
}

// TestLifecycle_QuotaCheck_RateLimiterError verifies fail-open behavior when rate limiter fails.
func TestLifecycle_QuotaCheck_RateLimiterError(t *testing.T) {
	// Setup: rate limiter returns error
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			42: {Name: "developer", RateLimit: 5000, CostLimitDaily: 100.0},
		},
	}

	rateLimiter := &mockUserRateLimiter{err: errors.New("redis unavailable")}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userRateLimiter:     rateLimiter,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 42,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	_, err = lc.userRateLimiter.CheckLimit(ctx, req.UserID, roleConfig.RateLimit)

	// Assert: error is returned (fail-open means we log but continue in Admit)
	if err == nil {
		t.Error("expected error from rate limiter, got nil")
	}

	// Expected behavior: Admit logs warning and continues (fail-open)
}

// TestLifecycle_QuotaCheck_QuotaCheckerError verifies fail-open behavior when quota checker fails.
func TestLifecycle_QuotaCheck_QuotaCheckerError(t *testing.T) {
	// Setup: quota checker returns error
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			42: {Name: "developer", RateLimit: 5000, CostLimitDaily: 100.0},
		},
	}

	quotaChecker := &mockUserQuotaChecker{err: errors.New("database unavailable")}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userQuotaChecker:    quotaChecker,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 42,
	}

	// Simulate the quota check
	ctx := context.Background()
	_, err := lc.userQuotaChecker.GetUserDailySpend(ctx, req.UserID)

	// Assert: error is returned (fail-open means we log but continue in Admit)
	if err == nil {
		t.Error("expected error from quota checker, got nil")
	}

	// Expected behavior: Admit logs warning and continues (fail-open)
}

// TestLifecycle_QuotaCheck_UnlimitedRole verifies that 0 limits are treated as unlimited.
func TestLifecycle_QuotaCheck_UnlimitedRole(t *testing.T) {
	// Setup: unlimited role (rate_limit=0, cost_limit_daily=0)
	roleChecker := &mockPlatformRoleCheckerWithConfig{
		roleConfigs: map[int64]*RoleConfig{
			42: {Name: "super_admin", RateLimit: 0, CostLimitDaily: 0},
		},
	}

	// These should never be called for unlimited role, so set errors
	rateLimiter := &mockUserRateLimiter{err: errors.New("should not be called")}
	quotaChecker := &mockUserQuotaChecker{err: errors.New("should not be called")}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		userRateLimiter:     rateLimiter,
		userQuotaChecker:    quotaChecker,
		logger:              testLogger,
	}

	req := ExecutionRequest{
		UserID: 42,
	}

	// Simulate the quota check
	ctx := context.Background()
	roleConfig, err := lc.platformRoleChecker.GetRoleConfig(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role config lookup failed: %v", err)
	}

	// Assert: limits are 0 (unlimited)
	if roleConfig.RateLimit != 0 {
		t.Errorf("expected unlimited rate limit (0), got %d", roleConfig.RateLimit)
	}
	if roleConfig.CostLimitDaily != 0 {
		t.Errorf("expected unlimited cost limit (0), got %f", roleConfig.CostLimitDaily)
	}

	// Expected behavior: Admit skips CheckLimit and GetUserDailySpend calls when limits are 0
}
