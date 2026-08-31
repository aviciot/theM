package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/aviciot/them/internal/session"
)

const (
	// sessionsDashPrefix is the Redis pub/sub prefix for per-app session events.
	// Full channel: them:dash:sessions:{appID}
	sessionsDashPrefix = dashPrefix + "sessions:"

	// sessionStateKeyPrefix is the Redis Hash storing current session snapshots.
	// Full key: them:dash:sessions:state:{appID}
	// TTL: 120s (refreshed on every session_start)
	sessionStateTTL = 120 * time.Second
)

// publisherRedis is the minimal Redis surface needed by SessionPublisher.
type publisherRedis interface {
	Publish(ctx context.Context, channel, payload string) error
	HSetEx(ctx context.Context, key string, ttl time.Duration, fields map[string]string) error
	HDel(ctx context.Context, key string, fields ...string) error
}

// SessionPublisher publishes session lifecycle events to the dashboard
// pub/sub channel (them:dash:sessions:{appID}) and maintains the snapshot
// hash (them:dash:sessions:state:{appID}) so late subscribers get current state.
type SessionPublisher struct {
	redis  publisherRedis
	logger *slog.Logger
}

// NewSessionPublisher constructs a SessionPublisher.
func NewSessionPublisher(rc publisherRedis, logger *slog.Logger) *SessionPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionPublisher{redis: rc, logger: logger}
}

// PublishSessionStart emits a session_start event and upserts the session into
// the snapshot hash. Best-effort — logs errors but never returns them so a
// Redis blip does not break the session.
func (p *SessionPublisher) PublishSessionStart(ctx context.Context, info session.SessionInfo) {
	if info.AppID == "" {
		return
	}
	evt := map[string]any{
		"type":        "session_start",
		"session_id":  info.SessionID,
		"session_info": info,
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	ch := sessionsDashPrefix + info.AppID
	if err := p.redis.Publish(ctx, ch, string(data)); err != nil {
		p.logger.Warn("dashboard: publish session_start failed", "app_id", info.AppID, "error", err)
	}

	// Upsert snapshot hash — field = session_id, value = JSON of SessionInfo.
	infoJSON, err := json.Marshal(info)
	if err != nil {
		return
	}
	stateKey := sessionStatePrefix + info.AppID
	if err := p.redis.HSetEx(ctx, stateKey, sessionStateTTL, map[string]string{
		info.SessionID: string(infoJSON),
	}); err != nil {
		p.logger.Warn("dashboard: upsert session state failed", "app_id", info.AppID, "error", err)
	}
}

// PublishSessionEnd emits a session_end event and removes the session from
// the snapshot hash. Best-effort.
func (p *SessionPublisher) PublishSessionEnd(ctx context.Context, sessionID, appID string) {
	if appID == "" {
		return
	}
	evt := map[string]any{
		"type":       "session_end",
		"session_id": sessionID,
	}
	data, _ := json.Marshal(evt)
	ch := sessionsDashPrefix + appID
	if err := p.redis.Publish(ctx, ch, string(data)); err != nil {
		p.logger.Warn("dashboard: publish session_end failed", "app_id", appID, "error", err)
	}

	stateKey := sessionStatePrefix + appID
	if err := p.redis.HDel(ctx, stateKey, sessionID); err != nil {
		p.logger.Warn("dashboard: del session state failed", "app_id", appID, "error", err)
	}
}
