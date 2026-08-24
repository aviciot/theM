package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/rueidis"
)

const (
	manifestKeyPrefix = "them:mcp:manifest:"
	healthKeyPrefix   = "them:mcp:health:"
	manifestTTL       = 5 * time.Minute
	healthTTL         = 90 * time.Second

	// ManifestChangedChannel is published to after every discovery run so
	// them-go-bridge replicas can invalidate their in-process tool-list caches.
	ManifestChangedChannel = "them:mcp:manifest:changed"
)

// Registry is an in-process cache of MCP server records backed by Redis.
// It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]Server // keyed by server ID
	redis   rueidis.Client
}

// NewRegistry creates an empty Registry backed by the given Redis client.
func NewRegistry(redis rueidis.Client) *Registry {
	return &Registry{
		servers: make(map[string]Server),
		redis:   redis,
	}
}

// Populate replaces the in-process cache with the provided server list.
// Called by the health loop after each DB reload.
func (r *Registry) Populate(servers []Server) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]Server, len(servers))
	for _, s := range servers {
		next[s.ID] = s
	}
	r.servers = next
}

// All returns a snapshot of all cached servers.
func (r *Registry) All() []Server {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Server, 0, len(r.servers))
	for _, s := range r.servers {
		out = append(out, s)
	}
	return out
}

// GetByID returns a server by ID from the in-process cache.
func (r *Registry) GetByID(id string) (Server, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.servers[id]
	return s, ok
}

// UpdateServer updates a single entry in the in-process cache.
func (r *Registry) UpdateServer(s Server) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[s.ID] = s
}

// CacheManifest writes a server's tools_manifest to Redis with a 5-min TTL.
func (r *Registry) CacheManifest(ctx context.Context, slug string, manifest json.RawMessage) error {
	cmd := r.redis.B().Set().
		Key(manifestKeyPrefix + slug).
		Value(string(manifest)).
		Ex(manifestTTL).
		Build()
	if err := r.redis.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("registry: cache manifest %s: %w", slug, err)
	}
	return nil
}

// GetCachedManifest returns a manifest from Redis cache. Returns nil, nil on miss.
func (r *Registry) GetCachedManifest(ctx context.Context, slug string) (json.RawMessage, error) {
	cmd := r.redis.B().Get().Key(manifestKeyPrefix + slug).Build()
	val, err := r.redis.Do(ctx, cmd).ToString()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("registry: get manifest %s: %w", slug, err)
	}
	return json.RawMessage(val), nil
}

// CacheHealth writes the latest health probe result to Redis with a 90s TTL.
func (r *Registry) CacheHealth(ctx context.Context, slug, status, lastError string) error {
	type healthEntry struct {
		Status    string `json:"status"`
		LastError string `json:"last_error,omitempty"`
		UpdatedAt string `json:"updated_at"`
	}
	data, _ := json.Marshal(healthEntry{
		Status:    status,
		LastError: lastError,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	cmd := r.redis.B().Set().
		Key(healthKeyPrefix + slug).
		Value(string(data)).
		Ex(healthTTL).
		Build()
	if err := r.redis.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("registry: cache health %s: %w", slug, err)
	}
	return nil
}

// PublishManifestChanged broadcasts a manifest-changed notification so
// them-go-bridge replicas invalidate their in-process caches.
func (r *Registry) PublishManifestChanged(ctx context.Context, slug string) error {
	cmd := r.redis.B().Publish().Channel(ManifestChangedChannel).Message(slug).Build()
	if err := r.redis.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("registry: publish manifest changed %s: %w", slug, err)
	}
	return nil
}
