//go:build integration

package dal_test

import (
	"os"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationDSN returns the test Postgres DSN from env or a sensible default.
func integrationDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable"
}

// newPgxQuerier wraps a pgxpool.Pool in admin.NewPgxQuerier (the canonical adapter).
func newPgxQuerier(pool *pgxpool.Pool) dal.Querier {
	return admin.NewPgxQuerier(pool)
}
