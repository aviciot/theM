package dal

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrDeployConflict is returned by CopyAgentsForDeploy when one or more agents
// already exist in the target tenant with a different content_hash. The deploy
// is aborted — no agents are copied. Callers must surface this to the user.
var ErrDeployConflict = errors.New("deploy: agent content conflict in target tenant")

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
	// Union app_orchestrators.allowed_agent_ids with app_agent_bindings.agent_id
	// so agents bound only via the bindings table (AppFlow apps with no delegating
	// orchestrator) are also copied into the target tenant.
	const agentIDsQ = `
SELECT DISTINCT agent_id::text FROM (
    SELECT unnest(ao.allowed_agent_ids) AS agent_id
    FROM them.app_orchestrators ao
    WHERE ao.application_id = $1::uuid
      AND ao.allowed_agent_ids IS NOT NULL
    UNION
    SELECT b.agent_id
    FROM them.app_agent_bindings b
    WHERE b.application_id = $1::uuid
      AND b.agent_id IS NOT NULL
) ids`

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

	// srcAgentDefExistsQ checks that the source agent_definitions row exists.
	const srcAgentDefExistsQ = `SELECT EXISTS(SELECT 1 FROM them.agent_definitions WHERE id = $1::uuid)`

	// insertAgentDef copies the source agent_definitions row into the target tenant
	// with a new target UUID. $1=targetTenantID, $2=targetID (new), $3=sourceID.
	// Required before insertSpec due to FK agent_runtime_specs.definition_id → agent_definitions.id.
	const insertAgentDef = `
INSERT INTO them.agent_definitions
    (id, tenant_id, agent_slug, revision, definition, definition_hash, status, created_at, updated_at, owner_id)
SELECT $2::uuid, $1::uuid, agent_slug, revision, definition, definition_hash, status, now(), now(), NULL
FROM them.agent_definitions
WHERE id = $3::uuid
ON CONFLICT (tenant_id, agent_slug, revision) DO NOTHING`

	// srcSpecExistsQ checks that the source agent_runtime_specs row exists.
	const srcSpecExistsQ = `SELECT EXISTS(SELECT 1 FROM them.agent_runtime_specs WHERE agent_id = $1::uuid)`

	// insertSpec copies the compiled AgentSpec from source agent to target agent.
	// $1=targetTenantID, $2=targetID, $3=sourceID.
	const insertSpec = `
INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, agent_id, spec, spec_hash, deployed_at)
SELECT gen_random_uuid(), $1::uuid, $2::uuid, $2::uuid, spec, spec_hash, now()
FROM them.agent_runtime_specs
WHERE agent_id = $3::uuid
ON CONFLICT (definition_id) DO NOTHING`

	// ── Phase 1: conflict detection — no writes ──────────────────────────────
	type existsResult struct {
		id   string
		hash string
	}
	existsCache := make(map[string]existsResult, len(agents)) // oldID → exists
	var conflictSlugs []string
	for _, ag := range agents {
		var res existsResult
		err := d.q.QueryRow(ctx, existsQ, ag.kind, ag.ns, ag.name, ag.version, targetTenantID).Scan(&res.id, &res.hash)
		if err == nil {
			existsCache[ag.oldID] = res
			if res.hash != ag.hash {
				conflictSlugs = append(conflictSlugs, ag.slug)
			}
		}
	}

	// Conflicts abort the deploy — caller sees ErrDeployConflict (wraps slug list in message).
	if len(conflictSlugs) > 0 {
		return CopyAgentsForDeployResult{ConflictSlugs: conflictSlugs},
			fmt.Errorf("%w: %v", ErrDeployConflict, conflictSlugs)
	}

	// copyCanvasAgentDeps copies agent_definitions and agent_runtime_specs from
	// sourceID into the target tenant under targetID. Fails if either source row
	// is missing — a missing spec indicates the source agent was never published
	// and the deploy must be aborted.
	copyCanvasAgentDeps := func(slug, sourceID, targetID string) error {
		var hasDef bool
		if err := d.q.QueryRow(ctx, srcAgentDefExistsQ, sourceID).Scan(&hasDef); err != nil {
			return fmt.Errorf("copy agents: check source agent_definition for %s: %w", slug, err)
		}
		if !hasDef {
			return fmt.Errorf("copy agents: source agent_definition missing for canvas agent %s (id=%s) — agent must be published before deploy", slug, sourceID)
		}
		var hasSpec bool
		if err := d.q.QueryRow(ctx, srcSpecExistsQ, sourceID).Scan(&hasSpec); err != nil {
			return fmt.Errorf("copy agents: check source spec for %s: %w", slug, err)
		}
		if !hasSpec {
			return fmt.Errorf("copy agents: source agent_runtime_spec missing for canvas agent %s (id=%s) — agent must be published before deploy", slug, sourceID)
		}
		if err := d.q.Exec(ctx, insertAgentDef, targetTenantID, targetID, sourceID); err != nil {
			return fmt.Errorf("copy agents: insert agent_definition for canvas agent %s: %w", slug, err)
		}
		if err := d.q.Exec(ctx, insertSpec, targetTenantID, targetID, sourceID); err != nil {
			return fmt.Errorf("copy agents: insert spec for canvas agent %s: %w", slug, err)
		}
		return nil
	}

	// ── Phase 2: writes ───────────────────────────────────────────────────────
	idMap := make(map[string]string, len(agents))
	var copiedSlugs, reusedSlugs []string

	for _, ag := range agents {
		if res, ok := existsCache[ag.oldID]; ok {
			// Agent already exists in target with matching hash — reuse.
			idMap[ag.oldID] = res.id
			reusedSlugs = append(reusedSlugs, ag.slug)
			// For canvas agents: ensure agent_definitions + spec are present in target.
			if ag.implType == "canvas_a2a" {
				if err := copyCanvasAgentDeps(ag.slug, ag.oldID, res.id); err != nil {
					return CopyAgentsForDeployResult{}, err
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
			if err := copyCanvasAgentDeps(ag.slug, ag.oldID, newID); err != nil {
				return CopyAgentsForDeployResult{}, err
			}
		}

		idMap[ag.oldID] = newID
		copiedSlugs = append(copiedSlugs, ag.slug)
	}

	return CopyAgentsForDeployResult{
		IDMap:         idMap,
		CopiedSlugs:   copiedSlugs,
		ReusedSlugs:   reusedSlugs,
		ConflictSlugs: conflictSlugs, // always nil here (conflicts abort above)
	}, nil
}

// DeployApplication atomically clones a source application into a target tenant.
// agentIDMap (old UUID → new UUID) is produced by CopyAgentsForDeploy; pass nil
// when the source app has no agents. provider_keys and app_mcp_credentials are
// intentionally NOT copied. app_params is copied with every secret entry
// stripped (docs/APP_EXPORT_IMPORT_INVESTIGATION.md §5): a secret entry is a
// JSON object carrying a "ct" (ciphertext) key (db/045_app_global_params.sql),
// a non-secret entry is a plain scalar — this was previously copied verbatim,
// leaking encrypted secret ciphertext into the target tenant. Returns the
// newly created Application.
func (d *DB) DeployApplication(ctx context.Context, sourceAppID, targetTenantID string, agentIDMap map[string]string) (Application, error) {
	const cte = `
WITH src AS (
    SELECT name, slug, enabled, runtime_config,
        COALESCE((
            SELECT jsonb_object_agg(key, value)
            FROM jsonb_each(app_params)
            WHERE NOT (jsonb_typeof(value) = 'object' AND value ? 'ct')
        ), '{}'::jsonb) AS app_params_filtered,
        active_definition_id
    FROM them.applications
    WHERE id = $1::uuid
),
new_app AS (
    INSERT INTO them.applications
        (id, tenant_id, name, slug, enabled, provider_keys, runtime_config, app_params, canvas, created_at, updated_at)
    SELECT
        gen_random_uuid(), $2::uuid, src.name,
        src.slug || '-' || substr(md5(random()::text), 1, 6),
        src.enabled, '{}'::jsonb, src.runtime_config, src.app_params_filtered,
        (SELECT a.canvas FROM them.applications a WHERE a.id = $1::uuid),
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

	if err := d.copyAppScopedConfigForDeploy(ctx, sourceAppID, a.ID, agentIDMap); err != nil {
		return Application{}, err
	}

	a.EntryPoints = d.ListEntryPoints(ctx, a.ID)
	a.AppOrchestrators = d.listAppOrchSummaries(ctx, a.ID)
	return a, nil
}

// appScopedConfigTable is one entry in the registry copyAppScopedConfigForDeploy
// (Phase 1, DB-to-DB deploy) and exportScopedConfig (Phase 2, file export) both
// walk. Adding a future application_id-scoped table to a deploy/export means
// adding one entry here — not writing a new bespoke CTE clause — per
// docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md's Phase 1 ("Option C").
//
// copySQL must be parameterized as: $1=sourceAppID, $2=newAppID. None of the
// 6 tables below carry their own tenant_id column — ownership is entirely via
// application_id, resolved through a join to them.applications by RLS policy
// (confirmed via \d on each table) — so no target-tenant parameter is needed.
//
// exportSQL must be parameterized as: $1=sourceAppID, and select exactly one
// to_jsonb(...) column per row, redacting the same secret columns copySQL
// already redacts (NULL for credential_bindings/credential_encrypted) — this
// is why export needs its own SELECT rather than reusing copySQL's RETURNING,
// which only returns id/agent_id, not the full row.
type appScopedConfigTable struct {
	name         string
	copySQL      string
	exportSQL    string
	remapAgentID bool // true if this table has an agent_id column needing agentIDMap remap
}

var appScopedConfigTables = []appScopedConfigTable{
	{
		name:         "middleware_wirings",
		remapAgentID: true,
		// node_id is the canvas-JSON instance_id, stable across environments —
		// no remap needed (docs/APP_EXPORT_IMPORT_INVESTIGATION.md §3). def_id
		// is a fixed platform-row FK (File Guard/PII/etc. builtin defs), copied
		// unchanged since these rows are expected to exist identically in every
		// environment via migrations, not something a deploy should clone.
		copySQL: `
INSERT INTO them.middleware_wirings
    (id, application_id, agent_id, def_id, position, config_override, enabled, node_id, component_definition_id, component_version)
SELECT
    gen_random_uuid(), $2::uuid, mw.agent_id, mw.def_id, mw.position, mw.config_override, mw.enabled, mw.node_id, mw.component_definition_id, mw.component_version
FROM them.middleware_wirings mw
WHERE mw.application_id = $1::uuid
RETURNING id, agent_id`,
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT mw.agent_id, md.slug AS def_slug, mw.position, mw.config_override, mw.enabled, mw.node_id
    FROM them.middleware_wirings mw
    JOIN them.middleware_defs md ON md.id = mw.def_id
    WHERE mw.application_id = $1::uuid
) t`,
	},
	{
		name:         "app_agent_bindings",
		remapAgentID: true,
		// credential_bindings intentionally excluded (Fernet ciphertext secrets,
		// docs/APP_EXPORT_IMPORT_INVESTIGATION.md §5) — the target must supply
		// its own credentials. config_overrides/agent_params/policies are
		// configured behavior and travel with the deploy.
		copySQL: `
INSERT INTO them.app_agent_bindings
    (id, application_id, agent_id, definition_id, credential_bindings, config_overrides, policies, agent_params, created_at, updated_at)
SELECT
    gen_random_uuid(), $2::uuid, b.agent_id, b.definition_id, '{}'::jsonb, b.config_overrides, b.policies, b.agent_params, now(), now()
FROM them.app_agent_bindings b
WHERE b.application_id = $1::uuid
RETURNING id, agent_id`,
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT b.agent_id, b.config_overrides, b.policies, b.agent_params
    FROM them.app_agent_bindings b
    WHERE b.application_id = $1::uuid
) t`,
	},
	{
		// The binding (which MCP server this app expects, by mcp_server_id — not
		// copied/remapped, since mcp_servers rows are tenant registrations that
		// must already exist identically in the target) travels; the credential
		// itself (credential_encrypted) does not, same reasoning as agent auth
		// tokens. auth_header_name is configured behavior and is kept.
		name: "app_mcp_credentials",
		copySQL: `
INSERT INTO them.app_mcp_credentials
    (id, application_id, mcp_server_id, credential_encrypted, auth_header_name, created_at, updated_at)
SELECT
    gen_random_uuid(), $2::uuid, c.mcp_server_id, NULL, c.auth_header_name, now(), now()
FROM them.app_mcp_credentials c
WHERE c.application_id = $1::uuid
  AND EXISTS (SELECT 1 FROM them.mcp_servers ms WHERE ms.id = c.mcp_server_id)`,
		// mcp_server_id travels as the server's SLUG, not its UUID — a UUID is
		// only stable within one environment; import must re-resolve by slug
		// in the target tenant (docs/APP_EXPORT_IMPORT_INVESTIGATION.md §2).
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT ms.slug AS mcp_server_slug, c.auth_header_name
    FROM them.app_mcp_credentials c
    JOIN them.mcp_servers ms ON ms.id = c.mcp_server_id
    WHERE c.application_id = $1::uuid
) t`,
	},
	{
		// node_id is the canvas-JSON instance_id — string-stable, no remap.
		name: "app_flow_llm_overrides",
		copySQL: `
INSERT INTO them.app_flow_llm_overrides (application_id, node_id, provider, model, updated_at)
SELECT $2::uuid, o.node_id, o.provider, o.model, now()
FROM them.app_flow_llm_overrides o
WHERE o.application_id = $1::uuid`,
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT o.node_id, o.provider, o.model
    FROM them.app_flow_llm_overrides o
    WHERE o.application_id = $1::uuid
) t`,
	},
	{
		name: "app_temporal_config",
		copySQL: `
INSERT INTO them.app_temporal_config (application_id, max_concurrent_workflows, workflow_timeout_s, activity_timeout_s, retry_max_attempts, updated_at)
SELECT $2::uuid, c.max_concurrent_workflows, c.workflow_timeout_s, c.activity_timeout_s, c.retry_max_attempts, now()
FROM them.app_temporal_config c
WHERE c.application_id = $1::uuid`,
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT c.max_concurrent_workflows, c.workflow_timeout_s, c.activity_timeout_s, c.retry_max_attempts
    FROM them.app_temporal_config c
    WHERE c.application_id = $1::uuid
) t`,
	},
	{
		name: "app_debug_config",
		copySQL: `
INSERT INTO them.app_debug_config (application_id, log_verbosity, updated_at)
SELECT $2::uuid, c.log_verbosity, now()
FROM them.app_debug_config c
WHERE c.application_id = $1::uuid`,
		exportSQL: `
SELECT to_jsonb(t) FROM (
    SELECT c.log_verbosity
    FROM them.app_debug_config c
    WHERE c.application_id = $1::uuid
) t`,
	},
}

// copyAppScopedConfigForDeploy walks appScopedConfigTables and copies every
// application_id-scoped table's rows from sourceAppID into newAppID, remapping
// agent_id where the table carries one. This is the extension point for future
// canvas-configurable features that use their own DB table instead of the app
// JSON — see docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md.
func (d *DB) copyAppScopedConfigForDeploy(ctx context.Context, sourceAppID, newAppID string, agentIDMap map[string]string) error {
	for _, tbl := range appScopedConfigTables {
		if !tbl.remapAgentID {
			if err := d.q.Exec(ctx, tbl.copySQL, sourceAppID, newAppID); err != nil {
				return fmt.Errorf("deploy: copy %s: %w", tbl.name, err)
			}
			continue
		}

		rows, err := d.q.Query(ctx, tbl.copySQL, sourceAppID, newAppID)
		if err != nil {
			return fmt.Errorf("deploy: copy %s: %w", tbl.name, err)
		}
		type copiedRow struct {
			id      string
			agentID *string
		}
		var copied []copiedRow
		for rows.Next() {
			var r copiedRow
			if err := rows.Scan(&r.id, &r.agentID); err != nil {
				rows.Close()
				return fmt.Errorf("deploy: copy %s: scan: %w", tbl.name, err)
			}
			copied = append(copied, r)
		}
		rows.Close()

		if len(agentIDMap) == 0 {
			continue
		}
		remapQ := fmt.Sprintf(`UPDATE them.%s SET agent_id=$2::uuid WHERE id=$1::uuid`, tbl.name)
		for _, r := range copied {
			if r.agentID == nil {
				continue // llm-node wiring with no agent — nothing to remap
			}
			if newAgentID, ok := agentIDMap[*r.agentID]; ok {
				if err := d.q.Exec(ctx, remapQ, r.id, newAgentID); err != nil {
					return fmt.Errorf("deploy: remap agent_id for %s row %s: %w", tbl.name, r.id, err)
				}
			}
		}
	}
	return nil
}
