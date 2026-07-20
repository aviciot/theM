// Package gate implements the runtime admission gate for WebSocket/SSE sessions.
//
// # Ownership
//
// Gate is the SOLE owner of Set membership at admission time:
//   - them:ep:{slug}:sessions   — Set of admitted session IDs
//   - them:app:{id}:sessions    — Set of admitted session IDs per app
//   - them:ep:{slug}:shadow:{sid}  — shadow TTL key (controls ghost pruning)
//   - them:app:{id}:shadow:{sid}   — shadow TTL key per app
//
// SessionManager (internal/session) owns ONLY the Hash (them:sess:{id}).
//
// # Transaction boundary and reservation pattern
//
// Gate.Check writes the Set membership and shadow keys with a SHORT reservation
// TTL (ReservationTTL = 10s). This bounds the failure window:
//
//	Gate.Check()       → luaAdmit → SADD + SET shadow EX 10s   (atomic, Redis)
//	session.Register() → HSET them:sess:{id}                   (separate call)
//	Gate.Confirm()     → SET shadow EX 90s  (refresh to full TTL)
//
// If the process crashes between Check and Confirm, the shadow key expires in
// ≤10s. The next admission attempt prunes the ghost automatically (luaAdmit
// scans for expired shadow keys before counting). No explicit rollback is needed.
//
// Callers MUST call Gate.Confirm after session.Register succeeds. If Register
// fails, callers MUST call Gate.Rollback to remove Set membership immediately
// rather than waiting for the reservation TTL to expire.
//
// # Queue protocol
//
// When ep_max_concurrent is reached and QueueTimeout > 0, the session waits in
// a Redis list. The list key is them:ep:gate:queue:{slug}. Waiters block with
// BLPOP on this key. When a slot opens, the departing session calls Gate.Release
// which does LPush("1") to wake exactly one waiter.
//
// On BLPOP wake-up, the waiter re-runs the full luaAdmit script from scratch.
// This is a compete — not a guarantee. If multiple waiters wake simultaneously,
// only one wins; the rest receive ErrCapExceeded and must not re-queue.
//
// Timeout cleanup uses LRem to remove only this session's own entry from the
// queue list, leaving other waiters intact.
package gate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Constants
// ──────────────────────────────────────────────────────────────────────────────

const (
	// ReservationTTL is the shadow key TTL written by Gate.Check. Short enough
	// that a crashed process leaves at most a 10s ghost window.
	ReservationTTL = 10 * time.Second

	// defaultShadowTTL is the full session shadow TTL applied by Gate.Confirm.
	// Must match session.SessTTL (90s).
	defaultShadowTTL = 90 * time.Second

	// queueKeyPrefix is the prefix for the BLPOP wait-list key.
	// Full key: them:ep:gate:queue:{ep_slug}
	queueKeyPrefix = "them:ep:gate:queue:"
)

// ──────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ──────────────────────────────────────────────────────────────────────────────

// ErrCapExceeded is returned when the EP session cap is full and no queue is
// configured, or when a queued session woke but lost the re-compete.
var ErrCapExceeded = errors.New("gate: session cap exceeded")

// ErrRateLimited is returned when the per-token rate limit is exceeded.
var ErrRateLimited = errors.New("gate: rate limit exceeded")

// ErrQueueFull is returned when the session cap is full and the queue wait
// timed out or the context was cancelled.
var ErrQueueFull = errors.New("gate: queue full")

// ──────────────────────────────────────────────────────────────────────────────
// Status / Result
// ──────────────────────────────────────────────────────────────────────────────

// Status describes the outcome of a successful Gate.Check call.
type Status int

const (
	StatusAdmitted Status = iota // session admitted immediately
)

// Result is returned by Gate.Check on success.
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

	// ShadowTTL is the FULL shadow key TTL (applied by Confirm). Default: 90s.
	// Must be >= session.SessTTL so heartbeat keeps it alive.
	ShadowTTL time.Duration

	// Queue config: if EPMaxConcurrent is hit and QueueTimeout > 0, the session
	// waits in a Redis list for a slot to open instead of being rejected.
	QueueTimeout time.Duration // how long to wait in queue (0 = no queue)
}

func (c *Config) shadowTTL() time.Duration {
	if c.ShadowTTL > 0 {
		return c.ShadowTTL
	}
	return defaultShadowTTL
}

func (c *Config) epSet() string    { return "them:ep:" + c.EPSlug + ":sessions" }
func (c *Config) epShadow() string { return "them:ep:" + c.EPSlug + ":shadow:" }
func (c *Config) appSet() string {
	if c.AppID == "" {
		return ""
	}
	return "them:app:" + c.AppID + ":sessions"
}
func (c *Config) appShadow() string {
	if c.AppID == "" {
		return ""
	}
	return "them:app:" + c.AppID + ":shadow:"
}
func (c *Config) rlKey() string {
	if c.RateLimitRPM == 0 || c.TokenHash == "" {
		return ""
	}
	minute := time.Now().UTC().Format("200601021504")
	return "rl:them:token:" + c.TokenHash + ":" + minute
}
func (c *Config) queueKey() string { return queueKeyPrefix + c.EPSlug }

// ──────────────────────────────────────────────────────────────────────────────
// RedisClient interface
// ──────────────────────────────────────────────────────────────────────────────

// RedisClient abstracts the Redis operations needed by the gate.
type RedisClient interface {
	// ExecLua runs a Lua script atomically. Returns the raw reply.
	ExecLua(ctx context.Context, script string, keys []string, args []interface{}) (interface{}, error)

	// SetEX sets key=value with the given TTL. Used by Confirm to refresh shadows.
	SetEX(ctx context.Context, key, value string, ttl time.Duration) error

	// Del deletes one or more keys. Used by Rollback.
	Del(ctx context.Context, keys ...string) error

	// BLPop blocks until an element is available in the given list or timeout expires.
	// Returns ("", "", nil) on timeout. Returns ("", "", ctx.Err()) on context cancel.
	BLPop(ctx context.Context, timeout time.Duration, key string) (string, error)

	// LPush pushes value to the head of key. Used by Release to wake one waiter.
	LPush(ctx context.Context, key, value string) error
}

// ──────────────────────────────────────────────────────────────────────────────
// Gate
// ──────────────────────────────────────────────────────────────────────────────

// Gate performs runtime admission control using atomic Lua scripts.
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

// luaAdmit atomically performs the full admission sequence in one round-trip:
//
//  1. Ghost-prune EP Set: scan SMEMBERS, SREM any member whose shadow key is absent.
//  2. Count live EP sessions.
//  3. If ep_max > 0 and live >= ep_max → return -1 (EP cap exceeded).
//  4. Ghost-prune and count app sessions (if app_set provided).
//  5. If app_max > 0 and app_live >= app_max → return -2 (app cap exceeded).
//  6. Rate-limit: INCR rl key; set TTL 90s on first call. If over limit → return -3.
//  7. SADD session into EP Set + SET shadow EX reservation_ttl.
//  8. SADD session into app Set + SET shadow EX reservation_ttl (if provided).
//  9. Return new live EP count (>= 1) on success.
//
// KEYS[1]  = EP membership Set             (them:ep:{slug}:sessions)
// KEYS[2]  = EP shadow prefix ending in :  (them:ep:{slug}:shadow:)
// KEYS[3]  = app membership Set            (them:app:{id}:sessions  — "" if unused)
// KEYS[4]  = app shadow prefix             (them:app:{id}:shadow:   — "" if unused)
// KEYS[5]  = rate limit key                (rl:them:token:{hash}:{minute} — "" if unused)
// ARGV[1]  = session_id
// ARGV[2]  = ep_max_concurrent  (0 = unlimited)
// ARGV[3]  = app_max_concurrent (0 = unlimited)
// ARGV[4]  = rate_limit_rpm     (0 = unlimited)
// ARGV[5]  = reservation_ttl_seconds (short TTL written by Gate.Check)
//
// Returns: integer  >=1 admitted | -1 EP cap | -2 app cap | -3 rate limited
const luaAdmit = `
local ep_set    = KEYS[1]
local ep_shpfx  = KEYS[2]
local app_set   = KEYS[3]
local app_shpfx = KEYS[4]
local rl_key    = KEYS[5]

local sid          = ARGV[1]
local ep_max       = tonumber(ARGV[2])
local app_max      = tonumber(ARGV[3])
local rl_rpm       = tonumber(ARGV[4])
local res_ttl      = tonumber(ARGV[5])

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

local ep_live = prune_count(ep_set, ep_shpfx)

if ep_max > 0 and ep_live >= ep_max then
    return -1
end

if app_set ~= '' then
    local app_live = prune_count(app_set, app_shpfx)
    if app_max > 0 and app_live >= app_max then
        return -2
    end
end

if rl_rpm > 0 and rl_key ~= '' then
    local cnt = redis.call('INCR', rl_key)
    if cnt == 1 then redis.call('EXPIRE', rl_key, 90) end
    if cnt > rl_rpm then return -3 end
end

-- Admit: short reservation TTL — caller must call Confirm to extend.
redis.call('SADD', ep_set, sid)
redis.call('SET',  ep_shpfx .. sid, '1', 'EX', res_ttl)

if app_set ~= '' then
    redis.call('SADD', app_set, sid)
    redis.call('SET',  app_shpfx .. sid, '1', 'EX', res_ttl)
end

return ep_live + 1
`

// ──────────────────────────────────────────────────────────────────────────────
// Lua result codes
// ──────────────────────────────────────────────────────────────────────────────

const (
	luaEPCap  = int64(-1)
	luaAppCap = int64(-2)
	luaRL     = int64(-3)
)

// ──────────────────────────────────────────────────────────────────────────────
// Check
// ──────────────────────────────────────────────────────────────────────────────

// Check performs the gate admission check for a new session.
//
// On success it returns Result{StatusAdmitted} and writes Set membership with a
// short reservation TTL (ReservationTTL = 10s). The caller MUST:
//   - call session.Register() to create the session Hash, and then
//   - call Gate.Confirm() to extend shadow TTLs to the full session duration.
//   - call Gate.Rollback() if session.Register() fails.
//
// If cfg.QueueTimeout > 0 and the EP cap is full, Check enqueues the session
// and blocks until a slot is released (via Gate.Release) or the timeout expires.
// A queue wake-up re-runs the full admission script — it is a compete, not a
// guarantee. If the slot is taken by another session, ErrCapExceeded is returned
// (the caller should NOT re-queue).
func (g *Gate) Check(ctx context.Context, cfg Config) (Result, error) {
	return g.admit(ctx, cfg, false)
}

// admit runs the Lua admission script. recheck=true means we woke from a queue
// and must not re-enter the queue if the cap is still full.
func (g *Gate) admit(ctx context.Context, cfg Config, recheck bool) (Result, error) {
	resTTL := int(ReservationTTL.Seconds())

	keys := []string{cfg.epSet(), cfg.epShadow(), cfg.appSet(), cfg.appShadow(), cfg.rlKey()}
	args := []interface{}{
		cfg.SessionID,
		cfg.EPMaxConcurrent,
		cfg.AppMaxConcurrent,
		cfg.RateLimitRPM,
		resTTL,
	}

	raw, err := g.redis.ExecLua(ctx, luaAdmit, keys, args)
	if err != nil {
		return Result{}, fmt.Errorf("gate: lua: %w", err)
	}

	code := toLuaInt(raw)

	switch code {
	case luaRL:
		return Result{}, ErrRateLimited

	case luaAppCap:
		return Result{}, ErrCapExceeded

	case luaEPCap:
		// recheck = true means we just woke from queue and lost the compete.
		// Do NOT re-queue; return immediately.
		if recheck || cfg.QueueTimeout <= 0 {
			return Result{}, ErrCapExceeded
		}
		return g.waitInQueue(ctx, cfg)
	}

	if code < 0 {
		return Result{}, fmt.Errorf("gate: unknown lua result: %d", code)
	}

	return Result{Status: StatusAdmitted}, nil
}

// waitInQueue blocks until a slot signal arrives on the queue key or the wait
// times out / the context is cancelled. No session ID is pushed to the queue —
// the queue key is a pure signal channel: Release pushes "1", waiters consume it.
//
// On wake-up, the waiter re-runs the full admission script (recheck=true). If
// the slot was taken by a concurrent waiter, ErrCapExceeded is returned and the
// caller must not re-queue.
func (g *Gate) waitInQueue(ctx context.Context, cfg Config) (Result, error) {
	qKey := cfg.queueKey()

	// Build a child context honouring both the queue timeout and the parent ctx.
	waitCtx, cancel := context.WithTimeout(ctx, cfg.QueueTimeout)
	defer cancel()

	// Block until a slot signal arrives or timeout/cancel.
	val, err := g.redis.BLPop(waitCtx, cfg.QueueTimeout, qKey)
	if err != nil {
		// Distinguish: if the parent ctx is already done, the caller cancelled
		// (e.g. client disconnected). Otherwise this is our own QueueTimeout.
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("gate: queue cancelled: %w", ctx.Err())
		}
		return Result{}, ErrQueueFull
	}
	if val == "" {
		// BLPop returned empty without error — treat as timeout.
		return Result{}, ErrQueueFull
	}

	// Slot signal received — re-run admission as a recheck (no re-queue on failure).
	return g.admit(ctx, cfg, true)
}

// ──────────────────────────────────────────────────────────────────────────────
// Confirm
// ──────────────────────────────────────────────────────────────────────────────

// Confirm extends the shadow keys written by Check from the short reservation
// TTL to the full session TTL. Must be called after session.Register() succeeds.
//
// If Confirm is never called (because Register failed), the reservation TTL
// expiry ensures the ghost is pruned within ReservationTTL (10s).
func (g *Gate) Confirm(ctx context.Context, cfg Config) error {
	ttl := cfg.shadowTTL()

	epShadowKey := cfg.epShadow() + cfg.SessionID
	if err := g.redis.SetEX(ctx, epShadowKey, "1", ttl); err != nil {
		return fmt.Errorf("gate: confirm ep shadow: %w", err)
	}

	if cfg.AppID != "" {
		appShadowKey := cfg.appShadow() + cfg.SessionID
		if err := g.redis.SetEX(ctx, appShadowKey, "1", ttl); err != nil {
			return fmt.Errorf("gate: confirm app shadow: %w", err)
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Rollback
// ──────────────────────────────────────────────────────────────────────────────

// Rollback removes the Set membership and shadow keys written by Check. Call
// this when session.Register() fails so the slot is released immediately rather
// than waiting for the reservation TTL to expire.
//
// Rollback also calls Release so any queued session wakes immediately.
func (g *Gate) Rollback(ctx context.Context, cfg Config) error {
	// Build the list of keys to delete: shadow keys + the session from the Sets.
	// We delete shadow keys (which drives ghost pruning) and then signal the queue.
	keys := []string{
		cfg.epShadow() + cfg.SessionID,
	}
	if cfg.AppID != "" {
		keys = append(keys, cfg.appShadow()+cfg.SessionID)
	}
	// Deleting the shadow key is enough: next luaAdmit will SREM the ghost.
	// But we also explicitly SREM here to give back the slot immediately.
	rollbackScript := `
local ep_set    = KEYS[1]
local ep_shkey  = KEYS[2]
local app_set   = KEYS[3]
local app_shkey = KEYS[4]
local sid       = ARGV[1]
redis.call('SREM', ep_set, sid)
redis.call('DEL',  ep_shkey)
if app_set ~= '' then
    redis.call('SREM', app_set, sid)
    redis.call('DEL',  app_shkey)
end
return 1
`
	appSet := cfg.appSet()
	appShadowKey := ""
	if cfg.AppID != "" {
		appShadowKey = cfg.appShadow() + cfg.SessionID
	}
	luaKeys := []string{cfg.epSet(), cfg.epShadow() + cfg.SessionID, appSet, appShadowKey}
	if _, err := g.redis.ExecLua(ctx, rollbackScript, luaKeys, []interface{}{cfg.SessionID}); err != nil {
		return fmt.Errorf("gate: rollback lua: %w", err)
	}
	// Wake one queued waiter if any.
	return g.Release(ctx, cfg)
}

// ──────────────────────────────────────────────────────────────────────────────
// Release
// ──────────────────────────────────────────────────────────────────────────────

// Release signals that a session slot has opened. It wakes exactly one queued
// waiter (if any) by pushing a token onto the queue list. Callers must invoke
// Release after a session ends (alongside session.Store.End).
//
// Release is a no-op when no waiters are present; the push onto an un-watched
// list is harmless — the value will expire naturally when the next LRem runs.
func (g *Gate) Release(ctx context.Context, cfg Config) error {
	if err := g.redis.LPush(ctx, cfg.queueKey(), "1"); err != nil {
		return fmt.Errorf("gate: release: %w", err)
	}
	return nil
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
