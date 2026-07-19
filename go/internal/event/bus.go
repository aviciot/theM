// Package event provides an in-process fan-out event bus for domain events.
// Events are published by topic and delivered to all matching subscribers on
// buffered channels. If a subscriber's buffer is full the event is dropped for
// that subscriber — the bus never blocks on a slow consumer.
// The wildcard topic "*" receives every published event regardless of topic.
// Bus is safe for concurrent use.
package event

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Event is a domain event with a typed topic.
type Event struct {
	Topic     string          // e.g. "run.token", "run.tool_call", "run.completed"
	RunID     string
	ContextID string
	Payload   json.RawMessage // arbitrary JSON payload
	Timestamp time.Time
}

// Bus is an in-process fan-out event bus.
// Subscribers receive events on a buffered channel.
// If a subscriber's channel is full (slow consumer) the event is dropped for
// that subscriber — the bus never blocks on a slow consumer.
// Topic "*" receives all events regardless of their Topic field.
type Bus interface {
	// Publish sends e to all subscribers whose topic matches e.Topic and to
	// all wildcard ("*") subscribers. The call returns as soon as all channel
	// sends have been attempted (non-blocking). Publish is safe to call
	// concurrently from multiple goroutines.
	Publish(ctx context.Context, e Event)

	// Subscribe registers a new subscriber for the given topic. bufSize sets
	// the capacity of the returned channel. The returned func() is an
	// unsubscribe function: calling it removes the subscription and closes
	// the channel. Calling it more than once is safe (idempotent).
	Subscribe(ctx context.Context, topic string, bufSize int) (<-chan Event, func())
}

// sub is one subscriber entry held by the bus.
type sub struct {
	ch     chan Event
	closed bool
}

// busBus is the concrete Bus implementation.
type busBus struct {
	mu   sync.RWMutex
	subs map[string][]*sub
}

// NewBus creates a new in-process fan-out bus.
func NewBus() Bus {
	return &busBus{
		subs: make(map[string][]*sub),
	}
}

// Publish delivers e to every subscriber whose topic matches e.Topic, and to
// every wildcard ("*") subscriber. Channels that are full are skipped silently
// (drop semantics). Publish holds a read lock so multiple goroutines can
// publish concurrently.
func (b *busBus) Publish(_ context.Context, e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Collect target subscriber lists: exact-topic + wildcard.
	var targets []*sub
	if list, ok := b.subs[e.Topic]; ok {
		targets = append(targets, list...)
	}
	// Wildcard subscribers receive everything; avoid double-delivery when the
	// published topic is itself "*".
	if e.Topic != "*" {
		if list, ok := b.subs["*"]; ok {
			targets = append(targets, list...)
		}
	}

	for _, s := range targets {
		// Non-blocking send — drop event for slow consumers.
		select {
		case s.ch <- e:
		default:
		}
	}
}

// Subscribe creates a buffered channel with capacity bufSize and registers it
// for topic. The returned unsubscribe function removes the subscription and
// closes the channel.
func (b *busBus) Subscribe(_ context.Context, topic string, bufSize int) (<-chan Event, func()) {
	ch := make(chan Event, bufSize)
	s := &sub{ch: ch}

	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], s)
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()

			list := b.subs[topic]
			filtered := list[:0]
			for _, candidate := range list {
				if candidate != s {
					filtered = append(filtered, candidate)
				}
			}
			b.subs[topic] = filtered

			if !s.closed {
				s.closed = true
				close(s.ch)
			}
		})
	}

	return ch, unsub
}
