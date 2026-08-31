package admin

// synthesizeAppCard calls the card_synthesizer LLM role to generate an A2A
// agent card for an application entry point. It is given the orchestrator's
// system_prompt (the application's intent/persona) and the display_name,
// description, and skills of every sub-agent wired to that orchestrator.
//
// The synthesizer produces a JSON card with name, description, and a skills
// array that reflects what the application as a whole can do — not the
// individual agent internals.
//
// Configuration is read from them.config where config_key='system_agents':
//
//	{
//	  "roles": {
//	    "card_synthesizer": {
//	      "enabled": true,
//	      "provider": "anthropic",
//	      "model": "claude-haiku-4-5-20251001",
//	      "api_key_encrypted": "enc:...",
//	      "system_prompt": "optional override"
//	    }
//	  }
//	}

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// synthesizerDAL is the minimal DAL surface synthesizeAppCard needs.
type synthesizerDAL interface {
	GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
}

// subAgentSummary is the subset of an agent row that the synthesizer sees.
type subAgentSummary struct {
	DisplayName string
	Description string
	Skills      []any
}

const defaultSynthesizerPrompt = `You are an AI application analyst. You will be given:
1. An orchestrator's purpose (system prompt) — this defines what the application is trying to do.
2. A list of sub-agents with their names, descriptions, and skills.

Synthesize a JSON A2A agent card that describes what this APPLICATION can do as a whole.
The card should reflect capabilities from the user's perspective, not internal agent details.

Return ONLY valid JSON in this exact shape (no markdown, no explanation):
{
  "name": "<short application name>",
  "description": "<one-sentence description of what this application does for users>",
  "skills": [
    {
      "id": "<snake_case_id>",
      "name": "<skill name>",
      "description": "<what a user can do with this skill>",
      "tags": ["<tag1>", "<tag2>"]
    }
  ]
}`

// synthesizeAppCard calls the card_synthesizer LLM role and returns the
// synthesized card as a raw JSON object. Returns nil on any failure so callers
// can degrade gracefully.
func synthesizeAppCard(
	ctx context.Context,
	d synthesizerDAL,
	fernetKey []byte,
	orchDisplayName string,
	orchSystemPrompt string,
	agents []subAgentSummary,
) map[string]any {
	row, err := d.GetConfig(ctx, "system_agents")
	if err != nil || row == nil {
		return nil
	}

	var cfg saConfigStored
	if err := json.Unmarshal(row.Value, &cfg); err != nil {
		return nil
	}
	role := cfg.Roles["card_synthesizer"]
	if !role.Enabled || role.Provider == nil || role.Model == nil {
		return nil
	}

	apiKey := ""
	if role.APIKeyEncrypted != nil {
		decrypted, err := crypto.DecryptStored(fernetKey, *role.APIKeyEncrypted)
		if err != nil || decrypted == "" {
			return nil
		}
		apiKey = decrypted
	}
	if apiKey == "" {
		return nil
	}

	systemPrompt := defaultSynthesizerPrompt
	if role.SystemPrompt != nil && *role.SystemPrompt != "" {
		systemPrompt = *role.SystemPrompt
	}

	baseURL := ""
	if role.BaseURL != nil {
		baseURL = *role.BaseURL
	}

	userMsg := buildSynthesizerPrompt(orchDisplayName, orchSystemPrompt, agents)

	result := callSynthesizerLLM(ctx, *role.Provider, *role.Model, apiKey, baseURL, systemPrompt, userMsg)
	return result
}

// buildSynthesizerPrompt assembles the user message sent to the LLM.
func buildSynthesizerPrompt(orchDisplayName, orchSystemPrompt string, agents []subAgentSummary) string {
	var sb strings.Builder
	sb.WriteString("## Orchestrator\n")
	sb.WriteString(fmt.Sprintf("Name: %s\n", orchDisplayName))
	if orchSystemPrompt != "" {
		// Trim to 2000 chars to avoid blowing context — the gist is enough.
		prompt := orchSystemPrompt
		if len(prompt) > 2000 {
			prompt = prompt[:2000] + "..."
		}
		sb.WriteString(fmt.Sprintf("Purpose:\n%s\n", prompt))
	}

	sb.WriteString("\n## Sub-agents\n")
	for _, a := range agents {
		sb.WriteString(fmt.Sprintf("\n### %s\n", a.DisplayName))
		if a.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", a.Description))
		}
		if len(a.Skills) > 0 {
			sb.WriteString("Skills:\n")
			for _, s := range a.Skills {
				if sm, ok := s.(map[string]any); ok {
					name, _ := sm["name"].(string)
					desc, _ := sm["description"].(string)
					if name != "" {
						sb.WriteString(fmt.Sprintf("  - %s: %s\n", name, desc))
					}
				}
			}
		}
	}
	return sb.String()
}

// callSynthesizerLLM dispatches to the right provider and returns the parsed
// card JSON. Returns nil on any network/parse failure.
func callSynthesizerLLM(ctx context.Context, provider, model, apiKey, baseURL, systemPrompt, userMsg string) map[string]any {
	var respText string
	var err error

	switch provider {
	case "anthropic":
		respText, err = callAnthropicSynth(ctx, model, apiKey, systemPrompt, userMsg)
	case "openai":
		url := "https://api.openai.com/v1/chat/completions"
		if baseURL != "" {
			url = baseURL
		}
		respText, err = callOpenAICompatSynth(ctx, model, apiKey, url, systemPrompt, userMsg)
	case "groq":
		respText, err = callOpenAICompatSynth(ctx, model, apiKey, "https://api.groq.com/openai/v1/chat/completions", systemPrompt, userMsg)
	default:
		if baseURL != "" {
			respText, err = callOpenAICompatSynth(ctx, model, apiKey, baseURL, systemPrompt, userMsg)
		}
	}

	if err != nil || respText == "" {
		return nil
	}

	// Strip markdown fences if the model wrapped the JSON.
	text := strings.TrimSpace(respText)
	if idx := strings.Index(text, "{"); idx > 0 {
		text = text[idx:]
	}
	if idx := strings.LastIndex(text, "}"); idx >= 0 && idx < len(text)-1 {
		text = text[:idx+1]
	}

	var card map[string]any
	if err := json.Unmarshal([]byte(text), &card); err != nil {
		return nil
	}
	return card
}

func callAnthropicSynth(ctx context.Context, model, apiKey, systemPrompt, userMsg string) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := map[string]any{
		"model":      model,
		"max_tokens": 800,
		"system":     systemPrompt,
		"messages":   []msg{{Role: "user", Content: userMsg}},
	}
	b, _ := json.Marshal(payload)

	httpCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return "", err
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", nil
}

func callOpenAICompatSynth(ctx context.Context, model, apiKey, url, systemPrompt, userMsg string) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := map[string]any{
		"model":      model,
		"max_tokens": 800,
		"messages": []msg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
	}
	b, _ := json.Marshal(payload)

	httpCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai-compat status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return "", err
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) > 0 {
		return out.Choices[0].Message.Content, nil
	}
	return "", nil
}
