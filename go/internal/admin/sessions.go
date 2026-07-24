package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/service"
)

// SessionsHandler handles /api/v1/admin/sessions routes.
type SessionsHandler struct {
	svc *service.SessionAdminService
}

// NewSessionsHandler creates a SessionsHandler.
func NewSessionsHandler(r service.SessionReader) *SessionsHandler {
	return &SessionsHandler{svc: service.NewSessionAdminService(r)}
}

// Routes mounts the session admin endpoints.
func (h *SessionsHandler) Routes(r chi.Router) {
	r.Get("/sessions", h.List)
	r.Post("/sessions/{session_id}/disconnect", h.Disconnect)
}

// List handles GET /api/v1/admin/sessions?app_id=<id> OR ?ep_slug=<slug>
// Exactly one of app_id or ep_slug must be provided.
func (h *SessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	epSlug := r.URL.Query().Get("ep_slug")

	if appID == "" && epSlug == "" {
		writeError(w, http.StatusBadRequest, "one of app_id or ep_slug is required")
		return
	}
	if appID != "" && epSlug != "" {
		writeError(w, http.StatusBadRequest, "only one of app_id or ep_slug may be specified")
		return
	}

	var result service.SessionListResult
	var err error
	if appID != "" {
		result, err = h.svc.ListByApp(r.Context(), appID)
	} else {
		result, err = h.svc.ListByEP(r.Context(), epSlug)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list sessions: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Disconnect handles POST /api/v1/admin/sessions/{session_id}/disconnect
func (h *SessionsHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	delivered, err := h.svc.Disconnect(r.Context(), sessionID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "disconnect session: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sessionID,
		"signal_delivered": delivered,
	})
}
