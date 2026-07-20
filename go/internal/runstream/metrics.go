// Phase 11c-B metrics for the Redis Streams event transport.
//
// These counters/gauges are registered on the default Prometheus registry at
// package init and are incremented by the streamer as it replays and reads
// events. They complement the pub/sub reconnect metrics in stream.go.
package runstream

import "github.com/prometheus/client_golang/prometheus"

var (
	// xaddTotal counts stream entries appended, observed indirectly (the Go
	// bridge does not write to the stream — Python does via the Lua script).
	// Reserved for future XLen-based observation; kept for parity with the design.
	xaddTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_xadd_total",
		Help: "Stream entries appended (observed via XLen, not direct write)",
	})
	// xaddErrors counts Lua-script / XADD failures observed on the read path.
	xaddErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_xadd_errors_total",
		Help: "Lua script / XADD failures",
	})
	// replaySessions counts reconnect sessions that performed an XRANGE replay.
	replaySessions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_replay_sessions_total",
		Help: "Reconnect sessions that used XRANGE replay",
	})
	// replayEvents counts the total number of events replayed across all sessions.
	replayEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_replay_events_total",
		Help: "Total events replayed across all sessions",
	})
	// replayUnavailable counts sessions where last_event_id had been trimmed
	// out of the stream (MAXLEN) and a replay_unavailable event was emitted.
	replayUnavailable = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_replay_unavailable_total",
		Help: "Sessions where last_event_id was trimmed",
	})
	// modeGauge reports the active event-transport mode: 0=pubsub, 1=dual, 2=streams.
	modeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "them_runstream_mode",
		Help: "Event transport mode: 0=pubsub, 1=dual, 2=streams",
	})
)

func init() {
	prometheus.MustRegister(xaddTotal, xaddErrors, replaySessions, replayEvents, replayUnavailable, modeGauge)
}

// SetModeGauge records the active RUN_EVENTS_MODE on the them_runstream_mode
// gauge. Called once at startup in main.go.
func SetModeGauge(mode string) {
	switch mode {
	case "dual":
		modeGauge.Set(1)
	case "streams":
		modeGauge.Set(2)
	default:
		modeGauge.Set(0)
	}
}
