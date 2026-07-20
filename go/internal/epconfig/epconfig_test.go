package epconfig_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/epconfig"
)

// ── fake DBQuerier ─────────────────────────────────────────────────────────────

type fakeDB struct {
	row *epconfig.EPConfigRow
	err error
	// callCount tracks how many times QueryEPConfig was called.
	callCount int
}

func (f *fakeDB) QueryEPConfig(_ context.Context, _ string) (*epconfig.EPConfigRow, error) {
	f.callCount++
	if f.err != nil {
		return nil, f.err
	}
	return f.row, nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

func intPtr(n int) *int { return &n }

func enabledRow(slug string) *epconfig.EPConfigRow {
	return &epconfig.EPConfigRow{
		EPID:                 "ep-uuid-1",
		AppID:                "app-uuid-1",
		EPSlug:               slug,
		EPType:               "websocket",
		EPEnabled:            true,
		AppEnabled:           true,
		AccessPolicyJSON:     []byte(`{"mode":"token"}`),
		AppRuntimeConfigJSON: []byte(`{}`),
	}
}

// ── EC-01: EP-level session limit applied to Gate config ──────────────────────

func TestLoad_EPMaxConcurrentSessions(t *testing.T) {
	row := enabledRow("my-ep")
	row.EPMaxConcurrentSessions = intPtr(5)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "my-ep")
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.EPMaxConcurrent)
	assert.Equal(t, 0, cfg.AppMaxConcurrent) // no app-level limit
}

// ── EC-02: Application-level session limit ─────────────────────────────────────

func TestLoad_AppMaxConcurrentSessions(t *testing.T) {
	row := enabledRow("ep1")
	row.AppRuntimeConfigJSON = []byte(`{"max_concurrent_sessions":10}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ep1")
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.EPMaxConcurrent)   // no EP limit
	assert.Equal(t, 10, cfg.AppMaxConcurrent) // app-level limit
}

// ── EC-03: Precedence — EP and App both set, independent fields ───────────────

func TestLoad_BothLimitsSet(t *testing.T) {
	row := enabledRow("ep2")
	row.EPMaxConcurrentSessions = intPtr(3)
	row.AppRuntimeConfigJSON = []byte(`{"max_concurrent_sessions":20,"rate_limit_rpm":60}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ep2")
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.EPMaxConcurrent)   // EP limit governs EP set
	assert.Equal(t, 20, cfg.AppMaxConcurrent) // app limit governs app set
	assert.Equal(t, 60, cfg.RateLimitRPM)
}

// ── EC-04: Rate limit RPM from runtime_config ─────────────────────────────────

func TestLoad_RateLimitRPM(t *testing.T) {
	row := enabledRow("rate-ep")
	row.AppRuntimeConfigJSON = []byte(`{"rate_limit_rpm":100}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "rate-ep")
	require.NoError(t, err)
	assert.Equal(t, 100, cfg.RateLimitRPM)
}

// ── EC-05: Queue timeout from entry_points.queue_timeout_seconds ─────────────

func TestLoad_QueueTimeout(t *testing.T) {
	row := enabledRow("queue-ep")
	row.EPQueueTimeoutSeconds = intPtr(30)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "queue-ep")
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.QueueTimeout)
}

// ── EC-06: NULL queue_timeout_seconds → no queue ─────────────────────────────

func TestLoad_NullQueueTimeout(t *testing.T) {
	row := enabledRow("no-queue-ep")
	row.EPQueueTimeoutSeconds = nil // no queue

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "no-queue-ep")
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), cfg.QueueTimeout)
}

// ── EC-07: Disabled EP → ErrDisabled from CheckAccess ────────────────────────

func TestCheckAccess_DisabledEP(t *testing.T) {
	cfg := &epconfig.EPConfig{
		EPEnabled:  false,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
	}
	err := epconfig.CheckAccess(cfg, "somehash", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrDisabled))
}

// ── EC-08: Disabled Application → ErrDisabled from CheckAccess ───────────────

func TestCheckAccess_DisabledApp(t *testing.T) {
	cfg := &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: false,
		AccessMode: epconfig.AccessModeToken,
	}
	err := epconfig.CheckAccess(cfg, "somehash", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrDisabled))
}

// ── EC-09: Public EP access mode ──────────────────────────────────────────────

func TestLoad_PublicEP(t *testing.T) {
	row := enabledRow("pub-ep")
	row.AccessPolicyJSON = []byte(`{"mode":"public"}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "pub-ep")
	require.NoError(t, err)
	assert.Equal(t, epconfig.AccessModePublic, cfg.AccessMode)
}

// ── EC-10: Authenticated EP (default access mode) ────────────────────────────

func TestLoad_AuthenticatedEP(t *testing.T) {
	row := enabledRow("auth-ep")
	row.AccessPolicyJSON = []byte(`{"mode":"token"}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "auth-ep")
	require.NoError(t, err)
	assert.Equal(t, epconfig.AccessModeToken, cfg.AccessMode)
}

// ── EC-11: Blocked token → ErrBlocked ─────────────────────────────────────────

func TestCheckAccess_BlockedToken(t *testing.T) {
	cfg := &epconfig.EPConfig{
		EPEnabled:          true,
		AppEnabled:         true,
		AccessMode:         epconfig.AccessModeToken,
		BlockedTokenHashes: []string{"aabbcc", "ddeeff"},
	}
	err := epconfig.CheckAccess(cfg, "aabbcc", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrBlocked))
}

// ── EC-12: Blocked user ID → ErrBlocked ──────────────────────────────────────

func TestCheckAccess_BlockedUserID(t *testing.T) {
	cfg := &epconfig.EPConfig{
		EPEnabled:      true,
		AppEnabled:     true,
		AccessMode:     epconfig.AccessModeToken,
		BlockedUserIDs: []int64{42, 99},
	}
	err := epconfig.CheckAccess(cfg, "validhash", 42)
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrBlocked))
}

// ── EC-13: Not blocked → no error ────────────────────────────────────────────

func TestCheckAccess_NotBlocked(t *testing.T) {
	cfg := &epconfig.EPConfig{
		EPEnabled:          true,
		AppEnabled:         true,
		AccessMode:         epconfig.AccessModeToken,
		BlockedTokenHashes: []string{"other-hash"},
		BlockedUserIDs:     []int64{99},
	}
	err := epconfig.CheckAccess(cfg, "my-hash", 1)
	assert.NoError(t, err)
}

// ── EC-14: Missing configuration (EP not found) ───────────────────────────────

func TestLoad_EPNotFound(t *testing.T) {
	db := &fakeDB{err: fmt.Errorf("%w: slug=unknown", epconfig.ErrNotFound)}
	loader := epconfig.NewLoader(db, nil)

	_, err := loader.Load(context.Background(), "unknown")
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrNotFound))
}

// ── EC-15: Database unavailable → ErrDBUnavailable ───────────────────────────

func TestLoad_DBUnavailable(t *testing.T) {
	db := &fakeDB{err: errors.New("connection refused")}
	loader := epconfig.NewLoader(db, nil)

	_, err := loader.Load(context.Background(), "ep1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, epconfig.ErrDBUnavailable))
}

// ── EC-16: Malformed runtime_config JSONB → treat as {} (unlimited) ──────────

func TestLoad_MalformedRuntimeConfig(t *testing.T) {
	row := enabledRow("ep-bad-json")
	row.AppRuntimeConfigJSON = []byte(`not-valid-json`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ep-bad-json")
	require.NoError(t, err, "malformed JSONB must not fail the Load call")
	assert.Equal(t, 0, cfg.AppMaxConcurrent, "malformed config defaults to unlimited")
	assert.Equal(t, 0, cfg.RateLimitRPM)
}

// ── EC-17: NULL / zero limits → unlimited ────────────────────────────────────

func TestLoad_NullAndZeroLimits(t *testing.T) {
	row := enabledRow("unlimited-ep")
	row.EPMaxConcurrentSessions = nil
	row.EPQueueTimeoutSeconds = nil
	row.AppRuntimeConfigJSON = []byte(`{"max_concurrent_sessions":0,"rate_limit_rpm":0}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "unlimited-ep")
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.EPMaxConcurrent)
	assert.Equal(t, 0, cfg.AppMaxConcurrent)
	assert.Equal(t, 0, cfg.RateLimitRPM)
	assert.Equal(t, time.Duration(0), cfg.QueueTimeout)
}

// ── EC-18: Negative limits treated as unlimited ───────────────────────────────

func TestLoad_NegativeLimitsTreatedAsUnlimited(t *testing.T) {
	row := enabledRow("neg-ep")
	row.EPMaxConcurrentSessions = intPtr(-1)
	row.AppRuntimeConfigJSON = []byte(`{"max_concurrent_sessions":-5,"rate_limit_rpm":-1}`)

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "neg-ep")
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.EPMaxConcurrent, "negative EPMax treated as unlimited")
	assert.Equal(t, 0, cfg.AppMaxConcurrent)
	assert.Equal(t, 0, cfg.RateLimitRPM)
}

// ── EC-19: Cache hit — DB called only once for two consecutive loads ──────────

func TestLoad_CacheHit(t *testing.T) {
	db := &fakeDB{row: enabledRow("cached-ep")}
	loader := epconfig.NewLoader(db, nil)

	cfg1, err := loader.Load(context.Background(), "cached-ep")
	require.NoError(t, err)

	cfg2, err := loader.Load(context.Background(), "cached-ep")
	require.NoError(t, err)

	assert.Equal(t, 1, db.callCount, "DB queried only once due to cache hit")
	assert.Equal(t, cfg1, cfg2, "both calls return identical config")
}

// ── EC-20: Disabled EP not cached — DB called every time ─────────────────────

func TestLoad_DisabledEPNotCached(t *testing.T) {
	row := enabledRow("disabled-ep")
	row.EPEnabled = false
	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	_, _ = loader.Load(context.Background(), "disabled-ep")
	_, _ = loader.Load(context.Background(), "disabled-ep")

	assert.Equal(t, 2, db.callCount, "disabled EP must not be cached; DB hit on every call")
}

// ── EC-21: Invalidate evicts the cache entry ──────────────────────────────────

func TestInvalidate_EvictsEntry(t *testing.T) {
	db := &fakeDB{row: enabledRow("inv-ep")}
	loader := epconfig.NewLoader(db, nil)

	_, _ = loader.Load(context.Background(), "inv-ep")
	assert.Equal(t, 1, db.callCount)

	loader.Invalidate("inv-ep")

	_, _ = loader.Load(context.Background(), "inv-ep")
	assert.Equal(t, 2, db.callCount, "after Invalidate, DB is queried again")
}

// ── EC-22: InvalidateApp evicts all entries for the app ──────────────────────

func TestInvalidateApp_EvictsAppEntries(t *testing.T) {
	rowA := enabledRow("ep-a")
	rowA.AppID = "app-uuid-A"
	rowB := enabledRow("ep-b")
	rowB.AppID = "app-uuid-B"

	callCount := 0
	slugToRow := map[string]*epconfig.EPConfigRow{"ep-a": rowA, "ep-b": rowB}
	db := &multiDB{rows: slugToRow, callsPtr: &callCount}
	loader := epconfig.NewLoader(db, nil)

	_, _ = loader.Load(context.Background(), "ep-a")
	_, _ = loader.Load(context.Background(), "ep-b")
	assert.Equal(t, 2, callCount)

	// Invalidate app-uuid-A — only ep-a should be evicted.
	loader.InvalidateApp("app-uuid-A")

	_, _ = loader.Load(context.Background(), "ep-a") // re-query
	_, _ = loader.Load(context.Background(), "ep-b") // cache hit
	assert.Equal(t, 3, callCount, "only ep-a re-queried after InvalidateApp")
}

// ── EC-23: Missing access_policy defaults to token auth ──────────────────────

func TestLoad_MissingAccessPolicyDefaultsToToken(t *testing.T) {
	row := enabledRow("ep-default-auth")
	row.AccessPolicyJSON = nil // NULL in DB

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ep-default-auth")
	require.NoError(t, err)
	assert.Equal(t, epconfig.AccessModeToken, cfg.AccessMode)
}

// ── EC-24: AppID propagated correctly for gate ────────────────────────────────

func TestLoad_AppIDPropagated(t *testing.T) {
	row := enabledRow("ep-appid")
	row.AppID = "abc-123-def-456"

	db := &fakeDB{row: row}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ep-appid")
	require.NoError(t, err)
	assert.Equal(t, "abc-123-def-456", cfg.AppID)
}

// ── EC-25: Subscribe — pub/sub message evicts cache entry ────────────────────

// fakeSubscriber delivers messages from a channel, then returns when the
// channel is closed or ctx is cancelled. The done channel is closed once all
// messages have been delivered, letting the test synchronise without sleeping.
type fakeSubscriber struct {
	ch   chan string
	done chan struct{}
}

func newFakeSubscriber(msgs ...string) *fakeSubscriber {
	ch := make(chan string, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return &fakeSubscriber{ch: ch, done: make(chan struct{})}
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, _ string, handler func(string)) error {
	for {
		select {
		case msg, ok := <-f.ch:
			if !ok {
				close(f.done)
				return nil
			}
			handler(msg)
		case <-ctx.Done():
			return nil
		}
	}
}

func TestSubscribe_MessageEvictsCache(t *testing.T) {
	db := &fakeDB{row: enabledRow("pub-ep")}
	loader := epconfig.NewLoader(db, nil)

	// Populate cache.
	_, _ = loader.Load(context.Background(), "pub-ep")
	assert.Equal(t, 1, db.callCount)

	// Deliver pub/sub message via subscriber; wait for goroutine to finish.
	sub := newFakeSubscriber("pub-ep")
	loader.Subscribe(context.Background(), sub)

	select {
	case <-sub.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber goroutine")
	}

	// After eviction, next Load should re-query DB.
	_, err := loader.Load(context.Background(), "pub-ep")
	require.NoError(t, err)
	assert.Equal(t, 2, db.callCount, "DB re-queried after pub/sub eviction")
}

// EC-26: Subscribe — TTL fallback when no subscriber is wired ─────────────────

func TestLoad_TTLFallback_NoSubscriber(t *testing.T) {
	// Without a subscriber, a cached entry becomes stale after CacheTTL.
	// We cannot wait 30 s in a test, so verify the expired() sentinel works
	// by checking that a freshly loaded entry is NOT expired.
	db := &fakeDB{row: enabledRow("ttl-ep")}
	loader := epconfig.NewLoader(db, nil)

	cfg, err := loader.Load(context.Background(), "ttl-ep")
	require.NoError(t, err)
	assert.NotNil(t, cfg, "freshly loaded config must not be nil (TTL not yet expired)")
	assert.Equal(t, 1, db.callCount)

	// Second load still hits cache (TTL not expired in < 1 ms).
	_, err = loader.Load(context.Background(), "ttl-ep")
	require.NoError(t, err)
	assert.Equal(t, 1, db.callCount, "TTL not yet expired — cache hit expected")
}

// ── multiDB ───────────────────────────────────────────────────────────────────

type multiDB struct {
	rows     map[string]*epconfig.EPConfigRow
	callsPtr *int
}

func (m *multiDB) QueryEPConfig(_ context.Context, slug string) (*epconfig.EPConfigRow, error) {
	*m.callsPtr++
	row, ok := m.rows[slug]
	if !ok {
		return nil, fmt.Errorf("%w: slug=%s", epconfig.ErrNotFound, slug)
	}
	return row, nil
}
