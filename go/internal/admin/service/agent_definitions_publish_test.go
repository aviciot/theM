package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// publishSvc creates a service with a cache fake for publish tests.
func publishSvc(f *agentDefFakeDal) *service.AgentDefinitionService {
	key := make([]byte, 32) // all-zero test key
	return service.NewAgentDefinitionService(f, &fakeCache{}, key)
}

func validPublishDef() dal.AgentDefinition {
	return dal.AgentDefinition{
		ID:             "def-uuid-1",
		TenantID:       "00000000-0000-0000-0000-000000000001",
		AgentSlug:      "my_agent",
		Revision:       1,
		Status:         "draft",
		DefinitionHash: "abc123",
		Definition: []byte(`{
			"agent_root": {
				"display_name": "My Agent",
				"description": "test agent",
				"version": "1.0.0"
			},
			"skills": []
		}`),
	}
}

func TestValidateAgentDefinition_NotFound(t *testing.T) {
	f := &agentDefFakeDal{getAgentDefErr: pgx.ErrNoRows}
	svc := publishSvc(f)
	_, err := svc.ValidateAgentDefinition(context.Background(), "t1", "missing", nil)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateAgentDefinition_CompileError(t *testing.T) {
	def := validPublishDef()
	def.Definition = []byte(`{"agent_root": {}, "skills": []}`) // missing display_name
	f := &agentDefFakeDal{agentDef: def}
	svc := publishSvc(f)
	_, err := svc.ValidateAgentDefinition(context.Background(), "t1", "def-uuid-1", nil)
	var compErr *service.AgentCompileError
	if !errors.As(err, &compErr) {
		t.Errorf("expected *AgentCompileError, got %T: %v", err, err)
	}
}

func TestValidateAgentDefinition_Valid(t *testing.T) {
	f := &agentDefFakeDal{agentDef: validPublishDef()}
	svc := publishSvc(f)
	report, err := svc.ValidateAgentDefinition(context.Background(), "t1", "def-uuid-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Valid {
		t.Error("expected Valid=true")
	}
}

func TestValidateAgentDefinition_InlineDefinitionOverridesDB(t *testing.T) {
	// DB has a valid definition; caller supplies an invalid inline one.
	// The service must validate the inline definition, not the DB copy.
	f := &agentDefFakeDal{agentDef: validPublishDef()}
	svc := publishSvc(f)
	badDef := []byte(`{"agent_root": {}, "skills": []}`) // missing display_name
	_, err := svc.ValidateAgentDefinition(context.Background(), "t1", "def-uuid-1", badDef)
	var compErr *service.AgentCompileError
	if !errors.As(err, &compErr) {
		t.Errorf("expected *AgentCompileError for invalid inline def, got %T: %v", err, err)
	}
}

func TestValidateAgentDefinition_InlineValidDefinition(t *testing.T) {
	// DB has an invalid definition; caller supplies a valid inline one.
	// The service must use the inline definition and return Valid=true.
	badDef := validPublishDef()
	badDef.Definition = []byte(`{"agent_root": {}, "skills": []}`)
	f := &agentDefFakeDal{agentDef: badDef}
	svc := publishSvc(f)
	goodDef := validPublishDef().Definition
	report, err := svc.ValidateAgentDefinition(context.Background(), "t1", "def-uuid-1", goodDef)
	if err != nil {
		t.Fatalf("expected no error for valid inline def, got: %v", err)
	}
	if !report.Valid {
		t.Error("expected Valid=true for valid inline def")
	}
}

func TestPublishAgentDefinition_NotFound(t *testing.T) {
	f := &agentDefFakeDal{getAgentDefErr: pgx.ErrNoRows}
	svc := publishSvc(f)
	_, err := svc.PublishAgentDefinition(context.Background(), "t1", "missing")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPublishAgentDefinition_CompileError(t *testing.T) {
	def := validPublishDef()
	def.Definition = []byte(`{"agent_root": {}, "skills": []}`) // missing display_name
	f := &agentDefFakeDal{agentDef: def}
	svc := publishSvc(f)
	_, err := svc.PublishAgentDefinition(context.Background(), "t1", "def-uuid-1")
	var compErr *service.AgentCompileError
	if !errors.As(err, &compErr) {
		t.Errorf("expected *AgentCompileError, got %T: %v", err, err)
	}
}

func TestPublishAgentDefinition_Success(t *testing.T) {
	f := &agentDefFakeDal{agentDef: validPublishDef()}
	svc := publishSvc(f)
	result, err := svc.PublishAgentDefinition(context.Background(), "t1", "def-uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AgentID == "" {
		t.Error("AgentID must be set")
	}
	if result.SpecHash == "" {
		t.Error("SpecHash must be set")
	}
	if result.Revision != 1 {
		t.Errorf("expected revision=1, got %d", result.Revision)
	}
}

// ── Binding service tests ─────────────────────────────────────────────────────

func TestUpsertBinding_NoKeyWithCredentials(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := service.NewAgentDefinitionService(f, nil, nil) // no key
	err := svc.UpsertBinding(context.Background(), "app-1", "agent-1", service.AgentBindingUpsertInput{
		Credentials: map[string]string{"api_key": "secret-value"},
	})
	if !errors.Is(err, service.ErrEncryptionKeyMissing) {
		t.Errorf("expected ErrEncryptionKeyMissing, got %v", err)
	}
}

func TestUpsertBinding_EmptyCredentials_NoKeyRequired(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := service.NewAgentDefinitionService(f, nil, nil) // no key
	// Empty credentials should succeed even without a key.
	err := svc.UpsertBinding(context.Background(), "app-1", "agent-1", service.AgentBindingUpsertInput{
		Credentials: map[string]string{},
	})
	if err != nil {
		t.Errorf("unexpected error for empty credentials: %v", err)
	}
}

func TestUpsertBinding_WithKey(t *testing.T) {
	f := &agentDefFakeDal{}
	key := make([]byte, 32)
	svc := service.NewAgentDefinitionService(f, nil, key)
	err := svc.UpsertBinding(context.Background(), "app-1", "agent-1", service.AgentBindingUpsertInput{
		Credentials: map[string]string{"api_key": "my-secret"},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetBindingStatus_NotFound(t *testing.T) {
	f2 := &bindingNotFoundFake{}
	svc := service.NewAgentDefinitionService(f2, nil, nil)
	_, err := svc.GetBindingStatus(context.Background(), "app-1", "agent-1")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListBindings_Empty(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := service.NewAgentDefinitionService(f, nil, nil)
	bindings, err := svc.ListBindings(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bindings == nil {
		t.Error("ListBindings must return [] not nil")
	}
}

// bindingNotFoundFake overrides GetAgentBindingStatus to return pgx.ErrNoRows.
type bindingNotFoundFake struct {
	agentDefFakeDal
}

func (f *bindingNotFoundFake) GetAgentBindingStatus(_ context.Context, _, _ string) (dal.AgentBindingSlotStatus, error) {
	return dal.AgentBindingSlotStatus{}, pgx.ErrNoRows
}
