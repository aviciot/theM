package dal

import (
	"context"
	"fmt"
	"strings"
)

// AppOrchDetail carries the orchestrator fields needed to synthesize a card.
type AppOrchDetail struct {
	ID              string
	DisplayName     string
	SystemPrompt    string
	AllowedAgentIDs []string
}

// GetAppOrchForEP returns the orchestrator bound to the given entry point.
// Scoped to appID for tenant safety. Returns ErrNotFound when the EP has no
// orchestrator binding or when the EP/app does not exist.
func (d *DB) GetAppOrchForEP(ctx context.Context, appID, epID string) (AppOrchDetail, error) {
	const q = `
SELECT ao.id::text,
       COALESCE(ao.display_name, ao.name),
       COALESCE(ao.system_prompt, ''),
       COALESCE(ao.allowed_agent_ids, '{}')
FROM them.entry_points ep
JOIN them.app_orchestrators ao
  ON ao.id = ep.app_orchestrator_id
 AND ao.application_id = ep.application_id
WHERE ep.id             = $1::uuid
  AND ep.application_id = $2::uuid`

	var det AppOrchDetail
	var agentIDs []string
	err := d.q.QueryRow(ctx, q, epID, appID).Scan(
		&det.ID, &det.DisplayName, &det.SystemPrompt, &agentIDs,
	)
	if err != nil {
		return AppOrchDetail{}, err
	}
	det.AllowedAgentIDs = agentIDs
	return det, nil
}

// AgentCardSummary is the minimal agent data needed to synthesize a card.
type AgentCardSummary struct {
	DisplayName string
	Description string
	SkillsJSON  []byte
}

// GetAgentSummariesByIDs returns display_name, description, and skills for a
// list of agent UUIDs. Order is not guaranteed. Missing IDs are silently skipped.
func (d *DB) GetAgentSummariesByIDs(ctx context.Context, ids []string) ([]AgentCardSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+1)
		args[i] = id
	}
	q := fmt.Sprintf(`
SELECT display_name, description, COALESCE(skills, '[]'::jsonb)
FROM them.agents
WHERE id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := d.q.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentCardSummary
	for rows.Next() {
		var s AgentCardSummary
		if err := rows.Scan(&s.DisplayName, &s.Description, &s.SkillsJSON); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// SetEPAgentCard writes a synthesized agent card to entry_points.agent_card
// and updates card_synthesized_at. Scoped to appID for tenant safety.
func (d *DB) SetEPAgentCard(ctx context.Context, appID, epID string, card []byte) error {
	const q = `
UPDATE them.entry_points
SET agent_card          = $3::jsonb,
    card_synthesized_at = now(),
    updated_at          = now()
WHERE id             = $1::uuid
  AND application_id = $2::uuid`
	return d.q.Exec(ctx, q, epID, appID, string(card))
}

// AppReadinessRow carries the minimal data needed for app readiness validation.
// It tells the service whether a LLM provider+model is configured and whether
// an API key exists (either in app provider_keys or tenant llm_providers).
type AppReadinessRow struct {
	// HasOrchestrator is true when at least one enabled app_orchestrator is linked to the app.
	HasOrchestrator bool
	// Provider is the resolved provider name ("anthropic", "openai", …).
	// Resolution order: EP-level → orchestrator-level. Empty when not set.
	Provider string
	// Model is the resolved model name. Empty when not set.
	Model string
	// HasAppKey is true when provider_keys on the application contains a non-empty entry for Provider.
	HasAppKey bool
	// HasTenantKey is true when them.llm_providers has an enabled, tenant-scoped row for Provider.
	HasTenantKey bool
	// MemoryEnabled is true when any entry point linked to this app has memory turned on.
	MemoryEnabled bool
	// SummarizerProvider is set when memory is on and a summarizer provider is specified.
	SummarizerProvider string
	// SummarizerModel is set when memory is on and a summarizer model is specified.
	SummarizerModel string
	// HasSummarizerKey is true when there is an app or tenant key for SummarizerProvider.
	HasSummarizerKey bool
}

// GetAppReadinessInfo returns a single row that the admin service uses to validate
// whether an application is ready to be enabled. It resolves the provider/model
// using the same precedence the worker uses (EP override → orchestrator), then
// checks for API key presence in app provider_keys and tenant llm_providers.
//
// The query uses a single CTE to avoid N round-trips.
// tenantID is used to scope the tenant llm_providers lookup.
func (d *DB) GetAppReadinessInfo(ctx context.Context, tenantID, appID string) (AppReadinessRow, error) {
	const q = `
WITH orch AS (
    -- Pick any enabled orchestrator attached to this app.
    SELECT ao.llm_provider, ao.llm_model, ao.id AS orch_id
    FROM them.app_orchestrators ao
    WHERE ao.application_id = $1::uuid AND ao.enabled = true
    ORDER BY ao.created_at ASC
    LIMIT 1
),
ep AS (
    -- Pick any enabled EP that is connected to that orchestrator and may have an override.
    SELECT ep.llm_provider AS ep_provider,
           ep.llm_model    AS ep_model,
           ep.memory_enabled,
           ep.summarizer_provider,
           ep.summarizer_model
    FROM them.entry_points ep
    JOIN orch ON orch.orch_id = ep.app_orchestrator_id
    WHERE ep.application_id = $1::uuid AND ep.enabled = true
    ORDER BY ep.created_at ASC
    LIMIT 1
)
SELECT
    -- HasOrchestrator
    EXISTS(SELECT 1 FROM orch) AS has_orch,
    -- Resolved provider: EP override first, then orchestrator, then empty
    COALESCE(ep.ep_provider, orch.llm_provider, '') AS provider,
    -- Resolved model
    COALESCE(ep.ep_model, orch.llm_model, '') AS model,
    -- HasAppKey: application.provider_keys has a non-null entry for that provider
    CASE WHEN COALESCE(ep.ep_provider, orch.llm_provider, '') = '' THEN false
         ELSE COALESCE(
             (a.provider_keys -> COALESCE(ep.ep_provider, orch.llm_provider) ->> 'ct') IS NOT NULL
             AND (a.provider_keys -> COALESCE(ep.ep_provider, orch.llm_provider) ->> 'ct') <> '',
             false
         )
    END AS has_app_key,
    -- HasTenantKey: llm_providers has an enabled tenant-scoped row for that provider
    CASE WHEN COALESCE(ep.ep_provider, orch.llm_provider, '') = '' THEN false
         ELSE EXISTS(
             SELECT 1 FROM them.llm_providers lp
             WHERE lp.name       = COALESCE(ep.ep_provider, orch.llm_provider)
               AND lp.tenant_id  = $2::uuid
               AND lp.enabled    = true
         )
    END AS has_tenant_key,
    -- Memory fields
    COALESCE(ep.memory_enabled, false),
    COALESCE(ep.summarizer_provider, ''),
    COALESCE(ep.summarizer_model, ''),
    -- HasSummarizerKey
    CASE WHEN COALESCE(ep.summarizer_provider, '') = '' THEN false
         ELSE (
             COALESCE(
                 (a.provider_keys -> ep.summarizer_provider ->> 'ct') IS NOT NULL
                 AND (a.provider_keys -> ep.summarizer_provider ->> 'ct') <> '',
                 false
             )
             OR EXISTS(
                 SELECT 1 FROM them.llm_providers lp
                 WHERE lp.name       = ep.summarizer_provider
                   AND lp.tenant_id  = $2::uuid
                   AND lp.enabled    = true
             )
         )
    END AS has_sum_key
FROM them.applications a
LEFT JOIN orch ON true
LEFT JOIN ep   ON true
WHERE a.id = $1::uuid`

	var r AppReadinessRow
	err := d.q.QueryRow(ctx, q, appID, tenantID).Scan(
		&r.HasOrchestrator,
		&r.Provider,
		&r.Model,
		&r.HasAppKey,
		&r.HasTenantKey,
		&r.MemoryEnabled,
		&r.SummarizerProvider,
		&r.SummarizerModel,
		&r.HasSummarizerKey,
	)
	if err != nil {
		return AppReadinessRow{}, err
	}
	return r, nil
}
