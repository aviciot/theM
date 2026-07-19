package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

// SessionRedisClient implements session.RedisClient using a rueidis.Client.
// It provides all the operations needed by the session store.
type SessionRedisClient struct {
	client rueidis.Client
}

// NewSessionRedisClient wraps a rueidis.Client for use as a session.RedisClient.
func NewSessionRedisClient(client rueidis.Client) *SessionRedisClient {
	return &SessionRedisClient{client: client}
}

// HSetEx stores the fields of a hash and sets its TTL atomically via a Lua script.
func (s *SessionRedisClient) HSetEx(ctx context.Context, key string, ttl time.Duration, fields map[string]string) error {
	// HMSET then EXPIRE.
	pairs := make([]string, 0, len(fields)*2)
	for k, v := range fields {
		pairs = append(pairs, k, v)
	}
	args := make([]interface{}, 0, len(pairs)+1)
	for _, p := range pairs {
		args = append(args, p)
	}
	// Build HSET command
	b := s.client.B().Hset().Key(key).FieldValue()
	for k, v := range fields {
		b = b.FieldValue(k, v)
	}
	res := s.client.Do(ctx, b.Build())
	if err := res.Error(); err != nil {
		return err
	}
	exp := s.client.B().Expire().Key(key).Seconds(int64(ttl.Seconds())).Build()
	return s.client.Do(ctx, exp).Error()
}

// HGetAll returns all fields of a hash. Returns empty map if key missing.
func (s *SessionRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	cmd := s.client.B().Hgetall().Key(key).Build()
	res := s.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		return nil, err
	}
	return res.AsStrMap()
}

// Del deletes one or more keys.
func (s *SessionRedisClient) Del(ctx context.Context, keys ...string) error {
	cmd := s.client.B().Del().Key(keys...).Build()
	return s.client.Do(ctx, cmd).Error()
}

// Expire refreshes the TTL of a key.
func (s *SessionRedisClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	cmd := s.client.B().Expire().Key(key).Seconds(int64(ttl.Seconds())).Build()
	return s.client.Do(ctx, cmd).Error()
}

// Exists returns whether the given key exists.
func (s *SessionRedisClient) Exists(ctx context.Context, key string) (bool, error) {
	cmd := s.client.B().Exists().Key(key).Build()
	res := s.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		return false, err
	}
	n, err := res.AsInt64()
	return n > 0, err
}

// ExecLua runs a Lua script with keys and args.
func (s *SessionRedisClient) ExecLua(ctx context.Context, script string, keys []string, args []interface{}) (interface{}, error) {
	strArgs := make([]string, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			strArgs[i] = v
		case int:
			strArgs[i] = fmt.Sprintf("%d", v)
		default:
			strArgs[i] = fmt.Sprintf("%v", v)
		}
	}
	cmd := s.client.B().Eval().Script(script).Numkeys(int64(len(keys))).Key(keys...).Arg(strArgs...).Build()
	res := s.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		return nil, err
	}
	return res.AsInt64()
}

// Publish sends payload on channel.
func (s *SessionRedisClient) Publish(ctx context.Context, channel, payload string) error {
	cmd := s.client.B().Publish().Channel(channel).Message(payload).Build()
	return s.client.Do(ctx, cmd).Error()
}

// Subscribe blocks and delivers messages to handler until ctx is cancelled.
func (s *SessionRedisClient) Subscribe(ctx context.Context, channel string, handler func(payload string)) error {
	cmd := s.client.B().Subscribe().Channel(channel).Build()
	err := s.client.Receive(ctx, cmd, func(msg rueidis.PubSubMessage) {
		handler(msg.Message)
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

