package gate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/gate"
)

// ── Fake Redis ────────────────────────────────────────────────────────────────

// fakeRedis simulates Redis for gate tests. It executes the Lua scripts in Go,
// mirroring their logic so tests don't require a live Redis.
type fakeRedis struct {
	// sets maps key → set of members
	sets map[string]map[string]bool
	// strings maps key → value (for shadow keys and rl keys)
	strings map[string]string
	// counters maps key → int (for INCR)
	counters map[string]int
	// blpopQueue is used for queue tests
	blpopQueue chan string
	// bLPopErr can be set to simulate BLPOP timeout (return "" with no error)
	bLPopErr bool
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		sets:       make(map[string]map[string]bool),
		strings:    make(map[string]string),
		counters:   make(map[string]int),
		blpopQueue: make(chan string, 1),
	}
}

// sadds returns true if key is in the set.
func (f *fakeRedis) inSet(key, member string) bool {
	s, ok := f.sets[key]
	if !ok {
		return false
	}
	return s[member]
}

// ExecLua mirrors the luaAdmit script logic in Go.
func (f *fakeRedis) ExecLua(_ context.Context, _ string, keys []string, args []interface{}) (interface{}, error) {
	epSet := keys[0]
	epShadowPfx := keys[1]
	appSet := keys[2]
	appShadowPfx := keys[3]
	rlKey := keys[4]

	sid := args[0].(string)
	epMax := toInt(args[1])
	appMax := toInt(args[2])
	rateLimitRPM := toInt(args[3])
	shadowTTL := toInt(args[4])
	_ = shadowTTL // not used in fake — no actual expiry

	// Prune + count EP sessions
	epLive := f.pruneCount(epSet, epShadowPfx)

	// EP cap check
	if epMax > 0 && epLive >= epMax {
		return int64(-1), nil
	}

	// App cap check
	if appSet != "" {
		appLive := f.pruneCount(appSet, appShadowPfx)
		if appMax > 0 && appLive >= appMax {
			return int64(-2), nil
		}
	}

	// Rate limit check
	if rateLimitRPM > 0 && rlKey != "" {
		f.counters[rlKey]++
		if f.counters[rlKey] > rateLimitRPM {
			return int64(-3), nil
		}
	}

	// Admit: SADD EP + shadow
	if f.sets[epSet] == nil {
		f.sets[epSet] = make(map[string]bool)
	}
	f.sets[epSet][sid] = true
	f.strings[epShadowPfx+sid] = "1"

	// Admit: SADD app + shadow
	if appSet != "" {
		if f.sets[appSet] == nil {
			f.sets[appSet] = make(map[string]bool)
		}
		f.sets[appSet][sid] = true
		f.strings[appShadowPfx+sid] = "1"
	}

	return int64(epLive + 1), nil
}

// pruneCount scans the set and removes members whose shadow key is absent.
func (f *fakeRedis) pruneCount(setKey, shadowPfx string) int {
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

func (f *fakeRedis) BLPop(_ context.Context, _ time.Duration, _ ...string) (string, string, error) {
	if f.bLPopErr {
		return "", "", nil // simulate timeout
	}
	select {
	case v := <-f.blpopQueue:
		return "slot", v, nil
	default:
		return "", "", nil // timeout
	}
}

func (f *fakeRedis) LPush(_ context.Context, key, value string) error {
	// For queue tests: send a slot signal immediately so re-check can succeed.
	select {
	case f.blpopQueue <- value:
	default:
	}
	return nil
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.strings, k)
		delete(f.sets, k)
	}
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

// ── Tests ─────────────────────────────────────────────────────────────────────

func baseConfig() gate.Config {
	return gate.Config{
		EPSlug:    "ep1",
		AppID:     "app1",
		TokenHash: "abc123",
		SessionID: "sess-1",
		ShadowTTL: 90,
	}
}

// G-01: No limits → admit immediately, Set membership written.
func TestAdmitNoLimits(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	result, err := g.Check(context.Background(), baseConfig())
	require.NoError(t, err)
	assert.Equal(t, gate.StatusAdmitted, result.Status)

	// Set membership must be written.
	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"))
	assert.True(t, r.inSet("them:app:app1:sessions", "sess-1"))
	// Shadow keys must be written.
	assert.Equal(t, "1", r.strings["them:ep:ep1:shadow:sess-1"])
	assert.Equal(t, "1", r.strings["them:app:app1:shadow:sess-1"])
}

// G-02: EP cap = 1, one session admitted. Second rejected.
func TestEPCapExceeded(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1

	// First session — admitted.
	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	// Second session — cap full.
	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded), "expected ErrCapExceeded, got %v", err)
}

// G-03: App cap = 1, one session admitted. Second rejected with ErrCapExceeded.
func TestAppCapExceeded(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.AppMaxConcurrent = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrCapExceeded), "expected ErrCapExceeded, got %v", err)
}

// G-04: Rate limit = 1. First request admitted. Second rejected.
func TestRateLimit(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.RateLimitRPM = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrRateLimited), "expected ErrRateLimited, got %v", err)
}

// G-05: No app ID → only EP Set membership is written.
func TestNoAppID(t *testing.T) {
	r := newFakeRedis()
	g := gate.New(r)

	cfg := baseConfig()
	cfg.AppID = ""

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"))
	// No app Set writes expected.
	assert.False(t, r.inSet("them:app::sessions", "sess-1"))
}

// G-06: Ghost session in EP Set (shadow key absent) → pruned, cap check counts correctly.
func TestGhostPruning(t *testing.T) {
	r := newFakeRedis()
	// Manually plant a ghost: member in Set but no shadow key.
	r.sets["them:ep:ep1:sessions"] = map[string]bool{"ghost-sess": true}
	// No shadow key for ghost-sess → it will be pruned.

	g := gate.New(r)
	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1 // cap=1; ghost doesn't count, so new session fits

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err, "ghost should be pruned, admission should succeed")

	// Ghost removed, new session added.
	assert.False(t, r.inSet("them:ep:ep1:sessions", "ghost-sess"), "ghost should be removed")
	assert.True(t, r.inSet("them:ep:ep1:sessions", "sess-1"), "new session should be in Set")
}

// G-07: Queue timeout = 0 (no queue) → ErrCapExceeded when cap full.
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

// G-08: Queue timeout > 0 but BLPOP times out → ErrQueueFull.
func TestQueueTimeout(t *testing.T) {
	r := newFakeRedis()
	r.bLPopErr = true // simulate BLPOP returning empty (timeout)
	g := gate.New(r)

	cfg := baseConfig()
	cfg.EPMaxConcurrent = 1
	cfg.QueueTimeout = 1

	_, err := g.Check(context.Background(), cfg)
	require.NoError(t, err)

	cfg.SessionID = "sess-2"
	_, err = g.Check(context.Background(), cfg)
	assert.True(t, errors.Is(err, gate.ErrQueueFull), "expected ErrQueueFull, got %v", err)
}
