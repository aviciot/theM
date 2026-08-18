// Package service contains the admin business logic layer.
// It sits between the thin HTTP handlers (internal/admin/) and the data access
// layer (internal/admin/dal/). Handlers parse HTTP; service validates, applies
// defaults, and enforces business rules; DAL executes SQL.
//
// Import rule: service imports dal (for shared struct types and the Dal
// interface). Service must NOT import net/http, chi, or pgx.
package service

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
)

// Dal is the data-access surface the admin services depend on.
// The concrete *dal.DB satisfies this interface structurally; the service
// package never imports pgx and never sees SQL.
type Dal interface {
	// Agents — tenant-scoped
	ListAgents(ctx context.Context, tenantID string) ([]dal.Agent, error)
	GetAgent(ctx context.Context, tenantID, id string) (dal.Agent, error)
	CreateAgent(ctx context.Context, tenantID string, in dal.AgentInput, enabled bool) (string, error)
	UpdateAgent(ctx context.Context, tenantID, id string, in dal.AgentInput, enabled bool) error
	DeleteAgent(ctx context.Context, tenantID, id string) error
	// Agent actions — platform-global (no tenant scope)
	GetAgentBySlug(ctx context.Context, slug string) (dal.Agent, error)
	UpdateAgentScanResult(ctx context.Context, agentID string, result []byte) error
	GetAgentByID(ctx context.Context, id string) (dal.Agent, error)
	GetAgentTokenEncrypted(ctx context.Context, id string) (string, error)

	// Orchestrators — tenant-scoped
	ListOrchestrators(ctx context.Context, tenantID string) ([]dal.Orchestrator, error)
	GetOrchestrator(ctx context.Context, tenantID, name string) (dal.Orchestrator, error)
	CreateOrchestrator(ctx context.Context, tenantID string, in dal.OrchestratorInput, enabled bool) (string, error)
	UpdateOrchestrator(ctx context.Context, tenantID, name string, in dal.OrchestratorInput, enabled bool) error
	DeleteOrchestrator(ctx context.Context, tenantID, name string) error

	// Applications + entry points — tenant-scoped (apps); entry points scoped through app
	ListApplications(ctx context.Context, tenantID string) ([]dal.Application, error)
	GetApplication(ctx context.Context, tenantID, id string) (dal.Application, error)
	CreateApplication(ctx context.Context, tenantID, name string, enabled bool) (string, error)
	UpdateApplication(ctx context.Context, tenantID, id, name string, enabled bool) error
	DeleteApplication(ctx context.Context, tenantID, id string) error
	ListEntryPoints(ctx context.Context, appID string) []dal.EntryPoint
	CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled bool) (string, error)
	GetEntryPointSlug(ctx context.Context, epID, appID string) (string, error)
	GetEntryPointTenantAndSlug(ctx context.Context, epID, appID string) dal.EPTenantSlug
	UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled bool) error
	DeleteEntryPoint(ctx context.Context, epID, appID string) error
	ListEPSlugsForApp(ctx context.Context, appID string) []string
	ListEPTenantSlugsForApp(ctx context.Context, appID string) []dal.EPTenantSlug
	// Runtime config + bulk delete
	UpdateRuntimeConfig(ctx context.Context, tenantID, appID string, configJSON []byte) error
	ListAppOrchestratorNames(ctx context.Context, appID string) ([]string, error)
	BulkDeleteApplications(ctx context.Context, tenantID string, ids []string) (int64, error)
	// Provider keys
	GetProviderKeys(ctx context.Context, tenantID, appID string) ([]byte, error)
	SetProviderKey(ctx context.Context, tenantID, appID, provider string, encryptedKey []byte) error
	DeleteProviderKey(ctx context.Context, tenantID, appID, provider string) error

	// Runs — tenant-scoped
	ListRuns(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Run, error)
	GetRun(ctx context.Context, tenantID, runID string) (dal.Run, error)
	GetRunContextID(ctx context.Context, tenantID, runID string) (string, error)
	GetRunStats(ctx context.Context, tenantID string) (dal.RunStats, error)
	GetRunDetail(ctx context.Context, tenantID, runID string) (dal.RunDetail, error)
	GetRunTasks(ctx context.Context, runID string) ([]dal.Task, error)
	GetRunArtifacts(ctx context.Context, runID string) ([]dal.Artifact, error)
	ListContextSessions(ctx context.Context, tenantID, orchestrator string, limit int) ([]dal.ContextSession, error)
	GetContextArtifacts(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Artifact, error)
	GetContextMessages(ctx context.Context, contextID string, limit int) ([]dal.ContextMessage, error)
	CancelRun(ctx context.Context, tenantID, runID string) (dal.Run, error)
	DeleteRun(ctx context.Context, tenantID, runID string) error
	BulkDeleteRuns(ctx context.Context, tenantID string, runIDs []string) (int64, error)

	// Tokens — tenant-scoped
	ListTokens(ctx context.Context, tenantID string, userID *int64) ([]dal.Token, error)
	GetToken(ctx context.Context, tenantID, id string) (dal.Token, error)
	OrchestratorExists(ctx context.Context, tenantID, orchID string) (bool, error)
	CreateToken(ctx context.Context, tenantID string, in dal.TokenCreateRow) (dal.Token, error)
	UpdateToken(ctx context.Context, tenantID, id string, patch dal.TokenPatchRow) (hash string, out dal.Token, err error)
	DeleteToken(ctx context.Context, tenantID, id string) (hash string, err error)

	// Application definitions — tenant+app scoped
	GetNextRevision(ctx context.Context, appID string) (int, error)
	CreateDefinition(ctx context.Context, tenantID, appID string, rev int, defJSON []byte, hash string) (string, error)
	GetDefinition(ctx context.Context, tenantID, appID, defID string) (dal.AppDefinition, error)
	ListDefinitions(ctx context.Context, tenantID, appID string) ([]dal.AppDefinition, error)
	UpdateDraftDefinition(ctx context.Context, tenantID, appID, defID string, defJSON []byte, hash string) error
	DeleteDraftDefinition(ctx context.Context, tenantID, appID, defID string) error

	// Publish pipeline — Phase C
	PublishDefinition(ctx context.Context, tenantID, appID, defID, defHash string) (dal.PublishResult, error)
	UpsertAppOrchestrator(ctx context.Context, row dal.AppOrchestratorRow) (string, error)
	UpsertEntryPoint(ctx context.Context, row dal.EntryPointRow) (string, error)
	DeactivateStaleOrchestrators(ctx context.Context, tenantID, appID, defID string) error
	DeactivateStaleEntryPoints(ctx context.Context, tenantID, appID, defID string) error

	// Component definitions registry — platform-global for builtins, tenant-scoped for tenant-owned
	ListComponentDefinitions(ctx context.Context, tenantID string) ([]dal.ComponentDefinitionSummary, error)

	// Config table (monitoring, llm_routing, …) — platform-global, no tenant
	GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
	UpsertConfig(ctx context.Context, key string, value []byte) error

	// LLM providers — platform-global, no tenant
	ListProviders(ctx context.Context) ([]dal.LLMProvider, error)
	GetProvider(ctx context.Context, id int64) (dal.LLMProvider, error)
	CreateProvider(ctx context.Context, in dal.LLMProviderInput) (dal.LLMProvider, error)
	UpdateProvider(ctx context.Context, id int64, in dal.LLMProviderInput) (dal.LLMProvider, error)
	DeleteProvider(ctx context.Context, id int64) error
}

// Cache invalidates Redis caches on mutations.
// Nil is tolerated by every service method (no-op guard inside each service).
type Cache interface {
	Del(ctx context.Context, key string) error
	Publish(ctx context.Context, channel, message string) error
}

// Temporal sends HITL signals to Temporal workflows.
type Temporal interface {
	SignalRun(ctx context.Context, workflowID string, payload []byte) error
}

// enabledOrDefault returns *b if non-nil, otherwise true.
// All admin resources default to enabled=true on create and update when the
// caller omits the field.
func enabledOrDefault(b *bool) bool {
	if b != nil {
		return *b
	}
	return true
}
