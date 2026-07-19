package session_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviciot/them/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// Mock RedisClient
// ──────────────────────────────────────────────────────────────────────────────

type kv struct {
	value     string
	expiresAt time.Time // zero = no expiry
}

type mockRedis struct {
	mu          sync.Mutex
	store       map[string]*kv
	sets        map[string]map[string]struct{}
	subscribers map[string][]func(string)
	luaLog      []string // log of Lua calls for assertions
}

func newMockRedis() *mockRedis {
	return &mockRedis{
		store:       make(map[string]*kv),
		sets:        make(map[string]map[string]struct{}),
		subscribers: make(map[string][]func(string)),
	}
}

func (r *mockRedis) exists(key string) bool {
	entry, ok := r.store[key]
	if !ok {
		return false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(r.store, key)
		return false
	}
	return true
}

func (r *mockRedis) HSetEx(_ context.Context, key string, ttl time.Duration, fields map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Encode fields as a JSON-like string for simplicity.
	var sb strings.Builder
	for k, v := range fields {
		fmt.Fprintf(&sb, "%s=%s;", k, v)
	}
	r.store[key] = &kv{value: sb.String(), expiresAt: time.Now().Add(ttl)}
	return nil
}

func (r *mockRedis) HGetAll(_ context.Context, key string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.store[key]
	if !ok {
		return map[string]string{}, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(r.store, key)
		return map[string]string{}, nil
	}
	// Decode fields.
	fields := make(map[string]string)
	for _, pair := range strings.Split(entry.value, ";") {
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx < 0 {
			continue
		}
		fields[pair[:idx]] = pair[idx+1:]
	}
	return fields, nil
}

func (r *mockRedis) Del(_ context.Context, keys ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range keys {
		delete(r.store, k)
	}
	return nil
}

func (r *mockRedis) Expire(_ context.Context, key string, ttl time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.store[key]; ok {
		entry.expiresAt = time.Now().Add(ttl)
	}
	return nil
}

func (r *mockRedis) Exists(_ context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exists(key), nil
}

// ExecLua executes the three Lua scripts defined in session.go in-process.
// We match by detecting the script's first unique keyword.
func (r *mockRedis) ExecLua(_ context.Context, script string, keys []string, args []interface{}) (interface{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.luaLog = append(r.luaLog, script)

	switch {
	case strings.Contains(script, "'SADD'") && strings.Contains(script, "'SET'"):
		// luaRegister
		setKey := keys[0]
		shadowKey := keys[1]
		member := args[0].(string)
		ttlSec := 0
		fmt.Sscanf(fmt.Sprintf("%v", args[1]), "%d", &ttlSec)

		if r.sets[setKey] == nil {
			r.sets[setKey] = make(map[string]struct{})
		}
		r.sets[setKey][member] = struct{}{}
		r.store[shadowKey] = &kv{
			value:     "1",
			expiresAt: time.Now().Add(time.Duration(ttlSec) * time.Second),
		}
		return int64(1), nil

	case strings.Contains(script, "'SREM'") && strings.Contains(script, "'DEL'"):
		// luaEnd
		setKey := keys[0]
		shadowKey := keys[1]
		member := args[0].(string)

		if s, ok := r.sets[setKey]; ok {
			delete(s, member)
		}
		delete(r.store, shadowKey)
		return int64(1), nil

	case strings.Contains(script, "'SMEMBERS'") && strings.Contains(script, "shadow"):
		// luaPruneAndCount
		setKey := keys[0]
		shadowPrefix := args[0].(string)

		members := r.sets[setKey]
		live := 0
		for sid := range members {
			shadowKey := shadowPrefix + sid
			if r.exists(shadowKey) {
				live++
			} else {
				delete(members, sid)
			}
		}
		return int64(live), nil

	default:
		return nil, errors.New("mock: unknown Lua script")
	}
}

func (r *mockRedis) Publish(_ context.Context, channel, payload string) error {
	r.mu.Lock()
	subs := append([]func(string){}, r.subscribers[channel]...)
	r.mu.Unlock()
	for _, fn := range subs {
		fn(payload)
	}
	return nil
}

func (r *mockRedis) Subscribe(ctx context.Context, channel string, handler func(payload string)) error {
	r.mu.Lock()
	r.subscribers[channel] = append(r.subscribers[channel], handler)
	r.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

// helper: set a shadow key's expiry in the past to simulate expiry.
func (r *mockRedis) expireShadow(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.store[key]; ok {
		entry.expiresAt = time.Now().Add(-1 * time.Second)
	}
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// ──────────────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────────────

func testInfo(sessionID string) session.SessionInfo {
	return session.SessionInfo{
		SessionID:        sessionID,
		InstanceID:       "pod-1",
		UserID:           42,
		OrchestratorName: "default",
		EPSlug:           "ep-test",
		AppID:            "app-abc",
		ContextID:        "ctx-1",
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// Test 1: Register stores session hash and adds to EP/app Sets with shadow keys.
func TestStore_Register_StoresHashAndSets(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	info := testInfo("sess-001")
	require.NoError(t, store.Register(context.Background(), info))

	// Session hash was written.
	got, err := store.Get(context.Background(), "sess-001")
	require.NoError(t, err)
	assert.Equal(t, "sess-001", got.SessionID)
	assert.Equal(t, int64(42), got.UserID)

	// EP shadow key exists.
	shadowEP := "them:ep:ep-test:shadow:sess-001"
	ok, _ := rdb.Exists(context.Background(), shadowEP)
	assert.True(t, ok, "EP shadow key should exist")

	// App shadow key exists.
	shadowApp := "them:app:app-abc:shadow:sess-001"
	ok, _ = rdb.Exists(context.Background(), shadowApp)
	assert.True(t, ok, "app shadow key should exist")

	// Active session count incremented.
	assert.Equal(t, int32(1), store.ActiveSessions())
}

// Test 2: End removes hash, Set membership, and shadow keys.
func TestStore_End_Cleanup(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	info := testInfo("sess-002")
	require.NoError(t, store.Register(context.Background(), info))
	assert.Equal(t, int32(1), store.ActiveSessions())

	require.NoError(t, store.End(context.Background(), "sess-002", "ep-test", "app-abc"))

	// Session hash gone.
	_, err := store.Get(context.Background(), "sess-002")
	require.ErrorIs(t, err, session.ErrSessionNotFound)

	// Shadow keys gone.
	ok, _ := rdb.Exists(context.Background(), "them:ep:ep-test:shadow:sess-002")
	assert.False(t, ok, "EP shadow key should be deleted after End")
	ok, _ = rdb.Exists(context.Background(), "them:app:app-abc:shadow:sess-002")
	assert.False(t, ok, "app shadow key should be deleted after End")

	// Active session count decremented.
	assert.Equal(t, int32(0), store.ActiveSessions())
}

// Test 3: Get returns ErrSessionNotFound for missing session.
func TestStore_Get_NotFound(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	_, err := store.Get(context.Background(), "nonexistent")
	require.ErrorIs(t, err, session.ErrSessionNotFound)
}

// Test 4: CountEPSessions returns live count and prunes ghosts.
func TestStore_CountEPSessions_PrunesGhosts(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	// Register two sessions.
	for _, sid := range []string{"sess-a", "sess-b"} {
		require.NoError(t, store.Register(context.Background(), session.SessionInfo{
			SessionID:        sid,
			InstanceID:       "pod-1",
			UserID:           1,
			OrchestratorName: "default",
			EPSlug:           "ep-test",
			AppID:            "app-abc",
			ContextID:        "ctx-1",
			StartedAt:        time.Now().UTC().Format(time.RFC3339),
		}))
	}

	// Both sessions live.
	n, err := store.CountEPSessions(context.Background(), "ep-test")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Simulate sess-a's shadow key expiring (ghost session).
	rdb.expireShadow("them:ep:ep-test:shadow:sess-a")

	// CountEPSessions should prune sess-a and return 1.
	n, err = store.CountEPSessions(context.Background(), "ep-test")
	require.NoError(t, err)
	assert.Equal(t, 1, n, "ghost session sess-a should have been pruned")
}

// Test 5: WriteHeartbeat reports real session count (not hardcoded 0).
func TestStore_WriteHeartbeat_ReportsRealCount(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-heartbeat", noopLogger())

	// Register two sessions to bump the counter.
	for _, sid := range []string{"s1", "s2"} {
		require.NoError(t, store.Register(context.Background(), session.SessionInfo{
			SessionID:        sid,
			InstanceID:       "pod-heartbeat",
			UserID:           1,
			OrchestratorName: "default",
			StartedAt:        time.Now().UTC().Format(time.RFC3339),
		}))
	}

	require.NoError(t, store.WriteHeartbeat(context.Background()))

	// Read back pod hash and confirm sessions=2.
	fields, err := rdb.HGetAll(context.Background(), "them:pod:pod-heartbeat")
	require.NoError(t, err)
	assert.Equal(t, "2", fields["sessions"], "heartbeat must report real active session count")
}

// Test 6: SignalDisconnect publishes on control channel; SubscribeControl receives it.
func TestStore_SignalDisconnect_PubSub(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan struct{}, 1)
	go store.SubscribeControl(ctx, "sess-ctl", func() {
		received <- struct{}{}
	})

	// Wait for subscriber to register.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rdb.mu.Lock()
		n := len(rdb.subscribers["them:sess:control:sess-ctl"])
		rdb.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	require.NoError(t, store.SignalDisconnect(context.Background(), "sess-ctl"))

	select {
	case <-received:
		// pass
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect handler was not called within 2s")
	}
}

// Test 7: activeSessions counter is updated atomically by Register and End.
func TestStore_ActiveSessionsCounter_Atomic(t *testing.T) {
	rdb := newMockRedis()
	store := session.NewStore(rdb, "pod-1", noopLogger())

	assert.Equal(t, int32(0), store.ActiveSessions())

	for i := 0; i < 5; i++ {
		info := testInfo(fmt.Sprintf("sess-%d", i))
		info.EPSlug = ""
		info.AppID = ""
		require.NoError(t, store.Register(context.Background(), info))
	}
	assert.Equal(t, int32(5), store.ActiveSessions())

	require.NoError(t, store.End(context.Background(), "sess-0", "", ""))
	assert.Equal(t, int32(4), store.ActiveSessions())
}
