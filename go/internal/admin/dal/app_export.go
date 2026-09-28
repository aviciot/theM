package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ExportFileFormatVersion is the current envelope schema version. A future
// change to the envelope shape must bump this and either migrate or reject
// an older file at import time — see docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md
// Phase 2.
const ExportFileFormatVersion = 1

// ExportedApp is the full, portable, secrets-redacted export of one
// application — everything ExportApplication reads plus every agent it
// references, ready to be written to a JSON file and later fed into
// ImportApplication in a different tenant/environment with no live source
// app available.
type ExportedApp struct {
	ExportVersion int       `json:"export_version"`
	ExportedAt    time.Time `json:"exported_at"`

	Application           json.RawMessage      `json:"application"`
	ApplicationDefinition json.RawMessage      `json:"application_definition,omitempty"`
	EntryPoints           []json.RawMessage    `json:"entry_points"`
	AppOrchestrators      []json.RawMessage    `json:"app_orchestrators"`
	ScopedConfig          ExportedScopedConfig `json:"scoped_config"`
	Agents                []ExportedAgent      `json:"agents"`
}

// ExportedScopedConfig mirrors appScopedConfigTables (Phase 1) — one array
// per table, in the same registry order, keyed by table name so a future
// table addition to the registry is automatically exported with zero new
// struct fields (see exportSQL on appScopedConfigTable).
type ExportedScopedConfig struct {
	MiddlewareWirings   []json.RawMessage `json:"middleware_wirings"`
	AppAgentBindings    []json.RawMessage `json:"app_agent_bindings"`
	AppMCPCredentials   []json.RawMessage `json:"app_mcp_credentials"`
	AppFlowLLMOverrides []json.RawMessage `json:"app_flow_llm_overrides"`
	AppTemporalConfig   []json.RawMessage `json:"app_temporal_config"`
	AppDebugConfig      []json.RawMessage `json:"app_debug_config"`
}

// ExportedAgent bundles one agent referenced by the exported app's
// orchestrators, by value (not by reference) — there is no live target
// tenant to match against at export time, so the full row set travels in
// the file and the identity-match/conflict check happens at import time
// instead, against whichever tenant the file is being imported into.
type ExportedAgent struct {
	OldID               string          `json:"old_id"` // source-environment agent UUID, used only to remap agent_id references within this same file
	ComponentDefinition json.RawMessage `json:"component_definition"`
	Agent               json.RawMessage `json:"agent"`
	// AgentDefinition/AgentRuntimeSpec are set only for implementation_type
	// "canvas_a2a" agents (nil for a plain a2a_async agent, matching
	// CopyAgentsForDeploy's own copyCanvasAgentDeps gate).
	AgentDefinition  json.RawMessage `json:"agent_definition,omitempty"`
	AgentRuntimeSpec json.RawMessage `json:"agent_runtime_spec,omitempty"`
}

// applicationExportCols are the applications columns that travel with an
// export — provider_keys is excluded entirely (same as DeployApplication),
// id/tenant_id/active_definition_id are excluded because they get
// regenerated on import (active_definition_id is re-linked after the
// application_definition row is re-inserted, same two-step dependency
// DeployApplication's CTE already has via new_app/new_def/set_def).
const applicationExportCols = `name, enabled, conversation_token_limit, runtime_config, canvas, security_config,
    COALESCE((
        SELECT jsonb_object_agg(key, value)
        FROM jsonb_each(app_params)
        WHERE NOT (jsonb_typeof(value) = 'object' AND value ? 'ct')
    ), '{}'::jsonb) AS app_params`

// ExportApplication reads sourceAppID and everything it references into a
// single portable, secrets-redacted envelope. See docs/
// APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md Phase 2 for the design rationale
// (to_jsonb per table instead of ~10 hand-written Go structs).
func (d *DB) ExportApplication(ctx context.Context, sourceAppID string) (ExportedApp, error) {
	out := ExportedApp{
		ExportVersion: ExportFileFormatVersion,
		ExportedAt:    time.Now().UTC(),
	}

	appQ := fmt.Sprintf(`SELECT to_jsonb(t) FROM (SELECT %s FROM them.applications WHERE id = $1::uuid) t`, applicationExportCols)
	if err := d.q.QueryRow(ctx, appQ, sourceAppID).Scan(&out.Application); err != nil {
		return ExportedApp{}, fmt.Errorf("export: read application: %w", err)
	}

	const defQ = `
SELECT to_jsonb(t) FROM (
    SELECT ad.revision, ad.status, ad.definition, ad.definition_hash, ad.published_at
    FROM them.application_definitions ad
    JOIN them.applications a ON a.active_definition_id = ad.id
    WHERE a.id = $1::uuid
) t`
	var defRaw json.RawMessage
	if err := d.q.QueryRow(ctx, defQ, sourceAppID).Scan(&defRaw); err == nil {
		out.ApplicationDefinition = defRaw
	} else if !IsNoRows(err) {
		return ExportedApp{}, fmt.Errorf("export: read application_definition: %w", err)
	}

	const epQ = `
SELECT to_jsonb(t) FROM (
    SELECT ep.slug, ep.entry_point_type, ep.enabled,
        ep.memory_enabled, ep.summarize_every_n_calls, ep.memory_raw_fallback_n, ep.history_window,
        ep.summarizer_provider, ep.summarizer_model, ep.llm_provider, ep.llm_model,
        ep.allowed_principals,
        (SELECT ao.node_id FROM them.app_orchestrators ao WHERE ao.id = ep.app_orchestrator_id) AS app_orchestrator_node_id
    FROM them.entry_points ep
    WHERE ep.application_id = $1::uuid
) t`
	epRows, err := d.q.Query(ctx, epQ, sourceAppID)
	if err != nil {
		return ExportedApp{}, fmt.Errorf("export: read entry_points: %w", err)
	}
	out.EntryPoints, err = scanRawMessages(epRows)
	if err != nil {
		return ExportedApp{}, fmt.Errorf("export: scan entry_points: %w", err)
	}

	// api_key_encrypted columns explicitly NULL — same redaction
	// DeployApplication's CTE already applies (app_deploy.go's new_orchs).
	const orchQ = `
SELECT to_jsonb(t) FROM (
    SELECT ao.orchestrator_id, ao.name, ao.node_id, ao.kind, ao.delegatable, ao.display_name,
        ao.system_prompt, ao.allowed_agent_ids, ao.llm_provider, ao.llm_model,
        ao.llm_base_url, ao.max_iterations, ao.max_parallel_tools, ao.rate_limit_rpm, ao.daily_budget_usd,
        ao.voice_enabled, ao.transcription_provider, ao.transcription_model,
        ao.tts_enabled, ao.tts_provider, ao.tts_voice,
        ao.memory_enabled, ao.summarize_every_n_calls, ao.memory_raw_fallback_n,
        ao.summarizer_provider, ao.summarizer_model,
        ao.edges, ao.history_window, ao.budget_tokens, ao.enabled, ao.mcp_servers
    FROM them.app_orchestrators ao
    WHERE ao.application_id = $1::uuid
) t`
	orchRows, err := d.q.Query(ctx, orchQ, sourceAppID)
	if err != nil {
		return ExportedApp{}, fmt.Errorf("export: read app_orchestrators: %w", err)
	}
	out.AppOrchestrators, err = scanRawMessages(orchRows)
	if err != nil {
		return ExportedApp{}, fmt.Errorf("export: scan app_orchestrators: %w", err)
	}

	sc, err := d.exportScopedConfig(ctx, sourceAppID)
	if err != nil {
		return ExportedApp{}, err
	}
	out.ScopedConfig = sc

	agents, err := d.exportAgents(ctx, sourceAppID)
	if err != nil {
		return ExportedApp{}, err
	}
	out.Agents = agents

	return out, nil
}

// exportScopedConfig reads the 6 Phase-1 tables via their exportSQL, secrets
// already redacted identically to appScopedConfigTables' copySQL.
func (d *DB) exportScopedConfig(ctx context.Context, sourceAppID string) (ExportedScopedConfig, error) {
	var out ExportedScopedConfig
	for _, tbl := range appScopedConfigTables {
		rows, err := d.q.Query(ctx, tbl.exportSQL, sourceAppID)
		if err != nil {
			return ExportedScopedConfig{}, fmt.Errorf("export: read %s: %w", tbl.name, err)
		}
		msgs, err := scanRawMessages(rows)
		if err != nil {
			return ExportedScopedConfig{}, fmt.Errorf("export: scan %s: %w", tbl.name, err)
		}
		switch tbl.name {
		case "middleware_wirings":
			out.MiddlewareWirings = msgs
		case "app_agent_bindings":
			out.AppAgentBindings = msgs
		case "app_mcp_credentials":
			out.AppMCPCredentials = msgs
		case "app_flow_llm_overrides":
			out.AppFlowLLMOverrides = msgs
		case "app_temporal_config":
			out.AppTemporalConfig = msgs
		case "app_debug_config":
			out.AppDebugConfig = msgs
		default:
			return ExportedScopedConfig{}, fmt.Errorf("export: unrecognized scoped config table %q — add a case here when adding a registry entry", tbl.name)
		}
	}
	return out, nil
}

// exportAgents reads every agent referenced by sourceAppID's orchestrators,
// bundled by value. Mirrors CopyAgentsForDeploy's fetch logic but without
// any target-tenant existence/conflict check — that check moves to import
// time in ImportApplication, against whichever tenant is doing the import.
func (d *DB) exportAgents(ctx context.Context, sourceAppID string) ([]ExportedAgent, error) {
	const agentIDsQ = `
SELECT DISTINCT unnest(ao.allowed_agent_ids)::text
FROM them.app_orchestrators ao
WHERE ao.application_id = $1::uuid
  AND ao.allowed_agent_ids IS NOT NULL`

	rows, err := d.q.Query(ctx, agentIDsQ, sourceAppID)
	if err != nil {
		return nil, fmt.Errorf("export agents: fetch agent ids: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}

	const cdQ = `
SELECT a.id::text, to_jsonb(cd_row) , to_jsonb(a_row)
FROM them.agents a
JOIN them.component_definitions cd ON cd.id = a.id
CROSS JOIN LATERAL (SELECT cd.kind, cd.namespace, cd.name, cd.version, cd.display_name, cd.description,
    cd.implementation_type, cd.configuration_schema, cd.default_config, cd.capabilities,
    cd.input_schema, cd.output_schema, cd.credential_schema, cd.status, cd.content_hash, cd.enabled, cd.published_at) cd_row
CROSS JOIN LATERAL (SELECT a.slug, a.display_name, a.description, a.transport, a.endpoint_url,
    a.input_schema, a.timeout_seconds, a.max_concurrency, a.max_retries, a.enabled,
    a.agent_card, a.agent_card_url, a.skills, a.supports_streaming, a.supports_push,
    a.tags, a.icon, a.category, a.namespace, a.version, a.status, a.content_hash) a_row
WHERE a.id = ANY($1::uuid[])`

	cdRows, err := d.q.Query(ctx, cdQ, ids)
	if err != nil {
		return nil, fmt.Errorf("export agents: fetch details: %w", err)
	}
	defer cdRows.Close()

	var out []ExportedAgent
	for cdRows.Next() {
		var ea ExportedAgent
		if err := cdRows.Scan(&ea.OldID, &ea.ComponentDefinition, &ea.Agent); err != nil {
			return nil, fmt.Errorf("export agents: scan: %w", err)
		}
		out = append(out, ea)
	}

	// For canvas agents, also bundle agent_definitions + agent_runtime_specs.
	const defExistsAndFetchQ = `SELECT to_jsonb(t) FROM (SELECT agent_slug, revision, definition, definition_hash, status FROM them.agent_definitions WHERE id = $1::uuid) t`
	const specFetchQ = `SELECT to_jsonb(t) FROM (SELECT spec, spec_hash FROM them.agent_runtime_specs WHERE agent_id = $1::uuid) t`
	const isCanvasQ = `SELECT implementation_type = 'canvas_a2a' FROM them.component_definitions WHERE id = $1::uuid`
	const agentDefIDQ = `SELECT id::text FROM them.agent_definitions WHERE agent_slug = (SELECT slug FROM them.agents WHERE id = $1::uuid) AND tenant_id = (SELECT tenant_id FROM them.agents WHERE id = $1::uuid) ORDER BY revision DESC LIMIT 1`

	for i := range out {
		var isCanvas bool
		if err := d.q.QueryRow(ctx, isCanvasQ, out[i].OldID).Scan(&isCanvas); err != nil || !isCanvas {
			continue
		}
		var defID string
		if err := d.q.QueryRow(ctx, agentDefIDQ, out[i].OldID).Scan(&defID); err != nil {
			continue // no published definition — export what exists; import will fail loudly if this agent is actually needed as canvas_a2a
		}
		var defRaw json.RawMessage
		if err := d.q.QueryRow(ctx, defExistsAndFetchQ, defID).Scan(&defRaw); err == nil {
			out[i].AgentDefinition = defRaw
		}
		var specRaw json.RawMessage
		if err := d.q.QueryRow(ctx, specFetchQ, out[i].OldID).Scan(&specRaw); err == nil {
			out[i].AgentRuntimeSpec = specRaw
		}
	}

	return out, nil
}

// scanRawMessages drains rows of a single to_jsonb(...) column into a slice.
func scanRawMessages(rows RowScanner) ([]json.RawMessage, error) {
	defer rows.Close() //nolint:errcheck
	var out []json.RawMessage
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

// ── Import ──────────────────────────────────────────────────────────────────
//
// ImportApplication is structurally "DeployApplication + CopyAgentsForDeploy
// combined, but every INSERT's values come from a decoded envelope instead of
// a live SELECT ... FROM them.applications WHERE id = $1" — same target
// shape, same agentIDMap/conflict-detection logic, different source of truth.
// See docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md Phase 2.

// importApplicationRow/importOrchestratorRow/etc. decode exactly the JSON
// shape ExportApplication's to_jsonb SELECTs produce (keys = column/alias
// names) — these are import-only, deliberately not shared with any
// admin-API-facing type, since the envelope's shape is Phase 2's OWN contract
// (export_version-checked), not the admin API's response shape.
type importApplicationRow struct {
	Name                   string          `json:"name"`
	Enabled                bool            `json:"enabled"`
	ConversationTokenLimit *int            `json:"conversation_token_limit"`
	RuntimeConfig          json.RawMessage `json:"runtime_config"`
	Canvas                 json.RawMessage `json:"canvas"`
	SecurityConfig         json.RawMessage `json:"security_config"`
	AppParams              json.RawMessage `json:"app_params"`
}

type importDefinitionRow struct {
	Revision       int             `json:"revision"`
	Status         string          `json:"status"`
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
	PublishedAt    *time.Time      `json:"published_at"`
}

type importEntryPointRow struct {
	Slug                  string  `json:"slug"`
	EntryPointType        string  `json:"entry_point_type"`
	Enabled               bool    `json:"enabled"`
	MemoryEnabled         bool    `json:"memory_enabled"`
	SummarizeEveryNCalls  int     `json:"summarize_every_n_calls"`
	MemoryRawFallbackN    int     `json:"memory_raw_fallback_n"`
	HistoryWindow         int     `json:"history_window"`
	SummarizerProvider    *string `json:"summarizer_provider"`
	SummarizerModel       *string `json:"summarizer_model"`
	LLMProvider           *string `json:"llm_provider"`
	LLMModel              *string `json:"llm_model"`
	AllowedPrincipals     string  `json:"allowed_principals"`
	AppOrchestratorNodeID *string `json:"app_orchestrator_node_id"`
}

type importOrchestratorRow struct {
	OrchestratorID        *string         `json:"orchestrator_id"`
	Name                  string          `json:"name"`
	NodeID                string          `json:"node_id"`
	Kind                  string          `json:"kind"`
	Delegatable           bool            `json:"delegatable"`
	DisplayName           string          `json:"display_name"`
	SystemPrompt          string          `json:"system_prompt"`
	AllowedAgentIDs       []string        `json:"allowed_agent_ids"`
	LLMProvider           *string         `json:"llm_provider"`
	LLMModel              *string         `json:"llm_model"`
	LLMBaseURL            *string         `json:"llm_base_url"`
	MaxIterations         int             `json:"max_iterations"`
	MaxParallelTools      int             `json:"max_parallel_tools"`
	RateLimitRPM          *int            `json:"rate_limit_rpm"`
	DailyBudgetUSD        *float64        `json:"daily_budget_usd"`
	VoiceEnabled          bool            `json:"voice_enabled"`
	TranscriptionProvider *string         `json:"transcription_provider"`
	TranscriptionModel    *string         `json:"transcription_model"`
	TTSEnabled            bool            `json:"tts_enabled"`
	TTSProvider           *string         `json:"tts_provider"`
	TTSVoice              *string         `json:"tts_voice"`
	MemoryEnabled         bool            `json:"memory_enabled"`
	SummarizeEveryNCalls  int             `json:"summarize_every_n_calls"`
	MemoryRawFallbackN    int             `json:"memory_raw_fallback_n"`
	SummarizerProvider    *string         `json:"summarizer_provider"`
	SummarizerModel       *string         `json:"summarizer_model"`
	Edges                 []string        `json:"edges"`
	HistoryWindow         int             `json:"history_window"`
	BudgetTokens          *int            `json:"budget_tokens"`
	Enabled               bool            `json:"enabled"`
	MCPServers            json.RawMessage `json:"mcp_servers"`
}

type importMiddlewareWiringRow struct {
	AgentID        *string         `json:"agent_id"`
	DefSlug        string          `json:"def_slug"`
	Position       int             `json:"position"`
	ConfigOverride json.RawMessage `json:"config_override"`
	Enabled        bool            `json:"enabled"`
	NodeID         *string         `json:"node_id"`
}

type importAgentBindingRow struct {
	AgentID         string          `json:"agent_id"`
	ConfigOverrides json.RawMessage `json:"config_overrides"`
	Policies        json.RawMessage `json:"policies"`
	AgentParams     json.RawMessage `json:"agent_params"`
}

type importMCPCredentialRow struct {
	MCPServerSlug  string `json:"mcp_server_slug"`
	AuthHeaderName string `json:"auth_header_name"`
}

type importLLMOverrideRow struct {
	NodeID   string `json:"node_id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type importTemporalConfigRow struct {
	MaxConcurrentWorkflows *int `json:"max_concurrent_workflows"`
	WorkflowTimeoutS       *int `json:"workflow_timeout_s"`
	ActivityTimeoutS       *int `json:"activity_timeout_s"`
	RetryMaxAttempts       *int `json:"retry_max_attempts"`
}

type importDebugConfigRow struct {
	LogVerbosity string `json:"log_verbosity"`
}

type importComponentDefinitionRow struct {
	Kind                string          `json:"kind"`
	Namespace           string          `json:"namespace"`
	Name                string          `json:"name"`
	Version             int             `json:"version"`
	DisplayName         string          `json:"display_name"`
	Description         string          `json:"description"`
	ImplementationType  string          `json:"implementation_type"`
	ConfigurationSchema json.RawMessage `json:"configuration_schema"`
	DefaultConfig       json.RawMessage `json:"default_config"`
	Capabilities        json.RawMessage `json:"capabilities"`
	InputSchema         json.RawMessage `json:"input_schema"`
	OutputSchema        json.RawMessage `json:"output_schema"`
	CredentialSchema    json.RawMessage `json:"credential_schema"`
	Status              string          `json:"status"`
	ContentHash         string          `json:"content_hash"`
	Enabled             bool            `json:"enabled"`
	PublishedAt         *time.Time      `json:"published_at"`
}

type importAgentRow struct {
	Slug              string          `json:"slug"`
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	Transport         string          `json:"transport"`
	EndpointURL       *string         `json:"endpoint_url"`
	InputSchema       json.RawMessage `json:"input_schema"`
	TimeoutSeconds    int             `json:"timeout_seconds"`
	MaxConcurrency    int             `json:"max_concurrency"`
	MaxRetries        int             `json:"max_retries"`
	Enabled           bool            `json:"enabled"`
	AgentCard         json.RawMessage `json:"agent_card"`
	AgentCardURL      *string         `json:"agent_card_url"`
	Skills            json.RawMessage `json:"skills"`
	SupportsStreaming bool            `json:"supports_streaming"`
	SupportsPush      bool            `json:"supports_push"`
	Tags              []string        `json:"tags"`
	Icon              *string         `json:"icon"`
	Category          *string         `json:"category"`
	Namespace         string          `json:"namespace"`
	Version           int             `json:"version"`
	Status            string          `json:"status"`
	ContentHash       string          `json:"content_hash"`
}

type importAgentDefinitionRow struct {
	AgentSlug      string          `json:"agent_slug"`
	Revision       int             `json:"revision"`
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
	Status         string          `json:"status"`
}

type importAgentRuntimeSpecRow struct {
	Spec     json.RawMessage `json:"spec"`
	SpecHash string          `json:"spec_hash"`
}

// ImportApplication creates a brand-new application in targetTenantID from a
// previously-exported envelope, regenerating every UUID and remapping every
// reference to it — the same two-ID-family remap (application_id, agent_id)
// Phase 1's DeployApplication already proved sufficient
// (docs/APP_EXPORT_IMPORT_INVESTIGATION.md §3), just sourced from decoded
// JSON instead of a live SELECT. Returns ErrDeployConflict (same sentinel
// DeployApplication/CopyAgentsForDeploy use) if a bundled agent's identity
// already exists in the target tenant with different content.
func (d *DB) ImportApplication(ctx context.Context, env ExportedApp, targetTenantID string) (Application, error) {
	if env.ExportVersion != ExportFileFormatVersion {
		return Application{}, fmt.Errorf("import: unsupported export_version %d (this build supports %d)", env.ExportVersion, ExportFileFormatVersion)
	}

	agentIDMap, err := d.importAgents(ctx, env.Agents, targetTenantID)
	if err != nil {
		return Application{}, err
	}

	var appRow importApplicationRow
	if err := json.Unmarshal(env.Application, &appRow); err != nil {
		return Application{}, fmt.Errorf("import: decode application: %w", err)
	}

	const insertAppQ = `
INSERT INTO them.applications
    (id, tenant_id, name, slug, enabled, provider_keys, runtime_config, app_params, canvas, security_config, conversation_token_limit, created_at, updated_at)
VALUES
    (gen_random_uuid(), $1::uuid, $2,
     lower(regexp_replace($2, '[^a-z0-9_-]+', '-', 'g')) || '-' || substr(md5(random()::text), 1, 6),
     $3, '{}'::jsonb, COALESCE($4::jsonb, '{}'::jsonb), COALESCE($5::jsonb, '{}'::jsonb), $6::jsonb, COALESCE($7::jsonb, '{}'::jsonb), $8,
     now(), now())
RETURNING id::text`
	var newAppID string
	if err := d.q.QueryRow(ctx, insertAppQ,
		targetTenantID, appRow.Name, appRow.Enabled,
		nullableRaw(appRow.RuntimeConfig), nullableRaw(appRow.AppParams), nullableRaw(appRow.Canvas), nullableRaw(appRow.SecurityConfig),
		appRow.ConversationTokenLimit,
	).Scan(&newAppID); err != nil {
		return Application{}, fmt.Errorf("import: insert application: %w", err)
	}

	if len(env.ApplicationDefinition) > 0 {
		var defRow importDefinitionRow
		if err := json.Unmarshal(env.ApplicationDefinition, &defRow); err != nil {
			return Application{}, fmt.Errorf("import: decode application_definition: %w", err)
		}
		const insertDefQ = `
INSERT INTO them.application_definitions (id, application_id, tenant_id, revision, status, definition, definition_hash, created_at, published_at)
VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, now(), $7)
RETURNING id::text`
		var newDefID string
		if err := d.q.QueryRow(ctx, insertDefQ,
			newAppID, targetTenantID, defRow.Revision, defRow.Status, string(defRow.Definition), defRow.DefinitionHash, defRow.PublishedAt,
		).Scan(&newDefID); err != nil {
			return Application{}, fmt.Errorf("import: insert application_definition: %w", err)
		}
		if err := d.q.Exec(ctx, `UPDATE them.applications SET active_definition_id = $2::uuid WHERE id = $1::uuid`, newAppID, newDefID); err != nil {
			return Application{}, fmt.Errorf("import: link active_definition_id: %w", err)
		}
	}

	nodeIDToNewOrchID := make(map[string]string)
	for _, raw := range env.AppOrchestrators {
		var o importOrchestratorRow
		if err := json.Unmarshal(raw, &o); err != nil {
			return Application{}, fmt.Errorf("import: decode app_orchestrator: %w", err)
		}
		remappedAgentIDs := make([]string, 0, len(o.AllowedAgentIDs))
		for _, oldID := range o.AllowedAgentIDs {
			if newID, ok := agentIDMap[oldID]; ok {
				remappedAgentIDs = append(remappedAgentIDs, newID)
			} else {
				remappedAgentIDs = append(remappedAgentIDs, oldID)
			}
		}
		const insertOrchQ = `
INSERT INTO them.app_orchestrators
    (id, application_id, orchestrator_id, name, node_id, kind, delegatable, display_name,
     system_prompt, allowed_agent_ids, llm_provider, llm_model, llm_api_key_encrypted,
     llm_base_url, max_iterations, max_parallel_tools, rate_limit_rpm, daily_budget_usd,
     voice_enabled, transcription_provider, transcription_model, transcription_api_key_encrypted,
     tts_enabled, tts_provider, tts_voice, tts_api_key_encrypted,
     memory_enabled, summarize_every_n_calls, memory_raw_fallback_n,
     summarizer_provider, summarizer_model, summarizer_api_key_encrypted,
     edges, history_window, budget_tokens, enabled, mcp_servers, created_at, updated_at)
VALUES
    (gen_random_uuid(), $1::uuid, $2::uuid, $3, $4, $5, $6, $7,
     $8, $9::uuid[], $10, $11, NULL,
     $12, $13, $14, $15, $16,
     $17, $18, $19, NULL,
     $20, $21, $22, NULL,
     $23, $24, $25,
     $26, $27, NULL,
     $28::text[], $29, $30, $31, $32::jsonb, now(), now())
RETURNING id::text`
		var newOrchID string
		var orchestratorID any
		if o.OrchestratorID != nil {
			orchestratorID = *o.OrchestratorID
		}
		if err := d.q.QueryRow(ctx, insertOrchQ,
			newAppID, orchestratorID, o.Name, o.NodeID, o.Kind, o.Delegatable, o.DisplayName,
			o.SystemPrompt, remappedAgentIDs, o.LLMProvider, o.LLMModel,
			o.LLMBaseURL, o.MaxIterations, o.MaxParallelTools, o.RateLimitRPM, o.DailyBudgetUSD,
			o.VoiceEnabled, o.TranscriptionProvider, o.TranscriptionModel,
			o.TTSEnabled, o.TTSProvider, o.TTSVoice,
			o.MemoryEnabled, o.SummarizeEveryNCalls, o.MemoryRawFallbackN,
			o.SummarizerProvider, o.SummarizerModel,
			o.Edges, o.HistoryWindow, o.BudgetTokens, o.Enabled, nullableRaw(o.MCPServers),
		).Scan(&newOrchID); err != nil {
			return Application{}, fmt.Errorf("import: insert app_orchestrator %q: %w", o.Name, err)
		}
		nodeIDToNewOrchID[o.NodeID] = newOrchID
	}

	for _, raw := range env.EntryPoints {
		var ep importEntryPointRow
		if err := json.Unmarshal(raw, &ep); err != nil {
			return Application{}, fmt.Errorf("import: decode entry_point: %w", err)
		}
		var newOrchID any
		if ep.AppOrchestratorNodeID != nil {
			if id, ok := nodeIDToNewOrchID[*ep.AppOrchestratorNodeID]; ok {
				newOrchID = id
			}
		}
		const insertEPQ = `
INSERT INTO them.entry_points
    (id, application_id, tenant_id, slug, entry_point_type, enabled,
     memory_enabled, summarize_every_n_calls, memory_raw_fallback_n, history_window,
     summarizer_provider, summarizer_model, llm_provider, llm_model,
     allowed_principals, app_orchestrator_id, created_at, updated_at)
VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::uuid, now(), now())`
		if err := d.q.Exec(ctx, insertEPQ,
			newAppID, targetTenantID, ep.Slug, ep.EntryPointType, ep.Enabled,
			ep.MemoryEnabled, ep.SummarizeEveryNCalls, ep.MemoryRawFallbackN, ep.HistoryWindow,
			ep.SummarizerProvider, ep.SummarizerModel, ep.LLMProvider, ep.LLMModel,
			ep.AllowedPrincipals, newOrchID,
		); err != nil {
			return Application{}, fmt.Errorf("import: insert entry_point %q: %w", ep.Slug, err)
		}
	}

	if err := d.importScopedConfig(ctx, env.ScopedConfig, newAppID, agentIDMap); err != nil {
		return Application{}, err
	}

	newApp, err := d.GetApplication(ctx, targetTenantID, newAppID)
	if err != nil {
		return Application{}, fmt.Errorf("import: read back created application: %w", err)
	}
	return newApp, nil
}

// importAgents inserts every bundled agent into targetTenantID, matched by
// (kind, namespace, name, version) exactly like CopyAgentsForDeploy — reused
// if present with a matching content_hash, ErrDeployConflict if present with
// a different one, inserted fresh otherwise. Returns oldID → newID.
func (d *DB) importAgents(ctx context.Context, agents []ExportedAgent, targetTenantID string) (map[string]string, error) {
	idMap := make(map[string]string, len(agents))
	if len(agents) == 0 {
		return idMap, nil
	}

	const existsQ = `SELECT id::text, content_hash FROM them.component_definitions WHERE kind=$1 AND namespace=$2 AND name=$3 AND version=$4 AND tenant_id=$5::uuid LIMIT 1`
	const insertCD = `
INSERT INTO them.component_definitions
    (id, kind, namespace, name, version, display_name, description, implementation_type,
     configuration_schema, default_config, capabilities, input_schema, output_schema, credential_schema,
     scope, tenant_id, status, content_hash, enabled, created_at, published_at)
VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'tenant',$14,$15,$16,$17,now(),$18)
RETURNING id::text`
	const insertAgentQ = `
INSERT INTO them.agents
    (id, tenant_id, slug, display_name, description, transport, endpoint_url,
     input_schema, timeout_seconds, max_concurrency, max_retries, enabled,
     agent_card, agent_card_url, skills, supports_streaming, supports_push,
     tags, icon, category, namespace, version, scope, status, content_hash, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,'tenant',$23,$24,now(),now())
ON CONFLICT (tenant_id, slug) DO NOTHING`
	const insertAgentDefQ = `
INSERT INTO them.agent_definitions (id, tenant_id, agent_slug, revision, definition, definition_hash, status, created_at, updated_at, owner_id)
VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7, now(), now(), NULL)
ON CONFLICT (tenant_id, agent_slug) DO NOTHING`
	const insertSpecQ = `
INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, agent_id, spec, spec_hash, deployed_at)
VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $2::uuid, $3::jsonb, $4, now())
ON CONFLICT (definition_id) DO NOTHING`

	var conflicts []string
	for _, ea := range agents {
		var cd importComponentDefinitionRow
		if err := json.Unmarshal(ea.ComponentDefinition, &cd); err != nil {
			return nil, fmt.Errorf("import agents: decode component_definition for old id %s: %w", ea.OldID, err)
		}
		var existingID, existingHash string
		existsErr := d.q.QueryRow(ctx, existsQ, cd.Kind, cd.Namespace, cd.Name, cd.Version, targetTenantID).Scan(&existingID, &existingHash)
		if existsErr == nil {
			if existingHash != cd.ContentHash {
				conflicts = append(conflicts, cd.Name)
				continue
			}
			idMap[ea.OldID] = existingID
			continue
		}

		var a importAgentRow
		if err := json.Unmarshal(ea.Agent, &a); err != nil {
			return nil, fmt.Errorf("import agents: decode agent for old id %s: %w", ea.OldID, err)
		}

		var newID string
		if err := d.q.QueryRow(ctx, insertCD,
			cd.Kind, cd.Namespace, cd.Name, cd.Version, cd.DisplayName, cd.Description, cd.ImplementationType,
			nullableRaw(cd.ConfigurationSchema), nullableRaw(cd.DefaultConfig), nullableRaw(cd.Capabilities),
			nullableRaw(cd.InputSchema), nullableRaw(cd.OutputSchema), nullableRaw(cd.CredentialSchema),
			targetTenantID, cd.Status, cd.ContentHash, cd.Enabled, cd.PublishedAt,
		).Scan(&newID); err != nil {
			return nil, fmt.Errorf("import agents: insert component_definition %q: %w", cd.Name, err)
		}
		if err := d.q.Exec(ctx, insertAgentQ,
			newID, targetTenantID, a.Slug, a.DisplayName, a.Description, a.Transport, a.EndpointURL,
			nullableRaw(a.InputSchema), a.TimeoutSeconds, a.MaxConcurrency, a.MaxRetries, a.Enabled,
			nullableRaw(a.AgentCard), a.AgentCardURL, nullableRaw(a.Skills), a.SupportsStreaming, a.SupportsPush,
			a.Tags, a.Icon, a.Category, a.Namespace, a.Version, a.Status, a.ContentHash,
		); err != nil {
			return nil, fmt.Errorf("import agents: insert agent %q: %w", a.Slug, err)
		}

		if cd.ImplementationType == "canvas_a2a" {
			if len(ea.AgentDefinition) > 0 {
				var ad importAgentDefinitionRow
				if err := json.Unmarshal(ea.AgentDefinition, &ad); err != nil {
					return nil, fmt.Errorf("import agents: decode agent_definition for %q: %w", a.Slug, err)
				}
				if err := d.q.Exec(ctx, insertAgentDefQ, newID, targetTenantID, ad.AgentSlug, ad.Revision, string(ad.Definition), ad.DefinitionHash, ad.Status); err != nil {
					return nil, fmt.Errorf("import agents: insert agent_definition for %q: %w", a.Slug, err)
				}
			}
			if len(ea.AgentRuntimeSpec) > 0 {
				var spec importAgentRuntimeSpecRow
				if err := json.Unmarshal(ea.AgentRuntimeSpec, &spec); err != nil {
					return nil, fmt.Errorf("import agents: decode agent_runtime_spec for %q: %w", a.Slug, err)
				}
				if err := d.q.Exec(ctx, insertSpecQ, targetTenantID, newID, string(spec.Spec), spec.SpecHash); err != nil {
					return nil, fmt.Errorf("import agents: insert agent_runtime_spec for %q: %w", a.Slug, err)
				}
			}
		}

		idMap[ea.OldID] = newID
	}

	if len(conflicts) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrDeployConflict, conflicts)
	}
	return idMap, nil
}

// importScopedConfig inserts the 6 Phase-1 tables' rows from the decoded
// envelope into newAppID, remapping agent_id via agentIDMap and resolving
// def_slug/mcp_server_slug back to the target environment's real UUIDs.
func (d *DB) importScopedConfig(ctx context.Context, sc ExportedScopedConfig, newAppID string, agentIDMap map[string]string) error {
	remapAgent := func(oldID *string) any {
		if oldID == nil {
			return nil
		}
		if newID, ok := agentIDMap[*oldID]; ok {
			return newID
		}
		return *oldID
	}

	for _, raw := range sc.MiddlewareWirings {
		var w importMiddlewareWiringRow
		if err := json.Unmarshal(raw, &w); err != nil {
			return fmt.Errorf("import: decode middleware_wirings: %w", err)
		}
		var defID string
		if err := d.q.QueryRow(ctx, `SELECT id::text FROM them.middleware_defs WHERE slug = $1 LIMIT 1`, w.DefSlug).Scan(&defID); err != nil {
			return fmt.Errorf("import: middleware def %q not found in target environment (a required platform migration may be missing): %w", w.DefSlug, err)
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.middleware_wirings (application_id, agent_id, def_id, position, config_override, enabled, node_id)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb, $6, $7)`,
			newAppID, remapAgent(w.AgentID), defID, w.Position, nullableRaw(w.ConfigOverride), w.Enabled, w.NodeID,
		); err != nil {
			return fmt.Errorf("import: insert middleware_wirings: %w", err)
		}
	}

	for _, raw := range sc.AppAgentBindings {
		var b importAgentBindingRow
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("import: decode app_agent_bindings: %w", err)
		}
		agentID := b.AgentID
		if newID, ok := agentIDMap[b.AgentID]; ok {
			agentID = newID
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.app_agent_bindings (application_id, agent_id, credential_bindings, config_overrides, policies, agent_params)
			 VALUES ($1::uuid, $2::uuid, '{}'::jsonb, $3::jsonb, $4::jsonb, $5::jsonb)`,
			newAppID, agentID, nullableRaw(b.ConfigOverrides), nullableRaw(b.Policies), nullableRaw(b.AgentParams),
		); err != nil {
			return fmt.Errorf("import: insert app_agent_bindings: %w", err)
		}
	}

	for _, raw := range sc.AppMCPCredentials {
		var c importMCPCredentialRow
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("import: decode app_mcp_credentials: %w", err)
		}
		var mcpServerID string
		if err := d.q.QueryRow(ctx, `SELECT id::text FROM them.mcp_servers WHERE slug = $1 LIMIT 1`, c.MCPServerSlug).Scan(&mcpServerID); err != nil {
			continue // MCP server not registered in target env — skip, surfaced separately as a checklist item by the caller
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id, credential_encrypted, auth_header_name)
			 VALUES ($1::uuid, $2::uuid, NULL, $3)`,
			newAppID, mcpServerID, c.AuthHeaderName,
		); err != nil {
			return fmt.Errorf("import: insert app_mcp_credentials: %w", err)
		}
	}

	for _, raw := range sc.AppFlowLLMOverrides {
		var o importLLMOverrideRow
		if err := json.Unmarshal(raw, &o); err != nil {
			return fmt.Errorf("import: decode app_flow_llm_overrides: %w", err)
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.app_flow_llm_overrides (application_id, node_id, provider, model) VALUES ($1::uuid, $2, $3, $4)`,
			newAppID, o.NodeID, o.Provider, o.Model,
		); err != nil {
			return fmt.Errorf("import: insert app_flow_llm_overrides: %w", err)
		}
	}

	for _, raw := range sc.AppTemporalConfig {
		var c importTemporalConfigRow
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("import: decode app_temporal_config: %w", err)
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.app_temporal_config (application_id, max_concurrent_workflows, workflow_timeout_s, activity_timeout_s, retry_max_attempts)
			 VALUES ($1::uuid, $2, $3, $4, $5)`,
			newAppID, c.MaxConcurrentWorkflows, c.WorkflowTimeoutS, c.ActivityTimeoutS, c.RetryMaxAttempts,
		); err != nil {
			return fmt.Errorf("import: insert app_temporal_config: %w", err)
		}
	}

	for _, raw := range sc.AppDebugConfig {
		var c importDebugConfigRow
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("import: decode app_debug_config: %w", err)
		}
		if err := d.q.Exec(ctx,
			`INSERT INTO them.app_debug_config (application_id, log_verbosity) VALUES ($1::uuid, $2)`,
			newAppID, c.LogVerbosity,
		); err != nil {
			return fmt.Errorf("import: insert app_debug_config: %w", err)
		}
	}

	return nil
}

// nullableRaw returns nil for an empty/absent json.RawMessage (so a $N::jsonb
// bind parameter becomes SQL NULL, letting a COALESCE default apply) or the
// raw JSON string otherwise.
func nullableRaw(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return string(raw)
}
