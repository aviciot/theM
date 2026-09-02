package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/middleware"
)

// ── fake DB ───────────────────────────────────────────────────────────────────

// gateTestDB is a disabled-config DB: every QueryRow returns empty JSON → Enabled=false.
type gateTestDB struct {
	enqueueCount int
	nextID       string
	queryErr     error
}

func (d *gateTestDB) Exec(_ context.Context, sql string, _ ...any) error {
	if findSub(sql, "middleware_jobs") {
		d.enqueueCount++
	}
	return nil
}

func (d *gateTestDB) QueryRow(_ context.Context, _ string, _ ...any) middleware.SingleRowScanner {
	return &fakeRow{val: d.nextID, err: d.queryErr}
}

func (d *gateTestDB) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	return &fakeRows{}, nil
}

// enabledGateTestDB returns enabled security config + succeeds on quarantine INSERT.
type enabledGateTestDB struct {
	enqueueCount int
}

func (d *enabledGateTestDB) Exec(_ context.Context, sql string, _ ...any) error {
	if findSub(sql, "middleware_jobs") {
		d.enqueueCount++
	}
	return nil
}

func (d *enabledGateTestDB) QueryRow(_ context.Context, sql string, _ ...any) middleware.SingleRowScanner {
	if findSub(sql, "quarantine_artifacts") || findSub(sql, "run_artifacts") {
		// INSERT … RETURNING — return nothing (gate uses Exec, not QueryRow, for inserts now)
		return &fakeRow{val: ""}
	}
	// Security config query
	return &fakeRow{val: `{"enabled":true,"processors":{"av_scan":{"enabled":true,"max_file_mb":5}}}`}
}

func (d *enabledGateTestDB) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	return &fakeRows{}, nil
}

// ── fake store ────────────────────────────────────────────────────────────────

type fakeStore struct {
	putCalled int
	failPut   bool
}

func (s *fakeStore) PutQuarantine(_ context.Context, _ string, _ []byte, _ string) error {
	s.putCalled++
	if s.failPut {
		return context.DeadlineExceeded
	}
	return nil
}

// ── fake row / rows ───────────────────────────────────────────────────────────

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

// ── string helpers ────────────────────────────────────────────────────────────

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

// ── tests ─────────────────────────────────────────────────────────────────────

// TestFileGate_Disabled verifies that when security_config is disabled the gate
// returns ScanStatus="disabled" without touching storage.
func TestFileGate_Disabled(t *testing.T) {
	db := &gateTestDB{nextID: ""}
	store := &fakeStore{}

	gate := middleware.NewFileGate(db, store)
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
	if store.putCalled != 0 {
		t.Errorf("store should not be called when security is disabled")
	}
}

// TestFileGate_FetchFailsOpen verifies that a bad download URL does not block
// delivery.
func TestFileGate_FetchFailsOpen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	db := &gateTestDB{nextID: "artifact-uuid"}
	store := &fakeStore{}
	gate := middleware.NewFileGate(db, store)

	res, err := gate.Intercept(context.Background(), middleware.GateInput{
		DownloadURL:   ts.URL + "/file.bin",
		ApplicationID: "app-2",
		RunID:         "run-2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// DB returns disabled config → disabled, store never called
	if res.ScanStatus != "disabled" {
		t.Errorf("expected disabled (app not enabled), got %s", res.ScanStatus)
	}
}

// TestFileGate_InvalidateCache verifies that InvalidateCache removes the entry.
func TestFileGate_InvalidateCache(t *testing.T) {
	db := &gateTestDB{nextID: ""}
	gate := middleware.NewFileGate(db, &fakeStore{})
	_, _ = gate.Intercept(context.Background(), middleware.GateInput{ApplicationID: "app-3"})
	gate.InvalidateCache("app-3")
}

// TestFileGate_InterceptInline_Enabled verifies that InterceptInline stores bytes
// in MinIO quarantine and enqueues a job when security scanning is enabled.
func TestFileGate_InterceptInline_Enabled(t *testing.T) {
	db := &enabledGateTestDB{}
	store := &fakeStore{}
	gate := middleware.NewFileGate(db, store)

	data := []byte("hello from inline test")
	res, err := gate.InterceptInline(context.Background(), middleware.GateInput{
		FileName:      "test.txt",
		ContentType:   "text/plain",
		ApplicationID: "app-inline",
		RunID:         "00000000-0000-0000-0000-000000000001",
		SessionID:     "",
	}, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArtifactID == "" {
		t.Error("expected a quarantine artifact ID, got empty string")
	}
	if res.ScanStatus != "pending" {
		t.Errorf("expected pending, got %q", res.ScanStatus)
	}
	if store.putCalled != 1 {
		t.Errorf("expected 1 PutQuarantine call, got %d", store.putCalled)
	}
	if db.enqueueCount != 1 {
		t.Errorf("expected 1 middleware job enqueued, got %d", db.enqueueCount)
	}
}

// TestFileGate_InterceptInline_Disabled verifies that InterceptInline returns
// disabled when security is not enabled.
func TestFileGate_InterceptInline_Disabled(t *testing.T) {
	db := &gateTestDB{nextID: ""}
	store := &fakeStore{}
	gate := middleware.NewFileGate(db, store)

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
	if store.putCalled != 0 {
		t.Errorf("store should not be called when disabled")
	}
}

// TestFileGate_StoreFail_FailsOpen verifies that a MinIO write failure results
// in fail-open (disabled) rather than an error blocking delivery.
func TestFileGate_StoreFail_FailsOpen(t *testing.T) {
	db := &enabledGateTestDB{}
	store := &fakeStore{failPut: true}
	gate := middleware.NewFileGate(db, store)

	res, err := gate.InterceptInline(context.Background(), middleware.GateInput{
		FileName:      "file.bin",
		ApplicationID: "app-storefail",
		RunID:         "00000000-0000-0000-0000-000000000002",
	}, []byte("some bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ScanStatus != "disabled" {
		t.Errorf("expected fail-open (disabled) on store error, got %q", res.ScanStatus)
	}
}

// TestFileGate_NilStore_DoesNotPanic verifies that a nil store (no S3 configured)
// does not panic when security scanning is enabled for the application.
// Regression test for the nil pointer dereference that crashed them-go-worker.
func TestFileGate_NilStore_DoesNotPanic(t *testing.T) {
	db := &enabledGateTestDB{} // returns enabled security config
	gate := middleware.NewFileGate(db, nil)

	res, err := gate.InterceptInline(context.Background(), middleware.GateInput{
		FileName:      "file.bin",
		ApplicationID: "app-nilstore",
		RunID:         "00000000-0000-0000-0000-000000000003",
	}, []byte("some bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ScanStatus != "disabled" {
		t.Errorf("expected fail-open (disabled) with nil store, got %q", res.ScanStatus)
	}
}
