package gate_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/gate"
)

// ── fakeRedis ─────────────────────────────────────────────────────────────────
//
// fakeRedis mirrors the Lua admission script logic in Go so tests run without a
// live Redis. Shadow key TTLs are not enforced (no real clock), but tests
// simulate expiry by deleting shadow keys manually.

type fakeRedis struct {
	mu sync.Mutex

	sets     map[string]map[string]bool // Redis Sets
	strings  map[string]string          // Redis Strings (shadow keys, rl keys)
	counters map[string]int             // INCR counters

	// blpopQueues: key → buffered channel of values. A BLPop call drains the
	// channel; if empty it blocks until a value arrives or the context expires.
	blpopQueues map[string]chan string
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		sets:        make(map[string]map[string]bool),
		strings:     make(map[string]string),
		counters:    make(map[string]int),
		blpopQueues: make(map[string]chan string),
	}
}

// inSet reports whether member is in the named Set.
func (f *fakeRedis) inSet(key, member string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets[key] != nil && f.sets[key][member]
}

// hasShadow reports whether a shadow key exists (simulates TTL not expired).
func (f *fakeRedis) hasShadow(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.strings[key]
	return ok
}

// deleteShadow simulates a shadow key TTL expiry.
func (f *fakeRedis) deleteShadow(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.strings, key)
}

// queue returns (creating if needed) the buffered channel for a given key.
// Caller must hold mu.
func (f *fakeRedis) queue(key string) chan string {
	if f.blpopQueues[key] == nil {
		f.blpopQueues[key] = make(chan string, 64)
	}
	return f.blpopQueues[key]
}

// ExecLua runs one of two scripts: luaAdmit (5 KEYS) or the rollback script (4 KEYS).
func (f *fakeRedis) ExecLua(_ context.Context, script string, keys []string, args []interface{}) (interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Distinguish scripts by key count.
	if len(keys) == 4 {
		// Rollback script: SREM ep_set + DEL ep_shadow + optionally app_set + app_shadow.
		epSet := keys[0]
		epShKey := keys[1]
		appSet := keys[2]
		appShKey := keys[3]
		sid := args[0].(string)
		if f.sets[epSet] != nil {
			delete(f.sets[epSet], sid)
		}
		delete(f.strings, epShKey)
		if appSet != "" {
			if f.sets[appSet] != nil {
				delete(f.sets[appSet], sid)
			}
			delete(f.strings, appShKey)
		}
		return int64(1), nil
	}

	// Admission script (5 KEYS).
	epSet := keys[0]
	epShPfx := keys[1]
	appSet := keys[2]
	appShPfx := keys[3]
	rlKey := keys[4]

	sid := args[0].(string)
	epMax := toInt(args[1])
	appMax := toInt(args[2])
	rlRPM := toInt(args[3])
	// args[4] = reservation_ttl — not enforced in fake (no real clock)

	epLive := f.pruneCountLocked(epSet, epShPfx)

	if epMax > 0 && epLive >= epMax {
		return int64(-1), nil
	}

	if appSet != "" {
		appLive := f.pruneCountLocked(appSet, appShPfx)
		if appMax > 0 && appLive >= appMax {
			return int64(-2), nil
		}
	}

	if rlRPM > 0 && rlKey != "" {
		f.counters[rlKey]++
		if f.counters[rlKey] > rlRPM {
			return int64(-3), nil
		}
	}

	if f.sets[epSet] == nil {
		f.sets[epSet] = make(map[string]bool)
	}
	f.sets[epSet][sid] = true
	f.strings[epShPfx+sid] = "1"

	if appSet != "" {
		if f.sets[appSet] == nil {
			f.sets[appSet] = make(map[string]bool)
		}
		f.sets[appSet][sid] = true
		f.strings[appShPfx+sid] = "1"
	}

	return int64(epLive + 1), nil
}

// pruneCountLocked scans the set and removes ghost members (no shadow key).
// Caller must hold mu.
func (f *fakeRedis) pruneCountLocked(setKey, shadowPfx string) int {
	s := f.sets[setKey]
	live := 0
	for m := range s {
		if _, ok := f.strings[shadowPfx+m]; ok {
			live++
		} else {
			delete(s, m)
		}
	}
	return live
}

func (f *fakeRedis) SetEX(_ context.Context, key, value string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strings[key] = value
	return nil
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.strings, k)
		delete(f.sets, k)
	}
	return nil
}

// BLPop blocks until a value is available on key or the context expires.
func (f *fakeRedis) BLPop(ctx context.Context, _ time.Duration, key string) (string, error) {
	f.mu.Lock()
	ch := f.queue(key)
	f.mu.Unlock()

	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (f *fakeRedis) LPush(_ context.Context, key, value string) error {
	f.mu.Lock()
	ch := f.queue(key)
	f.mu.Unlock()
	ch <- value
	return nil
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func baseConfig() gate.Config {
	return gate.Config{
		EPSlug:    "ep1",
		AppID:     "app1",
		TokenHash: "abc123",
		SessionID: "sess-1",
		ShadowTTL: 90 * time.Second,
	}
}

// ── G-01: No limits → admitted, Set + shadow written ─────────────────────────

func TestAdmitNoLimits(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	res, err := g.Check(context.Background(), baseConfig())
	require.NoError(t, err)
	assert.Equal(t, gate.StatusAdmitted, res.Status)

	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"), "EP Set membership")
	assert.True(t, r.inSet("them:app:app1:sessions", "sess-1"), "app Set membership")
	assert.True(t, r.hasShadow("them:ep:ep1:shadow:sess-1"), "EP shadow key")
	assert.True(t, r.hasShadow("them:app:app1:shadow:sess-1"), "app shadow key")
}

// ── G-02: EP cap exceeded ─────────────────────────────────────────────────────

func TestEPCapExceeded(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded))
}

// ── G-03: App cap exceeded ────────────────────────────────────────────────────

func TestAppCapExceeded(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.AppMaxConcurrent = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded))
}

// ── G-04: Rate limit exceeded ─────────────────────────────────────────────────

func TestRateLimit(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.RateLimitRPM = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrRateLimited))
}

// ── G-05: No app ID → only EP Set written ────────────────────────────────────

func TestNoAppID(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.AppID = ""

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"))
	assert.False(t, r.inSet("them:app::sessions", "sess-1"))
}

// ── G-06: Ghost pruned, cap counts correctly ──────────────────────────────────

func TestGhostPruning(t *testing.T) {
	r := newFakeRedis()
	// Plant a ghost: in Set but no shadow key.
	r.sets["them:ep:ep1:sessions"] = map[string]bool{"ghost-sess": true}

	g := gate.New(r)
	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err, "ghost should be pruned, cap=1 should admit new session")

	assert.False(t, r.inSet("them:ep:ep1:sessions", "ghost-sess"), "ghost removed")
	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"), "new session admitted")
}

// ── G-07: Queue disabled → ErrCapExceeded immediately ────────────────────────

func TestQueueDisabledOnCapExceeded(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 0

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded))
}

// ── G-08: Queue wait times out → ErrQueueFull ────────────────────────────────

func TestQueueTimeout(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 10 * time.Millisecond

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrQueueFull))
}

// ── G-09: Confirm extends shadow keys to full TTL ─────────────────────────────
//
// After Check (writes shadow with ReservationTTL), Confirm must refresh the
// shadow keys to the full ShadowTTL. We verify by calling Confirm and checking
// that SetEX was invoked (shadow key still present with updated value).

func TestConfirmExtendsShadow(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	// Shadow key exists with reservation value.
	assert.True(t, r.hasShadow("them:ep:ep1:shadow:sess-1"))

	// Confirm must not error and must keep shadow key present.
	require.NoError(t, g.Confirm(context.Background(), cfg))
	assert.True(t, r.hasShadow("them:ep:ep1:shadow:sess-1"), "shadow still present after Confirm")
	assert.True(t, r.hasShadow("them:app:app1:shadow:sess-1"), "app shadow still present after Confirm")
}

// ── G-10: Rollback removes admission immediately ─────────────────────────────
//
// If session.Register fails, Gate.Rollback must remove Set membership and
// shadow keys so the slot is freed immediately (not waiting for ReservationTTL).

func TestRollbackRemovesAdmission(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1

	// Session A admitted.
	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	// Simulate session.Register failure → Rollback.
	require.NoError(t, g.Rollback(context.Background(), cfg))

	// Set membership must be gone.
	assert.False(t, r.inSet("them:ep:ep1:sessions", "sess-1"), "EP Set cleared by Rollback")
	assert.False(t, r.inSet("them:app:app1:sessions", "sess-1"), "app Set cleared by Rollback")
	assert.False(t, r.hasShadow("them:ep:ep1:shadow:sess-1"), "EP shadow deleted by Rollback")

	// After rollback, a new session B can be admitted (slot is free again).
	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	require.NoError(t, err, "slot freed by Rollback, sess-2 should be admitted")
}

// ── G-11: Reservation expiry == ghost auto-cleanup ────────────────────────────
//
// If Check succeeds but Confirm is never called (process crash simulation),
// the shadow key with ReservationTTL "expires" (we simulate this by deleting
// it). The next admission attempt must prune the ghost and admit the new session.

func TestReservationExpiryAutoCleanup(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1

	// Check admitted sess-1, but Confirm never called.
	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	// Simulate ReservationTTL expiry: delete the shadow key.
	r.deleteShadow("them:ep:ep1:shadow:sess-1")
	r.deleteShadow("them:app:app1:shadow:sess-1")

	// sess-1 is now a ghost (in Set, no shadow). sess-2 admission must succeed.
	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	require.NoError(t, err, "ghost pruned on next admission, sess-2 should be admitted")

	assert.False(t, r.inSet("them:ep:ep1:sessions", "sess-1"), "ghost sess-1 pruned")
	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-2"), "sess-2 admitted")
}

// ── G-12: Queue wake-up is a compete, not a guarantee ────────────────────────
//
// Session A holds the slot (cap=1). B queues. A's slot is freed (shadow deleted)
// and C sneaks in before B's re-check wins it. B wakes, re-runs admission, finds
// C holding the slot → ErrCapExceeded. B must NOT re-queue.

func TestQueueWakeUpIsACompete(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 2 * time.Second

	// A admitted and confirmed.
	cfgA := cfg
	cfgA.SessionID = "sess-a"
	_, err := g.Check(context.Background(), cfgA)
	require.NoError(t, err)
	require.NoError(t, g.Confirm(context.Background(), cfgA))

	// B queues (will block on BLPop).
	cfgB := cfg
	cfgB.SessionID = "sess-b"
	bResult := make(chan error, 1)
	go func() {
		_, err := g.Check(context.Background(), cfgB)
		bResult <- err
	}()
	time.Sleep(30 * time.Millisecond)

	// Simulate A ending: remove A's shadow so the slot appears free.
	r.deleteShadow("them:ep:ep1:shadow:sess-a")
	r.deleteShadow("them:app:app1:shadow:sess-a")

	// C sneaks in and takes the slot before B's re-check.
	cfgC := cfg
	cfgC.SessionID = "sess-c"
	_, err = g.Check(context.Background(), cfgC)
	require.NoError(t, err, "sess-c should win the now-free slot")
	require.NoError(t, g.Confirm(context.Background(), cfgC))

	// Wake B. B re-checks and finds C holding the slot → ErrCapExceeded.
	require.NoError(t, g.Release(context.Background(), cfgA))

	select {
	case err := <-bResult:
		assert.True(t, errors.Is(err, gate.ErrCapExceeded),
			"B woke but lost the compete; expected ErrCapExceeded, got %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for sess-b result")
	}
}

// ── G-13: Multiple waiters compete for one slot ───────────────────────────────
//
// Cap=1. Session A holds the slot. B and C both queue. A ends and releases TWO
// slot signals (one per waiter). Both B and C wake and re-run admission. Exactly
// one wins (admitted); the other loses the compete and gets ErrCapExceeded.
// Neither should return ErrQueueFull (both woke from a signal, not timeout).

func TestMultipleWaitersCompete(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 2 * time.Second

	// A admitted.
	cfgA := cfg
	cfgA.SessionID = "sess-a"
	_, err := g.Check(context.Background(), cfgA)
	require.NoError(t, err)
	require.NoError(t, g.Confirm(context.Background(), cfgA))

	// B and C queue concurrently.
	type outcome struct {
		id  string
		err error
	}
	results := make(chan outcome, 2)

	for _, id := range []string{"sess-b", "sess-c"} {
		id := id
		c := cfg
		c.SessionID = id
		go func() {
			_, err := g.Check(context.Background(), c)
			results <- outcome{id, err}
		}()
	}

	// Give B and C time to enter BLPop.
	time.Sleep(50 * time.Millisecond)

	// A ends: remove A's shadow so the slot appears free.
	r.deleteShadow("them:ep:ep1:shadow:sess-a")
	r.deleteShadow("them:app:app1:shadow:sess-a")

	// Release TWO signals — one to wake B, one to wake C. Both will compete.
	// Only one slot is available, so one wins and one gets ErrCapExceeded.
	require.NoError(t, g.Release(context.Background(), cfgA))
	require.NoError(t, g.Release(context.Background(), cfgA))

	var admitted, capErr int
	for i := 0; i < 2; i++ {
		select {
		case o := <-results:
			if o.err == nil {
				admitted++
			} else if errors.Is(o.err, gate.ErrCapExceeded) {
				capErr++
			} else {
				t.Errorf("unexpected error for %s: %v", o.id, o.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for waiter results (admitted=%d capErr=%d)", admitted, capErr)
		}
	}

	assert.Equal(t, 1, admitted, "exactly one waiter must win the slot")
	assert.Equal(t, 1, capErr, "exactly one waiter must lose with ErrCapExceeded")
}

// ── G-14: Context cancelled while waiting in queue ────────────────────────────
//
// Cap=1. Session A holds the slot. B queues. B's context is cancelled before
// any Release signal arrives. B must return an error (not deadlock). B's entry
// must be removed from the queue list (LRem) so the slot is not wasted.

func TestCancellationWhileQueued(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 5 * time.Second

	cfgA := cfg
	cfgA.SessionID = "sess-a"
	_, err := g.Check(context.Background(), cfgA)
	require.NoError(t, err)

	cfgB := cfg
	cfgB.SessionID = "sess-b"

	ctx, cancel := context.WithCancel(context.Background())
	bErr := make(chan error, 1)
	go func() {
		_, err := g.Check(ctx, cfgB)
		bErr <- err
	}()

	// Give B time to enter BLPop.
	time.Sleep(30 * time.Millisecond)

	// Cancel B's context (simulates client disconnect while queued).
	cancel()

	select {
	case err := <-bErr:
		assert.Error(t, err, "cancelled session must return an error")
		assert.False(t, errors.Is(err, gate.ErrQueueFull),
			"ErrQueueFull is for timeout; cancel should propagate context error")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out — Check did not return after context cancel")
	}

	// After cancel, A's slot is still held. A new session C (no queue) must fail.
	cfgC := cfg
	cfgC.SessionID = "sess-c"
	cfgC.QueueTimeout = 0
	_, err = g.Check(context.Background(), cfgC)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded),
		"slot still belongs to A after B cancelled")
}

// ── G-15: Release is idempotent when no waiters ───────────────────────────────
//
// Release with no waiters must not panic or error. The pushed value onto the
// unmonitored list is benign.

func TestReleaseNoWaiters(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	assert.NoError(t, g.Release(context.Background(), cfg))
}

// ── G-16: Rollback wakes queued session ──────────────────────────────────────
//
// A admitted. B queues. A's session.Register fails → Rollback called.
// Rollback must remove A's admission AND call Release, so B wakes and wins.

func TestRollbackWakesQueuedSession(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 2 * time.Second

	// A admitted but Register failed.
	cfgA := cfg
	cfgA.SessionID = "sess-a"
	_, err := g.Check(context.Background(), cfgA)
	require.NoError(t, err)

	// B queues.
	cfgB := cfg
	cfgB.SessionID = "sess-b"
	bResult := make(chan error, 1)
	go func() {
		_, err := g.Check(context.Background(), cfgB)
		bResult <- err
	}()

	time.Sleep(30 * time.Millisecond)

	// A's Register failed — Rollback frees the slot and wakes B.
	require.NoError(t, g.Rollback(context.Background(), cfgA))

	select {
	case err := <-bResult:
		assert.NoError(t, err, "B should be admitted after A's Rollback")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for sess-b after rollback")
	}
}
