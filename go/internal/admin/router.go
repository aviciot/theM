package admin

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/service"
)

// BuildRouter returns an http.Handler with all admin and runs routes mounted.
//
// jwtMiddleware is the JWT validation middleware (from auth.JWTMiddleware).
// Pass nil to disable JWT protection (development only).
//
// sessionReader is the session store for admin session listing/disconnect.
// Pass nil to disable session admin routes (development only).
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
//	GET    /runs
//	GET    /runs/{run_id}
//	POST   /runs/{run_id}/signal
func BuildRouter(
	db DBQuerier,
	cache CacheInvalidator,
	temporal TemporalSignaler,
	sessionReader service.SessionReader,
	jwtMiddleware func(http.Handler) http.Handler,
	logger *slog.Logger,
) http.Handler {
	r := chi.NewRouter()

	agents := NewAgentsHandler(db, cache)
	orchs := NewOrchestratorsHandler(db, cache)
	apps := NewApplicationsHandler(db, cache)
	runs := NewRunsHandler(db, temporal)
	tokens := NewTokensHandler(db, cache)
	monitoring := NewMonitoringConfigHandler(db)
	llmRouting := NewLLMRoutingHandler(db)

	// Admin routes — protected by JWT + super_admin role check.
	r.Group(func(admin chi.Router) {
		if jwtMiddleware != nil {
			admin.Use(jwtMiddleware)
		}
		admin.Use(RequireSuperAdmin(logger))

		// Mount under /admin prefix.
		admin.Route("/admin", func(a chi.Router) {
			agents.Routes(a)
			orchs.Routes(a)
			apps.Routes(a)
			tokens.Routes(a)
			monitoring.Routes(a)
			llmRouting.Routes(a)
			if sessionReader != nil {
				NewSessionsHandler(sessionReader).Routes(a)
			}
		})
	})

	// Runs routes — JWT protected.
	r.Group(func(runsGroup chi.Router) {
		if jwtMiddleware != nil {
			runsGroup.Use(jwtMiddleware)
		}
		runs.Routes(runsGroup)
	})

	return r
}
