package metrics_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/metrics"
)

// ── Fake Redis ────────────────────────────────────────────────────────────────

type hIncrCall struct {
	key, field string
	delta      int64
}

type expireCall struct {
	key string
	t   time.Time
}

type pfAddCall struct {
	key      string
	elements []string
}

type fakeRedis struct {
	hincrs  []hIncrCall
	expires []expireCall
	pfadds  []pfAddCall
	err     error
}

func (f *fakeRedis) HIncrBy(_ context.Context, key, field string, delta int64) error {
	if f.err != nil {
		return f.err
	}
	f.hincrs = append(f.hincrs, hIncrCall{key, field, delta})
	return nil
}

func (f *fakeRedis) ExpireAt(_ context.Context, key string, t time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.expires = append(f.expires, expireCall{key, t})
	return nil
}

func (f *fakeRedis) PFAdd(_ context.Context, key string, elements ...string) error {
	if f.err != nil {
		return f.err
	}
	f.pfadds = append(f.pfadds, pfAddCall{key, elements})
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// MR-01: RecordRun increments runs on both tenant and app keys.
func TestRecordRun_TenantAndApp(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	err := rec.RecordRun(context.Background(), "t1", "app1")
	require.NoError(t, err)

	date := time.Now().UTC().Format("2006-01-02")
	tenantKey := fmt.Sprintf("them:metrics:t1:%s", date)
	appKey := fmt.Sprintf("them:metrics:t1:app:app1:%s", date)

	var tenantRunIncrFound, appRunIncrFound bool
	for _, c := range fake.hincrs {
		if c.key == tenantKey && c.field == "runs" && c.delta == 1 {
			tenantRunIncrFound = true
		}
		if c.key == appKey && c.field == "runs" && c.delta == 1 {
			appRunIncrFound = true
		}
	}
	assert.True(t, tenantRunIncrFound, "tenant runs counter should be incremented")
	assert.True(t, appRunIncrFound, "app runs counter should be incremented")
}

// MR-02: RecordRun with empty appID skips app key.
func TestRecordRun_NoApp(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	err := rec.RecordRun(context.Background(), "t1", "")
	require.NoError(t, err)

	// Only the tenant-level HINCRBY should have been called (one call).
	assert.Equal(t, 1, len(fake.hincrs), "only one HINCRBY for empty appID")
}

// MR-03: RecordTokens increments tokens_in and tokens_out on both tenant and app keys.
func TestRecordTokens_TenantAndApp(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	err := rec.RecordTokens(context.Background(), "t2", "app2", 100, 50)
	require.NoError(t, err)

	date := time.Now().UTC().Format("2006-01-02")
	tenantKey := fmt.Sprintf("them:metrics:t2:%s", date)
	appKey := fmt.Sprintf("them:metrics:t2:app:app2:%s", date)

	fields := map[string]map[string]int64{}
	for _, c := range fake.hincrs {
		if fields[c.key] == nil {
			fields[c.key] = map[string]int64{}
		}
		fields[c.key][c.field] = c.delta
	}

	assert.Equal(t, int64(100), fields[tenantKey]["tokens_in"])
	assert.Equal(t, int64(50), fields[tenantKey]["tokens_out"])
	assert.Equal(t, int64(100), fields[appKey]["tokens_in"])
	assert.Equal(t, int64(50), fields[appKey]["tokens_out"])
}

// MR-04: RecordMCPCall increments mcp_calls on both keys.
func TestRecordMCPCall_TenantAndApp(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	err := rec.RecordMCPCall(context.Background(), "t3", "app3")
	require.NoError(t, err)

	date := time.Now().UTC().Format("2006-01-02")
	tenantKey := fmt.Sprintf("them:metrics:t3:%s", date)
	appKey := fmt.Sprintf("them:metrics:t3:app:app3:%s", date)

	var tenantFound, appFound bool
	for _, c := range fake.hincrs {
		if c.key == tenantKey && c.field == "mcp_calls" && c.delta == 1 {
			tenantFound = true
		}
		if c.key == appKey && c.field == "mcp_calls" && c.delta == 1 {
			appFound = true
		}
	}
	assert.True(t, tenantFound, "tenant mcp_calls counter should be incremented")
	assert.True(t, appFound, "app mcp_calls counter should be incremented")
}

// MR-05: RecordUser calls PFADD with formatted userID and sets expiry on HLL key.
func TestRecordUser_PFAddAndExpiry(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	err := rec.RecordUser(context.Background(), "t4", 42)
	require.NoError(t, err)

	date := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("them:metrics:t4:users:%s", date)

	require.Len(t, fake.pfadds, 1)
	assert.Equal(t, key, fake.pfadds[0].key)
	assert.Contains(t, fake.pfadds[0].elements, "42")

	var expFound bool
	for _, e := range fake.expires {
		if e.key == key {
			expFound = true
		}
	}
	assert.True(t, expFound, "ExpireAt should be set on HLL key")
}

// MR-06: NoopRecorder returns nil for all methods.
func TestNoopRecorder_AllNil(t *testing.T) {
	var rec metrics.Recorder = metrics.NoopRecorder{}
	ctx := context.Background()

	assert.NoError(t, rec.RecordRun(ctx, "t", "a"))
	assert.NoError(t, rec.RecordTokens(ctx, "t", "a", 1, 2))
	assert.NoError(t, rec.RecordMCPCall(ctx, "t", "a"))
	assert.NoError(t, rec.RecordUser(ctx, "t", 1))
}

// MR-07: ExpireAt is called with a future timestamp (~32 days from now).
func TestRecordRun_ExpireAtFuture(t *testing.T) {
	fake := &fakeRedis{}
	rec := metrics.NewRedisRecorder(fake)

	before := time.Now().UTC()
	err := rec.RecordRun(context.Background(), "t5", "")
	require.NoError(t, err)

	require.NotEmpty(t, fake.expires, "ExpireAt should be called")
	for _, e := range fake.expires {
		assert.True(t, e.t.After(before.Add(31*24*time.Hour)),
			"expiry must be at least 31 days in the future, got %v", e.t)
		assert.True(t, e.t.Before(before.Add(33*24*time.Hour)),
			"expiry must be at most 33 days in the future, got %v", e.t)
	}
}
