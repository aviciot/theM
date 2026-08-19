package dal

import (
	"context"
	"encoding/json"
)

// CanvasAgentRow carries all data needed to atomically publish a canvas agent
// into the three runtime tables: component_definitions, agents, agent_runtime_specs.
type CanvasAgentRow struct {
	AgentID       string // shared UUID across all three tables
	TenantID      string
	DefinitionID  string
	AgentSlug     string
	DisplayName   string
	Description   string
	Version       int
	ContentHash   string
	SpecJSON      []byte          // compiled AgentSpec JSONB
	SpecHash      string
	AgentCardJSON []byte          // agent_card JSONB for them.agents
	SkillsJSON    []byte          // skills JSONB for them.agents
	CredSchema    json.RawMessage // credential_schema for component_definitions
}

// GetAgentDefinitionForPublish returns a draft agent definition by tenant + ID.
// Returns pgx.ErrNoRows when not found or not owned by the tenant.
func (d *DB) GetAgentDefinitionForPublish(ctx context.Context, tenantID, id string) (AgentDefinition, error) {
	return d.GetAgentDefinition(ctx, tenantID, id)
}

// PublishCanvasAgent atomically inserts into component_definitions, agents, and
// agent_runtime_specs using a single multi-CTE query. On conflict (republish),
// component_definitions uses (kind,namespace,name,version); agents uses
// (tenant_id,slug); agent_runtime_specs uses (definition_id).
func (d *DB) PublishCanvasAgent(ctx context.Context, row CanvasAgentRow) error {
	const q = `
WITH cd AS (
    INSERT INTO them.component_definitions (
        id, kind, namespace, name, version,
        display_name, description, implementation_type,
        configuration_schema, default_config, capabilities,
        credential_schema, scope, tenant_id, status, content_hash
    ) VALUES (
        $1::uuid, 'agent', $9, $3, $4,
        $5, $6, 'canvas_a2a',
        '{}', '{}', '[]',
        $10::jsonb, 'tenant', $2::uuid, 'published', $7
    )
    ON CONFLICT (kind, namespace, name, version) DO UPDATE
        SET display_name  = EXCLUDED.display_name,
            description   = EXCLUDED.description,
            content_hash  = EXCLUDED.content_hash,
            credential_schema = EXCLUDED.credential_schema,
            published_at  = now()
    RETURNING id
),
ag AS (
    INSERT INTO them.agents (
        id, tenant_id, slug, display_name, description,
        transport, endpoint_url, input_schema,
        agent_card, skills,
        supports_streaming, supports_push,
        namespace, version, scope, status, content_hash
    )
    SELECT
        (SELECT id FROM cd),
        $2::uuid,
        $3,
        $5,
        $6,
        'canvas_a2a',
        '',
        '{}'::jsonb,
        $11::jsonb,
        $12::jsonb,
        false,
        false,
        $9,
        $4,
        'tenant',
        'published',
        $7
    ON CONFLICT (tenant_id, slug) DO UPDATE
        SET display_name    = EXCLUDED.display_name,
            description     = EXCLUDED.description,
            transport       = EXCLUDED.transport,
            agent_card      = EXCLUDED.agent_card,
            skills          = EXCLUDED.skills,
            content_hash    = EXCLUDED.content_hash,
            updated_at      = now()
    RETURNING id
),
ars AS (
    INSERT INTO them.agent_runtime_specs (
        id, tenant_id, definition_id, agent_id, spec, spec_hash, deployed_at
    )
    SELECT
        gen_random_uuid(),
        $2::uuid,
        $8::uuid,
        (SELECT id FROM ag),
        $13::jsonb,
        $14,
        now()
    ON CONFLICT (definition_id) DO UPDATE
        SET spec       = EXCLUDED.spec,
            spec_hash  = EXCLUDED.spec_hash,
            deployed_at = now()
)
SELECT 1`

	namespace := row.TenantID // use tenant_id as namespace for tenant-scoped agents
	return d.q.Exec(ctx, q,
		row.AgentID,          // $1
		row.TenantID,         // $2
		row.AgentSlug,        // $3
		row.Version,          // $4
		row.DisplayName,      // $5
		row.Description,      // $6
		row.ContentHash,      // $7
		row.DefinitionID,     // $8
		namespace,            // $9
		row.CredSchema,       // $10
		row.AgentCardJSON,    // $11
		row.SkillsJSON,       // $12
		row.SpecJSON,         // $13
		row.SpecHash,         // $14
	)
}

// MarkAgentDefinitionPublished updates agent_definitions.status to 'published'.
func (d *DB) MarkAgentDefinitionPublished(ctx context.Context, tenantID, id string) error {
	const q = `
		UPDATE them.agent_definitions
		   SET status='published', updated_at=now()
		 WHERE id=$2::uuid AND tenant_id=$1::uuid
		 RETURNING id::text`

	var retID string
	return d.q.ExecReturning(ctx, q, tenantID, id).Scan(&retID)
}
