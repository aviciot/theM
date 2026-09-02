// Package quota enforces per-tenant run limits before a run is admitted.
//
// Two limits are enforced at run-start time:
//   - max_concurrent_runs: counts active runs (admitted/running/input_required)
//     in PostgreSQL; fails with ErrConcurrentRunsExceeded when the limit is hit.
//   - runs_per_minute: 1-minute INCR window in Redis;
//     fails with ErrRunsRateLimited when the limit is hit.
//
// Both limits are advisory when their quota field is nil (no row or NULL column).
//
// Redis key scheme:
//
//	rl:them:{tenant_id}:runs:{minute}   TTL 90s
package quota

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrConcurrentRunsExceeded is returned when max_concurrent_runs is reached.
var ErrConcurrentRunsExceeded = errors.New("quota: max concurrent runs exceeded")

// ErrRunsRateLimited is returned when runs_per_minute is exceeded.
var ErrRunsRateLimited = errors.New("quota: runs per minute exceeded")

const keyTTL = 90 * time.Second

// RunCounter queries the live count of active runs for a tenant.
// The production implementation wraps dal.DB; tests inject a fake.
type RunCounter interface {
	CountActiveRuns(ctx context.Context, tenantID string) (int, error)
}

// RedisIncrementer is the Redis interface required for per-minute run counting.
// Matches the subset already defined in ratelimit.RedisIncrementer.
type RedisIncrementer interface {
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// Quota carries the limits to enforce. Nil pointer fields mean "no limit".
type Quota struct {
	MaxConcurrentRuns *int
	RunsPerMinute     *int
}

// Enforcer checks quota limits before a run is admitted.
type Enforcer struct {
	db    RunCounter
	redis RedisIncrementer
}

// New creates a production Enforcer. Both db and redis must not be nil.
func New(db RunCounter, redis RedisIncrementer) *Enforcer {
	return &Enforcer{db: db, redis: redis}
}

// Check enforces both concurrent-run and per-minute-run limits for tenantID.
// Returns ErrConcurrentRunsExceeded or ErrRunsRateLimited when a limit is hit.
// Returns nil when quota is zero-value (both limits nil) — no quota row means no enforcement.
// A Redis or DB error is returned as-is so callers can decide whether to fail open or closed.
func (e *Enforcer) Check(ctx context.Context, tenantID string, q Quota) error {
	if err := e.checkConcurrent(ctx, tenantID, q.MaxConcurrentRuns); err != nil {
		return err
	}
	return e.checkRPM(ctx, tenantID, q.RunsPerMinute)
}

func (e *Enforcer) checkConcurrent(ctx context.Context, tenantID string, limit *int) error {
	if limit == nil {
		return nil
	}
	count, err := e.db.CountActiveRuns(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("quota: count active runs: %w", err)
	}
	if count >= *limit {
		return ErrConcurrentRunsExceeded
	}
	return nil
}

func (e *Enforcer) checkRPM(ctx context.Context, tenantID string, limit *int) error {
	if limit == nil {
		return nil
	}
	key := fmt.Sprintf("rl:them:%s:runs:%d", tenantID, minuteBucket())
	count, err := e.redis.Incr(ctx, key)
	if err != nil {
		return fmt.Errorf("quota: incr runs/min key: %w", err)
	}
	_ = e.redis.Expire(ctx, key, keyTTL)
	if count > int64(*limit) {
		return ErrRunsRateLimited
	}
	return nil
}

func minuteBucket() int64 { return time.Now().Unix() / 60 }
