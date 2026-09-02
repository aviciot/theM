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
	AgentExists(ctx context.Context, id string) (bool, error)
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
	CreateApplication(ctx context.Context, tenantID, name, slug string, enabled bool) (string, error)
	UpdateApplication(ctx context.Context, tenantID, id, name, slug string, enabled bool) error
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
	SetOrchestratorLLM(ctx context.Context, appID, orchID, provider, model string) error
	SetOrchestratorVoice(ctx context.Context, appID, orchID string, in dal.OrchestratorVoiceInput) error
	SetEntryPointSummarizer(ctx context.Context, appID, epID string, enabled bool, everyN, fallbackN int, provider, model *string) error
	SetEntryPointLLM(ctx context.Context, appID, epID string, provider, model *string) error
	// App global params — app-scoped, secrets AES-GCM encrypted
	GetAppParams(ctx context.Context, tenantID, appID string) ([]byte, error)
	SetAppParam(ctx context.Context, tenantID, appID, name string, valueJSON []byte) error
	DeleteAppParam(ctx context.Context, tenantID, appID, name string) error
	SetOrchestratorMCPServers(ctx context.Context, appID, orchID string, servers []dal.MCPServerAttachment) error

	// Runs — tenant-scoped
	ListRuns(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Run, error)
	GetRun(ctx context.Context, tenantID, runID string) (dal.Run, error)
	GetRunContextID(ctx context.Context, tenantID, runID string) (string, error)
	GetRunStats(ctx context.Context, tenantID string) (dal.RunStats, error)
	GetRunDetail(ctx context.Context, tenantID, runID string) (dal.RunDetail, error)
	GetRunTasks(ctx context.Context, tenantID, runID string) ([]dal.Task, error)
	GetRunArtifacts(ctx context.Context, tenantID, runID string) ([]dal.Artifact, error)
	ListContextSessions(ctx context.Context, tenantID, orchestrator string, limit int) ([]dal.ContextSession, error)
	GetContextArtifacts(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Artifact, error)
	GetContextMessages(ctx context.Context, tenantID, contextID string, limit int) ([]dal.ContextMessage, error)
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

	// Agent definitions (Canvas A2A Builder, Phase 2) — tenant-scoped, design-time only
	GetNextAgentRevision(ctx context.Context, tenantID, agentSlug string) (int, error)
	CreateAgentDefinition(ctx context.Context, tenantID, agentSlug string, rev int, defJSON []byte, hash string, ownerID int) (string, error)
	GetAgentDefinition(ctx context.Context, tenantID, id string) (dal.AgentDefinition, error)
	ListAgentDefinitions(ctx context.Context, tenantID string) ([]dal.AgentDefinition, error)
	UpdateDraftAgentDefinition(ctx context.Context, tenantID, id string, defJSON []byte, hash string) error
	RevertPublishedToDraft(ctx context.Context, tenantID, id string, defJSON []byte, hash string) error
	DeleteDraftAgentDefinition(ctx context.Context, tenantID, id string) error

	// Canvas agent publish pipeline (Phase 3) — tenant-scoped
	GetAgentDefinitionForPublish(ctx context.Context, tenantID, id string) (dal.AgentDefinition, error)
	PublishCanvasAgent(ctx context.Context, row dal.CanvasAgentRow) error
	MarkAgentDefinitionPublished(ctx context.Context, tenantID, id string) error

	// Application agent bindings (Phase 3) — app-scoped, credentials AES-GCM encrypted
	UpsertAgentBinding(ctx context.Context, row dal.AgentBindingRow) error
	GetAgentBindingStatus(ctx context.Context, applicationID, agentID string) (dal.AgentBindingSlotStatus, error)
	ListAgentBindings(ctx context.Context, applicationID string) ([]dal.AgentBindingSlotStatus, error)
	DeleteAgentBinding(ctx context.Context, applicationID, agentID string) error

	// Agent runtime params — per-binding, secrets Fernet-encrypted
	GetAgentParamsForBinding(ctx context.Context, applicationID, agentID string) (dal.AgentParamsRow, error)
	GetRequiredParamsForAgent(ctx context.Context, agentID string) (dal.AgentParamsRow, error)
	UpsertAgentParams(ctx context.Context, applicationID, agentID string, paramsDelta []byte) error

	// Canvas agent LLM node overrides — provider+model per node, stored in config_overrides
	GetAgentLLMNodes(ctx context.Context, applicationID, agentID string) ([]byte, []byte, string, error)
	UpsertNodeLLMOverride(ctx context.Context, applicationID, agentID, nodeID, provider, model string) error

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

	// LLM providers — per-tenant override management
	ListProvidersForTenant(ctx context.Context, tenantID string) ([]dal.LLMProvider, error)
	GetProviderByNameForTenant(ctx context.Context, name, tenantID string) (dal.LLMProvider, error)
	GetProviderByNamePlatform(ctx context.Context, name string) (dal.LLMProvider, error)
	UpsertTenantProvider(ctx context.Context, tenantID string, in dal.LLMProviderInput) (dal.LLMProvider, error)

	// MCP servers — tenant-scoped
	ListMCPServers(ctx context.Context, tenantID string) ([]dal.MCPServer, error)
	GetMCPServer(ctx context.Context, id, tenantID string) (dal.MCPServer, error)
	CreateMCPServer(ctx context.Context, in dal.MCPServerInput) (dal.MCPServer, error)
	UpdateMCPServer(ctx context.Context, id, tenantID string, in dal.MCPServerInput) (dal.MCPServer, error)
	DeleteMCPServer(ctx context.Context, id, tenantID string) error

	// MCP app credentials — per-application, Fernet-encrypted
	GetAppMCPCredential(ctx context.Context, applicationID, serverID string) (dal.AppMCPCredential, error)
	ListAppMCPCredentials(ctx context.Context, applicationID string) ([]dal.AppMCPCredentialMeta, error)
	UpsertAppMCPCredential(ctx context.Context, applicationID, serverID, encryptedCred, headerName string) error
	DeleteAppMCPCredential(ctx context.Context, applicationID, serverID string) error
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
