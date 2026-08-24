package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/rueidis"
)

const (
	leaderKey = "them:mcp:leader"
	leaderTTL = 30 * time.Second
)

// LeaderLock uses a Redis SET NX PX lock so only one replica runs the
// health/discovery loop at a time. Other replicas serve /internal/execute
// as pure stateless HTTP servers.
type LeaderLock struct {
	redis      rueidis.Client
	instanceID string
}

// NewLeaderLock creates a LeaderLock for the given instance.
func NewLeaderLock(redis rueidis.Client, instanceID string) *LeaderLock {
	return &LeaderLock{redis: redis, instanceID: instanceID}
}

// TryAcquire attempts to acquire the leader lock. Returns true if this
// instance is now the leader. Renews the TTL if already held by this instance.
func (l *LeaderLock) TryAcquire(ctx context.Context) (bool, error) {
	// SET NX: only set if key does not exist
	cmd := l.redis.B().Set().
		Key(leaderKey).
		Value(l.instanceID).
		Nx().
		Px(leaderTTL).
		Build()
	ok, err := l.redis.Do(ctx, cmd).AsBool()
	if err != nil && !rueidis.IsRedisNil(err) {
		return false, fmt.Errorf("leader: try acquire: %w", err)
	}
	if ok {
		return true, nil
	}

	// Key exists — check if we already hold it (renew our own TTL)
	getCmd := l.redis.B().Get().Key(leaderKey).Build()
	holder, err := l.redis.Do(ctx, getCmd).ToString()
	if err != nil {
		return false, nil // someone else holds it
	}
	if holder == l.instanceID {
		// Renew TTL
		expireCmd := l.redis.B().Pexpire().Key(leaderKey).Milliseconds(leaderTTL.Milliseconds()).Build()
		_ = l.redis.Do(ctx, expireCmd)
		return true, nil
	}
	return false, nil
}

// Release explicitly releases the lock (called on clean shutdown).
func (l *LeaderLock) Release(ctx context.Context) {
	getCmd := l.redis.B().Get().Key(leaderKey).Build()
	holder, err := l.redis.Do(ctx, getCmd).ToString()
	if err != nil || holder != l.instanceID {
		return
	}
	delCmd := l.redis.B().Del().Key(leaderKey).Build()
	_ = l.redis.Do(ctx, delCmd)
}
