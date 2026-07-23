package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// epConfigChannel is the Redis pub/sub channel for cross-pod EP config cache invalidation.
const epConfigChannel = "them:ep:config:changed"

// validEPTypes is the canonical set of allowed entry_point_type values.
// Must stay in sync with the Python platform's _VALID_EP_TYPES list and
// docs/architecture-v2/schema-migrations.md MIG-002.
var validEPTypes = map[string]struct{}{
	"websocket": {},
	"sse":       {},
	"voice":     {},
}

// isValidEPType reports whether t is an allowed entry point type.
func isValidEPType(t string) bool {
	_, ok := validEPTypes[t]
	return ok
}

// ── Application types ─────────────────────────────────────────────────────────

// Application is the JSON representation of a them.applications row.
type Application struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Slug        string       `json:"slug"`
	Description string       `json:"description,omitempty"`
	Enabled     bool         `json:"enabled"`
	EntryPoints []EntryPoint `json:"entry_points,omitempty"`
}

// EntryPoint is one access door for an application.
type EntryPoint struct {
	ID               int64  `json:"id"`
	ApplicationID    int64  `json:"application_id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	EPType           string `json:"ep_type"`
	OrchestratorName string `json:"orchestrator_name"`
	Enabled          bool   `json:"enabled"`
}

// ApplicationInput is the request body for create/update.
type ApplicationInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// EntryPointInput is the request body for entry point create/update.
type EntryPointInput struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	EPType           string `json:"ep_type"`
	OrchestratorName string `json:"orchestrator_name"`
	Enabled          *bool  `json:"enabled,omitempty"`
}

// ── Applications handler ──────────────────────────────────────────────────────

// ApplicationsHandler handles /api/v1/admin/applications routes.
type ApplicationsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
}

// NewApplicationsHandler creates an ApplicationsHandler.
func NewApplicationsHandler(db DBQuerier, cache CacheInvalidator) *ApplicationsHandler {
	return &ApplicationsHandler{db: db, cache: cache}
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
	const q = `
		SELECT id, name, slug, COALESCE(description, ''), enabled
		FROM them.applications ORDER BY id`

	rows, err := h.db.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	apps := make([]Application, 0)
	for rows.Next() {
		var a Application
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		apps = append(apps, a)
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
// publishes a per-slug invalidation message for each. Called when an application-
// level change (update, delete) may affect all of its entry points.
func (h *ApplicationsHandler) invalidateAppEPs(r *http.Request, appID int64) {
	if h.cache == nil {
		return
	}
	const q = `SELECT slug FROM them.entry_points WHERE application_id = $1`
	rows, err := h.db.Query(r.Context(), q, appID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			break
		}
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
	if input.Name == "" || input.Slug == "" {
		writeError(w, http.StatusBadRequest, "name and slug are required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		INSERT INTO them.applications (name, slug, description, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	row := h.db.ExecReturning(r.Context(), q,
		input.Name, input.Slug, input.Description, enabled)

	var id int64
	if err := row.Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "create application: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%d", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	const q = `
		SELECT id, name, slug, COALESCE(description, ''), enabled
		FROM them.applications WHERE id=$1`

	row := h.db.QueryRow(r.Context(), q, id)
	var a Application
	if err := row.Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.Enabled); err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}

	// Load entry points.
	a.EntryPoints = h.loadEntryPoints(r, id)
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
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

	const q = `
		UPDATE them.applications
		SET name=$2, slug=$3, description=$4, enabled=$5, updated_at=now()
		WHERE id=$1`

	if err := h.db.Exec(r.Context(), q, id, input.Name, input.Slug, input.Description, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update application: "+err.Error())
		return
	}

	h.invalidateAppEPs(r, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	const q = `UPDATE them.applications SET enabled=false, updated_at=now() WHERE id=$1`
	if err := h.db.Exec(r.Context(), q, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete application: "+err.Error())
		return
	}

	h.invalidateAppEPs(r, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// CreateEntryPoint handles POST /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) CreateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Slug == "" || input.EPType == "" {
		writeError(w, http.StatusBadRequest, "slug and ep_type are required")
		return
	}
	if !isValidEPType(input.EPType) {
		writeError(w, http.StatusUnprocessableEntity,
			"invalid ep_type: must be one of websocket, sse, voice")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		INSERT INTO them.entry_points (application_id, slug, name, ep_type, orchestrator_name, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	row := h.db.ExecReturning(r.Context(), q,
		appID, input.Slug, input.Name, input.EPType, input.OrchestratorName, enabled)

	var epID int64
	if err := row.Scan(&epID); err != nil {
		writeError(w, http.StatusInternalServerError, "create entry point: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%d/entry-points/%d", appID, epID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": epID})
}

// UpdateEntryPoint handles PUT /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) UpdateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	epID, err := strconv.ParseInt(chi.URLParam(r, "ep_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entry point id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.EPType != "" && !isValidEPType(input.EPType) {
		writeError(w, http.StatusUnprocessableEntity,
			"invalid ep_type: must be one of websocket, sse, voice")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		UPDATE them.entry_points
		SET slug=$3, name=$4, ep_type=$5, orchestrator_name=$6, enabled=$7, updated_at=now()
		WHERE id=$1 AND application_id=$2`

	// Fetch old slug before the update so we can invalidate it too.
	// A slug rename leaves a stale cache entry under the old key without this.
	var oldSlug string
	oldSlugRow := h.db.QueryRow(r.Context(),
		`SELECT slug FROM them.entry_points WHERE id=$1 AND application_id=$2`, epID, appID)
	_ = oldSlugRow.Scan(&oldSlug) // non-fatal if lookup fails

	if err := h.db.Exec(r.Context(), q,
		epID, appID, input.Slug, input.Name, input.EPType, input.OrchestratorName, enabled,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "update entry point: "+err.Error())
		return
	}

	// Invalidate both old slug (may differ) and new slug (from request).
	// When slug is unchanged both calls publish the same value — harmless.
	h.invalidateEP(r, oldSlug)
	h.invalidateEP(r, input.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "updated": true})
}

// DeleteEntryPoint handles DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) DeleteEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	epID, err := strconv.ParseInt(chi.URLParam(r, "ep_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entry point id")
		return
	}

	// Fetch slug before disabling so we can publish the invalidation.
	var epSlug string
	slugRow := h.db.QueryRow(r.Context(),
		`SELECT slug FROM them.entry_points WHERE id=$1 AND application_id=$2`, epID, appID)
	_ = slugRow.Scan(&epSlug) // non-fatal if slug lookup fails

	const q = `UPDATE them.entry_points SET enabled=false, updated_at=now() WHERE id=$1 AND application_id=$2`
	if err := h.db.Exec(r.Context(), q, epID, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete entry point: "+err.Error())
		return
	}

	h.invalidateEP(r, epSlug)
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "deleted": true})
}

// loadEntryPoints returns the entry points for the given application ID.
func (h *ApplicationsHandler) loadEntryPoints(r *http.Request, appID int64) []EntryPoint {
	const q = `
		SELECT id, application_id, slug, name, ep_type,
		       COALESCE(orchestrator_name, ''), enabled
		FROM them.entry_points WHERE application_id=$1 ORDER BY id`

	rows, err := h.db.Query(r.Context(), q, appID)
	if err != nil {
		return make([]EntryPoint, 0)
	}
	defer rows.Close()

	eps := make([]EntryPoint, 0)
	for rows.Next() {
		var ep EntryPoint
		if err := rows.Scan(
			&ep.ID, &ep.ApplicationID, &ep.Slug, &ep.Name,
			&ep.EPType, &ep.OrchestratorName, &ep.Enabled,
		); err != nil {
			break
		}
		eps = append(eps, ep)
	}
	return eps
}
