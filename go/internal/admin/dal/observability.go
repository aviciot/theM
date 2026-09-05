package dal

import (
	"context"
)

// TenantObservabilitySummary is one row returned by ListObservabilitySummary.
// It aggregates per-tenant operational metrics for the super-admin observability view.
type TenantObservabilitySummary struct {
	TenantID    string `json:"tenant_id"`
	DisplayName string `json:"display_name"`
	// Last-30-day aggregates.
	RunCount30d      int64 `json:"run_count_30d"`
	TotalLLMTokens30d int64 `json:"total_llm_tokens_30d"`
	// Current quota limits (nil = unlimited).
	MaxAgents *int `json:"max_agents"`
	MaxApps   *int `json:"max_apps"`
	// Current resource counts.
	AgentCount int64 `json:"agent_count"`
	AppCount   int64 `json:"app_count"`
}

// ListObservabilitySummary returns one row per tenant with aggregate run/token
// usage (last 30 days) and current quota + resource counts.
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
			COALESCE(apc.app_count, 0)
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
			SELECT tenant_id, COUNT(*) AS app_count
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
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
