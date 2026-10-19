package admin

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
)

// mockDB implements dal.Querier for testing.
type mockDBForCache struct {
	queryFunc func(ctx context.Context, query string, args ...interface{}) (dal.RowScanner, error)
}

func (m *mockDBForCache) Query(ctx context.Context, query string, args ...interface{}) (dal.RowScanner, error) {
	return m.queryFunc(ctx, query, args...)
}

func (m *mockDBForCache) QueryRow(ctx context.Context, query string, args ...interface{}) dal.SingleRowScanner {
	return nil // not used in cache invalidator
}

func (m *mockDBForCache) Exec(ctx context.Context, query string, args ...interface{}) error {
	return nil // not used in cache invalidator
}

func (m *mockDBForCache) ExecReturning(ctx context.Context, query string, args ...interface{}) dal.SingleRowScanner {
	return nil // not used in cache invalidator
}

// mockRows implements dal.RowScanner for testing.
type mockRows struct {
	data   []string
	idx    int
	closed bool
}

func (m *mockRows) Close() error {
	m.closed = true
	return nil
}

func (m *mockRows) Next() bool {
	if m.idx >= len(m.data) {
		return false
	}
	return true
}

func (m *mockRows) Scan(dest ...interface{}) error {
	if m.idx >= len(m.data) {
		return nil
	}
	// Assume single string column
	if len(dest) > 0 {
		if ptr, ok := dest[0].(*string); ok {
			*ptr = m.data[m.idx]
		}
	}
	m.idx++
	return nil
}

func TestPolicyCacheInvalidator_InvalidateTenant(t *testing.T) {
	ctx := context.Background()
	tenantID := "test-tenant-id"
	clientIDs := []string{"client-1", "client-2", "client-3"}

	db := &mockDBForCache{
		queryFunc: func(ctx context.Context, query string, args ...interface{}) (dal.RowScanner, error) {
			return &mockRows{data: clientIDs}, nil
		},
	}

	// No Redis = no-op (should not panic)
	inv := NewPolicyCacheInvalidator(nil, db)
	if err := inv.InvalidateTenant(ctx, tenantID); err != nil {
		t.Fatalf("InvalidateTenant with nil redis failed: %v", err)
	}

	// With Redis mock (we can't easily mock rueidis.Client, so just verify nil doesn't panic)
	// Real test would need integration test with actual Redis
}

func TestPolicyCacheInvalidator_InvalidateProfile(t *testing.T) {
	ctx := context.Background()
	profileID := "profile-uuid"
	clientIDs := []string{"client-a", "client-b"}

	db := &mockDBForCache{
		queryFunc: func(ctx context.Context, query string, args ...interface{}) (dal.RowScanner, error) {
			return &mockRows{data: clientIDs}, nil
		},
	}

	// No Redis = no-op (should not panic)
	inv := NewPolicyCacheInvalidator(nil, db)
	if err := inv.InvalidateProfile(ctx, profileID); err != nil {
		t.Fatalf("InvalidateProfile with nil redis failed: %v", err)
	}
}

// TestPolicyCacheInvalidator_NilRedis verifies that nil redis is safe (no-op behavior).
func TestPolicyCacheInvalidator_NilRedis(t *testing.T) {
	db := &mockDBForCache{
		queryFunc: func(ctx context.Context, query string, args ...interface{}) (dal.RowScanner, error) {
			return &mockRows{data: []string{}}, nil
		},
	}

	inv := NewPolicyCacheInvalidator(nil, db)

	ctx := context.Background()
	if err := inv.InvalidateTenant(ctx, "tenant-1"); err != nil {
		t.Errorf("InvalidateTenant should be no-op with nil redis, got error: %v", err)
	}

	if err := inv.InvalidateProfile(ctx, "profile-1"); err != nil {
		t.Errorf("InvalidateProfile should be no-op with nil redis, got error: %v", err)
	}
}
