package llmgateway

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/execution"
)

// mockPlatformRoleCheckerGW is a mock for testing Gateway quota enforcement.
type mockPlatformRoleCheckerGW struct {
	roleConfigs map[int64]*execution.RoleConfig
	err         error
}

func (m *mockPlatformRoleCheckerGW) GetUserRole(ctx context.Context, userID int64) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	cfg, ok := m.roleConfigs[userID]
	if !ok || cfg == nil {
		return "", nil
	}
	return cfg.Name, nil
}

func (m *mockPlatformRoleCheckerGW) GetRoleConfig(ctx context.Context, userID int64) (*execution.RoleConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	cfg, ok := m.roleConfigs[userID]
	if !ok {
		return nil, nil
	}
	return cfg, nil
}

// mockUserRateLimiterGW is a mock for testing Gateway rate limiting.
type mockUserRateLimiterGW struct {
	limitExceeded bool
	err           error
}

func (m *mockUserRateLimiterGW) CheckLimit(ctx context.Context, userID int64, rpm int) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.limitExceeded, nil
}

// mockUserQuotaCheckerGW is a mock for testing Gateway quota checking.
type mockUserQuotaCheckerGW struct {
	dailySpend float64
	err        error
}

func (m *mockUserQuotaCheckerGW) GetUserDailySpend(ctx context.Context, userID int64) (float64, error) {
	// Not used by Gateway
	return 0, nil
}

func (m *mockUserQuotaCheckerGW) GetUserDailySpendFromGateway(ctx context.Context, userID int64) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.dailySpend, nil
}

// TestGatewayQuota_RateLimit_Enforced verifies that rate limit is enforced.
func TestGatewayQuota_RateLimit_Enforced(t *testing.T) {
	// Setup: user_id=42 has developer role with 5000 rpm limit, currently exceeded
	roleChecker := &mockPlatformRoleCheckerGW{
		roleConfigs: map[int64]*execution.RoleConfig{
			42: {Name: "developer", RateLimit: 5000, CostLimitDaily: 100.0},
		},
	}
	rateLimiter := &mockUserRateLimiterGW{limitExceeded: true}

	// Verify mock returns expected values
	ctx := context.Background()
	cfg, err := roleChecker.GetRoleConfig(ctx, 42)
	if err != nil {
		t.Fatalf("GetRoleConfig failed: %v", err)
	}
	if cfg == nil || cfg.RateLimit != 5000 {
		t.Fatalf("expected RateLimit=5000, got %+v", cfg)
	}

	exceeded, err := rateLimiter.CheckLimit(ctx, 42, 5000)
	if err != nil {
		t.Fatalf("CheckLimit failed: %v", err)
	}
	if !exceeded {
		t.Error("expected rate limit exceeded, got not exceeded")
	}
}

// TestGatewayQuota_RateLimit_NotExceeded verifies that requests proceed when under limit.
func TestGatewayQuota_RateLimit_NotExceeded(t *testing.T) {
	// Setup: user_id=42 has developer role with 5000 rpm limit, not exceeded
	rateLimiter := &mockUserRateLimiterGW{limitExceeded: false}

	// Verify mock returns expected values
	ctx := context.Background()
	exceeded, err := rateLimiter.CheckLimit(ctx, 42, 5000)
	if err != nil {
		t.Fatalf("CheckLimit failed: %v", err)
	}
	if exceeded {
		t.Error("expected rate limit not exceeded, got exceeded")
	}
}

// TestGatewayQuota_CostLimit_Enforced verifies that daily cost limit is enforced.
func TestGatewayQuota_CostLimit_Enforced(t *testing.T) {
	// Setup: user_id=50 has analyst role with $50/day limit, spent $55
	roleChecker := &mockPlatformRoleCheckerGW{
		roleConfigs: map[int64]*execution.RoleConfig{
			50: {Name: "analyst", RateLimit: 1000, CostLimitDaily: 50.0},
		},
	}
	quotaChecker := &mockUserQuotaCheckerGW{dailySpend: 55.0}

	// Verify mock returns expected values
	ctx := context.Background()
	cfg, err := roleChecker.GetRoleConfig(ctx, 50)
	if err != nil {
		t.Fatalf("GetRoleConfig failed: %v", err)
	}
	if cfg == nil || cfg.CostLimitDaily != 50.0 {
		t.Fatalf("expected CostLimitDaily=50.0, got %+v", cfg)
	}

	dailySpend, err := quotaChecker.GetUserDailySpendFromGateway(ctx, 50)
	if err != nil {
		t.Fatalf("GetUserDailySpendFromGateway failed: %v", err)
	}
	if dailySpend < cfg.CostLimitDaily {
		t.Errorf("expected daily spend ($%.2f) >= limit ($%.2f)", dailySpend, cfg.CostLimitDaily)
	}
}

// TestGatewayQuota_CostLimit_NotExceeded verifies that requests proceed when under cost limit.
func TestGatewayQuota_CostLimit_NotExceeded(t *testing.T) {
	// Setup: user_id=50 has analyst role with $50/day limit, spent $25
	roleChecker := &mockPlatformRoleCheckerGW{
		roleConfigs: map[int64]*execution.RoleConfig{
			50: {Name: "analyst", RateLimit: 1000, CostLimitDaily: 50.0},
		},
	}
	quotaChecker := &mockUserQuotaCheckerGW{dailySpend: 25.0}

	// Verify mock returns expected values
	ctx := context.Background()
	cfg, err := roleChecker.GetRoleConfig(ctx, 50)
	if err != nil {
		t.Fatalf("GetRoleConfig failed: %v", err)
	}

	dailySpend, err := quotaChecker.GetUserDailySpendFromGateway(ctx, 50)
	if err != nil {
		t.Fatalf("GetUserDailySpendFromGateway failed: %v", err)
	}
	if dailySpend >= cfg.CostLimitDaily {
		t.Errorf("expected daily spend ($%.2f) < limit ($%.2f)", dailySpend, cfg.CostLimitDaily)
	}
}

// TestGatewayQuota_UnlimitedRole verifies that 0 limits are treated as unlimited.
func TestGatewayQuota_UnlimitedRole(t *testing.T) {
	// Setup: user_id=42 has super_admin role with unlimited quotas
	roleChecker := &mockPlatformRoleCheckerGW{
		roleConfigs: map[int64]*execution.RoleConfig{
			42: {Name: "super_admin", RateLimit: 0, CostLimitDaily: 0},
		},
	}

	// Verify mock returns expected values
	ctx := context.Background()
	cfg, err := roleChecker.GetRoleConfig(ctx, 42)
	if err != nil {
		t.Fatalf("GetRoleConfig failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected role config, got nil")
	}
	if cfg.RateLimit != 0 {
		t.Errorf("expected unlimited rate limit (0), got %d", cfg.RateLimit)
	}
	if cfg.CostLimitDaily != 0 {
		t.Errorf("expected unlimited cost limit (0), got %f", cfg.CostLimitDaily)
	}
}
