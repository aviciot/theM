package admin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/registry"
	"github.com/aviciot/them/internal/temporal"
)

// registryQuerierAdapter adapts admin.DBQuerier to registry.DBQuerier.
// Both interfaces expose QueryRow with the same signature except for the
// return type — dal.SingleRowScanner vs registry.SingleRowScanner — which
// are structurally identical (both have Scan(...any) error).
// The adapter wraps the returned row to satisfy the registry interface.
type registryQuerierAdapter struct{ q DBQuerier }

func (a *registryQuerierAdapter) QueryRow(ctx context.Context, sql string, args ...any) registry.SingleRowScanner {
	return a.q.QueryRow(ctx, sql, args...)
}

// BuildRouter returns an http.Handler with all admin and runs routes mounted.
//
// Route classification:
//
//   - Tenant-scoped control-plane (agents, orchestrators, applications, tokens, runs):
//     JWT + RequireSuperAdmin + AdminTenantMiddleware. TenantID comes from the JWT
//     Claims set by jwtMiddleware; super_admin users with no tenant_id claim fall back
//     to the bootstrap tenant (covers all UI-authenticated admin users).
//
//   - Platform-global control-plane (llm-providers, monitoring-config, llm-routing, sessions):
//     JWT + RequireSuperAdmin only. No tenant scoping — these are platform-wide resources.
//
//   - Runtime data-plane (ws, sse, a2a): handled outside this router; not touched here.
//
// jwtMiddleware is the JWT validation middleware (HS256 or RS256).
// Pass nil to disable JWT protection (tests only).
//
// tokenCache is the bearer-token cache (reserved for future data-plane use).
// Pass nil in tests.
//
// sessionReader is the session store for admin session listing/disconnect.
// Pass nil to disable session admin routes (tests only).
//
// secretKey is THE_M_SECRET_KEY for Fernet LLM provider key encryption.
//
// redis is the rueidis client used by agent action endpoints (Discover/Test/SecurityScan).
// Pass nil to disable background scan jobs (tests only).
//
// fernetKey is the 32-byte Fernet key derived from secretKey for agent token decryption.
//
// Routes:
//
//	GET    /admin/agents
//	POST   /admin/agents
//	GET    /admin/agents/{id}
//	PUT    /admin/agents/{id}
//	DELETE /admin/agents/{id}
//	POST   /admin/agents/discover
//	POST   /admin/agents/{id}/test
//	POST   /admin/agents/{id}/security-scan
//	GET    /admin/orchestrators
//	... (full CRUD)
//	GET    /admin/applications
//	... (full CRUD + entry points)
//	POST   /admin/agent-definitions
//	GET    /admin/agent-definitions
//	GET    /admin/agent-definitions/{id}
//	PUT    /admin/agent-definitions/{id}
//	DELETE /admin/agent-definitions/{id}
//	GET    /admin/tokens
//	POST   /admin/tokens
//	GET    /admin/tokens/{token_id}
//	PATCH  /admin/tokens/{token_id}
//	DELETE /admin/tokens/{token_id}
//	GET    /admin/sessions
//	POST   /admin/sessions/{session_id}/disconnect
//	GET    /admin/llm-providers
//	POST   /admin/llm-providers
//	GET    /admin/llm-providers/{id}
//	PATCH  /admin/llm-providers/{id}
//	DELETE /admin/llm-providers/{id}
//	GET    /admin/system-agents
//	PUT    /admin/system-agents
//	POST   /admin/system-agents/{role}/test-llm
//	GET    /runs
//	GET    /runs/stats
//	POST   /runs/bulk-delete
//	GET    /runs/{run_id}
//	PATCH  /runs/{run_id}/cancel
//	DELETE /runs/{run_id}
//	GET    /runs/{run_id}/tasks
//	GET    /runs/{run_id}/artifacts
//	POST   /runs/{run_id}/signal
func BuildRouter(
	dbq DBQuerier,
	pools *db.Pools,
	cache CacheInvalidator,
	temporalSig TemporalSignaler,
	sessionReader service.SessionReader,
	jwtMiddleware func(http.Handler) http.Handler,
	tokenCache *auth.Cache,
	logger *slog.Logger,
	secretKey string,
	redis rueidis.Client,
	fernetKey []byte,
	mcpServiceURL string,
	anthropicAPIKey string,
	hitlStore *agentgen.HITLStore,
	canvasSignaler temporal.CanvasSignaler,
) http.Handler {
	r := chi.NewRouter()

	auditWriter := NewAuditWriter(pools)
	agents := NewAgentsHandler(dbq, pools, cache, redis, fernetKey, auditWriter)
	orchs := NewOrchestratorsHandler(dbq, pools, cache)
	apps := NewApplicationsHandler(dbq, pools, cache, fernetKey, auditWriter)
	defs := NewDefinitionsHandlerWithRegistry(dbq, registry.NewResolver(&registryQuerierAdapter{dbq}))
	runs := NewRunsHandler(dbq, temporalSig)
	tokens := NewTokensHandler(dbq, pools, cache)
	monitoring := NewMonitoringConfigHandler(dbq)
	llmRouting := NewLLMRoutingHandler(dbq)
	llmProviders := NewLLMProvidersHandler(dbq, secretKey)
	systemAgents := NewSystemAgentsHandler(dbq, fernetKey)
	tenants := NewTenantsHandler(dbq, auditWriter)
	managedApps := NewManagedAppsHandler(dbq)

	// Admin routes — all require JWT + super_admin.
	// Within /admin, tenant-scoped resources also require AdminTenantMiddleware.
	r.Group(func(adminGroup chi.Router) {
		if jwtMiddleware != nil {
			adminGroup.Use(jwtMiddleware)
		}
		adminGroup.Use(RequireSuperAdmin(logger))

		adminGroup.Route("/admin", func(a chi.Router) {
			// Tenant-scoped sub-group: agents, orchestrators, applications, tokens.
			// AdminTenantMiddleware extracts TenantID from the JWT Claims set by
			// jwtMiddleware. Super_admin users with no tenant_id claim fall back to
			// the bootstrap tenant — that covers all UI-authenticated admin users.
			a.Group(func(tenantScoped chi.Router) {
				tenantScoped.Use(AdminTenantMiddleware())
				agents.Routes(tenantScoped)
				orchs.Routes(tenantScoped)
				defs.Routes(tenantScoped)
				tokens.Routes(tenantScoped)
				reg := NewRegistryHandler(dbq)
				tenantScoped.Get("/component-definitions", reg.ListComponentDefinitions)

				// Schema + Generate must be registered BEFORE agentDefs.Routes so
				// chi does not interpret "schema" and "generate" as {id} path params.
				var llmCaller generateLLMCaller
				if anthropicAPIKey != "" {
					llmCaller = newAnthropicCompleter(anthropicAPIKey)
				}
				schemaHandler := NewAgentDefinitionSchemaHandler(dbq, llmCaller)
				tenantScoped.Get("/agent-definitions/schema", schemaHandler.Schema)
				tenantScoped.Post("/agent-definitions/generate", schemaHandler.Generate)

				agentDefs := NewAgentDefinitionsHandler(dbq, pools, cache, fernetKey)
				agentDefs.Routes(tenantScoped)

				// Agent bindings are mounted inside apps.Routes under the /applications/{id}
				// sub-tree so they share the same chi node and don't shadow the flat DELETE /{id}.
				bindings := NewAgentBindingsHandler(agentDefs.Svc())
				apps.Routes(tenantScoped, bindings)

				secCfg := NewSecurityConfigHandler(dbq, redis)
				secCfg.Routes(tenantScoped)

				mcpServers := NewMCPServersHandler(pools, secretKey, mcpServiceURL, auditWriter)
				mcpServers.Routes(tenantScoped)

				// HITL canvas task signal endpoint — only mounted when Temporal is enabled.
				if ct := NewCanvasTasksHandler(hitlStore, canvasSignaler); ct != nil {
					ct.Routes(tenantScoped)
				}

				managedApps.TenantRoutes(tenantScoped)

				auditLogs := NewAuditLogsHandler(dbq, pools)
				auditLogs.Routes(tenantScoped)
			})

			// Platform-global sub-group: llm-providers, monitoring-config,
			// llm-routing, system-agents, sessions. No tenant scoping — these
			// resources are platform-wide and apply to all tenants.
			// /node-types is mounted here for route grouping but moved to the
			// public group below — it needs no auth (static canvas metadata).
			monitoring.Routes(a)
			llmRouting.Routes(a)
			llmProviders.Routes(a)
			llmProviders.TenantProviderRoutes(a)
			systemAgents.Routes(a)
			tenants.Routes(a)
			if pools != nil {
				NewObservabilityHandler(pools).Routes(a)
			}
			managedApps.PlatformRoutes(a)
			if sessionReader != nil {
				NewSessionsHandler(sessionReader).Routes(a)
			}
			NewServicesStatsHandler(dbq, redis, logger).Routes(a)
		})

		// Runs routes are at /runs (not /admin/runs) to match the existing Traefik
		// rules that capture /api/v1/runs/*. They use JWT + super_admin + AdminTenantMiddleware
		// (same auth as other admin routes) instead of the old BearerTenantMiddleware.
		adminGroup.Group(func(runsGroup chi.Router) {
			runsGroup.Use(AdminTenantMiddleware())
			runs.Routes(runsGroup)
		})
	})

	// Debug proxy — authenticated, forwards HTTP requests server-side to avoid
	// CORS restrictions when the browser debugs an agent pipeline directly.
	if jwtMiddleware != nil {
		r.With(jwtMiddleware, RequireSuperAdmin(logger)).Post("/admin/debug-proxy", DebugProxyHandler{}.ServeHTTP)
	} else {
		r.Post("/admin/debug-proxy", DebugProxyHandler{}.ServeHTTP)
	}

	// Transform function endpoints — all public (stateless compute, no tenant data).
	// The admin UI is already behind session auth; these endpoints carry no secrets.
	tf := TransformHandler{}
	r.Get("/admin/transform-functions", tf.Catalog)
	r.Post("/admin/transform-test", tf.Test)
	r.Post("/admin/transform-assist", tf.Assist)

	// Public routes — no auth required.
	// /admin/node-types: static canvas node metadata, no tenant data.
	r.Get("/admin/node-types", NodeTypesHandler{}.ServeHTTP)

	return r
}
