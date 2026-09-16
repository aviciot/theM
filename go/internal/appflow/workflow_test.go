package appflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/domain"
)

// ── test doubles ─────────────────────────────────────────────────────────────

type fakeStatusUpdater struct {
	calls  []statusCall
	retErr error
}

type statusCall struct {
	runID  string
	status domain.RunStatus
	errMsg string
}

func (f *fakeStatusUpdater) UpdateRunStatus(_ context.Context, runID string, status domain.RunStatus, errMsg string) error {
	f.calls = append(f.calls, statusCall{runID, status, errMsg})
	return f.retErr
}

type fakeStreamPub struct {
	keys   []string
	retErr error
}

func (f *fakeStreamPub) XAdd(_ context.Context, key string, _ map[string]interface{}) error {
	f.keys = append(f.keys, key)
	return f.retErr
}

// ── AF-WF-01: FinalizeRunActivity updates status and publishes stream event ──

// TestFinalizeRunActivity_SuccessPath verifies that FinalizeRunActivity
// updates run status to completed and publishes a "done" event.
func TestFinalizeRunActivity_SuccessPath(t *testing.T) {
	statusUpdater := &fakeStatusUpdater{}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{
		StatusUpdater: statusUpdater,
		StreamPub:     streamPub,
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID:     "run-1",
		TenantID:  "tenant-1",
		Status:    "completed",
		FinalText: "all done",
	})
	if err != nil {
		t.Fatalf("FinalizeRunActivity: %v", err)
	}

	if len(statusUpdater.calls) != 1 {
		t.Fatalf("want 1 status update, got %d", len(statusUpdater.calls))
	}
	if statusUpdater.calls[0].status != domain.RunCompleted {
		t.Errorf("want RunCompleted, got %v", statusUpdater.calls[0].status)
	}
	if len(streamPub.keys) != 1 {
		t.Fatalf("want 1 stream publish, got %d", len(streamPub.keys))
	}
}

// AF-WF-01b: FinalizeRunActivity on error/rejected path publishes "error" event.
func TestFinalizeRunActivity_FailedPath(t *testing.T) {
	statusUpdater := &fakeStatusUpdater{}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{
		StatusUpdater: statusUpdater,
		StreamPub:     streamPub,
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID:    "run-2",
		TenantID: "tenant-1",
		Status:   "failed",
		ErrMsg:   "node exploded",
	})
	if err != nil {
		t.Fatalf("FinalizeRunActivity: %v", err)
	}

	if statusUpdater.calls[0].status != domain.RunFailed {
		t.Errorf("want RunFailed, got %v", statusUpdater.calls[0].status)
	}
	if len(streamPub.keys) != 1 {
		t.Fatalf("want 1 stream publish, got %d", len(streamPub.keys))
	}
}

// AF-WF-02: FinalizeRunActivity returns XAdd error so Temporal retries work.
func TestFinalizeRunActivity_XAddError_IsReturned(t *testing.T) {
	acts := &AppFlowActivities{
		StatusUpdater: &fakeStatusUpdater{},
		StreamPub:     &fakeStreamPub{retErr: errors.New("redis: connection refused")},
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID:  "run-3",
		Status: "completed",
	})
	if err == nil {
		t.Fatal("want error from XAdd, got nil")
	}
}

// AF-WF-02b: FinalizeRunActivity with nil StatusUpdater and StreamPub is a no-op (safe).
func TestFinalizeRunActivity_NilDeps_NoOp(t *testing.T) {
	acts := &AppFlowActivities{} // no StatusUpdater, no StreamPub
	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID:  "run-4",
		Status: "completed",
	})
	if err != nil {
		t.Fatalf("want nil with no deps, got %v", err)
	}
}

// AF-WF-03: FinalizeRunActivityInput JSON round-trip — status values survive serialization.
// ── AF-WF-04: InvokeAgentActivity ─────────────────────────────────────────────

type fakeAgentInvoker struct {
	response string
	err      error
}

func (f *fakeAgentInvoker) InvokeByID(_ context.Context, _, _, _, _ string) (string, error) {
	return f.response, f.err
}

// AF-WF-04: InvokeAgentActivity returns agent response text on success.
func TestInvokeAgentActivity_Success(t *testing.T) {
	acts := &AppFlowActivities{
		AgentInvoker: &fakeAgentInvoker{response: "Hello from agent!"},
	}
	out, err := acts.InvokeAgentActivity(context.Background(), AgentInvokeActivityInput{
		RunID:         "run-1",
		TenantID:      "tenant-1",
		ApplicationID: "app-1",
		NodeID:        "node-agent-1",
		AgentID:       "agent-uuid-1",
		UserMessage:   "Hi!",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ResponseText != "Hello from agent!" {
		t.Errorf("response: want %q, got %q", "Hello from agent!", out.ResponseText)
	}
}

// AF-WF-05: InvokeAgentActivity returns non-retryable error when agent_id is empty.
func TestInvokeAgentActivity_EmptyAgentID(t *testing.T) {
	acts := &AppFlowActivities{
		AgentInvoker: &fakeAgentInvoker{response: "should not be called"},
	}
	_, err := acts.InvokeAgentActivity(context.Background(), AgentInvokeActivityInput{
		RunID:         "run-2",
		TenantID:      "tenant-2",
		ApplicationID: "app-2",
		NodeID:        "node-agent-2",
		AgentID:       "", // empty — stamp missing
		UserMessage:   "Hi!",
	})
	if err == nil {
		t.Fatal("expected error for empty agent_id")
	}
}

// AF-WF-06: InvokeAgentActivity returns non-retryable error when AgentInvoker is nil.
func TestInvokeAgentActivity_NilInvoker(t *testing.T) {
	acts := &AppFlowActivities{AgentInvoker: nil}
	_, err := acts.InvokeAgentActivity(context.Background(), AgentInvokeActivityInput{
		AgentID: "some-agent-uuid",
	})
	if err == nil {
		t.Fatal("expected error when AgentInvoker is nil")
	}
}

func TestFinalizeRunActivityInput_JSONRoundTrip(t *testing.T) {
	in := FinalizeRunActivityInput{
		RunID:     "run-5",
		TenantID:  "tenant-5",
		Status:    "rejected",
		FinalText: "HIL rejected",
		ErrMsg:    "operator said no",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out FinalizeRunActivityInput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != in.Status {
		t.Errorf("status: want %q, got %q", in.Status, out.Status)
	}
	if out.ErrMsg != in.ErrMsg {
		t.Errorf("err_msg: want %q, got %q", in.ErrMsg, out.ErrMsg)
	}
}
