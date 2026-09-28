//go:build integration

package dal_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
)

// docs/APP_EXPORT_IMPORT_INVESTIGATION.md §5: DeployApplication previously
// copied applications.app_params verbatim, including secret entries (a JSON
// object carrying a "ct" ciphertext key, db/045_app_global_params.sql) —
// leaking encrypted secret material into the target tenant on every deploy.
// Fixed by filtering app_params inside the CTE itself (jsonb_object_agg over
// jsonb_each, dropping any entry whose value is an object with a "ct" key).
// These tests prove the filter against real Postgres, since a fake Querier
// cannot execute Postgres-specific jsonb functions.

func TestDAL_DeployApplication_StripsSecretAppParams(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	srcTenantID, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-target")

	appParams := map[string]any{
		"geoapify_key": map[string]any{"ct": "enc:some-ciphertext", "hint": "AB12"},
		"region":       "us-east-1",
		"max_retries":  3,
	}
	raw, err := json.Marshal(appParams)
	if err != nil {
		t.Fatalf("marshal app_params: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`UPDATE them.applications SET app_params = $2::jsonb WHERE id = $1::uuid RETURNING id`,
		srcAppID, raw).Scan(new(string)); err != nil {
		t.Fatalf("seed app_params: %v", err)
	}

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)
	deployed, err := d.DeployApplication(ctx, srcAppID, targetTenantID, nil)
	if err != nil {
		t.Fatalf("DeployApplication: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, deployed.ID) //nolint:errcheck
	})

	var gotRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT app_params FROM them.applications WHERE id = $1::uuid`, deployed.ID,
	).Scan(&gotRaw); err != nil {
		t.Fatalf("read deployed app_params: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal deployed app_params: %v", err)
	}

	if _, ok := got["geoapify_key"]; ok {
		t.Errorf("secret entry %q was copied to the target tenant — app_params = %v", "geoapify_key", got)
	}
	if got["region"] != "us-east-1" {
		t.Errorf("non-secret entry %q = %v, want %q preserved", "region", got["region"], "us-east-1")
	}
	if v, ok := got["max_retries"].(float64); !ok || v != 3 {
		t.Errorf("non-secret entry %q = %v, want %v preserved", "max_retries", got["max_retries"], 3)
	}

	_ = srcTenantID // only needed to construct the source app; not asserted directly
}

func TestDAL_DeployApplication_EmptyAppParams_ProducesEmptyObject(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	_, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-empty-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-empty-target")

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)
	deployed, err := d.DeployApplication(ctx, srcAppID, targetTenantID, nil)
	if err != nil {
		t.Fatalf("DeployApplication: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, deployed.ID) //nolint:errcheck
	})

	var gotRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT app_params FROM them.applications WHERE id = $1::uuid`, deployed.ID,
	).Scan(&gotRaw); err != nil {
		t.Fatalf("read deployed app_params: %v", err)
	}
	if string(gotRaw) != "{}" {
		t.Errorf("app_params for a source app with no params = %s, want {}", gotRaw)
	}
}

func TestDAL_DeployApplication_AllSecretAppParams_ProducesEmptyObject(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	_, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-allsecret-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-deploy-secrets-allsecret-target")

	appParams := map[string]any{
		"api_key":    map[string]any{"ct": "enc:aaa", "hint": "1111"},
		"other_key":  map[string]any{"ct": "enc:bbb", "hint": "2222"},
	}
	raw, err := json.Marshal(appParams)
	if err != nil {
		t.Fatalf("marshal app_params: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`UPDATE them.applications SET app_params = $2::jsonb WHERE id = $1::uuid RETURNING id`,
		srcAppID, raw).Scan(new(string)); err != nil {
		t.Fatalf("seed app_params: %v", err)
	}

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)
	deployed, err := d.DeployApplication(ctx, srcAppID, targetTenantID, nil)
	if err != nil {
		t.Fatalf("DeployApplication: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, deployed.ID) //nolint:errcheck
	})

	var gotRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT app_params FROM them.applications WHERE id = $1::uuid`, deployed.ID,
	).Scan(&gotRaw); err != nil {
		t.Fatalf("read deployed app_params: %v", err)
	}
	if string(gotRaw) != "{}" {
		t.Errorf("app_params for a source app with only secret entries = %s, want {} (all stripped)", gotRaw)
	}
}
