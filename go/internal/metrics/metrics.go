// Package metrics registers all application-level Prometheus metrics on the
// default registry. Import this package for its side effects (init) from main.go.
//
// Cardinality rules (enforced here):
//   - Never use request_id, session_id, run_id, or user_id as labels.
//   - Never use raw tenant_id — pending explicit architecture approval.
//   - Prefer low-cardinality labels: entry_point_type, result, reason,
//     route_group, tenant_tier.
//
// All metric names are prefixed "them_".
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ── Session metrics ────────────────────────────────────────────────────────────

var (
	// ActiveSessions is the current number of in-flight sessions on this replica.
	// Labels: ep_type (websocket|sse|a2a|voice|unknown).
	ActiveSessions = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "them_active_sessions",
		Help: "Number of active sessions currently held by this replica",
	}, []string{"ep_type"})

	// SessionsStarted counts session start events.
	// Labels: ep_type, result (admitted|rejected).
	SessionsStarted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "them_sessions_started_total",
		Help: "Total session start attempts",
	}, []string{"ep_type", "result"})

	// SessionsEnded counts session terminations.
	// Labels: ep_type, reason (client_disconnect|context_cancel|admin_signal|error).
	SessionsEnded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "them_sessions_ended_total",
		Help: "Total sessions ended",
	}, []string{"ep_type", "reason"})
)

// ── Gate metrics ───────────────────────────────────────────────────────────────

var (
	// GateAdmissions counts successful gate.Check calls.
	// Labels: ep_type.
	GateAdmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "them_gate_admissions_total",
		Help: "Total gate admission successes",
	}, []string{"ep_type"})

	// GateRejections counts rejected gate.Check calls.
	// Labels: ep_type, reason (cap_exceeded|rate_limited|queue_full).
	GateRejections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "them_gate_rejections_total",
		Help: "Total gate admission rejections",
	}, []string{"ep_type", "reason"})
)

// ── Event bus metrics ──────────────────────────────────────────────────────────

var (
	// EventBusDropped counts transient events dropped due to a full subscriber buffer.
	EventBusDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_event_bus_dropped_total",
		Help: "Transient events dropped by the in-process bus (slow consumer, buffer full)",
	})

	// EventBusCoalesced counts events that were intentionally coalesced/deduplicated
	// before delivery (reserved for future coalescing logic; currently 0).
	EventBusCoalesced = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_event_bus_coalesced_total",
		Help: "Events coalesced before delivery (future use)",
	})
)

// ── Connection metrics ─────────────────────────────────────────────────────────

var (
	// ActiveWSConnections is the current number of open WebSocket connections on this replica.
	ActiveWSConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "them_active_ws_connections",
		Help: "Number of open WebSocket connections on this replica",
	})

	// ActiveSSEConnections is the current number of open SSE connections on this replica.
	ActiveSSEConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "them_active_sse_connections",
		Help: "Number of open SSE connections on this replica",
	})
)

// ── Execution lifecycle metrics ────────────────────────────────────────────────

var (
	// RunStatusUpdateFailed counts cases where UpdateRunStatus (admitted→running)
	// failed for all retry attempts after a successful ExecuteWorkflow call.
	// A non-zero value means there are runs stuck as "admitted" in the DB even
	// though the Temporal workflow is actually executing. The reconciler must
	// be extended to also scan admitted rows to clean these up.
	RunStatusUpdateFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "them_run_status_update_failed_total",
		Help: "Successful ExecuteWorkflow calls where UpdateRunStatus(running) failed all retries — run stuck as admitted",
	})
)

// ── Shutdown / drain metrics ───────────────────────────────────────────────────

var (
	// GracefulDrainDuration records how long the shutdown drain took, in seconds.
	// Observed once per process shutdown.
	GracefulDrainDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "them_graceful_drain_duration_seconds",
		Help:    "Duration of the graceful shutdown drain window",
		Buckets: []float64{1, 5, 10, 15, 20, 25, 30, 45, 60},
	})
)

func init() {
	prometheus.MustRegister(
		// Session
		ActiveSessions,
		SessionsStarted,
		SessionsEnded,
		// Gate
		GateAdmissions,
		GateRejections,
		// Event bus
		EventBusDropped,
		EventBusCoalesced,
		// Connections
		ActiveWSConnections,
		ActiveSSEConnections,
		// Execution lifecycle
		RunStatusUpdateFailed,
		// Shutdown
		GracefulDrainDuration,
	)
}

// ObserveDrain records the actual drain duration. Call with the time.Time captured
// at drain start, immediately after httpServer.Shutdown returns.
func ObserveDrain(start time.Time) {
	GracefulDrainDuration.Observe(time.Since(start).Seconds())
}
