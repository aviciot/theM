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
	DisplayName    string          `json:"display_name"`
	Revision       int             `json:"revision"`
	Status         string          `json:"status"`
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
	OwnerID        *int            `json:"owner_id,omitempty"`
	OwnerUsername  string          `json:"owner_username,omitempty"`
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
// the new UUID. ownerID is the auth_service user id from the JWT (0 = unknown).
func (d *DB) CreateAgentDefinition(ctx context.Context, tenantID, agentSlug string, rev int, defJSON []byte, hash string, ownerID int) (string, error) {
	const q = `
		INSERT INTO them.agent_definitions
			(tenant_id, agent_slug, revision, status, definition, definition_hash, owner_id)
		VALUES ($1::uuid, $2, $3, 'draft', $4::jsonb, $5, NULLIF($6, 0))
		RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, tenantID, agentSlug, rev, defJSON, hash, ownerID)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetAgentDefinition returns a single agent definition row scoped to tenant.
// Returns pgx.ErrNoRows when not found or when it belongs to another tenant.
func (d *DB) GetAgentDefinition(ctx context.Context, tenantID, id string) (AgentDefinition, error) {
	const q = `
		SELECT ad.id::text, ad.tenant_id::text, ad.agent_slug,
		       COALESCE(ad.display_name, ad.agent_slug) AS display_name,
		       ad.revision, ad.status,
		       ad.definition, ad.definition_hash,
		       ad.owner_id, COALESCE(u.username, '') AS owner_username,
		       to_char(ad.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       to_char(ad.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS updated_at
		  FROM them.agent_definitions ad
		  LEFT JOIN auth_service.users u ON u.id = ad.owner_id
		 WHERE ad.id=$2::uuid AND ad.tenant_id=$1::uuid`

	var def AgentDefinition
	row := d.q.QueryRow(ctx, q, tenantID, id)
	if err := row.Scan(
		&def.ID,
		&def.TenantID,
		&def.AgentSlug,
		&def.DisplayName,
		&def.Revision,
		&def.Status,
		&def.Definition,
		&def.DefinitionHash,
		&def.OwnerID,
		&def.OwnerUsername,
		&def.CreatedAt,
		&def.UpdatedAt,
	); err != nil {
		return def, err
	}
	return def, nil
}

// ListAgentDefinitions returns all agent definitions for the given tenant,
// ordered by updated_at descending. Definition JSONB is omitted for performance
// — use GetAgentDefinition to load the full definition.
func (d *DB) ListAgentDefinitions(ctx context.Context, tenantID string) ([]AgentDefinition, error) {
	const q = `
		SELECT ad.id::text, ad.tenant_id::text, ad.agent_slug,
		       COALESCE(ad.display_name, ad.agent_slug) AS display_name,
		       ad.revision, ad.status,
		       NULL::jsonb AS definition, ad.definition_hash,
		       ad.owner_id, COALESCE(u.username, '') AS owner_username,
		       to_char(ad.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       to_char(ad.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS updated_at
		  FROM them.agent_definitions ad
		  LEFT JOIN auth_service.users u ON u.id = ad.owner_id
		 WHERE ad.tenant_id=$1::uuid
		 ORDER BY ad.updated_at DESC`

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
			&def.DisplayName,
			&def.Revision,
			&def.Status,
			&def.Definition,
			&def.DefinitionHash,
			&def.OwnerID,
			&def.OwnerUsername,
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
func (d *DB) DeleteDraftAgentDefinition(ctx context.Context, tenantID, id string) error {
	const q = `
		DELETE FROM them.agent_definitions
		 WHERE id=$2::uuid AND tenant_id=$1::uuid AND status='draft'
		 RETURNING id::text`

	var retID string
	return d.q.ExecReturning(ctx, q, tenantID, id).Scan(&retID)
}
