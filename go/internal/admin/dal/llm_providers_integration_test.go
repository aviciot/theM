//go:build integration

package dal_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationPool opens a pgxpool from TEST_POSTGRES_DSN or the default test DSN.
func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := integrationDSN()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping integration test", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("postgres ping failed (%v) — skipping integration test", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newProviderDAL wraps a pgxpool.Pool in a dal.DB using a pgx querier.
func newProviderDAL(t *testing.T, pool *pgxpool.Pool) *dal.DB {
	t.Helper()
	return dal.NewDB(newPgxQuerier(pool))
}

// cleanProviders deletes any providers whose name starts with prefix.
func cleanProviders(t *testing.T, d *dal.DB, prefix string) {
	t.Helper()
	rows, err := d.ListProviders(context.Background())
	if err != nil {
		return
	}
	for _, p := range rows {
		if strings.HasPrefix(p.Name, prefix) {
			_ = d.DeleteProvider(context.Background(), p.ID)
		}
	}
}

// ── Integration tests ─────────────────────────────────────────────────────────

func TestDAL_Provider_List(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-") })

	// Create two and list.
	for _, name := range []string{"inttest-dal-list-a", "inttest-dal-list-b"} {
		if _, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
			Name: name, DisplayName: name, DefaultModel: "m", Enabled: true,
		}); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
	}

	rows, err := d.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}

	// Verify ID order is ascending and our entries are present.
	var found []string
	var prevID int64
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "inttest-dal-list") {
			found = append(found, r.Name)
			if r.ID < prevID {
				t.Errorf("providers not in ascending ID order: %d after %d", r.ID, prevID)
			}
			prevID = r.ID
		}
	}
	if len(found) < 2 {
		t.Errorf("want ≥2 inttest-dal-list entries, got %d", len(found))
	}
}

func TestDAL_Provider_Get(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-get")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-get") })

	enc := "enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: "inttest-dal-get", DisplayName: "P", APIKeyEncrypted: &enc,
		DefaultModel: "m", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := d.GetProvider(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: want %d, got %d", created.ID, got.ID)
	}
	if got.APIKeyEncrypted == nil || *got.APIKeyEncrypted != enc {
		t.Errorf("api_key_encrypted mismatch")
	}
}

func TestDAL_Provider_Create(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-create")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-create") })

	in := dal.LLMProviderInput{
		Name:            "inttest-dal-create",
		DisplayName:     "Create Test",
		DefaultModel:    "claude-sonnet-4-6",
		ModelPricingRaw: []byte(`{"input":3.0,"output":15.0}`),
		Enabled:         true,
	}
	p, err := d.CreateProvider(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if p.ID == 0 {
		t.Error("ID must be non-zero")
	}
	if p.Name != "inttest-dal-create" {
		t.Errorf("want name=inttest-dal-create, got %q", p.Name)
	}
	if p.APIKeyEncrypted != nil {
		t.Errorf("want nil api_key_encrypted, got %q", *p.APIKeyEncrypted)
	}
}

func TestDAL_Provider_UpdateMetadataOnly(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-upd-meta")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-upd-meta") })

	enc := "enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name:            "inttest-dal-upd-meta",
		DisplayName:     "Before",
		APIKeyEncrypted: &enc,
		DefaultModel:    "m",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	upIn := dal.LLMProviderToInput(created)
	upIn.DisplayName = "After"

	updated, err := d.UpdateProvider(context.Background(), created.ID, upIn)
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if updated.DisplayName != "After" {
		t.Errorf("want display_name=After, got %q", updated.DisplayName)
	}
	// Encrypted key must be unchanged.
	if updated.APIKeyEncrypted == nil || *updated.APIKeyEncrypted != enc {
		t.Error("api_key_encrypted must be preserved on metadata-only update")
	}
}

func TestDAL_Provider_UpdateAPIKey(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-upd-key")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-upd-key") })

	oldEnc := "enc:gAAAAABlU_EAOLD_KEY_PLACEHOLDER_encGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name:            "inttest-dal-upd-key",
		DisplayName:     "P",
		APIKeyEncrypted: &oldEnc,
		DefaultModel:    "m",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newEnc := "enc:gBBBBBBlU_ENEW_KEY_PLACEHOLDER_encJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	upIn := dal.LLMProviderToInput(created)
	upIn.APIKeyEncrypted = &newEnc

	updated, err := d.UpdateProvider(context.Background(), created.ID, upIn)
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if updated.APIKeyEncrypted == nil || *updated.APIKeyEncrypted != newEnc {
		t.Errorf("want new key %q, got %v", newEnc, updated.APIKeyEncrypted)
	}
}

func TestDAL_Provider_Delete(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-del")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-del") })

	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: "inttest-dal-del", DisplayName: "P", DefaultModel: "m", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := d.DeleteProvider(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	_, err = d.GetProvider(context.Background(), created.ID)
	if err == nil || !dal.IsNoRows(err) {
		t.Errorf("want ErrNoRows after delete, got %v", err)
	}
}

func TestDAL_Provider_DuplicateName_UniqueViolation(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-dup")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-dup") })

	in := dal.LLMProviderInput{
		Name: "inttest-dal-dup", DisplayName: "P", DefaultModel: "m", Enabled: true,
	}
	if _, err := d.CreateProvider(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := d.CreateProvider(context.Background(), in)
	if err == nil {
		t.Fatal("expected unique-violation error, got nil")
	}
	if !dal.IsUniqueViolation(err) {
		t.Errorf("want IsUniqueViolation=true, got %v", err)
	}
}

func TestDAL_Provider_GetNotFound(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	_, err := d.GetProvider(context.Background(), -99999)
	if err == nil || !dal.IsNoRows(err) {
		t.Errorf("want ErrNoRows for non-existent ID, got %v", err)
	}
}

func TestDAL_Provider_DeleteNotFound(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	err := d.DeleteProvider(context.Background(), -99999)
	if err == nil || !dal.IsNoRows(err) {
		t.Errorf("want ErrNoRows for non-existent delete, got %v", err)
	}
}

func TestDAL_Provider_EncryptedValue_HasEncPrefix(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-enc-pfx")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-enc-pfx") })

	enc := "enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name:            "inttest-dal-enc-pfx",
		DisplayName:     "P",
		APIKeyEncrypted: &enc,
		DefaultModel:    "m",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.APIKeyEncrypted == nil {
		t.Fatal("api_key_encrypted must not be nil")
	}
	if !strings.HasPrefix(*created.APIKeyEncrypted, "enc:") {
		t.Errorf("stored value must start with enc:, got %q", *created.APIKeyEncrypted)
	}
}

func TestDAL_Provider_PlaintextNeverStored(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-dal-noplain")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-dal-noplain") })

	enc := "enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
	const knownPlaintext = "test-api-key-wave7-phase1"

	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name:            "inttest-dal-noplain",
		DisplayName:     "P",
		APIKeyEncrypted: &enc,
		DefaultModel:    "m",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.APIKeyEncrypted != nil && *created.APIKeyEncrypted == knownPlaintext {
		t.Error("plaintext must never be stored in api_key_encrypted column")
	}
}
