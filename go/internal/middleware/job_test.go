package middleware_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aviciot/them/internal/middleware"
)

// ── fake object store ─────────────────────────────────────────────────────────

type fakeObjectStore struct {
	quarantineData map[string][]byte // key → bytes
	artifactsData  map[string][]byte
	deletedKeys    []string
	failGet        bool
	failPutArtifact bool
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{
		quarantineData: make(map[string][]byte),
		artifactsData:  make(map[string][]byte),
	}
}

func (s *fakeObjectStore) GetQuarantine(_ context.Context, key string) ([]byte, error) {
	if s.failGet {
		return nil, context.DeadlineExceeded
	}
	d, ok := s.quarantineData[key]
	if !ok {
		return nil, context.DeadlineExceeded
	}
	return d, nil
}

func (s *fakeObjectStore) DeleteQuarantine(_ context.Context, key string) error {
	s.deletedKeys = append(s.deletedKeys, key)
	delete(s.quarantineData, key)
	return nil
}

func (s *fakeObjectStore) PutArtifact(_ context.Context, key string, data []byte, _ string) error {
	if s.failPutArtifact {
		return context.DeadlineExceeded
	}
	s.artifactsData[key] = data
	return nil
}

// ── fake job querier ──────────────────────────────────────────────────────────

// jobTestQuerier records Exec calls and returns canned QueryRow responses.
type jobTestQuerier struct {
	execCalls  []string
	queryRows  map[string]fakeJobRow // SQL substring → row
}

type fakeJobRow struct {
	cols []any
	err  error
}

func newJobTestQuerier() *jobTestQuerier {
	return &jobTestQuerier{queryRows: make(map[string]fakeJobRow)}
}

func (q *jobTestQuerier) Exec(_ context.Context, sql string, _ ...any) error {
	q.execCalls = append(q.execCalls, sql)
	return nil
}

func (q *jobTestQuerier) QueryRow(_ context.Context, sql string, _ ...any) middleware.SingleRowScanner {
	for key, row := range q.queryRows {
		if strings.Contains(sql, key) {
			return &fakeJobScanner{row: row}
		}
	}
	return &fakeJobScanner{row: fakeJobRow{err: context.DeadlineExceeded}}
}

func (q *jobTestQuerier) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	return &fakeRows{}, nil
}

func (q *jobTestQuerier) hasExec(sub string) bool {
	for _, s := range q.execCalls {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type fakeJobScanner struct{ row fakeJobRow }

func (s *fakeJobScanner) Scan(dest ...any) error {
	if s.row.err != nil {
		return s.row.err
	}
	for i, d := range dest {
		if i >= len(s.row.cols) {
			break
		}
		switch v := d.(type) {
		case *string:
			if sv, ok := s.row.cols[i].(string); ok {
				*v = sv
			}
		case *int64:
			if iv, ok := s.row.cols[i].(int64); ok {
				*v = iv
			}
		case *[]byte:
			if bv, ok := s.row.cols[i].([]byte); ok {
				*v = bv
			}
		}
	}
	return nil
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestJobDAL_EnqueueWithQuarantine verifies the SQL INSERT is executed.
func TestJobDAL_EnqueueWithQuarantine(t *testing.T) {
	q := newJobTestQuerier()
	dal := middleware.NewJobDAL(q)

	err := dal.EnqueueWithQuarantine(context.Background(),
		"artifact-uuid", "quarantine-uuid", "app-uuid", "run-uuid", "", []string{"av_scan"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !q.hasExec("middleware_jobs") {
		t.Error("expected middleware_jobs INSERT, not found in exec calls")
	}
}

// TestJobDAL_LoadFileBytes_QuarantinePath verifies that bytes are fetched from the
// fake store when QuarantineID is set.
func TestJobDAL_LoadFileBytes_QuarantinePath(t *testing.T) {
	q := newJobTestQuerier()
	q.queryRows["quarantine_artifacts"] = fakeJobRow{
		cols: []any{"report.pdf", "application/pdf", int64(42), "quarantine/abc123"},
	}

	store := newFakeObjectStore()
	store.quarantineData["quarantine/abc123"] = []byte("fake pdf bytes")

	dal := middleware.NewJobDAL(q)
	job := &middleware.Job{
		ArtifactID:   "art-uuid",
		QuarantineID: "q-uuid",
	}

	if err := dal.LoadFileBytes(context.Background(), job, store); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.FileName != "report.pdf" {
		t.Errorf("expected report.pdf, got %q", job.FileName)
	}
	if string(job.FileBytes) != "fake pdf bytes" {
		t.Errorf("unexpected bytes: %q", job.FileBytes)
	}
}

// TestJobDAL_Complete_CleanPath verifies that clean scan promotes bytes to artifacts
// and deletes the quarantine key.
func TestJobDAL_Complete_CleanPath(t *testing.T) {
	q := newJobTestQuerier()
	store := newFakeObjectStore()
	store.quarantineData["quarantine/q1"] = []byte("clean file")

	dal := middleware.NewJobDAL(q)
	job := &middleware.Job{
		ID:            "job-1",
		ArtifactID:    "art-1",
		QuarantineID:  "q-uuid-1",
		ApplicationID: "app-1",
		RunID:         "run-1",
		SessionID:     "",
		FileName:      "doc.txt",
		MimeType:      "text/plain",
		FileSize:      10,
		FileBytes:     []byte("clean file"),
		StorageKey:    "quarantine/q1",
	}

	res := middleware.JobResult{
		FinalStatus: "clean",
		ScannedAt:   time.Now().UTC(),
	}

	if err := dal.Complete(context.Background(), job, res, store); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify artifact was promoted to artifacts bucket
	if _, ok := store.artifactsData["artifacts/art-1"]; !ok {
		t.Error("expected artifact to be promoted to artifacts bucket")
	}
	// Verify quarantine key was deleted
	deleted := false
	for _, k := range store.deletedKeys {
		if k == "quarantine/q1" {
			deleted = true
		}
	}
	if !deleted {
		t.Error("expected quarantine/q1 to be deleted after clean scan")
	}
	// Verify DB received run_artifacts INSERT
	if !q.hasExec("run_artifacts") {
		t.Error("expected run_artifacts INSERT/UPDATE")
	}
}

// TestJobDAL_Complete_InfectedPath verifies that infected scan inserts a metadata-only
// row (no bytes promoted) and deletes quarantine.
func TestJobDAL_Complete_InfectedPath(t *testing.T) {
	q := newJobTestQuerier()
	store := newFakeObjectStore()
	store.quarantineData["quarantine/q2"] = []byte("eicar test virus string")

	dal := middleware.NewJobDAL(q)
	job := &middleware.Job{
		ID:            "job-2",
		ArtifactID:    "art-2",
		QuarantineID:  "q-uuid-2",
		ApplicationID: "app-2",
		RunID:         "run-2",
		FileName:      "virus.exe",
		MimeType:      "application/octet-stream",
		FileBytes:     []byte("eicar test virus string"),
		StorageKey:    "quarantine/q2",
	}

	res := middleware.JobResult{
		FinalStatus: "infected",
		Threat:      "Eicar-Test-Signature",
		ScannedAt:   time.Now().UTC(),
	}

	if err := dal.Complete(context.Background(), job, res, store); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Infected: NO artifact in artifacts bucket
	if _, ok := store.artifactsData["artifacts/art-2"]; ok {
		t.Error("infected artifact must NOT be promoted to artifacts bucket")
	}
	// Quarantine bytes deleted
	if _, ok := store.quarantineData["quarantine/q2"]; ok {
		t.Error("expected quarantine bytes to be deleted after infected scan")
	}
	// DB still gets a run_artifacts metadata row
	if !q.hasExec("run_artifacts") {
		t.Error("expected run_artifacts INSERT for infected artifact")
	}
}

// TestJobDAL_Complete_LegacyPath verifies backward compatibility when QuarantineID is empty.
func TestJobDAL_Complete_LegacyPath(t *testing.T) {
	q := newJobTestQuerier()
	store := newFakeObjectStore()

	dal := middleware.NewJobDAL(q)
	job := &middleware.Job{
		ID:           "job-3",
		ArtifactID:   "art-3",
		QuarantineID: "", // legacy path
	}
	res := middleware.JobResult{FinalStatus: "clean", ScannedAt: time.Now().UTC()}

	if err := dal.Complete(context.Background(), job, res, store); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !q.hasExec("run_artifacts") {
		t.Error("expected run_artifacts UPDATE for legacy path")
	}
	// No MinIO activity on legacy path
	if len(store.artifactsData) > 0 || len(store.deletedKeys) > 0 {
		t.Error("legacy path must not touch MinIO")
	}
}
