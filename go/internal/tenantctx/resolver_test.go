package tenantctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/tenantctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver implements SlugResolver for unit tests without a DB.
type fakeResolver struct {
	m map[string]string // slug → tenantID
}

func (f *fakeResolver) ResolveSlug(_ context.Context, slug string) (string, error) {
	if id, ok := f.m[slug]; ok {
		return id, nil
	}
	return "", tenantctx.ErrTenantSlugNotFound
}

// TS-01: known slug returns correct UUID.
func TestSlugResolver_HappyPath(t *testing.T) {
	r := &fakeResolver{m: map[string]string{"default": "00000000-0000-0000-0000-000000000001"}}
	id, err := r.ResolveSlug(context.Background(), "default")
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", id)
}

// TS-02: unknown slug returns ErrTenantSlugNotFound.
func TestSlugResolver_NotFound(t *testing.T) {
	r := &fakeResolver{m: map[string]string{}}
	_, err := r.ResolveSlug(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.True(t, errors.Is(err, tenantctx.ErrTenantSlugNotFound))
}

// TS-03: two slugs return distinct UUIDs.
func TestSlugResolver_TwoSlugsDistinct(t *testing.T) {
	r := &fakeResolver{m: map[string]string{
		"default":  "00000000-0000-0000-0000-000000000001",
		"avi-test": "00000000-0000-0000-0000-000000000002",
	}}
	id1, err1 := r.ResolveSlug(context.Background(), "default")
	require.NoError(t, err1)
	id2, err2 := r.ResolveSlug(context.Background(), "avi-test")
	require.NoError(t, err2)
	assert.NotEqual(t, id1, id2)
}

// TS-04: PgxSlugResolver with nil pool returns error on ResolveSlug, not a panic.
func TestPgxSlugResolver_NilPoolReturnsError(t *testing.T) {
	r := tenantctx.NewPgxSlugResolver(nil)
	require.NotNil(t, r)
	_, err := r.ResolveSlug(context.Background(), "any")
	assert.Error(t, err, "nil pool must return an error, not panic")
}

// Compile-time interface check.
var _ tenantctx.SlugResolver = (*tenantctx.PgxSlugResolver)(nil)
var _ tenantctx.SlugResolver = (*fakeResolver)(nil)
