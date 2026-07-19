package auth_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// Mock TokenQuerier
// ──────────────────────────────────────────────────────────────────────────────

type mockTokenQuerier struct {
	mu       sync.Mutex
	callsLog []string // records the hash arg of each QueryToken call
	rows     map[string]*auth.TokenRow
}

func newMockQuerier() *mockTokenQuerier {
	return &mockTokenQuerier{rows: make(map[string]*auth.TokenRow)}
}

func (m *mockTokenQuerier) addToken(hashHex string, row *auth.TokenRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[hashHex] = row
}

func (m *mockTokenQuerier) QueryToken(_ context.Context, hashHex string) (*auth.TokenRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callsLog = append(m.callsLog, hashHex)
	row, ok := m.rows[hashHex]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return row, nil
}

func (m *mockTokenQuerier) callCount(hashHex string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, h := range m.callsLog {
		if h == hashHex {
			n++
		}
	}
	return n
}

// ──────────────────────────────────────────────────────────────────────────────
// Mock RedisClient
// ──────────────────────────────────────────────────────────────────────────────

type mockRedis struct {
	mu          sync.Mutex
	store       map[string][]byte
	subscribers []func(payload string)
}

func newMockRedis() *mockRedis {
	return &mockRedis{store: make(map[string][]byte)}
}

func (r *mockRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.store[key]
	return v, ok, nil
}

func (r *mockRedis) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	r.store[key] = cp
	return nil
}

func (r *mockRedis) Del(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}

func (r *mockRedis) Publish(_ context.Context, _ string, payload string) error {
	r.mu.Lock()
	subs := make([]func(string), len(r.subscribers))
	copy(subs, r.subscribers)
	r.mu.Unlock()
	// Call handlers synchronously so tests don't need sleep.
	for _, fn := range subs {
		fn(payload)
	}
	return nil
}

func (r *mockRedis) Subscribe(ctx context.Context, _ string, handler func(payload string)) error {
	r.mu.Lock()
	r.subscribers = append(r.subscribers, handler)
	r.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (r *mockRedis) subscriberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subscribers)
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────────────────────────────────────

// testTokenHash replicates the sha256 hex formula from tokenHash() in jwt.go.
// We cannot call the unexported function from the test package, so we duplicate
// the exact formula here to pre-seed mock queriers with the right hash.
func testTokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h)
}

func testRow() *auth.TokenRow {
	now := time.Now()
	return &auth.TokenRow{
		ID:            1,
		ApplicationID: 0,
		Permissions:   []string{"read", "write"},
		CreatedAt:     now,
		ExpiresAt:     nil,
	}
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

const testRawToken = "test-bearer-token-abc123"

// ──────────────────────────────────────────────────────────────────────────────
// Token cache tests
// ──────────────────────────────────────────────────────────────────────────────

// Test 1: Validate with a valid token returns TokenInfo (mock DB returning a row).
func TestTokenCache_Validate_Hit(t *testing.T) {
	querier := newMockQuerier()
	rdb := newMockRedis()
	hash := testTokenHash(testRawToken)
	querier.addToken(hash, testRow())

	cache := auth.NewCache(querier, rdb, noopLogger())

	info, err := cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, int64(1), info.TokenID)
	assert.ElementsMatch(t, []string{"read", "write"}, info.Permissions)
}

// Test 2: Validate with an unknown token returns ErrTokenNotFound.
func TestTokenCache_Validate_Miss(t *testing.T) {
	querier := newMockQuerier() // empty — no tokens registered
	rdb := newMockRedis()
	cache := auth.NewCache(querier, rdb, noopLogger())

	_, err := cache.Validate(context.Background(), "unknown-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrTokenNotFound), "expected ErrTokenNotFound, got: %v", err)
}

// Test 3: Validate hits L1 cache on second call — DB queried only once.
func TestTokenCache_Validate_L1Cache(t *testing.T) {
	querier := newMockQuerier()
	rdb := newMockRedis()
	hash := testTokenHash(testRawToken)
	querier.addToken(hash, testRow())

	cache := auth.NewCache(querier, rdb, noopLogger())

	// First call — L1/L2 miss, falls through to DB.
	_, err := cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 1, querier.callCount(hash), "DB should be called once on first Validate")

	// Second call — L1 hit, DB must NOT be called again.
	_, err = cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 1, querier.callCount(hash), "DB should NOT be called again after L1 cache population")
}

// Test 4: Revoke evicts from L1 — subsequent Validate goes to DB again.
func TestTokenCache_Revoke_EvictsL1(t *testing.T) {
	querier := newMockQuerier()
	rdb := newMockRedis()
	hash := testTokenHash(testRawToken)
	querier.addToken(hash, testRow())

	cache := auth.NewCache(querier, rdb, noopLogger())

	// Seed L1 via Validate.
	_, err := cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 1, querier.callCount(hash), "should have hit DB once")

	// Revoke clears L1 (and L2).
	require.NoError(t, cache.Revoke(context.Background(), testRawToken))

	// Re-validate — L1 was evicted, should hit DB again.
	_, err = cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 2, querier.callCount(hash), "DB should be called again after Revoke clears L1")
}

// Test 5: Cross-pod invalidation via pub/sub — Subscribe removes L1 entry on
// receipt of a revocation message.
//
// Design: mockRedis.Subscribe blocks until ctx is cancelled (simulating a real
// pub/sub connection). mockRedis.Publish delivers to all registered handlers
// synchronously. We therefore need to wait until the Subscribe goroutine has
// appended its handler before we call Publish. We do this by polling
// rdb.subscriberCount() rather than sleeping.
func TestTokenCache_Subscribe_CrossPodInvalidation(t *testing.T) {
	querier := newMockQuerier()
	rdb := newMockRedis()
	hash := testTokenHash(testRawToken)
	querier.addToken(hash, testRow())

	cache := auth.NewCache(querier, rdb, noopLogger())

	// Start subscription goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Subscribe(ctx)

	// Wait until Subscribe has registered its handler with the mock redis.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rdb.subscriberCount() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.Greater(t, rdb.subscriberCount(), 0, "Subscribe goroutine did not register within 2s")

	// Populate L1 via Validate.
	_, err := cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 1, querier.callCount(hash), "DB called once")

	// Simulate a revocation by another pod:
	//   1. The revoking pod deletes L2 (them:session:token:{hash}).
	//   2. The revoking pod publishes the hash on them:token:revoked.
	// This pod's Subscribe handler evicts L1. L2 is already gone.
	// The mock Publish calls all subscribers synchronously, so the L1 eviction
	// happens inline before Publish returns.
	l2Key := "them:session:token:" + hash
	require.NoError(t, rdb.Del(context.Background(), l2Key), "simulate revoker deleting L2")
	require.NoError(t, rdb.Publish(context.Background(), "them:token:revoked", hash))

	// Re-validate — L1 and L2 are both gone, DB must be queried again.
	_, err = cache.Validate(context.Background(), testRawToken)
	require.NoError(t, err)
	assert.Equal(t, 2, querier.callCount(hash), "DB should be called again after cross-pod pub/sub eviction")
}
