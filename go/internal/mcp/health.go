package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// HealthLoop runs the background health-check and discovery cycle.
// It only executes on the leader replica (controlled by LeaderLock).
type HealthLoop struct {
	dal      *DAL
	registry *Registry
	leader   *LeaderLock
	interval time.Duration
	log      *slog.Logger
}

// NewHealthLoop creates a HealthLoop with the given dependencies.
func NewHealthLoop(dal *DAL, registry *Registry, leader *LeaderLock, intervalSeconds int, log *slog.Logger) *HealthLoop {
	return &HealthLoop{
		dal:      dal,
		registry: registry,
		leader:   leader,
		interval: time.Duration(intervalSeconds) * time.Second,
		log:      log,
	}
}

// Run starts the health loop and blocks until ctx is cancelled.
// It should be run in a goroutine.
func (h *HealthLoop) Run(ctx context.Context) {
	h.log.Info("health loop started", "interval", h.interval)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	// Run once immediately on startup.
	h.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			h.log.Info("health loop stopped")
			return
		case <-ticker.C:
			h.tick(ctx)
		}
	}
}

func (h *HealthLoop) tick(ctx context.Context) {
	// Only the leader runs the loop body.
	isLeader, err := h.leader.TryAcquire(ctx)
	if err != nil {
		h.log.Warn("health loop: leader acquire error", "error", err)
		return
	}
	if !isLeader {
		return
	}

	// Reload server list from DB every tick to pick up new registrations.
	servers, err := h.dal.ListEnabledServers(ctx)
	if err != nil {
		h.log.Error("health loop: list servers", "error", err)
		return
	}
	h.registry.Populate(servers)

	for _, s := range servers {
		h.probeServer(ctx, s)
	}
}

// ProbeServer runs a single health + discovery cycle for the given server.
// Exported so the HTTP handler can call it for on-demand probes.
func (h *HealthLoop) ProbeServer(ctx context.Context, serverID string) (Server, error) {
	s, err := h.dal.GetServerByID(ctx, serverID)
	if err != nil {
		return Server{}, err
	}
	h.probeServer(ctx, s)
	// Re-fetch to return updated state.
	return h.dal.GetServerByID(ctx, serverID)
}

func (h *HealthLoop) probeServer(ctx context.Context, s Server) {
	if s.URL == "" {
		h.log.Warn("health loop: server has no URL, skipping", "slug", s.Slug)
		return
	}

	client := NewClient(s.URL, "", "") // no auth for probe — just reachability
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := client.Probe(probeCtx)
	if err != nil {
		h.markUnreachable(ctx, s, err.Error())
		return
	}

	// Successful probe — run discovery if manifest is stale.
	h.runDiscovery(ctx, s, client)
}

func (h *HealthLoop) runDiscovery(ctx context.Context, s Server, client *Client) {
	discCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	result, err := client.Discover(discCtx)
	if err != nil {
		// Reachable but discovery failed → degraded.
		h.markDegraded(ctx, s, err.Error())
		return
	}

	// Detect tool count drop (>20% decrease = degraded).
	if len(result.Tools) > 0 {
		var prev []json.RawMessage
		_ = json.Unmarshal(s.ToolsManifest, &prev)
		if len(prev) > 0 && len(result.Tools) < int(float64(len(prev))*0.8) {
			h.log.Warn("health loop: tool count dropped", "slug", s.Slug,
				"before", len(prev), "after", len(result.Tools))
			h.markDegraded(ctx, s, "tool count dropped significantly")
			return
		}
	}

	manifest, _ := json.Marshal(result.Tools)
	caps, _ := json.Marshal(result.Capabilities)

	if err := h.dal.UpdateManifest(ctx, s.ID, manifest, caps); err != nil {
		h.log.Error("health loop: update manifest", "slug", s.Slug, "error", err)
	}
	if err := h.dal.UpdateHealth(ctx, s.ID, "healthy", ""); err != nil {
		h.log.Error("health loop: update health", "slug", s.Slug, "error", err)
	}

	s.ToolsManifest = manifest
	s.HealthStatus = "healthy"
	s.LastError = ""
	h.registry.UpdateServer(s)

	_ = h.registry.CacheManifest(ctx, s.Slug, manifest)
	_ = h.registry.CacheHealth(ctx, s.Slug, "healthy", "")
	_ = h.registry.PublishManifestChanged(ctx, s.Slug)

	h.log.Info("health loop: server healthy", "slug", s.Slug, "tools", len(result.Tools))
}

func (h *HealthLoop) markUnreachable(ctx context.Context, s Server, errMsg string) {
	_ = h.dal.UpdateHealth(ctx, s.ID, "unreachable", errMsg)
	_ = h.registry.CacheHealth(ctx, s.Slug, "unreachable", errMsg)
	s.HealthStatus = "unreachable"
	s.LastError = errMsg
	h.registry.UpdateServer(s)
	h.log.Warn("health loop: server unreachable", "slug", s.Slug, "error", errMsg)
}

func (h *HealthLoop) markDegraded(ctx context.Context, s Server, errMsg string) {
	_ = h.dal.UpdateHealth(ctx, s.ID, "degraded", errMsg)
	_ = h.registry.CacheHealth(ctx, s.Slug, "degraded", errMsg)
	s.HealthStatus = "degraded"
	s.LastError = errMsg
	h.registry.UpdateServer(s)
	h.log.Warn("health loop: server degraded", "slug", s.Slug, "error", errMsg)
}
