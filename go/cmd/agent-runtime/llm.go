package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
)

// buildSDKAgentCard constructs a proper a2a.AgentCard from the AgentSpec.
// It sets InputModes and OutputModes per-skill, populates SupportedInterfaces
// (the SDK v2.5 replacement for the deprecated URL field), and uses value-typed
// AgentCapabilities as required by the struct definition.
func buildSDKAgentCard(spec *agentgen.AgentSpec) *a2a.AgentCard {
	skills := make([]a2a.AgentSkill, len(spec.Skills))
	for i, sk := range spec.Skills {
		skills[i] = a2a.AgentSkill{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
			InputModes:  []string{"text/plain", "application/json"},
			OutputModes: []string{"text/plain"},
		}
	}

	agentURL := fmt.Sprintf("http://them-agent-runtime:9300/agents/%s", spec.Slug)

	return &a2a.AgentCard{
		Name:        spec.Card.Name,
		Description: spec.Card.Description,
		Version:     spec.Card.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(agentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         spec.Card.Capabilities.Streaming,
			PushNotifications: spec.Card.Capabilities.PushNotifications,
		},
		Skills: skills,
	}
}

// writeJSONRPCError writes a JSON-RPC 2.0 error response before the SDK handler is reached
// (i.e., during our auth/spec/binding middleware phase).
func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// multiLLMFactory routes to the correct provider implementation.
// Currently only "anthropic" is fully implemented; other providers return a clear error.
type multiLLMFactory struct {
	platformKey string
}

func (f *multiLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for provider %q — set a key in App Runtime", provider)
	}
	switch provider {
	case "anthropic", "":
		p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
		return &anthropicProviderAdapter{p: p}, nil
	default:
		return nil, fmt.Errorf("provider %q is not yet supported in the agent runtime; only 'anthropic' is available", provider)
	}
}

// anthropicProviderAdapter adapts llm.AnthropicProvider to agentgen.LLMProvider.
// It calls Provider.Stream with a single user message and collects text deltas.
type anthropicProviderAdapter struct {
	p *llm.AnthropicProvider
}

func (a *anthropicProviderAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msgs := []domain.Message{
		{
			Role:  domain.RoleUser,
			Parts: []domain.ContentPart{{Type: "text", Text: userPrompt}},
		},
	}
	opts := llm.Options{SystemPrompt: systemPrompt}
	ch, err := a.p.Stream(ctx, msgs, nil, opts)
	if err != nil {
		return "", fmt.Errorf("LLM stream start: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Delta)
		case "error":
			return "", fmt.Errorf("LLM stream error: %w", ev.Error)
		}
	}
	return sb.String(), nil
}

var _ agentgen.LLMProvider = (*anthropicProviderAdapter)(nil)
var _ agentgen.LLMFactory = (*multiLLMFactory)(nil)

// pgxAgentEndpointQueryer implements agentgen.AgentEndpointQueryer using pgxpool.
// Returns (agent_id, binding_id, endpoint_url, auth_token_encrypted) by joining
// agents → app_agent_bindings → applications to enforce tenant + binding ownership.
// Returns no row when the agent is disabled, not bound to the app, or wrong tenant.
type pgxAgentEndpointQueryer struct {
	pool *pgxpool.Pool
}

type pgxSingleRow struct{ row interface{ Scan(...any) error } }

func (r pgxSingleRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

func (q *pgxAgentEndpointQueryer) QueryAgentEndpoint(ctx context.Context, tenantID, applicationID, agentSlug string) agentgen.AgentEndpointRow {
	row := q.pool.QueryRow(ctx,
		`SELECT a.id::text, b.id::text,
		        COALESCE(a.endpoint_url,''), COALESCE(a.auth_token_encrypted,'')
		   FROM them.agents a
		   JOIN them.app_agent_bindings b ON b.agent_id = a.id
		   JOIN them.applications app     ON app.id = b.application_id
		  WHERE a.slug           = $1
		    AND b.application_id = $2::uuid
		    AND app.tenant_id    = $3::uuid
		    AND a.enabled        = true`,
		agentSlug, applicationID, tenantID)
	return pgxSingleRow{row: row}
}

var _ agentgen.AgentEndpointQueryer = (*pgxAgentEndpointQueryer)(nil)

// compile-time check that a2asrv.NewStaticAgentCardHandler is used (keeps import live in tests).
var _ = a2asrv.NewStaticAgentCardHandler
