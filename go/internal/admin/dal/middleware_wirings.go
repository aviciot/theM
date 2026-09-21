package dal

import (
	"context"
	"encoding/json"
)

// MiddlewareWiring represents one middleware_wirings row.
type MiddlewareWiring struct {
	ID             string          `json:"id"`
	ApplicationID  string          `json:"application_id"`
	AgentID        string          `json:"agent_id"`
	AgentSlug      string          `json:"agent_slug"`
	DefID          string          `json:"def_id"`
	DefSlug        string          `json:"def_slug"`
	Position       int             `json:"position"`
	ConfigOverride json.RawMessage `json:"config_override"`
	Enabled        bool            `json:"enabled"`
	NodeID         string          `json:"node_id,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// MiddlewareWiringInput is the request body for creating/updating a wiring.
type MiddlewareWiringInput struct {
	AgentID        string          `json:"agent_id"`
	DefSlug        string          `json:"def_slug"`
	Position       int             `json:"position"`
	ConfigOverride json.RawMessage `json:"config_override"`
	Enabled        *bool           `json:"enabled"`
	NodeID         string          `json:"node_id,omitempty"`
}

// ListMiddlewareWirings returns all wirings for an application.
func ListMiddlewareWirings(ctx context.Context, db Querier, appID string) ([]MiddlewareWiring, error) {
	const q = `
SELECT mw.id::text, mw.application_id::text, mw.agent_id::text,
       a.slug AS agent_slug,
       mw.def_id::text, md.slug AS def_slug,
       mw.position,
       COALESCE(mw.config_override, '{}'),
       mw.enabled,
       COALESCE(mw.node_id, ''),
       to_char(mw.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
       to_char(mw.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM   them.middleware_wirings mw
JOIN   them.agents           a  ON a.id  = mw.agent_id
JOIN   them.middleware_defs  md ON md.id = mw.def_id
WHERE  mw.application_id = $1::uuid
ORDER  BY mw.position, mw.created_at`

	rows, err := db.Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []MiddlewareWiring
	for rows.Next() {
		var w MiddlewareWiring
		var cfgRaw []byte
		if err := rows.Scan(
			&w.ID, &w.ApplicationID, &w.AgentID, &w.AgentSlug,
			&w.DefID, &w.DefSlug, &w.Position, &cfgRaw,
			&w.Enabled, &w.NodeID,
			&w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		w.ConfigOverride = cfgRaw
		out = append(out, w)
	}
	if out == nil {
		out = []MiddlewareWiring{}
	}
	return out, nil
}

// GetMiddlewareWiring returns one wiring row by ID, scoped to appID.
func GetMiddlewareWiring(ctx context.Context, db Querier, appID, wiringID string) (MiddlewareWiring, error) {
	const q = `
SELECT mw.id::text, mw.application_id::text, mw.agent_id::text,
       a.slug AS agent_slug,
       mw.def_id::text, md.slug AS def_slug,
       mw.position,
       COALESCE(mw.config_override, '{}'),
       mw.enabled,
       COALESCE(mw.node_id, ''),
       to_char(mw.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
       to_char(mw.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM   them.middleware_wirings mw
JOIN   them.agents           a  ON a.id  = mw.agent_id
JOIN   them.middleware_defs  md ON md.id = mw.def_id
WHERE  mw.id = $1::uuid AND mw.application_id = $2::uuid`

	var w MiddlewareWiring
	var cfgRaw []byte
	err := db.QueryRow(ctx, q, wiringID, appID).Scan(
		&w.ID, &w.ApplicationID, &w.AgentID, &w.AgentSlug,
		&w.DefID, &w.DefSlug, &w.Position, &cfgRaw,
		&w.Enabled, &w.NodeID,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return MiddlewareWiring{}, err
	}
	w.ConfigOverride = cfgRaw
	return w, nil
}

// CreateMiddlewareWiring inserts a new wiring row. Returns the created row.
func CreateMiddlewareWiring(ctx context.Context, db Querier, appID string, in MiddlewareWiringInput) (MiddlewareWiring, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	cfgRaw := in.ConfigOverride
	if len(cfgRaw) == 0 {
		cfgRaw = json.RawMessage("{}")
	}

	const q = `
INSERT INTO them.middleware_wirings
    (application_id, agent_id, def_id, position, config_override, enabled, node_id)
SELECT $1::uuid, $2::uuid, md.id, $3, $4::jsonb, $5,
       CASE WHEN $6 = '' THEN NULL ELSE $6 END
FROM   them.middleware_defs md
WHERE  md.slug = $7
RETURNING id::text`

	var id string
	if err := db.QueryRow(ctx, q,
		appID, in.AgentID, in.Position, cfgRaw, enabled, in.NodeID, in.DefSlug,
	).Scan(&id); err != nil {
		return MiddlewareWiring{}, err
	}
	return GetMiddlewareWiring(ctx, db, appID, id)
}

// UpdateMiddlewareWiring patches config_override, enabled, position, and node_id.
func UpdateMiddlewareWiring(ctx context.Context, db Querier, appID, wiringID string, in MiddlewareWiringInput) (MiddlewareWiring, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	cfgRaw := in.ConfigOverride
	if len(cfgRaw) == 0 {
		cfgRaw = json.RawMessage("{}")
	}

	const q = `
UPDATE them.middleware_wirings
SET    config_override = $3::jsonb,
       enabled         = $4,
       position        = $5,
       node_id         = CASE WHEN $6 = '' THEN NULL ELSE $6 END,
       updated_at      = now()
WHERE  id = $1::uuid AND application_id = $2::uuid`

	if err := db.Exec(ctx, q, wiringID, appID, cfgRaw, enabled, in.Position, in.NodeID); err != nil {
		return MiddlewareWiring{}, err
	}
	return GetMiddlewareWiring(ctx, db, appID, wiringID)
}

// DeleteMiddlewareWiring removes a wiring row scoped to appID.
func DeleteMiddlewareWiring(ctx context.Context, db Querier, appID, wiringID string) error {
	const q = `DELETE FROM them.middleware_wirings WHERE id = $1::uuid AND application_id = $2::uuid`
	return db.Exec(ctx, q, wiringID, appID)
}

// ListMiddlewareDefs returns all enabled middleware_defs (builtin + tenant-scoped).
func ListMiddlewareDefs(ctx context.Context, db Querier) ([]MiddlewareDefSummary, error) {
	const q = `
SELECT id::text, slug, kind, display_name, description, config, is_builtin, scope,
       COALESCE(emoji, ''), COALESCE(color, ''), COALESCE(bg_color, ''),
       edges, input_ports, output_ports, config_fields
FROM   them.middleware_defs
WHERE  enabled = true
ORDER  BY is_builtin DESC, slug`

	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []MiddlewareDefSummary
	for rows.Next() {
		var d MiddlewareDefSummary
		var cfgRaw, edgesRaw, inPortsRaw, outPortsRaw, configFieldsRaw []byte
		if err := rows.Scan(&d.ID, &d.Slug, &d.Kind, &d.DisplayName, &d.Description, &cfgRaw, &d.IsBuiltin, &d.Scope, &d.Emoji, &d.Color, &d.BgColor,
			&edgesRaw, &inPortsRaw, &outPortsRaw, &configFieldsRaw); err != nil {
			return nil, err
		}
		d.Config = cfgRaw
		if len(edgesRaw) > 0 {
			d.Edges = edgesRaw
		}
		if len(inPortsRaw) > 0 {
			d.InputPorts = inPortsRaw
		}
		if len(outPortsRaw) > 0 {
			d.OutputPorts = outPortsRaw
		}
		if len(configFieldsRaw) > 0 {
			d.ConfigFields = configFieldsRaw
		}
		out = append(out, d)
	}
	if out == nil {
		out = []MiddlewareDefSummary{}
	}
	return out, nil
}

// MiddlewareDefSummary is a lightweight view of a middleware_defs row.
type MiddlewareDefSummary struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
	IsBuiltin   bool            `json:"is_builtin"`
	Scope       string          `json:"scope"`
	Emoji       string          `json:"emoji,omitempty"`
	Color       string          `json:"color,omitempty"`
	BgColor     string          `json:"bg_color,omitempty"`
	// Node-contract fields (Phase 5, docs/NODE_REGISTRY_PLAN.md) — nullable in
	// the DB; nil until a row is seeded (see db/102_middleware_defs_node_contract.sql).
	// Raw JSON, not typed as nodedefs.EdgeRules/[]nodedefs.PortDef/etc., because
	// this package (internal/admin/dal) has no dependency on internal/nodedefs
	// today and this is the only caller — parse at the consumer if ever needed.
	Edges        json.RawMessage `json:"edges,omitempty"`
	InputPorts   json.RawMessage `json:"input_ports,omitempty"`
	OutputPorts  json.RawMessage `json:"output_ports,omitempty"`
	ConfigFields json.RawMessage `json:"config_fields,omitempty"`
}
