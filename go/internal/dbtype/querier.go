// Package dbtype defines the base query-execution interfaces used across the
// db, dal, and handler layers. It has no imports beyond the standard library
// and pgx — nothing in this repo imports it circularly.
package dbtype

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the base query-execution interface. Implemented by *pgxpool.Pool,
// pgx.Tx, and any test fake.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TenantQuerier marks a Querier that has had app.tenant_id set for the current
// transaction via db.Pools.BeginTenantTx. DAL functions for tenant-scoped
// operations accept this type.
type TenantQuerier interface {
	Querier
	IsTenantQuerier() struct{} // compile-time marker; prevents AdminQuerier from satisfying this
}

// AdminQuerier marks a Querier backed by the BYPASSRLS admin pool.
// Only db.Pools.NewAdminQuerier and db.Pools.BeginAdminTx produce one.
// DAL functions for cross-tenant/admin operations accept this type.
type AdminQuerier interface {
	Querier
	IsAdminQuerier() struct{} // compile-time marker; prevents TenantQuerier from satisfying this
}
