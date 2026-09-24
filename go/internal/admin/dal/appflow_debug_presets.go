package dal

import (
	"context"
	"encoding/json"
)

// AppFlowDebugPreset is the DB row representation of them.appflow_debug_presets.
// LLMOverrides is the raw JSONB — any custom-mode API key inside it is already
// Fernet-encrypted at rest; masking/decryption happen in the service layer.
type AppFlowDebugPreset struct {
	ID             string
	TenantID       string
	UserID         int64
	ApplicationID  string
	Name           string
	EntryPointSlug string
	UserMessage    string
	StepMode       bool
	LLMOverrides   json.RawMessage
	CreatedAt      string
	UpdatedAt      string
}

// AppFlowDebugPresetInput is used for create/update.
type AppFlowDebugPresetInput struct {
	TenantID       string
	UserID         int64
	ApplicationID  string
	Name           string
	EntryPointSlug string
	UserMessage    string
	StepMode       bool
	LLMOverrides   json.RawMessage
}

const appFlowDebugPresetSelectCols = `
	SELECT id::text, tenant_id::text, user_id, application_id::text, name,
	       entry_point_slug, user_message, step_mode, llm_overrides,
	       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM them.appflow_debug_presets`

func scanAppFlowDebugPreset(r RowScanner) (AppFlowDebugPreset, error) {
	var p AppFlowDebugPreset
	var overrides []byte
	if err := r.Scan(
		&p.ID, &p.TenantID, &p.UserID, &p.ApplicationID, &p.Name,
		&p.EntryPointSlug, &p.UserMessage, &p.StepMode, &overrides,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return p, err
	}
	p.LLMOverrides = overrides
	return p, nil
}

// ListAppFlowDebugPresets returns every preset the given user saved for this
// application, ordered by name. Scoped to tenant + user + application — a
// preset is never visible outside the user who created it.
func (d *DB) ListAppFlowDebugPresets(ctx context.Context, tenantID string, userID int64, applicationID string) ([]AppFlowDebugPreset, error) {
	rows, err := d.q.Query(ctx,
		appFlowDebugPresetSelectCols+`
			WHERE tenant_id = $1::uuid AND user_id = $2 AND application_id = $3::uuid
			ORDER BY name ASC`,
		tenantID, userID, applicationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppFlowDebugPreset
	for rows.Next() {
		p, err := scanAppFlowDebugPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []AppFlowDebugPreset{}
	}
	return out, nil
}

// GetAppFlowDebugPreset returns one preset by ID, scoped to tenant + user.
// Returns pgx.ErrNoRows when missing or not owned by userID.
func (d *DB) GetAppFlowDebugPreset(ctx context.Context, id, tenantID string, userID int64) (AppFlowDebugPreset, error) {
	row := d.q.QueryRow(ctx,
		appFlowDebugPresetSelectCols+` WHERE id = $1::uuid AND tenant_id = $2::uuid AND user_id = $3`,
		id, tenantID, userID,
	)
	return scanAppFlowDebugPreset(&singleToRow{s: row})
}

// UpsertAppFlowDebugPreset creates or replaces (by name) a preset for
// (tenant, user, application, name).
func (d *DB) UpsertAppFlowDebugPreset(ctx context.Context, in AppFlowDebugPresetInput) (AppFlowDebugPreset, error) {
	const q = `
		INSERT INTO them.appflow_debug_presets
		  (tenant_id, user_id, application_id, name, entry_point_slug, user_message, step_mode, llm_overrides)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (tenant_id, user_id, application_id, name) DO UPDATE SET
		  entry_point_slug = EXCLUDED.entry_point_slug,
		  user_message     = EXCLUDED.user_message,
		  step_mode        = EXCLUDED.step_mode,
		  llm_overrides    = EXCLUDED.llm_overrides,
		  updated_at       = now()
		RETURNING id::text, tenant_id::text, user_id, application_id::text, name,
		          entry_point_slug, user_message, step_mode, llm_overrides,
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	row := d.q.ExecReturning(ctx, q,
		in.TenantID, in.UserID, in.ApplicationID, in.Name,
		in.EntryPointSlug, in.UserMessage, in.StepMode, []byte(in.LLMOverrides),
	)
	return scanAppFlowDebugPreset(&singleToRow{s: row})
}

// DeleteAppFlowDebugPreset deletes one preset, scoped to tenant + user.
// Returns pgx.ErrNoRows when missing or not owned by userID.
func (d *DB) DeleteAppFlowDebugPreset(ctx context.Context, id, tenantID string, userID int64) error {
	const q = `DELETE FROM them.appflow_debug_presets WHERE id=$1::uuid AND tenant_id=$2::uuid AND user_id=$3 RETURNING id`
	row := d.q.ExecReturning(ctx, q, id, tenantID, userID)
	var deleted string
	return row.Scan(&deleted)
}
