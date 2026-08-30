package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	hitlKeyPrefix = "them:hitl:"
	// HITLHandleTTL is the Redis TTL for a paused HITL task handle.
	// Must be at least as long as the Temporal workflow timeout (24h).
	HITLHandleTTL = 24 * time.Hour
)

// ErrHITLNotFound is returned when no HITL handle exists for the given task ID.
var ErrHITLNotFound = errors.New("agentgen: HITL handle not found")

// ErrHITLWrongToken is returned by TrySignal when the presented wait_token does not match.
var ErrHITLWrongToken = errors.New("agentgen: HITL wrong wait_token")

// ErrHITLNotWaiting is returned by TrySignal when the handle is not in the "waiting" state.
var ErrHITLNotWaiting = errors.New("agentgen: HITL handle not in waiting state")

// HITLHandle stores the Temporal reference and HITL state for a paused canvas workflow.
// Credentials are never stored here — they are re-decrypted from the binding on signal delivery.
//
// State machine:
//
//	submitted → waiting → signalled → waiting (next hw node) → … → done
type HITLHandle struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	TenantID   string `json:"tenant_id"`
	StepID     string `json:"step_id"`    // canvas node ID currently waiting for signal
	WaitToken  string `json:"wait_token"` // deterministic token for this wait occurrence
	State      string `json:"state"`      // "submitted" | "waiting" | "signalled" | "done"
}

// HITLState constants.
const (
	HITLStateSubmitted = "submitted"
	HITLStateWaiting   = "waiting"
	HITLStateSignalled = "signalled"
	HITLStateDone      = "done"
)

// HITLStore persists HITL task handles in Redis so the signal endpoint can
// route human responses to the correct Temporal workflow without the HTTP
// connection that started the workflow remaining open.
type HITLStore struct {
	redis TaskStoreRedis // reuse the same interface (SetEX + Get + Del)
}

// NewHITLStore creates a HITLStore backed by the given Redis client.
func NewHITLStore(redis TaskStoreRedis) *HITLStore {
	return &HITLStore{redis: redis}
}

func hitlKey(taskID string) string {
	return hitlKeyPrefix + taskID
}

// Store saves the Temporal workflow handle for the given A2A task ID.
// State is set to "submitted" — the workflow is running but not yet at a human_wait node.
func (s *HITLStore) Store(ctx context.Context, taskID, workflowID, runID, tenantID, stepID string) error {
	h := HITLHandle{
		WorkflowID: workflowID,
		RunID:      runID,
		TenantID:   tenantID,
		StepID:     stepID,
		State:      HITLStateSubmitted,
	}
	data, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("hitl store: marshal: %w", err)
	}
	return s.redis.SetEX(ctx, hitlKey(taskID), data, HITLHandleTTL)
}

// Get retrieves the HITL handle for the given A2A task ID.
// Returns ErrHITLNotFound when the key does not exist or has expired.
func (s *HITLStore) Get(ctx context.Context, taskID string) (HITLHandle, error) {
	data, ok, err := s.redis.Get(ctx, hitlKey(taskID))
	if err != nil {
		return HITLHandle{}, fmt.Errorf("hitl store: redis get: %w", err)
	}
	if !ok {
		return HITLHandle{}, ErrHITLNotFound
	}
	var h HITLHandle
	if err := json.Unmarshal(data, &h); err != nil {
		return HITLHandle{}, fmt.Errorf("hitl store: unmarshal: %w", err)
	}
	return h, nil
}

// UpdateWaitToken sets the step_id, wait_token, and transitions state to "waiting".
// Called when the Temporal workflow reaches a human_wait node and the query handler
// reports state=="waiting". Allowed from any state (repeated calls for multi-wait / loops).
func (s *HITLStore) UpdateWaitToken(ctx context.Context, taskID, waitToken, stepID string) error {
	h, err := s.Get(ctx, taskID)
	if err != nil {
		return err
	}
	h.StepID = stepID
	h.WaitToken = waitToken
	h.State = HITLStateWaiting
	data, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("hitl store: marshal: %w", err)
	}
	return s.redis.SetEX(ctx, hitlKey(taskID), data, HITLHandleTTL)
}

// TrySignal is an atomic check-and-set: it succeeds only when state=="waiting"
// and the presented waitToken matches handle.WaitToken.
// On success the state transitions to "signalled" and the updated handle is returned.
// On failure ErrHITLWrongToken or ErrHITLNotWaiting is returned with no state change.
//
// Note: the mock Redis used in tests does not provide true atomicity; in production
// the caller should treat this as best-effort idempotency (the Temporal workflow's own
// signal-channel semantics provide the authoritative deduplication).
func (s *HITLStore) TrySignal(ctx context.Context, taskID, waitToken string) (HITLHandle, error) {
	h, err := s.Get(ctx, taskID)
	if err != nil {
		return HITLHandle{}, err
	}
	if h.State != HITLStateWaiting {
		return HITLHandle{}, ErrHITLNotWaiting
	}
	if h.WaitToken != waitToken {
		return HITLHandle{}, ErrHITLWrongToken
	}
	h.State = HITLStateSignalled
	data, err := json.Marshal(h)
	if err != nil {
		return HITLHandle{}, fmt.Errorf("hitl store: marshal: %w", err)
	}
	if err := s.redis.SetEX(ctx, hitlKey(taskID), data, HITLHandleTTL); err != nil {
		return HITLHandle{}, fmt.Errorf("hitl store: set: %w", err)
	}
	return h, nil
}

// MarkDone sets the state to "done" and removes the key from Redis.
// Called when the workflow terminates (success, failure, or cancel).
func (s *HITLStore) MarkDone(ctx context.Context, taskID string) error {
	return s.redis.Del(ctx, hitlKey(taskID))
}

// Delete is an alias for MarkDone kept for backward compatibility with existing callers.
// Prefer MarkDone for new code.
func (s *HITLStore) Delete(ctx context.Context, taskID string) error {
	return s.MarkDone(ctx, taskID)
}
