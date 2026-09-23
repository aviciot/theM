package service

import (
	"context"
	"log/slog"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// LLMProviderKeyOut is the HTTP response shape for a named provider key.
// The plaintext secret is never returned — only a masked hint.
type LLMProviderKeyOut struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Masked       string  `json:"masked"`
	IsDefault    bool    `json:"is_default"`
	LastTestOK   *bool   `json:"last_test_ok"`
	LastTestedAt *string `json:"last_tested_at"`
}

// LLMProviderKeyCreate is the request body for creating a named key.
type LLMProviderKeyCreate struct {
	Name      string `json:"name"`
	APIKey    string `json:"api_key"`
	IsDefault bool   `json:"is_default"`
}

// LLMProviderKeyPatch is the request body for renaming/rotating a named key.
// Nil pointer = field absent (leave unchanged).
type LLMProviderKeyPatch struct {
	Name   *string `json:"name"`
	APIKey *string `json:"api_key"` // non-nil + non-empty = rotate; absent = keep current secret
}

// LLMProviderKeyService owns the business logic for named LLM provider keys.
type LLMProviderKeyService struct {
	dal       Dal
	fernetKey []byte
}

// NewLLMProviderKeyService creates an LLMProviderKeyService.
func NewLLMProviderKeyService(d Dal, secretKey string) *LLMProviderKeyService {
	return &LLMProviderKeyService{
		dal:       d,
		fernetKey: crypto.DeriveKey(secretKey),
	}
}

// List returns all named keys for a provider, scoped to tenantID, masked.
func (s *LLMProviderKeyService) List(ctx context.Context, llmProviderID int64, tenantID string) ([]LLMProviderKeyOut, error) {
	rows, err := s.dal.ListLLMProviderKeys(ctx, llmProviderID, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]LLMProviderKeyOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toOut(r))
	}
	return out, nil
}

// Create validates, encrypts the secret, and persists a new named key.
// Returns ErrValidation for missing required fields and ErrConflict for a duplicate name.
// When IsDefault is true, any existing default key for this provider+tenant is cleared first.
func (s *LLMProviderKeyService) Create(ctx context.Context, llmProviderID int64, tenantID string, body LLMProviderKeyCreate) (LLMProviderKeyOut, error) {
	if body.Name == "" {
		return LLMProviderKeyOut{}, validation("name is required")
	}
	if body.APIKey == "" {
		return LLMProviderKeyOut{}, validation("api_key is required")
	}

	enc, err := crypto.EncryptStored(s.fernetKey, body.APIKey)
	if err != nil {
		slog.Warn("llm_provider_keys: failed to encrypt api_key on create", "error_category", "crypto_encrypt")
		return LLMProviderKeyOut{}, unprocessable("failed to encrypt api_key")
	}

	if body.IsDefault {
		if err := s.dal.ClearDefaultLLMProviderKeys(ctx, llmProviderID, tenantID); err != nil {
			return LLMProviderKeyOut{}, err
		}
	}

	row, err := s.dal.CreateLLMProviderKey(ctx, dal.LLMProviderKeyInput{
		LLMProviderID:   llmProviderID,
		TenantID:        tenantID,
		Name:            body.Name,
		APIKeyEncrypted: enc,
		IsDefault:       body.IsDefault,
	})
	if err != nil {
		if dal.IsUniqueViolation(err) {
			return LLMProviderKeyOut{}, ErrConflict
		}
		return LLMProviderKeyOut{}, err
	}
	return s.toOut(row), nil
}

// Update applies a PATCH (rename and/or rotate secret) using fetch-then-modify semantics.
// Returns ErrNotFound when the key does not exist or is not owned by tenantID.
func (s *LLMProviderKeyService) Update(ctx context.Context, id int64, tenantID string, patch LLMProviderKeyPatch) (LLMProviderKeyOut, error) {
	row, err := s.dal.GetLLMProviderKey(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderKeyOut{}, ErrNotFound
		}
		return LLMProviderKeyOut{}, err
	}

	if patch.Name != nil {
		if *patch.Name == "" {
			return LLMProviderKeyOut{}, validation("name cannot be empty")
		}
		row.Name = *patch.Name
	}
	if patch.APIKey != nil && *patch.APIKey != "" {
		enc, err := crypto.EncryptStored(s.fernetKey, *patch.APIKey)
		if err != nil {
			slog.Warn("llm_provider_keys: failed to encrypt api_key on update",
				"key_id", id, "error_category", "crypto_encrypt")
			return LLMProviderKeyOut{}, unprocessable("failed to encrypt api_key")
		}
		row.APIKeyEncrypted = enc
	}

	updated, err := s.dal.UpdateLLMProviderKey(ctx, id, tenantID, dal.LLMProviderKeyInput{
		LLMProviderID:   row.LLMProviderID,
		TenantID:        tenantID,
		Name:            row.Name,
		APIKeyEncrypted: row.APIKeyEncrypted,
		IsDefault:       row.IsDefault,
	})
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderKeyOut{}, ErrNotFound
		}
		if dal.IsUniqueViolation(err) {
			return LLMProviderKeyOut{}, ErrConflict
		}
		return LLMProviderKeyOut{}, err
	}
	return s.toOut(updated), nil
}

// SetDefault marks the given key as the default for its provider+tenant.
// Returns ErrNotFound when the key does not exist or is not owned by tenantID.
func (s *LLMProviderKeyService) SetDefault(ctx context.Context, id int64, tenantID string) (LLMProviderKeyOut, error) {
	row, err := s.dal.SetDefaultLLMProviderKey(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderKeyOut{}, ErrNotFound
		}
		return LLMProviderKeyOut{}, err
	}
	return s.toOut(row), nil
}

// Delete hard-deletes a named key. Returns ErrNotFound when it does not exist
// or is not owned by tenantID.
func (s *LLMProviderKeyService) Delete(ctx context.Context, id int64, tenantID string) error {
	err := s.dal.DeleteLLMProviderKey(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ResolveDecrypted returns the decrypted secret for a named key, scoped to tenantID.
// Returns ErrNotFound when the key does not exist or is not owned by tenantID.
func (s *LLMProviderKeyService) ResolveDecrypted(ctx context.Context, id int64, tenantID string) (string, error) {
	row, err := s.dal.GetLLMProviderKey(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	plain, err := crypto.DecryptStored(s.fernetKey, row.APIKeyEncrypted)
	if err != nil {
		return "", unprocessable("stored api_key could not be decrypted")
	}
	return plain, nil
}

// RecordTestResult stores the outcome of a Test-key probe call.
func (s *LLMProviderKeyService) RecordTestResult(ctx context.Context, id int64, tenantID string, ok bool) error {
	return s.dal.SetLLMProviderKeyTestResult(ctx, id, tenantID, ok)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// toOut converts a DAL row to the HTTP response shape, decrypting the secret
// only to produce a masked representation. Plaintext bytes are never returned.
func (s *LLMProviderKeyService) toOut(row dal.LLMProviderKey) LLMProviderKeyOut {
	masked := "****"
	if plain, err := crypto.DecryptStored(s.fernetKey, row.APIKeyEncrypted); err == nil {
		n := len(plain)
		if n > 8 {
			masked = plain[:4] + "..." + plain[n-4:]
		}
	} else {
		slog.Warn("llm_provider_keys: api_key decrypt failed for mask", "key_id", row.ID, "error_category", "crypto_decrypt")
	}
	return LLMProviderKeyOut{
		ID:           row.ID,
		Name:         row.Name,
		Masked:       masked,
		IsDefault:    row.IsDefault,
		LastTestOK:   row.LastTestOK,
		LastTestedAt: row.LastTestedAt,
	}
}
