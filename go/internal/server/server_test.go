package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/server"
	"github.com/stretchr/testify/assert"
)

// okPinger satisfies health.Pinger and always returns nil.
type okPinger struct{}

func (o *okPinger) Ping(_ context.Context) error { return nil }

func newTestRouter() http.Handler {
	h := health.New("test-instance", &okPinger{}, &okPinger{})
	return server.NewRouter(h)
}

func TestRoutes_LiveEndpointRegistered(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoutes_ReadyEndpointRegistered(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoutes_MetricsEndpointRegistered(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// promhttp returns 200 with text/plain content
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
}

func TestRoutes_UnknownPath_Returns404(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
