package auth

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
// Public types
// ──────────────────────────────────────────────────────────────────────────────

// TokenInfo holds the data for a validated bearer token.
type TokenInfo struct {
	TokenID     int64    `json:"token_id"`
	AppID       int64    `json:"app_id,omitempty"`
	Permissions []string `json:"permissions"`
	CreatedAt   int64    `json:"created_at"`
	ExpiresAt   int64    `json:"expires_at,omitempty"` // 0 = no expiry
}

// ErrTokenNotFound is returned when a token does not exist in any tier
// (L1, L2, DB) or has been revoked.
var ErrTokenNotFound = errors.New("auth: token not found or revoked")

// ──────────────────────────────────────────────────────────────────────────────
// Interfaces for dependency injection (enables unit tests without live infra)
// ──────────────────────────────────────────────────────────────────────────────

// TokenRow is the result of the DB lookup query.
type TokenRow struct {
	ID            int64
	ApplicationID int64
	Permissions   []string
	CreatedAt     time.Time
	ExpiresAt     *time.Time // nil means no expiry
}

// TokenQuerier abstracts the PostgreSQL query used to look up an access token
// by its hash. Implementations must check revoked=false and expiry.
type TokenQuerier interface {
	// QueryToken returns a TokenRow for the given sha256-hex token hash,
	// filtering for revoked=false and unexpired rows.
	// Returns ErrTokenNotFound when no matching row exists.
	QueryToken(ctx context.Context, hashHex string) (*TokenRow, error)
}

// RedisClient abstracts the Redis operations used by the token cache so tests
// can inject a fake without a live Redis server.
type RedisClient interface {
	// Get returns the raw JSON bytes stored at the given key, and whether the
	// key was found.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// SetEX stores value at key with the given TTL.
	SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del deletes the given key (used to evict L2 on revocation).
	Del(ctx context.Context, key string) error
	// Publish publishes payload on channel.
	Publish(ctx context.Context, channel, payload string) error
	// Subscribe blocks and delivers messages on channel to handler. Must return
	// when ctx is cancelled.
	Subscribe(ctx context.Context, channel string, handler func(payload string)) error
}

// ──────────────────────────────────────────────────────────────────────────────
// L1 entry
// ──────────────────────────────────────────────────────────────────────────────

const (
	l1TTL              = 300 * time.Second
	redisL2TTL         = 300 * time.Second
	revokeChannel      = "them:token:revoked"
	l2KeyPrefix        = "them:session:token:"
)

type l1Entry struct {
	info      *TokenInfo
	expiresAt time.Time
}

func (e *l1Entry) expired() bool {
	return time.Now().After(e.expiresAt)
}

// ──────────────────────────────────────────────────────────────────────────────
// Cache implementation
// ──────────────────────────────────────────────────────────────────────────────

// Cache validates bearer tokens with two-level caching:
//
//	L1: in-process sync.Map, TTL l1TTL per entry
//	L2: Redis HASH at them:session:token:{sha256_hex}, TTL l2TTL
//
// Cross-pod revocation: on Revoke() the revoker publishes the sha256_hex on
// the them:token:revoked channel. Every pod's Subscribe() goroutine removes
// the entry from L1 immediately.
type Cache struct {
	db     TokenQuerier
	redis  RedisClient
	l1     sync.Map // key: sha256_hex string → *l1Entry
	logger *slog.Logger
}

// NewCache constructs a Cache with the provided DB and Redis clients.
// Call Subscribe(ctx) in a goroutine after construction to enable cross-pod
// invalidation.
func NewCache(db TokenQuerier, redis RedisClient, logger *slog.Logger) *Cache {
	return &Cache{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// Validate looks up rawToken through L1 → L2 → DB.
// On a DB hit the result is written into L2 and L1 for future requests.
// Returns ErrTokenNotFound when the token does not exist or is revoked.
func (c *Cache) Validate(ctx context.Context, rawToken string) (*TokenInfo, error) {
	hash := tokenHash(rawToken)

	// ── L1 check ─────────────────────────────────────────────────────────────
	if v, ok := c.l1.Load(hash); ok {
		entry := v.(*l1Entry)
		if !entry.expired() {
			// Also check whether the token's own expiry has passed.
			if entry.info.ExpiresAt > 0 && time.Now().Unix() > entry.info.ExpiresAt {
				c.l1.Delete(hash)
			} else {
				return entry.info, nil
			}
		} else {
			c.l1.Delete(hash)
		}
	}

	// ── L2 check (Redis) ─────────────────────────────────────────────────────
	l2Key := l2KeyPrefix + hash
	if raw, found, err := c.redis.Get(ctx, l2Key); err == nil && found && len(raw) > 0 {
		var info TokenInfo
		if err := json.Unmarshal(raw, &info); err == nil {
			// Valid L2 hit — populate L1 and return.
			if info.ExpiresAt == 0 || time.Now().Unix() <= info.ExpiresAt {
				c.storeL1(hash, &info)
				return &info, nil
			}
			// Token has expired; remove stale L2 entry.
			_ = c.redis.Del(ctx, l2Key)
		}
	}

	// ── DB lookup ────────────────────────────────────────────────────────────
	row, err := c.db.QueryToken(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("auth: cache db lookup: %w", err)
	}

	info := rowToTokenInfo(row)

	// Populate L2 (best-effort — a Redis error must not block the caller).
	if encoded, err := json.Marshal(info); err == nil {
		_ = c.redis.SetEX(ctx, l2Key, encoded, redisL2TTL)
	}

	// Populate L1.
	c.storeL1(hash, info)

	return info, nil
}

// Revoke invalidates a token across all layers:
//  1. Deletes the Redis L2 key.
//  2. Publishes the sha256_hex on the them:token:revoked channel so all pods
//     immediately evict from L1.
//  3. Evicts from L1 on this pod.
//
// The caller is responsible for marking the token as revoked in the database
// (the cache does not write to them.access_tokens).
func (c *Cache) Revoke(ctx context.Context, rawToken string) error {
	hash := tokenHash(rawToken)
	l2Key := l2KeyPrefix + hash

	// Remove from L2 (best-effort).
	if err := c.redis.Del(ctx, l2Key); err != nil {
		c.logger.Warn("auth: revoke: failed to delete L2 key", "key", l2Key, "error", err)
	}

	// Broadcast to all pods.
	if err := c.redis.Publish(ctx, revokeChannel, hash); err != nil {
		c.logger.Warn("auth: revoke: failed to publish invalidation", "channel", revokeChannel, "error", err)
	}

	// Evict on this pod.
	c.l1.Delete(hash)

	return nil
}

// Subscribe starts the pub/sub listener for cross-pod token invalidation.
// It blocks until ctx is cancelled and should be run in a dedicated goroutine.
// On receipt of a message on them:token:revoked, it evicts the named hash from
// the L1 cache of this pod.
func (c *Cache) Subscribe(ctx context.Context) {
	c.logger.Info("auth: token cache pub/sub listener started", "channel", revokeChannel)
	err := c.redis.Subscribe(ctx, revokeChannel, func(payload string) {
		if payload == "" {
			return
		}
		c.l1.Delete(payload)
		c.logger.Debug("auth: L1 eviction via pub/sub", "hash", payload)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		c.logger.Error("auth: pub/sub listener exited with error", "error", err)
	}
}

// Close is a no-op; the Redis client is owned by the caller and must be closed
// separately.
func (c *Cache) Close() error { return nil }

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func (c *Cache) storeL1(hash string, info *TokenInfo) {
	c.l1.Store(hash, &l1Entry{
		info:      info,
		expiresAt: time.Now().Add(l1TTL),
	})
}

func rowToTokenInfo(row *TokenRow) *TokenInfo {
	info := &TokenInfo{
		TokenID:     row.ID,
		AppID:       row.ApplicationID,
		Permissions: row.Permissions,
		CreatedAt:   row.CreatedAt.Unix(),
	}
	if row.ExpiresAt != nil {
		info.ExpiresAt = row.ExpiresAt.Unix()
	}
	return info
}
