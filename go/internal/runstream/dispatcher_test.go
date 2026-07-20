package runstream_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/runstream"
)

// countingSubscriber is a Subscriber that records whether Subscribe was called.
// It returns an idle channel so the Pub/Sub Stream goroutine parks harmlessly.
type countingSubscriber struct {
	calls int32
}

func (c *countingSubscriber) Subscribe(_ context.Context, _ string) (<-chan string, error) {
	atomic.AddInt32(&c.calls, 1)
	return make(chan string), nil // idle
}

func (c *countingSubscriber) called() bool { return atomic.LoadInt32(&c.calls) > 0 }

// countingStreamer wraps a mockStreamer to record whether the Streams path ran.
type countingStreamer struct {
	xrangeCalls int32
}

func (c *countingStreamer) XRange(_ context.Context, _, _, _ string) ([]runstream.StreamEntry, error) {
	atomic.AddInt32(&c.xrangeCalls, 1)
	return nil, nil // replay empty → transition to live
}

func (c *countingStreamer) XRangeN(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	return nil, nil
}

func (c *countingStreamer) XRead(ctx context.Context, _ runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Millisecond):
	}
	return nil, nil
}

func (c *countingStreamer) called() bool { return atomic.LoadInt32(&c.xrangeCalls) > 0 }

// route runs the dispatcher and asserts which transport was selected. It gives
// the chosen backend a brief moment to record its first call, then cancels.
func route(t *testing.T, mode config.RunEventsMode, eventsTransport string, wantStreams bool) {
	t.Helper()
	sub := &countingSubscriber{}
	streamer := &countingStreamer{}
	d := runstream.NewDispatcher(mode, sub, streamer)

	ctx, cancel := context.WithCancel(context.Background())
	out, err := d.Stream(ctx, "run-x", eventsTransport, "")
	require.NoError(t, err)

	// Allow the backend goroutine to make its first Redis call.
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Drain so the goroutine can exit.
	go func() {
		for range out {
		}
	}()

	if wantStreams {
		assert.True(t, streamer.called(), "expected Streams transport")
		assert.False(t, sub.called(), "did not expect Pub/Sub transport")
	} else {
		assert.True(t, sub.called(), "expected Pub/Sub transport")
		assert.False(t, streamer.called(), "did not expect Streams transport")
	}
}

// 1. mode=pubsub → always Pub/Sub (eventsTransport ignored).
func TestDispatcher_PubsubMode_AlwaysPubsub(t *testing.T) {
	route(t, config.RunEventsModePublish, "streams", false)
	route(t, config.RunEventsModePublish, "pubsub", false)
}

// 2. mode=dual + eventsTransport=streams → Streams.
func TestDispatcher_DualMode_StreamsRun(t *testing.T) {
	route(t, config.RunEventsModeDual, "streams", true)
}

// 3. mode=dual + eventsTransport=pubsub → Pub/Sub (legacy run).
func TestDispatcher_DualMode_LegacyRun(t *testing.T) {
	route(t, config.RunEventsModeDual, "pubsub", false)
}

// 4. mode=streams + eventsTransport=streams → Streams.
func TestDispatcher_StreamsMode_StreamsRun(t *testing.T) {
	route(t, config.RunEventsModeStreams, "streams", true)
}

// 5. mode=streams + eventsTransport=pubsub → still Pub/Sub (legacy row).
func TestDispatcher_StreamsMode_LegacyRow(t *testing.T) {
	route(t, config.RunEventsModeStreams, "pubsub", false)
}

// 6. mode=pubsub + eventsTransport=streams → still Pub/Sub (mode precedence).
func TestDispatcher_PubsubMode_ModeTakesPrecedence(t *testing.T) {
	route(t, config.RunEventsModePublish, "streams", false)
}
