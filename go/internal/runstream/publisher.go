// Package runstream — StreamPublisher writes run events to Redis Streams.
//
// This file implements the write side of the cross-process run-event delivery
// path introduced in R-2C Phase 3. The Go Temporal worker (cmd/worker) runs in
// a separate process from the Bridge (cmd/them). Events published by the worker's
// orchestrator to the in-process event bus must also be written to the run's Redis
// Stream so the Bridge can read them via StreamFromRedis.
//
// # Wire format
//
// Each Redis Stream entry has a single field: "data" → JSON string.
// The JSON must be a top-level object containing at minimum a "type" key (string).
// All other keys in the Payload are preserved.  Example:
//
//	XADD them:dash:run:<runID>:stream * data '{"type":"token","content":"hello"}'
//	XADD them:dash:run:<runID>:stream * data '{"type":"done","run_id":"<runID>"}'
//
// This matches the format produced by the Python bridge (which also XADDs a
// "data" field with a JSON string) and is compatible with decodeEntry / parseMessage
// in this package.
//
// # JSON construction
//
// event.Event carries:
//   - Type      — the event type string
//   - Payload   — JSON-encoded inner payload (e.g., {"content":"..."})
//   - RunID     — run identifier
//   - ContextID — conversation context
//
// PublishEvent merges {type, run_id, context_id} into the Payload object so the
// result is a single flat JSON object understood by parseMessage.
package runstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aviciot/them/internal/event"
)

// StreamPublisher writes run events to the run's Redis Stream.
// Implemented by *cache.RunStreamerWriterRedisClient; tests inject a fake.
type StreamPublisher interface {
	// XAdd appends one entry to the stream key with the given fields.
	// The key is the fully-formed stream key (them:dash:run:{runID}:stream).
	// fields must contain at least "data" → JSON string.
	XAdd(ctx context.Context, key string, fields map[string]interface{}) error
}

const (
	// streamWriteMaxLen is the approximate MAXLEN cap passed to XADD, bounding
	// per-run stream retention.
	streamWriteMaxLen = 5000
)

// PublishEvent appends an event.Event to the run's Redis Stream so the Bridge
// process can read it via StreamFromRedis.
//
// The "data" field value is a flat JSON object:
//
//	{"type":"<evType>","run_id":"<runID>","context_id":"<contextID>", ...payload fields...}
//
// If pub is nil the call is a no-op (safe to call unconditionally when the
// publisher is not configured).
func PublishEvent(ctx context.Context, pub StreamPublisher, log *slog.Logger, ev event.Event) {
	if pub == nil {
		return
	}

	// Build the merged JSON object.
	// Start with the existing Payload (which is already a JSON object like
	// {"content":"..."} or {"run_id":"..."}) so we preserve all inner fields.
	merged := make(map[string]json.RawMessage)
	if len(ev.Payload) > 0 {
		// Best-effort unmarshal; if it fails we still emit a partial event.
		_ = json.Unmarshal(ev.Payload, &merged)
	}

	// Stamp top-level fields required by parseMessage / the WS handler.
	// These overwrite any duplicate keys that were already in Payload.
	typeJSON, _ := json.Marshal(ev.Type)
	merged["type"] = typeJSON
	if ev.RunID != "" {
		runIDJSON, _ := json.Marshal(ev.RunID)
		merged["run_id"] = runIDJSON
	}
	if ev.ContextID != "" {
		ctxIDJSON, _ := json.Marshal(ev.ContextID)
		merged["context_id"] = ctxIDJSON
	}

	raw, err := json.Marshal(merged)
	if err != nil {
		if log != nil {
			log.Warn("runstream: failed to marshal event for Redis Stream",
				"run_id", ev.RunID, "type", ev.Type, "error", err)
		}
		return
	}

	key := fmt.Sprintf(streamKeyFmt, ev.RunID)
	fields := map[string]interface{}{streamPayloadField: string(raw)}
	if err := pub.XAdd(ctx, key, fields); err != nil {
		if log != nil {
			log.Warn("runstream: failed to publish event to Redis Stream",
				"key", key, "type", ev.Type, "error", err)
		}
	}
}
