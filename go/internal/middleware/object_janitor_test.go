package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type janitorRow struct {
	id  int64
	key string
}

type fakeJanitorDB struct {
	rows     []janitorRow
	queryErr error
	execs    []string // "DELETE:<id>" or "FAIL:<id>:<msg>"
	args     []any
}

type fakeJanitorRows struct {
	rows []janitorRow
	pos  int
}

func (r *fakeJanitorRows) Next() bool { r.pos++; return r.pos <= len(r.rows) }
func (r *fakeJanitorRows) Scan(dest ...any) error {
	*dest[0].(*int64) = r.rows[r.pos-1].id
	*dest[1].(*string) = r.rows[r.pos-1].key
	return nil
}
func (r *fakeJanitorRows) Close() error { return nil }

func (f *fakeJanitorDB) Query(_ context.Context, sql string, args ...any) (RowScanner, error) {
	f.args = args
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &fakeJanitorRows{rows: f.rows}, nil
}

func (f *fakeJanitorDB) Exec(_ context.Context, sql string, args ...any) error {
	switch {
	case strings.HasPrefix(strings.TrimSpace(sql), "DELETE"):
		f.execs = append(f.execs, "DELETE:"+itoa(args[0].(int64)))
	case strings.Contains(sql, "attempts = attempts + 1"):
		f.execs = append(f.execs, "FAIL:"+itoa(args[0].(int64))+":"+args[1].(string))
	}
	return nil
}

func itoa(n int64) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

type fakeArtifactDeleter struct {
	deleted []string
	failFor map[string]error
}

func (f *fakeArtifactDeleter) DeleteArtifact(_ context.Context, key string) error {
	if err := f.failFor[key]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func TestObjectJanitor_DeletesObjectThenQueueRow(t *testing.T) {
	db := &fakeJanitorDB{rows: []janitorRow{{1, "a/1.bin"}, {2, "a/2.bin"}}}
	store := &fakeArtifactDeleter{}

	NewObjectJanitor(db, store, nil).SweepOnce(context.Background())

	if got := strings.Join(store.deleted, ","); got != "a/1.bin,a/2.bin" {
		t.Errorf("deleted objects = %q", got)
	}
	if got := strings.Join(db.execs, ","); got != "DELETE:1,DELETE:2" {
		t.Errorf("queue rows removed = %q", got)
	}
}

// A failed object delete keeps the queue row (attempt recorded) so it is retried; others still proceed.
func TestObjectJanitor_FailureIsRecordedAndKeepsRow(t *testing.T) {
	db := &fakeJanitorDB{rows: []janitorRow{{1, "bad"}, {2, "good"}}}
	store := &fakeArtifactDeleter{failFor: map[string]error{"bad": errors.New("minio down")}}

	NewObjectJanitor(db, store, nil).SweepOnce(context.Background())

	if got := strings.Join(db.execs, ","); got != "FAIL:1:minio down,DELETE:2" {
		t.Errorf("execs = %q", got)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "good" {
		t.Errorf("deleted = %v", store.deleted)
	}
}

// With no storage client nothing is touched: the rows stay queued until a worker with storage runs.
func TestObjectJanitor_NilStoreDoesNothing(t *testing.T) {
	db := &fakeJanitorDB{rows: []janitorRow{{1, "a"}}}
	NewObjectJanitor(db, nil, nil).SweepOnce(context.Background())
	if len(db.execs) != 0 || db.args != nil {
		t.Errorf("nil store must not query or modify anything: execs=%v args=%v", db.execs, db.args)
	}
}

func TestObjectJanitor_QueryErrorIsHarmless(t *testing.T) {
	db := &fakeJanitorDB{queryErr: errors.New("db down")}
	store := &fakeArtifactDeleter{}
	NewObjectJanitor(db, store, nil).SweepOnce(context.Background())
	if len(store.deleted) != 0 || len(db.execs) != 0 {
		t.Errorf("nothing may happen on a query error: %v %v", store.deleted, db.execs)
	}
}

// Rows that exhausted their attempts are excluded by the query (passed as $1) and the batch size is $2.
func TestObjectJanitor_QueryParameters(t *testing.T) {
	db := &fakeJanitorDB{}
	NewObjectJanitor(db, &fakeArtifactDeleter{}, nil).SweepOnce(context.Background())
	if len(db.args) != 2 || db.args[0] != janitorMaxAttempts || db.args[1] != 100 {
		t.Errorf("args = %v, want [%d 100]", db.args, janitorMaxAttempts)
	}
}
