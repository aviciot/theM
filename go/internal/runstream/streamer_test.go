package runstream_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/runstream"
)

// ── Mock RedisStreamer ──────────────────────────────────────────────────────

// mockStreamer is a scripted RedisStreamer. XRange/XRangeN return canned entry
// batches in order (one batch per call). XRead returns canned batches, then
// blocks (returns empty) once exhausted so the live loop parks on context.
type mockStreamer struct {
	mu sync.Mutex

	// xrangeBatches: consecutive XRange results. Empty batch signals replay end.
	xrangeBatches [][]runstream.StreamEntry
	xrangeCalls   int

	// oldest is returned by XRangeN(- + COUNT 1) for trim detection.
	oldest []runstream.StreamEntry

	// xreadBatches: consecutive XRead results.
	xreadBatches [][]runstream.StreamEntry
	xreadCalls   int

	// recorded XRead cursors (the id half of each Streams pair).
	readCursors []string
	// recorded stream key for the last XRead.
	lastKey string
}

func (m *mockStreamer) XRange(_ context.Context, _ /*key*/, _ /*start*/, _ /*stop*/ string) ([]runstream.StreamEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.xrangeCalls < len(m.xrangeBatches) {
		b := m.xrangeBatches[m.xrangeCalls]
		m.xrangeCalls++
		return b, nil
	}
	return nil, nil // replay complete
}

func (m *mockStreamer) XRangeN(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.oldest, nil
}

func (m *mockStreamer) XRead(ctx context.Context, args runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	m.mu.Lock()
	// record cursor for continuity assertions
	if len(args.Streams) >= 2 {
		m.lastKey = args.Streams[0]
		m.readCursors = append(m.readCursors, args.Streams[1])
	}
	if m.xreadCalls < len(m.xreadBatches) {
		b := m.xreadBatches[m.xreadCalls]
		m.xreadCalls++
		key := args.Streams[0]
		m.mu.Unlock()
		return []runstream.StreamMessage{{Stream: key, Entries: b}}, nil
	}
	m.mu.Unlock()
	// Exhausted: emulate a BLOCK timeout with no data. Sleep briefly so the loop
	// does not busy-spin, and honour context cancellation.
	select {
	case <-ctx.Done():
		return nil, nil
	case <-time.After(10 * time.Millisecond):
		return nil, nil
	}
}

// entry builds a stream entry whose "data" field is a JSON event of the given type.
func entry(id, evType string) runstream.StreamEntry {
	return runstream.StreamEntry{
		ID:     id,
		Values: map[string]interface{}{"data": fmt.Sprintf(`{"type":%q,"text":"x"}`, evType)},
	}
}

// collect drains out until it closes or timeout elapses, returning the event
// types in order.
func collect(t *testing.T, out <-chan event.Event, timeout time.Duration) []string {
	t.Helper()
	var types []string
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return types
			}
			types = append(types, ev.Type)
		case <-deadline:
			t.Fatalf("timed out waiting for channel close; got %v", types)
			return types
		}
	}
}

// ── Tests ───────────────────────────────────────────────────────────────────

// 1. Replay-only: XRANGE returns 5 entries, then 0 → 5 events, then close.
func TestStreamFromRedis_ReplayOnly(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("1-0", "token"), entry("2-0", "token"), entry("3-0", "token"),
				entry("4-0", "token"), entry("5-0", "done")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run1", runstream.StreamerOptions{})
	require.NoError(t, err)

	got := collect(t, out, 2*time.Second)
	assert.Equal(t, []string{"token", "token", "token", "token", "done"}, got)
}

// 2. Live-only: empty XRANGE, then XREAD returns 3 → 3 events.
func TestStreamFromRedis_LiveOnly(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: nil, // immediate replay end
		xreadBatches: [][]runstream.StreamEntry{
			{entry("10-0", "token")},
			{entry("11-0", "token"), entry("12-0", "done")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run2", runstream.StreamerOptions{})
	require.NoError(t, err)

	got := collect(t, out, 2*time.Second)
	assert.Equal(t, []string{"token", "token", "done"}, got)
}

// 3. Replay-to-live: 3 replay + 2 live = 5, in order, no duplicates.
func TestStreamFromRedis_ReplayToLive(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("1-0", "token"), entry("2-0", "token"), entry("3-0", "token")},
		},
		xreadBatches: [][]runstream.StreamEntry{
			{entry("4-0", "token"), entry("5-0", "done")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run3", runstream.StreamerOptions{})
	require.NoError(t, err)

	got := collect(t, out, 2*time.Second)
	assert.Equal(t, []string{"token", "token", "token", "token", "done"}, got)
}

// 4. Continuous cursor: XREAD starts from the last XRANGE entry ID, not "$".
func TestStreamFromRedis_ContinuousCursor(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("100-0", "token"), entry("200-5", "token")},
		},
		xreadBatches: [][]runstream.StreamEntry{
			{entry("300-0", "done")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run4", runstream.StreamerOptions{})
	require.NoError(t, err)
	_ = collect(t, out, 2*time.Second)

	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotEmpty(t, m.readCursors)
	// The first live XREAD must resume from the last replayed entry ID "200-5",
	// never from "$" or "0-0".
	assert.Equal(t, "200-5", m.readCursors[0])
	assert.NotEqual(t, "$", m.readCursors[0])
}

// 5. replay_unavailable: LastEventID trimmed → synthetic event first, then resume.
func TestStreamFromRedis_ReplayUnavailable(t *testing.T) {
	m := &mockStreamer{
		oldest: []runstream.StreamEntry{entry("500-0", "token")},
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("500-0", "token"), entry("600-0", "done")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run5", runstream.StreamerOptions{LastEventID: "100-0"})
	require.NoError(t, err)

	got := collect(t, out, 2*time.Second)
	require.NotEmpty(t, got)
	assert.Equal(t, "replay_unavailable", got[0], "first event must be replay_unavailable")
	assert.Contains(t, got, "done")
}

// 6. Terminal event closes channel: "done" ends the stream.
func TestStreamFromRedis_TerminalClosesChannel(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("1-0", "token"), entry("2-0", "done"), entry("3-0", "token")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, m, "run6", runstream.StreamerOptions{})
	require.NoError(t, err)

	got := collect(t, out, 2*time.Second)
	// The entry after "done" must NOT be delivered.
	assert.Equal(t, []string{"token", "done"}, got)
}

// 7. All 5 terminal types close the channel.
func TestStreamFromRedis_AllTerminalTypes(t *testing.T) {
	for _, term := range []string{"done", "error", "canceled", "terminated", "timed_out"} {
		t.Run(term, func(t *testing.T) {
			m := &mockStreamer{
				xrangeBatches: [][]runstream.StreamEntry{
					{entry("1-0", "token"), entry("2-0", term), entry("3-0", "token")},
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			out, err := runstream.StreamFromRedis(ctx, m, "run7", runstream.StreamerOptions{})
			require.NoError(t, err)

			got := collect(t, out, 2*time.Second)
			assert.Equal(t, []string{"token", term}, got)
		})
	}
}

// 8. Context cancel stops the stream: the goroutine exits and channel closes.
func TestStreamFromRedis_ContextCancelStops(t *testing.T) {
	m := &mockStreamer{
		xrangeBatches: [][]runstream.StreamEntry{
			{entry("1-0", "token")},
		},
		// No terminal event; live loop parks on XRead block.
	}
	ctx, cancel := context.WithCancel(context.Background())

	out, err := runstream.StreamFromRedis(ctx, m, "run8", runstream.StreamerOptions{})
	require.NoError(t, err)

	// Read the one replayed token, then cancel.
	first := <-out
	assert.Equal(t, "token", first.Type)
	cancel()

	// Channel must close after cancellation.
	select {
	case _, ok := <-out:
		if ok {
			// drain until close
			for range out {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after context cancel")
	}
}

// 9. Multi-pod safety: two concurrent readers for the same run each get their
// own cursor (no shared state across StreamFromRedis calls).
func TestStreamFromRedis_MultiPodSafety(t *testing.T) {
	newMock := func() *mockStreamer {
		return &mockStreamer{
			xrangeBatches: [][]runstream.StreamEntry{
				{entry("1-0", "token"), entry("2-0", "done")},
			},
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m1, m2 := newMock(), newMock()
	out1, err1 := runstream.StreamFromRedis(ctx, m1, "shared-run", runstream.StreamerOptions{})
	out2, err2 := runstream.StreamFromRedis(ctx, m2, "shared-run", runstream.StreamerOptions{})
	require.NoError(t, err1)
	require.NoError(t, err2)

	got1 := collect(t, out1, 2*time.Second)
	got2 := collect(t, out2, 2*time.Second)
	assert.Equal(t, []string{"token", "done"}, got1)
	assert.Equal(t, []string{"token", "done"}, got2)
}
