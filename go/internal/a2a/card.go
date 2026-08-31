package a2a

// card.go — agent card construction and serving using the official a2a-go/v2 SDK.
// The agent card is served at GET /a2a/{app_slug}/{ep_slug}/.well-known/agent.json.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
)

// handleAgentCard serves GET /a2a/{app_slug}/{ep_slug}/.well-known/agent.json.
// When a synthesized card exists in the DB it is served via the SDK handler.
// When not yet synthesized, a minimal card is returned from orchestrator name.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "ep_slug")

	base := s.publicURL
	if base == "" {
		scheme := r.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
		}
		host := r.Host
		if host == "" {
			host = "localhost"
		}
		base = scheme + "://" + host
	}
	epURL := fmt.Sprintf("%s/a2a/%s/%s", base, appSlug, epSlug)

	// Try to serve synthesized card from DB.
	if s.cardLoader != nil {
		row, err := s.cardLoader.LoadEPCard(r.Context(), appSlug, epSlug)
		if err == nil && len(row.AgentCardJSON) > 0 {
			// Parse stored card; inject the live URL and SDK-standard capabilities.
			var stored map[string]any
			if json.Unmarshal(row.AgentCardJSON, &stored) == nil {
				stored["url"] = epURL
				stored["capabilities"] = map[string]any{"streaming": true}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(stored)
				return
			}
		}
		// Card not yet synthesized — build minimal card from orchestrator name.
		if err == nil {
			name := row.OrchestratorDisplayName
			if name == "" {
				name = row.AppName
			}
			card := buildSDKAgentCard(name, "Powered by the-M orchestration platform", epURL)
			a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
			return
		}
	}

	// Final fallback — no loader or EP not found.
	card := buildSDKAgentCard("the-M Orchestrator", "AI orchestration platform", epURL)
	a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
}

// buildSDKAgentCard constructs a minimal *a2a.AgentCard from the given name,
// description, and endpoint URL. The URL is placed in SupportedInterfaces per
// the A2A v1.0 spec (deprecated top-level "url" field is intentionally omitted
// for SDK-built cards; synthesized cards retain the "url" key for compatibility).
func buildSDKAgentCard(name, description, epURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        name,
		Description: description,
		Version:     "1.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(epURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
		Skills: []a2a.AgentSkill{},
	}
}
