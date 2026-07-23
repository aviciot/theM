// Package epconfig resolves Entry Point and Application runtime configuration
// from the database on every inbound WS/SSE connection.
//
// # Precedence rules
//
//   - EPMaxConcurrent  ← entry_points.max_concurrent_sessions  (NULL → 0 = unlimited)
//   - AppMaxConcurrent ← applications.runtime_config.max_concurrent_sessions (missing/0 = unlimited)
//   - RateLimitRPM     ← applications.runtime_config.rate_limit_rpm (missing/0 = unlimited)
//   - QueueTimeout     ← entry_points.queue_timeout_seconds * time.Second (NULL/0 → no queue)
//   - EPEnabled        ← entry_points.enabled (false → fail-closed 403)
//   - AppEnabled       ← applications.enabled (false → fail-closed 403)
//   - AccessMode       ← entry_points.access_policy.mode ("public" | "token")
//   - BlockedTokens    ← applications.runtime_config.blocked_tokens (SHA-256 hex list)
//   - BlockedUserIDs   ← applications.runtime_config.blocked_user_ids
//
// # Fail-closed policy
//
// DB unavailability → ErrDBUnavailable → handler returns 503.
// Malformed JSONB → warning logged, unknown keys ignored; if root is not an
// object, treated as {} (unlimited). Disabled EP/App → ErrDisabled → 403.
// Blocked token/user → ErrBlocked → 403.
//
// # Caching
//
// EPConfig rows are cached in-process for CacheTTL (30 s). Only enabled configs
// are cached: a disabled EP is never stored, so every request re-queries until
// it is re-enabled.
//
// # Cross-pod cache invalidation
//
// When the admin API mutates an EP or application it publishes the EP slug on
// the Redis channel EPConfigChannel ("them:ep:config:changed"). Call
// Loader.Subscribe(ctx, subscriber) at startup to have every pod's in-process
// cache evicted immediately on receiving the message. Without a subscriber the
// TTL (30 s) is the stale-data safety net.
package epconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Constants
// ──────────────────────────────────────────────────────────────────────────────

// CacheTTL is the maximum age of a cached EPConfig before it is re-fetched.
const CacheTTL = 30 * time.Second

// EPConfigChannel is the Redis pub/sub channel on which the admin API publishes
// EP slug strings whenever an entry point or its parent application is mutated.
// Every pod subscribes on startup; receiving a slug evicts that slug's cache entry.
const EPConfigChannel = "them:ep:config:changed"

// AccessModePublic means no bearer token is required.
const AccessModePublic = "public"

// AccessModeToken means a valid bearer token is required (default).
const AccessModeToken = "token"

// ──────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ──────────────────────────────────────────────────────────────────────────────

// ErrDisabled is returned when the EP or its parent Application is disabled.
var ErrDisabled = errors.New("epconfig: entry point or application is disabled")

// ErrBlocked is returned when the token hash or user ID is on the app block-list.
var ErrBlocked = errors.New("epconfig: token or user is blocked by application policy")

// ErrNotFound is returned when no entry point row exists for the given slug.
var ErrNotFound = errors.New("epconfig: entry point not found")

// ErrDBUnavailable is returned when the DB query fails.
var ErrDBUnavailable = errors.New("epconfig: database unavailable")

// ──────────────────────────────────────────────────────────────────────────────
// EPConfig — resolved runtime configuration for one EP + its Application
// ──────────────────────────────────────────────────────────────────────────────

// EPConfig is the resolved, typed runtime configuration for one Entry Point.
// All limit fields use 0 to mean "unlimited" / "no limit".
type EPConfig struct {
	// Identity
	EPID          string // entry_points.id (UUID string)
	AppID         string // applications.id (UUID string)
	EPSlug        string
	AppEnabled    bool
	EPEnabled     bool
	EPType        string // "websocket" | "sse" | etc.
	AccessMode    string // "public" | "token"

	// Session limits
	EPMaxConcurrent  int           // entry_points.max_concurrent_sessions; 0 = unlimited
	AppMaxConcurrent int           // runtime_config.max_concurrent_sessions; 0 = unlimited
	RateLimitRPM     int           // runtime_config.rate_limit_rpm; 0 = unlimited
	QueueTimeout     time.Duration // entry_points.queue_timeout_seconds; 0 = no queue

	// Block-lists (from applications.runtime_config)
	BlockedTokenHashes []string // SHA-256 hex
	BlockedUserIDs     []int64

	// Timestamps (for cache management)
	fetchedAt time.Time
}

// expired reports whether this entry is older than CacheTTL.
func (c *EPConfig) expired() bool {
	return time.Since(c.fetchedAt) > CacheTTL
}

// ──────────────────────────────────────────────────────────────────────────────
// appRuntimeConfig — parsed from applications.runtime_config JSONB
// ──────────────────────────────────────────────────────────────────────────────

type appRuntimeConfig struct {
	MaxConcurrentSessions int     `json:"max_concurrent_sessions"`
	RateLimitRPM          int     `json:"rate_limit_rpm"`
	BlockedTokens         []string `json:"blocked_tokens"`
	BlockedUserIDs        []int64  `json:"blocked_user_ids"`
}

// parseRuntimeConfig parses the JSONB bytes from applications.runtime_config.
// Unknown keys are ignored. If data is nil/empty, returns zero-value config.
// Malformed JSON (not an object) returns zero-value with a logged warning.
func parseRuntimeConfig(logger *slog.Logger, data []byte) appRuntimeConfig {
	var cfg appRuntimeConfig
	if len(data) == 0 || string(data) == "null" || string(data) == "{}" {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.Warn("epconfig: malformed runtime_config, treating as unlimited", "error", err)
		return appRuntimeConfig{}
	}
	// Negative values treated as unlimited.
	if cfg.MaxConcurrentSessions < 0 {
		cfg.MaxConcurrentSessions = 0
	}
	if cfg.RateLimitRPM < 0 {
		cfg.RateLimitRPM = 0
	}
	return cfg
}

// ──────────────────────────────────────────────────────────────────────────────
// accessPolicy — parsed from entry_points.access_policy JSONB
// ──────────────────────────────────────────────────────────────────────────────

type accessPolicy struct {
	Mode string `json:"mode"`
}

func parseAccessPolicy(logger *slog.Logger, data []byte) string {
	if len(data) == 0 {
		return AccessModeToken
	}
	var p accessPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		logger.Warn("epconfig: malformed access_policy, defaulting to token auth", "error", err)
		return AccessModeToken
	}
	if p.Mode == AccessModePublic {
		return AccessModePublic
	}
	return AccessModeToken
}

// ──────────────────────────────────────────────────────────────────────────────
// DBQuerier — interface for the DB query
// ──────────────────────────────────────────────────────────────────────────────

// EPConfigRow is the raw data returned by a single DB query joining
// them.entry_points and them.applications.
type EPConfigRow struct {
	EPID                    string // UUID string
	AppID                   string // UUID string
	EPSlug                  string
	EPType                  string
	EPEnabled               bool
	EPMaxConcurrentSessions *int // NULL = unlimited
	EPQueueTimeoutSeconds   *int // NULL = no queue
	AccessPolicyJSON        []byte
	AppEnabled              bool
	AppRuntimeConfigJSON    []byte
}

// DBQuerier is the single query needed by the epconfig loader.
type DBQuerier interface {
	// QueryEPConfig fetches the config row for the given EP slug.
	// Returns ErrNotFound (wrapped) when no matching row exists.
	QueryEPConfig(ctx context.Context, epSlug string) (*EPConfigRow, error)
}

// ──────────────────────────────────────────────────────────────────────────────
// Loader
// ──────────────────────────────────────────────────────────────────────────────

// Loader resolves EPConfig for a given entry point slug. It caches results
// for CacheTTL to avoid a DB query on every connection.
type Loader struct {
	db     DBQuerier
	logger *slog.Logger

	mu    sync.Mutex
	cache map[string]*EPConfig // keyed by ep_slug
}

// NewLoader creates a Loader backed by the given DB querier.
func NewLoader(db DBQuerier, logger *slog.Logger) *Loader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Loader{
		db:     db,
		logger: logger,
		cache:  make(map[string]*EPConfig),
	}
}

// Load resolves the EPConfig for the given EP slug.
//
// Errors:
//   - ErrNotFound — no entry point with this slug exists
//   - ErrDBUnavailable — DB query failed
//
// Callers should additionally call CheckAccess after Load to enforce
// enabled/blocked checks.
func (l *Loader) Load(ctx context.Context, epSlug string) (*EPConfig, error) {
	l.mu.Lock()
	if cached, ok := l.cache[epSlug]; ok && !cached.expired() {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	row, err := l.db.QueryEPConfig(ctx, epSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		l.logger.Warn("epconfig: db query failed", "ep_slug", epSlug, "error", err)
		return nil, fmt.Errorf("%w: %v", ErrDBUnavailable, err)
	}

	cfg := l.buildConfig(row)

	// Only cache enabled configs. A disabled EP will always re-query until
	// re-enabled, ensuring the disabled state is enforced within one request.
	if cfg.EPEnabled && cfg.AppEnabled {
		l.mu.Lock()
		l.cache[epSlug] = cfg
		l.mu.Unlock()
	}

	return cfg, nil
}

// Invalidate evicts the cached config for the given EP slug. Call this when
// the admin API mutates an entry point or its parent application.
func (l *Loader) Invalidate(epSlug string) {
	l.mu.Lock()
	delete(l.cache, epSlug)
	l.mu.Unlock()
}

// InvalidateApp evicts all cached EP configs for entries whose AppID matches.
// Used when applications.runtime_config is updated.
func (l *Loader) InvalidateApp(appID string) {
	l.mu.Lock()
	for slug, cfg := range l.cache {
		if cfg.AppID == appID {
			delete(l.cache, slug)
		}
	}
	l.mu.Unlock()
}

// RedisSubscriber can subscribe to a Redis pub/sub channel and deliver messages
// to a callback function. Implemented by cache adapters (e.g., the session
// adapter already provides this pattern).
type RedisSubscriber interface {
	// Subscribe blocks until ctx is cancelled, invoking handler for each message
	// received on channel. Returns nil if ctx was cancelled; any other error
	// indicates a connection failure.
	Subscribe(ctx context.Context, channel string, handler func(payload string)) error
}

// Subscribe starts a background goroutine that listens on EPConfigChannel.
// Message payloads are either:
//   - An EP slug string → evict that single entry from the cache.
//   - A UUID string (app_id, 36 chars with hyphens) → evict all cached entries
//     belonging to that application (published by Python admin when app config changes).
//
// Call Subscribe once at startup after creating the Loader. It is optional:
// without it the 30-second TTL alone bounds staleness.
func (l *Loader) Subscribe(ctx context.Context, sub RedisSubscriber) {
	go func() {
		err := sub.Subscribe(ctx, EPConfigChannel, func(payload string) {
			if looksLikeUUID(payload) {
				l.InvalidateApp(payload)
				l.logger.Debug("epconfig: app cache evicted via pub/sub", "app_id", payload)
			} else {
				l.Invalidate(payload)
				l.logger.Debug("epconfig: ep cache evicted via pub/sub", "ep_slug", payload)
			}
		})
		if err != nil && ctx.Err() == nil {
			l.logger.Warn("epconfig: pub/sub subscriber exited with error", "error", err)
		}
	}()
}

// looksLikeUUID returns true if s has the standard UUID hyphenated format (8-4-4-4-12).
// Used to distinguish app_id payloads from EP slug payloads on the config channel.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func (l *Loader) buildConfig(row *EPConfigRow) *EPConfig {
	rt := parseRuntimeConfig(l.logger, row.AppRuntimeConfigJSON)
	accessMode := parseAccessPolicy(l.logger, row.AccessPolicyJSON)

	epMax := 0
	if row.EPMaxConcurrentSessions != nil && *row.EPMaxConcurrentSessions > 0 {
		epMax = *row.EPMaxConcurrentSessions
	}

	var queueTimeout time.Duration
	if row.EPQueueTimeoutSeconds != nil && *row.EPQueueTimeoutSeconds > 0 {
		queueTimeout = time.Duration(*row.EPQueueTimeoutSeconds) * time.Second
	}

	return &EPConfig{
		EPID:             row.EPID,
		AppID:            row.AppID,
		EPSlug:           row.EPSlug,
		EPType:           row.EPType,
		EPEnabled:        row.EPEnabled,
		AppEnabled:       row.AppEnabled,
		AccessMode:       accessMode,
		EPMaxConcurrent:  epMax,
		AppMaxConcurrent: rt.MaxConcurrentSessions,
		RateLimitRPM:     rt.RateLimitRPM,
		QueueTimeout:     queueTimeout,
		BlockedTokenHashes: rt.BlockedTokens,
		BlockedUserIDs:     rt.BlockedUserIDs,
		fetchedAt:        time.Now(),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// CheckAccess — enforces enabled/blocked policy after Load
// ──────────────────────────────────────────────────────────────────────────────

// CheckAccess enforces the security-critical checks that must never be cached:
//   - EP disabled → ErrDisabled
//   - Application disabled → ErrDisabled
//   - Token hash in blocked list → ErrBlocked
//   - User ID in blocked list → ErrBlocked
//
// These are checked on every request even when the EPConfig came from cache,
// because a disabled state must take effect immediately (fail-closed).
//
// tokenHash is the SHA-256 hex of the raw bearer token (empty string if public EP).
// userID is the authenticated user ID (0 if public EP).
func CheckAccess(cfg *EPConfig, tokenHash string, userID int64) error {
	if !cfg.AppEnabled {
		return ErrDisabled
	}
	if !cfg.EPEnabled {
		return ErrDisabled
	}

	// Block-list checks — only relevant for authenticated (non-public) EPs.
	if tokenHash != "" {
		for _, blocked := range cfg.BlockedTokenHashes {
			if blocked == tokenHash {
				return ErrBlocked
			}
		}
	}
	if userID != 0 {
		for _, blocked := range cfg.BlockedUserIDs {
			if blocked == userID {
				return ErrBlocked
			}
		}
	}
	return nil
}
