package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/tenantctx"
)

// TokensHandler handles /api/v1/admin/tokens routes.
type TokensHandler struct {
	legacySvc *service.TokenService
	pools     *db.Pools
	cache     CacheInvalidator
}

// NewTokensHandler creates a TokensHandler.
// When pools is non-nil each request uses a TenantTx (RLS-ready path).
func NewTokensHandler(legacyDB DBQuerier, pools *db.Pools, cache CacheInvalidator) *TokensHandler {
	return &TokensHandler{
		legacySvc: service.NewTokenService(dal.NewDB(legacyDB), cache, nil),
		pools:     pools,
		cache:     cache,
	}
}

func (h *TokensHandler) openSvc(ctx context.Context, tenantID string) (svc *service.TokenService, commit func(context.Context) error, rollback func(), err error) {
	if h.pools == nil {
		return h.legacySvc, func(_ context.Context) error { return nil }, func() {}, nil
	}
	tenantUUID, uuidErr := uuid.Parse(tenantID)
	if uuidErr != nil {
		return nil, nil, nil, uuidErr
	}
	tx, txErr := h.pools.BeginTenantTx(ctx, tenantUUID)
	if txErr != nil {
		return nil, nil, nil, txErr
	}
	rb := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}
	return service.NewTokenService(dal.NewDBFromTenantQuerier(tx), h.cache, nil), tx.Commit, rb, nil
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
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	tokens, err := svc.List(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, tokens)
}

// Create handles POST /api/v1/admin/tokens
func (h *TokensHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body tokenCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
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
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.Create(r.Context(), tenantID, in, body.OrchestratorID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/tokens/%s", out.ID))
	writeJSON(w, http.StatusCreated, out)
}

// Get handles GET /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	t, err := svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, t)
}

// Update handles PATCH /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")
	var body tokenPatchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	patch := dal.TokenPatchRow{
		Label:     body.Label,
		Enabled:   body.Enabled,
		ExpiresAt: body.ExpiresAt,
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	t, err := svc.Update(r.Context(), tenantID, id, patch)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Delete handles DELETE /api/v1/admin/tokens/{token_id}
func (h *TokensHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "token_id")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Delete(r.Context(), tenantID, id); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
