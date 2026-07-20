package runstream_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/runstream"
)

// ── Fake subscriber ───────────────────────────────────────────────────────────

// sequentialSubscriber returns a different response per Subscribe call.
// Each call pops the next entry. When the list is empty, Subscribe returns a
// channel that never receives (simulates a stable but idle subscription).
type sequentialSubscriber struct {
	mu        sync.Mutex
	responses []subscribeResponse
}

type subscribeResponse struct {
	ch  chan string
	err error
}

func (s *sequentialSubscriber) Subscribe(_ context.Context, _ string) (<-chan string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) == 0 {
		return make(chan string), nil // idle — never sends
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	if r.err != nil {
		return nil, r.err
	}
	return r.ch, nil
}

func (s *sequentialSubscriber) addCh(ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, subscribeResponse{ch: ch})
}

func (s *sequentialSubscriber) addErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, subscribeResponse{err: err})
}

// collectEvents drains out until it closes or timeout, returning event types.
func collectEvents(t *testing.T, out <-chan event.Event, timeout time.Duration) []string {
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
			t.Fatalf("timed out collecting events; received so far: %v", types)
			return types
		}
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestStream_ForwardsMessages verifies basic message forwarding.
func TestStream_ForwardsMessages(t *testing.T) {
	msgCh := make(chan string, 4)
	msgCh <- `{"type":"token","content":"hello"}`
	msgCh <- `{"type":"done","run_id":"abc"}`
	// Don't close — terminal event closes out immediately.

	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	out, err := runstream.Stream(context.Background(), sub, "run-fwd")
	require.NoError(t, err)

	got := collectEvents(t, out, 2*time.Second)
	require.Len(t, got, 2)
	assert.Equal(t, "token", got[0])
	assert.Equal(t, "done", got[1])
}

// TestStream_TerminalDoneClosesImmediately verifies that a "done" event closes
// the output channel without waiting for the source channel to close.
func TestStream_TerminalDoneClosesImmediately(t *testing.T) {
	msgCh := make(chan string, 4)
	msgCh <- `{"type":"token","content":"hi"}`
	msgCh <- `{"type":"done","run_id":"run-1"}`
	// Intentionally NOT closing msgCh.

	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	out, err := runstream.Stream(context.Background(), sub, "run-done-imm")
	require.NoError(t, err)

	got := collectEvents(t, out, 2*time.Second)
	require.Len(t, got, 2)
	assert.Equal(t, "done", got[len(got)-1])
}

// TestStream_TerminalErrorClosesImmediately verifies the same for "error"
// events (e.g. max_iterations=0 → status=stopped).
func TestStream_TerminalErrorClosesImmediately(t *testing.T) {
	msgCh := make(chan string, 2)
	msgCh <- `{"type":"error","message":"Reached max iterations (0)"}`
	// Channel stays open.

	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	out, err := runstream.Stream(context.Background(), sub, "run-err-imm")
	require.NoError(t, err)

	got := collectEvents(t, out, 2*time.Second)
	require.Len(t, got, 1)
	assert.Equal(t, "error", got[0])
}

// TestStream_ContextCancel verifies that cancelling the context closes the
// output channel promptly.
func TestStream_ContextCancel(t *testing.T) {
	msgCh := make(chan string)
	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	ctx, cancel := context.WithCancel(context.Background())
	out, err := runstream.Stream(ctx, sub, "run-cancel")
	require.NoError(t, err)

	cancel()

	select {
	case _, ok := <-out:
		assert.False(t, ok, "channel should be closed after context cancel")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel did not close promptly after context cancel")
	}
}

// TestStream_ReconnectOnSourceClose verifies that a source channel closing
// without a terminal event triggers a reconnect and resumes delivery.
func TestStream_ReconnectOnSourceClose(t *testing.T) {
	// First subscription: token then drops (Redis hiccup).
	first := make(chan string, 2)
	first <- `{"type":"token","content":"before-drop"}`
	close(first)

	// Second subscription (after reconnect): token + done.
	second := make(chan string, 4)
	second <- `{"type":"token","content":"after-reconnect"}`
	second <- `{"type":"done","run_id":"run-reconnect"}`

	sub := &sequentialSubscriber{}
	sub.addCh(first)
	sub.addCh(second)

	out, err := runstream.Stream(context.Background(), sub, "run-reconnect")
	require.NoError(t, err)

	// Must accommodate at least one backoff (ReconnectBaseDelay = 100ms).
	got := collectEvents(t, out, 5*time.Second)

	assert.GreaterOrEqual(t, len(got), 2, "expected events from both subscriptions")
	assert.Equal(t, "done", got[len(got)-1], "last event must be terminal")
}

// TestStream_ContextCancelDuringBackoff verifies that ctx cancellation during
// a reconnect backoff exits cleanly without further reconnect attempts.
func TestStream_ContextCancelDuringBackoff(t *testing.T) {
	// First subscription drops immediately.
	first := make(chan string)
	close(first)

	// Second entry would error — should never be reached.
	sub := &sequentialSubscriber{}
	sub.addCh(first)
	sub.addErr(errors.New("should not be called"))

	ctx, cancel := context.WithCancel(context.Background())
	out, err := runstream.Stream(ctx, sub, "run-cancel-backoff")
	require.NoError(t, err)

	// Cancel before backoff fires.
	cancel()

	select {
	case _, ok := <-out:
		assert.False(t, ok, "expected channel closed after ctx cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after ctx cancel during backoff")
	}
}

// TestStream_ReconnectExhaustionEmitsOneError verifies that exhausting all
// reconnect attempts emits exactly one synthetic error event then closes.
func TestStream_ReconnectExhaustionEmitsOneError(t *testing.T) {
	// First subscription drops immediately.
	first := make(chan string)
	close(first)

	// All ReconnectMaxAttempts subsequent Subscribe calls fail.
	sub := &sequentialSubscriber{}
	sub.addCh(first)
	reconnErr := errors.New("redis unavailable")
	for i := 0; i < runstream.ReconnectMaxAttempts; i++ {
		sub.addErr(reconnErr)
	}

	// Full backoff sequence: 100+200+400+800+1600+3200ms ≈ 6.3s total.
	// We allow 15s to be safe without being flaky.
	out, err := runstream.Stream(context.Background(), sub, "run-exhaust")
	require.NoError(t, err)

	got := collectEvents(t, out, 15*time.Second)

	require.Len(t, got, 1, "exactly one synthetic error event expected")
	assert.Equal(t, "error", got[0])
}

// TestStream_NoDuplicateTerminalEvent verifies that Stream closes after the
// first terminal event and does not deliver a second one.
func TestStream_NoDuplicateTerminalEvent(t *testing.T) {
	msgCh := make(chan string, 4)
	msgCh <- `{"type":"done","run_id":"run-dup"}`
	msgCh <- `{"type":"done","run_id":"run-dup"}` // should never be read
	close(msgCh)

	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	out, err := runstream.Stream(context.Background(), sub, "run-dup")
	require.NoError(t, err)

	got := collectEvents(t, out, 2*time.Second)
	require.Len(t, got, 1, "only one terminal event should be delivered")
	assert.Equal(t, "done", got[0])
}

// TestStream_NoGoroutineLeak verifies the goroutine exits cleanly after
// the output channel closes (terminal event path).
func TestStream_NoGoroutineLeak(t *testing.T) {
	msgCh := make(chan string, 2)
	msgCh <- `{"type":"done","run_id":"run-leak"}`
	// Channel stays open.

	sub := &sequentialSubscriber{}
	sub.addCh(msgCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runstream.Stream(ctx, sub, "run-leak")
	require.NoError(t, err)

	// Ranging over out blocks until it closes. If goroutine leaks, this hangs.
	var types []string
	for ev := range out {
		types = append(types, ev.Type)
	}

	assert.Equal(t, []string{"done"}, types)
}

// TestStream_TerminalAfterReconnectNoFurtherAttempts verifies that once a
// terminal event is received after a reconnect, no further reconnect is
// attempted even if the new source channel then closes.
func TestStream_TerminalAfterReconnectNoFurtherAttempts(t *testing.T) {
	// First subscription drops without terminal.
	first := make(chan string)
	close(first)

	// Second subscription sends terminal then closes.
	second := make(chan string, 2)
	second <- `{"type":"done","run_id":"run-post-reconnect"}`
	close(second)

	// Third would fire if Stream incorrectly retries after terminal.
	third := make(chan string, 2)
	third <- `{"type":"error","message":"should not appear"}`

	sub := &sequentialSubscriber{}
	sub.addCh(first)
	sub.addCh(second)
	sub.addCh(third)

	out, err := runstream.Stream(context.Background(), sub, "run-post-reconnect")
	require.NoError(t, err)

	got := collectEvents(t, out, 5*time.Second)

	// Only "done" should appear; the "error" from third must not.
	assert.Equal(t, []string{"done"}, got)
}
