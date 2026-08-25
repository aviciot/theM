package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/service"
)

// AGP-1: SetAppParam secret → stored JSON has ct+hint; GetPlaintextAppParams roundtrips.
func TestAppParam_SecretRoundtrip(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil) // nil cryptoKey = plain: prefix mode

	err := svc.SetAppParam(context.Background(), "tenant-1", "app-1", "geoapify_key",
		service.AppGlobalParamUpsertInput{Value: "key123456", Type: "secret"})
	if err != nil {
		t.Fatalf("SetAppParam: %v", err)
	}
	if d.setAppParamValue == nil {
		t.Fatal("DAL SetAppParam must be called")
	}

	// Stored JSON should be a providerKeyEntry: {"ct":"plain:key123456","hint":"3456"}
	var entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(d.setAppParamValue, &entry); err != nil {
		t.Fatalf("stored value is not a secret entry JSON: %v", err)
	}
	if entry.CT == "" {
		t.Error("ct must not be empty")
	}
	if entry.Hint != "3456" {
		t.Errorf("hint: want %q, got %q", "3456", entry.Hint)
	}

	// Round-trip via GetPlaintextAppParams.
	// Seed the fake DAL with the stored value so GetAppParams returns it.
	raw, _ := json.Marshal(map[string]json.RawMessage{"geoapify_key": d.setAppParamValue})
	d.providerKeysRaw = raw

	plain, err := svc.GetPlaintextAppParams(context.Background(), "tenant-1", "app-1")
	if err != nil {
		t.Fatalf("GetPlaintextAppParams: %v", err)
	}
	if plain["geoapify_key"] != "key123456" {
		t.Errorf("plaintext: want %q, got %q", "key123456", plain["geoapify_key"])
	}
}

// AGP-2: SetAppParam non-secret → stored as plain JSON string.
func TestAppParam_NonSecretStoredAsString(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil)

	err := svc.SetAppParam(context.Background(), "tenant-1", "app-1", "target_city",
		service.AppGlobalParamUpsertInput{Value: "Tel Aviv", Type: "string"})
	if err != nil {
		t.Fatalf("SetAppParam: %v", err)
	}

	var s string
	if err := json.Unmarshal(d.setAppParamValue, &s); err != nil {
		t.Fatalf("non-secret must be stored as JSON string: %v", err)
	}
	if s != "Tel Aviv" {
		t.Errorf("value: want %q, got %q", "Tel Aviv", s)
	}
}

// AGP-3: SetAppParam bad name → ErrValidation.
func TestAppParam_BadName(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	err := svc.SetAppParam(context.Background(), "t1", "app-1", "Bad Name",
		service.AppGlobalParamUpsertInput{Value: "x", Type: "string"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

// AGP-4: SetAppParam bad type → ErrUnprocessable.
func TestAppParam_BadType(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	err := svc.SetAppParam(context.Background(), "t1", "app-1", "my_key",
		service.AppGlobalParamUpsertInput{Value: "x", Type: "binary"})
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable, got %v", err)
	}
}

// AGP-5: GetAppParams for stored secret → IsSet=true, ValueHint set, Value empty.
func TestAppParam_GetSecretMasked(t *testing.T) {
	entry, _ := json.Marshal(map[string]any{
		"ct": "plain:sk-supersecret", "hint": "cret",
	})
	raw, _ := json.Marshal(map[string]json.RawMessage{"api_key": entry})
	d := &fakeDal{providerKeysRaw: raw}
	svc := service.NewAppService(d, nil, nil)

	params, err := svc.GetAppParams(context.Background(), "tenant-1", "app-1")
	if err != nil {
		t.Fatalf("GetAppParams: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("want 1 param, got %d", len(params))
	}
	p := params[0]
	if p.Name != "api_key" {
		t.Errorf("name: want api_key, got %q", p.Name)
	}
	if !p.IsSet {
		t.Error("IsSet must be true")
	}
	if p.ValueHint != "cret" {
		t.Errorf("ValueHint: want %q, got %q", "cret", p.ValueHint)
	}
	if p.Value != "" {
		t.Errorf("Value must be empty for secrets, got %q", p.Value)
	}
}

// AGP-6: GetAppParams for non-secret → IsSet=true, Value populated, ValueHint empty.
func TestAppParam_GetNonSecretValue(t *testing.T) {
	valRaw, _ := json.Marshal("Tel Aviv")
	raw, _ := json.Marshal(map[string]json.RawMessage{"city": valRaw})
	d := &fakeDal{providerKeysRaw: raw}
	svc := service.NewAppService(d, nil, nil)

	params, err := svc.GetAppParams(context.Background(), "tenant-1", "app-1")
	if err != nil {
		t.Fatalf("GetAppParams: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("want 1 param, got %d", len(params))
	}
	p := params[0]
	if p.Value != "Tel Aviv" {
		t.Errorf("Value: want %q, got %q", "Tel Aviv", p.Value)
	}
	if p.ValueHint != "" {
		t.Errorf("ValueHint must be empty for non-secrets, got %q", p.ValueHint)
	}
}

// AGP-7: DeleteAppParam calls DAL correctly.
func TestAppParam_Delete(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil)

	err := svc.DeleteAppParam(context.Background(), "tenant-1", "app-1", "api_key")
	if err != nil {
		t.Fatalf("DeleteAppParam: %v", err)
	}
	if !d.deleteAppParamCalled {
		t.Error("DAL DeleteAppParam must be called")
	}
}

// AGP-8: nil crypto key → plain: roundtrip works (test-mode encryption).
func TestAppParam_NilCryptoKey_Roundtrip(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, nil, nil) // nil key

	if err := svc.SetAppParam(context.Background(), "t1", "app-1", "k",
		service.AppGlobalParamUpsertInput{Value: "myvalue", Type: "secret"}); err != nil {
		t.Fatalf("SetAppParam: %v", err)
	}
	raw, _ := json.Marshal(map[string]json.RawMessage{"k": d.setAppParamValue})
	d.providerKeysRaw = raw

	plain, err := svc.GetPlaintextAppParams(context.Background(), "t1", "app-1")
	if err != nil {
		t.Fatalf("GetPlaintextAppParams: %v", err)
	}
	if plain["k"] != "myvalue" {
		t.Errorf("want %q, got %q", "myvalue", plain["k"])
	}
}
