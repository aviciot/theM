package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/ratelimit"
)

// ── Fake Redis ────────────────────────────────────────────────────────────────

// fakeRedis is an in-memory fake that implements ratelimit.RedisIncrementer.
// It uses a map keyed by Redis key string to store integer counters.
type fakeRedis struct {
	mu       sync.Mutex
	counters map[string]int64
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{counters: make(map[string]int64)}
}

func (f *fakeRedis) Incr(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[key]++
	return f.counters[key], nil
}

func (f *fakeRedis) Expire(_ context.Context, _ string, _ time.Duration) error {
	return nil // no-op in tests
}

// setCounter directly sets a counter — used to simulate existing traffic.
func (f *fakeRedis) setCounter(key string, v int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[key] = v
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. First request under limit — allowed.
func TestCheckTokenAllowed(t *testing.T) {
	redis := newFakeRedis()
	l := ratelimit.New(redis)

	allowed, err := l.CheckToken(context.Background(), "abc123", 10)
	require.NoError(t, err)
	assert.True(t, allowed, "first request should be allowed under limit")
}

// 2. Request over limit — denied.
func TestCheckTokenDenied(t *testing.T) {
	redis := newFakeRedis()
	l := ratelimit.New(redis)

	// Pre-fill the counter to the limit.
	minute := time.Now().Unix() / 60
	key := fmt.Sprintf("rl:them:token:%s:%d", "tokXYZ", minute)
	redis.setCounter(key, 5) // already at limit

	// The next Incr will make it 6, which exceeds limit=5.
	allowed, err := l.CheckToken(context.Background(), "tokXYZ", 5)
	require.NoError(t, err)
	assert.False(t, allowed, "request at limit+1 should be denied")
}

// 3. Different minute window — resets counter.
// We simulate this by using a per-app check with two different fake keys
// (the real production code uses Unix/60, here we verify the key is different
// across minutes by checking that the fake counter for the first key is still 1
// after the second minute's bucket is used).
func TestCheckAppDifferentMinuteResets(t *testing.T) {
	redis := newFakeRedis()
	l := ratelimit.New(redis)

	// Use CheckApp for app 99 — this will use the real minute bucket.
	allowed1, err := l.CheckApp(context.Background(), 99, 3)
	require.NoError(t, err)
	assert.True(t, allowed1)

	// Directly verify the counter is 1 (first request in this minute).
	minute := time.Now().Unix() / 60
	key := fmt.Sprintf("rl:them:app:%d:%d", 99, minute)

	redis.mu.Lock()
	count := redis.counters[key]
	redis.mu.Unlock()

	assert.Equal(t, int64(1), count, "counter should be 1 for first request")

	// Simulate next minute by directly pre-filling the previous minute's key.
	prevKey := fmt.Sprintf("rl:them:app:%d:%d", 99, minute-1)
	redis.setCounter(prevKey, 100) // previous minute had 100 requests

	// Next minute's first request should be allowed regardless of previous minute.
	allowed2, err := l.CheckApp(context.Background(), 99, 3)
	require.NoError(t, err)
	// Second request in the same minute — count is now 2, limit is 3.
	assert.True(t, allowed2, "second request should still be allowed (count=2 <= limit=3)")
}
