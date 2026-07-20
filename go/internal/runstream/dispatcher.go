// Dispatcher routes a run to the correct event transport (Pub/Sub or Redis
// Streams) based on the global RUN_EVENTS_MODE and the run's per-row
// events_transport value (Phase 11c-B).
//
// Transport selection is deterministic and never inferred from key existence or
// timing:
//
//	mode=pubsub                       → always Pub/Sub (events_transport ignored)
//	mode=dual, events_transport=streams  → Streams
//	mode=dual, events_transport=pubsub   → Pub/Sub (legacy run)
//	mode=streams, events_transport=streams → Streams
//	mode=streams, events_transport=pubsub  → Pub/Sub (legacy row, not forced)
//
// In non-pubsub modes, the run row's events_transport is authoritative: a run
// created before the cutover keeps 'pubsub' and is never forced onto Streams.
package runstream

import (
	"context"

	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/event"
)

// Dispatcher selects the transport for a run and returns an event channel.
type Dispatcher struct {
	mode config.RunEventsMode
	sub  Subscriber    // Pub/Sub subscriber (existing transport)
	rc   RedisStreamer // Redis Streams reader (Phase 11c)
}

// NewDispatcher constructs a Dispatcher. rc may be nil when mode is pubsub (the
// Streams path is never taken in that mode).
func NewDispatcher(mode config.RunEventsMode, sub Subscriber, rc RedisStreamer) *Dispatcher {
	return &Dispatcher{mode: mode, sub: sub, rc: rc}
}

// Stream returns an event channel for the run, choosing Pub/Sub or Streams per
// the rules documented on the package. eventsTransport is the run row's
// events_transport value; lastEventID is the client's resume cursor (only used
// on the Streams path).
func (d *Dispatcher) Stream(ctx context.Context, runID, eventsTransport, lastEventID string) (<-chan event.Event, error) {
	useStreams := false
	if d.mode != config.RunEventsModePublish {
		useStreams = eventsTransport == eventsTransportStreamsValue
	}
	if useStreams {
		return StreamFromRedis(ctx, d.rc, runID, StreamerOptions{LastEventID: lastEventID})
	}
	return Stream(ctx, d.sub, runID)
}

// eventsTransportStreamsValue is the events_transport column value that selects
// the Streams transport. Duplicated as a local const to avoid importing the
// runrecorder package (which would create an import cycle).
const eventsTransportStreamsValue = "streams"
