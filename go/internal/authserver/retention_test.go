package authserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRetentionDB struct {
	results []int64 // rows affected per call, in order (then 0)
	err     error
	calls   []string // "sessions" | "blacklist"
	args    [][]any
}

func (f *fakeRetentionDB) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	kind := "blacklist"
	if len(args) == 2 {
		kind = "sessions"
	}
	f.calls = append(f.calls, kind)
	f.args = append(f.args, args)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.results) == 0 {
		return 0, nil
	}
	n := f.results[0]
	f.results = f.results[1:]
	return n, nil
}

func TestPurgeExpired_LoopsUntilBatchNotFull(t *testing.T) {
	db := &fakeRetentionDB{results: []int64{2000, 2000, 150, 0}} // sessions: 3 statements; blacklist: 1 (0 rows)
	s, b, err := PurgeExpired(context.Background(), db, RetentionConfig{SessionGrace: 7 * 24 * time.Hour, Batch: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if s != 4150 || b != 0 {
		t.Errorf("sessions=%d blacklisted=%d, want 4150 / 0", s, b)
	}
	if got := len(db.calls); got != 4 {
		t.Errorf("statements = %d (%v), want 3 session batches + 1 blacklist", got, db.calls)
	}
}

func TestPurgeExpired_PassesGraceSecondsAndBatch(t *testing.T) {
	db := &fakeRetentionDB{}
	if _, _, err := PurgeExpired(context.Background(), db, RetentionConfig{SessionGrace: 2 * time.Hour, Batch: 500}); err != nil {
		t.Fatal(err)
	}
	if db.args[0][0] != float64(7200) || db.args[0][1] != 500 {
		t.Errorf("session args = %v, want [7200 500]", db.args[0])
	}
	if db.args[1][0] != 500 {
		t.Errorf("blacklist args = %v, want [500]", db.args[1])
	}
}

// Grace 0 keeps sessions forever but still clears expired blacklist rows.
func TestPurgeExpired_ZeroGraceKeepsSessions(t *testing.T) {
	db := &fakeRetentionDB{results: []int64{3}}
	s, b, err := PurgeExpired(context.Background(), db, RetentionConfig{SessionGrace: 0, Batch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if s != 0 || b != 3 || len(db.calls) != 1 || db.calls[0] != "blacklist" {
		t.Errorf("sessions=%d blacklisted=%d calls=%v", s, b, db.calls)
	}
}

func TestPurgeExpired_ErrorStopsAndReports(t *testing.T) {
	db := &fakeRetentionDB{err: errors.New("db down")}
	if _, _, err := PurgeExpired(context.Background(), db, RetentionConfig{SessionGrace: time.Hour, Batch: 10}); err == nil {
		t.Fatal("expected the error")
	}
	if len(db.calls) != 1 {
		t.Errorf("must stop at the first failure, calls=%v", db.calls)
	}
}

// A huge backlog is worked off over several runs, not in one unbounded loop.
func TestPurgeExpired_BoundedPerRun(t *testing.T) {
	full := make([]int64, 1000)
	for i := range full {
		full[i] = 10
	}
	db := &fakeRetentionDB{results: full}
	_, _, _ = PurgeExpired(context.Background(), db, RetentionConfig{SessionGrace: time.Hour, Batch: 10})
	if len(db.calls) > 2*maxBatchesPerRun {
		t.Errorf("statements = %d, want at most %d", len(db.calls), 2*maxBatchesPerRun)
	}
}

func TestRetentionConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("THE_M_AUTH_RETENTION_ENABLED", "")
	t.Setenv("THE_M_AUTH_RETENTION_SESSIONS_DAYS", "")
	t.Setenv("THE_M_AUTH_RETENTION_INTERVAL_MINUTES", "")
	c := RetentionConfigFromEnv()
	if !c.Enabled || c.SessionGrace != 7*24*time.Hour || c.Interval != time.Hour {
		t.Errorf("defaults wrong: %+v", c)
	}
	t.Setenv("THE_M_AUTH_RETENTION_ENABLED", "false")
	t.Setenv("THE_M_AUTH_RETENTION_SESSIONS_DAYS", "0")
	c = RetentionConfigFromEnv()
	if c.Enabled || c.SessionGrace != 0 {
		t.Errorf("overrides ignored: %+v", c)
	}
}
