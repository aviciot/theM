package cache

import (
	"context"

	"github.com/redis/rueidis"
)

// AdminCacheClient is a simple Redis adapter for the admin package's
// CacheInvalidator interface. It wraps a rueidis.Client and provides a single
// Del operation.
type AdminCacheClient struct {
	client rueidis.Client
}

// NewAdminCacheClient wraps a rueidis.Client for use as an admin.CacheInvalidator.
func NewAdminCacheClient(client rueidis.Client) *AdminCacheClient {
	return &AdminCacheClient{client: client}
}

// Del deletes the given key. Satisfies admin.CacheInvalidator.
func (c *AdminCacheClient) Del(ctx context.Context, key string) error {
	cmd := c.client.B().Del().Key(key).Build()
	return c.client.Do(ctx, cmd).Error()
}
