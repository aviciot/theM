package appflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
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
	keys     []string
	payloads []string
	retErr   error
}

func (f *fakeStreamPub) XAdd(_ context.Context, key string, fields map[string]interface{}) error {
	f.keys = append(f.keys, key)
	if data, ok := fields["data"].(string); ok {
		f.payloads = append(f.payloads, data)
	}
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

// ── App Canvas Debug Mode credential cleanup (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) ──
// Closes issue #4 from the d941aca3 review: a fixed TTL was the ONLY cleanup
// mechanism for debug credentials. FinalizeRunActivity now also triggers a
// proactive delete on run completion, gated on input.Debug.

type fakeDebugCredCleaner struct {
	calls  []cleanupCall
	retErr error
}

type cleanupCall struct {
	tenantID, runID string
}

func (f *fakeDebugCredCleaner) DeleteAllForRun(_ context.Context, tenantID, runID string) error {
	f.calls = append(f.calls, cleanupCall{tenantID, runID})
	return f.retErr
}

// TestFinalizeRunActivity_Debug_CleansUpCredentials verifies a debug run's
// terminal state triggers the proactive cleanup, with the correct
// tenant/run scoping.
func TestFinalizeRunActivity_Debug_CleansUpCredentials(t *testing.T) {
	cleaner := &fakeDebugCredCleaner{}
	acts := &AppFlowActivities{
		StatusUpdater:    &fakeStatusUpdater{},
		StreamPub:        &fakeStreamPub{},
		DebugCredCleaner: cleaner,
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID: "run-debug-1", TenantID: "tenant-1", Status: "completed", Debug: true,
	})
	if err != nil {
		t.Fatalf("FinalizeRunActivity: %v", err)
	}
	if len(cleaner.calls) != 1 {
		t.Fatalf("want 1 cleanup call, got %d", len(cleaner.calls))
	}
	if cleaner.calls[0].tenantID != "tenant-1" || cleaner.calls[0].runID != "run-debug-1" {
		t.Errorf("cleanup called with wrong scope: %+v", cleaner.calls[0])
	}
}

// TestFinalizeRunActivity_NotDebug_NeverCleansUp verifies a production
// (non-debug) run never triggers the cleanup call at all — there's nothing
// to clean up for it, and calling DeleteAllForRun unconditionally would be
// a wasted Redis SCAN on every production run.
func TestFinalizeRunActivity_NotDebug_NeverCleansUp(t *testing.T) {
	cleaner := &fakeDebugCredCleaner{}
	acts := &AppFlowActivities{
		StatusUpdater:    &fakeStatusUpdater{},
		StreamPub:        &fakeStreamPub{},
		DebugCredCleaner: cleaner,
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID: "run-prod-1", TenantID: "tenant-1", Status: "completed", Debug: false,
	})
	if err != nil {
		t.Fatalf("FinalizeRunActivity: %v", err)
	}
	if len(cleaner.calls) != 0 {
		t.Fatalf("want 0 cleanup calls for a non-debug run, got %d", len(cleaner.calls))
	}
}

// TestFinalizeRunActivity_Debug_NilCleaner_NoOp verifies a nil
// DebugCredCleaner (not configured) is safe — same nil-safety as every
// other dependency on AppFlowActivities.
func TestFinalizeRunActivity_Debug_NilCleaner_NoOp(t *testing.T) {
	acts := &AppFlowActivities{
		StatusUpdater: &fakeStatusUpdater{},
		StreamPub:     &fakeStreamPub{},
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID: "run-debug-2", TenantID: "tenant-1", Status: "completed", Debug: true,
	})
	if err != nil {
		t.Fatalf("FinalizeRunActivity: %v", err)
	}
}

// TestFinalizeRunActivity_Debug_CleanupErrorDoesNotFailActivity verifies a
// cleanup failure is swallowed, not returned — FinalizeRunActivity is
// idempotent and a retry would re-run the (already-succeeded) status update
// and stream publish above just to retry a Redis cleanup. An orphaned
// override still expires via its own TTL, so nothing leaks permanently.
func TestFinalizeRunActivity_Debug_CleanupErrorDoesNotFailActivity(t *testing.T) {
	cleaner := &fakeDebugCredCleaner{retErr: errors.New("redis: connection refused")}
	acts := &AppFlowActivities{
		StatusUpdater:    &fakeStatusUpdater{},
		StreamPub:        &fakeStreamPub{},
		DebugCredCleaner: cleaner,
	}

	err := acts.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{
		RunID: "run-debug-3", TenantID: "tenant-1", Status: "completed", Debug: true,
	})
	if err != nil {
		t.Fatalf("cleanup failure must not fail the activity, got: %v", err)
	}
	if len(cleaner.calls) != 1 {
		t.Fatalf("cleanup must still have been attempted, got %d calls", len(cleaner.calls))
	}
}

// AF-WF-03: FinalizeRunActivityInput JSON round-trip — status values survive serialization.
// ── AF-WF-04: InvokeAgentActivity ─────────────────────────────────────────────

type fakeAgentInvoker struct {
	response string
	result   AgentInvokeResult // takes priority over `response` when PartKind is set
	err      error
}

func (f *fakeAgentInvoker) InvokeByID(_ context.Context, _, _, _, _ string) (AgentInvokeResult, error) {
	if f.result.PartKind != "" {
		return f.result, f.err
	}
	return AgentInvokeResult{ResponseText: f.response}, f.err
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

// fakeFileGate implements FileGateChecker for tests.
type fakeFileGate struct {
	result    FileGateCheckOutput
	err       error
	lastInput FileGateCheckInput
	callCount int
}

func (f *fakeFileGate) Intercept(_ context.Context, in FileGateCheckInput) (FileGateCheckOutput, error) {
	f.callCount++
	f.lastInput = in
	return f.result, f.err
}

// AF-WF-18: FileGateActivity with a nil FileGate dependency is a safe no-op
// (docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md Phase 2) — a file is still
// recognized and traced by InvokeAgentActivity (Phase 1); this activity
// only ever ADDS scanning on top when a gate is actually configured.
func TestFileGateActivity_NilGate_NoOp(t *testing.T) {
	acts := &AppFlowActivities{FileGate: nil}
	out, err := acts.FileGateActivity(context.Background(), FileGateCheckInput{
		NodeID:  "agent-1",
		FileURL: "https://example.com/report.pdf",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ScanStatus != "disabled" {
		t.Errorf("ScanStatus = %q, want %q for a nil gate", out.ScanStatus, "disabled")
	}
}

// AF-WF-19: FileGateActivity delegates to the configured FileGate and
// passes NodeID through unchanged — the scoping key the Phase 2 fix in
// gate.go's loadWiringCfg actually uses to distinguish two canvas
// instances of the same agent.
func TestFileGateActivity_DelegatesToGate(t *testing.T) {
	gate := &fakeFileGate{result: FileGateCheckOutput{ArtifactID: "artifact-1", ScanStatus: "pending"}}
	acts := &AppFlowActivities{FileGate: gate}
	out, err := acts.FileGateActivity(context.Background(), FileGateCheckInput{
		RunID:    "run-1",
		NodeID:   "agent-1",
		FileURL:  "https://example.com/report.pdf",
		FileName: "report.pdf",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ScanStatus != "pending" || out.ArtifactID != "artifact-1" {
		t.Errorf("out = %+v, want the gate's real result passed through", out)
	}
	if gate.callCount != 1 {
		t.Fatalf("want 1 call to the gate, got %d", gate.callCount)
	}
	if gate.lastInput.NodeID != "agent-1" {
		t.Errorf("NodeID = %q, want it passed through unchanged", gate.lastInput.NodeID)
	}
}

// AF-WF-20: FileGateActivity surfaces a real gate error (wrapped, with
// node context) rather than swallowing it — a scan-infrastructure failure
// must not silently look like "scanning disabled".
func TestFileGateActivity_GateError_Propagates(t *testing.T) {
	gate := &fakeFileGate{err: errors.New("storage unavailable")}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{FileGate: gate, StreamPub: streamPub}
	_, err := acts.FileGateActivity(context.Background(), FileGateCheckInput{
		RunID:  "run-1",
		NodeID: "agent-1",
	})
	if err == nil {
		t.Fatal("expected an error to propagate from the gate")
	}
	if !strings.Contains(err.Error(), "storage unavailable") {
		t.Errorf("error = %v, want it to mention the underlying gate error", err)
	}
}

// AF-WF-07: findJoinNode locates the join from a fork's branches.
func TestFindJoinNode_Basic(t *testing.T) {
	nodeByID := map[string]*AppFlowNode{
		"agent_a": {ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
		"agent_b": {ID: "agent_b", Kind: "agent", AgentID: "uuid-b"},
		"join_1":  {ID: "join_1", Kind: "join"},
	}
	outEdges := map[string][]AppFlowEdge{
		"agent_a": {{Source: "agent_a", Target: "join_1"}},
		"agent_b": {{Source: "agent_b", Target: "join_1"}},
	}
	branches := []AppFlowEdge{
		{Source: "fork_1", Target: "agent_a"},
		{Source: "fork_1", Target: "agent_b"},
	}
	got := findJoinNode(branches, nodeByID, outEdges)
	if got != "join_1" {
		t.Errorf("findJoinNode: want join_1, got %q", got)
	}
}

// AF-WF-08: findJoinNode returns "" when no join node is reachable.
func TestFindJoinNode_NoJoin(t *testing.T) {
	nodeByID := map[string]*AppFlowNode{
		"agent_a": {ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
	}
	outEdges := map[string][]AppFlowEdge{}
	branches := []AppFlowEdge{{Source: "fork_1", Target: "agent_a"}}
	got := findJoinNode(branches, nodeByID, outEdges)
	if got != "" {
		t.Errorf("findJoinNode: want empty, got %q", got)
	}
}

// AF-WF-09: mergeBranchResults joins non-empty strings with newline.
func TestMergeBranchResults(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b"}, "a\nb"},
		{[]string{"a", "", "c"}, "a\nc"},
		{[]string{"", ""}, ""},
		{[]string{"only"}, "only"},
	}
	for _, tc := range cases {
		got := mergeBranchResults(tc.in)
		if got != tc.want {
			t.Errorf("mergeBranchResults(%v): want %q, got %q", tc.in, tc.want, got)
		}
	}
}

// ── AF-WF-10..16: InlineLLMActivity ───────────────────────────────────────────

type fakeInlineLLMCaller struct {
	response string
	err      error
	gotReq   InlineLLMRequest
	called   bool
}

func (f *fakeInlineLLMCaller) Complete(_ context.Context, req InlineLLMRequest) (string, error) {
	f.called = true
	f.gotReq = req
	return f.response, f.err
}

// AF-WF-10: InlineLLMActivity renders prompts, calls the caller, and returns
// text + OutputVar.
func TestInlineLLMActivity_Success(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "the answer is 42"}
	acts := &AppFlowActivities{InlineLLM: caller}

	out, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		RunID:         "run-1",
		TenantID:      "tenant-1",
		ApplicationID: "app-1",
		NodeID:        "llm-1",
		SystemPrompt:  "You are a {{.role}} assistant.",
		UserPrompt:    "Summarize: {{.input}}",
		Vars:          FlowVars{"role": "helpful", "input": "the quick brown fox"},
		Provider:      "anthropic",
		Model:         "claude",
		MaxTokens:     512,
		OutputVar:     "summary",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ResponseText != "the answer is 42" {
		t.Errorf("ResponseText: want %q, got %q", "the answer is 42", out.ResponseText)
	}
	if out.OutputVar != "summary" {
		t.Errorf("OutputVar: want %q, got %q", "summary", out.OutputVar)
	}
	if !caller.called {
		t.Fatal("expected InlineLLMCaller.Complete to be called")
	}
	if caller.gotReq.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("rendered SystemPrompt: got %q", caller.gotReq.SystemPrompt)
	}
	if caller.gotReq.UserPrompt != "Summarize: the quick brown fox" {
		t.Errorf("rendered UserPrompt: got %q", caller.gotReq.UserPrompt)
	}
	if caller.gotReq.ProviderName != "anthropic" || caller.gotReq.Model != "claude" {
		t.Errorf("provider/model not passed through: got %+v", caller.gotReq)
	}
	if caller.gotReq.MaxTokens != 512 {
		t.Errorf("MaxTokens: want 512, got %d", caller.gotReq.MaxTokens)
	}
}

// AF-WF-11: InlineLLMActivity returns a non-retryable NoInlineLLMCaller error
// when no InlineLLM dependency is configured.
func TestInlineLLMActivity_NilCaller(t *testing.T) {
	acts := &AppFlowActivities{}
	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		NodeID: "llm-1",
	})
	if err == nil {
		t.Fatal("expected error when InlineLLM is nil")
	}
	if !strings.Contains(err.Error(), "NoInlineLLMCaller") {
		t.Errorf("expected NoInlineLLMCaller error type, got: %v", err)
	}
}

// AF-WF-12: a bad template is a non-retryable render error, and the caller is
// never invoked.
func TestInlineLLMActivity_RenderError(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "should not be called"}
	acts := &AppFlowActivities{InlineLLM: caller}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		NodeID:       "llm-1",
		SystemPrompt: "{{.unbalanced",
		Vars:         FlowVars{},
	})
	if err == nil {
		t.Fatal("expected render error")
	}
	if !strings.Contains(err.Error(), "InlineLLMRenderFailed") {
		t.Errorf("expected InlineLLMRenderFailed error type, got: %v", err)
	}
	if caller.called {
		t.Error("InlineLLMCaller.Complete must not be called when rendering fails")
	}
}

// AF-WF-13: an empty rendered user prompt falls back to vars["input"].
func TestInlineLLMActivity_UserPromptFallsBackToInput(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "ok"}
	acts := &AppFlowActivities{InlineLLM: caller}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		NodeID:     "llm-1",
		UserPrompt: "",
		Vars:       FlowVars{"input": "fallback text"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.gotReq.UserPrompt != "fallback text" {
		t.Errorf("UserPrompt: want fallback %q, got %q", "fallback text", caller.gotReq.UserPrompt)
	}
}

// AF-WF-14: InlineLLMActivityInput must never carry a field that looks like a
// credential — activity inputs are persisted in Temporal workflow history.
// Matches on word boundaries (splitting Go's CamelCase field names) so
// "MaxTokens" does not false-positive on the "token" substring while
// "AuthToken"/"APIKey"/"ApiKey" style names would still be caught.
func TestInlineLLMActivity_NoKeyInInput(t *testing.T) {
	wordRe := regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+`)
	typ := reflect.TypeOf(InlineLLMActivityInput{})
	for i := 0; i < typ.NumField(); i++ {
		fieldName := typ.Field(i).Name
		for _, word := range wordRe.FindAllString(fieldName, -1) {
			w := strings.ToLower(word)
			for _, bad := range []string{"key", "token", "secret"} {
				if w == bad {
					t.Errorf("InlineLLMActivityInput.%s looks like a credential field (word %q)", fieldName, bad)
				}
			}
		}
	}
}

// AF-WF-15: Stream:true publishes exactly one "token" event to the run's
// stream key, alongside the node_start/node_done trace events every AppFlow
// activity now emits unconditionally (docs/APP_CANVAS_DEBUG_PLAN.md Phase 2).
func TestInlineLLMActivity_StreamPublishesToken(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "streamed response"}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{InlineLLM: caller, StreamPub: streamPub}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		RunID:  "run-9",
		NodeID: "llm-1",
		Vars:   FlowVars{"input": "hi"},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantKey := "them:dash:run:run-9:stream"
	for _, k := range streamPub.keys {
		if k != wantKey {
			t.Errorf("stream key: want %q, got %q", wantKey, k)
		}
	}

	var tokenPayloads []map[string]interface{}
	for _, raw := range streamPub.payloads {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("payload not valid JSON: %v", err)
		}
		if payload["type"] == "token" {
			tokenPayloads = append(tokenPayloads, payload)
		}
	}
	if len(tokenPayloads) != 1 {
		t.Fatalf("want exactly 1 token payload, got %d (all payloads: %v)", len(tokenPayloads), streamPub.payloads)
	}
	payload := tokenPayloads[0]
	if payload["content"] != "streamed response" {
		t.Errorf("payload content: want %q, got %v", "streamed response", payload["content"])
	}
	if payload["run_id"] != "run-9" {
		t.Errorf("payload run_id: want %q, got %v", "run-9", payload["run_id"])
	}
}

// AF-WF-16: MaxTokens 0 defaults to 1024 before reaching the caller.
func TestInlineLLMActivity_MaxTokensDefault(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "ok"}
	acts := &AppFlowActivities{InlineLLM: caller}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		NodeID:    "llm-1",
		Vars:      FlowVars{"input": "hi"},
		MaxTokens: 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.gotReq.MaxTokens != 1024 {
		t.Errorf("MaxTokens: want default 1024, got %d", caller.gotReq.MaxTokens)
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

// ── docs/APP_CANVAS_DEBUG_PLAN.md Phase 2: node_start/node_done/node_error trace events ──

// tracePayloadsOfType decodes every fakeStreamPub payload and returns only
// those whose "type" field matches wantType, in publish order.
func tracePayloadsOfType(t *testing.T, pub *fakeStreamPub, wantType string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, raw := range pub.payloads {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("payload not valid JSON: %v", err)
		}
		if payload["type"] == wantType {
			out = append(out, payload)
		}
	}
	return out
}

// AF-TR-01: InlineLLMActivity emits node_start then node_done on success,
// with kind="llm". node_done's detail carries the LLM's real response text
// (fixed 2026-09-24, docs/PLATFORM_AS_TENANT_PLAN.md Phase 6 — found live:
// the App Canvas Debug Mode inspector showed "No output captured for this
// node yet." for every LLM node because this activity hardcoded detail=""
// on node_done, discarding responseText even though it was already computed
// and used for token streaming a few lines later).
func TestInlineLLMActivity_EmitsStartAndDoneTrace(t *testing.T) {
	caller := &fakeInlineLLMCaller{response: "hi"}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{InlineLLM: caller, StreamPub: streamPub}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		RunID:  "run-tr-1",
		NodeID: "llm-1",
		Vars:   FlowVars{"input": "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	starts := tracePayloadsOfType(t, streamPub, "node_start")
	dones := tracePayloadsOfType(t, streamPub, "node_done")
	if len(starts) != 1 || len(dones) != 1 {
		t.Fatalf("want 1 node_start and 1 node_done, got %d and %d", len(starts), len(dones))
	}
	if starts[0]["kind"] != "llm" || starts[0]["node_id"] != "llm-1" || starts[0]["run_id"] != "run-tr-1" {
		t.Errorf("node_start: unexpected fields: %v", starts[0])
	}
	if dones[0]["kind"] != "llm" {
		t.Errorf("node_done kind: want %q, got %v", "llm", dones[0]["kind"])
	}
	if dones[0]["detail"] != "hi" {
		t.Errorf("node_done detail: want the LLM's real response %q, got %v", "hi", dones[0]["detail"])
	}
}

// AF-TR-02: InlineLLMActivity emits node_error (not node_done) when the LLM call fails.
func TestInlineLLMActivity_EmitsErrorTrace(t *testing.T) {
	caller := &fakeInlineLLMCaller{err: errors.New("boom")}
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{InlineLLM: caller, StreamPub: streamPub}

	_, err := acts.InlineLLMActivity(context.Background(), InlineLLMActivityInput{
		RunID:  "run-tr-2",
		NodeID: "llm-2",
		Vars:   FlowVars{"input": "hi"},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(tracePayloadsOfType(t, streamPub, "node_error")) != 1 {
		t.Fatalf("want 1 node_error, got %d", len(tracePayloadsOfType(t, streamPub, "node_error")))
	}
	if len(tracePayloadsOfType(t, streamPub, "node_done")) != 0 {
		t.Fatal("want 0 node_done on failure")
	}
}

// AF-TR-03: InvokeAgentActivity emits node_start/node_done with kind="agent".
// node_done's detail carries the agent's real response text (same fix and
// same reason as AF-TR-01 above — this activity had the identical
// discard-the-output-string bug).
func TestInvokeAgentActivity_EmitsStartAndDoneTrace(t *testing.T) {
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{
		AgentInvoker: &fakeAgentInvoker{response: "hi"},
		StreamPub:    streamPub,
	}
	_, err := acts.InvokeAgentActivity(context.Background(), AgentInvokeActivityInput{
		RunID:   "run-tr-3",
		NodeID:  "agent-1",
		AgentID: "agent-uuid-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	starts := tracePayloadsOfType(t, streamPub, "node_start")
	dones := tracePayloadsOfType(t, streamPub, "node_done")
	if len(starts) != 1 || len(dones) != 1 {
		t.Fatalf("want 1 node_start and 1 node_done, got %d and %d", len(starts), len(dones))
	}
	if starts[0]["kind"] != "agent" {
		t.Errorf("kind: want %q, got %v", "agent", starts[0]["kind"])
	}
	if dones[0]["detail"] != "hi" {
		t.Errorf("node_done detail: want the agent's real response %q, got %v", "hi", dones[0]["detail"])
	}
}

// AF-WF-07: InvokeAgentActivity carries a recognized file part through to
// AgentInvokeActivityOutput's new fields (docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md
// Phase 1) — previously ResponseText was the only field, and a non-text
// A2A response was silently indistinguishable from "agent said nothing".
func TestInvokeAgentActivity_CarriesFilePartThrough(t *testing.T) {
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{
		AgentInvoker: &fakeAgentInvoker{result: AgentInvokeResult{
			PartKind:        "file",
			FileURL:         "https://example.com/report.pdf",
			FileName:        "report.pdf",
			FileContentType: "application/pdf",
		}},
		StreamPub: streamPub,
	}
	out, err := acts.InvokeAgentActivity(context.Background(), AgentInvokeActivityInput{
		RunID:   "run-file-1",
		NodeID:  "agent-1",
		AgentID: "agent-uuid-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PartKind != "file" {
		t.Errorf("PartKind = %q, want %q", out.PartKind, "file")
	}
	if out.FileURL != "https://example.com/report.pdf" {
		t.Errorf("FileURL = %q, not carried through", out.FileURL)
	}
	if out.FileName != "report.pdf" || out.FileContentType != "application/pdf" {
		t.Errorf("FileName/FileContentType not carried through: %+v", out)
	}
	if out.ResponseText != "" {
		t.Errorf("ResponseText = %q, want empty for a file-only response", out.ResponseText)
	}
	// The trace's node_done detail should show a meaningful marker, not a
	// blank string (the same class of bug already fixed for LLM/agent text
	// responses earlier this session — see docs/CURRENT.md's bug #4).
	dones := tracePayloadsOfType(t, streamPub, "node_done")
	if len(dones) != 1 {
		t.Fatalf("want 1 node_done, got %d", len(dones))
	}
	if dones[0]["detail"] != "[file response, no text]" {
		t.Errorf("node_done detail = %v, want a non-blank file marker", dones[0]["detail"])
	}
}

// AF-TR-04: ExecuteHILActivity emits node_start unconditionally, and
// node_error (never node_done) when it fails before persisting — node_done
// for a successful HIL persist fires later from execHILNode (nodes.go) once
// the approval decision is known, not from this activity. No live DB is
// needed to exercise this: the nil-DB error path already covers the trace
// emission order without requiring *pgxpool.Pool (not mockable without one).
func TestExecuteHILActivity_EmitsStartThenErrorTrace_NilDB(t *testing.T) {
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{StreamPub: streamPub}

	_, err := acts.ExecuteHILActivity(context.Background(), HILActivityInput{
		RunID:  "run-tr-4",
		NodeID: "hil-1",
	})
	if err == nil {
		t.Fatal("want error with nil DB, got nil")
	}
	if len(tracePayloadsOfType(t, streamPub, "node_start")) != 1 {
		t.Fatalf("want 1 node_start, got %d", len(tracePayloadsOfType(t, streamPub, "node_start")))
	}
	if len(tracePayloadsOfType(t, streamPub, "node_error")) != 1 {
		t.Fatalf("want 1 node_error, got %d", len(tracePayloadsOfType(t, streamPub, "node_error")))
	}
	if len(tracePayloadsOfType(t, streamPub, "node_done")) != 0 {
		t.Fatal("want 0 node_done — HIL's done event fires from execHILNode, not this activity")
	}
}

type fakeRouterLLMCaller struct {
	label string
	err   error
}

func (f *fakeRouterLLMCaller) ClassifyIntent(_ context.Context, _, _ string, _ []string, _, _, _, _ string) (string, error) {
	return f.label, f.err
}

// AF-TR-06b: ExecuteRouterActivity emits node_start then node_done with
// detail="label=<chosen>" on success.
func TestExecuteRouterActivity_EmitsStartAndDoneTrace(t *testing.T) {
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{
		LLMCaller: &fakeRouterLLMCaller{label: "billing"},
		StreamPub: streamPub,
	}
	out, err := acts.ExecuteRouterActivity(context.Background(), RouterActivityInput{
		RunID:  "run-tr-7",
		NodeID: "router-1",
		Labels: []string{"billing", "support"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ChosenLabel != "billing" {
		t.Fatalf("chosen label: want %q, got %q", "billing", out.ChosenLabel)
	}
	dones := tracePayloadsOfType(t, streamPub, "node_done")
	if len(dones) != 1 {
		t.Fatalf("want 1 node_done, got %d", len(dones))
	}
	if dones[0]["detail"] != "label=billing" {
		t.Errorf("detail: want %q, got %v", "label=billing", dones[0]["detail"])
	}
}

// AF-TR-05: TraceNodeEventActivity publishes the given event type verbatim,
// including Detail when non-empty, and never returns an error.
func TestTraceNodeEventActivity_PublishesEvent(t *testing.T) {
	streamPub := &fakeStreamPub{}
	acts := &AppFlowActivities{StreamPub: streamPub}

	err := acts.TraceNodeEventActivity(context.Background(), TraceEventInput{
		RunID:     "run-tr-5",
		NodeID:    "cond-1",
		Kind:      "condition",
		EventType: "node_done",
		Detail:    "branch=true",
	})
	if err != nil {
		t.Fatalf("TraceNodeEventActivity must never return an error, got: %v", err)
	}
	dones := tracePayloadsOfType(t, streamPub, "node_done")
	if len(dones) != 1 {
		t.Fatalf("want 1 node_done, got %d", len(dones))
	}
	if dones[0]["detail"] != "branch=true" {
		t.Errorf("detail: want %q, got %v", "branch=true", dones[0]["detail"])
	}
	if dones[0]["node_id"] != "cond-1" || dones[0]["kind"] != "condition" {
		t.Errorf("unexpected fields: %v", dones[0])
	}
}

// AF-TR-06: TraceNodeEventActivity is a safe no-op when StreamPub is nil.
func TestTraceNodeEventActivity_NilStreamPub_NoOp(t *testing.T) {
	acts := &AppFlowActivities{}
	err := acts.TraceNodeEventActivity(context.Background(), TraceEventInput{
		RunID: "run-tr-6", NodeID: "n1", Kind: "condition", EventType: "node_start",
	})
	if err != nil {
		t.Fatalf("want nil error with no StreamPub, got %v", err)
	}
}

// docs/APP_CANVAS_DEBUG_PLAN.md Phase 1: activityTaskQueueFor selects the
// isolated debug queue when a run is in debug mode, and the production queue
// otherwise. AppFlowWorkflow uses this for every ActivityOptions.TaskQueue it
// builds, so a debug run's activities always land on them-dag-worker-debug.
func TestActivityTaskQueueFor(t *testing.T) {
	if got := activityTaskQueueFor(false); got != AppFlowTaskQueue {
		t.Errorf("debug=false: want %q, got %q", AppFlowTaskQueue, got)
	}
	if got := activityTaskQueueFor(true); got != AppFlowDebugTaskQueue {
		t.Errorf("debug=true: want %q, got %q", AppFlowDebugTaskQueue, got)
	}
	if AppFlowTaskQueue == AppFlowDebugTaskQueue {
		t.Fatal("AppFlowTaskQueue and AppFlowDebugTaskQueue must be distinct queue names")
	}
}
