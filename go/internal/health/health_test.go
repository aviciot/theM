package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okPinger always returns nil from Ping.
type okPinger struct{}

func (o *okPinger) Ping(_ context.Context) error { return nil }

// failPinger always returns an error from Ping.
type failPinger struct{ msg string }

func (f *failPinger) Ping(_ context.Context) error { return errors.New(f.msg) }

func TestLive_AlwaysReturns200(t *testing.T) {
	h := health.New("test-instance", &okPinger{}, &okPinger{})

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	h.Live(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "test-instance", body["instance"])
}

func TestReady_BothHealthy_Returns200(t *testing.T) {
	h := health.New("test-instance", &okPinger{}, &okPinger{})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "ok", body.Checks["postgres"])
	assert.Equal(t, "ok", body.Checks["redis"])
}

func TestReady_DBUnreachable_Returns503(t *testing.T) {
	h := health.New("test-instance", &failPinger{msg: "connection refused"}, &okPinger{})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "degraded", body.Status)
	assert.Contains(t, body.Checks["postgres"], "error:")
	assert.Equal(t, "ok", body.Checks["redis"])
}

func TestReady_RedisUnreachable_Returns503(t *testing.T) {
	h := health.New("test-instance", &okPinger{}, &failPinger{msg: "dial timeout"})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "degraded", body.Status)
	assert.Equal(t, "ok", body.Checks["postgres"])
	assert.Contains(t, body.Checks["redis"], "error:")
}

func TestReady_BothUnreachable_Returns503(t *testing.T) {
	h := health.New("test-instance", &failPinger{msg: "db down"}, &failPinger{msg: "redis down"})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "degraded", body.Status)
	assert.Contains(t, body.Checks["postgres"], "error:")
	assert.Contains(t, body.Checks["redis"], "error:")
}
