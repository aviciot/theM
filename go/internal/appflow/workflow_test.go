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

// AF-WF-15: Stream:true publishes exactly one token event to the run's stream key.
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
	if len(streamPub.keys) != 1 {
		t.Fatalf("want exactly 1 stream publish, got %d", len(streamPub.keys))
	}
	wantKey := "them:dash:run:run-9:stream"
	if streamPub.keys[0] != wantKey {
		t.Errorf("stream key: want %q, got %q", wantKey, streamPub.keys[0])
	}
	if len(streamPub.payloads) != 1 {
		t.Fatalf("want exactly 1 payload, got %d", len(streamPub.payloads))
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(streamPub.payloads[0]), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["type"] != "token" {
		t.Errorf("payload type: want %q, got %v", "token", payload["type"])
	}
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
