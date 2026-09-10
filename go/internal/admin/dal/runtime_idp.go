package dal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TenantRuntimeIDP holds the per-tenant external JWT validation configuration
// stored in them.tenant_runtime_config.
// This is distinct from TenantIDPConfig (employee SSO for dashboard login).
type TenantRuntimeIDP struct {
	TenantID  string
	JWKSUri   string
	Issuer    string
	Audience  string // empty = skip aud validation
	SubClaim  string // default "sub"
	UpdatedAt time.Time
}

const getRuntimeIDPQuery = `
SELECT tenant_id::text, jwks_uri, issuer, COALESCE(audience,''), COALESCE(sub_claim,'sub'), updated_at
  FROM them.tenant_runtime_config
 WHERE tenant_id = $1::uuid`

// GetTenantRuntimeIDP fetches the runtime IDP config for a tenant.
// Returns ErrNotFound (wrapped) when no row exists for the tenant.
func (d *DB) GetTenantRuntimeIDP(ctx context.Context, tenantID string) (*TenantRuntimeIDP, error) {
	row := d.q.QueryRow(ctx, getRuntimeIDPQuery, tenantID)
	var r TenantRuntimeIDP
	err := row.Scan(&r.TenantID, &r.JWKSUri, &r.Issuer, &r.Audience, &r.SubClaim, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("dal: GetTenantRuntimeIDP: %w", err)
	}
	return &r, nil
}

const upsertRuntimeIDPQuery = `
INSERT INTO them.tenant_runtime_config (tenant_id, jwks_uri, issuer, audience, sub_claim, updated_at)
VALUES ($1::uuid, $2, $3, NULLIF($4,''), NULLIF($5,''), now())
ON CONFLICT (tenant_id) DO UPDATE
   SET jwks_uri   = EXCLUDED.jwks_uri,
       issuer     = EXCLUDED.issuer,
       audience   = EXCLUDED.audience,
       sub_claim  = EXCLUDED.sub_claim,
       updated_at = now()
RETURNING tenant_id::text, jwks_uri, issuer, COALESCE(audience,''), COALESCE(sub_claim,'sub'), updated_at`

// UpsertTenantRuntimeIDP creates or replaces the runtime IDP config for a tenant.
func (d *DB) UpsertTenantRuntimeIDP(ctx context.Context, tenantID string, in TenantRuntimeIDP) (*TenantRuntimeIDP, error) {
	subClaim := in.SubClaim
	if subClaim == "" {
		subClaim = "sub"
	}
	row := d.q.ExecReturning(ctx, upsertRuntimeIDPQuery,
		tenantID, in.JWKSUri, in.Issuer, in.Audience, subClaim)
	var r TenantRuntimeIDP
	if err := row.Scan(&r.TenantID, &r.JWKSUri, &r.Issuer, &r.Audience, &r.SubClaim, &r.UpdatedAt); err != nil {
		return nil, fmt.Errorf("dal: UpsertTenantRuntimeIDP: %w", err)
	}
	return &r, nil
}

const deleteRuntimeIDPQuery = `
DELETE FROM them.tenant_runtime_config WHERE tenant_id = $1::uuid`

// DeleteTenantRuntimeIDP removes the runtime IDP config for a tenant.
// No error if the row does not exist.
func (d *DB) DeleteTenantRuntimeIDP(ctx context.Context, tenantID string) error {
	if err := d.q.Exec(ctx, deleteRuntimeIDPQuery, tenantID); err != nil {
		return fmt.Errorf("dal: DeleteTenantRuntimeIDP: %w", err)
	}
	return nil
}
