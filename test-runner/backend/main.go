package main

import (
	"log"
	"net/http"
	"os"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/handler"
	"github.com/aviciot/them-test-runner/scenario"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}
	os.MkdirAll(dataDir, 0755)

	cfg, err := config.Load(dataDir)
	if err != nil {
		log.Fatalf("config load: %v", err)
	}

	store := scenario.NewStore(dataDir)
	mgr := scenario.NewManager(dataDir)
	scHandler := handler.NewScenarioHandler(store)
	runHandler := handler.NewRunHandler(store, mgr)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Config
	r.Get("/api/config", handler.GetConfig)
	r.Put("/api/config", handler.PutConfig)
	r.Post("/api/config/test", handler.TestConfig)

	// Catalog
	r.Get("/api/tenants", handler.ListTenants)
	r.Get("/api/tenants/{slug}/apps", handler.ListApps)
	r.Get("/api/apps/{id}/eps", handler.ListEPs)

	// Scenarios
	r.Get("/api/scenarios", scHandler.List)
	r.Post("/api/scenarios", scHandler.Create)
	r.Put("/api/scenarios/{id}", scHandler.Update)
	r.Delete("/api/scenarios/{id}", scHandler.Delete)

	// Runs
	r.Post("/api/run", runHandler.Start)
	r.Get("/api/run/{runId}/stream", runHandler.Stream)
	r.Delete("/api/run/{runId}", runHandler.Cancel)

	// History
	r.Get("/api/history", runHandler.ListHistory)
	r.Get("/api/history/{runId}", runHandler.GetHistory)
	r.Delete("/api/history/{runId}", runHandler.DeleteHistory)

	addr := ":" + cfg.Port
	log.Printf("test-runner backend listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
