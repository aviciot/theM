// Package runstream subscribes to the Redis pub/sub channels for run events
// and forwards each message as an event.Event on a channel.
//
// Two channel patterns are used in the two-phase handshake:
//
//   - them:dash:run:{contextID}:ctx   — context channel; Python publishes the
//     "ready" event here so Go can learn the Python-generated run_id.
//   - them:dash:run:{runID}:tokens    — token stream; all subsequent events
//     (token, done, error, etc.) are published here using the Python run_id.
//
// IMPORTANT: Redis Pub/Sub provides at-most-once delivery. Events published
// before Subscribe is called, or during a brief network reconnect, are lost
// and will NOT be replayed. This limitation is explicit and intentional for
// Phase 10. Reconnect/replay is deferred to a later phase.
package runstream

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/aviciot/them/internal/event"
)

// Subscriber subscribes to a Redis pub/sub channel.
// Implemented by cache.RunStreamRedisClient.
type Subscriber interface {
	Subscribe(ctx context.Context, channel string) (<-chan string, error)
}

// Stream subscribes to them:dash:run:{runID}:tokens and forwards each
// JSON message payload as an event.Event on the returned channel.
// The channel is closed when ctx is cancelled or the subscription ends.
// Each message is expected to be a JSON object with at minimum a "type" field.
//
// At-most-once delivery: events published before Stream is called, or during
// a network reconnect gap, are lost and will not be replayed.
func Stream(ctx context.Context, sub Subscriber, runID string) (<-chan event.Event, error) {
	channel := "them:dash:run:" + runID + ":tokens"
	return subscribe(ctx, sub, channel, "run_id", runID)
}

// StreamContext subscribes to them:dash:run:{contextID}:ctx and forwards each
// JSON message payload as an event.Event on the returned channel.
//
// This is the first leg of the two-phase channel handshake: Go subscribes to
// the context channel before starting the Temporal workflow, waits for the
// "ready" event (which carries the Python-generated run_id), then subscribes
// to the tokens channel for subsequent events.
//
// At-most-once delivery: events published before StreamContext is called are lost.
func StreamContext(ctx context.Context, sub Subscriber, contextID string) (<-chan event.Event, error) {
	channel := "them:dash:run:" + contextID + ":ctx"
	return subscribe(ctx, sub, channel, "context_id", contextID)
}

// RunIDFromReady extracts the run_id field from a "ready" event payload.
// Returns ("", false) if the event type is not "ready" or run_id is absent/empty.
func RunIDFromReady(ev event.Event) (string, bool) {
	if ev.Type != "ready" {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		return "", false
	}
	raw, ok := m["run_id"]
	if !ok {
		return "", false
	}
	var runID string
	if err := json.Unmarshal(raw, &runID); err != nil || runID == "" {
		return "", false
	}
	return runID, true
}

// subscribe is the shared implementation for Stream and StreamContext.
// logKey/logVal are used only for warn-level log messages.
func subscribe(ctx context.Context, sub Subscriber, channel, logKey, logVal string) (<-chan event.Event, error) {
	msgCh, err := sub.Subscribe(ctx, channel)
	if err != nil {
		return nil, err
	}

	out := make(chan event.Event, 256)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				ev, parseErr := parseMessage(msg)
				if parseErr != nil {
					slog.Warn("runstream: skipping message — parse error",
						logKey, logVal,
						"error", parseErr,
					)
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// parseMessage deserialises a raw JSON string from Redis into an event.Event.
// The "type" field is required; all other fields are preserved in Payload.
func parseMessage(raw string) (event.Event, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return event.Event{}, err
	}

	typeRaw, ok := m["type"]
	if !ok {
		return event.Event{}, &missingTypeError{}
	}

	var evType string
	if err := json.Unmarshal(typeRaw, &evType); err != nil {
		return event.Event{}, &missingTypeError{}
	}

	// Re-serialise the full map as the payload.
	payload, err := json.Marshal(m)
	if err != nil {
		return event.Event{}, err
	}

	return event.Event{
		Type:    evType,
		Payload: payload,
	}, nil
}

type missingTypeError struct{}

func (e *missingTypeError) Error() string {
	return `runstream: message missing "type" field or type is not a string`
}
