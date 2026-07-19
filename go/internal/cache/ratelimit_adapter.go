package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

// RateLimitClient implements ratelimit.RedisIncrementer using rueidis.
type RateLimitClient struct {
	client rueidis.Client
}

// NewRateLimitClient wraps a rueidis.Client for use as a ratelimit.RedisIncrementer.
func NewRateLimitClient(client rueidis.Client) *RateLimitClient {
	return &RateLimitClient{client: client}
}

// Incr atomically increments the integer at key and returns the new value.
// If the key does not exist, it is created with value 0 before incrementing.
func (c *RateLimitClient) Incr(ctx context.Context, key string) (int64, error) {
	cmd := c.client.B().Incr().Key(key).Build()
	res := c.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		return 0, fmt.Errorf("ratelimit: incr %s: %w", key, err)
	}
	return res.AsInt64()
}

// Expire sets the TTL for an existing key. Best-effort — called after Incr.
func (c *RateLimitClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	cmd := c.client.B().Expire().Key(key).Seconds(int64(ttl.Seconds())).Build()
	return c.client.Do(ctx, cmd).Error()
}
