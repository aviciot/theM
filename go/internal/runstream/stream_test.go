package runstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
