// Package gate implements the runtime admission gate for WebSocket/SSE sessions.
//
// The gate is the SOLE owner of Set membership at admission time. A single atomic
// Lua script performs all of the following in one Redis round-trip:
//
//  1. Ghost prune: scan them:ep:{slug}:sessions, SREM members whose shadow key
//     has expired. This prevents ghost sessions from blocking admission.
//  2. Cap check: if live count >= ep_max_concurrent, return "full".
//  3. Rate limit: INCR rl:them:token:{hash}:{minute} with TTL 90s. If count
//     exceeds rate_limit_rpm, return "rate_limited".
//  4. App-level cap check: if app_max_concurrent > 0, check them:app:{id}:sessions.
//  5. SADD: add session_id to them:ep:{slug}:sessions and them:app:{id}:sessions.
//  6. SET shadow TTL keys for both Sets.
//
// SessionManager (internal/session) owns the Hash (them:sess:{id}) written AFTER
// the gate admits the session. The gate does NOT write the Hash.
//
// On End, SessionManager owns SREM (symmetric with whoever did the SADD here).
package gate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ──────────────────────────────────────────────────────────────────────────────

// ErrCapExceeded is returned when the session cap for the entry point is full.
var ErrCapExceeded = errors.New("gate: session cap exceeded")

// ErrRateLimited is returned when the per-token rate limit is exceeded.
var ErrRateLimited = errors.New("gate: rate limit exceeded")

// ErrQueueFull is returned when the session cap is full and the queue is also full.
var ErrQueueFull = errors.New("gate: queue full")

// ──────────────────────────────────────────────────────────────────────────────
// Result
// ──────────────────────────────────────────────────────────────────────────────

// Status describes the gate decision for a session.
type Status int

const (
	StatusAdmitted Status = iota // session was admitted immediately
	StatusQueued                 // session is waiting in the queue for a slot
)

// Result is returned by Gate.Check on a successful admission.
type Result struct {
	Status Status
}

// ──────────────────────────────────────────────────────────────────────────────
// Config
// ──────────────────────────────────────────────────────────────────────────────

// Config holds the per-request parameters for the gate decision.
type Config struct {
	EPSlug           string // entry point slug
	AppID            string // application ID (may be empty)
	TokenHash        string // sha256 hex of the bearer token — rate limit key
	SessionID        string // the new session being admitted
	EPMaxConcurrent  int    // max concurrent sessions for this EP (0 = unlimited)
	AppMaxConcurrent int    // max concurrent sessions for this app (0 = unlimited)
	RateLimitRPM     int    // max requests per minute per token (0 = unlimited)
	ShadowTTL        int    // shadow key TTL in seconds (default: 90)

	// Queue config: if EPMaxConcurrent is hit and QueueTimeout > 0, the session
	// waits in a Redis list for a slot to open instead of being rejected.
	QueueTimeout int    // seconds to wait in queue (0 = no queue, reject immediately)
	QueueMessage string // message to send to the client while queued (optional)
}

// ──────────────────────────────────────────────────────────────────────────────
// RedisClient interface
// ──────────────────────────────────────────────────────────────────────────────

// RedisClient abstracts the Redis operations needed by the gate.
type RedisClient interface {
	// ExecLua runs a Lua script atomically.
	ExecLua(ctx context.Context, script string, keys []string, args []interface{}) (interface{}, error)
	// BLPop blocks until an element is available in one of the lists or timeout.
	// Returns (key, value, nil) on success or ("", "", nil) on timeout.
	BLPop(ctx context.Context, timeout time.Duration, keys ...string) (string, string, error)
	// LPush pushes a value to the head of a list.
	LPush(ctx context.Context, key, value string) error
	// Del deletes one or more keys.
	Del(ctx context.Context, keys ...string) error
}

// ──────────────────────────────────────────────────────────────────────────────
// Gate
// ──────────────────────────────────────────────────────────────────────────────

// Gate performs runtime admission control using atomic Lua scripts.
// It is the sole owner of Set membership (them:ep:*:sessions, them:app:*:sessions)
// at admission time.
type Gate struct {
	redis RedisClient
}

// New creates a Gate backed by the given Redis client.
func New(redis RedisClient) *Gate {
	return &Gate{redis: redis}
}

// ──────────────────────────────────────────────────────────────────────────────
// Lua scripts
// ──────────────────────────────────────────────────────────────────────────────

// luaAdmit atomically:
//  1. Ghost-prune the EP membership Set (SMEMBERS + check shadow key + SREM ghosts)
//  2. Count live EP sessions
//  3. If ep_max > 0 and live >= ep_max → return -1 (cap exceeded)
//  4. Ghost-prune and count app sessions (if app_key provided)
//  5. If app_max > 0 and live_app >= app_max → return -2 (app cap exceeded)
//  6. Rate-limit check (if rate_limit_rpm > 0)
//     INCR rl key, set TTL 90s on first call. If count > rate_limit_rpm → return -3
//  7. SADD session into EP Set + SET shadow key EX shadow_ttl
//  8. SADD session into app Set + SET shadow key EX shadow_ttl (if app_key provided)
//  9. Return live count (>= 0) on success
//
// KEYS[1]  = EP membership Set            (them:ep:{slug}:sessions)
// KEYS[2]  = EP shadow prefix (ends in :) (them:ep:{slug}:shadow:)
// KEYS[3]  = app membership Set           (them:app:{id}:sessions  — "" if unused)
// KEYS[4]  = app shadow prefix            (them:app:{id}:shadow:   — "" if unused)
// KEYS[5]  = rate limit key               (rl:them:token:{hash}:{minute})
// ARGV[1]  = session_id
// ARGV[2]  = ep_max_concurrent  (0 = unlimited)
// ARGV[3]  = app_max_concurrent (0 = unlimited)
// ARGV[4]  = rate_limit_rpm     (0 = unlimited)
// ARGV[5]  = shadow_ttl         (seconds)
//
// Returns: integer
//   >= 0  admitted; value is the new live EP session count
//   -1    EP cap exceeded
//   -2    app cap exceeded
//   -3    rate limited
const luaAdmit = `
local ep_set    = KEYS[1]
local ep_shpfx  = KEYS[2]
local app_set   = KEYS[3]
local app_shpfx = KEYS[4]
local rl_key    = KEYS[5]

local sid            = ARGV[1]
local ep_max         = tonumber(ARGV[2])
local app_max        = tonumber(ARGV[3])
local rate_limit_rpm = tonumber(ARGV[4])
local shadow_ttl     = tonumber(ARGV[5])

-- Helper: prune ghosts and return live count for a set
local function prune_count(set_key, shpfx)
    local members = redis.call('SMEMBERS', set_key)
    local live = 0
    for _, m in ipairs(members) do
        if redis.call('EXISTS', shpfx .. m) == 1 then
            live = live + 1
        else
            redis.call('SREM', set_key, m)
        end
    end
    return live
end

-- 1. Prune + count EP sessions
local ep_live = prune_count(ep_set, ep_shpfx)

-- 2. EP cap check
if ep_max > 0 and ep_live >= ep_max then
    return -1
end

-- 3. App cap check (skip if app_set is empty)
if app_set ~= '' then
    local app_live = prune_count(app_set, app_shpfx)
    if app_max > 0 and app_live >= app_max then
        return -2
    end
end

-- 4. Rate limit check
if rate_limit_rpm > 0 and rl_key ~= '' then
    local count = redis.call('INCR', rl_key)
    if count == 1 then
        redis.call('EXPIRE', rl_key, 90)
    end
    if count > rate_limit_rpm then
        return -3
    end
end

-- 5. Admit: SADD EP Set + shadow key
redis.call('SADD', ep_set, sid)
redis.call('SET',  ep_shpfx .. sid, '1', 'EX', shadow_ttl)

-- 6. Admit: SADD app Set + shadow key (if provided)
if app_set ~= '' then
    redis.call('SADD', app_set, sid)
    redis.call('SET',  app_shpfx .. sid, '1', 'EX', shadow_ttl)
end

return ep_live + 1
`

// ──────────────────────────────────────────────────────────────────────────────
// Check
// ──────────────────────────────────────────────────────────────────────────────

const (
	resultCapExceeded  = int64(-1)
	resultAppCapExceeded = int64(-2)
	resultRateLimited  = int64(-3)

	defaultShadowTTL = 90

	queueKeyPrefix = "them:ep:gate:queue:"
)

// Check performs the gate admission check for a new session. On success it
// returns a Result. On rejection it returns one of ErrCapExceeded,
// ErrRateLimited, or ErrQueueFull.
//
// If cfg.QueueTimeout > 0 and the EP cap is exceeded, Check pushes the session
// into a Redis wait-queue and blocks until a slot opens or the timeout expires.
// The caller is expected to call session.Store.Register() after a successful Check.
func (g *Gate) Check(ctx context.Context, cfg Config) (Result, error) {
	shadowTTL := cfg.ShadowTTL
	if shadowTTL <= 0 {
		shadowTTL = defaultShadowTTL
	}

	epSet := "them:ep:" + cfg.EPSlug + ":sessions"
	epShadow := "them:ep:" + cfg.EPSlug + ":shadow:"
	appSet := ""
	appShadow := ""
	if cfg.AppID != "" {
		appSet = "them:app:" + cfg.AppID + ":sessions"
		appShadow = "them:app:" + cfg.AppID + ":shadow:"
	}

	minute := time.Now().UTC().Format("200601021504")
	rlKey := ""
	if cfg.RateLimitRPM > 0 && cfg.TokenHash != "" {
		rlKey = "rl:them:token:" + cfg.TokenHash + ":" + minute
	}

	keys := []string{epSet, epShadow, appSet, appShadow, rlKey}
	args := []interface{}{
		cfg.SessionID,
		cfg.EPMaxConcurrent,
		cfg.AppMaxConcurrent,
		cfg.RateLimitRPM,
		shadowTTL,
	}

	result, err := g.redis.ExecLua(ctx, luaAdmit, keys, args)
	if err != nil {
		return Result{}, fmt.Errorf("gate: lua: %w", err)
	}

	code := toLuaInt(result)

	switch code {
	case resultRateLimited:
		return Result{}, ErrRateLimited

	case resultAppCapExceeded:
		return Result{}, ErrCapExceeded

	case resultCapExceeded:
		if cfg.QueueTimeout <= 0 {
			return Result{}, ErrCapExceeded
		}
		// Queue path: push session into the wait queue and block until a slot opens.
		queueKey := queueKeyPrefix + cfg.EPSlug
		if err := g.redis.LPush(ctx, queueKey, cfg.SessionID); err != nil {
			return Result{}, fmt.Errorf("gate: queue push: %w", err)
		}
		// Wait for a slot signal. Session.End() publishes to the queue via LPUSH
		// on the same key so the next waiter can proceed.
		slotKey := queueKey + ":slot"
		deadline := time.Duration(cfg.QueueTimeout) * time.Second
		_, val, err := g.redis.BLPop(ctx, deadline, slotKey)
		// Clean up our place in the queue regardless of outcome.
		_ = g.redis.Del(ctx, queueKey)
		if err != nil || val == "" {
			return Result{}, ErrQueueFull
		}
		// Re-run admission now that a slot is available.
		return g.Check(ctx, cfg)
	}

	if code < 0 {
		return Result{}, fmt.Errorf("gate: unknown lua result: %d", code)
	}

	return Result{Status: StatusAdmitted}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func toLuaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
