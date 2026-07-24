package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// epConfigChannel is the Redis pub/sub channel for cross-pod EP config cache invalidation.
const epConfigChannel = "them:ep:config:changed"

// validEPTypes is the canonical set of allowed entry_point_type values.
// Must stay in sync with the Python platform's _VALID_EP_TYPES list.
var validEPTypes = map[string]struct{}{
	"websocket": {},
	"sse":       {},
	"voice":     {},
	"webrtc":    {},
	"a2a":       {},
}

// isValidEPType reports whether t is an allowed entry point type.
func isValidEPType(t string) bool {
	_, ok := validEPTypes[t]
	return ok
}

// ApplicationsHandler handles /api/v1/admin/applications routes.
type ApplicationsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
	dal   *dal.DB
}

// NewApplicationsHandler creates an ApplicationsHandler.
func NewApplicationsHandler(db DBQuerier, cache CacheInvalidator) *ApplicationsHandler {
	return &ApplicationsHandler{db: db, cache: cache, dal: dal.NewDB(db)}
}

// Routes mounts application and entry point CRUD endpoints.
func (h *ApplicationsHandler) Routes(r chi.Router) {
	r.Get("/applications", h.List)
	r.Post("/applications", h.Create)
	r.Get("/applications/{id}", h.Get)
	r.Put("/applications/{id}", h.Update)
	r.Patch("/applications/{id}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/applications/{id}", h.Delete)

	r.Post("/applications/{id}/entry-points", h.CreateEntryPoint)
	r.Put("/applications/{id}/entry-points/{ep_id}", h.UpdateEntryPoint)
	r.Patch("/applications/{id}/entry-points/{ep_id}", h.UpdateEntryPoint) // Python sends PATCH
	r.Delete("/applications/{id}/entry-points/{ep_id}", h.DeleteEntryPoint)
}

// List handles GET /api/v1/admin/applications.
func (h *ApplicationsHandler) List(w http.ResponseWriter, r *http.Request) {
	apps, err := h.dal.ListApplications(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// invalidateEP evicts one EP slug from the in-process cache on this pod and
// publishes to epConfigChannel so all other pods do the same.
func (h *ApplicationsHandler) invalidateEP(r *http.Request, epSlug string) {
	if h.cache == nil || epSlug == "" {
		return
	}
	_ = h.cache.Publish(r.Context(), epConfigChannel, epSlug)
}

// invalidateAppEPs fetches all EP slugs for the given application ID and
// publishes a per-slug invalidation message for each.
func (h *ApplicationsHandler) invalidateAppEPs(r *http.Request, appID string) {
	if h.cache == nil {
		return
	}
	for _, slug := range h.dal.ListEPSlugsForApp(r.Context(), appID) {
		_ = h.cache.Publish(r.Context(), epConfigChannel, slug)
	}
}

// Create handles POST /api/v1/admin/applications.
func (h *ApplicationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	id, err := h.dal.CreateApplication(r.Context(), input.Name, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create application: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	a, err := h.dal.GetApplication(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}

	a.EntryPoints = h.dal.ListEntryPoints(r.Context(), id)
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if err := h.dal.UpdateApplication(r.Context(), id, input.Name, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update application: "+err.Error())
		return
	}

	h.invalidateAppEPs(r, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	if err := h.dal.DeleteApplication(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete application: "+err.Error())
		return
	}

	h.invalidateAppEPs(r, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// CreateEntryPoint handles POST /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) CreateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Slug == "" || input.EntryPointType == "" {
		writeError(w, http.StatusBadRequest, "slug and entry_point_type are required")
		return
	}
	if !isValidEPType(input.EntryPointType) {
		writeError(w, http.StatusUnprocessableEntity,
			"invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	epID, err := h.dal.CreateEntryPoint(r.Context(), appID, input.Slug, input.EntryPointType, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create entry point: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s/entry-points/%s", appID, epID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": epID})
}

// UpdateEntryPoint handles PUT/PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) UpdateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.EntryPointType != "" && !isValidEPType(input.EntryPointType) {
		writeError(w, http.StatusUnprocessableEntity,
			"invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	// Fetch old slug for cache invalidation on rename.
	oldSlug, _ := h.dal.GetEntryPointSlug(r.Context(), epID, appID)

	if err := h.dal.UpdateEntryPoint(r.Context(), epID, appID, input.Slug, input.EntryPointType, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update entry point: "+err.Error())
		return
	}

	h.invalidateEP(r, oldSlug)
	h.invalidateEP(r, input.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "updated": true})
}

// DeleteEntryPoint handles DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) DeleteEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	epSlug, _ := h.dal.GetEntryPointSlug(r.Context(), epID, appID)

	if err := h.dal.DeleteEntryPoint(r.Context(), epID, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete entry point: "+err.Error())
		return
	}

	h.invalidateEP(r, epSlug)
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "deleted": true})
}
