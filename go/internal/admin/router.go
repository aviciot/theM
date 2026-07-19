package admin

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// BuildRouter returns an http.Handler with all admin and runs routes mounted.
//
// jwtMiddleware is the JWT validation middleware (from auth.JWTMiddleware).
// Pass nil to disable JWT protection (development only).
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
//	GET    /runs
//	GET    /runs/{run_id}
//	POST   /runs/{run_id}/signal
func BuildRouter(
	db DBQuerier,
	cache CacheInvalidator,
	temporal TemporalSignaler,
	jwtMiddleware func(http.Handler) http.Handler,
	logger *slog.Logger,
) http.Handler {
	r := chi.NewRouter()

	agents := NewAgentsHandler(db, cache)
	orchs := NewOrchestratorsHandler(db, cache)
	apps := NewApplicationsHandler(db, cache)
	runs := NewRunsHandler(db, temporal)

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
