package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/handler"
	"github.com/aviciot/them-test-runner/scenario"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	// Structured JSON logging — readable by docker logs and log aggregators.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}
	os.MkdirAll(dataDir, 0755)

	cfg, err := config.Load(dataDir)
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	store := scenario.NewStore(dataDir)
	mgr := scenario.NewManager(dataDir)
	scHandler := handler.NewScenarioHandler(store)
	runHandler := handler.NewRunHandler(store, mgr)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Config
	r.Get("/api/config", handler.GetConfig)
	r.Put("/api/config", handler.PutConfig)
	r.Post("/api/config/test", handler.TestConfig)

	// Catalog — proxy to the-M admin API
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
	slog.Info("test-runner backend starting", "addr", addr, "data_dir", dataDir)
	if err := http.ListenAndServe(addr, r); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// slogMiddleware logs each HTTP request with method, path, status, and latency.
func slogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Skip SSE stream endpoint from per-request log spam.
		if r.URL.Path != "" {
			slog.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"remote", r.RemoteAddr,
			)
		}
	})
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
