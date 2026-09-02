package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// statsDB returns minimal scan data so GetSecurityScanStats can complete.
// QueryRow returns 7 zeros (totals) then 2 zeros (latency).
// Query returns empty rows for trend, app breakdown, and recent jobs.
type statsDB struct {
	queryRowCall int
}

func (d *statsDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return &emptyRows{}, nil
}

func (d *statsDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	d.queryRowCall++
	switch d.queryRowCall {
	case 1:
		// totals: total, scanned, clean, infected, error, pending, disabled
		return &int64x7Row{}
	default:
		// latency: avg, p95
		return &float64x2Row{}
	}
}

func (d *statsDB) Exec(_ context.Context, _ string, _ ...any) error          { return nil }
func (d *statsDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

type int64x7Row struct{}

func (r *int64x7Row) Scan(dest ...any) error {
	for _, d := range dest {
		if p, ok := d.(*int64); ok {
			*p = 0
		}
	}
	return nil
}

type float64x2Row struct{}

func (r *float64x2Row) Scan(dest ...any) error {
	for _, d := range dest {
		if p, ok := d.(*float64); ok {
			*p = 0
		}
	}
	return nil
}

type emptyRows struct{}

func (r *emptyRows) Next() bool          { return false }
func (r *emptyRows) Scan(_ ...any) error { return nil }
func (r *emptyRows) Close() error        { return nil }

// TestServicesStats_GetStats_OK verifies the /admin/services/stats endpoint
// returns HTTP 200 with a JSON envelope containing a "security" key.
func TestServicesStats_GetStats_OK(t *testing.T) {
	db := &statsDB{}
	h := admin.NewServicesStatsHandler(db, nil, nil)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/services/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body, "security", "response must have 'security' key")
	assert.Contains(t, body, "worker_up", "response must have 'worker_up' key")
	assert.Equal(t, false, body["worker_up"], "worker_up must be false when redis is nil")
}

// TestServicesStats_WindowParam verifies that window=24h and window=30d are
// accepted without error (the query still returns 200).
func TestServicesStats_WindowParam(t *testing.T) {
	for _, w := range []string{"24h", "7d", "30d", ""} {
		db := &statsDB{}
		h := admin.NewServicesStatsHandler(db, nil, nil)
		r := chi.NewRouter()
		h.Routes(r)

		req := httptest.NewRequest(http.MethodGet, "/services/stats?window="+w, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "window=%q", w)
	}
}
