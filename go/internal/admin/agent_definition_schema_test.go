package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/agentgen"
)

// ── mock LLM caller ───────────────────────────────────────────────────────────

type mockLLMCaller struct {
	response string
	err      error
}

func (m *mockLLMCaller) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

// ── Schema tests ──────────────────────────────────────────────────────────────

func TestSchema_ReturnsWireFormatAndIssueCodes(t *testing.T) {
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/agent-definitions/schema", nil)

	h.Schema(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		WireFormat string                  `json:"wire_format"`
		IssueCodes []string                `json:"issue_codes"`
		NodeTypes  []agentgen.NodeTypeInfo `json:"node_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.WireFormat == "" {
		t.Error("wire_format must not be empty")
	}
	if !strings.Contains(resp.WireFormat, "agent_root") {
		t.Error("wire_format must contain 'agent_root'")
	}
	if !strings.Contains(resp.WireFormat, "skills") {
		t.Error("wire_format must contain 'skills'")
	}

	if len(resp.IssueCodes) == 0 {
		t.Error("issue_codes must not be empty")
	}
	for _, code := range []string{
		"DUPLICATE_SKILL", "CYCLE_DETECTED", "MISSING_INPUT_EDGE", "UNKNOWN_STEP_TYPE",
	} {
		found := false
		for _, c := range resp.IssueCodes {
			if c == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected issue code %q in response", code)
		}
	}

	if len(resp.NodeTypes) == 0 {
		t.Error("node_types must not be empty")
	}
	// Every node type must have config_fields, usage_notes, or examples for LLM context.
	for _, nt := range resp.NodeTypes {
		if len(nt.ConfigFields) == 0 && nt.UsageNotes == "" && len(nt.Examples) == 0 {
			t.Errorf("node type %q has no LLM knowledge (config_fields, usage_notes, examples all empty)", nt.Type)
		}
	}
}

func TestSchema_NodeTypesHaveConfigFields(t *testing.T) {
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/agent-definitions/schema", nil)

	h.Schema(w, r)

	var resp struct {
		NodeTypes []agentgen.NodeTypeInfo `json:"node_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	byType := make(map[agentgen.StepType]agentgen.NodeTypeInfo)
	for _, nt := range resp.NodeTypes {
		byType[nt.Type] = nt
	}

	// Core implemented nodes must have config fields.
	for _, st := range []agentgen.StepType{agentgen.StepLLM, agentgen.StepHTTP, agentgen.StepMCPCall, agentgen.StepTransform} {
		nt, ok := byType[st]
		if !ok {
			t.Errorf("node type %q missing from schema response", st)
			continue
		}
		if len(nt.ConfigFields) == 0 {
			t.Errorf("node type %q has no config_fields", st)
		}
	}
}

// ── Generate tests ────────────────────────────────────────────────────────────

func TestGenerate_EmptyPromptReturns400(t *testing.T) {
	llm := &mockLLMCaller{response: "{}"}
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, llm)

	body := `{"prompt":""}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/agent-definitions/generate", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	h.Generate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGenerate_BadJSONBodyReturns400(t *testing.T) {
	llm := &mockLLMCaller{response: "{}"}
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, llm)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/agent-definitions/generate", bytes.NewBufferString("not json"))
	r.Header.Set("Content-Type", "application/json")

	h.Generate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGenerate_NoLLMReturns501(t *testing.T) {
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, nil)

	body := `{"prompt":"build me an echo agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/agent-definitions/generate", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	h.Generate(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGenerate_ValidDefinitionReturnedWithIssues(t *testing.T) {
	// A minimal valid definition — Validate will produce warnings (stub nodes etc.)
	// but no structural errors.
	defJSON := `{
		"agent_root": {"display_name":"Echo Agent","description":"Echoes input","version":"1.0.0"},
		"skills": [{
			"skill_id": "echo",
			"name": "Echo",
			"steps": [
				{"id":"step_input","type":"input","next":["step_response"]},
				{"id":"step_response","type":"response","config":{"from_var":"input"}}
			]
		}]
	}`

	llm := &mockLLMCaller{response: defJSON}
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, llm)

	body := `{"prompt":"build an echo agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/agent-definitions/generate", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	h.Generate(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Definition json.RawMessage `json:"definition"`
		Issues     []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"issues"`
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Definition) == 0 {
		t.Error("definition must not be empty")
	}
	// Minimal echo is structurally valid; warnings may exist (e.g. missing output_var on input)
	// but no errors — so valid should be true.
	if !resp.Valid {
		errorCodes := []string{}
		for _, iss := range resp.Issues {
			if iss.Severity == "error" {
				errorCodes = append(errorCodes, iss.Code)
			}
		}
		if len(errorCodes) > 0 {
			t.Errorf("expected valid=true but got errors: %v", errorCodes)
		}
	}
}

func TestGenerate_LLMReturnsCodeFencedJSON(t *testing.T) {
	defJSON := "```json\n{\"agent_root\":{\"display_name\":\"X\",\"description\":\"Y\",\"version\":\"1\"},\"skills\":[{\"skill_id\":\"s\",\"name\":\"S\",\"steps\":[{\"id\":\"i\",\"type\":\"input\",\"next\":[\"r\"]},{\"id\":\"r\",\"type\":\"response\",\"config\":{\"from_var\":\"input\"}}]}]}\n```"

	llm := &mockLLMCaller{response: defJSON}
	h := admin.NewAgentDefinitionSchemaHandler(&fakeDB{}, llm)

	body := `{"prompt":"build agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/agent-definitions/generate", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")

	h.Generate(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
