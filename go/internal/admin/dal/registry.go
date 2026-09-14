package dal

import "context"

// ComponentDefinitionSummary is the shape returned by ListComponentDefinitions.
type ComponentDefinitionSummary struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Namespace          string `json:"namespace"`
	Name               string `json:"name"`
	Version            int    `json:"version"`
	DisplayName        string `json:"display_name"`
	Description        string `json:"description,omitempty"`
	ImplementationType string `json:"implementation_type"`
	Scope              string `json:"scope"`
	Status             string `json:"status"`
	Enabled            bool   `json:"enabled"`
}

// ResolveComponentIDByKindName finds the UUID of a published, enabled component
// in the given tenant by kind+name. Returns ("", nil) when not found.
func (d *DB) ResolveComponentIDByKindName(ctx context.Context, tenantID, kind, name string) (string, error) {
	const q = `
		SELECT id::text
		  FROM them.component_definitions
		 WHERE kind = $1 AND name = $2
		   AND (scope = 'builtin' OR tenant_id = $3::uuid)
		   AND enabled = true AND status = 'published'
		 ORDER BY (tenant_id = $3::uuid) DESC, version DESC
		 LIMIT 1`
	var id string
	err := d.q.QueryRow(ctx, q, kind, name, tenantID).Scan(&id)
	if err != nil {
		if IsNoRows(err) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

// ListComponentDefinitions returns all published, enabled component definitions
// accessible to the given tenant (builtins + tenant-owned).
func (d *DB) ListComponentDefinitions(ctx context.Context, tenantID string) ([]ComponentDefinitionSummary, error) {
	const q = `
		SELECT id::text, kind, namespace, name, version, display_name,
		       COALESCE(description, ''), implementation_type, scope, status, enabled
		  FROM them.component_definitions
		 WHERE status = 'published'
		   AND enabled = true
		   AND (scope = 'builtin' OR tenant_id = $1::uuid)
		 ORDER BY kind, display_name`

	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	defs := make([]ComponentDefinitionSummary, 0)
	for rows.Next() {
		var cd ComponentDefinitionSummary
		if err := rows.Scan(&cd.ID, &cd.Kind, &cd.Namespace, &cd.Name, &cd.Version,
			&cd.DisplayName, &cd.Description, &cd.ImplementationType, &cd.Scope, &cd.Status, &cd.Enabled); err != nil {
			return nil, err
		}
		defs = append(defs, cd)
	}
	return defs, nil
}
