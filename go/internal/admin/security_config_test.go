package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/tenantctx"
)

// secCfgDB is a minimal fake admin.DBQuerier for the security config handler.
type secCfgDB struct {
	cfg     string // raw JSONB returned on SELECT
	execSQL string // last SQL passed to Exec
}

func (d *secCfgDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}
func (d *secCfgDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &secCfgRow{val: d.cfg}
}
func (d *secCfgDB) Exec(_ context.Context, sql string, _ ...any) error {
	d.execSQL = sql
	return nil
}
func (d *secCfgDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{err: nil}
}

// secCfgRow scans a single []byte value for the security_config column.
type secCfgRow struct{ val string }

func (s *secCfgRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if b, ok := dest[0].(*[]byte); ok {
			*b = []byte(s.val)
		}
	}
	return nil
}

// buildSecCfgHandler wires up the security config handler on a chi router,
// injecting a fixed tenant ID so MustTenantIDFromCtx does not panic.
func buildSecCfgHandler(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tenantctx.WithTenantID(r.Context(), "00000000-0000-0000-0000-000000000001")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	h := admin.NewSecurityConfigHandler(db, nil) // nil redis = skip pub/sub
	h.Routes(r)
	return r
}

// TestSecurityConfigGet_ReturnsDefault verifies that GET returns the merged
// default config when the DB has no stored config (empty JSON).
func TestSecurityConfigGet_ReturnsDefault(t *testing.T) {
	db := &secCfgDB{cfg: `{}`}
	h := buildSecCfgHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/applications/app-1/security-config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg middleware.SecurityConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Default config should have Enabled=false
	if cfg.Enabled {
		t.Error("default config should have Enabled=false")
	}
}

// TestSecurityConfigPut_ValidConfig verifies that PUT with a valid config saves
// it and returns 200.
func TestSecurityConfigPut_ValidConfig(t *testing.T) {
	db := &secCfgDB{cfg: `{}`}
	h := buildSecCfgHandler(db)

	body := middleware.SecurityConfig{Enabled: false}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/security-config",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestSecurityConfigPut_InvalidJSON verifies that a malformed body returns 400.
func TestSecurityConfigPut_InvalidJSON(t *testing.T) {
	db := &secCfgDB{}
	h := buildSecCfgHandler(db)

	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/security-config",
		bytes.NewBufferString("not-json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestSecurityConfigPut_InvalidMaxFileMB verifies that av_scan.max_file_mb=0
// returns 422.
func TestSecurityConfigPut_InvalidMaxFileMB(t *testing.T) {
	db := &secCfgDB{}
	h := buildSecCfgHandler(db)

	avRaw, _ := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 0})
	body := middleware.SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{"av_scan": avRaw},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/security-config",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
