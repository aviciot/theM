package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// providerTestSecretKey is a test-only key — never used with real data.
const providerTestSecretKey = "wave7-service-test-secret-do-not-use"

func newProviderSvc(d *fakeDal) *service.LLMProviderService {
	return service.NewLLMProviderService(d, providerTestSecretKey)
}

// encryptForTest produces a valid "enc:..." stored value using the test secret key.
func encryptForTest(plaintext string) string {
	key := crypto.DeriveKey(providerTestSecretKey)
	stored, err := crypto.EncryptStored(key, plaintext)
	if err != nil {
		panic("encryptForTest: " + err.Error())
	}
	return stored
}

// pgUniqueErr returns a *pgconn.PgError with SQLSTATE 23505.
func pgUniqueErr() error {
	return &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
}

// ── Masking tests ─────────────────────────────────────────────────────────────

func TestMaskKey_NoKey_ReturnsNil(t *testing.T) {
	d := &fakeDal{providers: []dal.LLMProvider{{ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m"}}}
	svc := newProviderSvc(d)
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 provider, got %d", len(list))
	}
	p := list[0]
	if p.APIKeySet {
		t.Error("want api_key_set=false when no key stored")
	}
	if p.APIKeyMasked != nil {
		t.Errorf("want api_key_masked=null, got %q", *p.APIKeyMasked)
	}
}

func TestMaskKey_ShortPlaintext_ReturnsFourStars(t *testing.T) {
	// "hello" is 5 chars, ≤8 → "****"
	stored := encryptForTest("hello")
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		APIKeyEncrypted: &stored,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	if !list[0].APIKeySet {
		t.Error("want api_key_set=true")
	}
	if list[0].APIKeyMasked == nil || *list[0].APIKeyMasked != "****" {
		t.Errorf("want ****, got %v", list[0].APIKeyMasked)
	}
}

func TestMaskKey_ExactlyEightChars_ReturnsFourStars(t *testing.T) {
	stored := encryptForTest("12345678") // 8 chars → ≤8 → "****"
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		APIKeyEncrypted: &stored,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	if list[0].APIKeyMasked == nil || *list[0].APIKeyMasked != "****" {
		t.Errorf("want ****, got %v", list[0].APIKeyMasked)
	}
}

func TestMaskKey_LongPlaintext_ReturnsFirstFourDotsLastFour(t *testing.T) {
	// "sk-ant-api03-testkey" is 20 chars → first4...last4
	plain := "sk-ant-api03-testkey"
	stored := encryptForTest(plain)
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		APIKeyEncrypted: &stored,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	masked := list[0].APIKeyMasked
	if masked == nil {
		t.Fatal("want non-nil api_key_masked")
	}
	want := plain[:4] + "..." + plain[len(plain)-4:]
	if *masked != want {
		t.Errorf("want %q, got %q", want, *masked)
	}
}

func TestMaskKey_DecryptError_ReturnsFourStars(t *testing.T) {
	// Corrupted stored value — bad token but has enc: prefix.
	badStored := "enc:notavalidfernettokengAAAAABad"
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		APIKeyEncrypted: &badStored,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	if !list[0].APIKeySet {
		t.Error("want api_key_set=true (key is set, just unreadable)")
	}
	if list[0].APIKeyMasked == nil || *list[0].APIKeyMasked != "****" {
		t.Errorf("want **** on decrypt error, got %v", list[0].APIKeyMasked)
	}
}

func TestMaskKey_NoPlaintextInOutput(t *testing.T) {
	plain := "sk-ant-api03-verysecretkey12345678"
	stored := encryptForTest(plain)
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		APIKeyEncrypted: &stored,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	masked := ""
	if list[0].APIKeyMasked != nil {
		masked = *list[0].APIKeyMasked
	}
	// The plaintext must not appear verbatim in the masked value.
	if strings.Contains(masked, plain) {
		t.Errorf("plaintext must not appear in masked output; got %q", masked)
	}
}

// ── Create tests ──────────────────────────────────────────────────────────────

func TestProviderService_Create_NoKey_ReturnsKeyNotSet(t *testing.T) {
	d := &fakeDal{createdProvider: dal.LLMProvider{
		ID: 1, Name: "anthropic", DisplayName: "Anthropic", DefaultModel: "claude-sonnet-4-6",
	}}
	svc := newProviderSvc(d)
	out, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "anthropic", DisplayName: "Anthropic", DefaultModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.APIKeySet {
		t.Error("want api_key_set=false when no key provided")
	}
	if out.APIKeyMasked != nil {
		t.Error("want api_key_masked=null when no key provided")
	}
}

func TestProviderService_Create_WithKey_EncryptsBeforePersist(t *testing.T) {
	enc := encryptForTest("sk-test-api-key-abc12345")
	d := &fakeDal{createdProvider: dal.LLMProvider{
		ID: 2, Name: "openai", DisplayName: "OpenAI", DefaultModel: "gpt-4o",
		APIKeyEncrypted: &enc,
	}}
	svc := newProviderSvc(d)
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "openai", DisplayName: "OpenAI", DefaultModel: "gpt-4o",
		APIKey: "sk-test-api-key-abc12345",
	})
	if err != nil {
		t.Fatal(err)
	}
	// DAL must have received an encrypted key (not plaintext).
	if len(d.createProviderCalls) == 0 {
		t.Fatal("CreateProvider not called")
	}
	dalIn := d.createProviderCalls[0]
	if dalIn.APIKeyEncrypted == nil {
		t.Error("DAL must receive a non-nil api_key_encrypted")
	}
	if dalIn.APIKeyEncrypted != nil && !strings.HasPrefix(*dalIn.APIKeyEncrypted, "enc:") {
		t.Errorf("DAL api_key_encrypted must start with enc:, got %q", *dalIn.APIKeyEncrypted)
	}
	// Must never store the plaintext.
	if dalIn.APIKeyEncrypted != nil && strings.Contains(*dalIn.APIKeyEncrypted, "sk-test-api-key-abc12345") {
		t.Error("plaintext must not appear in the stored encrypted value")
	}
}

func TestProviderService_Create_DuplicateName_ReturnsConflict(t *testing.T) {
	d := &fakeDal{createProviderErr: pgUniqueErr()}
	svc := newProviderSvc(d)
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "anthropic", DisplayName: "A", DefaultModel: "m",
	})
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}

func TestProviderService_Create_MissingName_ReturnsValidation(t *testing.T) {
	svc := newProviderSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		DisplayName: "Anthropic", DefaultModel: "m",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestProviderService_Create_MissingDisplayName_ReturnsValidation(t *testing.T) {
	svc := newProviderSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "anthropic", DefaultModel: "m",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestProviderService_Create_MissingDefaultModel_ReturnsValidation(t *testing.T) {
	svc := newProviderSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "anthropic", DisplayName: "Anthropic",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

// ── Get tests ─────────────────────────────────────────────────────────────────

func TestProviderService_Get_NotFound(t *testing.T) {
	d := &fakeDal{getProviderErr: pgx.ErrNoRows}
	svc := newProviderSvc(d)
	_, err := svc.Get(context.Background(), 999)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderService_Get_Found_ReturnsOut(t *testing.T) {
	enc := encryptForTest("sk-ant-my-key-1234567890")
	d := &fakeDal{provider: dal.LLMProvider{
		ID: 5, Name: "anthropic", DisplayName: "Anthropic", DefaultModel: "claude-sonnet-4-6",
		APIKeyEncrypted: &enc, Enabled: true,
	}}
	svc := newProviderSvc(d)
	out, err := svc.Get(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != 5 {
		t.Errorf("want ID=5, got %d", out.ID)
	}
	if !out.APIKeySet {
		t.Error("want api_key_set=true")
	}
}

// ── Update tests ──────────────────────────────────────────────────────────────

func TestProviderService_Update_NoKeyChange_PreservesEncryptedKey(t *testing.T) {
	existingEnc := encryptForTest("sk-existing-key-12345678")
	d := &fakeDal{
		provider: dal.LLMProvider{
			ID: 1, Name: "anthropic", DisplayName: "Old", DefaultModel: "m",
			APIKeyEncrypted: &existingEnc, Enabled: true,
		},
		updatedProvider: dal.LLMProvider{
			ID: 1, Name: "anthropic", DisplayName: "New", DefaultModel: "m",
			APIKeyEncrypted: &existingEnc, Enabled: true,
		},
	}
	svc := newProviderSvc(d)
	newDisplay := "New"
	// api_key absent from patch (APIKeyPresent=false)
	patch := service.LLMProviderPatch{DisplayName: &newDisplay}
	_, err := svc.Update(context.Background(), 1, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.updateProviderCalls) == 0 {
		t.Fatal("UpdateProvider not called")
	}
	dalIn := d.updateProviderCalls[0]
	if dalIn.APIKeyEncrypted == nil || *dalIn.APIKeyEncrypted != existingEnc {
		t.Errorf("existing encrypted key must be preserved; want %q, got %v", existingEnc, dalIn.APIKeyEncrypted)
	}
}

func TestProviderService_Update_WithNewKey_RotatesEncryptedKey(t *testing.T) {
	existingEnc := encryptForTest("sk-old-key-12345678")
	d := &fakeDal{
		provider: dal.LLMProvider{
			ID: 1, Name: "anthropic", DisplayName: "A", DefaultModel: "m",
			APIKeyEncrypted: &existingEnc, Enabled: true,
		},
		updatedProvider: dal.LLMProvider{ID: 1, Name: "anthropic", DisplayName: "A", DefaultModel: "m", Enabled: true},
	}
	svc := newProviderSvc(d)
	newKey := "sk-new-key-abcdefghij"
	patch := service.LLMProviderPatch{APIKey: &newKey, APIKeyPresent: true}
	_, err := svc.Update(context.Background(), 1, patch)
	if err != nil {
		t.Fatal(err)
	}
	dalIn := d.updateProviderCalls[0]
	if dalIn.APIKeyEncrypted == nil {
		t.Error("new encrypted key must be non-nil")
	}
	if dalIn.APIKeyEncrypted != nil && *dalIn.APIKeyEncrypted == existingEnc {
		t.Error("new encrypted key must differ from old")
	}
	if dalIn.APIKeyEncrypted != nil && !strings.HasPrefix(*dalIn.APIKeyEncrypted, "enc:") {
		t.Errorf("new key must start with enc:, got %q", *dalIn.APIKeyEncrypted)
	}
	// Plaintext of new key must not appear in the stored value.
	if dalIn.APIKeyEncrypted != nil && strings.Contains(*dalIn.APIKeyEncrypted, "sk-new-key-abcdefghij") {
		t.Error("plaintext must not appear in the stored encrypted value")
	}
}

func TestProviderService_Update_ClearKey_SetsNil(t *testing.T) {
	existingEnc := encryptForTest("sk-existing-key-12345678")
	d := &fakeDal{
		provider: dal.LLMProvider{
			ID: 1, Name: "anthropic", DisplayName: "A", DefaultModel: "m",
			APIKeyEncrypted: &existingEnc, Enabled: true,
		},
		updatedProvider: dal.LLMProvider{ID: 1, Name: "anthropic", DisplayName: "A", DefaultModel: "m", Enabled: true},
	}
	svc := newProviderSvc(d)
	emptyKey := ""
	patch := service.LLMProviderPatch{APIKey: &emptyKey, APIKeyPresent: true}
	_, err := svc.Update(context.Background(), 1, patch)
	if err != nil {
		t.Fatal(err)
	}
	dalIn := d.updateProviderCalls[0]
	if dalIn.APIKeyEncrypted != nil {
		t.Errorf("clearing key: want nil api_key_encrypted, got %q", *dalIn.APIKeyEncrypted)
	}
}

func TestProviderService_Update_NotFound(t *testing.T) {
	d := &fakeDal{getProviderErr: pgx.ErrNoRows}
	svc := newProviderSvc(d)
	_, err := svc.Update(context.Background(), 999, service.LLMProviderPatch{})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// ── Delete tests ──────────────────────────────────────────────────────────────

func TestProviderService_Delete_NotFound(t *testing.T) {
	d := &fakeDal{deleteProviderErr: pgx.ErrNoRows}
	svc := newProviderSvc(d)
	err := svc.Delete(context.Background(), 999)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestProviderService_Delete_Success(t *testing.T) {
	svc := newProviderSvc(&fakeDal{})
	err := svc.Delete(context.Background(), 1)
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
}

// ── List tests ────────────────────────────────────────────────────────────────

func TestProviderService_List_Empty_ReturnsEmptySlice(t *testing.T) {
	svc := newProviderSvc(&fakeDal{providers: []dal.LLMProvider{}})
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(list) != 0 {
		t.Errorf("want 0 providers, got %d", len(list))
	}
}

func TestProviderService_List_ModelPricing_DefaultsToEmptyMap(t *testing.T) {
	// nil ModelPricingRaw → service returns {} not null
	d := &fakeDal{providers: []dal.LLMProvider{{
		ID: 1, Name: "p", DisplayName: "P", DefaultModel: "m",
		ModelPricingRaw: nil,
	}}}
	svc := newProviderSvc(d)
	list, _ := svc.List(context.Background())
	if list[0].ModelPricing == nil {
		t.Error("model_pricing must be {} not null")
	}
}

// ── No plaintext in errors ────────────────────────────────────────────────────

func TestProviderService_Create_NoCryptoInternalsInError(t *testing.T) {
	// createProviderErr is a generic error → propagated as-is, not ErrConflict.
	d := &fakeDal{createProviderErr: errors.New("connection reset")}
	svc := newProviderSvc(d)
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "x", DisplayName: "X", DefaultModel: "m", APIKey: "sk-secret-12345678",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// The error message must not contain the plaintext api_key.
	if strings.Contains(err.Error(), "sk-secret-12345678") {
		t.Errorf("error message must not contain api_key plaintext: %v", err)
	}
}

// ── Null/empty field behavior ─────────────────────────────────────────────────

func TestProviderService_Create_NilModelPricing_DefaultsToEmptyMap(t *testing.T) {
	// When model_pricing is not set in request, defaults to {}
	d := &fakeDal{createdProvider: dal.LLMProvider{
		ID: 1, Name: "n", DisplayName: "D", DefaultModel: "m",
	}}
	svc := newProviderSvc(d)
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "n", DisplayName: "D", DefaultModel: "m",
		// ModelPricing intentionally nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.createProviderCalls) == 0 {
		t.Fatal("CreateProvider not called")
	}
	// The DAL must receive "{}" raw JSON (not nil).
	dalIn := d.createProviderCalls[0]
	if string(dalIn.ModelPricingRaw) != "{}" {
		t.Errorf("want model_pricing_raw={}, got %q", string(dalIn.ModelPricingRaw))
	}
}

func TestProviderService_Create_EnabledDefaultsToTrue(t *testing.T) {
	d := &fakeDal{createdProvider: dal.LLMProvider{ID: 1, Name: "n", DisplayName: "D", DefaultModel: "m", Enabled: true}}
	svc := newProviderSvc(d)
	_, err := svc.Create(context.Background(), service.LLMProviderCreate{
		Name: "n", DisplayName: "D", DefaultModel: "m",
		// Enabled is nil → should default to true
	})
	if err != nil {
		t.Fatal(err)
	}
	dalIn := d.createProviderCalls[0]
	if !dalIn.Enabled {
		t.Error("enabled must default to true when not provided")
	}
}
