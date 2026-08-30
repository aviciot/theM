package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

const (
	a2aTaskKeyPrefix = "them:agent:a2atask:"
	a2aTaskTTL       = 24 * time.Hour
)

// a2aTaskEntry is persisted JSON per task.
type a2aTaskEntry struct {
	Task    *a2a.Task              `json:"task"`
	Version taskstore.TaskVersion  `json:"version"`
	User    string                 `json:"user"`
}

// RedisA2ATaskStore implements taskstore.Store for the SDK using the same
// Redis interface as RedisTaskStore. Tasks are stored by their A2A task ID.
// Ownership/tenant filtering is not enforced here — the SDK and our middleware
// handle that boundary. Cross-tenant safety is enforced in parseInvocationContext.
type RedisA2ATaskStore struct {
	redis   TaskStoreRedis
	counter atomic.Int64
}

// NewRedisA2ATaskStore creates a Redis-backed taskstore.Store.
func NewRedisA2ATaskStore(redis TaskStoreRedis) *RedisA2ATaskStore {
	return &RedisA2ATaskStore{redis: redis}
}

func a2aTaskKey(taskID a2a.TaskID) string {
	return a2aTaskKeyPrefix + string(taskID)
}

func (s *RedisA2ATaskStore) nextVersion() taskstore.TaskVersion {
	return taskstore.TaskVersion(s.counter.Add(1))
}

// Create stores a new task. Returns taskstore.ErrTaskAlreadyExists if already present.
func (s *RedisA2ATaskStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	key := a2aTaskKey(task.ID)
	// Check existence.
	_, exists, err := s.redis.Get(ctx, key)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: redis get: %w", err)
	}
	if exists {
		return taskstore.TaskVersionMissing, taskstore.ErrTaskAlreadyExists
	}
	v := s.nextVersion()
	entry := a2aTaskEntry{Task: task, Version: v}
	data, err := json.Marshal(entry)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: marshal: %w", err)
	}
	if err := s.redis.SetEX(ctx, key, data, a2aTaskTTL); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: redis set: %w", err)
	}
	return v, nil
}

// Update applies an update to the stored task.
// Returns taskstore.ErrConcurrentModification if PrevVersion does not match.
// Returns a2a.ErrTaskNotFound if the task does not exist.
func (s *RedisA2ATaskStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	key := a2aTaskKey(req.Task.ID)
	data, exists, err := s.redis.Get(ctx, key)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: redis get: %w", err)
	}
	if !exists {
		return taskstore.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	var entry a2aTaskEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: unmarshal: %w", err)
	}
	if req.PrevVersion != taskstore.TaskVersionMissing && entry.Version != req.PrevVersion {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	v := s.nextVersion()
	entry.Task = req.Task
	entry.Version = v
	newData, err := json.Marshal(entry)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: marshal: %w", err)
	}
	if err := s.redis.SetEX(ctx, key, newData, a2aTaskTTL); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: redis set: %w", err)
	}
	return v, nil
}

// Get retrieves a stored task. Returns a2a.ErrTaskNotFound if missing.
func (s *RedisA2ATaskStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	data, exists, err := s.redis.Get(ctx, a2aTaskKey(taskID))
	if err != nil {
		return nil, fmt.Errorf("a2a task store: redis get: %w", err)
	}
	if !exists {
		return nil, a2a.ErrTaskNotFound
	}
	var entry a2aTaskEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("a2a task store: unmarshal: %w", err)
	}
	return &taskstore.StoredTask{
		Task:    entry.Task,
		Version: entry.Version,
		User:    entry.User,
	}, nil
}

// List retrieves tasks matching the request. Currently returns an empty list
// (listing is not required for HITL correctness; callers use GetTask by ID).
func (s *RedisA2ATaskStore) List(_ context.Context, _ *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return &a2a.ListTasksResponse{Tasks: []*a2a.Task{}}, nil
}

// compile-time check.
var _ taskstore.Store = (*RedisA2ATaskStore)(nil)

// ErrA2ATaskNotFound is used to detect missing tasks; we also use a2a.ErrTaskNotFound.
var ErrA2ATaskNotFound = errors.New("agentgen: A2A task not found")
