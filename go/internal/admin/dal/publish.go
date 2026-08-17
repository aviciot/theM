package dal

import (
	"context"
)

// ── Publish-related row types ─────────────────────────────────────────────────

// PublishResult holds the outcome of a successful publish operation.
type PublishResult struct {
	DefinitionID   string `json:"definition_id"`
	Revision       int    `json:"revision"`
	DefinitionHash string `json:"definition_hash"`
}

// AppOrchestratorRow is the shape used to upsert an app_orchestrators projection row.
type AppOrchestratorRow struct {
	ApplicationID         string
	TenantID              string
	Name                  string   // immutable identifier — Temporal key
	InstanceID            string   // from definition JSON, used as node_id
	Kind                  string   // "standard"|"router"|"voice"
	Delegatable           bool
	LLMProvider           *string
	LLMModel              *string
	SystemPrompt          *string
	MaxIterations         int
	MaxParallelTools      int
	HistoryWindow         int
	BudgetTokens          *int
	AllowedAgentIDs       []string
	ComponentDefinitionID *string
	ComponentVersion      *int
	SourceDefinitionID    string
	SourceDefinitionHash  string
}

// EntryPointRow is the shape used to upsert an entry_points projection row.
type EntryPointRow struct {
	ApplicationID          string
	TenantID               string
	Slug                   string   // immutable external id
	InstanceID             string   // from definition JSON (not stored; used for wiring)
	EntryPointType         string   // "websocket"|"sse"|"voice"|"webrtc"|"a2a"
	AppOrchestratorID      *string  // resolved UUID — NULL if not bound yet
	AccessPolicy           []byte   // JSONB
	ConversationTokenLimit *int
	MaxConcurrentSessions  *int
	QueueTimeoutSeconds    *int
	QueueMessage           *string
	SourceDefinitionID     string
	SourceDefinitionHash   string
}

// ── PublishDefinition ─────────────────────────────────────────────────────────

// PublishDefinition atomically marks a draft definition as published and sets it
// as the active definition for the application.
//
// Preconditions (enforced by SQL):
//   - The definition row must exist with the given tenant + app.
//   - The status must be 'draft' (AND status='draft' in WHERE clause).
//
// Returns pgx.ErrNoRows if not found, wrong tenant/app, or not a draft.
// The caller (service) is responsible for all registry resolution and projection
// compilation before calling this — this method only persists the final state.
func (d *DB) PublishDefinition(ctx context.Context, tenantID, appID, defID, defHash string) (PublishResult, error) {
	const q = `
		UPDATE them.application_definitions
		   SET status      = 'published',
		       published_at = now()
		 WHERE id             = $1::uuid
		   AND application_id = $2::uuid
		   AND tenant_id      = $3::uuid
		   AND status         = 'draft'
		RETURNING id::text, revision, definition_hash`

	var res PublishResult
	row := d.q.ExecReturning(ctx, q, defID, appID, tenantID)
	if err := row.Scan(&res.DefinitionID, &res.Revision, &res.DefinitionHash); err != nil {
		return PublishResult{}, err
	}

	// Update the application's active_definition_id in a separate statement.
	// Both writes use the same Querier which may be a transaction.
	const updateApp = `
		UPDATE them.applications
		   SET active_definition_id = $1::uuid
		 WHERE id        = $2::uuid
		   AND tenant_id = $3::uuid`

	if err := d.q.Exec(ctx, updateApp, defID, appID, tenantID); err != nil {
		return PublishResult{}, err
	}

	return res, nil
}

// ── UpsertAppOrchestrator ─────────────────────────────────────────────────────

// UpsertAppOrchestrator creates or updates an app_orchestrator projection row.
// On conflict (application_id, name) it updates all mutable columns.
// Returns the orchestrator UUID.
func (d *DB) UpsertAppOrchestrator(ctx context.Context, row AppOrchestratorRow) (string, error) {
	// Build allowed_agent_ids as a text array literal for safe casting.
	// pgx/v5 does not expose pq.Array; we cast via PostgreSQL ARRAY constructor.
	const q = `
		INSERT INTO them.app_orchestrators
			(application_id, tenant_id, name, node_id, kind, delegatable,
			 llm_provider, llm_model, system_prompt,
			 max_iterations, max_parallel_tools, history_window, budget_tokens,
			 allowed_agent_ids,
			 component_definition_id, component_version,
			 source_definition_id, source_definition_hash,
			 enabled)
		VALUES
			($1::uuid, $2::uuid, $3, $4, $5, $6,
			 $7, $8, $9,
			 $10, $11, $12, $13,
			 $14::uuid[],
			 $15::uuid, $16,
			 $17::uuid, $18,
			 true)
		ON CONFLICT (application_id, name) DO UPDATE SET
			node_id               = EXCLUDED.node_id,
			kind                  = EXCLUDED.kind,
			delegatable           = EXCLUDED.delegatable,
			llm_provider          = EXCLUDED.llm_provider,
			llm_model             = EXCLUDED.llm_model,
			system_prompt         = EXCLUDED.system_prompt,
			max_iterations        = EXCLUDED.max_iterations,
			max_parallel_tools    = EXCLUDED.max_parallel_tools,
			history_window        = EXCLUDED.history_window,
			budget_tokens         = EXCLUDED.budget_tokens,
			allowed_agent_ids     = EXCLUDED.allowed_agent_ids,
			component_definition_id = EXCLUDED.component_definition_id,
			component_version     = EXCLUDED.component_version,
			source_definition_id  = EXCLUDED.source_definition_id,
			source_definition_hash = EXCLUDED.source_definition_hash,
			enabled               = true,
			updated_at            = now()
		RETURNING id::text`

	// Convert []string agent IDs to a PostgreSQL text array literal.
	agentIDs := stringSliceToTextArray(row.AllowedAgentIDs)

	var id string
	scanRow := d.q.ExecReturning(ctx, q,
		row.ApplicationID,
		row.TenantID,
		row.Name,
		row.InstanceID, // node_id
		row.Kind,
		row.Delegatable,
		row.LLMProvider,
		row.LLMModel,
		row.SystemPrompt,
		row.MaxIterations,
		row.MaxParallelTools,
		row.HistoryWindow,
		row.BudgetTokens,
		agentIDs, // allowed_agent_ids — text[] cast done in SQL
		row.ComponentDefinitionID,
		row.ComponentVersion,
		row.SourceDefinitionID,
		row.SourceDefinitionHash,
	)
	if err := scanRow.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// ── UpsertEntryPoint ──────────────────────────────────────────────────────────

// UpsertEntryPoint creates or updates an entry_point projection row.
// On conflict (tenant_id, slug) it updates all mutable columns.
// Returns the entry point UUID.
func (d *DB) UpsertEntryPoint(ctx context.Context, row EntryPointRow) (string, error) {
	const q = `
		INSERT INTO them.entry_points
			(application_id, tenant_id, slug, entry_point_type,
			 app_orchestrator_id,
			 access_policy, conversation_token_limit,
			 max_concurrent_sessions, queue_timeout_seconds, queue_message,
			 source_definition_id, source_definition_hash,
			 enabled)
		VALUES
			($1::uuid, $2::uuid, $3, $4,
			 $5::uuid,
			 $6::jsonb, $7,
			 $8, $9, $10,
			 $11::uuid, $12,
			 true)
		ON CONFLICT (tenant_id, slug) DO UPDATE SET
			application_id          = EXCLUDED.application_id,
			entry_point_type        = EXCLUDED.entry_point_type,
			app_orchestrator_id     = EXCLUDED.app_orchestrator_id,
			access_policy           = EXCLUDED.access_policy,
			conversation_token_limit = EXCLUDED.conversation_token_limit,
			max_concurrent_sessions = EXCLUDED.max_concurrent_sessions,
			queue_timeout_seconds   = EXCLUDED.queue_timeout_seconds,
			queue_message           = EXCLUDED.queue_message,
			source_definition_id    = EXCLUDED.source_definition_id,
			source_definition_hash  = EXCLUDED.source_definition_hash,
			enabled                 = true,
			updated_at              = now()
		RETURNING id::text`

	var id string
	scanRow := d.q.ExecReturning(ctx, q,
		row.ApplicationID,
		row.TenantID,
		row.Slug,
		row.EntryPointType,
		row.AppOrchestratorID,
		row.AccessPolicy,
		row.ConversationTokenLimit,
		row.MaxConcurrentSessions,
		row.QueueTimeoutSeconds,
		row.QueueMessage,
		row.SourceDefinitionID,
		row.SourceDefinitionHash,
	)
	if err := scanRow.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// ── DeactivateStaleOrchestrators ─────────────────────────────────────────────

// DeactivateStaleOrchestrators sets enabled=false for app_orchestrators that
// belong to the given application but whose source_definition_id != defID.
// Constrained to (application_id AND tenant_id) to prevent cross-app clobber.
func (d *DB) DeactivateStaleOrchestrators(ctx context.Context, tenantID, appID, defID string) error {
	const q = `
		UPDATE them.app_orchestrators
		   SET enabled    = false,
		       updated_at = now()
		 WHERE application_id     = $1::uuid
		   AND tenant_id          = $2::uuid
		   AND (source_definition_id IS NULL OR source_definition_id != $3::uuid)`

	return d.q.Exec(ctx, q, appID, tenantID, defID)
}

// ── DeactivateStaleEntryPoints ────────────────────────────────────────────────

// DeactivateStaleEntryPoints sets enabled=false for entry_points that
// belong to the given application but whose source_definition_id != defID.
// Constrained to (application_id AND tenant_id) to prevent cross-app clobber.
func (d *DB) DeactivateStaleEntryPoints(ctx context.Context, tenantID, appID, defID string) error {
	const q = `
		UPDATE them.entry_points
		   SET enabled    = false,
		       updated_at = now()
		 WHERE application_id     = $1::uuid
		   AND tenant_id          = $2::uuid
		   AND (source_definition_id IS NULL OR source_definition_id != $3::uuid)`

	return d.q.Exec(ctx, q, appID, tenantID, defID)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// stringSliceToTextArray converts a []string to a PostgreSQL text array literal
// of the form {"elem1","elem2",...}.  An empty or nil slice produces '{}'.
// The SQL column cast (::uuid[]) is in the query itself.
func stringSliceToTextArray(ss []string) string {
	if len(ss) == 0 {
		return "{}"
	}
	out := "{"
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		// Escape backslashes and double-quotes inside element values.
		escaped := ""
		for _, c := range s {
			switch c {
			case '"':
				escaped += `\"`
			case '\\':
				escaped += `\\`
			default:
				escaped += string(c)
			}
		}
		out += `"` + escaped + `"`
	}
	out += "}"
	return out
}
