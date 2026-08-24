package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// worker manages the health+discovery lifecycle for a single MCP server.
// Each worker runs in its own goroutine with its own probe ticker and
// independent exponential backoff. A panic in one worker never affects others.
type worker struct {
	server          Server
	dal             *DAL
	registry        *Registry
	baseInterval    time.Duration
	maxProbeTimeout time.Duration
	log             *slog.Logger
}

func newWorker(server Server, dal *DAL, registry *Registry, baseInterval, maxProbeTimeout time.Duration, log *slog.Logger) *worker {
	return &worker{
		server:          server,
		dal:             dal,
		registry:        registry,
		baseInterval:    baseInterval,
		maxProbeTimeout: maxProbeTimeout,
		log:             log.With("slug", server.Slug, "server_id", server.ID),
	}
}

// run is the per-server goroutine entry point. It blocks until ctx is cancelled.
func (w *worker) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("worker panic recovered", "panic", fmt.Sprintf("%v", r))
		}
	}()

	w.log.Info("worker started")
	defer w.log.Info("worker stopped")

	// Probe immediately on startup — don't wait for the first tick.
	w.probe(ctx)

	interval := w.baseInterval

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			w.probe(ctx)
			// Adjust interval based on current health state.
			interval = w.nextInterval()
		}
	}
}

// nextInterval returns the probe interval for the next tick based on current
// health state. Healthy servers probe at baseInterval. Unhealthy servers back
// off exponentially up to 10 minutes to avoid hammering unreachable endpoints.
func (w *worker) nextInterval() time.Duration {
	switch w.server.HealthStatus {
	case "unreachable":
		next := w.baseInterval * 2
		if next > 10*time.Minute {
			next = 10 * time.Minute
		}
		return next
	case "degraded":
		next := w.baseInterval + w.baseInterval/2
		if next > 5*time.Minute {
			next = 5 * time.Minute
		}
		return next
	default:
		return w.baseInterval
	}
}

// probe runs one health+discovery cycle for this worker's server.
// It updates DB, Redis cache, and the in-process registry.
func (w *worker) probe(ctx context.Context) {
	if w.server.URL == "" {
		w.log.Warn("no URL configured — skipping probe")
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, w.maxProbeTimeout)
	defer cancel()

	// auth-less client for probe — reachability check only.
	client := NewClient(w.server.URL, "", "")

	_, err := client.Probe(probeCtx)
	if err != nil {
		w.setStatus(ctx, "unreachable", err.Error())
		return
	}

	// Reachable — run discovery.
	result, err := client.Discover(probeCtx)
	if err != nil {
		w.setStatus(ctx, "degraded", err.Error())
		return
	}

	// Detect significant tool count drop (>20%) vs last known manifest.
	if w.isToolCountDrop(result.Tools) {
		msg := fmt.Sprintf("tool count dropped from %d to %d", w.manifestToolCount(), len(result.Tools))
		w.setStatus(ctx, "degraded", msg)
		return
	}

	w.setManifest(ctx, result)
	w.setStatus(ctx, "healthy", "")
}

func (w *worker) setStatus(ctx context.Context, status, errMsg string) {
	if err := w.dal.UpdateHealth(ctx, w.server.ID, status, errMsg); err != nil {
		w.log.Error("update health in DB", "error", err)
	}
	if err := w.registry.CacheHealth(ctx, w.server.Slug, status, errMsg); err != nil {
		w.log.Warn("cache health in Redis", "error", err)
	}

	// Update in-process registry state.
	w.server.HealthStatus = status
	w.server.LastError = errMsg
	w.registry.UpdateServer(w.server)

	if status == "healthy" {
		w.log.Info("probe ok", "tools", w.manifestToolCount())
	} else {
		w.log.Warn("probe result", "status", status, "error", errMsg)
	}
}

func (w *worker) setManifest(ctx context.Context, result *DiscoveryResult) {
	manifest, err := json.Marshal(result.Tools)
	if err != nil {
		w.log.Error("marshal tools manifest", "error", err)
		return
	}
	caps, _ := json.Marshal(result.Capabilities)

	if err := w.dal.UpdateManifest(ctx, w.server.ID, manifest, caps); err != nil {
		w.log.Error("update manifest in DB", "error", err)
		return
	}
	if err := w.registry.CacheManifest(ctx, w.server.Slug, manifest); err != nil {
		w.log.Warn("cache manifest in Redis", "error", err)
	}
	if err := w.registry.PublishManifestChanged(ctx, w.server.Slug); err != nil {
		w.log.Warn("publish manifest changed", "error", err)
	}

	w.server.ToolsManifest = manifest
}

func (w *worker) manifestToolCount() int {
	if len(w.server.ToolsManifest) == 0 {
		return 0
	}
	var tools []Tool
	if err := json.Unmarshal(w.server.ToolsManifest, &tools); err != nil {
		return 0
	}
	return len(tools)
}

func (w *worker) isToolCountDrop(newTools []Tool) bool {
	prev := w.manifestToolCount()
	if prev == 0 {
		return false // no baseline — can't determine a drop
	}
	return len(newTools) > 0 && len(newTools) < int(float64(prev)*0.8)
}
