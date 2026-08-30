package agentgen

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockHITLRedis is a minimal in-memory implementation of TaskStoreRedis for HITLStore tests.
type mockHITLRedis struct {
	data map[string][]byte
}

func newMockHITLRedis() *mockHITLRedis {
	return &mockHITLRedis{data: make(map[string][]byte)}
}

func (m *mockHITLRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.data[key]
	return v, ok, nil
}

func (m *mockHITLRedis) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *mockHITLRedis) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// ── HS-1: Store then Get returns the same handle ─────────────────────────────

func TestHITLStore_StoreAndGet(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	err := s.Store(context.Background(), "task-1", "wf-abc", "run-xyz", "tenant-t1", "step-hw1")
	if err != nil {
		t.Fatalf("Store: unexpected error: %v", err)
	}

	h, err := s.Get(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if h.WorkflowID != "wf-abc" {
		t.Errorf("WorkflowID: want wf-abc, got %q", h.WorkflowID)
	}
	if h.RunID != "run-xyz" {
		t.Errorf("RunID: want run-xyz, got %q", h.RunID)
	}
	if h.TenantID != "tenant-t1" {
		t.Errorf("TenantID: want tenant-t1, got %q", h.TenantID)
	}
	if h.StepID != "step-hw1" {
		t.Errorf("StepID: want step-hw1, got %q", h.StepID)
	}
	if h.State != HITLStateSubmitted {
		t.Errorf("State: want submitted, got %q", h.State)
	}
}

// ── HS-2: Get on missing key returns ErrHITLNotFound ─────────────────────────

func TestHITLStore_GetMissing(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_, err := s.Get(context.Background(), "no-such-task")
	if !errors.Is(err, ErrHITLNotFound) {
		t.Errorf("expected ErrHITLNotFound, got: %v", err)
	}
}

// ── HS-3: Delete removes the handle so Get returns ErrHITLNotFound ───────────

func TestHITLStore_Delete(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	if err := s.Store(context.Background(), "task-2", "wf-del", "run-del", "t1", "step-del"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.Delete(context.Background(), "task-2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.Get(context.Background(), "task-2")
	if !errors.Is(err, ErrHITLNotFound) {
		t.Errorf("expected ErrHITLNotFound after delete, got: %v", err)
	}
}

// ── HS-4: Store overwrites an existing entry ─────────────────────────────────

func TestHITLStore_StoreOverwrite(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_ = s.Store(context.Background(), "task-3", "wf-old", "run-old", "t1", "step-old")
	_ = s.Store(context.Background(), "task-3", "wf-new", "run-new", "t1", "step-new")

	h, err := s.Get(context.Background(), "task-3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if h.WorkflowID != "wf-new" {
		t.Errorf("overwrite: want wf-new, got %q", h.WorkflowID)
	}
}

// ── HS-5: Key prefix is correct ──────────────────────────────────────────────

func TestHITLStore_KeyPrefix(t *testing.T) {
	m := newMockHITLRedis()
	s := NewHITLStore(m)

	_ = s.Store(context.Background(), "tid-abc", "wf", "run", "t1", "step")

	expectedKey := hitlKeyPrefix + "tid-abc"
	if _, ok := m.data[expectedKey]; !ok {
		t.Errorf("expected key %q in store, got keys: %v", expectedKey, m.data)
	}
}

// ── HS-6: UpdateWaitToken changes state to waiting and sets wait_token ────────

func TestHITLStore_UpdateWaitToken(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	if err := s.Store(context.Background(), "task-upd", "wf-u", "run-u", "t1", "hw1"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.UpdateWaitToken(context.Background(), "task-upd", "tok-abc123", "hw2"); err != nil {
		t.Fatalf("UpdateWaitToken: %v", err)
	}

	h, err := s.Get(context.Background(), "task-upd")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if h.State != HITLStateWaiting {
		t.Errorf("State: want waiting, got %q", h.State)
	}
	if h.WaitToken != "tok-abc123" {
		t.Errorf("WaitToken: want tok-abc123, got %q", h.WaitToken)
	}
	if h.StepID != "hw2" {
		t.Errorf("StepID: want hw2, got %q", h.StepID)
	}
}

// ── HS-7: TrySignal succeeds with correct token, state → signalled ────────────

func TestHITLStore_TrySignal_Success(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_ = s.Store(context.Background(), "task-sig", "wf-s", "run-s", "t1", "hw1")
	_ = s.UpdateWaitToken(context.Background(), "task-sig", "correct-tok", "hw1")

	h, err := s.TrySignal(context.Background(), "task-sig", "correct-tok")
	if err != nil {
		t.Fatalf("TrySignal: unexpected error: %v", err)
	}
	if h.State != HITLStateSignalled {
		t.Errorf("State: want signalled, got %q", h.State)
	}

	// Verify persisted state.
	h2, err := s.Get(context.Background(), "task-sig")
	if err != nil {
		t.Fatalf("Get after TrySignal: %v", err)
	}
	if h2.State != HITLStateSignalled {
		t.Errorf("persisted State: want signalled, got %q", h2.State)
	}
}

// ── HS-8: TrySignal with wrong token returns ErrHITLWrongToken, state unchanged

func TestHITLStore_TrySignal_WrongToken(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_ = s.Store(context.Background(), "task-wrong", "wf-w", "run-w", "t1", "hw1")
	_ = s.UpdateWaitToken(context.Background(), "task-wrong", "real-tok", "hw1")

	_, err := s.TrySignal(context.Background(), "task-wrong", "bad-tok")
	if !errors.Is(err, ErrHITLWrongToken) {
		t.Errorf("expected ErrHITLWrongToken, got: %v", err)
	}

	// State must remain waiting.
	h, _ := s.Get(context.Background(), "task-wrong")
	if h.State != HITLStateWaiting {
		t.Errorf("state must remain waiting after wrong token, got %q", h.State)
	}
}

// ── HS-9: TrySignal when state != waiting returns ErrHITLNotWaiting ───────────

func TestHITLStore_TrySignal_NotWaiting(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	// Store without calling UpdateWaitToken → state is "submitted".
	_ = s.Store(context.Background(), "task-notwait", "wf-nw", "run-nw", "t1", "hw1")

	_, err := s.TrySignal(context.Background(), "task-notwait", "any-tok")
	if !errors.Is(err, ErrHITLNotWaiting) {
		t.Errorf("expected ErrHITLNotWaiting, got: %v", err)
	}
}

// ── HS-10: MarkDone removes handle ────────────────────────────────────────────

func TestHITLStore_MarkDone(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_ = s.Store(context.Background(), "task-done", "wf-d", "run-d", "t1", "hw1")
	if err := s.MarkDone(context.Background(), "task-done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	_, err := s.Get(context.Background(), "task-done")
	if !errors.Is(err, ErrHITLNotFound) {
		t.Errorf("expected ErrHITLNotFound after MarkDone, got: %v", err)
	}
}

// ── HS-11: repeated wait — UpdateWaitToken when state=signalled → state=waiting, new token

func TestHITLStore_RepeatedWait(t *testing.T) {
	s := NewHITLStore(newMockHITLRedis())

	_ = s.Store(context.Background(), "task-repeat", "wf-r", "run-r", "t1", "hw1")

	// First wait cycle.
	_ = s.UpdateWaitToken(context.Background(), "task-repeat", "tok-1", "hw1")
	_, _ = s.TrySignal(context.Background(), "task-repeat", "tok-1")

	h, _ := s.Get(context.Background(), "task-repeat")
	if h.State != HITLStateSignalled {
		t.Fatalf("State after first signal: want signalled, got %q", h.State)
	}

	// Second wait cycle (next hw node in loop body).
	if err := s.UpdateWaitToken(context.Background(), "task-repeat", "tok-2", "hw2"); err != nil {
		t.Fatalf("UpdateWaitToken for second wait: %v", err)
	}
	h, _ = s.Get(context.Background(), "task-repeat")
	if h.State != HITLStateWaiting {
		t.Errorf("State after second UpdateWaitToken: want waiting, got %q", h.State)
	}
	if h.WaitToken != "tok-2" {
		t.Errorf("WaitToken: want tok-2, got %q", h.WaitToken)
	}
	if h.StepID != "hw2" {
		t.Errorf("StepID: want hw2, got %q", h.StepID)
	}
}
