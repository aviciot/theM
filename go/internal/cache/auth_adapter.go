package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/rueidis"
)

// AuthRedisClient implements auth.RedisClient using a rueidis.Client.
// It provides all five operations needed by auth.Cache: Get, SetEX, Del,
// Publish, and Subscribe.
type AuthRedisClient struct {
	client rueidis.Client
}

// NewAuthRedisClient wraps a rueidis.Client for use as an auth.RedisClient.
func NewAuthRedisClient(client rueidis.Client) *AuthRedisClient {
	return &AuthRedisClient{client: client}
}

// Get returns the raw bytes stored at key and whether the key was found.
func (a *AuthRedisClient) Get(ctx context.Context, key string) ([]byte, bool, error) {
	cmd := a.client.B().Get().Key(key).Build()
	res := a.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	raw, err := res.AsBytes()
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// SetEX stores value at key with the given TTL.
func (a *AuthRedisClient) SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := a.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()
	return a.client.Do(ctx, cmd).Error()
}

// Del deletes the given key.
func (a *AuthRedisClient) Del(ctx context.Context, key string) error {
	cmd := a.client.B().Del().Key(key).Build()
	return a.client.Do(ctx, cmd).Error()
}

// Publish publishes payload on channel.
func (a *AuthRedisClient) Publish(ctx context.Context, channel, payload string) error {
	cmd := a.client.B().Publish().Channel(channel).Message(payload).Build()
	return a.client.Do(ctx, cmd).Error()
}

// Subscribe blocks and delivers messages on channel to handler until ctx is
// cancelled.
func (a *AuthRedisClient) Subscribe(ctx context.Context, channel string, handler func(payload string)) error {
	cmd := a.client.B().Subscribe().Channel(channel).Build()
	err := a.client.Receive(ctx, cmd, func(msg rueidis.PubSubMessage) {
		handler(msg.Message)
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
