package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/transport"
)

// RuntimeIDPLoaderAdapter adapts dal.DB to the transport.RuntimeIDPLoader interface.
// Used by Lifecycle to load per-tenant external JWT config at admission time.
type RuntimeIDPLoaderAdapter struct {
	db *dal.DB
}

// NewRuntimeIDPLoader wraps a dal.DB as a transport.RuntimeIDPLoader.
func NewRuntimeIDPLoader(db *dal.DB) *RuntimeIDPLoaderAdapter {
	return &RuntimeIDPLoaderAdapter{db: db}
}

// GetTenantRuntimeIDP fetches and converts the dal row to transport.RuntimeIDPConfig.
// Returns a wrapped pgx.ErrNoRows when no config exists for the tenant.
func (a *RuntimeIDPLoaderAdapter) GetTenantRuntimeIDP(ctx context.Context, tenantID string) (*transport.RuntimeIDPConfig, error) {
	row, err := a.db.GetTenantRuntimeIDP(ctx, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	subClaim := row.SubClaim
	if subClaim == "" {
		subClaim = "sub"
	}
	return &transport.RuntimeIDPConfig{
		JWKSUri:  row.JWKSUri,
		Issuer:   row.Issuer,
		Audience: row.Audience,
		SubClaim: subClaim,
	}, nil
}
