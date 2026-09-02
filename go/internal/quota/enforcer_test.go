package quota_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aviciot/them/internal/quota"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeCounter struct{ count int; err error }

func (f *fakeCounter) CountActiveRuns(_ context.Context, _ string) (int, error) {
	return f.count, f.err
}

type fakeRedis struct {
	val int64
	err error
}

func (f *fakeRedis) Incr(_ context.Context, _ string) (int64, error) {
	f.val++
	if f.err != nil {
		return 0, f.err
	}
	return f.val, nil
}
func (f *fakeRedis) Expire(_ context.Context, _ string, _ time.Duration) error { return nil }

const testTenantID = "00000000-0000-0000-0000-000000000001"

func intPtr(v int) *int { return &v }

// ── QE-01: nil quota limits → always passes ───────────────────────────────────

func TestEnforcer_NilLimits(t *testing.T) {
	e := quota.New(&fakeCounter{count: 99}, &fakeRedis{})
	if err := e.Check(context.Background(), testTenantID, quota.Quota{}); err != nil {
		t.Fatalf("QE-01: expected nil, got %v", err)
	}
}

// ── QE-02: concurrent runs below limit → passes ───────────────────────────────

func TestEnforcer_ConcurrentBelowLimit(t *testing.T) {
	e := quota.New(&fakeCounter{count: 3}, &fakeRedis{})
	q := quota.Quota{MaxConcurrentRuns: intPtr(5)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-02: expected nil, got %v", err)
	}
}

// ── QE-03: concurrent runs at limit → ErrConcurrentRunsExceeded ──────────────

func TestEnforcer_ConcurrentAtLimit(t *testing.T) {
	e := quota.New(&fakeCounter{count: 5}, &fakeRedis{})
	q := quota.Quota{MaxConcurrentRuns: intPtr(5)}
	err := e.Check(context.Background(), testTenantID, q)
	if !errors.Is(err, quota.ErrConcurrentRunsExceeded) {
		t.Fatalf("QE-03: expected ErrConcurrentRunsExceeded, got %v", err)
	}
}

// ── QE-04: runs/min within limit → passes ────────────────────────────────────

func TestEnforcer_RPMBelowLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 4})
	q := quota.Quota{RunsPerMinute: intPtr(10)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-04: expected nil, got %v", err)
	}
}

// ── QE-05: runs/min exceeds limit → ErrRunsRateLimited ───────────────────────

func TestEnforcer_RPMExceeded(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 10}) // next Incr returns 11
	q := quota.Quota{RunsPerMinute: intPtr(10)}
	err := e.Check(context.Background(), testTenantID, q)
	if !errors.Is(err, quota.ErrRunsRateLimited) {
		t.Fatalf("QE-05: expected ErrRunsRateLimited, got %v", err)
	}
}

// ── QE-06: DB error counting runs is surfaced ─────────────────────────────────

func TestEnforcer_DBError(t *testing.T) {
	dbErr := errors.New("db unavailable")
	e := quota.New(&fakeCounter{err: dbErr}, &fakeRedis{})
	q := quota.Quota{MaxConcurrentRuns: intPtr(5)}
	err := e.Check(context.Background(), testTenantID, q)
	if err == nil {
		t.Fatal("QE-06: expected error, got nil")
	}
	if errors.Is(err, quota.ErrConcurrentRunsExceeded) {
		t.Fatal("QE-06: expected wrapped DB error, not ErrConcurrentRunsExceeded")
	}
}
