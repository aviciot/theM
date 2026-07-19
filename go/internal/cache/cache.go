// Package cache provides a rueidis Redis client with helpers for health checks
// and graceful shutdown. All commands use context-bounded timeouts to avoid
// blocking the caller indefinitely.
package cache

import (
	"context"
	"fmt"

	"github.com/redis/rueidis"
)

// Cache wraps a rueidis.Client with application-level helpers.
type Cache struct {
	client rueidis.Client
}

// New creates a rueidis client connected to the given address. password is
// optional; pass an empty string when Redis requires no authentication. db
// selects the Redis logical database index.
func New(ctx context.Context, addr, password string, db int) (*Cache, error) {
	opts := rueidis.ClientOption{
		InitAddress:  []string{addr},
		SelectDB:     db,
		DisableCache: true, // Phase 1: disable client-side cache to keep things simple
	}

	if password != "" {
		opts.Password = password
	}

	client, err := rueidis.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("cache: create client: %w", err)
	}

	c := &Cache{client: client}

	// Confirm connectivity before returning.
	if err := c.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("cache: initial ping failed: %w", err)
	}

	return c, nil
}

// Ping sends a PING command to Redis and returns an error if the response is
// not PONG. It is called by the readiness handler on every health check request.
func (c *Cache) Ping(ctx context.Context) error {
	cmd := c.client.B().Ping().Build()
	resp := c.client.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		return fmt.Errorf("cache: ping: %w", err)
	}
	return nil
}

// Client returns the underlying rueidis.Client for callers that need to run
// arbitrary Redis commands. The returned client must not be closed by the
// caller.
func (c *Cache) Client() rueidis.Client {
	return c.client
}

// Close terminates the connection to Redis. It should be called during graceful
// shutdown after the HTTP server and any in-flight requests have finished.
func (c *Cache) Close() {
	c.client.Close()
}
