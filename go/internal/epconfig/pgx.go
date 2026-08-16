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

// epConfigQuery joins entry_points → applications → app_orchestrators (LEFT JOIN).
// The LEFT JOIN on app_orchestrators means ao.id and ao.name are NULL when
// entry_points.app_orchestrator_id IS NULL (unbound EP). Handlers must treat
// an empty OrchestratorName as a configuration error — they must NOT fall back
// to using the EP slug as the orchestrator name (SEC-04).
//
// The JOIN on app_orchestrators also implicitly verifies that the bound
// orchestrator belongs to the same application as the entry point, because
// app_orchestrators.application_id must equal ep.application_id. This is
// enforced by the FK constraint, but we add the explicit join condition for
// clarity and defence-in-depth.
const epConfigQuery = `
SELECT
    ep.id::text,
    a.id::text,
    COALESCE(a.tenant_id::text, ''),
    ep.slug,
    ep.entry_point_type,
    ep.enabled,
    ep.max_concurrent_sessions,
    ep.queue_timeout_seconds,
    COALESCE(ep.access_policy, '{"mode":"token"}')::text,
    a.enabled,
    COALESCE(a.runtime_config, '{}')::text,
    ep.app_orchestrator_id::text,
    ao.name
FROM them.entry_points ep
JOIN them.applications a ON a.id = ep.application_id
LEFT JOIN them.app_orchestrators ao
    ON ao.id = ep.app_orchestrator_id
   AND ao.application_id = ep.application_id
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
		&row.TenantID,
		&row.EPSlug,
		&row.EPType,
		&row.EPEnabled,
		&row.EPMaxConcurrentSessions,
		&row.EPQueueTimeoutSeconds,
		&accessPolicyText,
		&row.AppEnabled,
		&runtimeConfigText,
		&row.AppOrchestratorID,
		&row.OrchestratorName,
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
