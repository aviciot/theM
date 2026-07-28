package metrics_test

import (
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/metrics"
)

// resetGauges zeros the gauges that tests increment, so tests are independent.
// This is safe because all metrics tests run sequentially in a single package.
func resetGauges() {
	metrics.ActiveWSConnections.Set(0)
	metrics.ActiveSSEConnections.Set(0)
	metrics.ActiveSessions.Reset()
}

// TestActiveWSConnections verifies that the WS connection gauge increments and
// decrements correctly and is exported on the default registry.
func TestActiveWSConnections(t *testing.T) {
	resetGauges()

	metrics.ActiveWSConnections.Inc()
	metrics.ActiveWSConnections.Inc()

	got := testutil.ToFloat64(metrics.ActiveWSConnections)
	assert.Equal(t, float64(2), got, "expected 2 active WS connections")

	metrics.ActiveWSConnections.Dec()
	got = testutil.ToFloat64(metrics.ActiveWSConnections)
	assert.Equal(t, float64(1), got, "expected 1 active WS connection after dec")

	resetGauges()
}

// TestActiveSSEConnections verifies that the SSE connection gauge increments and
// decrements correctly.
func TestActiveSSEConnections(t *testing.T) {
	resetGauges()

	metrics.ActiveSSEConnections.Inc()

	got := testutil.ToFloat64(metrics.ActiveSSEConnections)
	assert.Equal(t, float64(1), got)

	metrics.ActiveSSEConnections.Dec()
	got = testutil.ToFloat64(metrics.ActiveSSEConnections)
	assert.Equal(t, float64(0), got)
}

// TestActiveSessionsGauge verifies session gauge per ep_type label.
func TestActiveSessionsGauge(t *testing.T) {
	resetGauges()

	metrics.ActiveSessions.WithLabelValues("websocket").Inc()
	metrics.ActiveSessions.WithLabelValues("websocket").Inc()
	metrics.ActiveSessions.WithLabelValues("sse").Inc()

	ws := testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("websocket"))
	sse := testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("sse"))
	assert.Equal(t, float64(2), ws)
	assert.Equal(t, float64(1), sse)

	metrics.ActiveSessions.WithLabelValues("websocket").Dec()
	ws = testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("websocket"))
	assert.Equal(t, float64(1), ws)

	resetGauges()
}

// TestGateAdmissionsCounter verifies gate admission counter per ep_type.
func TestGateAdmissionsCounter(t *testing.T) {
	before := testutil.ToFloat64(metrics.GateAdmissions.WithLabelValues("websocket"))
	metrics.GateAdmissions.WithLabelValues("websocket").Inc()
	after := testutil.ToFloat64(metrics.GateAdmissions.WithLabelValues("websocket"))
	assert.Equal(t, before+1, after, "gate admission counter should increment by 1")
}

// TestGateRejectionsCounter verifies gate rejection counter per ep_type and reason.
func TestGateRejectionsCounter(t *testing.T) {
	before := testutil.ToFloat64(metrics.GateRejections.WithLabelValues("sse", "cap_exceeded"))
	metrics.GateRejections.WithLabelValues("sse", "cap_exceeded").Inc()
	after := testutil.ToFloat64(metrics.GateRejections.WithLabelValues("sse", "cap_exceeded"))
	assert.Equal(t, before+1, after)
}

// TestEventBusDroppedCounter verifies the event-bus dropped counter increments.
func TestEventBusDroppedCounter(t *testing.T) {
	before := testutil.ToFloat64(metrics.EventBusDropped)
	metrics.EventBusDropped.Add(5)
	after := testutil.ToFloat64(metrics.EventBusDropped)
	assert.Equal(t, before+5, after)
}

// TestSessionsStartedCounter verifies the session started counter per ep_type and result.
func TestSessionsStartedCounter(t *testing.T) {
	before := testutil.ToFloat64(metrics.SessionsStarted.WithLabelValues("websocket", "admitted"))
	metrics.SessionsStarted.WithLabelValues("websocket", "admitted").Inc()
	after := testutil.ToFloat64(metrics.SessionsStarted.WithLabelValues("websocket", "admitted"))
	assert.Equal(t, before+1, after)
}

// TestSessionsEndedCounter verifies the session ended counter per ep_type and reason.
func TestSessionsEndedCounter(t *testing.T) {
	before := testutil.ToFloat64(metrics.SessionsEnded.WithLabelValues("websocket", "client_disconnect"))
	metrics.SessionsEnded.WithLabelValues("websocket", "client_disconnect").Inc()
	after := testutil.ToFloat64(metrics.SessionsEnded.WithLabelValues("websocket", "client_disconnect"))
	assert.Equal(t, before+1, after)
}

// TestObserveDrain verifies that ObserveDrain records a sample in the histogram.
func TestObserveDrain(t *testing.T) {
	start := time.Now().Add(-2 * time.Second) // pretend drain took ~2s
	metrics.ObserveDrain(start)

	// Gather and verify at least one histogram observation exists.
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "them_graceful_drain_duration_seconds" {
			for _, m := range mf.GetMetric() {
				if m.GetHistogram().GetSampleCount() >= 1 {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "expected at least one drain duration observation")
}

// TestMetricNamesRegistered verifies all expected metric names are registered in
// the default Prometheus registry — guard against typos and missing init() calls.
// Uses Describe (not Gather) so GaugeVec/CounterVec with no observed label
// combinations are still visible.
func TestMetricNamesRegistered(t *testing.T) {
	expected := []string{
		"them_active_sessions",
		"them_sessions_started_total",
		"them_sessions_ended_total",
		"them_gate_admissions_total",
		"them_gate_rejections_total",
		"them_event_bus_dropped_total",
		"them_event_bus_coalesced_total",
		"them_active_ws_connections",
		"them_active_sse_connections",
		"them_graceful_drain_duration_seconds",
	}

	// Collect descriptors from the default registry via a describe channel.
	descCh := make(chan *prometheus.Desc, 1024)
	prometheus.DefaultRegisterer.(prometheus.Collector).Describe(descCh)
	close(descCh)

	registered := make(map[string]bool)
	for d := range descCh {
		// Desc.String() contains the fully-qualified name in the form:
		//   Desc{fqName: "them_foo", ...}
		// Extract the name by looking for fqName in the string.
		s := d.String()
		for _, name := range expected {
			if strings.Contains(s, `"`+name+`"`) {
				registered[name] = true
			}
		}
	}

	for _, name := range expected {
		assert.True(t, registered[name], "metric %q not found in registry", name)
	}
}

// TestHighCardinalityLabelsAbsent verifies that no metric uses high-cardinality
// label names that are prohibited by the cardinality rules.
func TestHighCardinalityLabelsAbsent(t *testing.T) {
	prohibited := []string{"session_id", "run_id", "request_id", "user_id", "tenant_id"}

	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, mf := range mfs {
		// Only check them_ metrics.
		if !strings.HasPrefix(mf.GetName(), "them_") {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				for _, bad := range prohibited {
					assert.NotEqual(t, bad, lp.GetName(),
						"metric %q uses prohibited high-cardinality label %q",
						mf.GetName(), bad)
				}
			}
		}
	}
}

// TestActiveSessionsGauge_LabelIsolation verifies that different ep_type labels
// are tracked independently (one tenant's gauge does not affect another EP type).
func TestActiveSessionsGauge_LabelIsolation(t *testing.T) {
	resetGauges()

	metrics.ActiveSessions.WithLabelValues("websocket").Inc()
	metrics.ActiveSessions.WithLabelValues("sse").Inc()
	metrics.ActiveSessions.WithLabelValues("sse").Inc()

	ws := testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("websocket"))
	sse := testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("sse"))
	a2a := testutil.ToFloat64(metrics.ActiveSessions.WithLabelValues("a2a"))

	assert.Equal(t, float64(1), ws, "websocket gauge should be independent")
	assert.Equal(t, float64(2), sse, "sse gauge should be independent")
	assert.Equal(t, float64(0), a2a, "a2a gauge should start at 0")

	resetGauges()
}

// ── Cardinality rule documentation ────────────────────────────────────────────
//
// The following label values ARE permitted (low-cardinality):
//   ep_type:  websocket, sse, a2a, voice, unknown
//   result:   admitted, rejected
//   reason:   cap_exceeded, rate_limited, queue_full, client_disconnect,
//             context_cancel, admin_signal, error
//
// The following labels are NEVER permitted:
//   session_id, run_id, request_id, user_id, tenant_id
//
// See TestHighCardinalityLabelsAbsent for the automated enforcement.

// helper to satisfy dto import (avoids unused-import error if testutil covers all needs).
var _ *dto.MetricFamily
