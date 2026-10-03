package cache

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/rueidis"
	temporalclient "go.temporal.io/sdk/client"
)

// AppFlowSignalUserInput mirrors appflow.AppFlowSignalUserInput.
// Duplicated here (not imported) to avoid a circular dependency:
// cache → appflow → cache would cycle.
const appFlowSignalUserInputPrefix = "appflow_user_input"

// UserInputSignaler implements ws.UserInputSignaler.
// It reads them:wait:{runID} from Redis: if the key exists, the run is paused
// at a wait_for_input node — send a Temporal signal with the user's message
// and delete the key; otherwise do nothing.
type UserInputSignaler struct {
	redis    rueidis.Client
	temporal temporalclient.Client
}

// NewUserInputSignaler creates a UserInputSignaler backed by the given Redis
// and Temporal clients.
func NewUserInputSignaler(rc rueidis.Client, tc temporalclient.Client) *UserInputSignaler {
	return &UserInputSignaler{redis: rc, temporal: tc}
}

// SignalUserInput checks them:wait:{runID}. If the key exists, it sends the
// Temporal signal appflow_user_input:{nodeID} with message as the payload,
// deletes the key, and returns (true, nil). Returns (false, nil) when the run
// is not waiting. Returns (false, err) on Redis or Temporal errors.
func (s *UserInputSignaler) SignalUserInput(ctx context.Context, tenantID, runID, message string) (bool, error) {
	key := "them:wait:" + runID

	// GET the node ID stored by PendingWaitSetActivity.
	getCmd := s.redis.B().Get().Key(key).Build()
	nodeID, err := s.redis.Do(ctx, getCmd).ToString()
	if err != nil {
		if isNilErr(err) {
			return false, nil // key absent — run not waiting
		}
		return false, fmt.Errorf("SignalUserInput: Redis GET %s: %w", key, err)
	}
	if nodeID == "" {
		return false, nil
	}

	// Send the Temporal signal before deleting the key, so there is no window
	// where the key is gone but the workflow hasn't received the signal yet
	// (a crash between DEL and Signal would lose the message; this order means
	// the worst case is a duplicate signal, which the workflow ignores because
	// the channel is already consumed).
	sigName := appFlowSignalUserInputPrefix + ":" + nodeID
	wfID := "appflow:" + tenantID + ":" + runID
	if err := s.temporal.SignalWorkflow(ctx, wfID, "", sigName, message); err != nil {
		return false, fmt.Errorf("SignalUserInput: SignalWorkflow %s: %w", wfID, err)
	}

	// Delete the key so the NEXT message on this run doesn't re-signal.
	delCmd := s.redis.B().Del().Key(key).Build()
	_ = s.redis.Do(ctx, delCmd).Error() // best-effort; TTL is the fallback

	return true, nil
}

// isNilErr reports whether a rueidis error is the Redis nil bulk-string error
// (key not found). rueidis surfaces this as rueidis.Nil.
func isNilErr(err error) bool {
	return errors.Is(err, rueidis.Nil)
}
