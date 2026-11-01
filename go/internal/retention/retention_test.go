package retention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeDB struct {
	mu       sync.Mutex
	lockOK   bool
	lockErr  error
	released int
	execRes  map[string][]int64 // table → rows affected per call
	execErr  map[string]error
	counts   map[string]int64
	execs    []string // table of each Exec
	counted  []string
	args     [][]any
}

func tableOf(sql string) string {
	for _, t := range []string{"them.access_tokens", "them.runs", "them.tasks", "them.gateway_requests", "them.audit_logs"} {
		if strings.Contains(sql, " FROM "+t+" ") || strings.Contains(sql, "DELETE FROM "+t+" ") {
			return t
		}
	}
	return "?"
}

func (f *fakeDB) AcquireLock(context.Context, int64) (func(), bool, error) {
	if f.lockErr != nil {
		return nil, false, f.lockErr
	}
	if !f.lockOK {
		return nil, false, nil
	}
	return func() { f.mu.Lock(); f.released++; f.mu.Unlock() }, true, nil
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	t := tableOf(sql)
	f.execs = append(f.execs, t)
	f.args = append(f.args, args)
	if err := f.execErr[t]; err != nil {
		return 0, err
	}
	if r := f.execRes[t]; len(r) > 0 {
		f.execRes[t] = r[1:]
		return r[0], nil
	}
	return 0, nil
}

func (f *fakeDB) Count(_ context.Context, sql string, args ...any) (int64, error) {
	t := tableOf(sql)
	f.counted = append(f.counted, t)
	return f.counts[t], nil
}

func cfg(days map[string]int, dry bool) Config {
	return Config{Policies: DefaultPolicies(days), Batch: 100, DryRun: dry}
}

func TestRunOnce_OnlyEnabledPoliciesRun(t *testing.T) {
	db := &fakeDB{lockOK: true}
	RunOnce(context.Background(), db, cfg(map[string]int{"ACCESS_TOKENS": 7}, false), quiet)
	if len(db.execs) != 1 || db.execs[0] != "them.access_tokens" {
		t.Errorf("only the enabled policy may run, got %v", db.execs)
	}
}

func TestRunOnce_LoopsUntilBatchNotFull_AndPassesParameters(t *testing.T) {
	db := &fakeDB{lockOK: true, execRes: map[string][]int64{"them.runs": {100, 100, 30}}}
	out := RunOnce(context.Background(), db, cfg(map[string]int{"RUNS": 30}, false), quiet)
	if out["RUNS"] != 230 {
		t.Errorf("deleted = %d, want 230", out["RUNS"])
	}
	if len(db.execs) != 3 {
		t.Errorf("statements = %d, want 3", len(db.execs))
	}
	if db.args[0][0] != float64(30*24*3600) || db.args[0][1] != 100 {
		t.Errorf("args = %v, want [seconds batch]", db.args[0])
	}
}

func TestRunOnce_DryRunOnlyCounts(t *testing.T) {
	db := &fakeDB{lockOK: true, counts: map[string]int64{"them.access_tokens": 60, "them.audit_logs": 5}}
	out := RunOnce(context.Background(), db, cfg(map[string]int{"ACCESS_TOKENS": 7, "AUDIT": 90}, true), quiet)
	if len(db.execs) != 0 {
		t.Errorf("dry-run must never delete, executed %v", db.execs)
	}
	if out["ACCESS_TOKENS"] != 60 || out["AUDIT"] != 5 {
		t.Errorf("counts = %v", out)
	}
}

func TestRunOnce_LockHeldElsewhere_DoesNothing(t *testing.T) {
	db := &fakeDB{lockOK: false}
	RunOnce(context.Background(), db, cfg(map[string]int{"ACCESS_TOKENS": 7}, false), quiet)
	if len(db.execs) != 0 || len(db.counted) != 0 {
		t.Errorf("must not touch data without the lock: %v %v", db.execs, db.counted)
	}
}

func TestRunOnce_LockErrorDoesNothing(t *testing.T) {
	db := &fakeDB{lockErr: errors.New("no conn")}
	RunOnce(context.Background(), db, cfg(map[string]int{"ACCESS_TOKENS": 7}, false), quiet)
	if len(db.execs) != 0 {
		t.Errorf("executed %v", db.execs)
	}
}

func TestRunOnce_ReleasesLockOnce(t *testing.T) {
	db := &fakeDB{lockOK: true}
	RunOnce(context.Background(), db, cfg(map[string]int{"ACCESS_TOKENS": 7}, false), quiet)
	if db.released != 1 {
		t.Errorf("released %d times, want 1", db.released)
	}
}

// One failing table must not stop the others.
func TestRunOnce_ErrorInOnePolicyDoesNotStopOthers(t *testing.T) {
	db := &fakeDB{lockOK: true,
		execErr: map[string]error{"them.runs": errors.New("boom")},
		execRes: map[string][]int64{"them.audit_logs": {3}}}
	out := RunOnce(context.Background(), db, cfg(map[string]int{"RUNS": 30, "AUDIT": 90}, false), quiet)
	if out["AUDIT"] != 3 {
		t.Errorf("audit retention must still run after the runs policy failed: %v", out)
	}
}

func TestRunOnce_BoundedPerPolicy(t *testing.T) {
	full := make([]int64, 1000)
	for i := range full {
		full[i] = 100
	}
	db := &fakeDB{lockOK: true, execRes: map[string][]int64{"them.gateway_requests": full}}
	RunOnce(context.Background(), db, cfg(map[string]int{"GATEWAY": 30}, false), quiet)
	if len(db.execs) > maxBatchesPerPolicy {
		t.Errorf("statements = %d, want at most %d", len(db.execs), maxBatchesPerPolicy)
	}
}

// The filters are the safety net: pin them so they cannot be widened by accident.
func TestPolicyFilters(t *testing.T) {
	byName := map[string]Policy{}
	for _, p := range DefaultPolicies(map[string]int{}) {
		byName[p.Name] = p
	}
	tok := byName["ACCESS_TOKENS"].Where
	for _, must := range []string{"expires_at IS NOT NULL", "expires_at < now()", "playground-", "tr-"} {
		if !strings.Contains(tok, must) {
			t.Errorf("access_tokens filter lost %q: %s", must, tok)
		}
	}
	runs := byName["RUNS"].Where
	if strings.Contains(runs, "'running'") || strings.Contains(runs, "'admitted'") || !strings.Contains(runs, "'stopped'") {
		t.Errorf("runs filter must name only terminal statuses: %s", runs)
	}
	chat := byName["CHAT"].Where
	if strings.Contains(chat, "working") || strings.Contains(chat, "input-required") || strings.Contains(chat, "submitted") {
		t.Errorf("chat filter must not touch open tasks: %s", chat)
	}
	for name, p := range byName {
		if !strings.Contains(p.Where, "$1") {
			t.Errorf("%s filter must use the retention parameter $1", name)
		}
		if !strings.Contains(p.deleteSQL(), "LIMIT $2") || strings.Contains(p.countSQL(), "$2") {
			t.Errorf("%s SQL shape wrong: %s | %s", name, p.deleteSQL(), p.countSQL())
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	for _, k := range []string{"THE_M_RETENTION_DRY_RUN", "THE_M_RETENTION_INTERVAL_MINUTES", "THE_M_RETENTION_ACCESS_TOKENS_DAYS", "THE_M_RETENTION_RUNS_DAYS", "THE_M_RETENTION_CHAT_DAYS", "THE_M_RETENTION_GATEWAY_DAYS", "THE_M_RETENTION_AUDIT_DAYS"} {
		t.Setenv(k, "")
	}
	c := ConfigFromEnv()
	days := map[string]int{}
	for _, p := range c.Policies {
		days[p.Name] = p.Days
	}
	if !c.DryRun || days["ACCESS_TOKENS"] != 7 || days["RUNS"] != 0 || days["CHAT"] != 0 || days["GATEWAY"] != 0 || days["AUDIT"] != 0 {
		t.Errorf("defaults wrong: dry=%v days=%v (only temporary tokens may be on by default, and dry-run must default on)", c.DryRun, days)
	}
	t.Setenv("THE_M_RETENTION_DRY_RUN", "false")
	t.Setenv("THE_M_RETENTION_RUNS_DAYS", "30")
	c = ConfigFromEnv()
	for _, p := range c.Policies {
		if p.Name == "RUNS" && p.Days != 30 {
			t.Errorf("RUNS days = %d", p.Days)
		}
	}
	if c.DryRun {
		t.Error("THE_M_RETENTION_DRY_RUN=false ignored")
	}
}
