package admin

// llmCardAnalysis calls the security_scanner system-agent role to assess a
// target agent's card/skills for security risk. Ported from what used to be
// agents/security_scanner/scanner.py's llm_card_analysis — that step now runs
// in Go (same place as classifyAgent/synthesizeAppCard) so it can resolve a
// tenant's own general/custom mode config instead of always using the
// scanner container's single hardcoded ANTHROPIC_API_KEY. The Python scanner
// (agents/security_scanner/) now only runs the HTTP surface probes; this
// function replaces the LLM half of what run_scan used to compute.
//
// Never returns an error to the caller — any failure (no config, no usable
// key, network, bad JSON) degrades to a zero-findings result with a
// "probes only" summary, exactly matching the Python version's degraded path
// so compute_score's downstream -10 degraded penalty logic (ported into
// mergeSecurityScanResult) keeps working unchanged.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aviciot/them/internal/crypto"
)

const securityScanSystemPrompt = `You are a security auditor for an AI agent orchestration platform. You analyze one agent's declared metadata (agent card, description, and skills) for security risk. You do NOT execute anything or call the agent. Judge only what the metadata reveals.

Assess these dimensions:
1. Skill scope — are any skills dangerously broad or capable of destructive/arbitrary action (e.g. "execute commands", "read any file", "run arbitrary code", unrestricted network/filesystem/database access)?
2. Description quality — is the tool description accurate, specific, and appropriately scoped? Flag vague, over-promising, or manipulable descriptions that raise prompt-injection risk.
3. Input/output modes — are risky or unconstrained data types accepted with no stated limits?
4. Missing guardrails — absence of an input schema or constraints means the agent accepts unbounded input; treat as elevated risk.

Return ONLY a JSON object, no prose, no markdown fences, with this exact shape:
{
  "summary": "<one plain-English sentence summarizing overall security posture>",
  "findings": [
    {
      "id": "<short_snake_case_id>",
      "label": "<short human label>",
      "status": "pass" | "warn" | "fail",
      "risk": "low" | "medium" | "high",
      "detail": "<one sentence: what you observed>",
      "recommendation": "<one sentence: concrete fix, or 'No action needed'>"
    }
  ]
}
Rules: 2-5 findings. Use "pass"/"low" for things that look fine. Reserve "fail"/"high" for genuinely dangerous scope or missing auth-relevant guardrails. Be concise and specific.`

// securityScanLLMResult mirrors scanner.py's llm_card_analysis return shape.
type securityScanLLMResult struct {
	Summary  string           `json:"summary"`
	Findings []map[string]any `json:"findings"`
}

// llmCardAnalysis resolves tenantID's security_scanner config (general or
// custom mode, falling back to the platform-global them.config['system_agents']
// row) and asks it to assess payload for security risk. tenantID must be the
// caller's own tenant — never resolve another tenant's config here.
func llmCardAnalysis(ctx context.Context, d classifierDAL, fernetKey []byte, tenantID string, payload scanAgentPayload) securityScanLLMResult {
	degraded := securityScanLLMResult{Summary: "Card analysis unavailable — probes only (no key configured)."}

	// Load platform-global config first — used as the fallback when the
	// tenant has no row / mode="custom" with unset fields (same pattern as
	// classifyAgent/synthesizeAppCard).
	var platformProvider, platformModel, platformAPIKey, platformBaseURL string
	var scRow saRoleStored
	if row, err := d.GetConfig(ctx, "system_agents"); err == nil && row != nil {
		var stored saConfigStored
		if err := json.Unmarshal(row.Value, &stored); err == nil {
			scRow = stored.Roles["security_scanner"]
		}
	}
	if scRow.Enabled && scRow.Provider != nil && scRow.Model != nil && scRow.APIKeyEncrypted != nil {
		if apiKey, err := crypto.DecryptStored(fernetKey, *scRow.APIKeyEncrypted); err == nil && apiKey != "" {
			platformProvider = *scRow.Provider
			platformModel = *scRow.Model
			platformAPIKey = apiKey
			if scRow.BaseURL != nil {
				platformBaseURL = *scRow.BaseURL
			}
		}
	}

	resolved, ok := resolveSystemAgentRole(ctx, d, fernetKey, tenantID, "security_scanner",
		platformProvider, platformModel, platformAPIKey, platformBaseURL, "")
	if !ok {
		return degraded
	}

	userPrompt := buildSecurityScanUserPrompt(payload)
	respText, err := dispatchLLMText(ctx, resolved.Provider, resolved.Model, resolved.APIKey, resolved.BaseURL, securityScanSystemPrompt, userPrompt)
	if err != nil || respText == "" {
		return degraded
	}

	text := strings.TrimSpace(respText)
	if idx := strings.Index(text, "{"); idx > 0 {
		text = text[idx:]
	}
	if idx := strings.LastIndex(text, "}"); idx >= 0 && idx < len(text)-1 {
		text = text[:idx+1]
	}

	var result securityScanLLMResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return degraded
	}
	return result
}

// buildSecurityScanUserPrompt ports scanner.py's _build_user_prompt.
func buildSecurityScanUserPrompt(payload scanAgentPayload) string {
	skillsJSON, _ := json.MarshalIndent(payload.Skills, "", "  ")
	scheme := "http"
	if strings.HasPrefix(payload.EndpointURL, "https://") {
		scheme = "https"
	}

	hasInputSchema := false
	if skillsList, ok := payload.Skills.([]any); ok {
		for _, s := range skillsList {
			if sm, ok := s.(map[string]any); ok {
				if _, present := sm["inputModes"]; present {
					hasInputSchema = true
					break
				}
			}
		}
	}
	hasSchemaStr := "no"
	if hasInputSchema {
		hasSchemaStr = "yes"
	}

	desc := payload.Description
	if desc == "" {
		desc = "(none)"
	}

	var sb strings.Builder
	sb.WriteString("Agent under review:\n\n")
	sb.WriteString("slug: " + orQuestionMark(payload.Slug) + "\n")
	sb.WriteString("display_name: " + orQuestionMark(payload.DisplayName) + "\n\n")
	sb.WriteString("Description (this is the text the orchestrating LLM sees to decide when to call it):\n")
	sb.WriteString(desc + "\n\n")
	sb.WriteString("Declared skills (JSON):\n")
	sb.Write(skillsJSON)
	sb.WriteString("\n\n")
	sb.WriteString("Capabilities: streaming=" + boolStr(payload.SupportsStreaming) + ", push=" + boolStr(payload.SupportsPush) + "\n")
	sb.WriteString("Input schema present: " + hasSchemaStr + "\n")
	sb.WriteString("Endpoint scheme: " + scheme + "\n\n")
	sb.WriteString("Analyze and return the JSON object.")
	return sb.String()
}

func orQuestionMark(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
