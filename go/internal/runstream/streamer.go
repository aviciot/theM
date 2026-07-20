// Redis Streams reader for run events (Phase 11c-B).
//
// StreamFromRedis reads events from the durable Redis stream
// them:dash:run:{runID}:stream, replaying history from a client-supplied
// last_event_id and then blocking live. Unlike the Pub/Sub subscriber in
// stream.go, this transport is at-least-once with full replay: a client that
// disconnects and reconnects recovers every event published during the gap.
//
// # Continuous cursor (no gap at replay→live)
//
// The cursor starts at opts.LastEventID (or "0-0" for a fresh connection) and
// advances with every entry processed. XRANGE replays from the cursor to "+";
// when replay is exhausted the same cursor drives XREAD BLOCK. Because XREAD
// starts from the last processed entry ID — not "$" — no entry written between
// the end of replay and the start of the live read can be dropped.
//
// # replay_unavailable
//
// If LastEventID predates the oldest retained entry (MAXLEN trim), a synthetic
// {"type":"replay_unavailable","reason":"history_trimmed","run_id":"..."} event
// is emitted first, then replay resumes from the oldest available entry.
//
// # Terminal events
//
// Receiving any of the five terminal event types (done/error/canceled/
// terminated/timed_out) forwards that event and closes the output channel.
package runstream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/event"
)

const (
	// streamMaxLen is the approximate MAXLEN used when Python XADDs to the
	// stream. Kept here for documentation / test parity; the Go reader never
	// writes to the stream.
	streamMaxLen = 5000

	// streamKeyFmt builds the stream key for a run.
	streamKeyFmt = "them:dash:run:%s:stream"

	// liveBlockMillis is the XREAD BLOCK timeout. A bounded block lets the read
	// loop periodically observe context cancellation instead of blocking forever.
	liveBlockMillis = 5000

	// streamStart is the sentinel for "from the very beginning of the stream".
	streamStart = "0-0"

	// streamPayloadField is the field name Python writes the JSON payload under
	// (XADD ... 'data' <json>).
	streamPayloadField = "data"
)

// terminalEventTypes is the authoritative set of event types that end a run.
// Must match TERMINAL_EVENT_TYPES in app/temporal/activities.py.
var terminalEventTypes = map[string]struct{}{
	"done":       {},
	"error":      {},
	"canceled":   {},
	"terminated": {},
	"timed_out":  {},
}

func isStreamTerminal(evType string) bool {
	_, ok := terminalEventTypes[evType]
	return ok
}

// StreamEntry is one stream entry: its ID plus its field/value map.
type StreamEntry struct {
	ID     string
	Values map[string]interface{}
}

// StreamMessage is the per-stream result of an XREAD: the stream key plus the
// entries returned for it.
type StreamMessage struct {
	Stream  string
	Entries []StreamEntry
}

// XReadArgs are the arguments for a single XREAD call.
type XReadArgs struct {
	// Streams is an alternating list of key, id pairs, e.g.
	// ["them:dash:run:R:stream", "123-0"].
	Streams []string
	// Count caps entries returned per stream; 0 = unlimited.
	Count int64
	// Block is the block timeout in milliseconds; 0 = block indefinitely.
	Block int64
}

// RedisStreamer is the Redis surface required by StreamFromRedis. It is
// implemented by the rueidis-backed adapter in internal/cache and by mocks in
// tests. All methods must respect ctx cancellation.
type RedisStreamer interface {
	// XRange returns entries in [start, stop] inclusive.
	XRange(ctx context.Context, key, start, stop string) ([]StreamEntry, error)
	// XRangeN returns at most count entries in [start, stop] inclusive.
	XRangeN(ctx context.Context, key, start, stop string, count int64) ([]StreamEntry, error)
	// XRead blocks (per args.Block) for entries after the given cursor(s).
	XRead(ctx context.Context, args XReadArgs) ([]StreamMessage, error)
}

// StreamerOptions configures replay and live reads.
type StreamerOptions struct {
	// LastEventID is the client's resume cursor. "" or "0-0" means start from
	// the beginning (full replay of retained history).
	LastEventID string
}

// StreamFromRedis reads events from the run's Redis stream, replaying from
// opts.LastEventID and then blocking live, forwarding each as an event.Event.
// The returned channel is closed when ctx is cancelled, a terminal event is
// received, or an unrecoverable Redis error occurs.
//
// The function returns immediately after starting the reader goroutine; errors
// that occur only during reading are surfaced as a synthetic error event on the
// channel, mirroring the Pub/Sub Stream contract.
func StreamFromRedis(ctx context.Context, rc RedisStreamer, runID string, opts StreamerOptions) (<-chan event.Event, error) {
	key := fmt.Sprintf(streamKeyFmt, runID)
	out := make(chan event.Event, 256)

	go func() {
		defer close(out)

		// cursor is the exclusive lower bound for the next read. It starts at the
		// client-supplied last_event_id (or 0-0) and advances continuously.
		cursor := opts.LastEventID
		if cursor == "" {
			cursor = streamStart
		}

		// ── Trim detection ────────────────────────────────────────────────────
		// If the client is resuming from a real cursor (not the beginning) and
		// that cursor predates the oldest retained entry, history was trimmed.
		// Emit replay_unavailable and resume from the oldest available entry.
		resuming := cursor != streamStart
		if resuming {
			oldest, err := rc.XRangeN(ctx, key, "-", "+", 1)
			if err != nil {
				emitError(ctx, out, runID, "stream read failed")
				return
			}
			if len(oldest) > 0 && compareStreamIDs(oldest[0].ID, cursor) > 0 {
				// oldest entry is newer than the client's cursor → gap.
				replayUnavailable.Inc()
				if !emit(ctx, out, replayUnavailableEvent(runID)) {
					return
				}
				// Resume replay from the oldest available entry, inclusive. Set the
				// cursor to the exclusive predecessor of oldest so the XRANGE below
				// includes it.
				cursor = exclusivePredecessor(oldest[0].ID)
			}
		}

		// ── Replay loop (XRANGE) ──────────────────────────────────────────────
		// Read from (cursor, +]. rueidis/Redis XRANGE start is inclusive, so we
		// use the exclusive form "(<cursor>" to avoid re-emitting the cursor entry.
		replayed := false
		for {
			if ctx.Err() != nil {
				return
			}
			start := "(" + cursor
			if cursor == streamStart {
				// From the very beginning: "-" is inclusive of the first entry.
				start = "-"
			}
			entries, err := rc.XRange(ctx, key, start, "+")
			if err != nil {
				emitError(ctx, out, runID, "stream read failed")
				return
			}
			if len(entries) == 0 {
				break // replay complete → transition to live
			}
			if !replayed {
				replayed = true
				replaySessions.Inc()
			}
			for _, e := range entries {
				ev, ok := decodeEntry(e)
				if !ok {
					cursor = e.ID
					continue
				}
				replayEvents.Inc()
				if !emit(ctx, out, ev) {
					return
				}
				cursor = e.ID
				if isStreamTerminal(ev.Type) {
					return
				}
			}
		}

		// ── Live loop (XREAD BLOCK from the continuous cursor) ────────────────
		for {
			if ctx.Err() != nil {
				return
			}
			msgs, err := rc.XRead(ctx, XReadArgs{
				Streams: []string{key, cursor},
				Count:   0,
				Block:   liveBlockMillis,
			})
			if err != nil {
				// A block timeout with no data is reported by the adapter as an
				// empty result (nil error). A real error is treated as fatal only
				// if the context is still live; on ctx cancellation just exit.
				if ctx.Err() != nil {
					return
				}
				emitError(ctx, out, runID, "stream read failed")
				return
			}
			for _, m := range msgs {
				for _, e := range m.Entries {
					ev, ok := decodeEntry(e)
					if !ok {
						cursor = e.ID
						continue
					}
					if !emit(ctx, out, ev) {
						return
					}
					cursor = e.ID
					if isStreamTerminal(ev.Type) {
						return
					}
				}
			}
		}
	}()

	return out, nil
}

// decodeEntry extracts the JSON payload from a stream entry's "data" field and
// parses it into an event.Event. Returns ok=false for malformed entries (which
// the caller skips while still advancing the cursor).
func decodeEntry(e StreamEntry) (event.Event, bool) {
	raw, ok := e.Values[streamPayloadField]
	if !ok {
		return event.Event{}, false
	}
	s, ok := raw.(string)
	if !ok {
		// Some clients may return []byte.
		if b, isBytes := raw.([]byte); isBytes {
			s = string(b)
		} else {
			return event.Event{}, false
		}
	}
	ev, err := parseMessage(s)
	if err != nil {
		return event.Event{}, false
	}
	return ev, true
}

// emit sends ev on out, honouring ctx cancellation. Returns false if ctx was
// cancelled before the send completed (caller should stop).
func emit(ctx context.Context, out chan<- event.Event, ev event.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitError forwards a synthetic terminal error event, matching the Pub/Sub
// Stream contract. Increments the XADD-error counter for observability.
func emitError(ctx context.Context, out chan<- event.Event, runID, message string) {
	xaddErrors.Inc()
	msg := fmt.Sprintf(`{"type":"error","message":%q,"run_id":%q}`, message, runID)
	_ = emit(ctx, out, event.Event{
		Type:    "error",
		Payload: json.RawMessage(msg),
	})
}

// replayUnavailableEvent builds the synthetic replay_unavailable event emitted
// when the client's last_event_id has been trimmed out of the stream.
func replayUnavailableEvent(runID string) event.Event {
	msg := fmt.Sprintf(`{"type":"replay_unavailable","reason":"history_trimmed","run_id":%q}`, runID)
	return event.Event{
		Type:    "replay_unavailable",
		Payload: json.RawMessage(msg),
	}
}
