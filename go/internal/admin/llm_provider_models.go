package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// listAvailableModels fetches the live model list from a provider's real API,
// using the given decrypted API key. Mirrors probeLLMWithBase's dispatch shape.
func listAvailableModels(ctx context.Context, provider, apiKey, baseURL string) ([]string, string) {
	switch provider {
	case "anthropic":
		return fetchAnthropicModels(ctx, apiKey)
	case "openai":
		if baseURL != "" {
			return fetchOpenAICompatModels(ctx, apiKey, baseURL)
		}
		return fetchOpenAICompatModels(ctx, apiKey, "https://api.openai.com/v1/models")
	case "groq":
		return fetchOpenAICompatModels(ctx, apiKey, "https://api.groq.com/openai/v1/models")
	case "gemini":
		return fetchGeminiModels(ctx, apiKey)
	default:
		if baseURL != "" {
			return fetchOpenAICompatModels(ctx, apiKey, baseURL)
		}
		return nil, "unsupported provider: " + provider
	}
}

func fetchAnthropicModels(ctx context.Context, apiKey string) ([]string, string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if errMsg := doModelsFetch(req, &out); errMsg != "" {
		return nil, errMsg
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return models, ""
}

// fetchOpenAICompatModels fetches GET {baseURL}/models (or baseURL itself if it
// already ends in "/models") for any OpenAI-compatible provider (openai, groq, custom).
func fetchOpenAICompatModels(ctx context.Context, apiKey, url string) ([]string, string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if errMsg := doModelsFetch(req, &out); errMsg != "" {
		return nil, errMsg
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return models, ""
}

func fetchGeminiModels(ctx context.Context, apiKey string) ([]string, string) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if errMsg := doModelsFetch(req, &out); errMsg != "" {
		return nil, errMsg
	}
	models := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, m.Name)
	}
	sort.Strings(models)
	return models, ""
}

// doModelsFetch performs req, decodes a 200 JSON body into out, and returns
// a non-empty error message on any failure (network, non-200, decode).
func doModelsFetch(req *http.Request, out any) string {
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "failed to decode provider response"
	}
	return ""
}
