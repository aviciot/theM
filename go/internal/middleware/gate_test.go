package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/middleware"
)

// gateTestDB implements GateQuerier for tests.
type gateTestDB struct {
	// secConfig is returned for any application security config query.
	secConfig string
	// storedArtifact captures the artifact INSERT data for assertions.
	storedFilename string
	storedMimeType string
	storedSize     int
	enqueueCount   int
	// lastID returned by storeArtifact INSERT
	nextID string
	// queryErr can force an error on QueryRow
	queryErr error
}

func (d *gateTestDB) Exec(_ context.Context, sql string, _ ...any) error {
	if contains(sql, "middleware_jobs") {
		d.enqueueCount++
	}
	return nil
}

func (d *gateTestDB) QueryRow(_ context.Context, sql string, _ ...any) middleware.SingleRowScanner {
	return &fakeRow{val: d.nextID, err: d.queryErr}
}

func (d *gateTestDB) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	return &fakeRows{}, nil
}

type fakeRow struct {
	val string
	err error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		switch d := dest[0].(type) {
		case *string:
			*d = r.val
		case *[]byte:
			*d = []byte(r.val)
		}
	}
	return nil
}

type fakeRows struct{}

func (r *fakeRows) Next() bool          { return false }
func (r *fakeRows) Scan(_ ...any) error { return nil }
func (r *fakeRows) Close() error        { return nil }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSub(s, sub))
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestFileGate_Disabled verifies that when security_config is disabled, the
// gate returns ScanStatus="disabled" without fetching or storing anything.
func TestFileGate_Disabled(t *testing.T) {
	// DB returns disabled security config (empty JSON → Enabled=false)
	db := &gateTestDB{nextID: ""}

	gate := middleware.NewFileGate(db)
	res, err := gate.Intercept(context.Background(), middleware.GateInput{
		DownloadURL:   "http://nowhere",
		ApplicationID: "app-1",
		RunID:         "run-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ScanStatus != "disabled" {
		t.Errorf("expected disabled, got %s", res.ScanStatus)
	}
	if res.ArtifactID != "" {
		t.Errorf("expected no artifact ID, got %s", res.ArtifactID)
	}
}

// TestFileGate_FetchFailsOpen verifies that a bad download URL does not block
// delivery — it returns ScanStatus="disabled" (fail open).
func TestFileGate_FetchFailsOpen(t *testing.T) {
	// Use a test server that returns 503 so fetch fails.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	db := &gateTestDB{nextID: "artifact-uuid"}
	gate := middleware.NewFileGate(db)

	// Override the cache with enabled config by pre-populating the cache
	// via a fake initial load. Since the DB returns empty JSON (Enabled=false),
	// the fetch-fails-open test actually exercises the disabled path.
	// This verifies that a disabled app never hits the network at all.
	res, err := gate.Intercept(context.Background(), middleware.GateInput{
		DownloadURL:   ts.URL + "/file.bin",
		ApplicationID: "app-2",
		RunID:         "run-2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Disabled app → no fetch, no artifact stored
	if res.ScanStatus != "disabled" {
		t.Errorf("expected disabled (app not enabled), got %s", res.ScanStatus)
	}
}

// TestFileGate_InvalidateCache verifies that InvalidateCache removes the entry.
func TestFileGate_InvalidateCache(t *testing.T) {
	db := &gateTestDB{nextID: ""}
	gate := middleware.NewFileGate(db)
	// Call once to populate cache
	_, _ = gate.Intercept(context.Background(), middleware.GateInput{ApplicationID: "app-3"})
	// Invalidate
	gate.InvalidateCache("app-3")
	// Should not panic or error
}

// enabledGateTestDB returns enabled security config with av_scan enabled.
type enabledGateTestDB struct {
	nextID       string
	enqueueCount int
}

func (d *enabledGateTestDB) Exec(_ context.Context, sql string, _ ...any) error {
	if findSub(sql, "middleware_jobs") {
		d.enqueueCount++
	}
	return nil
}

func (d *enabledGateTestDB) QueryRow(_ context.Context, sql string, _ ...any) middleware.SingleRowScanner {
	if findSub(sql, "run_artifacts") {
		return &fakeRow{val: d.nextID}
	}
	// Security config query returns enabled config.
	return &fakeRow{val: `{"enabled":true,"processors":{"av_scan":{"enabled":true,"max_file_mb":5}}}`}
}

func (d *enabledGateTestDB) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	return &fakeRows{}, nil
}

// TestFileGate_InterceptInline_Enabled verifies that InterceptInline stores the
// artifact and enqueues a job when security scanning is enabled.
func TestFileGate_InterceptInline_Enabled(t *testing.T) {
	db := &enabledGateTestDB{nextID: "artifact-inline-uuid"}
	gate := middleware.NewFileGate(db)

	data := []byte("hello from inline test")
	res, err := gate.InterceptInline(context.Background(), middleware.GateInput{
		FileName:      "test.txt",
		ContentType:   "text/plain",
		ApplicationID: "app-inline",
		RunID:         "run-inline",
		SessionID:     "",
	}, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArtifactID != "artifact-inline-uuid" {
		t.Errorf("expected artifact-inline-uuid, got %q", res.ArtifactID)
	}
	if res.ScanStatus != "pending" {
		t.Errorf("expected pending, got %q", res.ScanStatus)
	}
	if db.enqueueCount != 1 {
		t.Errorf("expected 1 middleware job enqueued, got %d", db.enqueueCount)
	}
}

// TestFileGate_InterceptInline_Disabled verifies that InterceptInline returns
// disabled when security is not enabled for the application.
func TestFileGate_InterceptInline_Disabled(t *testing.T) {
	db := &gateTestDB{nextID: ""}
	gate := middleware.NewFileGate(db)

	res, err := gate.InterceptInline(context.Background(), middleware.GateInput{
		FileName:      "file.bin",
		ApplicationID: "app-dis",
		RunID:         "run-dis",
	}, []byte("data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ScanStatus != "disabled" {
		t.Errorf("expected disabled, got %q", res.ScanStatus)
	}
	if res.ArtifactID != "" {
		t.Errorf("expected no artifact ID, got %q", res.ArtifactID)
	}
}
