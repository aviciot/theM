package events

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

// ClickHouseStore implements EventStore using ClickHouse.
// Schema: db/clickhouse/001_events.sql
// Design: docs/OBSERVABILITY_STORAGE_DESIGN.md §10
type ClickHouseStore struct {
	db *sql.DB
}

// NewClickHouseStore creates a ClickHouse-backed event store.
// dsn format: "clickhouse://events_reader:password@them-clickhouse:9000/them_events"
func NewClickHouseStore(dsn string) (*ClickHouseStore, error) {
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}

	return &ClickHouseStore{db: db}, nil
}

// ListGatewayRequests queries events_gateway_request.
func (c *ClickHouseStore) ListGatewayRequests(ctx context.Context, tenantID string, filter GatewayRequestFilter) ([]GatewayRequestEvent, error) {
	query := `
		SELECT
			request_id,
			application_id,
			client_id,
			provider,
			model_requested,
			model_served,
			status,
			http_status,
			error_code,
			tokens_in,
			tokens_out,
			cost_usd,
			latency_ms,
			ttfb_ms,
			streamed
		FROM them_events.events_gateway_request FINAL
		WHERE tenant_id = ?`

	args := []interface{}{tenantID}

	if filter.ApplicationID != "" {
		query += " AND application_id = ?"
		args = append(args, filter.ApplicationID)
	}
	if filter.ClientID != "" {
		query += " AND client_id = ?"
		args = append(args, filter.ClientID)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if !filter.StartTime.IsZero() {
		query += " AND ts >= ?"
		args = append(args, filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		query += " AND ts < ?"
		args = append(args, filter.EndTime)
	}

	query += " ORDER BY ts DESC"

	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query gateway requests: %w", err)
	}
	defer rows.Close()

	var results []GatewayRequestEvent
	for rows.Next() {
		var e GatewayRequestEvent
		var applicationID, clientID, provider, modelRequested, modelServed, status, errorCode sql.NullString
		var httpStatus sql.NullInt32
		var latencyMS, ttfbMS sql.NullInt32

		err := rows.Scan(
			&e.RequestID,
			&applicationID,
			&clientID,
			&provider,
			&modelRequested,
			&modelServed,
			&status,
			&httpStatus,
			&errorCode,
			&e.TokensIn,
			&e.TokensOut,
			&e.CostUSD,
			&latencyMS,
			&ttfbMS,
			&e.Streamed,
		)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: scan gateway request: %w", err)
		}

		e.ApplicationID = applicationID.String
		e.ClientID = clientID.String
		e.Provider = provider.String
		e.ModelRequested = modelRequested.String
		e.ModelServed = modelServed.String
		e.Status = status.String
		e.ErrorCode = errorCode.String
		if httpStatus.Valid {
			e.HTTPStatus = uint16(httpStatus.Int32)
		}
		if latencyMS.Valid {
			e.LatencyMS = uint32(latencyMS.Int32)
		}
		if ttfbMS.Valid {
			e.TTFBMS = uint32(ttfbMS.Int32)
		}

		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: iterate gateway requests: %w", err)
	}

	return results, nil
}

// GetGatewayRequestByID fetches a single gateway request.
func (c *ClickHouseStore) GetGatewayRequestByID(ctx context.Context, tenantID string, requestID string) (*GatewayRequestEvent, error) {
	query := `
		SELECT
			request_id,
			application_id,
			client_id,
			provider,
			model_requested,
			model_served,
			status,
			http_status,
			error_code,
			tokens_in,
			tokens_out,
			cost_usd,
			latency_ms,
			ttfb_ms,
			streamed
		FROM them_events.events_gateway_request FINAL
		WHERE tenant_id = ? AND request_id = ?
		LIMIT 1`

	var e GatewayRequestEvent
	var applicationID, clientID, provider, modelRequested, modelServed, status, errorCode sql.NullString
	var httpStatus sql.NullInt32
	var latencyMS, ttfbMS sql.NullInt32

	err := c.db.QueryRowContext(ctx, query, tenantID, requestID).Scan(
		&e.RequestID,
		&applicationID,
		&clientID,
		&provider,
		&modelRequested,
		&modelServed,
		&status,
		&httpStatus,
		&errorCode,
		&e.TokensIn,
		&e.TokensOut,
		&e.CostUSD,
		&latencyMS,
		&ttfbMS,
		&e.Streamed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("clickhouse: get gateway request: %w", err)
	}

	e.ApplicationID = applicationID.String
	e.ClientID = clientID.String
	e.Provider = provider.String
	e.ModelRequested = modelRequested.String
	e.ModelServed = modelServed.String
	e.Status = status.String
	e.ErrorCode = errorCode.String
	if httpStatus.Valid {
		e.HTTPStatus = uint16(httpStatus.Int32)
	}
	if latencyMS.Valid {
		e.LatencyMS = uint32(latencyMS.Int32)
	}
	if ttfbMS.Valid {
		e.TTFBMS = uint32(ttfbMS.Int32)
	}

	return &e, nil
}

// UsageByDay aggregates gateway requests by day.
func (c *ClickHouseStore) UsageByDay(ctx context.Context, tenantID string, filter UsageFilter) ([]UsagePoint, error) {
	query := `
		SELECT
			toDate(ts) AS day,
			COUNT(*) AS requests,
			SUM(tokens_in) AS tokens_in,
			SUM(tokens_out) AS tokens_out,
			SUM(cost_usd) AS cost_usd
		FROM them_events.events_gateway_request FINAL
		WHERE tenant_id = ?`

	args := []interface{}{tenantID}

	if filter.ApplicationID != "" {
		query += " AND application_id = ?"
		args = append(args, filter.ApplicationID)
	}
	if !filter.StartTime.IsZero() {
		query += " AND ts >= ?"
		args = append(args, filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		query += " AND ts < ?"
		args = append(args, filter.EndTime)
	}

	query += " GROUP BY day ORDER BY day DESC"

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: usage by day: %w", err)
	}
	defer rows.Close()

	var results []UsagePoint
	for rows.Next() {
		var point UsagePoint
		var day time.Time

		err := rows.Scan(&day, &point.Requests, &point.TokensIn, &point.TokensOut, &point.CostUSD)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: scan usage point: %w", err)
		}

		point.Timestamp = day
		results = append(results, point)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: iterate usage points: %w", err)
	}

	return results, nil
}

// ToolCallsForRun returns tool calls for a given run.
func (c *ClickHouseStore) ToolCallsForRun(ctx context.Context, tenantID string, runID string) ([]ToolCallEvent, error) {
	query := `
		SELECT
			application_id,
			run_id,
			request_id,
			trace_id,
			user_id,
			tool_name,
			mcp_server,
			status,
			duration_ms,
			args_ref,
			result_ref
		FROM them_events.events_tool_call FINAL
		WHERE tenant_id = ? AND run_id = ?
		ORDER BY ts ASC`

	rows, err := c.db.QueryContext(ctx, query, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query tool calls: %w", err)
	}
	defer rows.Close()

	var results []ToolCallEvent
	for rows.Next() {
		var e ToolCallEvent

		err := rows.Scan(
			&e.ApplicationID,
			&e.RunID,
			&e.RequestID,
			&e.TraceID,
			&e.UserID,
			&e.ToolName,
			&e.MCPServer,
			&e.Status,
			&e.DurationMS,
			&e.ArgsRef,
			&e.ResultRef,
		)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: scan tool call: %w", err)
		}

		results = append(results, e)
	}

	return results, nil
}

// UserActivity returns user activity events.
func (c *ClickHouseStore) UserActivity(ctx context.Context, tenantID string, filter ActivityFilter) ([]UserActivityEvent, error) {
	query := `
		SELECT
			application_id,
			user_id,
			action,
			resource_type,
			resource_id,
			outcome,
			ip,
			user_agent,
			trace_id
		FROM them_events.events_user_activity FINAL
		WHERE tenant_id = ?`

	args := []interface{}{tenantID}

	if filter.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}
	if filter.ResourceType != "" {
		query += " AND resource_type = ?"
		args = append(args, filter.ResourceType)
	}
	if !filter.StartTime.IsZero() {
		query += " AND ts >= ?"
		args = append(args, filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		query += " AND ts < ?"
		args = append(args, filter.EndTime)
	}

	query += " ORDER BY ts DESC"

	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query user activity: %w", err)
	}
	defer rows.Close()

	var results []UserActivityEvent
	for rows.Next() {
		var e UserActivityEvent

		err := rows.Scan(
			&e.ApplicationID,
			&e.UserID,
			&e.Action,
			&e.ResourceType,
			&e.ResourceID,
			&e.Outcome,
			&e.IP,
			&e.UserAgent,
			&e.TraceID,
		)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: scan user activity: %w", err)
		}

		results = append(results, e)
	}

	return results, nil
}

// Ping checks ClickHouse connectivity.
func (c *ClickHouseStore) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

// Close closes the ClickHouse connection.
func (c *ClickHouseStore) Close() error {
	return c.db.Close()
}

// ClickHouseSink writes events directly to ClickHouse (batch insert).
type ClickHouseSink struct {
	db *sql.DB
}

// NewClickHouseSink creates a ClickHouse event sink.
// dsn format: "clickhouse://events_writer:password@them-clickhouse:9000/them_events"
func NewClickHouseSink(dsn string) (*ClickHouseSink, error) {
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}

	return &ClickHouseSink{db: db}, nil
}

// Emit writes an event to ClickHouse.
func (c *ClickHouseSink) Emit(ctx context.Context, event Event) error {
	// Validate BEFORE attempting write
	if err := ValidateEvent(event); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedEvent, err)
	}

	var err error
	switch event.Kind {
	case EventKindGatewayRequest:
		err = c.writeGatewayRequest(ctx, event.TenantID, event.Timestamp, event.EventID, event.GatewayRequest)
	case EventKindToolCall:
		err = c.writeToolCall(ctx, event.TenantID, event.Timestamp, event.EventID, event.ToolCall)
	case EventKindUserActivity:
		err = c.writeUserActivity(ctx, event.TenantID, event.Timestamp, event.EventID, event.UserActivity)
	case EventKindAudit:
		err = c.writeAudit(ctx, event.TenantID, event.Timestamp, event.EventID, event.Audit)
	default:
		return fmt.Errorf("clickhouse sink: unknown event kind: %s", event.Kind)
	}

	// Check if write failed due to ClickHouse connection issue (transient)
	if err != nil && isClickHouseConnectionError(err) {
		return fmt.Errorf("%w: %v", ErrTransientFailure, err)
	}

	return err
}

func isClickHouseConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "dial tcp") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "no such host")
}

// Close closes the ClickHouse connection.
func (c *ClickHouseSink) Close(ctx context.Context) error {
	return c.db.Close()
}

func (c *ClickHouseSink) writeGatewayRequest(ctx context.Context, tenantID string, ts time.Time, eventID string, gw *GatewayRequestEvent) error {
	if gw == nil {
		return fmt.Errorf("clickhouse sink: nil gateway request event")
	}

	query := `
		INSERT INTO them_events.events_gateway_request (
			event_id, schema_version, ts, tenant_id, application_id, client_id,
			user_id, request_id, trace_id, provider, model_requested, model_served,
			status, http_status, error_code, tokens_in, tokens_out, cost_usd,
			latency_ms, ttfb_ms, streamed, body_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Convert empty strings to nil for UUID fields (ClickHouse requires valid UUID or NULL)
	var applicationID, clientID, userID, traceID, bodyRef interface{}
	if gw.ApplicationID != "" {
		applicationID = gw.ApplicationID
	}
	if gw.ClientID != "" {
		clientID = gw.ClientID
	}
	if gw.UserID != "" {
		userID = gw.UserID
	}
	if gw.TraceID != "" {
		traceID = gw.TraceID
	}
	if gw.BodyRef != "" {
		bodyRef = gw.BodyRef
	}

	_, err := c.db.ExecContext(ctx, query,
		eventID,
		1, // schema_version
		ts,
		tenantID,
		applicationID,
		clientID,
		userID,
		gw.RequestID,
		traceID,
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
		bodyRef,
	)

	return err
}

func (c *ClickHouseSink) writeToolCall(ctx context.Context, tenantID string, ts time.Time, eventID string, tc *ToolCallEvent) error {
	if tc == nil {
		return nil // Skip nil events
	}

	query := `
		INSERT INTO them_events.events_tool_call (
			event_id, schema_version, ts, tenant_id, application_id, run_id,
			request_id, trace_id, user_id, tool_name, mcp_server, status,
			duration_ms, args_ref, result_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Convert empty strings to nil for UUID fields
	var tenantUUID, applicationID, runID, requestID, traceID, userID, argsRef, resultRef interface{}
	if tenantID != "" {
		tenantUUID = tenantID
	}
	if tc.ApplicationID != "" {
		applicationID = tc.ApplicationID
	}
	if tc.RunID != "" {
		runID = tc.RunID
	}
	if tc.RequestID != "" {
		requestID = tc.RequestID
	}
	if tc.TraceID != "" {
		traceID = tc.TraceID
	}
	if tc.UserID != "" {
		userID = tc.UserID
	}
	if tc.ArgsRef != "" {
		argsRef = tc.ArgsRef
	}
	if tc.ResultRef != "" {
		resultRef = tc.ResultRef
	}

	_, err := c.db.ExecContext(ctx, query,
		eventID,
		1,
		ts,
		tenantUUID,
		applicationID,
		runID,
		requestID,
		traceID,
		userID,
		tc.ToolName,
		tc.MCPServer,
		tc.Status,
		tc.DurationMS,
		argsRef,
		resultRef,
	)

	return err
}

func (c *ClickHouseSink) writeUserActivity(ctx context.Context, tenantID string, ts time.Time, eventID string, ua *UserActivityEvent) error {
	if ua == nil {
		return nil
	}

	query := `
		INSERT INTO them_events.events_user_activity (
			event_id, schema_version, ts, tenant_id, application_id, user_id,
			action, resource_type, resource_id, outcome, ip, user_agent, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Convert empty strings to nil for UUID fields
	var tenantUUID, applicationID, userID, resourceID, traceID interface{}
	if tenantID != "" {
		tenantUUID = tenantID
	}
	if ua.ApplicationID != "" {
		applicationID = ua.ApplicationID
	}
	if ua.UserID != "" {
		userID = ua.UserID
	}
	if ua.ResourceID != "" {
		resourceID = ua.ResourceID
	}
	if ua.TraceID != "" {
		traceID = ua.TraceID
	}

	_, err := c.db.ExecContext(ctx, query,
		eventID,
		1,
		ts,
		tenantUUID,
		applicationID,
		userID,
		ua.Action,
		ua.ResourceType,
		resourceID,
		ua.Outcome,
		ua.IP,
		ua.UserAgent,
		traceID,
	)

	return err
}

func (c *ClickHouseSink) writeAudit(ctx context.Context, tenantID string, ts time.Time, eventID string, audit *AuditEvent) error {
	if audit == nil {
		return nil
	}

	query := `
		INSERT INTO them_events.events_audit (
			event_id, schema_version, ts, tenant_id, user_id, action,
			resource_type, resource_id, before_ref, after_ref, prev_hash, hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := c.db.ExecContext(ctx, query,
		eventID,
		1,
		ts,
		tenantID,
		audit.UserID,
		audit.Action,
		audit.ResourceType,
		audit.ResourceID,
		audit.BeforeRef,
		audit.AfterRef,
		audit.PrevHash,
		audit.Hash,
	)

	return err
}
