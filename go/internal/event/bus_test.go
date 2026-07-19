package event

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeEvent builds an Event for the given topic with a simple JSON payload.
func makeEvent(topic, runID string) Event {
	payload, _ := json.Marshal(map[string]string{"key": "value"})
	return Event{
		Topic:     topic,
		RunID:     runID,
		ContextID: "ctx-1",
		Payload:   json.RawMessage(payload),
		Timestamp: time.Now(),
	}
}

// TestPublish_specificTopic verifies that a subscriber on topic A receives
// events published to topic A.
func TestPublish_specificTopic(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	ch, unsub := b.Subscribe(ctx, "run.token", 10)
	defer unsub()

	e := makeEvent("run.token", "run-1")
	b.Publish(ctx, e)

	select {
	case got := <-ch:
		assert.Equal(t, e.Topic, got.Topic)
		assert.Equal(t, e.RunID, got.RunID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected event not received")
	}
}

// TestPublish_wrongTopic verifies that a subscriber on topic B does NOT receive
// events published to topic A.
func TestPublish_wrongTopic(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	ch, unsub := b.Subscribe(ctx, "run.completed", 10)
	defer unsub()

	b.Publish(ctx, makeEvent("run.token", "run-1"))

	select {
	case got := <-ch:
		t.Fatalf("unexpected event received: %+v", got)
	case <-time.After(50 * time.Millisecond):
		// correct — no delivery expected
	}
}

// TestWildcard verifies that a subscriber on topic "*" receives all events.
func TestWildcard(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	ch, unsub := b.Subscribe(ctx, "*", 10)
	defer unsub()

	b.Publish(ctx, makeEvent("run.token", "run-1"))
	b.Publish(ctx, makeEvent("run.completed", "run-2"))

	received := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case got := <-ch:
			received = append(received, got.Topic)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}
	assert.ElementsMatch(t, []string{"run.token", "run.completed"}, received)
}

// TestSlowConsumer verifies that a full subscriber channel causes the event to
// be dropped without blocking the publisher.
func TestSlowConsumer(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	// Buffer size 1 — will fill up after the first event.
	ch, unsub := b.Subscribe(ctx, "run.token", 1)
	defer unsub()

	done := make(chan struct{})
	go func() {
		// Publish 10 events rapidly; only the first should land in the channel.
		for i := 0; i < 10; i++ {
			b.Publish(ctx, makeEvent("run.token", "run-slow"))
		}
		close(done)
	}()

	select {
	case <-done:
		// Publish goroutine finished without blocking — correct.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on slow consumer")
	}

	// Channel should have exactly 1 event (the first one that fit).
	require.Len(t, ch, 1)
}

// TestUnsubscribe verifies that after unsubscribing the channel is closed and
// no further events are delivered.
func TestUnsubscribe(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	ch, unsub := b.Subscribe(ctx, "run.token", 10)

	// Drain once to confirm subscribe works.
	b.Publish(ctx, makeEvent("run.token", "run-1"))
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first event not received")
	}

	// Unsubscribe — channel must be closed.
	unsub()

	// Calling unsub twice must not panic (idempotent).
	unsub()

	// Channel should be closed (range over it should exit immediately).
	timeout := time.After(100 * time.Millisecond)
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed")
	case <-timeout:
		t.Fatal("channel not closed after unsubscribe")
	}

	// Publishing after unsubscribe must not deliver or panic.
	b.Publish(ctx, makeEvent("run.token", "run-2"))
}

// TestConcurrentPublish verifies there are no data races when many goroutines
// publish simultaneously. Run with go test -race to catch races.
func TestConcurrentPublish(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	ch, unsub := b.Subscribe(ctx, "run.token", 1000)
	defer unsub()

	const publishers = 50
	const eventsEach = 20

	ready := make(chan struct{})
	done := make(chan struct{}, publishers)

	for i := 0; i < publishers; i++ {
		go func() {
			<-ready
			for j := 0; j < eventsEach; j++ {
				b.Publish(ctx, makeEvent("run.token", "run-concurrent"))
			}
			done <- struct{}{}
		}()
	}

	close(ready) // release all goroutines simultaneously

	for i := 0; i < publishers; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent publish timed out")
		}
	}

	// Drain remaining events — just verify no panic or deadlock.
	drain := time.After(50 * time.Millisecond)
loop:
	for {
		select {
		case <-ch:
		case <-drain:
			break loop
		}
	}
}
