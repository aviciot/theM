package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/agentgen"
)

// generateLLMCaller is the interface the Generate handler uses to call an LLM.
// The real implementation wraps the Anthropic Messages API; tests inject a mock.
type generateLLMCaller interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// AgentDefinitionSchemaHandler serves:
//
//	GET  /admin/agent-definitions/schema   — wire format + node schema + issue codes
//	POST /admin/agent-definitions/generate — LLM-generated agent definition JSON
type AgentDefinitionSchemaHandler struct {
	db  *dal.DB
	llm generateLLMCaller // may be nil (Generate returns 501 when unset)
}

// NewAgentDefinitionSchemaHandler creates the handler.
// Pass nil for llm to disable the Generate endpoint (returns 501).
func NewAgentDefinitionSchemaHandler(db DBQuerier, llm generateLLMCaller) *AgentDefinitionSchemaHandler {
	return &AgentDefinitionSchemaHandler{db: dal.NewDB(db), llm: llm}
}

// ── Schema endpoint ───────────────────────────────────────────────────────────

// wireFormat is the canonical shape of the agent definition JSON the LLM must produce.
const wireFormat = `{
  "agent_root": {
    "display_name": "string (required)",
    "description": "string",
    "version": "string",
    "icon": "string (optional emoji)",
    "category": "string (optional)"
  },
  "skills": [
    {
      "skill_id": "string (unique within agent)",
      "name": "string",
      "description": "string (optional)",
      "tags": "string[] (optional)",
      "input_modes": "string[] (optional, e.g. [\"text\"])",
      "output_modes": "string[] (optional, e.g. [\"text\"])",
      "steps": [
        {
          "id": "string (unique within skill, snake_case)",
          "type": "StepType — one of the registered node types",
          "label": "string (optional, human-readable name)",
          "config": "object — fields defined per node type config_fields",
          "next": "string[] (optional, step IDs to execute after this one)",
          "branches": "object (optional, for branch nodes: {\"true_next\": \"step_id\", \"false_next\": \"step_id\"})",
          "inputs": "object (optional, explicit port bindings: {\"port_id\": {\"name\": \"var_name\"}})",
          "outputs": "object (optional, explicit port bindings)"
        }
      ]
    }
  ]
}`

// issueCodes lists every validation issue code the compiler can emit.
var issueCodes = []string{
	"DUPLICATE_SKILL",
	"MISSING_FIELD",
	"DUPLICATE_STEP",
	"UNKNOWN_STEP_TYPE",
	"DANGLING_NEXT",
	"DANGLING_BRANCH",
	"MISSING_INPUT_EDGE",
	"TOO_MANY_INPUT_EDGES",
	"SOURCE_HAS_INPUT",
	"MISSING_OUTPUT_EDGE",
	"SINK_HAS_OUTPUT",
	"TOO_MANY_OUTPUT_EDGES",
	"NODE_NOT_EXECUTABLE",
	"BROKEN_BINDING",
	"UNRESOLVED_INPUT",
	"CYCLE_DETECTED",
}

type agentDefinitionSchemaResponse struct {
	WireFormat string                 `json:"wire_format"`
	IssueCodes []string               `json:"issue_codes"`
	NodeTypes  []agentgen.NodeTypeInfo `json:"node_types"`
}

// Schema handles GET /admin/agent-definitions/schema.
func (h *AgentDefinitionSchemaHandler) Schema(w http.ResponseWriter, r *http.Request) {
	resp := agentDefinitionSchemaResponse{
		WireFormat: wireFormat,
		IssueCodes: issueCodes,
		NodeTypes:  agentgen.AllNodeTypeInfos(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ── Generate endpoint ─────────────────────────────────────────────────────────

type generateRequest struct {
	Prompt string `json:"prompt"`
}

type generateResponse struct {
	Definition json.RawMessage `json:"definition"`
	Issues     []issueDoc      `json:"issues"`
	Valid       bool            `json:"valid"`
}

type issueDoc struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	NodeID   string `json:"node_id,omitempty"`
	SkillID  string `json:"skill_id,omitempty"`
	Field    string `json:"field,omitempty"`
}

// Generate handles POST /admin/agent-definitions/generate.
func (h *AgentDefinitionSchemaHandler) Generate(w http.ResponseWriter, r *http.Request) {
	if h.llm == nil {
		http.Error(w, "LLM not configured", http.StatusNotImplemented)
		return
	}

	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	// Load tenant LLM providers for context.
	providers, _ := h.db.ListProviders(r.Context())

	systemPrompt := buildSystemPrompt(providers)
	raw, err := h.llm.Complete(r.Context(), systemPrompt, req.Prompt)
	if err != nil {
		http.Error(w, "LLM error: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Extract JSON from the LLM response (it may wrap it in a code fence).
	extracted, err := extractJSON(raw)
	if err != nil {
		http.Error(w, "LLM did not return valid JSON: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Validate the extracted JSON using the agentgen compiler.
	issues, valid := validateDefinition(extracted)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(generateResponse{
		Definition: json.RawMessage(extracted),
		Issues:     issues,
		Valid:       valid,
	})
}

// buildSystemPrompt constructs the LLM system prompt from node schemas and tenant providers.
func buildSystemPrompt(providers []dal.LLMProvider) string {
	nodes := agentgen.AllNodeTypeInfos()

	var sb strings.Builder
	sb.WriteString("You are the-M Canvas Agent Builder AI.\n")
	sb.WriteString("Your task: generate a valid agent definition JSON from the user's description.\n\n")

	sb.WriteString("## Wire format\n")
	sb.WriteString("The JSON you produce MUST match this shape exactly (use display_name, not name, for agent_root):\n```json\n")
	sb.WriteString(wireFormat)
	sb.WriteString("\n```\n\n")

	sb.WriteString("## Available node types\n")
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("### %s (`%s`)\n", n.Label, n.Type))
		sb.WriteString(n.Description + "\n")
		if n.UsageNotes != "" {
			sb.WriteString("**Usage:** " + n.UsageNotes + "\n")
		}
		if len(n.ConfigFields) > 0 {
			sb.WriteString("**Config fields:**\n")
			for _, f := range n.ConfigFields {
				req := ""
				if f.Required {
					req = " (required)"
				}
				sb.WriteString(fmt.Sprintf("- `%s` (%s%s): %s\n", f.Key, f.Type, req, f.Description))
				if f.Example != "" {
					sb.WriteString(fmt.Sprintf("  Example: `%s`\n", f.Example))
				}
			}
		}
		if len(n.Examples) > 0 {
			sb.WriteString("**Examples:**\n")
			for _, ex := range n.Examples {
				cfgBytes, _ := json.Marshal(ex.Config)
				sb.WriteString(fmt.Sprintf("- %s: `%s`\n", ex.Description, string(cfgBytes)))
			}
		}
		if len(n.AllowedSuccessors) > 0 {
			types := make([]string, len(n.AllowedSuccessors))
			for i, t := range n.AllowedSuccessors {
				types[i] = string(t)
			}
			sb.WriteString("**Allowed successors:** " + strings.Join(types, ", ") + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Validation rules\n")
	sb.WriteString("- Every skill must start with exactly one `input` node\n")
	sb.WriteString("- Every skill must end with exactly one `response` or `stream_out` node\n")
	sb.WriteString("- Step IDs must be unique within a skill\n")
	sb.WriteString("- All `next` references must point to real step IDs\n")
	sb.WriteString("- No cycles allowed in step graph\n\n")

	if len(providers) > 0 {
		sb.WriteString("## Available LLM providers (for llm node model field)\n")
		for _, p := range providers {
			if p.Enabled {
				sb.WriteString(fmt.Sprintf("- %s: default model `%s`\n", p.DisplayName, p.DefaultModel))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Output instructions\n")
	sb.WriteString("Return ONLY the JSON object. No explanation, no markdown, no code fence.\n")
	sb.WriteString("The JSON must be valid and match the wire format exactly.\n")

	return sb.String()
}

// reJSONBlock extracts a JSON code fence from LLM output.
var reJSONBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// extractJSON extracts the first valid JSON object from an LLM response.
// It handles bare JSON and code-fence-wrapped JSON.
func extractJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)

	// Fast path: already a bare JSON object.
	if strings.HasPrefix(raw, "{") {
		var check any
		if err := json.Unmarshal([]byte(raw), &check); err == nil {
			return raw, nil
		}
	}

	// Try extracting from a code fence.
	if m := reJSONBlock.FindStringSubmatch(raw); len(m) == 2 {
		candidate := strings.TrimSpace(m[1])
		var check any
		if err := json.Unmarshal([]byte(candidate), &check); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no valid JSON object found in LLM response")
}

// validateDefinition parses and validates an agent definition JSON using the agentgen compiler.
// Returns a slice of issues and true when no error-severity issues are found.
func validateDefinition(jsonStr string) ([]issueDoc, bool) {
	issues, err := agentgen.ValidateDefinitionJSON([]byte(jsonStr))
	if err != nil {
		return []issueDoc{{Code: "PARSE_ERROR", Severity: "error", Message: err.Error()}}, false
	}

	docs := make([]issueDoc, 0, len(issues))
	hasError := false
	for _, iss := range issues {
		docs = append(docs, issueDoc{
			Code:     iss.Code,
			Severity: iss.Severity,
			Message:  iss.Message,
			NodeID:   iss.NodeID,
			SkillID:  iss.SkillID,
			Field:    iss.Field,
		})
		if iss.Severity == "error" {
			hasError = true
		}
	}
	return docs, !hasError
}
