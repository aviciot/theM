package appliveness

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake publisher ────────────────────────────────────────────────────────────

type fakePublisher struct {
	mu       sync.Mutex
	cacheKey string
	cacheVal string
	pubCh    string
	pubMsg   string
}

func (f *fakePublisher) setCache(_ context.Context, key, val string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cacheKey, f.cacheVal = key, val
	return nil
}

func (f *fakePublisher) publish(_ context.Context, channel, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubCh, f.pubMsg = channel, msg
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func testServerPort(rawURL string) int {
	u, _ := url.Parse(rawURL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func nopLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ── tests ─────────────────────────────────────────────────────────────────────

func TestProbeAll_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	results := probeAll([]string{"my-app"}, testServerPort(srv.URL))

	require.Contains(t, results, "my-app")
	assert.True(t, results["my-app"].Reachable)
	assert.NotNil(t, results["my-app"].LatencyMs)
}

func TestProbeAll_Unreachable(t *testing.T) {
	// Port 1 is never open.
	results := probeAll([]string{"dead-app"}, 1)

	require.Contains(t, results, "dead-app")
	assert.False(t, results["dead-app"].Reachable)
	assert.Nil(t, results["dead-app"].LatencyMs)
}

func TestPublishResults_CacheKeyAndChannel(t *testing.T) {
	pub := &fakePublisher{}
	results := map[string]Liveness{
		"ep-one": {Reachable: true, LatencyMs: ptrInt64(12)},
	}

	err := publishResults(context.Background(), pub, results)
	require.NoError(t, err)

	assert.Equal(t, cacheKey, pub.cacheKey)
	assert.Equal(t, pubChannel, pub.pubCh)
}

func TestPublishResults_PayloadShape(t *testing.T) {
	pub := &fakePublisher{}
	results := map[string]Liveness{
		"ep-one": {Reachable: true, LatencyMs: ptrInt64(12)},
	}

	require.NoError(t, publishResults(context.Background(), pub, results))

	// pub/sub message must be {type:"app_status", statuses:{...}}
	var event map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(pub.pubMsg), &event))

	var typ string
	require.NoError(t, json.Unmarshal(event["type"], &typ))
	assert.Equal(t, "app_status", typ)

	var statuses map[string]Liveness
	require.NoError(t, json.Unmarshal(event["statuses"], &statuses))
	assert.True(t, statuses["ep-one"].Reachable)
	assert.EqualValues(t, 12, *statuses["ep-one"].LatencyMs)

	// cache value must be just the statuses map (no envelope)
	var cached map[string]Liveness
	require.NoError(t, json.Unmarshal([]byte(pub.cacheVal), &cached))
	assert.True(t, cached["ep-one"].Reachable)
}

func ptrInt64(v int64) *int64 { return &v }
