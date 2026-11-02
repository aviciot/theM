// Package dbversion checks at startup that the database is not behind the code (backlog F-135).
//
// Migrations (db/NNN_*.sql) are applied by hand. db/125 was committed together with the code that writes
// gateway_requests.user_id but never applied, so every gateway usage write failed with a one-line warning
// for days. Each migration from 127 on records itself in them.schema_migrations; Expected names the newest
// one this code needs, and a service that finds it missing says so loudly at startup.
package dbversion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Expected is the newest migration this code requires (the file name without .sql). Bump it with every new
// db/NNN_*.sql; TestExpectedMatchesNewestMigration fails when it lags the newest file.
const Expected = "128_retention_grants"

// ErrBehind is returned (by Enforce, only when THE_M_REQUIRE_MIGRATIONS=true) when Expected is not recorded.
var ErrBehind = errors.New("database schema is behind the code")

// Recorded reports whether a migration version is present in them.schema_migrations.
type Recorded func(ctx context.Context, version string) (bool, error)

// Verify logs the outcome and returns ErrBehind when Expected is not recorded. A failure to check (missing
// table, permissions, connection) is logged and returns nil: the check must never be what takes a service down.
func Verify(ctx context.Context, recorded Recorded, log *slog.Logger) error {
	ok, err := recorded(ctx, Expected)
	if err != nil {
		log.Warn("could not verify the database schema version", "expected_migration", Expected, "error", err)
		return nil
	}
	if !ok {
		log.Error("database schema is BEHIND the code: the newest migration is not recorded in them.schema_migrations — "+
			"apply it before relying on this deployment (docs/guides/database-migrations.md)",
			"missing_migration", Expected, "file", "db/"+Expected+".sql")
		return fmt.Errorf("%w: migration %s is not recorded", ErrBehind, Expected)
	}
	log.Info("database schema is current", "migration", Expected)
	return nil
}

// Enforce checks the schema using pool. It returns an error only when the schema is behind AND
// THE_M_REQUIRE_MIGRATIONS=true; otherwise it just logs (so a stale dev database still starts).
func Enforce(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	err := Verify(ctx, func(ctx context.Context, version string) (bool, error) {
		var ok bool
		e := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM them.schema_migrations WHERE version = $1)`, version).Scan(&ok)
		return ok, e
	}, log)
	return gate(err)
}

// gate turns a "behind" result into a startup failure only when THE_M_REQUIRE_MIGRATIONS=true.
func gate(err error) error {
	if err != nil && strings.EqualFold(os.Getenv("THE_M_REQUIRE_MIGRATIONS"), "true") {
		return err
	}
	return nil
}
