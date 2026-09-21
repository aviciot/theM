//go:build integration

package llmresolve

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/crypto"
)

// testFernetKey is the key used to encrypt llm_providers.api_key_encrypted
// fixtures in these tests. That column is always written via
// crypto.EncryptStored in production (internal/admin/service/llm_providers.go)
// — unlike applications.provider_keys, it never uses the "plain:" test-mode
// convention, so fixtures must use real ciphertext.
func testFernetKey() []byte {
	return crypto.DeriveKey("llmresolve-integration-test-secret")
}

func mustEncrypt(t *testing.T, plaintext string) string {
	t.Helper()
	enc, err := crypto.EncryptStored(testFernetKey(), plaintext)
	if err != nil {
		t.Fatalf("EncryptStored(%q): %v", plaintext, err)
	}
	return enc
}

// integrationDSN returns the test Postgres DSN from env or a sensible default.
func integrationDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable"
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), integrationDSN())
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

// setupTenantAndApp creates a tenant + application row for the test and
// registers cleanup. Returns (tenantID, applicationID).
func setupTenantAndApp(t *testing.T, pool *pgxpool.Pool, slugPrefix string) (string, string) {
	t.Helper()
	ctx := context.Background()

	var tenantID string
	err := pool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ($1,$2)
		 ON CONFLICT (slug) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id::text`, slugPrefix+"-tenant", "LLMResolve Test Tenant").Scan(&tenantID)
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}

	appSlug := slugPrefix + "-app-" + uuid.NewString()[:8]
	var appID string
	err = pool.QueryRow(ctx,
		`INSERT INTO them.applications (tenant_id, slug, name, provider_keys)
		 VALUES ($1::uuid, $2, $3, '{}') RETURNING id::text`,
		tenantID, appSlug, "LLMResolve Test App").Scan(&appID)
	if err != nil {
		t.Fatalf("insert application: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM them.applications WHERE id = $1::uuid`, appID)            //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM them.llm_providers WHERE tenant_id = $1::uuid`, tenantID) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM them.tenants WHERE id = $1::uuid`, tenantID)              //nolint:errcheck
	})
	return tenantID, appID
}

// TestResolveProvider_AppKeyWinsOverTenantAndPlatform verifies the app -> tenant
// -> platform precedence: when all three levels have a key for the same
// provider, the app-level provider_keys entry wins.
func TestResolveProvider_AppKeyWinsOverTenantAndPlatform(t *testing.T) {
	pool := integrationPool(t)
	tenantID, appID := setupTenantAndApp(t, pool, "inttest-llmr-prec")
	ctx := context.Background()

	// App-level key (plain: prefix — test mode, no crypto key needed).
	_, err := pool.Exec(ctx,
		`UPDATE them.applications SET provider_keys = $1::jsonb WHERE id = $2::uuid`,
		`{"testprov": {"ct": "plain:app-level-key"}}`, appID)
	if err != nil {
		t.Fatalf("set app provider_keys: %v", err)
	}

	// Tenant-level llm_providers row.
	_, err = pool.Exec(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, api_key_encrypted, tenant_id, enabled)
		 VALUES ('testprov','Test Provider','m',$1,$2::uuid,true)`,
		mustEncrypt(t, "tenant-level-key"), tenantID)
	if err != nil {
		t.Fatalf("insert tenant llm_providers row: %v", err)
	}

	r := New(pool, testFernetKey(), nil)
	resolved, err := r.ResolveProvider(ctx, appID, tenantID, "testprov")
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if resolved.Key != "app-level-key" {
		t.Errorf("want app-level key to win, got %q", resolved.Key)
	}
}

// TestResolveProvider_FallsBackToTenantWhenNoAppKey verifies that when the
// app has no key for the provider, the tenant-scoped llm_providers row is used.
func TestResolveProvider_FallsBackToTenantWhenNoAppKey(t *testing.T) {
	pool := integrationPool(t)
	tenantID, appID := setupTenantAndApp(t, pool, "inttest-llmr-tenant")
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, api_key_encrypted, tenant_id, enabled)
		 VALUES ('testprov2','Test Provider','m',$1,$2::uuid,true)`,
		mustEncrypt(t, "tenant-only-key"), tenantID)
	if err != nil {
		t.Fatalf("insert tenant llm_providers row: %v", err)
	}

	r := New(pool, testFernetKey(), nil)
	resolved, err := r.ResolveProvider(ctx, appID, tenantID, "testprov2")
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if resolved.Key != "tenant-only-key" {
		t.Errorf("want tenant-level key, got %q", resolved.Key)
	}
}

// TestResolveProvider_NoPlatformKeyFallback verifies that when neither the app
// nor the tenant has a key for a provider, the platform-default key is NOT
// used as a fallback. Tenants must configure their own keys. The resolved key
// must be empty; base_url and pricing from the platform row are still returned
// (metadata only — not the key).
func TestResolveProvider_NoPlatformKeyFallback(t *testing.T) {
	pool := integrationPool(t)
	tenantID, appID := setupTenantAndApp(t, pool, "inttest-llmr-plat")
	ctx := context.Background()

	const provider = "inttest-llmr-platform-provider"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.llm_providers WHERE name = $1 AND tenant_id IS NULL`, provider) //nolint:errcheck
	})
	_, err := pool.Exec(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, api_key_encrypted, base_url, model_pricing, tenant_id, enabled)
		 VALUES ($1,'Test Provider','m',$2,'https://api.example.com','{"m":{"input":1,"output":2}}'::jsonb,NULL,true)`,
		provider, mustEncrypt(t, "platform-key"))
	if err != nil {
		t.Fatalf("insert platform llm_providers row: %v", err)
	}

	r := New(pool, testFernetKey(), nil)
	resolved, err := r.ResolveProvider(ctx, appID, tenantID, provider)
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	// Platform key must NOT be used — tenant pays for their own LLM usage.
	if resolved.Key != "" {
		t.Errorf("want empty key (platform key must not fall back), got %q", resolved.Key)
	}
	// base_url and pricing from the platform row should still be available.
	if resolved.BaseURL != "https://api.example.com" {
		t.Errorf("want platform base_url in metadata, got %q", resolved.BaseURL)
	}
	if _, ok := resolved.Pricing["m"]; !ok {
		t.Error("want platform pricing metadata available")
	}
}

// TestAppProviderKey_CrossTenantAppID_ReturnsEmpty is the regression test for
// the missing tenant_id filter this change fixed: reading an app-level key
// by application ID alone (without checking the caller's own tenant owns
// that application) would let one tenant read another tenant's app key.
func TestAppProviderKey_CrossTenantAppID_ReturnsEmpty(t *testing.T) {
	pool := integrationPool(t)
	_, appID := setupTenantAndApp(t, pool, "inttest-llmr-xtenant-owner")
	otherTenantID, _ := setupTenantAndApp(t, pool, "inttest-llmr-xtenant-attacker")
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`UPDATE them.applications SET provider_keys = $1::jsonb WHERE id = $2::uuid`,
		`{"testprov3": {"ct": "plain:victim-key"}}`, appID)
	if err != nil {
		t.Fatalf("set app provider_keys: %v", err)
	}

	r := New(pool, nil, nil)
	key, err := r.AppProviderKey(ctx, appID, otherTenantID, "testprov3")
	if err != nil {
		t.Fatalf("AppProviderKey: %v", err)
	}
	if key != "" {
		t.Errorf("want empty key when applicationID belongs to a different tenant, got %q", key)
	}
}

// TestResolveProvider_PricingComesFromMatchedProviderRow verifies that
// model_pricing on the llm_providers row that supplied base_url/key is
// surfaced through Resolved.Pricing.
func TestResolveProvider_PricingComesFromMatchedProviderRow(t *testing.T) {
	pool := integrationPool(t)
	tenantID, appID := setupTenantAndApp(t, pool, "inttest-llmr-pricing")
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, api_key_encrypted, model_pricing, tenant_id, enabled)
		 VALUES ('testprov4','Test Provider','m','plain:some-key',$1::jsonb,$2::uuid,true)`,
		`{"my-model": {"input": 3.0, "output": 15.0}}`, tenantID)
	if err != nil {
		t.Fatalf("insert tenant llm_providers row with pricing: %v", err)
	}

	r := New(pool, nil, nil)
	resolved, err := r.ResolveProvider(ctx, appID, tenantID, "testprov4")
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	price, ok := resolved.Pricing["my-model"]
	if !ok {
		t.Fatal("want my-model present in resolved pricing")
	}
	if price.InputPerToken != 3.0/1e6 || price.OutputPerToken != 15.0/1e6 {
		t.Errorf("want per-token rates converted from per-million, got %+v", price)
	}
}
