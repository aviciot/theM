// Package runstream subscribes to the Redis pub/sub channel
// `them:dash:run:{runID}:tokens` and forwards each message as an event.Event
// on a channel.
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
						"run_id", runID,
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
