package dal

import (
	"context"
)

// AppObservabilitySummary is one row returned by ListAppObservabilitySummary.
type AppObservabilitySummary struct {
	ApplicationID string `json:"application_id"`
	AppName       string `json:"app_name"`
	// Last-30-day DB aggregates.
	RunCount30d    int64 `json:"run_count_30d"`
	TokensIn30d    int64 `json:"tokens_in_30d"`
	TokensOut30d   int64 `json:"tokens_out_30d"`
	MCPCalls30d    int64 `json:"mcp_calls_30d"`
	// Today live numbers from Redis (zero if not yet populated).
	RunsToday    int64 `json:"runs_today"`
	TokensInToday  int64 `json:"tokens_in_today"`
	TokensOutToday int64 `json:"tokens_out_today"`
	MCPCallsToday  int64 `json:"mcp_calls_today"`
	ActiveUsersToday int64 `json:"active_users_today"`
	// IsLive is true when today's numbers came from Redis.
	IsLive bool `json:"is_live"`
}

// TenantObservabilitySummary is one row returned by ListObservabilitySummary.
// It aggregates per-tenant operational metrics for the super-admin observability view.
type TenantObservabilitySummary struct {
	TenantID    string `json:"tenant_id"`
	DisplayName string `json:"display_name"`
	// Last-30-day aggregates.
	RunCount30d       int64 `json:"run_count_30d"`
	TotalLLMTokens30d int64 `json:"total_llm_tokens_30d"`
	// Current quota limits (nil = unlimited).
	MaxAgents *int `json:"max_agents"`
	MaxApps   *int `json:"max_apps"`
	// Current resource counts.
	AgentCount int64 `json:"agent_count"`
	AppCount   int64 `json:"app_count"`
	// Today live numbers from Redis (zero if not yet populated).
	RunsToday        int64 `json:"runs_today"`
	ActiveUsersToday int64 `json:"active_users_today"`
	// IsLive is true when today's numbers came from Redis.
	IsLive bool `json:"is_live"`
	// AppIDs holds this tenant's application UUIDs, populated by ListObservabilitySummary
	// so the handler can query per-app Redis keys without a second DB round-trip.
	// Not exposed in JSON.
	AppIDs []string `json:"-"`
}

// ListObservabilitySummary returns one row per tenant with aggregate run/token
// usage (last 30 days), current quota + resource counts, and the list of
// application IDs (used by the handler to fetch per-app Redis metrics).
// The querier MUST use the Admin (BYPASSRLS) pool — this is intentional; it is
// a super-admin cross-tenant view and must bypass RLS policies.
func ListObservabilitySummary(ctx context.Context, q Querier) ([]TenantObservabilitySummary, error) {
	const query = `
		SELECT
			t.id::text,
			t.display_name,
			COALESCE(r.run_count, 0),
			COALESCE(r.total_llm_tokens, 0),
			q.max_agents,
			q.max_apps,
			COALESCE(ac.agent_count, 0),
			COALESCE(apc.app_count, 0),
			COALESCE(apc.app_ids, '{}')
		FROM them.tenants t
		LEFT JOIN (
			SELECT
				tenant_id,
				COUNT(*) AS run_count,
				COALESCE(SUM(total_tokens_in + total_tokens_out), 0) AS total_llm_tokens
			FROM them.runs
			WHERE started_at >= now() - INTERVAL '30 days'
			GROUP BY tenant_id
		) r ON r.tenant_id = t.id
		LEFT JOIN them.tenant_quotas q ON q.tenant_id = t.id
		LEFT JOIN (
			SELECT tenant_id, COUNT(*) AS agent_count
			FROM them.agents
			GROUP BY tenant_id
		) ac ON ac.tenant_id = t.id
		LEFT JOIN (
			SELECT tenant_id, COUNT(*) AS app_count,
			       ARRAY_AGG(id::text) AS app_ids
			FROM them.applications
			GROUP BY tenant_id
		) apc ON apc.tenant_id = t.id
		ORDER BY t.created_at ASC`

	rows, err := q.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TenantObservabilitySummary, 0)
	for rows.Next() {
		var s TenantObservabilitySummary
		if err := rows.Scan(
			&s.TenantID, &s.DisplayName,
			&s.RunCount30d, &s.TotalLLMTokens30d,
			&s.MaxAgents, &s.MaxApps,
			&s.AgentCount, &s.AppCount,
			&s.AppIDs,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ListAppObservabilitySummary returns one row per application for the given tenant
// with 30-day run and token aggregates from them.runs / them.run_usage.
// The querier MUST use the Admin (BYPASSRLS) pool.
func ListAppObservabilitySummary(ctx context.Context, q Querier, tenantID string) ([]AppObservabilitySummary, error) {
	const query = `
		SELECT
			a.id::text,
			a.name,
			COALESCE(r.run_count, 0),
			COALESCE(r.tokens_in, 0),
			COALESCE(r.tokens_out, 0),
			COALESCE(r.mcp_calls, 0)
		FROM them.applications a
		LEFT JOIN (
			SELECT
				ru.application_id,
				COUNT(DISTINCT ru.run_id) AS run_count,
				COALESCE(SUM(ru.tokens_in), 0)  AS tokens_in,
				COALESCE(SUM(ru.tokens_out), 0) AS tokens_out,
				COALESCE(SUM(ru.mcp_calls), 0)  AS mcp_calls
			FROM them.run_usage ru
			JOIN them.runs rn ON rn.id = ru.run_id
			WHERE rn.tenant_id = $1
			  AND rn.started_at >= now() - INTERVAL '30 days'
			GROUP BY ru.application_id
		) r ON r.application_id = a.id
		WHERE a.tenant_id = $1
		ORDER BY a.name ASC`

	rows, err := q.Query(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AppObservabilitySummary, 0)
	for rows.Next() {
		var s AppObservabilitySummary
		if err := rows.Scan(
			&s.ApplicationID, &s.AppName,
			&s.RunCount30d, &s.TokensIn30d, &s.TokensOut30d, &s.MCPCalls30d,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
