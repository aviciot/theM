package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// GatewayHandler exposes admin CRUD for the LLM Gateway:
// clients, profiles, profile steps, policy settings, and the requests log.
type GatewayHandler struct {
	db DBQuerier
}

// NewGatewayHandler creates a GatewayHandler.
func NewGatewayHandler(db DBQuerier) *GatewayHandler {
	return &GatewayHandler{db: db}
}

// Routes mounts all gateway admin endpoints onto r.
func (h *GatewayHandler) Routes(r chi.Router) {
	// Clients
	r.Get("/gateway/clients", h.ListClients)
	r.Post("/gateway/clients", h.CreateClient)
	r.Get("/gateway/clients/{client_id}", h.GetClient)
	r.Patch("/gateway/clients/{client_id}", h.PatchClient)
	r.Delete("/gateway/clients/{client_id}", h.DeleteClient)

	// Profiles
	r.Get("/gateway/profiles", h.ListProfiles)
	r.Post("/gateway/profiles", h.CreateProfile)
	r.Delete("/gateway/profiles/{profile_id}", h.DeleteProfile)

	// Profile steps
	r.Get("/gateway/profiles/{profile_id}/steps", h.ListProfileSteps)
	r.Post("/gateway/profiles/{profile_id}/steps", h.AddProfileStep)
	r.Delete("/gateway/profile-steps/{step_id}", h.DeleteProfileStep)

	// Policy (one row per tenant)
	r.Get("/gateway/policy", h.GetPolicy)
	r.Put("/gateway/policy", h.PutPolicy)

	// Requests log (read-only)
	r.Get("/gateway/requests", h.ListRequests)
}

// ── Clients ───────────────────────────────────────────────────────────────────

func (h *GatewayHandler) ListClients(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	d := dal.NewDB(h.db)
	clients, err := d.ListGatewayClients(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list gateway clients")
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

type createClientRequest struct {
	Label string `json:"label"`
}

type createClientResponse struct {
	dal.GatewayClient
	Token string `json:"token,omitempty"` // only returned on create
}

func (h *GatewayHandler) CreateClient(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	var body createClientRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	// Generate a random 32-byte bearer token and store its sha256 hash.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])

	d := dal.NewDB(h.db)
	client, err := d.CreateGatewayClient(r.Context(), tenantID, tokenHash, body.Label)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create gateway client")
		return
	}
	writeJSON(w, http.StatusCreated, createClientResponse{GatewayClient: client, Token: token})
}

func (h *GatewayHandler) GetClient(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	clientID := chi.URLParam(r, "client_id")

	d := dal.NewDB(h.db)
	client, err := d.GetGatewayClient(r.Context(), tenantID, clientID)
	if err != nil {
		if dal.IsNoRows(err) {
			writeError(w, http.StatusNotFound, "gateway client not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get gateway client")
		return
	}
	writeJSON(w, http.StatusOK, client)
}

type patchClientRequest struct {
	ProfileID *string `json:"profile_id"` // null clears it
}

func (h *GatewayHandler) PatchClient(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	clientID := chi.URLParam(r, "client_id")

	var body patchClientRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	d := dal.NewDB(h.db)
	if err := d.SetGatewayClientProfile(r.Context(), tenantID, clientID, body.ProfileID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update gateway client")
		return
	}

	client, err := d.GetGatewayClient(r.Context(), tenantID, clientID)
	if err != nil {
		if dal.IsNoRows(err) {
			writeError(w, http.StatusNotFound, "gateway client not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get gateway client")
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (h *GatewayHandler) DeleteClient(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	clientID := chi.URLParam(r, "client_id")

	d := dal.NewDB(h.db)
	if err := d.DeleteGatewayClient(r.Context(), tenantID, clientID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete gateway client")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Profiles ──────────────────────────────────────────────────────────────────

func (h *GatewayHandler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	d := dal.NewDB(h.db)
	profiles, err := d.ListGatewayProfiles(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list gateway profiles")
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

type createProfileRequest struct {
	Name string `json:"name"`
}

func (h *GatewayHandler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	var body createProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	d := dal.NewDB(h.db)
	profile, err := d.CreateGatewayProfile(r.Context(), tenantID, body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create gateway profile")
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (h *GatewayHandler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	profileID := chi.URLParam(r, "profile_id")

	d := dal.NewDB(h.db)
	if err := d.DeleteGatewayProfile(r.Context(), tenantID, profileID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete gateway profile")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Profile steps ─────────────────────────────────────────────────────────────

func (h *GatewayHandler) ListProfileSteps(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profile_id")
	d := dal.NewDB(h.db)
	steps, err := d.ListGatewayProfileSteps(r.Context(), profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list profile steps")
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

type addStepRequest struct {
	DefID    string          `json:"def_id"`
	Position int             `json:"position"`
	Config   json.RawMessage `json:"config"`
}

func (h *GatewayHandler) AddProfileStep(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profile_id")

	var body addStepRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DefID == "" {
		writeError(w, http.StatusBadRequest, "def_id is required")
		return
	}

	d := dal.NewDB(h.db)
	step, err := d.AddGatewayProfileStep(r.Context(), profileID, body.DefID, body.Position, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add profile step")
		return
	}
	writeJSON(w, http.StatusCreated, step)
}

func (h *GatewayHandler) DeleteProfileStep(w http.ResponseWriter, r *http.Request) {
	stepID := chi.URLParam(r, "step_id")
	d := dal.NewDB(h.db)
	if err := d.DeleteGatewayProfileStep(r.Context(), stepID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete profile step")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Policy ────────────────────────────────────────────────────────────────────

func (h *GatewayHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	d := dal.NewDB(h.db)
	policy, err := d.GetGatewayPolicy(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get gateway policy")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

type putPolicyRequest struct {
	AllowedModels       []string        `json:"allowed_models"`
	ModelAliases        json.RawMessage `json:"model_aliases"`
	MaxTokensPerRequest *int            `json:"max_tokens_per_request"`
	MonthlyBudgetUSD    *float64        `json:"monthly_budget_usd"`
}

func (h *GatewayHandler) PutPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	var body putPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	d := dal.NewDB(h.db)
	if err := d.UpsertGatewayPolicy(r.Context(), tenantID, body.AllowedModels, body.ModelAliases, body.MaxTokensPerRequest, body.MonthlyBudgetUSD); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save gateway policy")
		return
	}

	policy, err := d.GetGatewayPolicy(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get gateway policy")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// ── Requests log ──────────────────────────────────────────────────────────────

func (h *GatewayHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}

	d := dal.NewDB(h.db)
	reqs, err := d.ListGatewayRequests(r.Context(), tenantID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list gateway requests")
		return
	}
	writeJSON(w, http.StatusOK, reqs)
}
