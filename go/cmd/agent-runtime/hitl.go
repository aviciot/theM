package main

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

// HITLRequestHandler wraps an inner a2asrv.RequestHandler to intercept GetTask,
// SubscribeToTask, and CancelTask for HITL tasks. Non-HITL tasks are delegated to
// the inner handler unchanged.
//
// For HITL tasks it:
//   - GetTask: polls hitl_status, syncs HITLStore state, then delegates.
//   - SubscribeToTask: polls hitl_status every ~1s while the connection is alive;
//     emits InputRequired when the workflow reaches a human_wait node; awaits the
//     final result when signalled; emits Completed. No permanent background goroutines.
//   - CancelTask: cancels the Temporal workflow, marks the handle done, then delegates.
type HITLRequestHandler struct {
	inner     a2asrv.RequestHandler
	hitlStore *agentgen.HITLStore
	querier   temporal.CanvasHITLQuerier
	awaiter   temporal.CanvasAwaiter
	canceler  temporal.CanvasCanceler
	signaler  temporal.CanvasSignaler
	taskStore *agentgen.RedisA2ATaskStore
	logger    *slog.Logger
}

// syncHITLState queries the Temporal workflow and updates HITLStore when the
// workflow has advanced (e.g., reached waiting state). Returns the updated handle
// and ok=false if no HITL handle exists for this task.
func (h *HITLRequestHandler) syncHITLState(ctx context.Context, taskID string) (agentgen.HITLHandle, bool) {
	handle, err := h.hitlStore.Get(ctx, taskID)
	if errors.Is(err, agentgen.ErrHITLNotFound) {
		return agentgen.HITLHandle{}, false
	}
	if err != nil || h.querier == nil {
		return handle, true
	}

	status, err := h.querier.QueryHITLStatus(ctx, handle.WorkflowID, handle.RunID)
	if err != nil {
		// Workflow may be terminal — mark done.
		_ = h.hitlStore.MarkDone(ctx, taskID)
		return agentgen.HITLHandle{}, false
	}

	// Sync state transitions: submitted→waiting, signalled→waiting (next step).
	if status.State == agentgen.HITLStateWaiting && handle.WaitToken != status.WaitToken {
		if uerr := h.hitlStore.UpdateWaitToken(ctx, taskID, status.WaitToken, status.StepID); uerr == nil {
			handle.State = agentgen.HITLStateWaiting
			handle.WaitToken = status.WaitToken
			handle.StepID = status.StepID
		}
	}
	return handle, true
}

// GetTask syncs HITL state before delegating to the inner handler.
func (h *HITLRequestHandler) GetTask(ctx context.Context, req *a2a.GetTaskRequest) (*a2a.Task, error) {
	h.syncHITLState(ctx, string(req.ID)) //nolint:errcheck
	return h.inner.GetTask(ctx, req)
}

// SubscribeToTask polls the Temporal hitl_status query while the connection is alive.
// Emits InputRequired when the workflow reaches a human_wait node.
// Emits Completed+Artifact when the workflow is done.
// Delegates to the inner handler for non-HITL tasks.
func (h *HITLRequestHandler) SubscribeToTask(ctx context.Context, req *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	taskID := string(req.ID)
	handle, isHITL := h.syncHITLState(ctx, taskID)
	if !isHITL || h.querier == nil {
		return h.inner.SubscribeToTask(ctx, req)
	}

	return func(yield func(a2a.Event, error) bool) {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		// Emit current state immediately when already waiting.
		if handle.State == agentgen.HITLStateWaiting {
			prompt := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("waiting for human input"))
			ev := &a2a.TaskStatusUpdateEvent{
				TaskID:    req.ID,
				ContextID: "",
				Status:    a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: prompt},
			}
			if !yield(ev, nil) {
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, qErr := h.querier.QueryHITLStatus(ctx, handle.WorkflowID, handle.RunID)
				if qErr != nil {
					// Workflow terminated — check for final result.
					if h.awaiter != nil {
						result, aErr := h.awaiter.AwaitResult(ctx, handle.WorkflowID, handle.RunID)
						if aErr == nil && result != nil {
							artEv := a2a.NewArtifactEvent(&fakeTaskInfo{id: req.ID}, a2a.NewTextPart(result.Text))
							if !yield(artEv, nil) {
								return
							}
							doneEv := &a2a.TaskStatusUpdateEvent{
								TaskID: req.ID, ContextID: "",
								Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
							}
							yield(doneEv, nil) //nolint:errcheck
						} else {
							failEv := &a2a.TaskStatusUpdateEvent{
								TaskID: req.ID, ContextID: "",
								Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
							}
							yield(failEv, nil) //nolint:errcheck
						}
					}
					_ = h.hitlStore.MarkDone(ctx, taskID)
					return
				}

				// Sync state — new human_wait occurrence.
				if status.State == agentgen.HITLStateWaiting && status.WaitToken != handle.WaitToken {
					_ = h.hitlStore.UpdateWaitToken(ctx, taskID, status.WaitToken, status.StepID)
					handle.WaitToken = status.WaitToken
					handle.StepID = status.StepID
					handle.State = agentgen.HITLStateWaiting
					prompt := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("waiting for human input"))
					ev := &a2a.TaskStatusUpdateEvent{
						TaskID: req.ID, ContextID: "",
						Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: prompt},
					}
					if !yield(ev, nil) {
						return
					}
				}
			}
		}
	}
}

// fakeTaskInfo is a minimal a2a.TaskInfoProvider used when constructing artifact events
// outside of an ExecutorContext (e.g., in SubscribeToTask reconnect path).
type fakeTaskInfo struct {
	id a2a.TaskID
}

func (f *fakeTaskInfo) TaskInfo() a2a.TaskInfo {
	return a2a.TaskInfo{TaskID: f.id}
}

// CancelTask cancels the Temporal workflow if a HITL handle exists, then delegates.
func (h *HITLRequestHandler) CancelTask(ctx context.Context, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
	taskID := string(req.ID)
	if handle, err := h.hitlStore.Get(ctx, taskID); err == nil && h.canceler != nil {
		if cerr := h.canceler.CancelWorkflow(ctx, handle.WorkflowID, handle.RunID); cerr != nil {
			h.logger.Warn("HITLRequestHandler: CancelWorkflow failed",
				"task_id", taskID, "workflow_id", handle.WorkflowID, "err", cerr)
		}
		_ = h.hitlStore.MarkDone(ctx, taskID)
	}
	return h.inner.CancelTask(ctx, req)
}

// Delegate all other methods to the inner handler.

func (h *HITLRequestHandler) ListTasks(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return h.inner.ListTasks(ctx, req)
}
func (h *HITLRequestHandler) SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return h.inner.SendMessage(ctx, req)
}
func (h *HITLRequestHandler) SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return h.inner.SendStreamingMessage(ctx, req)
}
func (h *HITLRequestHandler) GetTaskPushConfig(ctx context.Context, req *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return h.inner.GetTaskPushConfig(ctx, req)
}
func (h *HITLRequestHandler) ListTaskPushConfigs(ctx context.Context, req *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return h.inner.ListTaskPushConfigs(ctx, req)
}
func (h *HITLRequestHandler) CreateTaskPushConfig(ctx context.Context, cfg *a2a.PushConfig) (*a2a.PushConfig, error) {
	return h.inner.CreateTaskPushConfig(ctx, cfg)
}
func (h *HITLRequestHandler) DeleteTaskPushConfig(ctx context.Context, req *a2a.DeleteTaskPushConfigRequest) error {
	return h.inner.DeleteTaskPushConfig(ctx, req)
}
func (h *HITLRequestHandler) GetExtendedAgentCard(ctx context.Context, req *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return h.inner.GetExtendedAgentCard(ctx, req)
}

// compile-time check that HITLRequestHandler satisfies RequestHandler.
var _ a2asrv.RequestHandler = (*HITLRequestHandler)(nil)
