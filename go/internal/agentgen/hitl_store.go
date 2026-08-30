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

// HITLHandle stores the Temporal reference needed to signal a paused canvas workflow.
// Credentials are never stored here — they are re-decrypted from the binding on signal delivery.
type HITLHandle struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id"` // canvas node ID waiting for the signal
}

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
func (s *HITLStore) Store(ctx context.Context, taskID, workflowID, runID, stepID string) error {
	h := HITLHandle{WorkflowID: workflowID, RunID: runID, StepID: stepID}
	data, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("hitl store: marshal: %w", err)
	}
	return s.redis.SetEX(ctx, hitlKey(taskID), data, HITLHandleTTL)
}

// Get retrieves the Temporal workflow handle for the given A2A task ID.
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

// Delete removes the handle after the workflow completes or is cancelled.
func (s *HITLStore) Delete(ctx context.Context, taskID string) error {
	return s.redis.Del(ctx, hitlKey(taskID))
}
