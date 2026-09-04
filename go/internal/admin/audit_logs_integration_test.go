//go:build integration

package admin_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// AL-04: Cross-tenant isolation — rows written for tenant B must not appear
// when querying with tenant A's context. Requires a live PostgreSQL instance.
// Run with: go test -tags=integration -v ./internal/admin/... -run TestAuditLogs_CrossTenantIsolation
func TestAuditLogs_CrossTenantIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()

	// Use admin DSN for both pools (test environment; BYPASSRLS required for writes).
	pools, err := db.NewPools(ctx, dsn, dsn)
	require.NoError(t, err)
	defer pools.Close()

	adminDB := dal.NewDBFromAdminQuerier(pools.NewAdminQuerier())

	// These must be real tenant UUIDs that exist (or do not — the FK may be nullable in test).
	// We use the bootstrap tenant for A (always exists) and a stable test UUID for B.
	const tenantA = "00000000-0000-0000-0000-000000000001"
	const tenantB = "00000000-0000-0000-0000-000000000002"
	const markerEntityID = "al04-isolation-marker"

	// Write a row scoped to tenant B.
	err = adminDB.WriteAuditLog(ctx, dal.AuditEntry{
		TenantID:   tenantB,
		Action:     "agent.create",
		EntityType: "agent",
		EntityID:   markerEntityID,
		Actor:      "test@example.com",
	})
	require.NoError(t, err)

	// Query as tenant A — marker must not appear.
	logsA, err := adminDB.ListAuditLogs(ctx, tenantA, 200, 0)
	require.NoError(t, err)
	for _, l := range logsA {
		if l.EntityID != nil {
			assert.NotEqual(t, markerEntityID, *l.EntityID,
				"tenant B row leaked into tenant A result set")
		}
	}

	// Query as tenant B — marker must be present.
	logsB, err := adminDB.ListAuditLogs(ctx, tenantB, 200, 0)
	require.NoError(t, err)
	found := false
	for _, l := range logsB {
		if l.EntityID != nil && *l.EntityID == markerEntityID {
			found = true
			break
		}
	}
	assert.True(t, found, "tenant B marker row not found when querying as tenant B")
}
