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
	// Subscribe registers channels to receive events for the given topic.
	// bufSize controls the main channel buffer. The second channel (termCh)
	// has capacity 1 and receives only terminal events ("done", "error") —
	// guaranteed delivery even when the main channel is full.
	// The returned function unsubscribes and closes both channels; it is idempotent.
	Subscribe(ctx context.Context, topic string, bufSize int) (<-chan Event, <-chan Event, func())

	// Publish sends an event to all subscribers of event.Topic and any
	// wildcard ("*") subscribers. Non-blocking per subscriber.
	// Terminal events ("done", "error") are routed to termCh (capacity 1)
	// and are never dropped by a full main channel.
	Publish(ctx context.Context, event Event)
}

// InMemoryBus is an in-process fan-out event bus.
type InMemoryBus struct {
	mu          sync.Mutex
	subscribers map[string][]chanEntry
}

type chanEntry struct {
	ch     chan Event
	termCh chan Event // capacity 1; receives only "done"/"error" events
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

// Subscribe registers buffered channels to receive events for topic.
// topic may be "*" to receive all events.
// Returns (evCh, termCh, unsub). Terminal events ("done"/"error") are routed
// to termCh (capacity 1) and are guaranteed to arrive even when evCh is full.
func (b *InMemoryBus) Subscribe(_ context.Context, topic string, bufSize int) (<-chan Event, <-chan Event, func()) {
	if bufSize <= 0 {
		bufSize = 256
	}
	ch := make(chan Event, bufSize)
	termCh := make(chan Event, 1)

	b.mu.Lock()
	b.subscribers[topic] = append(b.subscribers[topic], chanEntry{ch: ch, termCh: termCh})
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
					close(termCh)
					b.subscribers[topic] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			if len(b.subscribers[topic]) == 0 {
				delete(b.subscribers, topic)
			}
		})
	}
	return ch, termCh, unsub
}

// isTerminal reports whether an event type requires guaranteed delivery.
// Only "done" and "error" are terminal; at most one per run should arrive.
func isTerminal(typ string) bool {
	return typ == "done" || typ == "error"
}

// Publish sends event to every subscriber of event.Topic and all wildcard ("*") subscribers.
// Non-blocking per subscriber; drops transient events if the channel buffer is full.
// Terminal events ("done"/"error") are routed to termCh (capacity 1) and are never
// dropped by a full main channel. A second terminal event to the same subscriber is
// silently discarded (only one terminal event per run is expected).
//
// All sends are performed while holding b.mu so that a concurrent unsub cannot
// close a channel between the moment we read its pointer and the moment we send
// to it (which would cause a send-on-closed-channel panic detected as a data
// race by the Go race detector). The sends are non-blocking (select/default),
// so holding the lock during them does not risk deadlock.
func (b *InMemoryBus) Publish(_ context.Context, ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}

	terminal := isTerminal(ev.Type)

	b.mu.Lock()
	defer b.mu.Unlock()

	b.publishToEntries(b.subscribers[ev.Topic], ev, terminal)
	if ev.Topic != "*" {
		b.publishToEntries(b.subscribers["*"], ev, terminal)
	}
}

// publishToEntries sends ev to each entry in the list.
// Must be called with b.mu held.
//
// Terminal events ("done"/"error") are sent to both ch (best-effort) and
// termCh (guaranteed — never dropped if termCh has capacity). This ensures
// the handler always receives the terminal notification via termCh even when
// ch is full, while the normal drain path on ch still works when there is room.
func (b *InMemoryBus) publishToEntries(entries []chanEntry, ev Event, terminal bool) {
	for _, entry := range entries {
		if entry.closed {
			continue
		}
		// Always attempt delivery to the main channel (best-effort, drop if full).
		select {
		case entry.ch <- ev:
		default:
			// drop — slow consumer; terminal guarantee comes from termCh below
		}
		if terminal {
			// Route to the dedicated termCh; silently discard if already full
			// (a second terminal event to the same subscriber is unexpected).
			select {
			case entry.termCh <- ev:
			default:
			}
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
