package agentgen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunIDContext(t *testing.T) {
	if got := RunIDFromContext(context.Background()); got != "" {
		t.Errorf("empty context must carry no run id, got %q", got)
	}
	ctx := WithRunID(context.Background(), "run-1")
	if got := RunIDFromContext(ctx); got != "run-1" {
		t.Errorf("run id = %q, want run-1", got)
	}
	// An empty id must not hide one that is already set.
	if got := RunIDFromContext(WithRunID(ctx, "")); got != "run-1" {
		t.Errorf("WithRunID(ctx, \"\") must leave the context unchanged, got %q", got)
	}
}

// The run id rides in ActivityIC (Temporal history) from the invocation context, and is omitted when empty.
func TestActivityIC_CarriesRunID(t *testing.T) {
	ic := ActivityICFromInvocationContext(&InvocationContext{TenantID: "t", ApplicationID: "a", AgentID: "g", RunID: "run-9", A2ACallDepth: 1})
	if ic.RunID != "run-9" {
		t.Fatalf("ActivityIC.RunID = %q, want run-9", ic.RunID)
	}
	raw, _ := json.Marshal(ic)
	var back ActivityIC
	if err := json.Unmarshal(raw, &back); err != nil || back.RunID != "run-9" {
		t.Errorf("run id must survive the JSON round-trip: %v %+v", err, back)
	}
	noRun, _ := json.Marshal(ActivityICFromInvocationContext(&InvocationContext{TenantID: "t", ApplicationID: "a", AgentID: "g"}))
	var m map[string]any
	if err := json.Unmarshal(noRun, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["run_id"]; has {
		t.Errorf("run_id must be omitted when empty: %s", noRun)
	}
}

type ctxCapturingMCP struct{ runID string }

func (c *ctxCapturingMCP) Call(ctx context.Context, _, _, _ string, _ map[string]any) (json.RawMessage, error) {
	c.runID = RunIDFromContext(ctx)
	return json.RawMessage(`{"ok":true}`), nil
}

// execMCP hands the invocation's run id to the MCP caller through the context.
func TestExecMCP_PutsRunIDInCallContext(t *testing.T) {
	step := &StepSpec{ID: "m1", Config: json.RawMessage(`{"mcp_server_slug":"s","tool_name":"t","output_var":"r"}`)}
	for _, runID := range []string{"run-42", ""} {
		stub := &ctxCapturingMCP{}
		ic := &InvocationContext{TenantID: "t", ApplicationID: "a", AgentID: "g", RunID: runID}
		if err := execMCP(context.Background(), &Interpreter{mcpCaller: stub}, ic, step, PipelineVars{}); err != nil {
			t.Fatalf("execMCP: %v", err)
		}
		if stub.runID != runID {
			t.Errorf("caller saw run id %q, want %q", stub.runID, runID)
		}
	}
}

// The HTTP caller sends the run id in the request body (and omits it when there is none).
func TestHTTPMCPCaller_SendsRunID(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		_, _ = w.Write([]byte(`{"result":{"ok":true}}`))
	}))
	defer srv.Close()
	c := NewHTTPMCPCaller(srv.URL, srv.Client())

	if _, err := c.Call(WithRunID(context.Background(), "run-77"), "app-1", "srv", "tool", map[string]any{"q": 1}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if _, err := c.Call(context.Background(), "app-1", "srv", "tool", nil); err != nil {
		t.Fatalf("Call (no run): %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if bodies[0]["run_id"] != "run-77" || bodies[0]["application_id"] != "app-1" {
		t.Errorf("first request must carry the run id: %v", bodies[0])
	}
	if _, has := bodies[1]["run_id"]; has {
		t.Errorf("a call without a run must omit run_id: %v", bodies[1])
	}
}
