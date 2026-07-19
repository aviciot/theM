// Package session manages WebSocket/edge session lifecycle in Redis.
//
// Design (fixes Critical finding #1 from the architecture review):
//
//   The Python session manager has a TTL-mismatch bug: the session Hash expires
//   after 90 s on pod crash, but the Set membership keys (them:ep:*:sessions,
//   them:app:*:sessions) have no TTL and never shrink automatically. Ghost
//   session IDs accumulate, and if the system is at cap with all-ghost sessions,
//   no new sessions can enter.
//
//   Fix: every SADD into a membership Set is paired with an atomically-created
//   shadow key (them:ep:{slug}:shadow:{session_id}) that carries the same TTL as
//   the session Hash. Ghost pruning scans the Set and removes members whose
//   shadow key has expired. This check is embedded in the membership-count query
//   so it runs automatically on every gate decision without a separate sweep.
//
// Redis keys:
//
//   them:sess:{session_id}              Hash  TTL=SessTTL (heartbeat-refreshed)
//   them:ep:{ep_slug}:sessions          Set   (no TTL — managed by Lua scripts)
//   them:ep:{ep_slug}:shadow:{sid}      String "1" TTL=SessTTL — companion to Set
//   them:app:{app_id}:sessions          Set   (no TTL — managed by Lua scripts)
//   them:app:{app_id}:shadow:{sid}      String "1" TTL=SessTTL — companion to Set
//   them:pod:{instance_id}              Hash  TTL=PodTTL (written by WriteHeartbeat)
//   them:pods                           Set   (no TTL — pod registry)
//   them:sess:control:{session_id}      pub/sub channel — admin disconnect signal
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Constants
// ──────────────────────────────────────────────────────────────────────────────

const (
	// SessTTL is the Redis TTL for a session Hash and its shadow keys.
	// Heartbeats must arrive within this window or the session is considered dead.
	SessTTL = 90 * time.Second

	// PodTTL is the TTL for a pod's heartbeat key.
	// Heartbeat loop interval must be shorter than this (typically 15 s).
	PodTTL = 30 * time.Second

	sessPrefix    = "them:sess:"
	epPrefix      = "them:ep:"
	appPrefix     = "them:app:"
	podPrefix     = "them:pod:"
	podsKey       = "them:pods"
	controlPrefix = "them:sess:control:"
)

// ──────────────────────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────────────────────

// SessionInfo holds the metadata stored for each active session.
type SessionInfo struct {
	SessionID        string `json:"session_id"`
	InstanceID       string `json:"instance_id"`
	UserID           int64  `json:"user_id"`
	OrchestratorName string `json:"orchestrator_name"`
	EPSlug           string `json:"ep_slug,omitempty"`
	AppID            string `json:"app_id,omitempty"`
	ContextID        string `json:"context_id"`
	StartedAt        string `json:"started_at"`
}

// ErrSessionNotFound is returned by Get when the session does not exist.
var ErrSessionNotFound = errors.New("session: not found")

// ──────────────────────────────────────────────────────────────────────────────
// Redis client interface
// ──────────────────────────────────────────────────────────────────────────────

// RedisClient abstracts the Redis operations needed by the session store.
// The production implementation wraps rueidis; tests inject a fake.
type RedisClient interface {
	// HSetEx stores the fields of a hash and sets its TTL atomically.
	HSetEx(ctx context.Context, key string, ttl time.Duration, fields map[string]string) error
	// HGetAll returns all fields of a hash. Returns empty map if key missing.
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// Del deletes one or more keys.
	Del(ctx context.Context, keys ...string) error
	// Expire refreshes the TTL of a key.
	Expire(ctx context.Context, key string, ttl time.Duration) error
	// Exists returns whether the given key exists.
	Exists(ctx context.Context, key string) (bool, error)

	// ExecLua runs the given Lua script with keys and args. Returns the raw
	// Redis reply as an interface{}. The caller must type-assert as needed.
	ExecLua(ctx context.Context, script string, keys []string, args []interface{}) (interface{}, error)

	// Publish sends payload on channel.
	Publish(ctx context.Context, channel, payload string) error
	// Subscribe blocks and delivers messages to handler until ctx is cancelled.
	Subscribe(ctx context.Context, channel string, handler func(payload string)) error
}

// ──────────────────────────────────────────────────────────────────────────────
// Store
// ──────────────────────────────────────────────────────────────────────────────

// Store manages session state in Redis. The activeSessions counter is maintained
// by-value using atomic operations and is used by WriteHeartbeat to report the
// accurate session count to the pod registry (fixing the hardcoded 0 bug in
// the Python platform).
type Store struct {
	redis          RedisClient
	instanceID     string
	activeSessions int32 // updated atomically by Register/End
	logger         *slog.Logger
}

// NewStore creates a Store for the given Redis client and instance ID.
func NewStore(redis RedisClient, instanceID string, logger *slog.Logger) *Store {
	return &Store{
		redis:      redis,
		instanceID: instanceID,
		logger:     logger,
	}
}

// ActiveSessions returns the current number of sessions tracked by this pod.
func (s *Store) ActiveSessions() int32 {
	return atomic.LoadInt32(&s.activeSessions)
}

// ──────────────────────────────────────────────────────────────────────────────
// Lua scripts
// ──────────────────────────────────────────────────────────────────────────────

// luaRegister atomically:
//  1. SADD session_id into the membership Set
//  2. SET shadow key "1" EX ttl_seconds
//
// KEYS[1] = membership Set  (e.g. them:ep:{slug}:sessions)
// KEYS[2] = shadow key      (e.g. them:ep:{slug}:shadow:{sid})
// ARGV[1] = session_id (member)
// ARGV[2] = ttl_seconds (integer)
const luaRegister = `
redis.call('SADD', KEYS[1], ARGV[1])
redis.call('SET',  KEYS[2], '1', 'EX', tonumber(ARGV[2]))
return 1
`

// luaEnd atomically:
//  1. SREM session_id from the membership Set
//  2. DEL shadow key
//
// KEYS[1] = membership Set
// KEYS[2] = shadow key
// ARGV[1] = session_id (member)
const luaEnd = `
redis.call('SREM', KEYS[1], ARGV[1])
redis.call('DEL',  KEYS[2])
return 1
`

// luaPruneAndCount atomically:
//  1. SMEMBERS the Set
//  2. For each member, check whether its shadow key exists
//  3. SREM any ghost member (shadow key missing)
//  4. Return the count of live members
//
// KEYS[1]  = membership Set                   (e.g. them:ep:{slug}:sessions)
// ARGV[1]  = shadow key prefix including ":"  (e.g. them:ep:{slug}:shadow:)
//
// Returns: integer count of live (non-ghost) sessions
const luaPruneAndCount = `
local members = redis.call('SMEMBERS', KEYS[1])
local live = 0
for _, sid in ipairs(members) do
    local shadow = ARGV[1] .. sid
    if redis.call('EXISTS', shadow) == 1 then
        live = live + 1
    else
        redis.call('SREM', KEYS[1], sid)
    end
end
return live
`

// ──────────────────────────────────────────────────────────────────────────────
// Register
// ──────────────────────────────────────────────────────────────────────────────

// Register stores a new session in Redis and adds it to the EP/app membership
// Sets with shadow TTL keys. The atomic Lua scripts ensure the Set and shadow
// key are always in sync even under concurrent operations.
//
// Best-effort — logs and returns nil on Redis error so callers are not blocked.
func (s *Store) Register(ctx context.Context, info SessionInfo) error {
	sessKey := sessPrefix + info.SessionID
	ttlSec := int(SessTTL.Seconds())

	// Store session hash.
	fields := sessionInfoToFields(info)
	if err := s.redis.HSetEx(ctx, sessKey, SessTTL, fields); err != nil {
		s.logger.Warn("session: register: hset failed", "session_id", info.SessionID, "error", err)
		return nil // best-effort
	}

	// Add to EP membership Set + shadow key (atomic Lua).
	if info.EPSlug != "" {
		setKey := epPrefix + info.EPSlug + ":sessions"
		shadowKey := epPrefix + info.EPSlug + ":shadow:" + info.SessionID
		if _, err := s.redis.ExecLua(ctx, luaRegister,
			[]string{setKey, shadowKey},
			[]interface{}{info.SessionID, ttlSec},
		); err != nil {
			s.logger.Warn("session: register: ep lua failed", "session_id", info.SessionID, "error", err)
		}
	}

	// Add to app membership Set + shadow key (atomic Lua).
	if info.AppID != "" {
		setKey := appPrefix + info.AppID + ":sessions"
		shadowKey := appPrefix + info.AppID + ":shadow:" + info.SessionID
		if _, err := s.redis.ExecLua(ctx, luaRegister,
			[]string{setKey, shadowKey},
			[]interface{}{info.SessionID, ttlSec},
		); err != nil {
			s.logger.Warn("session: register: app lua failed", "session_id", info.SessionID, "error", err)
		}
	}

	atomic.AddInt32(&s.activeSessions, 1)
	s.logger.Info("session: registered",
		"session_id", info.SessionID, "ep_slug", info.EPSlug,
		"app_id", info.AppID, "user_id", info.UserID)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// End
// ──────────────────────────────────────────────────────────────────────────────

// End removes a session from Redis and atomically removes it from the EP/app
// membership Sets, also deleting shadow keys.
func (s *Store) End(ctx context.Context, sessionID, epSlug, appID string) error {
	sessKey := sessPrefix + sessionID

	if err := s.redis.Del(ctx, sessKey); err != nil {
		s.logger.Warn("session: end: del session failed", "session_id", sessionID, "error", err)
	}

	if epSlug != "" {
		setKey := epPrefix + epSlug + ":sessions"
		shadowKey := epPrefix + epSlug + ":shadow:" + sessionID
		if _, err := s.redis.ExecLua(ctx, luaEnd,
			[]string{setKey, shadowKey},
			[]interface{}{sessionID},
		); err != nil {
			s.logger.Warn("session: end: ep lua failed", "session_id", sessionID, "error", err)
		}
	}

	if appID != "" {
		setKey := appPrefix + appID + ":sessions"
		shadowKey := appPrefix + appID + ":shadow:" + sessionID
		if _, err := s.redis.ExecLua(ctx, luaEnd,
			[]string{setKey, shadowKey},
			[]interface{}{sessionID},
		); err != nil {
			s.logger.Warn("session: end: app lua failed", "session_id", sessionID, "error", err)
		}
	}

	atomic.AddInt32(&s.activeSessions, -1)
	s.logger.Info("session: ended", "session_id", sessionID)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Touch
// ──────────────────────────────────────────────────────────────────────────────

// Touch refreshes the TTL of the session Hash and its shadow keys for the given
// EP slug and app ID. Call on every heartbeat or active message to prevent
// expiry while the session is live.
func (s *Store) Touch(ctx context.Context, sessionID, epSlug, appID string) error {
	sessKey := sessPrefix + sessionID
	if err := s.redis.Expire(ctx, sessKey, SessTTL); err != nil {
		return fmt.Errorf("session: touch: %w", err)
	}
	if epSlug != "" {
		shadowKey := epPrefix + epSlug + ":shadow:" + sessionID
		_ = s.redis.Expire(ctx, shadowKey, SessTTL) // best-effort
	}
	if appID != "" {
		shadowKey := appPrefix + appID + ":shadow:" + sessionID
		_ = s.redis.Expire(ctx, shadowKey, SessTTL) // best-effort
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Get
// ──────────────────────────────────────────────────────────────────────────────

// Get retrieves session metadata. Returns ErrSessionNotFound if the session has
// expired or never existed.
func (s *Store) Get(ctx context.Context, sessionID string) (*SessionInfo, error) {
	fields, err := s.redis.HGetAll(ctx, sessPrefix+sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: get: %w", err)
	}
	if len(fields) == 0 {
		return nil, ErrSessionNotFound
	}
	info := fieldsToSessionInfo(fields)
	return &info, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Membership count (with ghost pruning)
// ──────────────────────────────────────────────────────────────────────────────

// CountEPSessions returns the number of live sessions for an entry point.
// Ghost sessions (shadow key expired) are pruned from the Set atomically.
func (s *Store) CountEPSessions(ctx context.Context, epSlug string) (int, error) {
	return s.pruneAndCount(ctx, epPrefix+epSlug+":sessions", epPrefix+epSlug+":shadow:")
}

// CountAppSessions returns the number of live sessions for an application.
// Ghost sessions are pruned from the Set atomically.
func (s *Store) CountAppSessions(ctx context.Context, appID string) (int, error) {
	return s.pruneAndCount(ctx, appPrefix+appID+":sessions", appPrefix+appID+":shadow:")
}

func (s *Store) pruneAndCount(ctx context.Context, setKey, shadowPrefix string) (int, error) {
	result, err := s.redis.ExecLua(ctx, luaPruneAndCount,
		[]string{setKey},
		[]interface{}{shadowPrefix},
	)
	if err != nil {
		return 0, fmt.Errorf("session: count: %w", err)
	}
	switch v := result.(type) {
	case int64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("session: count: unexpected Lua return type %T", result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Pod heartbeat
// ──────────────────────────────────────────────────────────────────────────────

// WriteHeartbeat writes the pod liveness record to Redis. The session count is
// read via atomic.LoadInt32(&s.activeSessions) — not hardcoded to 0. This fixes
// the multi-pod session tracking bug in the Python platform.
func (s *Store) WriteHeartbeat(ctx context.Context) error {
	podKey := podPrefix + s.instanceID
	count := atomic.LoadInt32(&s.activeSessions)
	fields := map[string]string{
		"instance_id": s.instanceID,
		"sessions":    fmt.Sprintf("%d", count),
		"ts":          time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.redis.HSetEx(ctx, podKey, PodTTL, fields); err != nil {
		return fmt.Errorf("session: heartbeat: hset: %w", err)
	}
	if err := s.redis.Publish(ctx, podsKey, s.instanceID); err != nil {
		s.logger.Warn("session: heartbeat: publish pod failed", "error", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Admin disconnect signal
// ──────────────────────────────────────────────────────────────────────────────

// SignalDisconnect publishes a disconnect signal for the given session on the
// them:sess:control:{session_id} pub/sub channel. The edge handler subscribed
// to this channel will close the WebSocket/SSE connection with code 4000.
func (s *Store) SignalDisconnect(ctx context.Context, sessionID string) error {
	ch := controlPrefix + sessionID
	return s.redis.Publish(ctx, ch, "disconnect")
}

// SubscribeControl subscribes to the disconnect control channel for a session
// and calls handler whenever a disconnect signal arrives. Blocks until ctx is
// cancelled. Used by edge handlers to receive admin disconnect signals.
func (s *Store) SubscribeControl(ctx context.Context, sessionID string, handler func()) {
	ch := controlPrefix + sessionID
	_ = s.redis.Subscribe(ctx, ch, func(_ string) {
		handler()
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Field serialisation helpers
// ──────────────────────────────────────────────────────────────────────────────

func sessionInfoToFields(info SessionInfo) map[string]string {
	return map[string]string{
		"session_id":        info.SessionID,
		"instance_id":       info.InstanceID,
		"user_id":           fmt.Sprintf("%d", info.UserID),
		"orchestrator_name": info.OrchestratorName,
		"ep_slug":           info.EPSlug,
		"app_id":            info.AppID,
		"context_id":        info.ContextID,
		"started_at":        info.StartedAt,
	}
}

func fieldsToSessionInfo(fields map[string]string) SessionInfo {
	var userID int64
	if v := fields["user_id"]; v != "" {
		fmt.Sscanf(v, "%d", &userID)
	}
	return SessionInfo{
		SessionID:        fields["session_id"],
		InstanceID:       fields["instance_id"],
		UserID:           userID,
		OrchestratorName: fields["orchestrator_name"],
		EPSlug:           fields["ep_slug"],
		AppID:            fields["app_id"],
		ContextID:        fields["context_id"],
		StartedAt:        fields["started_at"],
	}
}

