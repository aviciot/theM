package admin

// DiscoverEP handles POST /api/v1/admin/applications/{id}/entry-points/{ep_id}/discover.
// It synthesizes an A2A agent card for the entry point by combining the
// orchestrator's system_prompt with the display_name, description, and skills
// of every wired sub-agent. The result is persisted to entry_points.agent_card
// and returned to the caller.
//
// The synthesis is performed by the card_synthesizer system agent role
// (configured in /admin/system-agents). If that role is disabled or
// unconfigured the handler still returns the raw orchestrator+agent context
// with ok:false and a descriptive detail so the caller can show a partial result.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/tenantctx"
)

// DiscoverEP synthesizes and persists the A2A agent card for one entry point.
func (h *ApplicationsHandler) DiscoverEP(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "app id and ep_id are required")
		return
	}

	// Verify the app belongs to this tenant.
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if _, err := svc.Get(r.Context(), tenantID, appID); err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	_ = commit(r.Context())

	// Load orchestrator bound to this EP.
	orch, err := h.legacyDAL.GetAppOrchForEP(r.Context(), appID, epID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"detail": "entry point has no orchestrator binding",
		})
		return
	}

	// Load sub-agent summaries.
	agentRows, err := h.legacyDAL.GetAgentSummariesByIDs(r.Context(), orch.AllowedAgentIDs)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"detail": "failed to load sub-agents",
		})
		return
	}

	// Convert to synthesizer input.
	agents := make([]subAgentSummary, 0, len(agentRows))
	for _, row := range agentRows {
		var skills []any
		if len(row.SkillsJSON) > 0 {
			_ = json.Unmarshal(row.SkillsJSON, &skills)
		}
		agents = append(agents, subAgentSummary{
			DisplayName: row.DisplayName,
			Description: row.Description,
			Skills:      skills,
		})
	}

	// Call card_synthesizer LLM role.
	card := synthesizeAppCard(
		r.Context(),
		h.legacyDAL,
		h.fernetKey,
		orch.DisplayName,
		orch.SystemPrompt,
		agents,
	)
	if card == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"detail": "card_synthesizer role is disabled or not configured — enable it in Settings → System Agents",
		})
		return
	}

	// Persist to DB.
	cardBytes, err := json.Marshal(card)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal card")
		return
	}
	if err := h.legacyDAL.SetEPAgentCard(r.Context(), appID, epID, cardBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "save card")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"card": card,
	})
}
