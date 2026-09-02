package middleware_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aviciot/them/internal/middleware"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeReaperQuerier struct {
	rows    []middleware.ExpiredQuarantineRow
	deleted []string // IDs deleted via Exec
	queryErr error
	execErr  error
}

func (f *fakeReaperQuerier) Query(_ context.Context, _ string, _ ...any) (middleware.RowScanner, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &fakeReaperRows{rows: f.rows}, nil
}

func (f *fakeReaperQuerier) Exec(_ context.Context, _ string, args ...any) error {
	if f.execErr != nil {
		return f.execErr
	}
	if len(args) > 0 {
		if id, ok := args[0].(string); ok {
			f.deleted = append(f.deleted, id)
		}
	}
	return nil
}

type fakeReaperRows struct {
	rows  []middleware.ExpiredQuarantineRow
	index int
}

func (r *fakeReaperRows) Next() bool  { r.index++; return r.index <= len(r.rows) }
func (r *fakeReaperRows) Close() error { return nil }
func (r *fakeReaperRows) Scan(dest ...any) error {
	row := r.rows[r.index-1]
	if len(dest) >= 2 {
		*dest[0].(*string) = row.ID
		*dest[1].(*string) = row.StorageKey
	}
	return nil
}

type fakeReaperStore struct {
	deletedKeys []string
	deleteErr   error
}

func (s *fakeReaperStore) GetQuarantine(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (s *fakeReaperStore) DeleteQuarantine(_ context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletedKeys = append(s.deletedKeys, key)
	return nil
}
func (s *fakeReaperStore) PutArtifact(_ context.Context, _ string, _ []byte, _ string) error {
	return nil
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestReaper_DeletesExpiredRows verifies the happy path: expired rows are
// deleted from both MinIO and the DB.
func TestReaper_DeletesExpiredRows(t *testing.T) {
	q := &fakeReaperQuerier{
		rows: []middleware.ExpiredQuarantineRow{
			{ID: "aaa", StorageKey: "quarantine/aaa"},
			{ID: "bbb", StorageKey: "quarantine/bbb"},
		},
	}
	store := &fakeReaperStore{}
	r := middleware.NewReaper(q, store, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.ReapOnce(ctx)

	if len(q.deleted) != 2 {
		t.Fatalf("expected 2 DB deletes, got %d", len(q.deleted))
	}
	if len(store.deletedKeys) != 2 {
		t.Fatalf("expected 2 MinIO deletes, got %d", len(store.deletedKeys))
	}
}

// TestReaper_NoRows verifies the reaper is a no-op when there are no expired rows.
func TestReaper_NoRows(t *testing.T) {
	q := &fakeReaperQuerier{}
	store := &fakeReaperStore{}
	r := middleware.NewReaper(q, store, nil)

	r.ReapOnce(context.Background())

	if len(q.deleted) != 0 || len(store.deletedKeys) != 0 {
		t.Fatalf("expected no deletes, got db=%d minio=%d", len(q.deleted), len(store.deletedKeys))
	}
}

// TestReaper_MinIOErrorDoesNotBlockDBDelete verifies that a MinIO delete failure
// does not prevent the DB row from being cleaned up.
func TestReaper_MinIOErrorDoesNotBlockDBDelete(t *testing.T) {
	q := &fakeReaperQuerier{
		rows: []middleware.ExpiredQuarantineRow{
			{ID: "ccc", StorageKey: "quarantine/ccc"},
		},
	}
	store := &fakeReaperStore{deleteErr: errors.New("minio unavailable")}
	r := middleware.NewReaper(q, store, nil)

	r.ReapOnce(context.Background())

	// DB row should still be deleted even though MinIO failed.
	if len(q.deleted) != 1 {
		t.Fatalf("expected 1 DB delete, got %d", len(q.deleted))
	}
}

// TestReaper_EmptyStorageKeySkipsMinIO verifies that a row with no storage_key
// (bytes already scrubbed) skips MinIO and only deletes the DB row.
func TestReaper_EmptyStorageKeySkipsMinIO(t *testing.T) {
	q := &fakeReaperQuerier{
		rows: []middleware.ExpiredQuarantineRow{
			{ID: "ddd", StorageKey: ""},
		},
	}
	store := &fakeReaperStore{}
	r := middleware.NewReaper(q, store, nil)

	r.ReapOnce(context.Background())

	if len(q.deleted) != 1 {
		t.Fatalf("expected 1 DB delete, got %d", len(q.deleted))
	}
	if len(store.deletedKeys) != 0 {
		t.Fatalf("expected 0 MinIO deletes, got %d", len(store.deletedKeys))
	}
}

// TestReaper_QueryErrorIsHandled verifies a DB query error doesn't panic.
func TestReaper_QueryErrorIsHandled(t *testing.T) {
	q := &fakeReaperQuerier{queryErr: errors.New("db error")}
	r := middleware.NewReaper(q, nil, nil)
	r.ReapOnce(context.Background()) // must not panic
}
