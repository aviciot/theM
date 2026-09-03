// Package db provides pgx connection pools and helper methods for the
// application to interact with PostgreSQL. It owns the pool lifecycle and
// exposes Ping / Close for health checks and graceful shutdown.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/dbtype"
)

// DB wraps a pgxpool.Pool with application-level helpers.
type DB struct {
	pool *pgxpool.Pool
}

// New creates and validates a new pgx connection pool from the given DSN.
// It calls pool.Ping to confirm the database is reachable before returning.
func New(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: initial ping failed: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Ping checks that at least one database connection is healthy.
// It is called by the readiness handler on every health check request.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool for callers that need to run
// queries directly. The returned pool must not be closed by the caller.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Close releases all connections in the pool. It should be called during
// graceful shutdown after the HTTP server has stopped accepting new requests.
func (d *DB) Close() {
	d.pool.Close()
}

// ── RLS pool infrastructure ───────────────────────────────────────────────────

// Compile-time interface assertions.
var _ dbtype.TenantQuerier = (*TenantTx)(nil)
var _ dbtype.AdminQuerier = (*AdminTx)(nil)
var _ dbtype.AdminQuerier = (*adminQuerier)(nil)

// Pools holds the two connection pools required for RLS enforcement:
//   - App: connected as them_app (no BYPASSRLS) — for tenant-scoped operations
//   - Admin: connected as them_admin (BYPASSRLS) — for cross-tenant/admin operations
type Pools struct {
	App   *pgxpool.Pool // them_app role — no BYPASSRLS; tenant-scoped ops
	Admin *pgxpool.Pool // them_admin role — BYPASSRLS; admin/platform/cross-tenant ops
}

// NewPools creates and validates both the App and Admin connection pools.
// appDSN must connect as them_app (no BYPASSRLS).
// adminDSN must connect as them_admin (BYPASSRLS).
func NewPools(ctx context.Context, appDSN, adminDSN string) (*Pools, error) {
	appCfg, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse app pool config: %w", err)
	}
	appPool, err := pgxpool.NewWithConfig(ctx, appCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create app pool: %w", err)
	}
	if err := appPool.Ping(ctx); err != nil {
		appPool.Close()
		return nil, fmt.Errorf("db: app pool ping failed: %w", err)
	}

	adminCfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		appPool.Close()
		return nil, fmt.Errorf("db: parse admin pool config: %w", err)
	}
	adminPool, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		appPool.Close()
		return nil, fmt.Errorf("db: create admin pool: %w", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		appPool.Close()
		adminPool.Close()
		return nil, fmt.Errorf("db: admin pool ping failed: %w", err)
	}

	return &Pools{App: appPool, Admin: adminPool}, nil
}

// Close releases all connections in both pools.
func (p *Pools) Close() {
	p.Admin.Close()
	p.App.Close()
}

// BeginTenantTx acquires a connection from the App pool and sets app.tenant_id
// for the duration of the transaction. tenantID must come from JWT claims only.
//
// Call-site pattern:
//
//	tx, err := pools.BeginTenantTx(ctx, tenantID)
//	if err != nil { return err }
//	defer func() {
//	    cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	    defer cancel()
//	    tx.Rollback(cleanupCtx)
//	}()
//	// ... DAL calls ...
//	return tx.Commit(ctx)
func (p *Pools) BeginTenantTx(ctx context.Context, tenantID uuid.UUID) (*TenantTx, error) {
	pgTx, err := p.App.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: begin tenant tx: %w", err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pgTx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		_ = pgTx.Rollback(cleanupCtx)
		return nil, fmt.Errorf("db: set tenant id: %w", err)
	}
	return &TenantTx{tx: pgTx}, nil
}

// BeginAdminTx acquires a connection from the Admin pool (BYPASSRLS) and
// begins a transaction. No app.tenant_id is set.
func (p *Pools) BeginAdminTx(ctx context.Context) (*AdminTx, error) {
	pgTx, err := p.Admin.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: begin admin tx: %w", err)
	}
	return &AdminTx{tx: pgTx}, nil
}

// NewAdminQuerier returns a non-transactional AdminQuerier backed by the Admin pool.
func (p *Pools) NewAdminQuerier() dbtype.AdminQuerier {
	return &adminQuerier{pool: p.Admin}
}

// ── TenantTx ─────────────────────────────────────────────────────────────────

// TenantTx wraps pgx.Tx from the App pool. Produced only by Pools.BeginTenantTx.
// It implements dbtype.TenantQuerier — DAL functions for tenant-scoped ops accept this.
type TenantTx struct{ tx pgx.Tx }

func (t *TenantTx) IsTenantQuerier() struct{} { return struct{}{} }

func (t *TenantTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}
func (t *TenantTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}
func (t *TenantTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}
func (t *TenantTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *TenantTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// ── AdminTx ──────────────────────────────────────────────────────────────────

// AdminTx wraps pgx.Tx from the Admin pool. Produced only by Pools.BeginAdminTx.
// It implements dbtype.AdminQuerier.
type AdminTx struct{ tx pgx.Tx }

func (a *AdminTx) IsAdminQuerier() struct{} { return struct{}{} }

func (a *AdminTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.tx.Query(ctx, sql, args...)
}
func (a *AdminTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.tx.QueryRow(ctx, sql, args...)
}
func (a *AdminTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return a.tx.Exec(ctx, sql, args...)
}
func (a *AdminTx) Commit(ctx context.Context) error   { return a.tx.Commit(ctx) }
func (a *AdminTx) Rollback(ctx context.Context) error { return a.tx.Rollback(ctx) }

// ── adminQuerier ─────────────────────────────────────────────────────────────

// adminQuerier wraps the Admin pool for non-transactional admin queries.
type adminQuerier struct{ pool *pgxpool.Pool }

func (a *adminQuerier) IsAdminQuerier() struct{} { return struct{}{} }

func (a *adminQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.pool.Query(ctx, sql, args...)
}
func (a *adminQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.pool.QueryRow(ctx, sql, args...)
}
func (a *adminQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return a.pool.Exec(ctx, sql, args...)
}
