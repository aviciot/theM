package llmgateway

import (
	"context"
	"encoding/json"

	"github.com/aviciot/them/internal/governance"
)

// policyRow is one governance_policies row relevant to a gateway client.
type policyRow struct {
	ScopeType string // tenant | profile | client (| application | user, ignored here)
	ScopeID   string
	Doc       []byte
}

// policyFromRows resolves the client's governance docs into the runtime Policy.
// Limits from every row are enforced (tightest wins). Rows must be ordered least →
// most specific (tenant, profile, client override) so later rows win ties.
// Returns nil when there are no rows (no enforcement). Undecodable docs are skipped.
func policyFromRows(rows []policyRow) *Policy {
	var sps []governance.ScopedPolicy
	for _, r := range rows {
		var doc governance.Policy
		if json.Unmarshal(r.Doc, &doc) != nil {
			continue
		}
		doc.Normalize()
		st := governance.ScopeType(r.ScopeType)
		if r.ScopeType == "profile" {
			st = governance.ScopeClient // a client's profile IS its client-scope policy
		}
		sps = append(sps, governance.ScopedPolicy{Type: st, ID: r.ScopeID, Policy: doc})
	}
	if len(sps) == 0 {
		return nil
	}
	eff := governance.Resolve(sps...)

	p := &Policy{
		AllowLists:          eff.ModelAllow,
		ModelAliases:        eff.Aliases,
		MaxTokensPerRequest: eff.MaxTokens,
		LoggingLevel:        eff.Logging.Level,
	}
	// Runtime enforces one monthly USD cap today (tightest across scopes); other
	// budgets are carried in the doc and enforced once Redis meters land (slice 3).
	for _, b := range eff.Budgets {
		if b.Metric == governance.MetricUSD && b.Period == governance.PeriodMonth && b.OnExceed == governance.ActionBlock {
			if p.MonthlyBudgetUSD == 0 || b.Limit < p.MonthlyBudgetUSD {
				p.MonthlyBudgetUSD = b.Limit
			}
		}
	}
	return p
}

// ModelAllowed reports whether model passes every allow-list (legacy AllowedModels
// plus one list per governance scope). No lists = allow all.
func (p *Policy) ModelAllowed(model string) bool {
	eff := governance.Effective{ModelAllow: p.AllowLists}
	if len(p.AllowedModels) > 0 {
		eff.ModelAllow = append(append([][]string{}, p.AllowLists...), p.AllowedModels)
	}
	return eff.ModelAllowed(model)
}

// loadPolicyFromDB loads the tenant policy, the client's assigned profile and the
// client's own override from governance_policies and resolves them.
func (d *DAL) loadPolicyFromDB(ctx context.Context, tenantID, clientID string) (*Policy, error) {
	const q = `
		SELECT g.scope_type, g.scope_id, g.doc
		FROM them.governance_policies g
		WHERE g.tenant_id = $1::uuid
		  AND ( (g.scope_type = 'tenant' AND g.scope_id = $1::text)
		     OR (g.scope_type = 'client' AND g.scope_id = $2::text)
		     OR  g.id = (SELECT governance_profile_id FROM them.gateway_clients
		                 WHERE tenant_id = $1::uuid AND id = $2::uuid) )
		ORDER BY CASE g.scope_type WHEN 'tenant' THEN 0 WHEN 'profile' THEN 1 ELSE 2 END`

	rows, err := d.pool.Query(ctx, q, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []policyRow
	for rows.Next() {
		var r policyRow
		if err := rows.Scan(&r.ScopeType, &r.ScopeID, &r.Doc); err != nil {
			return nil, err
		}
		prs = append(prs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policyFromRows(prs), nil
}
