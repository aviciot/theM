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

func intPtr(v int) *int    { return &v }
func int64Ptr(v int64) *int64 { return &v }

type fakeTokenCounter struct{ sum int64; err error }

func (f *fakeTokenCounter) SumMonthlyTokens(_ context.Context, _ string) (int64, error) {
	return f.sum, f.err
}

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

// ── QE-07: monthly runs nil limit → always passes ─────────────────────────────

func TestEnforcer_MonthlyNilLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{})
	// MonthlyRuns is nil — no enforcement.
	if err := e.Check(context.Background(), testTenantID, quota.Quota{}); err != nil {
		t.Fatalf("QE-07: expected nil, got %v", err)
	}
}

// ── QE-08: monthly runs within limit → passes ────────────────────────────────

func TestEnforcer_MonthlyBelowLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 499}) // next Incr → 500
	q := quota.Quota{MonthlyRuns: intPtr(1000)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-08: expected nil, got %v", err)
	}
}

// ── QE-09: monthly runs exceeds limit → ErrMonthlyRunsExceeded ───────────────

func TestEnforcer_MonthlyExceeded(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 1000}) // next Incr → 1001
	q := quota.Quota{MonthlyRuns: intPtr(1000)}
	err := e.Check(context.Background(), testTenantID, q)
	if !errors.Is(err, quota.ErrMonthlyRunsExceeded) {
		t.Fatalf("QE-09: expected ErrMonthlyRunsExceeded, got %v", err)
	}
}

// ── QE-10: api/min nil limit → passes ────────────────────────────────────────

func TestEnforcer_APIRPMNilLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 999})
	if err := e.Check(context.Background(), testTenantID, quota.Quota{}); err != nil {
		t.Fatalf("QE-10: expected nil, got %v", err)
	}
}

// ── QE-11: api/min within limit → passes ─────────────────────────────────────

func TestEnforcer_APIRPMBelowLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 4})
	q := quota.Quota{APIRequestsPerMinute: intPtr(10)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-11: expected nil, got %v", err)
	}
}

// ── QE-12: api/min exceeds limit → ErrAPIRateLimited ─────────────────────────

func TestEnforcer_APIRPMExceeded(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{val: 10}) // next Incr → 11
	q := quota.Quota{APIRequestsPerMinute: intPtr(10)}
	err := e.Check(context.Background(), testTenantID, q)
	if !errors.Is(err, quota.ErrAPIRateLimited) {
		t.Fatalf("QE-12: expected ErrAPIRateLimited, got %v", err)
	}
}

// ── QE-13: monthly_llm_tokens nil limit → passes ─────────────────────────────

func TestEnforcer_MonthlyLLMTokensNilLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{}).WithTokenCounter(&fakeTokenCounter{sum: 999999})
	if err := e.Check(context.Background(), testTenantID, quota.Quota{}); err != nil {
		t.Fatalf("QE-13: expected nil, got %v", err)
	}
}

// ── QE-14: monthly_llm_tokens below limit → passes ───────────────────────────

func TestEnforcer_MonthlyLLMTokensBelowLimit(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{}).WithTokenCounter(&fakeTokenCounter{sum: 5000})
	q := quota.Quota{MonthlyLLMTokens: int64Ptr(10000)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-14: expected nil, got %v", err)
	}
}

// ── QE-15: monthly_llm_tokens at limit → ErrMonthlyLLMTokensExceeded ─────────

func TestEnforcer_MonthlyLLMTokensExceeded(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{}).WithTokenCounter(&fakeTokenCounter{sum: 10000})
	q := quota.Quota{MonthlyLLMTokens: int64Ptr(10000)}
	err := e.Check(context.Background(), testTenantID, q)
	if !errors.Is(err, quota.ErrMonthlyLLMTokensExceeded) {
		t.Fatalf("QE-15: expected ErrMonthlyLLMTokensExceeded, got %v", err)
	}
}

// ── QE-16: monthly_llm_tokens DB error → fail-open (nil) ─────────────────────

func TestEnforcer_MonthlyLLMTokensDBError(t *testing.T) {
	e := quota.New(&fakeCounter{}, &fakeRedis{}).
		WithTokenCounter(&fakeTokenCounter{err: errors.New("db down")})
	q := quota.Quota{MonthlyLLMTokens: int64Ptr(100)}
	// DB error should fail-open: no enforcement.
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-16: expected nil (fail-open), got %v", err)
	}
}

// ── QE-17: no tokenDB attached → fail-open even with limit set ───────────────

func TestEnforcer_MonthlyLLMTokensNoCounter(t *testing.T) {
	// WithTokenCounter not called — enforcer has nil tokenDB.
	e := quota.New(&fakeCounter{}, &fakeRedis{})
	q := quota.Quota{MonthlyLLMTokens: int64Ptr(1)}
	if err := e.Check(context.Background(), testTenantID, q); err != nil {
		t.Fatalf("QE-17: expected nil (no counter = fail-open), got %v", err)
	}
}
