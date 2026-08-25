package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aviciot/them/internal/crypto"
)

// worker manages the health+discovery lifecycle for a single MCP server.
// Each worker runs in its own goroutine with its own probe ticker and
// independent exponential backoff. A panic in one worker never affects others.
//
// The worker holds a single persistent Client for its server. The MCP spec
// (2025-03-26) requires initialize to be a one-time session handshake, not
// a per-request step. The client is initialized once and reused across probe
// cycles; it re-initializes on session expiry (HTTP 404).
type worker struct {
	server          Server
	dal             *DAL
	registry        *Registry
	baseInterval    time.Duration
	maxProbeTimeout time.Duration
	fernetKey       []byte
	log             *slog.Logger
	client          *Client // persistent; initialized once per session
}

func newWorker(server Server, dal *DAL, registry *Registry, baseInterval, maxProbeTimeout time.Duration, fernetKey []byte, log *slog.Logger) *worker {
	return &worker{
		server:          server,
		dal:             dal,
		registry:        registry,
		baseInterval:    baseInterval,
		maxProbeTimeout: maxProbeTimeout,
		fernetKey:       fernetKey,
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

	// Ensure we have an initialized session. On first probe, or after session
	// expiry (HTTP 404), create a fresh client with the probe credential and initialize.
	if w.client == nil {
		headerName, authValue := w.resolveProbeAuth()
		w.client = NewClient(w.server.URL, headerName, authValue)
	}
	if !w.client.initialized {
		if err := w.client.Initialize(probeCtx); err != nil {
			w.client = nil // discard; will retry next cycle
			w.setStatus(ctx, "unreachable", err.Error())
			return
		}
	}

	result, err := w.client.Discover(probeCtx)
	if err != nil {
		if IsSessionExpired(err) {
			// Session expired — discard client, re-initialize next cycle.
			w.log.Info("session expired, will re-initialize on next probe")
			w.client = nil
		}
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

// resolveProbeAuth decrypts probe_credential_encrypted and returns the
// header name + value to use when building the probe Client.
// Returns ("", "") when no probe credential is configured (auth_type=none or no stored token).
func (w *worker) resolveProbeAuth() (headerName, authValue string) {
	if w.server.AuthType == "none" || w.server.ProbeCredentialEncrypted == "" {
		return "", ""
	}
	plain, err := crypto.DecryptStored(w.fernetKey, w.server.ProbeCredentialEncrypted)
	if err != nil {
		w.log.Warn("probe: failed to decrypt probe credential — probing without auth",
			"error", err)
		return "", ""
	}
	switch w.server.AuthType {
	case "bearer":
		return "Authorization", "Bearer " + plain
	case "header":
		// For custom header auth, the header name is not stored at server level
		// (it lives on app_mcp_credentials per-app). Default to Authorization.
		return "Authorization", plain
	default:
		return "Authorization", plain
	}
}
