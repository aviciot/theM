// Metrics for the Redis Streams run-event transport.
//
// These counters are registered on the default Prometheus registry at package
// init and are incremented by the streamer as it replays and reads events.
package runstream

import "github.com/prometheus/client_golang/prometheus"

var (
	// xaddErrors counts XADD / stream-read failures observed on the read path.
	xaddErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_runstream_xadd_errors_total",
		Help: "Stream read/XADD failures",
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
)

func init() {
	prometheus.MustRegister(xaddErrors, replaySessions, replayEvents, replayUnavailable)
}
