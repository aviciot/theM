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

	err := s.Store(context.Background(), "task-1", "wf-abc", "run-xyz", "step-hw1")
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
	if h.StepID != "step-hw1" {
		t.Errorf("StepID: want step-hw1, got %q", h.StepID)
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

	if err := s.Store(context.Background(), "task-2", "wf-del", "run-del", "step-del"); err != nil {
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

	_ = s.Store(context.Background(), "task-3", "wf-old", "run-old", "step-old")
	_ = s.Store(context.Background(), "task-3", "wf-new", "run-new", "step-new")

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

	_ = s.Store(context.Background(), "tid-abc", "wf", "run", "step")

	expectedKey := hitlKeyPrefix + "tid-abc"
	if _, ok := m.data[expectedKey]; !ok {
		t.Errorf("expected key %q in store, got keys: %v", expectedKey, m.data)
	}
}
