package admin

import (
	"context"

	"github.com/redis/rueidis"
)

// PolicyCacheInvalidator invalidates gateway policy caches in Redis.
type PolicyCacheInvalidator struct {
	redis rueidis.Client
	db    DBQuerier
}

// NewPolicyCacheInvalidator creates a cache invalidator. redis may be nil
// (no invalidation performed).
func NewPolicyCacheInvalidator(redis rueidis.Client, db DBQuerier) *PolicyCacheInvalidator {
	return &PolicyCacheInvalidator{redis: redis, db: db}
}

// InvalidateTenant invalidates all gateway clients in a tenant (called after
// tenant policy is updated).
func (p *PolicyCacheInvalidator) InvalidateTenant(ctx context.Context, tenantID string) error {
	if p.redis == nil {
		return nil
	}

	// Load all client IDs for this tenant
	const q = `SELECT id::text FROM them.gateway_clients WHERE tenant_id = $1::uuid`
	rows, err := p.db.Query(ctx, q, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var clientID string
		if err := rows.Scan(&clientID); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, "them:gateway:policy:v2:"+clientID)
	}
	rows.Close()

	// Delete all keys in one batch (best-effort, ignore errors)
	if len(keys) > 0 {
		dels := make([]rueidis.Completed, 0, len(keys))
		for _, key := range keys {
			dels = append(dels, p.redis.B().Del().Key(key).Build())
		}
		for _, cmd := range dels {
			_ = p.redis.Do(ctx, cmd)
		}
	}
	return nil
}

// InvalidateProfile invalidates all gateway clients using a specific policy
// profile (called after profile is created/updated/deleted).
func (p *PolicyCacheInvalidator) InvalidateProfile(ctx context.Context, profileID string) error {
	if p.redis == nil {
		return nil
	}

	// Load all client IDs using this profile
	const q = `SELECT id::text FROM them.gateway_clients WHERE governance_profile_id = $1::uuid`
	rows, err := p.db.Query(ctx, q, profileID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var clientID string
		if err := rows.Scan(&clientID); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, "them:gateway:policy:v2:"+clientID)
	}
	rows.Close()

	// Delete all keys in one batch (best-effort, ignore errors)
	if len(keys) > 0 {
		dels := make([]rueidis.Completed, 0, len(keys))
		for _, key := range keys {
			dels = append(dels, p.redis.B().Del().Key(key).Build())
		}
		for _, cmd := range dels {
			_ = p.redis.Do(ctx, cmd)
		}
	}
	return nil
}
