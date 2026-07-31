package tenantctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/tenantctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-01: Round-trip — WithTenantID stores and TenantIDFromCtx retrieves.
func TestTenantCtx_RoundTrip(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	ctx := tenantctx.WithTenantID(context.Background(), id)

	got, err := tenantctx.TenantIDFromCtx(ctx)
	require.NoError(t, err)
	assert.Equal(t, id, got)
}

// TC-02: Missing tenant — empty context returns ErrNoTenant.
func TestTenantCtx_MissingTenant(t *testing.T) {
	_, err := tenantctx.TenantIDFromCtx(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, tenantctx.ErrNoTenant),
		"expected ErrNoTenant, got: %v", err)
}

// TC-03: Empty string stored — TenantIDFromCtx returns ErrInvalidTenant.
func TestTenantCtx_EmptyStringIsInvalid(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), "")
	_, err := tenantctx.TenantIDFromCtx(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, tenantctx.ErrInvalidTenant),
		"expected ErrInvalidTenant, got: %v", err)
}

// TC-04: Two tenants in separate contexts do not cross-contaminate.
func TestTenantCtx_TwoTenantsIndependent(t *testing.T) {
	const alpha = "aaaaaaaa-0000-0000-0000-000000000001"
	const bravo = "bbbbbbbb-0000-0000-0000-000000000002"

	ctxA := tenantctx.WithTenantID(context.Background(), alpha)
	ctxB := tenantctx.WithTenantID(context.Background(), bravo)

	gotA, err := tenantctx.TenantIDFromCtx(ctxA)
	require.NoError(t, err)
	assert.Equal(t, alpha, gotA)

	gotB, err := tenantctx.TenantIDFromCtx(ctxB)
	require.NoError(t, err)
	assert.Equal(t, bravo, gotB)

	// Cross-check: ctxA must not have bravo's ID.
	assert.NotEqual(t, bravo, gotA)
	assert.NotEqual(t, alpha, gotB)
}

// TC-05: Child context inherits tenant; override in child does not affect parent.
func TestTenantCtx_ChildOverrideDoesNotAffectParent(t *testing.T) {
	const parent = "pppppppp-0000-0000-0000-000000000001"
	const child = "cccccccc-0000-0000-0000-000000000002"

	parentCtx := tenantctx.WithTenantID(context.Background(), parent)
	childCtx := tenantctx.WithTenantID(parentCtx, child)

	gotChild, err := tenantctx.TenantIDFromCtx(childCtx)
	require.NoError(t, err)
	assert.Equal(t, child, gotChild)

	// Parent context is unchanged.
	gotParent, err := tenantctx.TenantIDFromCtx(parentCtx)
	require.NoError(t, err)
	assert.Equal(t, parent, gotParent)
}

// TC-06: MustTenantIDFromCtx panics when tenant is absent.
func TestTenantCtx_MustPanicsOnMissing(t *testing.T) {
	assert.Panics(t, func() {
		tenantctx.MustTenantIDFromCtx(context.Background())
	})
}

// TC-07: MustTenantIDFromCtx returns the value when present.
func TestTenantCtx_MustReturnsValue(t *testing.T) {
	const id = "dddddddd-0000-0000-0000-000000000003"
	ctx := tenantctx.WithTenantID(context.Background(), id)
	got := tenantctx.MustTenantIDFromCtx(ctx)
	assert.Equal(t, id, got)
}

// TC-08: tenantKey is not a string, so a raw string key cannot retrieve the value.
// This verifies no stringly-typed key collision.
func TestTenantCtx_StringKeyCannotOverride(t *testing.T) {
	// Store a value using a raw string key — must not be retrieved by TenantIDFromCtx.
	type stringKey string
	ctx := context.WithValue(context.Background(), stringKey("tenant_id"), "spoofed-tenant")

	_, err := tenantctx.TenantIDFromCtx(ctx)
	assert.True(t, errors.Is(err, tenantctx.ErrNoTenant),
		"a string key must not be retrievable via TenantIDFromCtx; got: %v", err)
}
