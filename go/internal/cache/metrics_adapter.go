package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/metrics"
)

// MetricsRedisClient implements metrics.RedisMetricsClient using rueidis.
type MetricsRedisClient struct {
	client rueidis.Client
}

// NewMetricsRedisClient wraps a rueidis.Client for use as a metrics.RedisMetricsClient.
func NewMetricsRedisClient(client rueidis.Client) *MetricsRedisClient {
	return &MetricsRedisClient{client: client}
}

// HIncrBy atomically increments a hash field by delta.
func (c *MetricsRedisClient) HIncrBy(ctx context.Context, key, field string, delta int64) error {
	cmd := c.client.B().Hincrby().Key(key).Field(field).Increment(delta).Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("metrics: hincrby %s.%s: %w", key, field, err)
	}
	return nil
}

// ExpireAt sets an absolute expiry on a key.
func (c *MetricsRedisClient) ExpireAt(ctx context.Context, key string, t time.Time) error {
	cmd := c.client.B().Expireat().Key(key).Timestamp(t.Unix()).Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("metrics: expireat %s: %w", key, err)
	}
	return nil
}

// PFAdd adds elements to a HyperLogLog key.
func (c *MetricsRedisClient) PFAdd(ctx context.Context, key string, elements ...string) error {
	cmd := c.client.B().Pfadd().Key(key).Element(elements...).Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("metrics: pfadd %s: %w", key, err)
	}
	return nil
}

var _ metrics.RedisMetricsClient = (*MetricsRedisClient)(nil)
