package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

// GateRedisClient implements gate.RedisClient using a rueidis.Client.
// It provides the atomic Lua execution, SetEX, Del, BLPop, and LPush
// operations required by the admission gate.
type GateRedisClient struct {
	client rueidis.Client
}

// NewGateRedisClient wraps a rueidis.Client for use as a gate.RedisClient.
func NewGateRedisClient(client rueidis.Client) *GateRedisClient {
	return &GateRedisClient{client: client}
}

// ExecLua runs a Lua script atomically with the given keys and args.
func (g *GateRedisClient) ExecLua(ctx context.Context, script string, keys []string, args []interface{}) (interface{}, error) {
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
	cmd := g.client.B().Eval().Script(script).Numkeys(int64(len(keys))).Key(keys...).Arg(strArgs...).Build()
	res := g.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		return nil, err
	}
	return res.ToAny()
}

// SetEX sets key=value with the given TTL.
func (g *GateRedisClient) SetEX(ctx context.Context, key, value string, ttl time.Duration) error {
	cmd := g.client.B().Set().Key(key).Value(value).Ex(ttl).Build()
	return g.client.Do(ctx, cmd).Error()
}

// Del deletes one or more keys.
func (g *GateRedisClient) Del(ctx context.Context, keys ...string) error {
	cmd := g.client.B().Del().Key(keys...).Build()
	return g.client.Do(ctx, cmd).Error()
}

// BLPop blocks until an element is available in the list or the timeout expires.
// Returns ("", "", nil) on timeout. Returns ("", "", ctx.Err()) on context cancel.
func (g *GateRedisClient) BLPop(ctx context.Context, timeout time.Duration, key string) (string, error) {
	secs := int64(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	cmd := g.client.B().Blpop().Key(key).Timeout(float64(secs)).Build()
	res := g.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		// rueidis returns an error for timeout (nil bulk reply); treat as timeout.
		return "", nil
	}
	vals, err := res.AsStrSlice()
	if err != nil || len(vals) < 2 {
		return "", nil
	}
	return vals[1], nil
}

// LPush pushes value to the head of key.
func (g *GateRedisClient) LPush(ctx context.Context, key, value string) error {
	cmd := g.client.B().Lpush().Key(key).Element(value).Build()
	return g.client.Do(ctx, cmd).Error()
}
