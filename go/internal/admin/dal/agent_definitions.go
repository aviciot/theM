package dal

import (
	"context"
	"encoding/json"
)

// AgentDefinition is a row from them.agent_definitions.
type AgentDefinition struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	AgentSlug      string          `json:"agent_slug"`
	Revision       int             `json:"revision"`
	Status         string          `json:"status"`
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// GetNextAgentRevision returns COALESCE(MAX(revision),0)+1 for the given
// tenant + agent slug combination.
func (d *DB) GetNextAgentRevision(ctx context.Context, tenantID, agentSlug string) (int, error) {
	const q = `SELECT COALESCE(MAX(revision),0)+1 FROM them.agent_definitions WHERE tenant_id=$1::uuid AND agent_slug=$2`
	var rev int
	row := d.q.QueryRow(ctx, q, tenantID, agentSlug)
	if err := row.Scan(&rev); err != nil {
		return 0, err
	}
	return rev, nil
}

// CreateAgentDefinition inserts a new draft agent definition row and returns
// the new UUID.
func (d *DB) CreateAgentDefinition(ctx context.Context, tenantID, agentSlug string, rev int, defJSON []byte, hash string) (string, error) {
	const q = `
		INSERT INTO them.agent_definitions
			(tenant_id, agent_slug, revision, status, definition, definition_hash)
		VALUES ($1::uuid, $2, $3, 'draft', $4::jsonb, $5)
		RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, tenantID, agentSlug, rev, defJSON, hash)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetAgentDefinition returns a single agent definition row scoped to tenant.
// Returns pgx.ErrNoRows when not found or when it belongs to another tenant.
// Parameters: tenantID then id (matches the Dal interface order).
func (d *DB) GetAgentDefinition(ctx context.Context, tenantID, id string) (AgentDefinition, error) {
	const q = `
		SELECT id::text, tenant_id::text, agent_slug, revision, status,
		       definition, definition_hash,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS updated_at
		  FROM them.agent_definitions
		 WHERE id=$2::uuid AND tenant_id=$1::uuid`

	var def AgentDefinition
	row := d.q.QueryRow(ctx, q, tenantID, id)
	if err := row.Scan(
		&def.ID,
		&def.TenantID,
		&def.AgentSlug,
		&def.Revision,
		&def.Status,
		&def.Definition,
		&def.DefinitionHash,
		&def.CreatedAt,
		&def.UpdatedAt,
	); err != nil {
		return def, err
	}
	return def, nil
}

// ListAgentDefinitions returns all agent definitions for the given tenant,
// ordered by updated_at descending. Returns an empty (non-nil) slice when
// there are no rows.
func (d *DB) ListAgentDefinitions(ctx context.Context, tenantID string) ([]AgentDefinition, error) {
	const q = `
		SELECT id::text, tenant_id::text, agent_slug, revision, status,
		       definition, definition_hash,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS updated_at
		  FROM them.agent_definitions
		 WHERE tenant_id=$1::uuid
		 ORDER BY updated_at DESC`

	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	defs := make([]AgentDefinition, 0)
	for rows.Next() {
		var def AgentDefinition
		if err := rows.Scan(
			&def.ID,
			&def.TenantID,
			&def.AgentSlug,
			&def.Revision,
			&def.Status,
			&def.Definition,
			&def.DefinitionHash,
			&def.CreatedAt,
			&def.UpdatedAt,
		); err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// UpdateDraftAgentDefinition updates definition + hash for a draft agent
// definition row scoped to tenant. Returns pgx.ErrNoRows if the row is not
// found, does not belong to the tenant, or is not a draft (status != 'draft').
// Parameters: tenantID then id (matches the Dal interface order).
func (d *DB) UpdateDraftAgentDefinition(ctx context.Context, tenantID, id string, defJSON []byte, hash string) error {
	const q = `
		UPDATE them.agent_definitions
		   SET definition=$3::jsonb, definition_hash=$4, updated_at=now()
		 WHERE id=$2::uuid AND tenant_id=$1::uuid AND status='draft'
		 RETURNING id::text`

	var retID string
	return d.q.ExecReturning(ctx, q, tenantID, id, defJSON, hash).Scan(&retID)
}

// DeleteDraftAgentDefinition hard-deletes a draft agent definition row scoped
// to tenant. Returns pgx.ErrNoRows if the row is not found, does not belong
// to the tenant, or is not a draft (status != 'draft').
// Parameters: tenantID then id (matches the Dal interface order).
func (d *DB) DeleteDraftAgentDefinition(ctx context.Context, tenantID, id string) error {
	const q = `
		DELETE FROM them.agent_definitions
		 WHERE id=$2::uuid AND tenant_id=$1::uuid AND status='draft'
		 RETURNING id::text`

	var retID string
	return d.q.ExecReturning(ctx, q, tenantID, id).Scan(&retID)
}
