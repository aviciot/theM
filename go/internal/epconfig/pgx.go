package epconfig

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier implements DBQuerier against a live pgxpool.Pool.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier wraps a pgxpool.Pool as a DBQuerier for epconfig resolution.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

// epConfigQuery is the single query that joins entry_points → applications.
// All columns used by EPConfig are selected here. NULLable columns are
// scanned into pointer types so the caller can apply defaults.
const epConfigQuery = `
SELECT
    ep.id::text,
    a.id::text,
    ep.slug,
    ep.entry_point_type,
    ep.enabled,
    ep.max_concurrent_sessions,
    ep.queue_timeout_seconds,
    COALESCE(ep.access_policy, '{"mode":"token"}')::text,
    a.enabled,
    COALESCE(a.runtime_config, '{}')::text
FROM them.entry_points ep
JOIN them.applications a ON a.id = ep.application_id
WHERE ep.slug = $1
LIMIT 1`

// QueryEPConfig fetches one EPConfigRow for the given slug.
// Returns ErrNotFound (wrapped) when no row is found.
func (q *PgxQuerier) QueryEPConfig(ctx context.Context, epSlug string) (*EPConfigRow, error) {
	var row EPConfigRow
	var accessPolicyText string
	var runtimeConfigText string

	err := q.pool.QueryRow(ctx, epConfigQuery, epSlug).Scan(
		&row.EPID,
		&row.AppID,
		&row.EPSlug,
		&row.EPType,
		&row.EPEnabled,
		&row.EPMaxConcurrentSessions,
		&row.EPQueueTimeoutSeconds,
		&accessPolicyText,
		&row.AppEnabled,
		&runtimeConfigText,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: slug=%s", ErrNotFound, epSlug)
		}
		return nil, fmt.Errorf("epconfig: query: %w", err)
	}

	row.AccessPolicyJSON = []byte(accessPolicyText)
	row.AppRuntimeConfigJSON = []byte(runtimeConfigText)
	return &row, nil
}
