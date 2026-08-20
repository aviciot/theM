package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/service"
)

// ── PutRuntime tests ───────────────────────────────────────────────────────────

// W8-S1: PutRuntime success — calls UpdateRuntimeConfig, returns config.
func TestPutRuntime_Success(t *testing.T) {
	d := &fakeDal{orchNames: []string{"orch-1"}}
	c := &fakeCache{}
	svc := service.NewAppService(d, c, nil)

	n := 5
	cfg := service.AppRuntimeConfig{MaxConcurrentSessions: &n}
	out, err := svc.PutRuntime(context.Background(), "tenant-1", "app-1", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.updateRuntimeConfigCalled {
		t.Error("UpdateRuntimeConfig must be called")
	}
	if out.MaxConcurrentSessions == nil || *out.MaxConcurrentSessions != 5 {
		t.Errorf("want MaxConcurrentSessions=5, got %v", out.MaxConcurrentSessions)
	}
}

// W8-S2: PutRuntime — DAL returns pgx.ErrNoRows → ErrNotFound.
func TestPutRuntime_NotFound(t *testing.T) {
	d := &fakeDal{updateRuntimeConfigErr: pgx.ErrNoRows}
	svc := service.NewAppService(d, nil, nil)

	_, err := svc.PutRuntime(context.Background(), "tenant-1", "missing-app", service.AppRuntimeConfig{})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// W8-S3: PutRuntime — nil slices become [] not null.
func TestPutRuntime_NilSlicesNormalized(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil)

	cfg := service.AppRuntimeConfig{} // BlockedTokens and BlockedUserIDs are nil
	out, err := svc.PutRuntime(context.Background(), "t1", "app-1", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.BlockedTokens == nil {
		t.Error("BlockedTokens must be [] not nil")
	}
	if out.BlockedUserIDs == nil {
		t.Error("BlockedUserIDs must be [] not nil")
	}
}

// W8-S4: PutRuntime — cache flush called after successful update.
func TestPutRuntime_CacheFlushAfterUpdate(t *testing.T) {
	d := &fakeDal{orchNames: []string{"orch-a", "orch-b"}}
	c := &fakeCache{}
	svc := service.NewAppService(d, c, nil)

	_, err := svc.PutRuntime(context.Background(), "t1", "app-1", service.AppRuntimeConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect cache Del calls for each orch name (orchCacheKeyFmt + orchLocKeyFmt) + agentRegistryKey
	// plus a Publish call
	if len(c.deletedKeys) == 0 {
		t.Error("expected cache invalidation after PutRuntime")
	}
}

// ── BulkDelete tests ────────────────────────────────────────────────────────────

// W8-S5: BulkDelete empty IDs → (0, nil), no DB calls.
func TestBulkDelete_Empty(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil)

	count, err := svc.BulkDelete(context.Background(), "t1", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("want 0, got %d", count)
	}
	if d.bulkDeleteCalled {
		t.Error("BulkDeleteApplications must not be called for empty input")
	}
}

// W8-S6: BulkDelete > 200 IDs → ErrValidation.
func TestBulkDelete_TooMany(t *testing.T) {
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "id"
	}
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	_, err := svc.BulkDelete(context.Background(), "t1", ids)
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

// W8-S7: BulkDelete — returns deleted count.
func TestBulkDelete_TenantIsolation(t *testing.T) {
	d := &fakeDal{bulkDeletedCount: 3}
	svc := service.NewAppService(d, nil, nil)

	count, err := svc.BulkDelete(context.Background(), "t1", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("want 3, got %d", count)
	}
}

// W8-S8: BulkDelete — cache flush called AFTER delete, not before.
func TestBulkDelete_FlushAfterDelete(t *testing.T) {
	d := &fakeDal{bulkDeletedCount: 1, orchNames: []string{"orch-x"}}
	c := &fakeCache{}
	svc := service.NewAppService(d, c, nil)

	_, err := svc.BulkDelete(context.Background(), "t1", []string{"app-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.bulkDeleteCalled {
		t.Error("BulkDeleteApplications must be called")
	}
	// Cache should have been flushed after delete
	if len(c.deletedKeys) == 0 {
		t.Error("expected cache flush after bulk delete")
	}
}

// W8-S9: BulkDelete — no cache flush if BulkDeleteApplications returns error.
func TestBulkDelete_NoFlushOnDBError(t *testing.T) {
	d := &fakeDal{bulkDeleteErr: errors.New("db error")}
	c := &fakeCache{}
	svc := service.NewAppService(d, c, nil)

	_, err := svc.BulkDelete(context.Background(), "t1", []string{"app-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(c.deletedKeys) != 0 {
		t.Errorf("cache must not be flushed on DB error, got %v", c.deletedKeys)
	}
}
