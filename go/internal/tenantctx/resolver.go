package tenantctx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTenantSlugNotFound is returned when no enabled tenant matches the slug.
var ErrTenantSlugNotFound = errors.New("tenantctx: tenant slug not found or disabled")

// SlugResolver resolves a tenant slug to its UUID.
type SlugResolver interface {
	ResolveSlug(ctx context.Context, slug string) (tenantID string, err error)
}

type cacheEntry struct {
	tenantID string
	expiry   time.Time
}

// PgxSlugResolver implements SlugResolver against a live pgxpool.Pool.
// Results are cached in a sync.Map with a 5-minute TTL to avoid a DB
// round-trip on every WS/SSE/A2A connection.
type PgxSlugResolver struct {
	pool  *pgxpool.Pool
	cache sync.Map
	ttl   time.Duration
}

// NewPgxSlugResolver creates a resolver backed by the given pool.
// Use rlsPools.Admin — the tenants table requires BYPASSRLS for cross-tenant reads.
func NewPgxSlugResolver(pool *pgxpool.Pool) *PgxSlugResolver {
	return &PgxSlugResolver{pool: pool, ttl: 5 * time.Minute}
}

const slugLookupQuery = `
SELECT id FROM them.tenants WHERE slug = $1 AND enabled = true LIMIT 1`

// ResolveSlug returns the UUID for the given slug, or ErrTenantSlugNotFound
// if no enabled tenant with that slug exists. Returns an error if the pool is nil.
func (r *PgxSlugResolver) ResolveSlug(ctx context.Context, slug string) (string, error) {
	if r.pool == nil {
		return "", fmt.Errorf("tenantctx: resolver has no DB pool")
	}
	now := time.Now()
	if v, ok := r.cache.Load(slug); ok {
		e := v.(cacheEntry)
		if e.expiry.After(now) {
			return e.tenantID, nil
		}
		r.cache.Delete(slug)
	}

	var tenantID string
	err := r.pool.QueryRow(ctx, slugLookupQuery, slug).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrTenantSlugNotFound, slug)
		}
		return "", fmt.Errorf("tenantctx: resolve slug %q: %w", slug, err)
	}

	r.cache.Store(slug, cacheEntry{tenantID: tenantID, expiry: now.Add(r.ttl)})
	return tenantID, nil
}
