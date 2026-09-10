package metrics

import (
	"context"
	"fmt"
	"time"
)

// RedisMetricsClient is the Redis interface required by RedisRecorder.
// Production wraps rueidis; tests inject a fake.
type RedisMetricsClient interface {
	HIncrBy(ctx context.Context, key, field string, delta int64) error
	ExpireAt(ctx context.Context, key string, t time.Time) error
	PFAdd(ctx context.Context, key string, elements ...string) error
}

// Recorder accumulates per-tenant and per-app metrics in Redis.
type Recorder interface {
	RecordRun(ctx context.Context, tenantID, appID string) error
	RecordTokens(ctx context.Context, tenantID, appID string, tokensIn, tokensOut int64) error
	RecordMCPCall(ctx context.Context, tenantID, appID string) error
	RecordUser(ctx context.Context, tenantID string, userID int64) error
}

// RedisRecorder writes metrics to Redis using HINCRBY (counters) and PFADD (unique users).
//
// Key patterns:
//
//	them:metrics:{tenant_id}:{YYYY-MM-DD}              Hash  TTL 32 days  per-tenant daily totals
//	them:metrics:{tenant_id}:app:{app_id}:{YYYY-MM-DD} Hash  TTL 32 days  per-app daily totals
//	them:metrics:{tenant_id}:users:{YYYY-MM-DD}        HLL   TTL 32 days  unique users (PFADD)
//
// Hash fields: runs, tokens_in, tokens_out, mcp_calls
type RedisRecorder struct {
	redis RedisMetricsClient
}

// NewRedisRecorder creates a RedisRecorder backed by the given client.
func NewRedisRecorder(redis RedisMetricsClient) *RedisRecorder {
	return &RedisRecorder{redis: redis}
}

const metricsTTL = 32 * 24 * time.Hour

func dailyKey(tenantID, date string) string {
	return fmt.Sprintf("them:metrics:%s:%s", tenantID, date)
}

func appDailyKey(tenantID, appID, date string) string {
	return fmt.Sprintf("them:metrics:%s:app:%s:%s", tenantID, appID, date)
}

func usersKey(tenantID, date string) string {
	return fmt.Sprintf("them:metrics:%s:users:%s", tenantID, date)
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

func metricsExpireAt() time.Time {
	return time.Now().UTC().Add(metricsTTL)
}

func (r *RedisRecorder) incrField(ctx context.Context, key, field string, delta int64) error {
	if err := r.redis.HIncrBy(ctx, key, field, delta); err != nil {
		return err
	}
	return r.redis.ExpireAt(ctx, key, metricsExpireAt())
}

// RecordRun increments the runs counter for tenant and app daily buckets.
func (r *RedisRecorder) RecordRun(ctx context.Context, tenantID, appID string) error {
	date := todayUTC()
	if err := r.incrField(ctx, dailyKey(tenantID, date), "runs", 1); err != nil {
		return fmt.Errorf("metrics: record run tenant=%s: %w", tenantID, err)
	}
	if appID != "" {
		if err := r.incrField(ctx, appDailyKey(tenantID, appID, date), "runs", 1); err != nil {
			return fmt.Errorf("metrics: record run app=%s: %w", appID, err)
		}
	}
	return nil
}

// RecordTokens increments tokens_in and tokens_out for tenant and app daily buckets.
func (r *RedisRecorder) RecordTokens(ctx context.Context, tenantID, appID string, tokensIn, tokensOut int64) error {
	date := todayUTC()
	key := dailyKey(tenantID, date)
	if err := r.incrField(ctx, key, "tokens_in", tokensIn); err != nil {
		return fmt.Errorf("metrics: record tokens_in tenant=%s: %w", tenantID, err)
	}
	if err := r.incrField(ctx, key, "tokens_out", tokensOut); err != nil {
		return fmt.Errorf("metrics: record tokens_out tenant=%s: %w", tenantID, err)
	}
	if appID != "" {
		appKey := appDailyKey(tenantID, appID, date)
		if err := r.incrField(ctx, appKey, "tokens_in", tokensIn); err != nil {
			return fmt.Errorf("metrics: record tokens_in app=%s: %w", appID, err)
		}
		if err := r.incrField(ctx, appKey, "tokens_out", tokensOut); err != nil {
			return fmt.Errorf("metrics: record tokens_out app=%s: %w", appID, err)
		}
	}
	return nil
}

// RecordMCPCall increments the mcp_calls counter for tenant and app daily buckets.
func (r *RedisRecorder) RecordMCPCall(ctx context.Context, tenantID, appID string) error {
	date := todayUTC()
	if err := r.incrField(ctx, dailyKey(tenantID, date), "mcp_calls", 1); err != nil {
		return fmt.Errorf("metrics: record mcp_call tenant=%s: %w", tenantID, err)
	}
	if appID != "" {
		if err := r.incrField(ctx, appDailyKey(tenantID, appID, date), "mcp_calls", 1); err != nil {
			return fmt.Errorf("metrics: record mcp_call app=%s: %w", appID, err)
		}
	}
	return nil
}

// RecordUser adds userID to the HyperLogLog unique-user set for the tenant today.
func (r *RedisRecorder) RecordUser(ctx context.Context, tenantID string, userID int64) error {
	date := todayUTC()
	key := usersKey(tenantID, date)
	elem := fmt.Sprintf("%d", userID)
	if err := r.redis.PFAdd(ctx, key, elem); err != nil {
		return fmt.Errorf("metrics: record user tenant=%s: %w", tenantID, err)
	}
	return r.redis.ExpireAt(ctx, key, metricsExpireAt())
}

// NoopRecorder discards all metrics. Used when Redis is not configured.
type NoopRecorder struct{}

func (NoopRecorder) RecordRun(_ context.Context, _, _ string) error                { return nil }
func (NoopRecorder) RecordTokens(_ context.Context, _, _ string, _, _ int64) error { return nil }
func (NoopRecorder) RecordMCPCall(_ context.Context, _, _ string) error             { return nil }
func (NoopRecorder) RecordUser(_ context.Context, _ string, _ int64) error          { return nil }
