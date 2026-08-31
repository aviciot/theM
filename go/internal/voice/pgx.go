package voice

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/temporal/workerconfig"
)

// NewWorkerConfigLoader returns a workerconfig.Loader backed by the given pool
// and Fernet key. Voice uses this to load orchestrator config per-run without
// going through Temporal.
func NewWorkerConfigLoader(pool *pgxpool.Pool, fernetKey []byte) workerconfig.Loader {
	return workerconfig.NewPgxLoader(pool, fernetKey)
}

// PgxLoader implements ConfigLoader against a live pgxpool.Pool.
type PgxLoader struct {
	pool *pgxpool.Pool
}

// NewPgxLoader wraps a pgxpool.Pool as a ConfigLoader for voice entry points.
func NewPgxLoader(pool *pgxpool.Pool) *PgxLoader {
	return &PgxLoader{pool: pool}
}

// voiceConfigQuery resolves a voice entry point's config in one query:
// entry_points → applications → app_orchestrators (voice config columns).
// Scoped by (tenant_id, app_slug, ep_slug) — migration 048 adds slug to
// applications, making the triple the canonical unique key for an entry point.
const voiceConfigQuery = `
SELECT
    ep.enabled,
    a.enabled,
    COALESCE(ep.access_policy->>'mode', 'token'),
    a.id::text,
    a.tenant_id::text,
    COALESCE(ao.id::text, ''),
    COALESCE(ao.transcription_provider, ''),
    COALESCE(ao.transcription_model, ''),
    COALESCE(ao.tts_enabled, false),
    COALESCE(ao.tts_provider, ''),
    COALESCE(ao.tts_voice, ''),
    COALESCE(ao.llm_model, '')
FROM them.entry_points ep
JOIN them.applications a ON a.id = ep.application_id
LEFT JOIN them.app_orchestrators ao
    ON ao.id = ep.app_orchestrator_id
   AND ao.application_id = ep.application_id
WHERE ep.tenant_id = $1::uuid
  AND a.slug        = $2
  AND ep.slug       = $3
  AND ep.entry_point_type = 'voice'
LIMIT 1`

// LoadVoiceConfig fetches the resolved voice configuration for the given
// (tenantID, appSlug, epSlug) triple. Returns an error when the EP does not
// exist or is not of type "voice".
func (q *PgxLoader) LoadVoiceConfig(ctx context.Context, tenantID, appSlug, epSlug string) (*EPVoiceConfig, error) {
	var cfg EPVoiceConfig
	var ttsModelProxy string

	err := q.pool.QueryRow(ctx, voiceConfigQuery, tenantID, appSlug, epSlug).Scan(
		&cfg.EPEnabled,
		&cfg.AppEnabled,
		&cfg.AccessMode,
		&cfg.AppID,
		&cfg.TenantID,
		&cfg.OrchestratorID,
		&cfg.STTProvider,
		&cfg.STTModel,
		&cfg.TTSEnabled,
		&cfg.TTSProvider,
		&cfg.TTSVoice,
		&ttsModelProxy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("voice: entry point not found: slug=%s", epSlug)
		}
		return nil, fmt.Errorf("voice: db query: %w", err)
	}
	cfg.TTSModel = ttsModelProxy
	return &cfg, nil
}
