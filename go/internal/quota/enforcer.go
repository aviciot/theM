// Package quota enforces per-tenant run limits before a run is admitted.
//
// Four limits are enforced at run-start time (Admit):
//   - max_concurrent_runs: counts active runs in PostgreSQL.
//   - runs_per_minute: 1-minute INCR window in Redis.
//   - monthly_runs: monthly INCR counter in Redis.
//   - api_requests_per_minute: 1-minute INCR window in Redis (API admission rate).
//
// One limit is enforced at run-start time only when a monthly_llm_tokens quota is set:
//   - monthly_llm_tokens: sums total_tokens_in + total_tokens_out from them.runs
//     for the current calendar month in PostgreSQL.
//
// All limits are advisory when their quota field is nil — no quota row means no enforcement.
//
// Redis key scheme:
//
//	rl:them:{tenant_id}:runs:{minute}              TTL 90s   (runs_per_minute)
//	rl:them:{tenant_id}:runs:monthly:{YYYY-MM}     TTL ~month+48h (monthly_runs)
//	rl:them:{tenant_id}:api:{minute}               TTL 90s   (api_requests_per_minute)
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

// ErrMonthlyRunsExceeded is returned when monthly_runs is reached.
var ErrMonthlyRunsExceeded = errors.New("quota: monthly run limit exceeded")

// ErrAPIRateLimited is returned when api_requests_per_minute is exceeded.
var ErrAPIRateLimited = errors.New("quota: api requests per minute exceeded")

// ErrMonthlyLLMTokensExceeded is returned when monthly_llm_tokens is reached.
var ErrMonthlyLLMTokensExceeded = errors.New("quota: monthly LLM token limit exceeded")

const keyTTL = 90 * time.Second

// RunCounter queries the live count of active runs for a tenant.
// The production implementation wraps dal.DB; tests inject a fake.
type RunCounter interface {
	CountActiveRuns(ctx context.Context, tenantID string) (int, error)
}

// MonthlyTokenCounter queries the total LLM tokens used by a tenant in the current calendar month.
// Sums total_tokens_in + total_tokens_out from them.runs WHERE completed this month.
type MonthlyTokenCounter interface {
	SumMonthlyTokens(ctx context.Context, tenantID string) (int64, error)
}

// RedisIncrementer is the Redis interface required for per-minute run counting.
// Matches the subset already defined in ratelimit.RedisIncrementer.
type RedisIncrementer interface {
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// Quota carries the limits to enforce. Nil pointer fields mean "no limit".
type Quota struct {
	MaxConcurrentRuns    *int
	RunsPerMinute        *int
	MonthlyRuns          *int
	APIRequestsPerMinute *int
	MonthlyLLMTokens     *int64
}

// Enforcer checks quota limits before a run is admitted.
type Enforcer struct {
	db      RunCounter
	tokenDB MonthlyTokenCounter
	redis   RedisIncrementer
}

// New creates a production Enforcer. db and redis must not be nil.
// tokenDB may be nil — if nil, monthly_llm_tokens enforcement is skipped.
func New(db RunCounter, redis RedisIncrementer) *Enforcer {
	return &Enforcer{db: db, redis: redis}
}

// WithTokenCounter attaches a MonthlyTokenCounter to enable monthly_llm_tokens enforcement.
func (e *Enforcer) WithTokenCounter(tc MonthlyTokenCounter) *Enforcer {
	e.tokenDB = tc
	return e
}

// Check enforces all configured quota limits for tenantID.
// Returns a typed error sentinel when a limit is hit; nil when all pass.
// Returns nil when quota is zero-value (all limits nil) — no enforcement.
// A Redis or DB error is returned as-is so callers can decide whether to fail open or closed.
func (e *Enforcer) Check(ctx context.Context, tenantID string, q Quota) error {
	if err := e.checkConcurrent(ctx, tenantID, q.MaxConcurrentRuns); err != nil {
		return err
	}
	if err := e.checkRPM(ctx, tenantID, q.RunsPerMinute); err != nil {
		return err
	}
	if err := e.checkMonthly(ctx, tenantID, q.MonthlyRuns); err != nil {
		return err
	}
	if err := e.checkAPIRPM(ctx, tenantID, q.APIRequestsPerMinute); err != nil {
		return err
	}
	return e.checkMonthlyLLMTokens(ctx, tenantID, q.MonthlyLLMTokens)
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

func (e *Enforcer) checkMonthly(ctx context.Context, tenantID string, limit *int) error {
	if limit == nil {
		return nil
	}
	now := time.Now().UTC()
	// Key format: rl:them:{tenant_id}:runs:monthly:{YYYY-MM}
	// TTL: 48 h past end-of-month to survive minor clock skew.
	key := fmt.Sprintf("rl:them:%s:runs:monthly:%04d-%02d", tenantID, now.Year(), now.Month())
	count, err := e.redis.Incr(ctx, key)
	if err != nil {
		return fmt.Errorf("quota: incr monthly runs key: %w", err)
	}
	// Set TTL only on first increment; subsequent calls are no-ops if key already has a TTL.
	if count == 1 {
		// Calculate seconds remaining in current month + 48 h buffer.
		firstOfNext := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		ttl := firstOfNext.Sub(now) + 48*time.Hour
		_ = e.redis.Expire(ctx, key, ttl)
	}
	if count > int64(*limit) {
		return ErrMonthlyRunsExceeded
	}
	return nil
}

func (e *Enforcer) checkAPIRPM(ctx context.Context, tenantID string, limit *int) error {
	if limit == nil {
		return nil
	}
	key := fmt.Sprintf("rl:them:%s:api:%d", tenantID, minuteBucket())
	count, err := e.redis.Incr(ctx, key)
	if err != nil {
		return fmt.Errorf("quota: incr api/min key: %w", err)
	}
	_ = e.redis.Expire(ctx, key, keyTTL)
	if count > int64(*limit) {
		return ErrAPIRateLimited
	}
	return nil
}

func (e *Enforcer) checkMonthlyLLMTokens(ctx context.Context, tenantID string, limit *int64) error {
	if limit == nil || e.tokenDB == nil {
		return nil
	}
	used, err := e.tokenDB.SumMonthlyTokens(ctx, tenantID)
	if err != nil {
		// Fail-open: a DB error here should not block runs.
		return nil
	}
	if used >= *limit {
		return ErrMonthlyLLMTokensExceeded
	}
	return nil
}
