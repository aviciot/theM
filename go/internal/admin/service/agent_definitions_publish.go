package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/agentgen"
)

// AgentCompileError is returned by ValidateAgentDefinition and PublishAgentDefinition
// when compile-time errors are found. The handler maps this to 422 Unprocessable Entity.
type AgentCompileError struct {
	Errors []agentgen.Issue `json:"errors"`
}

func (e *AgentCompileError) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, ce := range e.Errors {
		msgs[i] = ce.Error()
	}
	return "agent definition compile failed: " + strings.Join(msgs, "; ")
}

// AgentValidationReport is returned to the caller after validation.
// Valid is true even when warnings are present — only errors block.
type AgentValidationReport struct {
	Valid  bool             `json:"valid"`
	Issues []agentgen.Issue `json:"issues,omitempty"`
}

// AgentPublishResult is returned after a successful publish.
type AgentPublishResult struct {
	AgentID      string `json:"agent_id"`
	DefinitionID string `json:"definition_id"`
	Revision     int    `json:"revision"`
	SpecHash     string `json:"spec_hash"`
}

// ValidateAgentDefinition compiles the definition without persisting anything.
// When rawDefinition is non-nil it is validated directly (live canvas state from
// the frontend). When nil the saved DB definition is loaded. The agent_slug and
// tenant-ownership check always require a DB read regardless of which path is taken.
// Returns AgentValidationReport on success, *AgentCompileError on compile failure.
func (s *AgentDefinitionService) ValidateAgentDefinition(ctx context.Context, tenantID, id string, rawDefinition json.RawMessage) (*AgentValidationReport, error) {
	// Always load the DB row to verify tenant ownership and get the agent slug.
	def, err := s.dal.GetAgentDefinitionForPublish(ctx, tenantID, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Use the caller-supplied definition when provided; fall back to the saved one.
	definitionToValidate := def.Definition
	if len(rawDefinition) > 0 {
		definitionToValidate = rawDefinition
	}

	_, issues := agentgen.Validate(
		"00000000-0000-0000-0000-000000000000", // placeholder — validate only
		tenantID,
		id,
		def.AgentSlug,
		definitionToValidate,
	)
	// Only hard errors fail validation; warnings are surfaced to the canvas.
	var errors []agentgen.Issue
	for _, iss := range issues {
		if iss.Severity == "error" {
			errors = append(errors, iss)
		}
	}
	if len(errors) > 0 {
		return nil, &AgentCompileError{Errors: errors}
	}
	return &AgentValidationReport{Valid: true, Issues: issues}, nil
}

// PublishAgentDefinition compiles the definition, atomically writes the three
// runtime tables, and marks the definition as published.
func (s *AgentDefinitionService) PublishAgentDefinition(ctx context.Context, tenantID, id string) (*AgentPublishResult, error) {
	def, err := s.dal.GetAgentDefinitionForPublish(ctx, tenantID, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Shared UUID: the agent's ID in the runtime tables equals the definition ID.
	agentID := id

	spec, issues := agentgen.CompileForPublish(agentID, tenantID, id, def.AgentSlug, def.Definition)
	if len(issues) > 0 {
		return nil, &AgentCompileError{Errors: issues}
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	credSchema, err := buildCredentialSchema(spec.CredentialSlots)
	if err != nil {
		return nil, fmt.Errorf("build cred schema: %w", err)
	}

	agentCardJSON, err := buildAgentCard(spec)
	if err != nil {
		return nil, fmt.Errorf("build agent card: %w", err)
	}

	skillsJSON, err := buildSkillsArray(spec)
	if err != nil {
		return nil, fmt.Errorf("build skills: %w", err)
	}

	h := sha256.Sum256(specJSON)
	specHash := fmt.Sprintf("%x", h)

	row := dal.CanvasAgentRow{
		AgentID:       agentID,
		TenantID:      tenantID,
		DefinitionID:  id,
		AgentSlug:     spec.Slug,
		DisplayName:   spec.Card.Name,
		Description:   spec.Card.Description,
		Version:       def.Revision,
		ContentHash:   def.DefinitionHash,
		SpecJSON:      specJSON,
		SpecHash:      specHash,
		AgentCardJSON: agentCardJSON,
		SkillsJSON:    skillsJSON,
		CredSchema:    credSchema,
	}

	if err := s.dal.PublishCanvasAgent(ctx, row); err != nil {
		if dal.IsUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}

	if err := s.dal.MarkAgentDefinitionPublished(ctx, tenantID, id); err != nil && !dal.IsNoRows(err) {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Publish(ctx, "them:agents:registry:"+tenantID, agentID)
	}

	return &AgentPublishResult{
		AgentID:      agentID,
		DefinitionID: id,
		Revision:     def.Revision,
		SpecHash:     specHash,
	}, nil
}

// ── Binding service ───────────────────────────────────────────────────────────

// AgentBindingUpsertInput is the request body for POST/PUT binding endpoints.
type AgentBindingUpsertInput struct {
	DefinitionID    *string           `json:"definition_id,omitempty"`
	Credentials     map[string]string `json:"credentials"` // slot_name → plaintext
	ConfigOverrides map[string]any    `json:"config_overrides,omitempty"`
	Policies        map[string]any    `json:"policies,omitempty"`
}

// ErrEncryptionKeyMissing is returned when a credential is provided but no
// encryption key is configured.
var ErrEncryptionKeyMissing = errors.New("encryption key not configured")

// UpsertBinding encrypts credentials with AES-GCM and upserts the binding row.
func (s *AgentDefinitionService) UpsertBinding(ctx context.Context, applicationID, agentID string, input AgentBindingUpsertInput) error {
	encrypted := make(map[string]string, len(input.Credentials))
	for slot, plaintext := range input.Credentials {
		if plaintext == "" {
			continue
		}
		if len(s.fernetKey) == 0 {
			return ErrEncryptionKeyMissing
		}
		ct, err := encryptAESGCM(s.fernetKey, []byte(plaintext))
		if err != nil {
			return fmt.Errorf("encrypt slot %q: %w", slot, err)
		}
		encrypted[slot] = ct
	}

	credJSON, err := json.Marshal(encrypted)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}

	cfgJSON, err := json.Marshal(input.ConfigOverrides)
	if err != nil {
		return fmt.Errorf("marshal config_overrides: %w", err)
	}

	polJSON, err := json.Marshal(input.Policies)
	if err != nil {
		return fmt.Errorf("marshal policies: %w", err)
	}

	return s.dal.UpsertAgentBinding(ctx, dal.AgentBindingRow{
		ApplicationID:          applicationID,
		AgentID:                agentID,
		DefinitionID:           input.DefinitionID,
		CredentialBindingsJSON: credJSON,
		ConfigOverridesJSON:    cfgJSON,
		PoliciesJSON:           polJSON,
	})
}

// GetBindingStatus returns the slot-set status for one binding (no plaintext).
func (s *AgentDefinitionService) GetBindingStatus(ctx context.Context, applicationID, agentID string) (dal.AgentBindingSlotStatus, error) {
	status, err := s.dal.GetAgentBindingStatus(ctx, applicationID, agentID)
	if err != nil {
		if dal.IsNoRows(err) {
			return dal.AgentBindingSlotStatus{}, ErrNotFound
		}
		return dal.AgentBindingSlotStatus{}, err
	}
	return status, nil
}

// ListBindings returns all bindings for an application (slot-set status only).
func (s *AgentDefinitionService) ListBindings(ctx context.Context, applicationID string) ([]dal.AgentBindingSlotStatus, error) {
	bindings, err := s.dal.ListAgentBindings(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if bindings == nil {
		bindings = []dal.AgentBindingSlotStatus{}
	}
	return bindings, nil
}

// DeleteBinding removes an app↔agent binding.
func (s *AgentDefinitionService) DeleteBinding(ctx context.Context, applicationID, agentID string) error {
	err := s.dal.DeleteAgentBinding(ctx, applicationID, agentID)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ── AES-GCM encryption ────────────────────────────────────────────────────────

// encryptAESGCM encrypts plaintext with a 32-byte key using AES-256-GCM.
// Output: base64url(nonce || ciphertext || gcm-tag).
// Credential values are NEVER logged or returned in API responses.
func encryptAESGCM(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.URLEncoding.EncodeToString(sealed), nil
}

// DecryptAESGCM decrypts a value produced by encryptAESGCM. Exported for use
// by the runtime layer when resolving credentials at invocation time.
func DecryptAESGCM(key []byte, encoded string) (string, error) {
	data, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}

// ── Build helpers ─────────────────────────────────────────────────────────────

func buildCredentialSchema(slots []agentgen.CredentialSlotSpec) (json.RawMessage, error) {
	type credEntry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Required    bool   `json:"required"`
	}
	entries := make([]credEntry, 0, len(slots))
	for _, s := range slots {
		entries = append(entries, credEntry{Name: s.Name, Description: s.Description, Required: s.Required})
	}
	return json.Marshal(entries)
}

func buildAgentCard(spec *agentgen.AgentSpec) ([]byte, error) {
	type agentCardOut struct {
		Name         string                    `json:"name"`
		Description  string                    `json:"description"`
		Version      string                    `json:"version"`
		Icon         string                    `json:"icon,omitempty"`
		Category     string                    `json:"category,omitempty"`
		Capabilities agentgen.CapabilitiesSpec `json:"capabilities"`
		Skills       []map[string]any          `json:"skills"`
	}
	skillMaps := make([]map[string]any, 0, len(spec.Skills))
	for _, sk := range spec.Skills {
		skillMaps = append(skillMaps, map[string]any{
			"id":          sk.ID,
			"name":        sk.Name,
			"description": sk.Description,
			"tags":        sk.Tags,
			"inputModes":  sk.InputModes,
			"outputModes": sk.OutputModes,
		})
	}
	card := agentCardOut{
		Name:         spec.Card.Name,
		Description:  spec.Card.Description,
		Version:      spec.Card.Version,
		Icon:         spec.Card.Icon,
		Category:     spec.Card.Category,
		Capabilities: spec.Card.Capabilities,
		Skills:       skillMaps,
	}
	return json.Marshal(card)
}

func buildSkillsArray(spec *agentgen.AgentSpec) ([]byte, error) {
	type skillRow struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	rows := make([]skillRow, 0, len(spec.Skills))
	for _, sk := range spec.Skills {
		rows = append(rows, skillRow{ID: sk.ID, Name: sk.Name, Description: sk.Description, Tags: sk.Tags})
	}
	return json.Marshal(rows)
}
