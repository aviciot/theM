package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/jackc/pgx/v5"
)

func newProviderKeySvc(d *fakeDal) *service.LLMProviderKeyService {
	return service.NewLLMProviderKeyService(d, providerTestSecretKey)
}

// ── Create tests ──────────────────────────────────────────────────────────────

func TestProviderKeyService_Create_MissingName_ReturnsValidation(t *testing.T) {
	svc := newProviderKeySvc(&fakeDal{})
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		APIKey: "sk-test-key-12345678",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestProviderKeyService_Create_MissingAPIKey_ReturnsValidation(t *testing.T) {
	svc := newProviderKeySvc(&fakeDal{})
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		Name: "Key_for_april",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestProviderKeyService_Create_EncryptsBeforePersist(t *testing.T) {
	d := &fakeDal{createdProviderKey: dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "Key_for_april"}}
	svc := newProviderKeySvc(d)
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		Name: "Key_for_april", APIKey: "sk-test-key-12345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.createProviderKeyCalls) == 0 {
		t.Fatal("CreateLLMProviderKey not called")
	}
	in := d.createProviderKeyCalls[0]
	if !strings.HasPrefix(in.APIKeyEncrypted, "enc:") {
		t.Errorf("want encrypted key with enc: prefix, got %q", in.APIKeyEncrypted)
	}
	if strings.Contains(in.APIKeyEncrypted, "sk-test-key-12345678") {
		t.Error("plaintext must not appear in the stored encrypted value")
	}
}

func TestProviderKeyService_Create_DuplicateName_ReturnsConflict(t *testing.T) {
	d := &fakeDal{createProviderKeyErr: pgUniqueErr()}
	svc := newProviderKeySvc(d)
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		Name: "dup", APIKey: "sk-test-key-12345678",
	})
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}

func TestProviderKeyService_Create_IsDefault_ClearsExistingDefaultFirst(t *testing.T) {
	d := &fakeDal{createdProviderKey: dal.LLMProviderKey{ID: 2, LLMProviderID: 1, TenantID: strp("tid"), Name: "k2", IsDefault: true}}
	svc := newProviderKeySvc(d)
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		Name: "k2", APIKey: "sk-test-key-12345678", IsDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.clearDefaultKeysCalls != 1 {
		t.Errorf("want ClearDefaultLLMProviderKeys called once, got %d", d.clearDefaultKeysCalls)
	}
}

func TestProviderKeyService_Create_NotDefault_DoesNotClear(t *testing.T) {
	d := &fakeDal{createdProviderKey: dal.LLMProviderKey{ID: 2, LLMProviderID: 1, TenantID: strp("tid"), Name: "k2"}}
	svc := newProviderKeySvc(d)
	_, err := svc.Create(context.Background(), 1, strp("tid"), service.LLMProviderKeyCreate{
		Name: "k2", APIKey: "sk-test-key-12345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.clearDefaultKeysCalls != 0 {
		t.Errorf("want ClearDefaultLLMProviderKeys not called, got %d calls", d.clearDefaultKeysCalls)
	}
}

// ── Update tests ──────────────────────────────────────────────────────────────

func TestProviderKeyService_Update_NotFound(t *testing.T) {
	d := &fakeDal{getProviderKeyErr: pgx.ErrNoRows}
	svc := newProviderKeySvc(d)
	newName := "renamed"
	_, err := svc.Update(context.Background(), 999, strp("tid"), service.LLMProviderKeyPatch{Name: &newName})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderKeyService_Update_RenameOnly_PreservesSecret(t *testing.T) {
	existingEnc := encryptForTest("sk-existing-key-12345678")
	d := &fakeDal{
		providerKey:        dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "old", APIKeyEncrypted: existingEnc},
		updatedProviderKey: dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "new", APIKeyEncrypted: existingEnc},
	}
	svc := newProviderKeySvc(d)
	newName := "new"
	_, err := svc.Update(context.Background(), 1, strp("tid"), service.LLMProviderKeyPatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.updateProviderKeyCalls) == 0 {
		t.Fatal("UpdateLLMProviderKey not called")
	}
	in := d.updateProviderKeyCalls[0]
	if in.APIKeyEncrypted != existingEnc {
		t.Errorf("secret must be preserved on rename-only update; want %q, got %q", existingEnc, in.APIKeyEncrypted)
	}
	if in.Name != "new" {
		t.Errorf("want name=new, got %q", in.Name)
	}
}

func TestProviderKeyService_Update_EmptyName_ReturnsValidation(t *testing.T) {
	d := &fakeDal{providerKey: dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "old"}}
	svc := newProviderKeySvc(d)
	empty := ""
	_, err := svc.Update(context.Background(), 1, strp("tid"), service.LLMProviderKeyPatch{Name: &empty})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestProviderKeyService_Update_RotateSecret(t *testing.T) {
	oldEnc := encryptForTest("sk-old-key-12345678")
	d := &fakeDal{
		providerKey:        dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "k", APIKeyEncrypted: oldEnc},
		updatedProviderKey: dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "k"},
	}
	svc := newProviderKeySvc(d)
	newKey := "sk-new-key-abcdefghij"
	_, err := svc.Update(context.Background(), 1, strp("tid"), service.LLMProviderKeyPatch{APIKey: &newKey})
	if err != nil {
		t.Fatal(err)
	}
	in := d.updateProviderKeyCalls[0]
	if in.APIKeyEncrypted == oldEnc {
		t.Error("secret must be rotated, not preserved")
	}
	if !strings.HasPrefix(in.APIKeyEncrypted, "enc:") {
		t.Errorf("rotated secret must have enc: prefix, got %q", in.APIKeyEncrypted)
	}
	if strings.Contains(in.APIKeyEncrypted, "sk-new-key-abcdefghij") {
		t.Error("plaintext must not appear in the stored encrypted value")
	}
}

// ── SetDefault / Delete tests ─────────────────────────────────────────────────

func TestProviderKeyService_SetDefault_NotFound(t *testing.T) {
	d := &fakeDal{setDefaultProviderKeyErr: pgx.ErrNoRows}
	svc := newProviderKeySvc(d)
	_, err := svc.SetDefault(context.Background(), 999, strp("tid"))
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderKeyService_Delete_NotFound(t *testing.T) {
	d := &fakeDal{deleteProviderKeyErr: pgx.ErrNoRows}
	svc := newProviderKeySvc(d)
	err := svc.Delete(context.Background(), 999, strp("tid"))
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderKeyService_Delete_Success(t *testing.T) {
	svc := newProviderKeySvc(&fakeDal{})
	if err := svc.Delete(context.Background(), 1, strp("tid")); err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
}

// ── ResolveDecrypted tests ─────────────────────────────────────────────────────

func TestProviderKeyService_ResolveDecrypted_NotFound(t *testing.T) {
	d := &fakeDal{getProviderKeyErr: pgx.ErrNoRows}
	svc := newProviderKeySvc(d)
	_, err := svc.ResolveDecrypted(context.Background(), 999, strp("tid"))
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderKeyService_ResolveDecrypted_ReturnsPlaintext(t *testing.T) {
	enc := encryptForTest("sk-my-real-key-99999999")
	d := &fakeDal{providerKey: dal.LLMProviderKey{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), APIKeyEncrypted: enc}}
	svc := newProviderKeySvc(d)
	plain, err := svc.ResolveDecrypted(context.Background(), 1, strp("tid"))
	if err != nil {
		t.Fatal(err)
	}
	if plain != "sk-my-real-key-99999999" {
		t.Errorf("want decrypted plaintext, got %q", plain)
	}
}

// ── Masking tests ─────────────────────────────────────────────────────────────

func TestProviderKeyService_List_MasksSecret_NoPlaintextInOutput(t *testing.T) {
	plain := "sk-ant-api03-verysecretkey12345678"
	enc := encryptForTest(plain)
	d := &fakeDal{providerKeys: []dal.LLMProviderKey{{ID: 1, LLMProviderID: 1, TenantID: strp("tid"), Name: "k", APIKeyEncrypted: enc}}}
	svc := newProviderKeySvc(d)
	list, err := svc.List(context.Background(), 1, strp("tid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 key, got %d", len(list))
	}
	if strings.Contains(list[0].Masked, plain) {
		t.Errorf("plaintext must not appear in masked output; got %q", list[0].Masked)
	}
}

func TestProviderKeyService_List_Empty_ReturnsEmptySlice(t *testing.T) {
	svc := newProviderKeySvc(&fakeDal{providerKeys: []dal.LLMProviderKey{}})
	list, err := svc.List(context.Background(), 1, strp("tid"))
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Error("want non-nil empty slice, got nil")
	}
}

// ── RecordTestResult ───────────────────────────────────────────────────────────

func TestProviderKeyService_RecordTestResult_PassesThrough(t *testing.T) {
	d := &fakeDal{}
	svc := newProviderKeySvc(d)
	if err := svc.RecordTestResult(context.Background(), 1, strp("tid"), true); err != nil {
		t.Fatal(err)
	}
	if len(d.setTestResultCalls) != 1 || !d.setTestResultCalls[0] {
		t.Errorf("want RecordTestResult(true) recorded, got %v", d.setTestResultCalls)
	}
}
