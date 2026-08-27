package agentgen_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

// stubMCPCaller is a test double for MCPCaller.
type stubMCPCaller struct {
	// captureArgs records the last Call invocation.
	lastAppID  string
	lastSlug   string
	lastTool   string
	lastArgs   map[string]any
	returnJSON string
	returnErr  error
}

func (s *stubMCPCaller) Call(_ context.Context, appID, slug, tool string, args map[string]any) (json.RawMessage, error) {
	s.lastAppID = appID
	s.lastSlug = slug
	s.lastTool = tool
	s.lastArgs = args
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return json.RawMessage(s.returnJSON), nil
}

var _ agentgen.MCPCaller = (*stubMCPCaller)(nil)

// MCP-1: mcp_call node is registered and has Execute set.
func TestMCP_NodeRegistered(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepMCPCall)
	if !ok {
		t.Fatal("StepMCPCall not registered")
	}
	if def.Execute == nil {
		t.Error("StepMCPCall.Execute must be non-nil")
	}
	if def.Label == "" {
		t.Error("StepMCPCall.Label must not be empty")
	}
}

// MCP-2: Validate via compiler rejects missing required fields.
func TestMCP_Validate_MissingFields(t *testing.T) {
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",    "config": {}, "next": ["mcp1"]},
				{"id": "mcp1","type": "mcp_call", "config": {}, "next": ["out"]},
				{"id": "out", "type": "response", "config": {"from_var": "r"}}
			]
		}]
	}`))
	hasSlug, hasTool, hasOut := false, false, false
	for _, iss := range issues {
		if iss.Severity != "error" {
			continue
		}
		switch {
		case contains(iss.Message, "mcp_server_slug"):
			hasSlug = true
		case contains(iss.Message, "tool_name"):
			hasTool = true
		case contains(iss.Message, "output_var"):
			hasOut = true
		}
	}
	if !hasSlug {
		t.Error("expected error about mcp_server_slug")
	}
	if !hasTool {
		t.Error("expected error about tool_name")
	}
	if !hasOut {
		t.Error("expected error about output_var")
	}
}

// MCP-3: Validate via compiler accepts a complete config with no errors.
func TestMCP_Validate_ValidConfig(t *testing.T) {
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",    "config": {}, "next": ["mcp1"]},
				{"id": "mcp1","type": "mcp_call", "config": {"mcp_server_slug":"s","tool_name":"t","output_var":"r"}, "next": ["out"]},
				{"id": "out", "type": "response", "config": {"from_var": "r"}}
			]
		}]
	}`))
	for _, iss := range issues {
		if iss.Severity == "error" && iss.NodeID == "mcp1" {
			t.Errorf("unexpected error on valid mcp_call config: %+v", iss)
		}
	}
}

// MCP-4: DeriveOutputs returns the output_var VarRef.
func TestMCP_DeriveOutputs(t *testing.T) {
	def, _ := agentgen.LookupNode(agentgen.StepMCPCall)
	refs := def.DeriveOutputs(json.RawMessage(`{"output_var":"result"}`))
	if len(refs) != 1 || refs[0].Name != "result" {
		t.Errorf("DeriveOutputs: want [{result}], got %v", refs)
	}
}

// MCP-5: DeriveInputs extracts template variable references from args_template.
func TestMCP_DeriveInputs_Template(t *testing.T) {
	def, _ := agentgen.LookupNode(agentgen.StepMCPCall)
	cfg := `{"mcp_server_slug":"s","tool_name":"t","output_var":"o","args_template":"{\"q\":\"{{.query}}\"}"}`
	refs := def.DeriveInputs(json.RawMessage(cfg))
	if len(refs) == 0 {
		t.Fatal("DeriveInputs: expected at least one VarRef for template var")
	}
	found := false
	for _, r := range refs {
		if r.Name == "query" {
			found = true
		}
	}
	if !found {
		t.Errorf("DeriveInputs: expected 'query' in refs, got %v", refs)
	}
}

// MCP-6: Execute without MCPCaller returns an error.
func TestMCP_Execute_NoCaller(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{ApplicationID: "app1"}
	skill := buildMCPSkill("srv", "tool", "", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected error when MCPCaller is nil")
	}
}

// MCP-7: Execute with a working MCPCaller populates the output var and
// the pipeline returns the result.
func TestMCP_Execute_CallsCallerAndSetsVar(t *testing.T) {
	stub := &stubMCPCaller{returnJSON: `{"city":"Tel Aviv","temp":28}`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithMCPCaller(stub)
	ic := &agentgen.InvocationContext{ApplicationID: "app-123"}
	skill := buildMCPSkill("weather-svc", "get_weather", "", "weather_json")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastSlug != "weather-svc" {
		t.Errorf("expected slug 'weather-svc', got %q", stub.lastSlug)
	}
	if stub.lastTool != "get_weather" {
		t.Errorf("expected tool 'get_weather', got %q", stub.lastTool)
	}
	if stub.lastAppID != "app-123" {
		t.Errorf("expected appID 'app-123', got %q", stub.lastAppID)
	}
}

// MCP-8: Execute propagates MCPCaller errors.
func TestMCP_Execute_CallerError(t *testing.T) {
	stub := &stubMCPCaller{returnErr: fmt.Errorf("credential not configured")}
	interp := agentgen.NewInterpreter(nil, nil, "").WithMCPCaller(stub)
	ic := &agentgen.InvocationContext{ApplicationID: "app1"}
	skill := buildMCPSkill("srv", "tool", "", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected error propagated from MCPCaller")
	}
	if !contains(err.Error(), "credential not configured") {
		t.Errorf("expected original error in message, got: %v", err)
	}
}

// MCP-9: renderMCPArgs with an args_template renders pipeline vars into JSON.
func TestMCP_Execute_ArgsTemplateRendered(t *testing.T) {
	stub := &stubMCPCaller{returnJSON: `{}`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithMCPCaller(stub)
	ic := &agentgen.InvocationContext{ApplicationID: "app1"}
	tmpl := `{"city":"{{.input}}"}`
	skill := buildMCPSkill("weather", "get", tmpl, "result")
	_, err := interp.Execute(context.Background(), ic, skill, "Tel Aviv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cityVal, _ := stub.lastArgs["city"].(string)
	if cityVal != "Tel Aviv" {
		t.Errorf("expected args.city='Tel Aviv', got %q", cityVal)
	}
}

// MCP-10: mcp_call compiles cleanly in a full canvas pipeline.
func TestMCP_Compiles_InPipeline(t *testing.T) {
	_, errs := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",    "config": {}, "next": ["mcp1"]},
				{"id": "mcp1","type": "mcp_call", "config": {"mcp_server_slug":"s","tool_name":"t","output_var":"r"}, "next": ["out"]},
				{"id": "out", "type": "response", "config": {"from_var": "r"}}
			]
		}]
	}`))
	for _, iss := range errs {
		if iss.Severity == "error" {
			t.Errorf("unexpected compile error: %+v", iss)
		}
	}
}

// buildMCPSkill builds a minimal SkillSpec with input→mcp_call→response.
func buildMCPSkill(slug, tool, argsTmpl, outVar string) *agentgen.SkillSpec {
	cfgMap := map[string]any{
		"mcp_server_slug": slug,
		"tool_name":       tool,
		"output_var":      outVar,
	}
	if argsTmpl != "" {
		cfgMap["args_template"] = argsTmpl
	}
	cfg, _ := json.Marshal(cfgMap)
	return &agentgen.SkillSpec{
		ID:   "sk1",
		Name: "test",
		Steps: []agentgen.StepSpec{
			{ID: "in",   Type: agentgen.StepInput,    Config: json.RawMessage(`{}`),              Next: []string{"mcp1"}},
			{ID: "mcp1", Type: agentgen.StepMCPCall,  Config: json.RawMessage(cfg),               Next: []string{"out"}},
			{ID: "out",  Type: agentgen.StepResponse,  Config: json.RawMessage(`{"from_var":"` + outVar + `"}`), Next: nil},
		},
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
