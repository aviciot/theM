package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	taskKeyPrefix = "them:agent:task:"
	taskTTL       = 24 * time.Hour
)

// ErrTaskNotFound is returned when a task does not exist or ownership check fails.
// 404 is the correct response — do not disclose task existence to other tenants.
var ErrTaskNotFound = errors.New("agentgen: task not found")

// TaskState holds the durable state of an agent task in Redis.
// Credentials are NEVER stored here — on resume, they are re-decrypted from the binding.
type TaskState struct {
	TaskID        string          `json:"task_id"`
	TenantID      string          `json:"tenant_id"`
	ApplicationID string          `json:"application_id"`
	AgentID       string          `json:"agent_id"`
	BindingID     string          `json:"binding_id"`
	Status        string          `json:"status"` // submitted|working|completed|failed|canceled|input-required
	Artifacts     []string        `json:"artifacts"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	// PausedState holds interpreter state for human-wait resume.
	PausedState json.RawMessage `json:"paused_state,omitempty"`
}

// TaskStoreRedis is the Redis interface required by RedisTaskStore.
type TaskStoreRedis interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// RedisTaskStore stores task state in Redis.
// Ownership is enforced on every read — mismatch returns ErrTaskNotFound (not forbidden).
type RedisTaskStore struct {
	redis TaskStoreRedis
}

// NewRedisTaskStore creates a RedisTaskStore backed by the given Redis client.
func NewRedisTaskStore(redis TaskStoreRedis) *RedisTaskStore {
	return &RedisTaskStore{redis: redis}
}

func taskKey(taskID string) string {
	return taskKeyPrefix + taskID
}

// Get retrieves a task, verifying ownership. Returns ErrTaskNotFound on mismatch.
func (s *RedisTaskStore) Get(ctx context.Context, taskID, tenantID, applicationID string) (*TaskState, error) {
	data, ok, err := s.redis.Get(ctx, taskKey(taskID))
	if err != nil {
		return nil, fmt.Errorf("redis get task: %w", err)
	}
	if !ok {
		return nil, ErrTaskNotFound
	}
	var ts TaskState
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, fmt.Errorf("unmarshal task state: %w", err)
	}
	// Invariant 3: verify ownership before returning anything.
	// ErrTaskNotFound — do not disclose task existence to other tenants/apps.
	if ts.TenantID != tenantID || ts.ApplicationID != applicationID {
		return nil, ErrTaskNotFound
	}
	return &ts, nil
}

// Set writes a task state to Redis and refreshes TTL.
func (s *RedisTaskStore) Set(ctx context.Context, ts *TaskState) error {
	ts.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(ts)
	if err != nil {
		return fmt.Errorf("marshal task state: %w", err)
	}
	return s.redis.SetEX(ctx, taskKey(ts.TaskID), data, taskTTL)
}

// Delete removes a task from Redis.
func (s *RedisTaskStore) Delete(ctx context.Context, taskID string) error {
	return s.redis.Del(ctx, taskKey(taskID))
}

// Create stores a new task state.
func (s *RedisTaskStore) Create(ctx context.Context, ts *TaskState) error {
	ts.CreatedAt = time.Now().UTC()
	ts.UpdatedAt = ts.CreatedAt
	data, err := json.Marshal(ts)
	if err != nil {
		return fmt.Errorf("marshal task state: %w", err)
	}
	return s.redis.SetEX(ctx, taskKey(ts.TaskID), data, taskTTL)
}
