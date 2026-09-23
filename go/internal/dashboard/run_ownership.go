package dashboard

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxRunOwnershipChecker implements RunOwnershipChecker against them.runs
// directly. Deliberately its own tiny query rather than depending on
// internal/admin/dal.DB — this package has no other DB dependency and one
// query doesn't justify pulling in the full admin DAL.
type PgxRunOwnershipChecker struct {
	pool *pgxpool.Pool
}

// NewPgxRunOwnershipChecker wraps a pgxpool.Pool (pass the same admin/RLS
// pool other Go services already use, e.g. rlsPools.Admin in cmd/them/main.go).
func NewPgxRunOwnershipChecker(pool *pgxpool.Pool) *PgxRunOwnershipChecker {
	return &PgxRunOwnershipChecker{pool: pool}
}

// RunBelongsToTenant reports whether runID exists and its tenant_id matches
// tenantID. Malformed UUIDs and not-found both return (false, nil) — never
// distinguish "invalid id" from "wrong tenant" to a caller.
func (c *PgxRunOwnershipChecker) RunBelongsToTenant(ctx context.Context, tenantID, runID string) (bool, error) {
	var exists bool
	err := c.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM them.runs WHERE id = $1::uuid AND tenant_id = $2::uuid)`,
		runID, tenantID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		// A malformed UUID surfaces as a driver error (invalid input syntax),
		// not pgx.ErrNoRows — treat the same way: refuse, don't propagate.
		return false, nil //nolint:nilerr // intentional: malformed input must not be distinguishable from "not found" to the caller
	}
	return exists, nil
}
