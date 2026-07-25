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
	// Agents
	ListAgents(ctx context.Context) ([]dal.Agent, error)
	GetAgent(ctx context.Context, id string) (dal.Agent, error)
	CreateAgent(ctx context.Context, in dal.AgentInput, enabled bool) (string, error)
	UpdateAgent(ctx context.Context, id string, in dal.AgentInput, enabled bool) error
	DeleteAgent(ctx context.Context, id string) error

	// Orchestrators
	ListOrchestrators(ctx context.Context) ([]dal.Orchestrator, error)
	GetOrchestrator(ctx context.Context, name string) (dal.Orchestrator, error)
	CreateOrchestrator(ctx context.Context, in dal.OrchestratorInput, enabled bool) (string, error)
	UpdateOrchestrator(ctx context.Context, name string, in dal.OrchestratorInput, enabled bool) error
	DeleteOrchestrator(ctx context.Context, name string) error

	// Applications + entry points
	ListApplications(ctx context.Context) ([]dal.Application, error)
	GetApplication(ctx context.Context, id string) (dal.Application, error)
	CreateApplication(ctx context.Context, name string, enabled bool) (string, error)
	UpdateApplication(ctx context.Context, id, name string, enabled bool) error
	DeleteApplication(ctx context.Context, id string) error
	ListEntryPoints(ctx context.Context, appID string) []dal.EntryPoint
	CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled bool) (string, error)
	GetEntryPointSlug(ctx context.Context, epID, appID string) (string, error)
	UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled bool) error
	DeleteEntryPoint(ctx context.Context, epID, appID string) error
	ListEPSlugsForApp(ctx context.Context, appID string) []string

	// Runs
	ListRuns(ctx context.Context, contextID string, limit int) ([]dal.Run, error)
	GetRun(ctx context.Context, runID string) (dal.Run, error)
	GetRunContextID(ctx context.Context, runID string) (string, error)

	// Tokens
	ListTokens(ctx context.Context, userID *int64) ([]dal.Token, error)
	GetToken(ctx context.Context, id string) (dal.Token, error)
	OrchestratorExists(ctx context.Context, orchID string) (bool, error)
	CreateToken(ctx context.Context, in dal.TokenCreateRow) (dal.Token, error)
	UpdateToken(ctx context.Context, id string, patch dal.TokenPatchRow) (hash string, out dal.Token, err error)
	DeleteToken(ctx context.Context, id string) (hash string, err error)

	// Config table (monitoring, llm_routing, …)
	GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
	UpsertConfig(ctx context.Context, key string, value []byte) error

	// LLM providers
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
