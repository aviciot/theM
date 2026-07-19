// Package db provides a pgx connection pool and helper methods for the
// application to interact with PostgreSQL. It owns the pool lifecycle and
// exposes Ping / Close for health checks and graceful shutdown.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
