package events

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements EventStore using the existing Postgres schema.
// This is a **copy** of today's gateway-request queries moved behind the interface —
// the live handler keeps using its own code until migration (Stage B).
// No RLS here — all queries filter tenant_id explicitly (design §7.1).
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a Postgres-backed event store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// ListGatewayRequests queries them.gateway_requests.
func (p *PostgresStore) ListGatewayRequests(ctx context.Context, tenantID string, filter GatewayRequestFilter) ([]GatewayRequestEvent, error) {
	// Build dynamic query based on filter
	q := `
		SELECT
			id::text,
			COALESCE(client_id::text, '') AS client_id,
			provider,
			model_requested,
			model_served,
			status,
			http_status,
			COALESCE(error_code, '') AS error_code,
			tokens_in,
			tokens_out,
			cost_usd,
			COALESCE(latency_ms, 0) AS latency_ms,
			COALESCE(ttfb_ms, 0) AS ttfb_ms,
			streamed,
			created_at
		FROM them.gateway_requests
		WHERE tenant_id = $1::uuid`

	args := []interface{}{tenantID}
	argIdx := 2

	if filter.ApplicationID != "" {
		q += fmt.Sprintf(" AND application_id = $%d::uuid", argIdx)
		args = append(args, filter.ApplicationID)
		argIdx++
	}
	if filter.ClientID != "" {
		q += fmt.Sprintf(" AND client_id = $%d::uuid", argIdx)
		args = append(args, filter.ClientID)
		argIdx++
	}
	if filter.Status != "" {
		q += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, filter.Status)
		argIdx++
	}
	if !filter.StartTime.IsZero() {
		q += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, filter.StartTime)
		argIdx++
	}
	if !filter.EndTime.IsZero() {
		q += fmt.Sprintf(" AND created_at < $%d", argIdx)
		args = append(args, filter.EndTime)
		argIdx++
	}

	q += " ORDER BY created_at DESC"

	limit := filter.Limit
	if limit == 0 {
		limit = 100 // default
	}
	if limit > 1000 {
		limit = 1000 // max
	}
	q += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)
	argIdx++

	if filter.Offset > 0 {
		q += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list gateway requests: %w", err)
	}
	defer rows.Close()

	var results []GatewayRequestEvent
	for rows.Next() {
		var e GatewayRequestEvent
		var requestID string
		var httpStatus int
		var createdAt time.Time

		err := rows.Scan(
			&requestID,
			&e.ClientID,
			&e.Provider,
			&e.ModelRequested,
			&e.ModelServed,
			&e.Status,
			&httpStatus,
			&e.ErrorCode,
			&e.TokensIn,
			&e.TokensOut,
			&e.CostUSD,
			&e.LatencyMS,
			&e.TTFBMS,
			&e.Streamed,
			&createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan gateway request: %w", err)
		}

		e.RequestID = requestID
		e.HTTPStatus = uint16(httpStatus)
		// ApplicationID and UserID not in current gateway_requests schema — will be added in migration
		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate gateway requests: %w", err)
	}

	return results, nil
}

// GetGatewayRequestByID fetches a single gateway request by ID.
func (p *PostgresStore) GetGatewayRequestByID(ctx context.Context, tenantID string, requestID string) (*GatewayRequestEvent, error) {
	q := `
		SELECT
			id::text,
			COALESCE(client_id::text, '') AS client_id,
			provider,
			model_requested,
			model_served,
			status,
			http_status,
			COALESCE(error_code, '') AS error_code,
			tokens_in,
			tokens_out,
			cost_usd,
			COALESCE(latency_ms, 0) AS latency_ms,
			COALESCE(ttfb_ms, 0) AS ttfb_ms,
			streamed
		FROM them.gateway_requests
		WHERE tenant_id = $1::uuid AND id = $2::uuid
		LIMIT 1`

	var e GatewayRequestEvent
	var httpStatus int

	err := p.pool.QueryRow(ctx, q, tenantID, requestID).Scan(
		&e.RequestID,
		&e.ClientID,
		&e.Provider,
		&e.ModelRequested,
		&e.ModelServed,
		&e.Status,
		&httpStatus,
		&e.ErrorCode,
		&e.TokensIn,
		&e.TokensOut,
		&e.CostUSD,
		&e.LatencyMS,
		&e.TTFBMS,
		&e.Streamed,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil // not found
		}
		return nil, fmt.Errorf("postgres: get gateway request: %w", err)
	}

	e.HTTPStatus = uint16(httpStatus)
	return &e, nil
}

// UsageByDay aggregates gateway_requests by day.
func (p *PostgresStore) UsageByDay(ctx context.Context, tenantID string, filter UsageFilter) ([]UsagePoint, error) {
	// Simple day aggregation
	q := `
		SELECT
			DATE(created_at AT TIME ZONE 'UTC') AS day,
			COUNT(*) AS requests,
			SUM(tokens_in) AS tokens_in,
			SUM(tokens_out) AS tokens_out,
			SUM(cost_usd) AS cost_usd
		FROM them.gateway_requests
		WHERE tenant_id = $1::uuid`

	args := []interface{}{tenantID}
	argIdx := 2

	if filter.ApplicationID != "" {
		q += fmt.Sprintf(" AND application_id = $%d::uuid", argIdx)
		args = append(args, filter.ApplicationID)
		argIdx++
	}
	if !filter.StartTime.IsZero() {
		q += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, filter.StartTime)
		argIdx++
	}
	if !filter.EndTime.IsZero() {
		q += fmt.Sprintf(" AND created_at < $%d", argIdx)
		args = append(args, filter.EndTime)
		argIdx++
	}

	q += " GROUP BY day ORDER BY day DESC"

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: usage by day: %w", err)
	}
	defer rows.Close()

	var results []UsagePoint
	for rows.Next() {
		var point UsagePoint
		var day time.Time

		err := rows.Scan(&day, &point.Requests, &point.TokensIn, &point.TokensOut, &point.CostUSD)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan usage point: %w", err)
		}

		point.Timestamp = day
		results = append(results, point)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate usage points: %w", err)
	}

	return results, nil
}

// ToolCallsForRun — not implemented yet (no table exists in current schema).
func (p *PostgresStore) ToolCallsForRun(ctx context.Context, tenantID string, runID string) ([]ToolCallEvent, error) {
	// run_steps table exists but doesn't have the event shape yet
	// Return empty for now — will be implemented in Stage B area 3
	return []ToolCallEvent{}, nil
}

// UserActivity — not implemented yet (no table exists).
func (p *PostgresStore) UserActivity(ctx context.Context, tenantID string, filter ActivityFilter) ([]UserActivityEvent, error) {
	// Will be implemented in Stage B area 2
	return []UserActivityEvent{}, nil
}

// Ping checks Postgres connectivity.
func (p *PostgresStore) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// PostgresSink writes events directly to Postgres tables (synchronous, blocking).
// Used when THE_M_EVENTS_<AREA>=postgres (default until migration).
type PostgresSink struct {
	pool *pgxpool.Pool
}

// NewPostgresSink creates a Postgres event sink.
func NewPostgresSink(pool *pgxpool.Pool) *PostgresSink {
	return &PostgresSink{pool: pool}
}

// Emit writes the event to the corresponding Postgres table.
func (p *PostgresSink) Emit(ctx context.Context, event Event) error {
	switch event.Kind {
	case EventKindGatewayRequest:
		return p.writeGatewayRequest(ctx, event.TenantID, event.Timestamp, event.GatewayRequest)
	case EventKindToolCall:
		// Not implemented yet — run_steps table exists but schema differs
		return nil
	case EventKindUserActivity:
		// Not implemented yet — no table
		return nil
	case EventKindAudit:
		// audit_logs table exists but has different shape
		return nil
	default:
		return fmt.Errorf("postgres sink: unknown event kind: %s", event.Kind)
	}
}

// Close is a no-op for Postgres sink (pool lifecycle managed externally).
func (p *PostgresSink) Close(ctx context.Context) error {
	return nil
}

func (p *PostgresSink) writeGatewayRequest(ctx context.Context, tenantID string, ts time.Time, gw *GatewayRequestEvent) error {
	if gw == nil {
		return fmt.Errorf("postgres sink: nil gateway request event")
	}

	q := `
		INSERT INTO them.gateway_requests
			(id, tenant_id, client_id, provider, model_requested, model_served,
			 status, http_status, error_code, tokens_in, tokens_out, cost_usd,
			 latency_ms, ttfb_ms, streamed, created_at)
		VALUES
			($1::uuid, $2::uuid, NULLIF($3,'')::uuid, $4, $5, $6,
			 $7, NULLIF($8,0), NULLIF($9,''), $10, $11, $12,
			 NULLIF($13,0), NULLIF($14,0), $15, $16)`

	_, err := p.pool.Exec(ctx, q,
		gw.RequestID,
		tenantID,
		gw.ClientID,
		gw.Provider,
		gw.ModelRequested,
		gw.ModelServed,
		gw.Status,
		gw.HTTPStatus,
		gw.ErrorCode,
		gw.TokensIn,
		gw.TokensOut,
		gw.CostUSD,
		gw.LatencyMS,
		gw.TTFBMS,
		gw.Streamed,
		ts,
	)
	return err
}
