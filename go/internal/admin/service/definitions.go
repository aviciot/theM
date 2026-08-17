package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/aviciot/them/internal/admin/dal"
)

// DefinitionService owns the business logic for application definition CRUD.
// It is a standalone service — it does not embed AppService.
type DefinitionService struct {
	dal      Dal
	registry RegistryResolver // nil in CRUD-only mode (tests, Phase B)
}

// NewDefinitionService creates a DefinitionService without a registry resolver.
// Suitable for CRUD operations only (Phase B). Validate/Publish require a resolver.
func NewDefinitionService(d Dal) *DefinitionService {
	return &DefinitionService{dal: d}
}

// NewDefinitionServiceWithRegistry creates a DefinitionService wired with a
// component registry resolver. Required for ValidateDefinition and PublishDefinition.
func NewDefinitionServiceWithRegistry(d Dal, r RegistryResolver) *DefinitionService {
	return &DefinitionService{dal: d, registry: r}
}

// hashDefinition computes a canonical sha256 hash over the definition JSON.
// The raw JSON is re-marshalled through encoding/json to normalize whitespace
// and key ordering before hashing.
func hashDefinition(raw json.RawMessage) (string, error) {
	// Normalize: unmarshal into any and remarshal to canonical form.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// validateDefinition performs minimal structural validation on a raw JSON
// definition object. Returns ErrValidation for malformed inputs and
// ErrUnprocessable for semantic violations (e.g. duplicate instance IDs).
func validateDefinition(raw json.RawMessage) error {
	// Must be a JSON object.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return validation("definition must be a JSON object")
	}
	if top == nil {
		return validation("definition must be a JSON object")
	}

	// Reject any key literally named "secret_value" at any nesting level.
	if err := rejectSecretKeys(raw); err != nil {
		return err
	}

	// Validate "components" if present.
	if compsRaw, ok := top["components"]; ok {
		var comps []json.RawMessage
		if err := json.Unmarshal(compsRaw, &comps); err != nil {
			return validation("components must be a JSON array")
		}
		seen := make(map[string]struct{}, len(comps))
		for i, compRaw := range comps {
			var comp map[string]json.RawMessage
			if err := json.Unmarshal(compRaw, &comp); err != nil {
				return validation("each component must be a JSON object")
			}
			instRaw, ok := comp["instance_id"]
			if !ok {
				return validation("each component must have an instance_id")
			}
			var instID string
			if err := json.Unmarshal(instRaw, &instID); err != nil || instID == "" {
				return validation("component instance_id must be a non-empty string")
			}
			_ = i
			if _, dup := seen[instID]; dup {
				return unprocessable("duplicate instance_id in components: " + instID)
			}
			seen[instID] = struct{}{}
		}
	}

	// Validate "connections" if present.
	if connsRaw, ok := top["connections"]; ok {
		var conns []json.RawMessage
		if err := json.Unmarshal(connsRaw, &conns); err != nil {
			return validation("connections must be a JSON array")
		}
		for _, connRaw := range conns {
			var conn map[string]json.RawMessage
			if err := json.Unmarshal(connRaw, &conn); err != nil {
				return validation("each connection must be a JSON object")
			}
			src, hasSrc := conn["source"]
			tgt, hasTgt := conn["target"]
			if !hasSrc || !hasTgt {
				return validation("each connection must have source and target")
			}
			var srcStr, tgtStr string
			if err := json.Unmarshal(src, &srcStr); err != nil || srcStr == "" {
				return validation("connection source must be a non-empty string")
			}
			if err := json.Unmarshal(tgt, &tgtStr); err != nil || tgtStr == "" {
				return validation("connection target must be a non-empty string")
			}
		}
	}

	// Validate "entry_points" if present.
	if epsRaw, ok := top["entry_points"]; ok {
		var eps []json.RawMessage
		if err := json.Unmarshal(epsRaw, &eps); err != nil {
			return validation("entry_points must be a JSON array")
		}
		for _, epRaw := range eps {
			var ep map[string]json.RawMessage
			if err := json.Unmarshal(epRaw, &ep); err != nil {
				return validation("each entry_point must be a JSON object")
			}
			for _, field := range []string{"instance_id", "slug", "protocol"} {
				fieldRaw, ok := ep[field]
				if !ok {
					return validation("each entry_point must have " + field)
				}
				var s string
				if err := json.Unmarshal(fieldRaw, &s); err != nil || s == "" {
					return validation("entry_point " + field + " must be a non-empty string")
				}
			}
		}
	}

	return nil
}

// rejectSecretKeys scans all keys in a JSON value (recursively) and rejects:
//  1. Any key literally named "secret_value".
//  2. Any string value that starts with "enc:" when the key contains "secret" or "key".
func rejectSecretKeys(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil // already validated as object above; skip
	}
	return walkSecrets(v)
}

func walkSecrets(v any) error {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if k == "secret_value" {
				return validation(`definition must not contain "secret_value" keys`)
			}
			if s, ok := child.(string); ok {
				lk := strings.ToLower(k)
				if strings.HasPrefix(s, "enc:") && (strings.Contains(lk, "secret") || strings.Contains(lk, "key")) {
					return validation(`definition must not contain encrypted secret fields`)
				}
			}
			if err := walkSecrets(child); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range val {
			if err := walkSecrets(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateDraft validates the definition, computes the next revision, hashes,
// and inserts a new draft row. Returns the new definition UUID.
func (s *DefinitionService) CreateDraft(ctx context.Context, tenantID, appID string, defRaw json.RawMessage) (string, int, error) {
	if err := validateDefinition(defRaw); err != nil {
		return "", 0, err
	}

	rev, err := s.dal.GetNextRevision(ctx, appID)
	if err != nil {
		return "", 0, err
	}

	hash, err := hashDefinition(defRaw)
	if err != nil {
		return "", 0, validation("definition is not valid JSON")
	}

	id, err := s.dal.CreateDefinition(ctx, tenantID, appID, rev, []byte(defRaw), hash)
	if err != nil {
		if dal.IsNoRows(err) {
			// Sub-SELECT found no matching application row — tenant mismatch or app not found.
			return "", 0, ErrNotFound
		}
		return "", 0, err
	}
	return id, rev, nil
}

// GetDefinition fetches a definition scoped to tenant + application.
// Returns ErrNotFound on miss or tenant mismatch.
func (s *DefinitionService) GetDefinition(ctx context.Context, tenantID, appID, defID string) (dal.AppDefinition, error) {
	def, err := s.dal.GetDefinition(ctx, tenantID, appID, defID)
	if err != nil {
		if dal.IsNoRows(err) {
			return dal.AppDefinition{}, ErrNotFound
		}
		return dal.AppDefinition{}, err
	}
	return def, nil
}

// ListDefinitions returns all definitions for the tenant + application ordered
// by revision descending. Returns an empty slice (not nil) when there are none.
func (s *DefinitionService) ListDefinitions(ctx context.Context, tenantID, appID string) ([]dal.AppDefinition, error) {
	defs, err := s.dal.ListDefinitions(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	if defs == nil {
		defs = []dal.AppDefinition{}
	}
	return defs, nil
}

// UpdateDraft updates the definition + hash for a draft. Returns ErrNotFound
// if the definition does not exist or belongs to a different tenant/application.
// Returns ErrConflict if the definition exists but is published (not a draft).
func (s *DefinitionService) UpdateDraft(ctx context.Context, tenantID, appID, defID string, defRaw json.RawMessage) error {
	if err := validateDefinition(defRaw); err != nil {
		return err
	}

	hash, err := hashDefinition(defRaw)
	if err != nil {
		return validation("definition is not valid JSON")
	}

	err = s.dal.UpdateDraftDefinition(ctx, tenantID, appID, defID, []byte(defRaw), hash)
	if err == nil {
		return nil
	}
	if !dal.IsNoRows(err) {
		return err
	}

	// 0 rows updated — distinguish "not found" from "not a draft".
	existing, getErr := s.dal.GetDefinition(ctx, tenantID, appID, defID)
	if getErr != nil {
		// Not found at all.
		return ErrNotFound
	}
	if existing.Status == "published" {
		return ErrConflict
	}
	return ErrNotFound
}

// DeleteDraft hard-deletes a draft definition. Returns ErrNotFound if the
// definition does not exist or belongs to a different tenant/application.
// Returns ErrConflict if the definition is published.
func (s *DefinitionService) DeleteDraft(ctx context.Context, tenantID, appID, defID string) error {
	err := s.dal.DeleteDraftDefinition(ctx, tenantID, appID, defID)
	if err == nil {
		return nil
	}
	if !dal.IsNoRows(err) {
		return err
	}

	// 0 rows deleted — distinguish "not found" from "not a draft".
	existing, getErr := s.dal.GetDefinition(ctx, tenantID, appID, defID)
	if getErr != nil {
		return ErrNotFound
	}
	if existing.Status == "published" {
		return ErrConflict
	}
	return ErrNotFound
}
