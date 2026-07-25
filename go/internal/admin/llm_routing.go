package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// LLMRoutingHandler handles /admin/llm-providers/routing/config routes.
type LLMRoutingHandler struct {
	svc *service.ConfigService
}

// NewLLMRoutingHandler creates an LLMRoutingHandler.
func NewLLMRoutingHandler(db DBQuerier) *LLMRoutingHandler {
	return &LLMRoutingHandler{svc: service.NewConfigService(dal.NewDB(db))}
}

// Routes mounts GET and PUT for llm-providers/routing/config.
// The literal sub-path /routing/config must be registered before any /{id}
// wildcard to avoid route conflicts in chi.
func (h *LLMRoutingHandler) Routes(r chi.Router) {
	r.Get("/llm-providers/routing/config", h.Get)
	r.Put("/llm-providers/routing/config", h.Put)
}

// Get handles GET /api/v1/admin/llm-providers/routing/config.
func (h *LLMRoutingHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetLLMRouting(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// Put handles PUT /api/v1/admin/llm-providers/routing/config.
func (h *LLMRoutingHandler) Put(w http.ResponseWriter, r *http.Request) {
	var body service.LLMRoutingConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	out, err := h.svc.PutLLMRouting(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
