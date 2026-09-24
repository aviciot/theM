package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// llmOverridePresetEntry is the shape of one entry in a preset's llm_overrides
// map, keyed by canvas node_id — mirrors llmOverrideBody
// (go/internal/admin/appflow_debug.go) plus the at-rest encrypted key field.
// APIKeyEncrypted is never populated from a plaintext APIKey directly by
// json.Unmarshal — Save/toOut translate between the two explicitly so a raw
// key can never be written to, or read back from, the stored JSONB by
// accident.
type llmOverridePresetEntry struct {
	Mode            string `json:"mode"`
	Provider        string `json:"provider,omitempty"`
	KeyID           *int64 `json:"key_id,omitempty"`
	Model           string `json:"model,omitempty"`
	APIKeyEncrypted string `json:"api_key_encrypted,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
}

// AppFlowDebugPresetLLMOverrideIn is one entry of the incoming (plaintext)
// llm_overrides map on Save.
type AppFlowDebugPresetLLMOverrideIn struct {
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
	KeyID    *int64 `json:"key_id,omitempty"`
	Model    string `json:"model,omitempty"`
	APIKey   string `json:"api_key,omitempty"` // plaintext write-only; encrypted before storage
	BaseURL  string `json:"base_url,omitempty"`
}

// AppFlowDebugPresetLLMOverrideOut is one entry of the outgoing (masked)
// llm_overrides map on Get/List.
type AppFlowDebugPresetLLMOverrideOut struct {
	Mode         string  `json:"mode"`
	Provider     string  `json:"provider,omitempty"`
	KeyID        *int64  `json:"key_id,omitempty"`
	Model        string  `json:"model,omitempty"`
	APIKeyMasked *string `json:"api_key_masked,omitempty"`
	BaseURL      string  `json:"base_url,omitempty"`
}

// AppFlowDebugPresetOut is the HTTP response shape.
type AppFlowDebugPresetOut struct {
	ID             string                                      `json:"id"`
	Name           string                                      `json:"name"`
	EntryPointSlug string                                      `json:"entry_point_slug"`
	UserMessage    string                                      `json:"user_message"`
	StepMode       bool                                        `json:"step_mode"`
	LLMOverrides   map[string]AppFlowDebugPresetLLMOverrideOut `json:"llm_overrides"`
	CreatedAt      string                                      `json:"created_at"`
	UpdatedAt      string                                      `json:"updated_at"`
}

// AppFlowDebugPresetIn is the POST/PUT request body.
type AppFlowDebugPresetIn struct {
	Name           string                                     `json:"name"`
	EntryPointSlug string                                     `json:"entry_point_slug"`
	UserMessage    string                                     `json:"user_message"`
	StepMode       bool                                       `json:"step_mode"`
	LLMOverrides   map[string]AppFlowDebugPresetLLMOverrideIn `json:"llm_overrides"`
}

// AppFlowDebugPresetService owns save/load/delete for a user's saved debug
// panel presets (docs/APP_CANVAS_DEBUG_PLAN.md). Presets are personal — scoped
// to (tenant, user, application) — never shared across users, even within the
// same tenant/app (see docs/UNIFIED_ROLE_GOVERNANCE_DESIGN.md for why this is
// deliberately NOT folded into the tenant Role model).
type AppFlowDebugPresetService struct {
	dal       Dal
	fernetKey []byte
}

// NewAppFlowDebugPresetService creates an AppFlowDebugPresetService.
func NewAppFlowDebugPresetService(d Dal, secretKey string) *AppFlowDebugPresetService {
	return &AppFlowDebugPresetService{dal: d, fernetKey: crypto.DeriveKey(secretKey)}
}

// List returns every preset the caller saved for this application.
func (s *AppFlowDebugPresetService) List(ctx context.Context, tenantID string, userID int64, applicationID string) ([]AppFlowDebugPresetOut, error) {
	rows, err := s.dal.ListAppFlowDebugPresets(ctx, tenantID, userID, applicationID)
	if err != nil {
		return nil, err
	}
	out := make([]AppFlowDebugPresetOut, 0, len(rows))
	for _, row := range rows {
		o, err := s.toOut(row)
		if err != nil {
			continue // skip a corrupt row rather than fail the whole list
		}
		out = append(out, o)
	}
	return out, nil
}

// Save creates or replaces (by name) a preset for the caller. Any api_key
// present in body.LLMOverrides is Fernet-encrypted before storage — the
// plaintext value is never written to the DB.
func (s *AppFlowDebugPresetService) Save(ctx context.Context, tenantID string, userID int64, applicationID string, body AppFlowDebugPresetIn) (AppFlowDebugPresetOut, error) {
	if body.Name == "" {
		return AppFlowDebugPresetOut{}, validation("name is required")
	}
	if body.EntryPointSlug == "" {
		return AppFlowDebugPresetOut{}, validation("entry_point_slug is required")
	}

	stored := make(map[string]llmOverridePresetEntry, len(body.LLMOverrides))
	for nodeID, in := range body.LLMOverrides {
		entry := llmOverridePresetEntry{
			Mode: in.Mode, Provider: in.Provider, KeyID: in.KeyID,
			Model: in.Model, BaseURL: in.BaseURL,
		}
		if in.APIKey != "" {
			enc, err := crypto.EncryptStored(s.fernetKey, in.APIKey)
			if err != nil {
				slog.Warn("appflow_debug_presets: failed to encrypt api_key", "error_category", "crypto_encrypt")
				return AppFlowDebugPresetOut{}, unprocessable("failed to encrypt api_key")
			}
			entry.APIKeyEncrypted = enc
		}
		stored[nodeID] = entry
	}

	rawOverrides, err := json.Marshal(stored)
	if err != nil {
		return AppFlowDebugPresetOut{}, unprocessable("invalid llm_overrides")
	}

	row, err := s.dal.UpsertAppFlowDebugPreset(ctx, dal.AppFlowDebugPresetInput{
		TenantID:       tenantID,
		UserID:         userID,
		ApplicationID:  applicationID,
		Name:           body.Name,
		EntryPointSlug: body.EntryPointSlug,
		UserMessage:    body.UserMessage,
		StepMode:       body.StepMode,
		LLMOverrides:   rawOverrides,
	})
	if err != nil {
		return AppFlowDebugPresetOut{}, err
	}
	return s.toOut(row)
}

// Delete removes one preset owned by the caller. Returns ErrNotFound when
// missing or not owned by userID.
func (s *AppFlowDebugPresetService) Delete(ctx context.Context, tenantID string, userID int64, id string) error {
	if err := s.dal.DeleteAppFlowDebugPreset(ctx, id, tenantID, userID); err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *AppFlowDebugPresetService) toOut(row dal.AppFlowDebugPreset) (AppFlowDebugPresetOut, error) {
	var stored map[string]llmOverridePresetEntry
	if len(row.LLMOverrides) > 0 {
		if err := json.Unmarshal(row.LLMOverrides, &stored); err != nil {
			return AppFlowDebugPresetOut{}, err
		}
	}

	overrides := make(map[string]AppFlowDebugPresetLLMOverrideOut, len(stored))
	for nodeID, entry := range stored {
		out := AppFlowDebugPresetLLMOverrideOut{
			Mode: entry.Mode, Provider: entry.Provider, KeyID: entry.KeyID,
			Model: entry.Model, BaseURL: entry.BaseURL,
		}
		if entry.APIKeyEncrypted != "" {
			hint := keyHintFor(s.fernetKey, entry.APIKeyEncrypted)
			out.APIKeyMasked = &hint
		}
		overrides[nodeID] = out
	}

	return AppFlowDebugPresetOut{
		ID:             row.ID,
		Name:           row.Name,
		EntryPointSlug: row.EntryPointSlug,
		UserMessage:    row.UserMessage,
		StepMode:       row.StepMode,
		LLMOverrides:   overrides,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}
