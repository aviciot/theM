package dal

import (
	"context"
	"encoding/json"
)

// OrchestratorVoiceInput carries STT/TTS configuration for SetOrchestratorVoice.
type OrchestratorVoiceInput struct {
	STTProvider  string
	STTModel     string
	TTSProvider  string
	TTSVoice     string
	VoiceEnabled bool
	TTSEnabled   bool
}

// SetOrchestratorMCPServers writes the mcp_servers JSONB array for one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator belonging to another application.
func (d *DB) SetOrchestratorMCPServers(ctx context.Context, appID, orchID string, servers []MCPServerAttachment) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	const q = `
		UPDATE them.app_orchestrators
		SET mcp_servers = $3::jsonb, updated_at = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, orchID, appID, raw).Scan(&id)
}

// SetOrchestratorLLM updates llm_provider and llm_model on one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator belonging to another application.
func (d *DB) SetOrchestratorLLM(ctx context.Context, appID, orchID, provider, model string) error {
	const q = `
		UPDATE them.app_orchestrators
		SET llm_provider = $3, llm_model = $4, updated_at = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, orchID, appID, provider, model).Scan(&id)
}

// SetOrchestratorVoice writes STT/TTS configuration to one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator belonging to another application.
func (d *DB) SetOrchestratorVoice(ctx context.Context, appID, orchID string, in OrchestratorVoiceInput) error {
	const q = `
		UPDATE them.app_orchestrators
		SET transcription_provider = NULLIF($3, ''),
		    transcription_model     = NULLIF($4, ''),
		    tts_provider            = NULLIF($5, ''),
		    tts_voice               = NULLIF($6, ''),
		    voice_enabled           = $7,
		    tts_enabled             = $8,
		    updated_at              = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, orchID, appID,
		in.STTProvider, in.STTModel, in.TTSProvider, in.TTSVoice, in.VoiceEnabled, in.TTSEnabled,
	).Scan(&id)
}

// ListAppOrchestratorNames returns the names of all app_orchestrators for the given application.
// Used by cache flush before a bulk delete.
func (d *DB) ListAppOrchestratorNames(ctx context.Context, appID string) ([]string, error) {
	const q = `SELECT name FROM them.app_orchestrators WHERE application_id=$1::uuid`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, nil
}
