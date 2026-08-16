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
// The WHERE clause filters by BOTH tenant_id AND slug (tenant-safe resolution,
// migration 028). This prevents one tenant's EP shadowing another tenant's EP
// with the same slug. The UNIQUE(tenant_id, slug) DB constraint guarantees the
// query returns at most one row.
//
// The LEFT JOIN on app_orchestrators means ao.id and ao.name are NULL when
// entry_points.app_orchestrator_id IS NULL (unbound EP). Handlers must treat
// an empty OrchestratorName as a configuration error — they must NOT fall back
// to using the EP slug as the orchestrator name (SEC-04).
//
// The JOIN on app_orchestrators also implicitly verifies that the bound
// orchestrator belongs to the same application as the entry point, because
// app_orchestrators.application_id must equal ep.application_id.
const epConfigQuery = `
SELECT
    ep.id::text,
    a.id::text,
    ep.tenant_id::text,
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
WHERE ep.tenant_id = $1::uuid
  AND ep.slug = $2
LIMIT 1`

// QueryEPConfig fetches one EPConfigRow for the given tenant ID and slug.
// Returns ErrNotFound (wrapped) when no matching row exists.
func (q *PgxQuerier) QueryEPConfig(ctx context.Context, tenantID, epSlug string) (*EPConfigRow, error) {
	var row EPConfigRow
	var accessPolicyText string
	var runtimeConfigText string

	err := q.pool.QueryRow(ctx, epConfigQuery, tenantID, epSlug).Scan(
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
