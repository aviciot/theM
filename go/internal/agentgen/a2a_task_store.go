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

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// ctxKeyApplicationID holds the application ID for tenant isolation.
	ctxKeyApplicationID contextKey = "a2a_task_application_id"
)

// a2aTaskEntry is persisted JSON per task.
type a2aTaskEntry struct {
	Task          *a2a.Task             `json:"task"`
	Version       taskstore.TaskVersion `json:"version"`
	User          string                `json:"user"`
	ApplicationID string                `json:"application_id"` // Tenant isolation: tasks scoped to application
}

// RedisA2ATaskStore implements taskstore.Store for the SDK with tenant isolation.
// Tasks are keyed by application_id + task_id to prevent cross-tenant access.
// ApplicationID must be set in context via WithA2ATaskApplicationID before calling
// any store methods, otherwise operations return ErrA2ATaskNoAppID.
type RedisA2ATaskStore struct {
	redis   TaskStoreRedis
	counter atomic.Int64
}

// NewRedisA2ATaskStore creates a Redis-backed taskstore.Store.
func NewRedisA2ATaskStore(redis TaskStoreRedis) *RedisA2ATaskStore {
	return &RedisA2ATaskStore{redis: redis}
}

// WithA2ATaskApplicationID returns a context with the application ID set for task isolation.
// All task store operations require this context value; missing it returns ErrA2ATaskNoAppID.
func WithA2ATaskApplicationID(ctx context.Context, applicationID string) context.Context {
	return context.WithValue(ctx, ctxKeyApplicationID, applicationID)
}

// a2aTaskApplicationIDFromCtx extracts the application ID from context.
// Returns empty string if not set.
func a2aTaskApplicationIDFromCtx(ctx context.Context) string {
	if appID, ok := ctx.Value(ctxKeyApplicationID).(string); ok {
		return appID
	}
	return ""
}

func a2aTaskKey(applicationID string, taskID a2a.TaskID) string {
	return a2aTaskKeyPrefix + applicationID + ":" + string(taskID)
}

func (s *RedisA2ATaskStore) nextVersion() taskstore.TaskVersion {
	return taskstore.TaskVersion(s.counter.Add(1))
}

// Create stores a new task. Returns taskstore.ErrTaskAlreadyExists if already present.
// Returns ErrA2ATaskNoAppID if context does not contain an application ID.
func (s *RedisA2ATaskStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	appID := a2aTaskApplicationIDFromCtx(ctx)
	if appID == "" {
		return taskstore.TaskVersionMissing, ErrA2ATaskNoAppID
	}
	key := a2aTaskKey(appID, task.ID)
	// Check existence.
	_, exists, err := s.redis.Get(ctx, key)
	if err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a task store: redis get: %w", err)
	}
	if exists {
		return taskstore.TaskVersionMissing, taskstore.ErrTaskAlreadyExists
	}
	v := s.nextVersion()
	entry := a2aTaskEntry{Task: task, Version: v, ApplicationID: appID}
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
// Returns ErrA2ATaskNoAppID if context does not contain an application ID.
// Returns ErrA2ATaskForbidden if the context application ID does not match the stored task's application.
func (s *RedisA2ATaskStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	appID := a2aTaskApplicationIDFromCtx(ctx)
	if appID == "" {
		return taskstore.TaskVersionMissing, ErrA2ATaskNoAppID
	}
	key := a2aTaskKey(appID, req.Task.ID)
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
	// Ownership check: reject cross-application access.
	if entry.ApplicationID != appID {
		return taskstore.TaskVersionMissing, ErrA2ATaskForbidden
	}
	if req.PrevVersion != taskstore.TaskVersionMissing && entry.Version != req.PrevVersion {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	v := s.nextVersion()
	entry.Task = req.Task
	entry.Version = v
	// Preserve ApplicationID across updates.
	entry.ApplicationID = appID
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
// Returns ErrA2ATaskNoAppID if context does not contain an application ID.
// Returns ErrA2ATaskForbidden if the context application ID does not match the stored task's application.
func (s *RedisA2ATaskStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	appID := a2aTaskApplicationIDFromCtx(ctx)
	if appID == "" {
		return nil, ErrA2ATaskNoAppID
	}
	data, exists, err := s.redis.Get(ctx, a2aTaskKey(appID, taskID))
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
	// Ownership check: reject cross-application access.
	if entry.ApplicationID != appID {
		return nil, ErrA2ATaskForbidden
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

// ErrA2ATaskNoAppID is returned when the context does not contain an application ID.
var ErrA2ATaskNoAppID = errors.New("agentgen: A2A task operation requires application ID in context")

// ErrA2ATaskForbidden is returned when the context application ID does not match the task's application.
var ErrA2ATaskForbidden = errors.New("agentgen: A2A task access forbidden: application mismatch")

// ScopedA2ATaskStore wraps RedisA2ATaskStore and injects an application ID into all operations.
// This ensures tenant isolation when passing the store to the A2A SDK, which doesn't know about
// our multi-tenancy model.
type ScopedA2ATaskStore struct {
	store         *RedisA2ATaskStore
	applicationID string
}

// NewScopedA2ATaskStore creates a task store scoped to a single application.
// All operations automatically include the application ID for tenant isolation.
func NewScopedA2ATaskStore(store *RedisA2ATaskStore, applicationID string) *ScopedA2ATaskStore {
	return &ScopedA2ATaskStore{store: store, applicationID: applicationID}
}

// Create stores a new task with the scoped application ID.
func (s *ScopedA2ATaskStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	ctx = WithA2ATaskApplicationID(ctx, s.applicationID)
	return s.store.Create(ctx, task)
}

// Update applies an update to the stored task with ownership check.
func (s *ScopedA2ATaskStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	ctx = WithA2ATaskApplicationID(ctx, s.applicationID)
	return s.store.Update(ctx, req)
}

// Get retrieves a stored task with ownership check.
func (s *ScopedA2ATaskStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	ctx = WithA2ATaskApplicationID(ctx, s.applicationID)
	return s.store.Get(ctx, taskID)
}

// List delegates to the underlying store.
func (s *ScopedA2ATaskStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	ctx = WithA2ATaskApplicationID(ctx, s.applicationID)
	return s.store.List(ctx, req)
}

// Compile-time check that ScopedA2ATaskStore implements taskstore.Store.
var _ taskstore.Store = (*ScopedA2ATaskStore)(nil)
