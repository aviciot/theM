// Package event provides an in-process fan-out event bus. Producers publish
// events identified by a string topic; consumers subscribe to a topic and
// receive all events published after they subscribe. The bus is safe for
// concurrent use.
//
// Design: each topic maintains a list of subscriber channels. Publish sends
// the event to every subscriber channel non-blocking (drops if the channel
// buffer is full) to avoid slow subscribers blocking producers.
// A wildcard subscriber on topic "*" receives all events.
package event

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Event is a generic event published on the bus.
type Event struct {
	// Topic is the bus topic (e.g., a context_id for conversation events).
	Topic string
	// Type is the event kind: "token", "tool_call", "tool_result", "done", "error".
	Type string
	// RunID is the run this event belongs to.
	RunID string
	// ContextID is the conversation context.
	ContextID string
	// Payload is the event-specific data.
	Payload json.RawMessage
	// Timestamp is when the event was published.
	Timestamp time.Time
}

// Bus is the in-process fan-out event bus interface.
type Bus interface {
	// Subscribe registers a channel to receive events for the given topic.
	// bufSize controls the channel buffer. The returned function unsubscribes
	// and closes the channel; it is idempotent.
	Subscribe(ctx context.Context, topic string, bufSize int) (<-chan Event, func())

	// Publish sends an event to all subscribers of event.Topic and any
	// wildcard ("*") subscribers. Non-blocking per subscriber.
	Publish(ctx context.Context, event Event)
}

// InMemoryBus is an in-process fan-out event bus.
type InMemoryBus struct {
	mu          sync.Mutex
	subscribers map[string][]chanEntry
}

type chanEntry struct {
	ch     chan Event
	closed bool
}

// NewBus returns a new InMemoryBus.
func NewBus() *InMemoryBus {
	return &InMemoryBus{
		subscribers: make(map[string][]chanEntry),
	}
}

// New is an alias for NewBus.
func New() *InMemoryBus { return NewBus() }

// Subscribe registers a buffered channel to receive events for topic.
// topic may be "*" to receive all events.
func (b *InMemoryBus) Subscribe(_ context.Context, topic string, bufSize int) (<-chan Event, func()) {
	if bufSize <= 0 {
		bufSize = 256
	}
	ch := make(chan Event, bufSize)

	b.mu.Lock()
	b.subscribers[topic] = append(b.subscribers[topic], chanEntry{ch: ch})
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			subs := b.subscribers[topic]
			for i, s := range subs {
				if s.ch == ch && !s.closed {
					b.subscribers[topic][i].closed = true
					close(ch)
					b.subscribers[topic] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			if len(b.subscribers[topic]) == 0 {
				delete(b.subscribers, topic)
			}
		})
	}
	return ch, unsub
}

// Publish sends event to every subscriber of event.Topic and all wildcard ("*") subscribers.
// Non-blocking per subscriber; drops if the channel buffer is full.
func (b *InMemoryBus) Publish(_ context.Context, ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}

	b.mu.Lock()
	// Collect target channels: topic subscribers + wildcard subscribers.
	var targets []chan Event
	for _, entry := range b.subscribers[ev.Topic] {
		if !entry.closed {
			targets = append(targets, entry.ch)
		}
	}
	if ev.Topic != "*" {
		for _, entry := range b.subscribers["*"] {
			if !entry.closed {
				targets = append(targets, entry.ch)
			}
		}
	}
	b.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
			// drop — slow consumer
		}
	}
}

// SimplePublish is a convenience method that publishes an event using the
// Topic+Type+Payload pattern used by the orchestrator layer.
// Payload should be JSON-marshalled before calling.
func (b *InMemoryBus) SimplePublish(topic, eventType string, payload any) {
	var raw json.RawMessage
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	b.Publish(context.Background(), Event{
		Topic:     topic,
		Type:      eventType,
		Payload:   raw,
		Timestamp: time.Now(),
	})
}
