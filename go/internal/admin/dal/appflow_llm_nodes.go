package dal

import "context"

// GetActiveDefinitionJSON returns the raw definition JSON for the application's
// active (published) definition. Returns pgx.ErrNoRows when the application has
// no active definition.
func (d *DB) GetActiveDefinitionJSON(ctx context.Context, applicationID string) ([]byte, error) {
	const q = `
		SELECT ad.definition
		  FROM them.applications a
		  JOIN them.application_definitions ad ON ad.id = a.active_definition_id
		 WHERE a.id = $1::uuid`
	var defJSON []byte
	err := d.q.QueryRow(ctx, q, applicationID).Scan(&defJSON)
	return defJSON, err
}

// AppFlowLLMOverride is one stored provider+model override for an inline LLM
// node in an app canvas.
type AppFlowLLMOverride struct {
	NodeID   string
	Provider string
	Model    string
}

// ListAppFlowLLMOverrides returns all stored overrides for one application.
func (d *DB) ListAppFlowLLMOverrides(ctx context.Context, applicationID string) ([]AppFlowLLMOverride, error) {
	const q = `
		SELECT node_id, provider, model
		  FROM them.app_flow_llm_overrides
		 WHERE application_id = $1::uuid`
	rows, err := d.q.Query(ctx, q, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	overrides := make([]AppFlowLLMOverride, 0)
	for rows.Next() {
		var o AppFlowLLMOverride
		if err := rows.Scan(&o.NodeID, &o.Provider, &o.Model); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, nil
}

// UpsertAppFlowLLMOverride sets the provider+model override for one inline LLM
// node in an app canvas.
func (d *DB) UpsertAppFlowLLMOverride(ctx context.Context, applicationID, nodeID, provider, model string) error {
	const q = `
		INSERT INTO them.app_flow_llm_overrides (application_id, node_id, provider, model)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (application_id, node_id) DO UPDATE
		    SET provider = $3, model = $4, updated_at = now()`
	return d.q.Exec(ctx, q, applicationID, nodeID, provider, model)
}
