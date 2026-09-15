package dal

import (
	"context"
	"fmt"
	"time"
)

// CopyAgentsForDeployResult holds the outcome of a CopyAgentsForDeploy call.
type CopyAgentsForDeployResult struct {
	IDMap         map[string]string // old UUID → new/existing UUID
	CopiedSlugs   []string          // agents inserted fresh
	ReusedSlugs   []string          // agents already present in target tenant (not overwritten)
	ConflictSlugs []string          // reused agents whose content_hash differs from source
}

// CopyAgentsForDeploy copies all agents referenced by sourceAppID's orchestrators
// into targetTenantID. Agents already present in the target tenant (matched by
// kind+namespace+name+version) are reused without modification.
//
// For canvas agents (implementation_type='canvas_a2a'), agent_runtime_specs is also
// copied so the agent is executable in the target tenant.
//
// Secrets (auth_token_encrypted and all *_api_key_encrypted columns) are intentionally
// NOT copied — the target tenant must supply their own credentials.
//
// Returns IDMap (old UUID → new/existing UUID), CopiedSlugs, ReusedSlugs, and
// ConflictSlugs (reused agents whose content_hash differs from the source).
func (d *DB) CopyAgentsForDeploy(ctx context.Context, sourceAppID, targetTenantID string) (CopyAgentsForDeployResult, error) {
	const agentIDsQ = `
SELECT DISTINCT unnest(ao.allowed_agent_ids)::text
FROM them.app_orchestrators ao
WHERE ao.application_id = $1::uuid
  AND ao.allowed_agent_ids IS NOT NULL`

	rows, err := d.q.Query(ctx, agentIDsQ, sourceAppID)
	if err != nil {
		return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: fetch agent ids: %w", err)
	}
	var srcIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			srcIDs = append(srcIDs, id)
		}
	}
	rows.Close()

	if len(srcIDs) == 0 {
		return CopyAgentsForDeployResult{}, nil
	}

	const agentQ = `
SELECT
    a.id::text, cd.kind, cd.namespace, cd.name, cd.version,
    cd.display_name, cd.description, cd.implementation_type,
    cd.configuration_schema, cd.default_config, cd.capabilities,
    cd.input_schema, cd.output_schema, cd.credential_schema,
    cd.status, cd.content_hash, cd.enabled, cd.published_at,
    a.slug, a.display_name, a.description, a.transport, a.endpoint_url,
    a.input_schema, a.timeout_seconds, a.max_concurrency, a.max_retries,
    a.agent_card, a.agent_card_url, a.skills, a.supports_streaming, a.supports_push,
    a.tags, a.icon, a.category
FROM them.agents a
JOIN them.component_definitions cd ON cd.id = a.id
WHERE a.id = ANY($1::uuid[])`

	agentRows, err := d.q.Query(ctx, agentQ, srcIDs)
	if err != nil {
		return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: fetch agent details: %w", err)
	}
	defer agentRows.Close()

	type agentToCopy struct {
		oldID                       string
		kind, ns, name              string
		version                     int
		cdDisplayName, cdDesc       string
		implType                    string
		configSchema, defaultConfig []byte
		capabilities                []byte
		inputSchemaCd, outputSchema []byte
		credSchema                  []byte
		status, hash                string
		enabled                     bool
		publishedAt                 *time.Time
		slug, aDisplayName, aDesc   string
		transport                   string
		endpointURL                 *string
		aInputSchema                []byte
		timeoutSec, maxConc         int
		maxRetry                    int
		agentCard                   []byte
		agentCardURL                *string
		skills                      []byte
		streaming, push             bool
		tags                        []string
		icon, category              *string
	}

	var agents []agentToCopy
	for agentRows.Next() {
		var ag agentToCopy
		if err := agentRows.Scan(
			&ag.oldID, &ag.kind, &ag.ns, &ag.name, &ag.version,
			&ag.cdDisplayName, &ag.cdDesc, &ag.implType,
			&ag.configSchema, &ag.defaultConfig, &ag.capabilities,
			&ag.inputSchemaCd, &ag.outputSchema, &ag.credSchema,
			&ag.status, &ag.hash, &ag.enabled, &ag.publishedAt,
			&ag.slug, &ag.aDisplayName, &ag.aDesc, &ag.transport, &ag.endpointURL,
			&ag.aInputSchema, &ag.timeoutSec, &ag.maxConc, &ag.maxRetry,
			&ag.agentCard, &ag.agentCardURL, &ag.skills, &ag.streaming, &ag.push,
			&ag.tags, &ag.icon, &ag.category,
		); err != nil {
			return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: scan: %w", err)
		}
		agents = append(agents, ag)
	}

	idMap := make(map[string]string, len(agents))
	var copiedSlugs, reusedSlugs, conflictSlugs []string

	// existsQ returns id + content_hash so we can detect conflicts.
	const existsQ = `
SELECT id::text, content_hash FROM them.component_definitions
WHERE kind=$1 AND namespace=$2 AND name=$3 AND version=$4 AND tenant_id=$5::uuid
LIMIT 1`

	const insertCD = `
INSERT INTO them.component_definitions
    (id, kind, namespace, name, version, display_name, description,
     implementation_type, configuration_schema, default_config, capabilities,
     input_schema, output_schema, credential_schema, scope, tenant_id,
     status, content_hash, enabled, created_at, published_at)
VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'tenant',$14,$15,$16,$17,now(),$18)
RETURNING id::text`

	const insertAgent = `
INSERT INTO them.agents
    (id, tenant_id, slug, display_name, description, transport, endpoint_url,
     input_schema, timeout_seconds, max_concurrency, max_retries, enabled,
     agent_card, agent_card_url, skills, supports_streaming, supports_push,
     tags, icon, category, namespace, version, scope, status, content_hash,
     created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,'tenant',$23,$24,now(),now())
ON CONFLICT (tenant_id, slug) DO NOTHING`

	// insertAgentDef copies the agent_definitions source row into the target tenant.
	// Required before inserting agent_runtime_specs due to FK agent_runtime_specs.definition_id → agent_definitions.id.
	// owner_id is set NULL — target tenant has different user IDs.
	const insertAgentDef = `
INSERT INTO them.agent_definitions
    (id, tenant_id, agent_slug, revision, definition, definition_hash, status, created_at, updated_at, owner_id)
SELECT $2::uuid, $1::uuid, agent_slug, revision, definition, definition_hash, status, now(), now(), NULL
FROM them.agent_definitions
WHERE id = $2::uuid
ON CONFLICT DO NOTHING`

	// insertSpec copies the compiled AgentSpec for canvas agents (implementation_type='canvas_a2a').
	// Secrets are not stored in agent_runtime_specs so no filtering is needed.
	// definition_id = agent_id (same UUID) as per canvas agent publish convention.
	const insertSpec = `
INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, agent_id, spec, spec_hash, deployed_at)
SELECT gen_random_uuid(), $1::uuid, $2::uuid, $2::uuid, spec, spec_hash, now()
FROM them.agent_runtime_specs
WHERE agent_id = $3::uuid
ON CONFLICT (definition_id) DO NOTHING`

	// ensureSpecQ inserts a missing spec for an already-existing canvas agent in the target.
	const ensureSpecQ = `
INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, agent_id, spec, spec_hash, deployed_at)
SELECT gen_random_uuid(), $1::uuid, $2::uuid, $2::uuid, spec, spec_hash, now()
FROM them.agent_runtime_specs
WHERE agent_id = $3::uuid
ON CONFLICT (definition_id) DO NOTHING`

	for _, ag := range agents {
		var existingID, existingHash string
		err := d.q.QueryRow(ctx, existsQ, ag.kind, ag.ns, ag.name, ag.version, targetTenantID).Scan(&existingID, &existingHash)
		if err == nil {
			// Agent already exists in target tenant.
			idMap[ag.oldID] = existingID
			reusedSlugs = append(reusedSlugs, ag.slug)
			if existingHash != ag.hash {
				conflictSlugs = append(conflictSlugs, ag.slug)
			}
			// For canvas agents: ensure agent_definitions + spec exist even when agent row is reused.
			if ag.implType == "canvas_a2a" {
				if err := d.q.Exec(ctx, insertAgentDef, targetTenantID, existingID); err != nil {
					return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: ensure agent_definition for reused canvas agent %s: %w", ag.slug, err)
				}
				if err := d.q.Exec(ctx, ensureSpecQ, targetTenantID, existingID, ag.oldID); err != nil {
					return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: ensure spec for reused canvas agent %s: %w", ag.slug, err)
				}
			}
			continue
		}

		// Agent not in target — insert component_definitions + agents + spec (canvas only).
		var newID string
		if err := d.q.QueryRow(ctx, insertCD,
			ag.kind, ag.ns, ag.name, ag.version,
			ag.cdDisplayName, ag.cdDesc, ag.implType,
			ag.configSchema, ag.defaultConfig, ag.capabilities,
			ag.inputSchemaCd, ag.outputSchema, ag.credSchema,
			targetTenantID, ag.status, ag.hash, ag.enabled, ag.publishedAt,
		).Scan(&newID); err != nil {
			return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: insert component_definition for %s: %w", ag.slug, err)
		}

		if err := d.q.Exec(ctx, insertAgent,
			newID, targetTenantID, ag.slug, ag.aDisplayName, ag.aDesc,
			ag.transport, ag.endpointURL, ag.aInputSchema,
			ag.timeoutSec, ag.maxConc, ag.maxRetry, ag.enabled,
			ag.agentCard, ag.agentCardURL, ag.skills, ag.streaming, ag.push,
			ag.tags, ag.icon, ag.category, ag.ns, ag.version, ag.status, ag.hash,
		); err != nil {
			return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: insert agent %s: %w", ag.slug, err)
		}

		if ag.implType == "canvas_a2a" {
			// Copy agent_definitions row first (FK required by agent_runtime_specs).
			if err := d.q.Exec(ctx, insertAgentDef, targetTenantID, newID); err != nil {
				return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: insert agent_definition for canvas agent %s: %w", ag.slug, err)
			}
			if err := d.q.Exec(ctx, insertSpec, targetTenantID, newID, ag.oldID); err != nil {
				return CopyAgentsForDeployResult{}, fmt.Errorf("copy agents: insert spec for canvas agent %s: %w", ag.slug, err)
			}
		}

		idMap[ag.oldID] = newID
		copiedSlugs = append(copiedSlugs, ag.slug)
	}

	return CopyAgentsForDeployResult{
		IDMap:         idMap,
		CopiedSlugs:   copiedSlugs,
		ReusedSlugs:   reusedSlugs,
		ConflictSlugs: conflictSlugs,
	}, nil
}

// DeployApplication atomically clones a source application into a target tenant.
// agentIDMap (old UUID → new UUID) is produced by CopyAgentsForDeploy; pass nil
// when the source app has no agents. provider_keys and app_mcp_credentials are
// intentionally NOT copied. Returns the newly created Application.
func (d *DB) DeployApplication(ctx context.Context, sourceAppID, targetTenantID string, agentIDMap map[string]string) (Application, error) {
	const cte = `
WITH src AS (
    SELECT name, slug, enabled, runtime_config, app_params, active_definition_id
    FROM them.applications
    WHERE id = $1::uuid
),
new_app AS (
    INSERT INTO them.applications
        (id, tenant_id, name, slug, enabled, provider_keys, runtime_config, app_params, created_at, updated_at)
    SELECT
        gen_random_uuid(), $2::uuid, name,
        slug || '-' || substr(md5(random()::text), 1, 6),
        enabled, '{}'::jsonb, runtime_config, app_params,
        now(), now()
    FROM src
    RETURNING id, name, slug, enabled
),
new_def AS (
    INSERT INTO them.application_definitions
        (id, application_id, tenant_id, revision, status, definition, definition_hash, created_at, published_at)
    SELECT
        gen_random_uuid(), (SELECT id FROM new_app), $2::uuid,
        ad.revision, ad.status, ad.definition, ad.definition_hash,
        now(), ad.published_at
    FROM them.application_definitions ad
    WHERE ad.id = (SELECT active_definition_id FROM src)
    RETURNING id
),
set_def AS (
    UPDATE them.applications
    SET active_definition_id = (SELECT id FROM new_def)
    WHERE id = (SELECT id FROM new_app)
    RETURNING id, active_definition_id
),
new_orchs AS (
    INSERT INTO them.app_orchestrators (
        id, application_id, orchestrator_id, name, node_id, kind, delegatable, display_name,
        system_prompt, allowed_agent_ids, llm_provider, llm_model, llm_api_key_encrypted,
        llm_base_url, max_iterations, max_parallel_tools, rate_limit_rpm, daily_budget_usd,
        voice_enabled, transcription_provider, transcription_model, transcription_api_key_encrypted,
        tts_enabled, tts_provider, tts_voice, tts_api_key_encrypted,
        memory_enabled, summarize_every_n_calls, memory_raw_fallback_n,
        summarizer_provider, summarizer_model, summarizer_api_key_encrypted,
        edges, history_window, budget_tokens, enabled, mcp_servers,
        created_at, updated_at
    )
    SELECT
        gen_random_uuid(), (SELECT id FROM new_app), ao.orchestrator_id,
        substr(ao.name, 1, 57) || '-' || substr((SELECT slug FROM new_app), length((SELECT slug FROM new_app))-5, 6),
        ao.node_id, ao.kind, ao.delegatable, ao.display_name,
        ao.system_prompt, ao.allowed_agent_ids, ao.llm_provider, ao.llm_model,
        NULL, ao.llm_base_url, ao.max_iterations, ao.max_parallel_tools, ao.rate_limit_rpm, ao.daily_budget_usd,
        ao.voice_enabled, ao.transcription_provider, ao.transcription_model, NULL,
        ao.tts_enabled, ao.tts_provider, ao.tts_voice, NULL,
        ao.memory_enabled, ao.summarize_every_n_calls, ao.memory_raw_fallback_n,
        ao.summarizer_provider, ao.summarizer_model, NULL,
        ao.edges, ao.history_window, ao.budget_tokens, ao.enabled, ao.mcp_servers,
        now(), now()
    FROM them.app_orchestrators ao
    WHERE ao.application_id = $1::uuid
    RETURNING id, node_id
),
new_eps AS (
    INSERT INTO them.entry_points
        (id, application_id, tenant_id, slug, entry_point_type, enabled,
         memory_enabled, summarize_every_n_calls, memory_raw_fallback_n, history_window,
         summarizer_provider, summarizer_model, llm_provider, llm_model,
         allowed_principals, app_orchestrator_id, created_at, updated_at)
    SELECT
        gen_random_uuid(), (SELECT id FROM new_app), $2::uuid,
        ep.slug, ep.entry_point_type, ep.enabled,
        COALESCE(ep.memory_enabled, false),
        COALESCE(ep.summarize_every_n_calls, 10),
        COALESCE(ep.memory_raw_fallback_n, 3),
        COALESCE(ep.history_window, 20),
        ep.summarizer_provider, ep.summarizer_model,
        ep.llm_provider, ep.llm_model,
        COALESCE(ep.allowed_principals, 'internal'),
        (SELECT no.id FROM new_orchs no WHERE no.node_id = (
            SELECT ao.node_id FROM them.app_orchestrators ao WHERE ao.id = ep.app_orchestrator_id
        )),
        now(), now()
    FROM them.entry_points ep
    WHERE ep.application_id = $1::uuid
    RETURNING id
)
SELECT
    na.id::text,
    na.name,
    na.slug,
    COALESCE(t.slug, ''),
    na.enabled,
    d.revision,
    d.status
FROM new_app na
JOIN them.tenants t ON t.id = $2::uuid
LEFT JOIN set_def sd ON sd.id = na.id
LEFT JOIN them.application_definitions d ON d.id = sd.active_definition_id`

	row := d.q.QueryRow(ctx, cte, sourceAppID, targetTenantID)
	a, err := scanApplication(row)
	if err != nil {
		return Application{}, err
	}

	// Remap allowed_agent_ids on the cloned orchestrators using the agent ID map.
	if len(agentIDMap) > 0 {
		const remapQ = `UPDATE them.app_orchestrators SET allowed_agent_ids=$2::uuid[] WHERE id=$1::uuid`
		orchs := d.listAppOrchSummaries(ctx, a.ID)
		for _, o := range orchs {
			if len(o.AllowedAgentIDs) == 0 {
				continue
			}
			remapped := make([]string, len(o.AllowedAgentIDs))
			for i, oldID := range o.AllowedAgentIDs {
				if newID, ok := agentIDMap[oldID]; ok {
					remapped[i] = newID
				} else {
					remapped[i] = oldID
				}
			}
			if err := d.q.Exec(ctx, remapQ, o.ID, remapped); err != nil {
				return Application{}, fmt.Errorf("deploy: remap agent ids for orch %s: %w", o.ID, err)
			}
		}
	}

	a.EntryPoints = d.ListEntryPoints(ctx, a.ID)
	a.AppOrchestrators = d.listAppOrchSummaries(ctx, a.ID)
	return a, nil
}
