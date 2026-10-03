package cache

import (
	"context"
	"time"

	"github.com/redis/rueidis"
)

// pendingWaitTTL is the Redis TTL for the pending-wait key.
// Sized at 4 hours — long enough for any realistic conversational wait
// but bounded so a crashed run never leaves stale routing state forever.
const pendingWaitTTL = 4 * time.Hour

// PendingWaitRedisClient implements appflow.PendingWaitStore via rueidis.
// It writes/deletes the them:wait:{runID} key used by the WS/SSE handler
// to route subsequent user messages as Temporal signals.
type PendingWaitRedisClient struct {
	c rueidis.Client
}

// NewPendingWaitRedisClient wraps a rueidis client for pending-wait key writes.
func NewPendingWaitRedisClient(c rueidis.Client) *PendingWaitRedisClient {
	return &PendingWaitRedisClient{c: c}
}

// SetWait writes them:wait:{runID} = nodeID with a 4-hour TTL.
func (p *PendingWaitRedisClient) SetWait(ctx context.Context, runID, nodeID string) error {
	key := "them:wait:" + runID
	cmd := p.c.B().Set().Key(key).Value(nodeID).Ex(pendingWaitTTL).Build()
	return p.c.Do(ctx, cmd).Error()
}

// DelWait removes them:wait:{runID}.
func (p *PendingWaitRedisClient) DelWait(ctx context.Context, runID string) error {
	key := "them:wait:" + runID
	cmd := p.c.B().Del().Key(key).Build()
	return p.c.Do(ctx, cmd).Error()
}
