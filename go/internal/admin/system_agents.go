package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

const systemAgentsConfigKey = "system_agents"

// ── On-disk config shape ──────────────────────────────────────────────────────

type saRoleStored struct {
	Enabled         bool    `json:"enabled"`
	Provider        *string `json:"provider"`
	Model           *string `json:"model"`
	BaseURL         *string `json:"base_url"`
	SystemPrompt    *string `json:"system_prompt"`
	APIKeyEncrypted *string `json:"api_key_encrypted"`
}

type saConfigStored struct {
	Roles map[string]saRoleStored `json:"roles"`
}

// ── API shapes ────────────────────────────────────────────────────────────────

type SystemAgentRoleOut struct {
	Enabled      bool    `json:"enabled"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	BaseURL      *string `json:"base_url"`
	SystemPrompt *string `json:"system_prompt"`
	APIKeyHint   *string `json:"api_key_hint"` // masked, never plaintext
}

type SystemAgentsOut struct {
	Roles map[string]SystemAgentRoleOut `json:"roles"`
}

type SystemAgentRoleIn struct {
	Enabled      *bool   `json:"enabled"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	BaseURL      *string `json:"base_url"`
	SystemPrompt *string `json:"system_prompt"`
	APIKey       *string `json:"api_key"` // plaintext write-only; nil/blank = keep existing
}

type SystemAgentsIn struct {
	Roles map[string]SystemAgentRoleIn `json:"roles"`
}

// ── Handler ───────────────────────────────────────────────────────────────────

type SystemAgentsHandler struct {
	dal       *dal.DB
	fernetKey []byte
}

func NewSystemAgentsHandler(db DBQuerier, fernetKey []byte) *SystemAgentsHandler {
	return &SystemAgentsHandler{dal: dal.NewDB(db), fernetKey: fernetKey}
}

func (h *SystemAgentsHandler) Routes(r chi.Router) {
	r.Get("/system-agents", h.Get)
	r.Put("/system-agents", h.Put)
	r.Post("/system-agents/{role}/test-llm", h.TestLLM)
}

// ── GET /admin/system-agents ──────────────────────────────────────────────────

func (h *SystemAgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	writeJSON(w, http.StatusOK, h.configToOut(cfg))
}

// ── PUT /admin/system-agents ──────────────────────────────────────────────────

func (h *SystemAgentsHandler) Put(w http.ResponseWriter, r *http.Request) {
	var body SystemAgentsIn
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	if cfg.Roles == nil {
		cfg.Roles = make(map[string]saRoleStored)
	}

	for roleName, incoming := range body.Roles {
		existing := cfg.Roles[roleName]

		if incoming.Enabled != nil {
			existing.Enabled = *incoming.Enabled
		}
		if incoming.Provider != nil {
			s := *incoming.Provider
			if s == "" {
				existing.Provider = nil
			} else {
				existing.Provider = &s
			}
		}
		if incoming.Model != nil {
			s := *incoming.Model
			if s == "" {
				existing.Model = nil
			} else {
				existing.Model = &s
			}
		}
		if incoming.BaseURL != nil {
			s := *incoming.BaseURL
			if s == "" {
				existing.BaseURL = nil
			} else {
				existing.BaseURL = &s
			}
		}
		if incoming.SystemPrompt != nil {
			s := *incoming.SystemPrompt
			if s == "" {
				existing.SystemPrompt = nil
			} else {
				existing.SystemPrompt = &s
			}
		}
		if incoming.APIKey != nil && *incoming.APIKey != "" {
			enc, err := crypto.EncryptStored(h.fernetKey, *incoming.APIKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to encrypt API key")
				return
			}
			existing.APIKeyEncrypted = &enc
		}

		cfg.Roles[roleName] = existing
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialise config")
		return
	}
	if err := h.dal.UpsertConfig(r.Context(), systemAgentsConfigKey, raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}

	writeJSON(w, http.StatusOK, h.configToOut(cfg))
}

// ── POST /admin/system-agents/{role}/test-llm ─────────────────────────────────

func (h *SystemAgentsHandler) TestLLM(w http.ResponseWriter, r *http.Request) {
	role := chi.URLParam(r, "role")

	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Load stored config for this role to fill gaps.
	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	stored := cfg.Roles[role]

	provider := body.Provider
	if provider == "" && stored.Provider != nil {
		provider = *stored.Provider
	}
	model := body.Model
	if model == "" && stored.Model != nil {
		model = *stored.Model
	}
	baseURL := body.BaseURL
	if baseURL == "" && stored.BaseURL != nil {
		baseURL = *stored.BaseURL
	}

	apiKey := body.APIKey
	if apiKey == "" && stored.APIKeyEncrypted != nil {
		decrypted, err := crypto.DecryptStored(h.fernetKey, *stored.APIKeyEncrypted)
		if err == nil {
			apiKey = decrypted
		}
	}

	if provider == "" || model == "" {
		writeError(w, http.StatusBadRequest, "provider and model are required")
		return
	}
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "no API key provided or stored for this role")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ok, testErr := probeLLMWithBase(ctx, provider, model, apiKey, baseURL)
	resp := map[string]any{"ok": ok}
	if !ok {
		resp["error"] = testErr
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *SystemAgentsHandler) loadConfig(ctx context.Context) (saConfigStored, error) {
	row, err := h.dal.GetConfig(ctx, systemAgentsConfigKey)
	if err != nil {
		return saConfigStored{}, err
	}
	if row == nil {
		return saConfigStored{Roles: map[string]saRoleStored{
			"classifier":       {Enabled: false},
			"card_synthesizer": {Enabled: false},
		}}, nil
	}
	var cfg saConfigStored
	if err := json.Unmarshal(row.Value, &cfg); err != nil {
		return saConfigStored{}, err
	}
	if cfg.Roles == nil {
		cfg.Roles = make(map[string]saRoleStored)
	}
	return cfg, nil
}

func (h *SystemAgentsHandler) configToOut(cfg saConfigStored) SystemAgentsOut {
	out := SystemAgentsOut{Roles: make(map[string]SystemAgentRoleOut, len(cfg.Roles))}
	for name, r := range cfg.Roles {
		role := SystemAgentRoleOut{
			Enabled:      r.Enabled,
			Provider:     r.Provider,
			Model:        r.Model,
			BaseURL:      r.BaseURL,
			SystemPrompt: r.SystemPrompt,
		}
		if r.APIKeyEncrypted != nil && *r.APIKeyEncrypted != "" {
			hint := keyHint(h.fernetKey, *r.APIKeyEncrypted)
			role.APIKeyHint = &hint
		}
		out.Roles[name] = role
	}
	return out
}

// keyHint decrypts the stored key and returns a masked representation:
// first 4 chars + 8 bullets + last 4 chars. Returns "" on any error.
func keyHint(fernetKey []byte, encrypted string) string {
	plain, err := crypto.DecryptStored(fernetKey, encrypted)
	if err != nil || len(plain) < 8 {
		return ""
	}
	return plain[:4] + strings.Repeat("•", 8) + plain[len(plain)-4:]
}
