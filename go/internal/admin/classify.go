package admin

// classifyAgent calls the Anthropic API to assign a category and icon to an
// agent based on its name, description, and skills. It is best-effort: any
// error returns ("", "") so the caller can continue without a category.
//
// Configuration is read from them.config row where config_key='system_agents'.
// Expected shape:
//
//	{
//	  "roles": {
//	    "classifier": {
//	      "enabled": true,
//	      "provider": "anthropic",
//	      "model": "claude-haiku-4-5-20251001",
//	      "api_key_encrypted": "enc:..."
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
	"regexp"
	"strings"
	"time"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// classifierDAL is the minimal DAL surface classifyAgent needs.
type classifierDAL interface {
	GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
}

var (
	classifierValidCategories = map[string]bool{
		"Research": true, "Coding": true, "Vision": true, "Security": true,
		"A2A": true, "Data": true, "Communication": true, "Agent": true,
	}
	classifierValidIcon = regexp.MustCompile(`^[a-zA-Z0-9_]{1,40}$`)
)

// classifierConfig is the unmarshalled shape of the 'system_agents' config row.
type classifierConfig struct {
	Roles struct {
		Classifier struct {
			Enabled          bool   `json:"enabled"`
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			APIKeyEncrypted  string `json:"api_key_encrypted"`
		} `json:"classifier"`
	} `json:"roles"`
}

// classifyAgent returns (category, icon) using the Anthropic classifier.
// Any failure silently returns ("", "").
func classifyAgent(
	ctx context.Context,
	d classifierDAL,
	fernetKey []byte,
	displayName, description string,
	skills []any,
) (category, icon string) {
	// Load config — failure is silent.
	row, err := d.GetConfig(ctx, "system_agents")
	if err != nil || row == nil {
		return "", ""
	}

	var cfg classifierConfig
	if err := json.Unmarshal(row.Value, &cfg); err != nil {
		return "", ""
	}
	if !cfg.Roles.Classifier.Enabled {
		return "", ""
	}

	// Decrypt API key.
	apiKey, err := crypto.DecryptStored(fernetKey, cfg.Roles.Classifier.APIKeyEncrypted)
	if err != nil || apiKey == "" {
		return "", ""
	}

	// Build skill names list.
	var skillNames []string
	for _, s := range skills {
		if sm, ok := s.(map[string]any); ok {
			if name, ok := sm["name"].(string); ok && name != "" {
				skillNames = append(skillNames, name)
			}
		}
	}

	model := cfg.Roles.Classifier.Model
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}

	systemPrompt := "You are an agent classifier. Given an agent's name, description, and skills, return ONLY valid JSON:\n{\"category\": \"<one of: Research|Coding|Vision|Security|A2A|Data|Communication|Agent>\", \"icon\": \"<Material Symbols name, e.g. hub, code, search, visibility>\"}\nNo explanation, no markdown, just JSON."
	userMsg := fmt.Sprintf("Name: %s\nDescription: %s\nSkills: %s",
		displayName, description, strings.Join(skillNames, ", "))

	// Call Anthropic Messages API.
	type anthropicMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type anthropicReq struct {
		Model     string         `json:"model"`
		MaxTokens int            `json:"max_tokens"`
		Messages  []anthropicMsg `json:"messages"`
		System    string         `json:"system"`
	}
	reqBody := anthropicReq{
		Model:     model,
		MaxTokens: 60,
		Messages:  []anthropicMsg{{Role: "user", Content: userMsg}},
		System:    systemPrompt,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", ""
	}

	httpCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", ""
	}

	// Extract text from response.
	var anthropicResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &anthropicResp); err != nil {
		return "", ""
	}
	var text string
	for _, c := range anthropicResp.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	if text == "" {
		return "", ""
	}

	// Parse the JSON result.
	var result struct {
		Category string `json:"category"`
		Icon     string `json:"icon"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return "", ""
	}

	// Validate category.
	if !classifierValidCategories[result.Category] {
		result.Category = "Agent"
	}

	// Validate icon.
	if !classifierValidIcon.MatchString(result.Icon) {
		result.Icon = "smart_toy"
	}

	return result.Category, result.Icon
}
