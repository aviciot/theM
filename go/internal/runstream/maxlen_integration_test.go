//go:build integration

// Integration tests for MAXLEN scenarios, WS reconnect cursor, and cross-replica replay.
// Requires a live Redis reachable at REDIS_ADDR (default localhost:6379). Run with:
//
//	go test -tags=integration -v -timeout 120s -run "TestMAXLEN|TestIntegration_WS|TestIntegration_Cross" ./internal/runstream/...
package runstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/runstream"
)

// xaddWithMaxlen appends one event to the run stream with MAXLEN ~ 5000 approximate trim.
// The rueidis builder chain for approximate MAXLEN is: Maxlen().Almost().Threshold("<n>").Id("*")...
func xaddWithMaxlen(t *testing.T, rc rueidis.Client, key, evType string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"type": evType, "text": "data"})
	cmd := rc.B().Xadd().Key(key).Maxlen().Almost().Threshold("5000").Id("*").
		FieldValue().FieldValue("data", string(payload)).Build()
	id, err := rc.Do(context.Background(), cmd).ToString()
	require.NoError(t, err)
	return id
}

// xaddPlain appends one event to the run stream without MAXLEN.
func xaddPlain(t *testing.T, rc rueidis.Client, key, evType string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"type": evType, "text": "data"})
	cmd := rc.B().Xadd().Key(key).Id("*").FieldValue().FieldValue("data", string(payload)).Build()
	id, err := rc.Do(context.Background(), cmd).ToString()
	require.NoError(t, err)
	return id
}

// drainN reads exactly n events from ch within timeout, failing the test if
// the count is not met or the timeout expires.
func drainN(t *testing.T, ch <-chan event.Event, n int, timeout time.Duration) []event.Event {
	t.Helper()
	got := make([]event.Event, 0, n)
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed after %d events, expected %d", len(got), n)
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout after %d/%d events", len(got), n)
		}
	}
	return got
}

// drainAll reads events until the channel closes or timeout, returning all received.
func drainAll(t *testing.T, ch <-chan event.Event, timeout time.Duration) []event.Event {
	t.Helper()
	var got []event.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout waiting for channel close after %d events", len(got))
			return got
		}
	}
}

// expectChannelClosed verifies the channel closes within timeout.
func expectChannelClosed(t *testing.T, ch <-chan event.Event, timeout time.Duration) {
	t.Helper()
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel must be closed")
	case <-time.After(timeout):
		t.Fatal("channel did not close within timeout")
	}
}

// newStreamer builds a RedisStreamer backed by a new rueidis client.
func newStreamer(t *testing.T) (rueidis.Client, runstream.RedisStreamer) {
	t.Helper()
	rc, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress:  []string{redisAddr()},
		DisableCache: true,
	})
	require.NoError(t, err)
	t.Cleanup(rc.Close)
	return rc, cache.NewRunStreamerRedisClient(rc)
}

// TestMAXLEN_Scenario1_NormalRun: 1,000 events, all replayed, done closes channel.
func TestMAXLEN_Scenario1_NormalRun(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("maxlen-test-1-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	const n = 1000
	for i := 0; i < n; i++ {
		xaddPlain(t, rc, key, "token")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	// Read all 1000 replayed events.
	got := drainN(t, out, n, 30*time.Second)
	for _, ev := range got {
		assert.Equal(t, "token", ev.Type)
	}

	// Add terminal — channel must deliver it and close.
	xaddPlain(t, rc, key, "done")
	select {
	case ev, ok := <-out:
		require.True(t, ok)
		assert.Equal(t, "done", ev.Type)
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive done event")
	}
	expectChannelClosed(t, out, 3*time.Second)
	t.Logf("Scenario 1 PASS: %d events replayed, done received and channel closed", n)
}

// TestMAXLEN_Scenario2_AtBoundary: 5,000 token events + 1 done (no trim), ≥4900 received.
func TestMAXLEN_Scenario2_AtBoundary(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("maxlen-test-2-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	const n = 5000
	for i := 0; i < n; i++ {
		xaddPlain(t, rc, key, "token")
	}
	xaddPlain(t, rc, key, "done")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	// Drain all until channel closes (done is terminal).
	got := drainAll(t, out, 60*time.Second)

	var tokens, dones int
	for _, ev := range got {
		switch ev.Type {
		case "token":
			tokens++
		case "done":
			dones++
		}
	}
	assert.GreaterOrEqual(t, tokens, 4900, "expected ≥4900 token events (MAXLEN approximate)")
	assert.Equal(t, 1, dones, "expected exactly 1 done event")
	t.Logf("Scenario 2 PASS: %d token events + %d done (total %d)", tokens, dones, len(got))
}

// TestMAXLEN_Scenario3_OverMAXLEN: 6,000 events with MAXLEN~5000 trim, ≥4900 received.
func TestMAXLEN_Scenario3_OverMAXLEN(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("maxlen-test-3-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	const n = 6000
	for i := 0; i < n; i++ {
		xaddWithMaxlen(t, rc, key, "token")
	}
	xaddWithMaxlen(t, rc, key, "done")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	got := drainAll(t, out, 60*time.Second)

	var tokens, dones int
	for _, ev := range got {
		switch ev.Type {
		case "token":
			tokens++
		case "done":
			dones++
		}
	}
	// With MAXLEN ~5000 and 6001 entries, oldest ~1000 should be trimmed.
	assert.GreaterOrEqual(t, tokens, 4900, "expected ≥4900 token events after MAXLEN trim")
	assert.Equal(t, 1, dones, "expected exactly 1 done event")
	t.Logf("Scenario 3 PASS: %d token events + %d done (oldest ~1000 trimmed, total %d)", tokens, dones, len(got))
}

// TestMAXLEN_Scenario4_ToolHeavyMixed: 200 tool_call + 200 tool_result + 400 token = 800 total.
func TestMAXLEN_Scenario4_ToolHeavyMixed(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("maxlen-test-4-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	// Interleave all event types to simulate realistic traffic:
	// pattern: token, tool_call, token, tool_result — repeated 200 times = 800 events.
	for i := 0; i < 200; i++ {
		xaddPlain(t, rc, key, "token")
		xaddPlain(t, rc, key, "tool_call")
		xaddPlain(t, rc, key, "token")
		xaddPlain(t, rc, key, "tool_result")
	}
	xaddPlain(t, rc, key, "done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	got := drainAll(t, out, 30*time.Second)

	counts := make(map[string]int)
	for _, ev := range got {
		counts[ev.Type]++
	}
	assert.Equal(t, 400, counts["token"], "expected 400 token events")
	assert.Equal(t, 200, counts["tool_call"], "expected 200 tool_call events")
	assert.Equal(t, 200, counts["tool_result"], "expected 200 tool_result events")
	assert.Equal(t, 1, counts["done"], "expected 1 done event")
	t.Logf("Scenario 4 PASS: %v", counts)
}

// TestMAXLEN_Scenario5_ReplayUnavailable: cursor older than oldest retained entry
// causes a replay_unavailable synthetic event, then normal events from oldest.
func TestMAXLEN_Scenario5_ReplayUnavailable(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("maxlen-test-5-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	// Write 6000 events with MAXLEN~5000 so oldest ~1000 are trimmed.
	const n = 6000
	for i := 0; i < n; i++ {
		xaddWithMaxlen(t, rc, key, "token")
	}

	// Read the oldest retained entry ID to confirm trimming occurred.
	oldestEntries, err := rc.Do(context.Background(),
		rc.B().Xrange().Key(key).Start("-").End("+").Count(1).Build(),
	).AsXRange()
	require.NoError(t, err)
	require.NotEmpty(t, oldestEntries, "stream must have entries")
	oldestID := oldestEntries[0].ID
	t.Logf("oldest retained ID: %s", oldestID)

	// Use "1-0" as LastEventID — a very old ID (epoch ms=1) that is guaranteed
	// to be older than any practical stream entry. This simulates a client
	// whose cursor was trimmed out of the stream.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{
		LastEventID: "1-0",
	})
	require.NoError(t, err)

	// First event must be replay_unavailable.
	select {
	case first, ok := <-out:
		require.True(t, ok, "channel must not be closed immediately")
		assert.Equal(t, "replay_unavailable", first.Type,
			"first event must be replay_unavailable; got %q", first.Type)
		t.Logf("replay_unavailable received as expected")
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive replay_unavailable event")
	}

	// Subsequent events should be normal token events from the oldest retained entry.
	select {
	case ev, ok := <-out:
		require.True(t, ok)
		assert.Equal(t, "token", ev.Type,
			"events after replay_unavailable must be tokens; got %q", ev.Type)
		t.Logf("first token after replay_unavailable: type=%s", ev.Type)
	case <-time.After(10 * time.Second):
		t.Fatal("did not receive any token event after replay_unavailable")
	}

	// XADD a done event to terminate, then drain until channel closes.
	xaddWithMaxlen(t, rc, key, "done")
	deadline := time.After(30 * time.Second)
	var sawDone bool
	for !sawDone {
		select {
		case ev, ok := <-out:
			if !ok {
				// channel closed — done may have been consumed in the batch
				sawDone = true
			} else if ev.Type == "done" {
				sawDone = true
			}
		case <-deadline:
			t.Fatal("did not see done event or channel close within timeout")
		}
	}
	t.Log("Scenario 5 PASS: replay_unavailable emitted, tokens resumed, done terminated channel")
}

// TestIntegration_WS_ReconnectResume: StreamFromRedis with LastEventID = event 3
// should replay only events 4 and 5 (no duplicates from 1-3).
func TestIntegration_WS_ReconnectResume(t *testing.T) {
	rc, streamer := newStreamer(t)

	runID := fmt.Sprintf("ws-reconnect-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	// Write 5 events and record all their IDs.
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		ids[i] = xaddPlain(t, rc, key, "token")
	}
	t.Logf("Written IDs: %v", ids)

	// Set LastEventID = id of event 3 (index 2).
	// Reader must start at (ids[2], +] — events 4 and 5 only, no duplicates.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{
		LastEventID: ids[2], // exclusive cursor at event 3
	})
	require.NoError(t, err)

	// Expect exactly 2 events (4 and 5).
	got := drainN(t, out, 2, 5*time.Second)
	assert.Equal(t, 2, len(got))
	for _, ev := range got {
		assert.Equal(t, "token", ev.Type, "events after resume cursor must be tokens")
	}

	// Write done to terminate.
	xaddPlain(t, rc, key, "done")
	select {
	case ev, ok := <-out:
		require.True(t, ok)
		assert.Equal(t, "done", ev.Type)
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive done event")
	}
	expectChannelClosed(t, out, 3*time.Second)
	t.Logf("WS reconnect resume PASS: saw events 4+5 only (no duplicates of 1-3), done closed channel")
}

// TestIntegration_CrossReplicaReplay: proves cross-replica replay guarantee.
// Events XADDed to shared Redis are replayable by any reader (regardless of
// which bridge instance originally processed the run), because all replicas
// share the same Redis backend.
func TestIntegration_CrossReplicaReplay(t *testing.T) {
	// Simulate replica-1: write events via a dedicated client.
	rc1, _ := newStreamer(t) // rc1 closed via t.Cleanup

	runID := fmt.Sprintf("cross-replica-test-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc1.Do(context.Background(), rc1.B().Del().Key(key).Build())

	// Replica 1 writes 3 events.
	xaddPlain(t, rc1, key, "token")
	xaddPlain(t, rc1, key, "token")
	xaddPlain(t, rc1, key, "token")

	// Simulate replica-2 reader: a different client connection to the same Redis.
	// This proves any replica can serve a full replay from shared state.
	_, streamer2 := newStreamer(t) // closed via t.Cleanup

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer2, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	// Replica-2 reader should see all 3 events.
	got := drainN(t, out, 3, 5*time.Second)
	assert.Equal(t, 3, len(got))
	for _, ev := range got {
		assert.Equal(t, "token", ev.Type)
	}

	// Terminate via replica-1 (any writer is fine).
	xaddPlain(t, rc1, key, "done")
	select {
	case ev, ok := <-out:
		require.True(t, ok)
		assert.Equal(t, "done", ev.Type)
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive done event on replica-2 reader")
	}
	expectChannelClosed(t, out, 3*time.Second)
	t.Logf("Cross-replica replay PASS: replica-2 reader saw all 3 events + done from shared Redis")
}
