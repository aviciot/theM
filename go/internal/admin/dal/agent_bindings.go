package dal

import (
	"context"
	"encoding/json"

	"github.com/aviciot/them/internal/agentgen"
)

// AgentBindingRow is the persisted shape for UpsertAgentBinding.
// CredentialBindingsJSON contains Fernet-encrypted values (NEVER plaintext).
type AgentBindingRow struct {
	ApplicationID          string
	AgentID                string
	DefinitionID           *string
	CredentialBindingsJSON []byte
	ConfigOverridesJSON    []byte
	PoliciesJSON           []byte
}

// AgentBindingSlotStatus is returned by GetAgentBindingStatus and
// ListAgentBindings. CredentialSet maps slot_name → true if ciphertext is
// present. NEVER returns the ciphertext or plaintext values.
type AgentBindingSlotStatus struct {
	ID            string
	ApplicationID string
	AgentID       string
	DefinitionID  *string
	CredentialSet map[string]bool
	ConfigOverrides json.RawMessage
	Policies        json.RawMessage
	CreatedAt       string
	UpdatedAt       string
}

// UpsertAgentBinding inserts or updates an app↔agent binding.
func (d *DB) UpsertAgentBinding(ctx context.Context, row AgentBindingRow) error {
	defID := row.DefinitionID
	credJSON := row.CredentialBindingsJSON
	if credJSON == nil {
		credJSON = []byte("{}")
	}
	cfgJSON := row.ConfigOverridesJSON
	if cfgJSON == nil {
		cfgJSON = []byte("{}")
	}
	polJSON := row.PoliciesJSON
	if polJSON == nil {
		polJSON = []byte("{}")
	}

	if defID != nil {
		const q = `
			INSERT INTO them.app_agent_bindings
				(application_id, agent_id, definition_id, credential_bindings, config_overrides, policies)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5::jsonb, $6::jsonb)
			ON CONFLICT (application_id, agent_id) DO UPDATE
				SET definition_id       = EXCLUDED.definition_id,
				    credential_bindings = EXCLUDED.credential_bindings,
				    config_overrides    = EXCLUDED.config_overrides,
				    policies            = EXCLUDED.policies,
				    updated_at          = now()`
		return d.q.Exec(ctx, q, row.ApplicationID, row.AgentID, *defID, credJSON, cfgJSON, polJSON)
	}

	const q = `
		INSERT INTO them.app_agent_bindings
			(application_id, agent_id, credential_bindings, config_overrides, policies)
		VALUES ($1::uuid, $2::uuid, $3::jsonb, $4::jsonb, $5::jsonb)
		ON CONFLICT (application_id, agent_id) DO UPDATE
			SET credential_bindings = EXCLUDED.credential_bindings,
			    config_overrides    = EXCLUDED.config_overrides,
			    policies            = EXCLUDED.policies,
			    updated_at          = now()`
	return d.q.Exec(ctx, q, row.ApplicationID, row.AgentID, credJSON, cfgJSON, polJSON)
}

// GetAgentBindingStatus returns the binding status for one app↔agent pair.
// Returns pgx.ErrNoRows when not found.
func (d *DB) GetAgentBindingStatus(ctx context.Context, applicationID, agentID string) (AgentBindingSlotStatus, error) {
	const q = `
		SELECT id::text, application_id::text, agent_id::text,
		       definition_id::text,
		       credential_bindings, config_overrides, policies,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM them.app_agent_bindings
		 WHERE application_id=$1::uuid AND agent_id=$2::uuid`

	var (
		s               AgentBindingSlotStatus
		defIDStr        *string
		credBindingsRaw []byte
		cfgRaw          []byte
		polRaw          []byte
	)
	row := d.q.QueryRow(ctx, q, applicationID, agentID)
	if err := row.Scan(
		&s.ID,
		&s.ApplicationID,
		&s.AgentID,
		&defIDStr,
		&credBindingsRaw,
		&cfgRaw,
		&polRaw,
		&s.CreatedAt,
		&s.UpdatedAt,
	); err != nil {
		return s, err
	}

	s.DefinitionID = defIDStr
	s.ConfigOverrides = cfgRaw
	s.Policies = polRaw
	s.CredentialSet = credentialSetFromJSON(credBindingsRaw)
	return s, nil
}

// ListAgentBindings returns all bindings for an application, slot-set status only.
func (d *DB) ListAgentBindings(ctx context.Context, applicationID string) ([]AgentBindingSlotStatus, error) {
	const q = `
		SELECT id::text, application_id::text, agent_id::text,
		       definition_id::text,
		       credential_bindings, config_overrides, policies,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM them.app_agent_bindings
		 WHERE application_id=$1::uuid
		 ORDER BY created_at`

	rows, err := d.q.Query(ctx, q, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]AgentBindingSlotStatus, 0)
	for rows.Next() {
		var (
			s               AgentBindingSlotStatus
			defIDStr        *string
			credBindingsRaw []byte
			cfgRaw          []byte
			polRaw          []byte
		)
		if err := rows.Scan(
			&s.ID,
			&s.ApplicationID,
			&s.AgentID,
			&defIDStr,
			&credBindingsRaw,
			&cfgRaw,
			&polRaw,
			&s.CreatedAt,
			&s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		s.DefinitionID = defIDStr
		s.ConfigOverrides = cfgRaw
		s.Policies = polRaw
		s.CredentialSet = credentialSetFromJSON(credBindingsRaw)
		result = append(result, s)
	}
	return result, nil
}

// DeleteAgentBinding removes the binding for an app↔agent pair.
// Returns pgx.ErrNoRows when not found.
func (d *DB) DeleteAgentBinding(ctx context.Context, applicationID, agentID string) error {
	const q = `
		DELETE FROM them.app_agent_bindings
		 WHERE application_id=$1::uuid AND agent_id=$2::uuid
		 RETURNING id::text`

	var id string
	return d.q.ExecReturning(ctx, q, applicationID, agentID).Scan(&id)
}

// credentialSetFromJSON converts a credential_bindings JSON object into a
// bool map (slot_name → true if set). NEVER returns values.
func credentialSetFromJSON(raw []byte) map[string]bool {
	result := make(map[string]bool)
	if len(raw) == 0 {
		return result
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return result
	}
	for k, v := range m {
		result[k] = v != ""
	}
	return result
}

// AgentParamsRow is the result of GetAgentParamsForBinding.
type AgentParamsRow struct {
	AgentParamsJSON    []byte
	RequiredParams     []agentgen.AgentParamSpec
}

// GetAgentParamsForBinding returns the agent_params JSON and the spec's RequiredParams
// for one (applicationID, agentID) pair.
// Returns pgx.ErrNoRows when the binding or runtime spec is absent.
func (d *DB) GetAgentParamsForBinding(ctx context.Context, applicationID, agentID string) (AgentParamsRow, error) {
	const q = `
		SELECT COALESCE(b.agent_params, '{}'),
		       COALESCE(s.spec->'required_params', '[]'::jsonb)
		  FROM them.app_agent_bindings b
		  JOIN them.agent_runtime_specs s ON s.agent_id = b.agent_id
		 WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid`

	var row AgentParamsRow
	var requiredParamsJSON []byte
	if err := d.q.QueryRow(ctx, q, applicationID, agentID).Scan(&row.AgentParamsJSON, &requiredParamsJSON); err != nil {
		return row, err
	}
	if len(requiredParamsJSON) > 0 && string(requiredParamsJSON) != "null" {
		_ = json.Unmarshal(requiredParamsJSON, &row.RequiredParams)
	}
	if row.RequiredParams == nil {
		row.RequiredParams = []agentgen.AgentParamSpec{}
	}
	return row, nil
}

// UpsertAgentParams merges paramsDelta into agent_params for the binding,
// creating the binding row if it does not exist.
// paramsDelta is a JSONB object; keys with JSON null values are deleted from the stored object.
func (d *DB) UpsertAgentParams(ctx context.Context, applicationID, agentID string, paramsDelta []byte) error {
	if paramsDelta == nil {
		paramsDelta = []byte("{}")
	}
	const q = `
		INSERT INTO them.app_agent_bindings (application_id, agent_id, agent_params)
		VALUES ($1::uuid, $2::uuid, $3::jsonb)
		ON CONFLICT (application_id, agent_id) DO UPDATE
		    SET agent_params = them.app_agent_bindings.agent_params || $3::jsonb,
		        updated_at   = now()`
	return d.q.Exec(ctx, q, applicationID, agentID, paramsDelta)
}
