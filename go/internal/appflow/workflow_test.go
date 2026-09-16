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
