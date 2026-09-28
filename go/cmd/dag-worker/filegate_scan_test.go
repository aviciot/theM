package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aviciot/them/internal/appflow"
)

// Tests for scanRawBytesFilePart — docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md
// Phase 0. Extracted from pgxAgentA2ACaller.InvokeByID (same reasoning
// decodeAgentSendMessageResponse was extracted for) specifically so the
// raw-bytes inline scan + wait logic is unit-testable with fakes —
// InvokeByID itself still needs a live Postgres pool + real HTTP call.

// fakeFileGateChecker implements appflow.FileGateChecker for tests.
type fakeFileGateChecker struct {
	out       appflow.FileGateCheckOutput
	err       error
	lastInput appflow.FileGateCheckInput
	callCount int
}

func (f *fakeFileGateChecker) Intercept(_ context.Context, in appflow.FileGateCheckInput) (appflow.FileGateCheckOutput, error) {
	f.callCount++
	f.lastInput = in
	return f.out, f.err
}

func (f *fakeFileGateChecker) InterceptInline(_ context.Context, in appflow.FileGateCheckInput, _ []byte) (appflow.FileGateCheckOutput, error) {
	f.callCount++
	f.lastInput = in
	return f.out, f.err
}

// fakeFileGateWaiterMain implements appflow.FileGateWaiter for tests (named
// distinctly from internal/appflow's own fakeFileGateWaiter — different
// package, same shape).
type fakeFileGateWaiterMain struct {
	result       appflow.FileGateWaitResult
	ok           bool
	lastArtifact string
	callCount    int
}

func (f *fakeFileGateWaiterMain) WaitForScanResult(_ context.Context, _, artifactID string, _ time.Duration) (appflow.FileGateWaitResult, bool) {
	f.callCount++
	f.lastArtifact = artifactID
	return f.result, f.ok
}

// S1-FG-01: a nil fileGate skips the scan entirely (disabled), never calls
// the waiter.
func TestScanRawBytesFilePart_NilFileGate_Disabled(t *testing.T) {
	waiter := &fakeFileGateWaiterMain{ok: true}
	result, err := scanRawBytesFilePart(context.Background(), nil, waiter,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileGateScanStatus != "disabled" {
		t.Errorf("FileGateScanStatus = %q, want %q", result.FileGateScanStatus, "disabled")
	}
	if waiter.callCount != 0 {
		t.Error("waiter must never be called when fileGate is nil")
	}
}

// S1-FG-02: InterceptInline's own error propagates, wrapped with node
// context — never swallowed.
func TestScanRawBytesFilePart_InterceptError_Propagates(t *testing.T) {
	gate := &fakeFileGateChecker{err: errors.New("storage unavailable")}
	_, err := scanRawBytesFilePart(context.Background(), gate, nil,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"),
		appflow.FileGateCheckInput{NodeID: "agent_1"})
	if err == nil {
		t.Fatal("expected an error to propagate from InterceptInline")
	}
	if !strings.Contains(err.Error(), "storage unavailable") || !strings.Contains(err.Error(), "agent_1") {
		t.Errorf("error = %v, want it to mention both the underlying error and the node", err)
	}
}

// S1-FG-03: ScanStatus "disabled" (no wiring configured) never calls the
// waiter — nothing was enqueued to wait for.
func TestScanRawBytesFilePart_ScanDisabled_NeverCallsWaiter(t *testing.T) {
	gate := &fakeFileGateChecker{out: appflow.FileGateCheckOutput{ScanStatus: "disabled"}}
	waiter := &fakeFileGateWaiterMain{ok: true, result: appflow.FileGateWaitResult{ScanStatus: "clean"}}
	result, err := scanRawBytesFilePart(context.Background(), gate, waiter,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileGateScanStatus != "disabled" {
		t.Errorf("FileGateScanStatus = %q, want %q", result.FileGateScanStatus, "disabled")
	}
	if waiter.callCount != 0 {
		t.Error("waiter must never be called when the scan itself is disabled")
	}
}

// S1-FG-04: a real "pending" scan calls the waiter and surfaces the real
// terminal verdict ("clean") once it resolves.
func TestScanRawBytesFilePart_PendingScan_WaitsForRealVerdict(t *testing.T) {
	gate := &fakeFileGateChecker{out: appflow.FileGateCheckOutput{ArtifactID: "artifact-1", ScanStatus: "pending"}}
	waiter := &fakeFileGateWaiterMain{ok: true, result: appflow.FileGateWaitResult{ScanStatus: "clean"}}
	result, err := scanRawBytesFilePart(context.Background(), gate, waiter,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileGateScanStatus != "clean" {
		t.Errorf("FileGateScanStatus = %q, want the real verdict %q, not the enqueue-time %q", result.FileGateScanStatus, "clean", "pending")
	}
	if waiter.callCount != 1 || waiter.lastArtifact != "artifact-1" {
		t.Errorf("waiter should be called once with the real ArtifactID, got callCount=%d lastArtifact=%q", waiter.callCount, waiter.lastArtifact)
	}
}

// S1-FG-05: an infected verdict fails the call non-retryably — the whole
// point of Phase 0 is that this path, which previously had no wait at all,
// must now actually block an infected raw-bytes file.
func TestScanRawBytesFilePart_InfectedVerdict_ReturnsError(t *testing.T) {
	gate := &fakeFileGateChecker{out: appflow.FileGateCheckOutput{ArtifactID: "artifact-1", ScanStatus: "pending"}}
	waiter := &fakeFileGateWaiterMain{ok: true, result: appflow.FileGateWaitResult{ScanStatus: "infected", Threat: "EICAR"}}
	_, err := scanRawBytesFilePart(context.Background(), gate, waiter,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{NodeID: "agent_1"})
	if err == nil {
		t.Fatal("expected an error for an infected verdict")
	}
	if !strings.Contains(err.Error(), "EICAR") {
		t.Errorf("error = %v, want it to mention the threat name", err)
	}
}

// S1-FG-06: a timed-out wait (ok=false) fails open — ScanStatus becomes
// "timeout", not an error — matching the classic Orchestrator's own
// documented fail-open-on-timeout precedent.
func TestScanRawBytesFilePart_WaitTimesOut_FailsOpen(t *testing.T) {
	gate := &fakeFileGateChecker{out: appflow.FileGateCheckOutput{ArtifactID: "artifact-1", ScanStatus: "pending"}}
	waiter := &fakeFileGateWaiterMain{ok: false}
	result, err := scanRawBytesFilePart(context.Background(), gate, waiter,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{})
	if err != nil {
		t.Fatalf("a timeout must not be an error, got: %v", err)
	}
	if result.FileGateScanStatus != "timeout" {
		t.Errorf("FileGateScanStatus = %q, want %q", result.FileGateScanStatus, "timeout")
	}
}

// S1-FG-07: a nil waiter with a pending scan leaves ScanStatus as
// "pending" — the trace should show the real enqueue-time state, not a
// fabricated verdict, when there's genuinely nothing to wait with.
func TestScanRawBytesFilePart_PendingScan_NilWaiter_LeavesStatusPending(t *testing.T) {
	gate := &fakeFileGateChecker{out: appflow.FileGateCheckOutput{ArtifactID: "artifact-1", ScanStatus: "pending"}}
	result, err := scanRawBytesFilePart(context.Background(), gate, nil,
		appflow.AgentInvokeResult{PartKind: "raw"}, []byte("bytes"), appflow.FileGateCheckInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileGateScanStatus != "pending" {
		t.Errorf("FileGateScanStatus = %q, want %q (unchanged, nothing to wait with)", result.FileGateScanStatus, "pending")
	}
}
