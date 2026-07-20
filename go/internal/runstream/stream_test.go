package runstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/runstream"
)

// fakeSubscriber is a test Subscriber that returns a pre-loaded message channel.
type fakeSubscriber struct {
	ch  chan string
	err error
}

func (f *fakeSubscriber) Subscribe(_ context.Context, _ string) (<-chan string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ch, nil
}

// TestStream_ForwardsMessages verifies that Stream forwards all messages from the
// underlying channel and the output channel closes when the source closes.
func TestStream_ForwardsMessages(t *testing.T) {
	msgCh := make(chan string, 4)
	msgCh <- `{"type":"token","content":"hello"}`
	msgCh <- `{"type":"done","run_id":"abc"}`
	close(msgCh)

	sub := &fakeSubscriber{ch: msgCh}

	ctx := context.Background()
	out, err := runstream.Stream(ctx, sub, "run-123")
	require.NoError(t, err)

	// Collect events with a timeout.
	var events []string
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				goto done
			}
			events = append(events, ev.Type)
		case <-timeout:
			t.Fatal("timed out waiting for events")
		}
	}
done:
	require.Len(t, events, 2, "expected exactly 2 events")
	assert.Equal(t, "token", events[0])
	assert.Equal(t, "done", events[1])
}

// TestStream_ContextCancel verifies that cancelling the context causes the
// output channel to close promptly without blocking.
func TestStream_ContextCancel(t *testing.T) {
	// Unbuffered channel — no messages will ever arrive.
	msgCh := make(chan string)
	sub := &fakeSubscriber{ch: msgCh}

	ctx, cancel := context.WithCancel(context.Background())

	out, err := runstream.Stream(ctx, sub, "run-cancel")
	require.NoError(t, err)

	// Cancel immediately.
	cancel()

	// The output channel should close within a short window.
	select {
	case _, ok := <-out:
		assert.False(t, ok, "channel should be closed after context cancel")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel did not close promptly after context cancel")
	}
}

// TestStreamContext_ForwardsMessages verifies that StreamContext subscribes to
// the :ctx channel (not :tokens) and forwards messages identically to Stream.
func TestStreamContext_ForwardsMessages(t *testing.T) {
	msgCh := make(chan string, 4)
	msgCh <- `{"type":"ready","run_id":"python-abc","context_id":"ctx-123"}`
	msgCh <- `{"type":"done","run_id":"python-abc"}`
	close(msgCh)

	sub := &fakeSubscriber{ch: msgCh}

	ctx := context.Background()
	out, err := runstream.StreamContext(ctx, sub, "ctx-123")
	require.NoError(t, err)

	var events []string
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				goto doneCtx
			}
			events = append(events, ev.Type)
		case <-timeout:
			t.Fatal("timed out waiting for events from StreamContext")
		}
	}
doneCtx:
	require.Len(t, events, 2, "expected exactly 2 events from context channel")
	assert.Equal(t, "ready", events[0])
	assert.Equal(t, "done", events[1])
}

// TestRunIDFromReady verifies the run_id extraction helper.
func TestRunIDFromReady(t *testing.T) {
	t.Run("ready event with run_id", func(t *testing.T) {
		ev := buildEvent(t, `{"type":"ready","run_id":"abc123","context_id":"ctx-1"}`)
		id, ok := runstream.RunIDFromReady(ev)
		assert.True(t, ok)
		assert.Equal(t, "abc123", id)
	})

	t.Run("non-ready event returns false", func(t *testing.T) {
		ev := buildEvent(t, `{"type":"token","content":"hello","run_id":"abc123"}`)
		id, ok := runstream.RunIDFromReady(ev)
		assert.False(t, ok)
		assert.Equal(t, "", id)
	})

	t.Run("ready event without run_id returns false", func(t *testing.T) {
		ev := buildEvent(t, `{"type":"ready","context_id":"ctx-1"}`)
		id, ok := runstream.RunIDFromReady(ev)
		assert.False(t, ok)
		assert.Equal(t, "", id)
	})

	t.Run("empty payload returns false", func(t *testing.T) {
		ev := buildEvent(t, `{"type":"ready"}`)
		id, ok := runstream.RunIDFromReady(ev)
		assert.False(t, ok)
		assert.Equal(t, "", id)
	})
}

// buildEvent is a test helper that parses a raw JSON message into an event.Event
// via Stream so the Type and Payload fields are set correctly.
func buildEvent(t *testing.T, raw string) event.Event {
	t.Helper()
	msgCh := make(chan string, 2)
	msgCh <- raw
	close(msgCh)
	sub := &fakeSubscriber{ch: msgCh}
	out, err := runstream.Stream(context.Background(), sub, "test")
	require.NoError(t, err)
	select {
	case ev, ok := <-out:
		require.True(t, ok, "expected an event from Stream")
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event in buildEvent")
		return event.Event{}
	}
}
