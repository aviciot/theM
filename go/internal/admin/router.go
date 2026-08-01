package admin

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/auth"
)

// BuildRouter returns an http.Handler with all admin and runs routes mounted.
//
// Route classification:
//
//   - Tenant-scoped control-plane (agents, orchestrators, applications, tokens, runs):
//     JWT + RequireSuperAdmin + BearerTenantMiddleware. TenantID comes exclusively
//     from the bearer token; never from headers, query params, or body.
//
//   - Platform-global control-plane (llm-providers, monitoring-config, llm-routing, sessions):
//     JWT + RequireSuperAdmin only. No tenant scoping — these are platform-wide resources.
//
//   - Runtime data-plane (ws, sse, a2a): handled outside this router; not touched here.
//
// jwtMiddleware is the JWT validation middleware (HS256 or RS256).
// Pass nil to disable JWT protection (tests only).
//
// tokenCache is the bearer-token cache used by BearerTenantMiddleware.
// Pass nil to skip tenant enforcement (tests that do not test tenant middleware).
//
// sessionReader is the session store for admin session listing/disconnect.
// Pass nil to disable session admin routes (tests only).
//
// secretKey is THE_M_SECRET_KEY for Fernet LLM provider key encryption.
//
// Routes:
//
//	GET    /admin/agents
//	POST   /admin/agents
//	GET    /admin/agents/{id}
//	PUT    /admin/agents/{id}
//	DELETE /admin/agents/{id}
//	GET    /admin/orchestrators
//	... (full CRUD)
//	GET    /admin/applications
//	... (full CRUD + entry points)
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
//	GET    /runs
//	GET    /runs/{run_id}
//	POST   /runs/{run_id}/signal
func BuildRouter(
	db DBQuerier,
	cache CacheInvalidator,
	temporal TemporalSignaler,
	sessionReader service.SessionReader,
	jwtMiddleware func(http.Handler) http.Handler,
	tokenCache *auth.Cache,
	logger *slog.Logger,
	secretKey string,
) http.Handler {
	r := chi.NewRouter()

	agents := NewAgentsHandler(db, cache)
	orchs := NewOrchestratorsHandler(db, cache)
	apps := NewApplicationsHandler(db, cache)
	runs := NewRunsHandler(db, temporal)
	tokens := NewTokensHandler(db, cache)
	monitoring := NewMonitoringConfigHandler(db)
	llmRouting := NewLLMRoutingHandler(db)
	llmProviders := NewLLMProvidersHandler(db, secretKey)

	// Admin routes — all require JWT + super_admin.
	// Within /admin, tenant-scoped resources also require BearerTenantMiddleware.
	r.Group(func(adminGroup chi.Router) {
		if jwtMiddleware != nil {
			adminGroup.Use(jwtMiddleware)
		}
		adminGroup.Use(RequireSuperAdmin(logger))

		adminGroup.Route("/admin", func(a chi.Router) {
			// Tenant-scoped sub-group: agents, orchestrators, applications, tokens.
			// BearerTenantMiddleware extracts TenantID from the same bearer token
			// that identifies the caller. TenantID is NEVER read from request data.
			a.Group(func(tenantScoped chi.Router) {
				if tokenCache != nil {
					tenantScoped.Use(auth.BearerTenantMiddleware(tokenCache))
				}
				agents.Routes(tenantScoped)
				orchs.Routes(tenantScoped)
				apps.Routes(tenantScoped)
				tokens.Routes(tenantScoped)
			})

			// Platform-global sub-group: llm-providers, monitoring-config,
			// llm-routing, sessions. No tenant scoping — these resources are
			// platform-wide and apply to all tenants.
			monitoring.Routes(a)
			llmRouting.Routes(a)
			llmProviders.Routes(a)
			if sessionReader != nil {
				NewSessionsHandler(sessionReader).Routes(a)
			}
		})
	})

	// Tenant-scoped runs routes — JWT + BearerTenantMiddleware.
	// Runs are tenant-owned; RequireSuperAdmin is not applied here (run access
	// uses the bearer token's tenant, not the super_admin JWT role).
	r.Group(func(runsGroup chi.Router) {
		if jwtMiddleware != nil {
			runsGroup.Use(jwtMiddleware)
		}
		if tokenCache != nil {
			runsGroup.Use(auth.BearerTenantMiddleware(tokenCache))
		}
		runs.Routes(runsGroup)
	})

	return r
}
