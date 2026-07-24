package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// TokensHandler handles /api/v1/admin/tokens routes.
type TokensHandler struct {
	svc *service.TokenService
}

// NewTokensHandler creates a TokensHandler.
func NewTokensHandler(db DBQuerier, cache CacheInvalidator) *TokensHandler {
	return &TokensHandler{svc: service.NewTokenService(dal.NewDB(db), cache, nil)}
}

// Routes mounts the token CRUD endpoints.
func (h *TokensHandler) Routes(r chi.Router) {
	r.Get("/tokens", h.List)
	r.Post("/tokens", h.Create)
	r.Get("/tokens/{token_id}", h.Get)
	r.Patch("/tokens/{token_id}", h.Update)
	r.Delete("/tokens/{token_id}", h.Delete)
}

// tokenCreateBody is the JSON request body for POST /admin/tokens.
type tokenCreateBody struct {
	Label          string  `json:"label"`
	UserID         int64   `json:"user_id"`
	OrchestratorID *string `json:"orchestrator_id"`
	ExpiresAt      *string `json:"expires_at"`
}

// tokenPatchBody is the JSON request body for PATCH /admin/tokens/{token_id}.
type tokenPatchBody struct {
	Label     *string `json:"label"`
	Enabled   *bool   `json:"enabled"`
	ExpiresAt *string `json:"expires_at"`
}

// List handles GET /api/v1/admin/tokens?user_id=<int>
func (h *TokensHandler) List(w http.ResponseWriter, r *http.Request) {
	var userID *int64
	if s := r.URL.Query().Get("user_id"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user_id")
			return
		}
		userID = &n
	}
	tokens, err := h.svc.List(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

// Create handles POST /api/v1/admin/tokens
func (h *TokensHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body tokenCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	in := dal.TokenCreateRow{
		Label:     body.Label,
		UserID:    body.UserID,
		ExpiresAt: body.ExpiresAt,
	}
	out, err := h.svc.Create(r.Context(), in, body.OrchestratorID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create token: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/tokens/%s", out.ID))
	writeJSON(w, http.StatusCreated, out)
}

// Get handles GET /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Update handles PATCH /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")

	var body tokenPatchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	patch := dal.TokenPatchRow{
		Label:     body.Label,
		Enabled:   body.Enabled,
		ExpiresAt: body.ExpiresAt,
	}
	t, err := h.svc.Update(r.Context(), id, patch)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update token: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Delete handles DELETE /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete token: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
