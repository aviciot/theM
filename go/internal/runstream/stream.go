// Package runstream delivers orchestration run events from Redis Streams to
// WS/SSE clients.
//
// The Go Temporal worker always writes run events to the durable Redis stream
// them:dash:run:{runID}:stream (see publisher.go / StreamPublisher). The bridge
// reads them back via StreamFromRedis (see streamer.go), which supports full
// replay from a client-supplied last_event_id resume cursor.
//
// This file holds the shared wire-format decoder used by both the replay and
// live read paths: parseMessage turns a raw JSON stream payload into an
// event.Event.
package runstream

import (
	"encoding/json"

	"github.com/aviciot/them/internal/event"
)

// parseMessage deserialises a raw JSON string from a Redis Stream entry's
// "data" field into an event.Event. The "type" field is required; all other
// fields are preserved in Payload.
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
