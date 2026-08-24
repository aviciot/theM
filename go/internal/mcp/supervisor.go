package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Supervisor manages one independent worker goroutine per MCP server.
// It reconciles the live server list from the DB against running workers
// every reconcileInterval, spawning workers for new servers and stopping
// workers for removed or disabled ones.
//
// Only the leader replica (LeaderLock) runs the Supervisor; non-leaders
// are pure stateless HTTP servers handling /internal/execute.
type Supervisor struct {
	dal               *DAL
	registry          *Registry
	leader            *LeaderLock
	probeInterval     time.Duration
	reconcileInterval time.Duration
	maxProbeTimeout   time.Duration
	log               *slog.Logger

	mu      sync.Mutex
	workers map[string]context.CancelFunc // keyed by server ID
}

// NewSupervisor creates a Supervisor. probeIntervalSeconds controls how often
// each worker probes its server. reconcileInterval is how often the supervisor
// checks the DB for new or removed servers (always 30s — not configurable).
func NewSupervisor(dal *DAL, registry *Registry, leader *LeaderLock, probeIntervalSeconds int, log *slog.Logger) *Supervisor {
	return &Supervisor{
		dal:               dal,
		registry:          registry,
		leader:            leader,
		probeInterval:     time.Duration(probeIntervalSeconds) * time.Second,
		reconcileInterval: 30 * time.Second,
		maxProbeTimeout:   15 * time.Second,
		log:               log,
		workers:           make(map[string]context.CancelFunc),
	}
}

// Run starts the supervisor loop and blocks until ctx is cancelled.
// Call in a dedicated goroutine.
func (s *Supervisor) Run(ctx context.Context) {
	s.log.Info("supervisor started", "probe_interval", s.probeInterval, "reconcile_interval", s.reconcileInterval)
	defer s.log.Info("supervisor stopped")

	ticker := time.NewTicker(s.reconcileInterval)
	defer ticker.Stop()

	// Reconcile immediately on startup.
	s.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return
		case <-ticker.C:
			s.reconcile(ctx)
		}
	}
}

// ProbeNow triggers an immediate synchronous health+discovery cycle for a
// single server. Used by the on-demand probe HTTP endpoint.
func (s *Supervisor) ProbeNow(ctx context.Context, serverID string) (Server, error) {
	srv, err := s.dal.GetServerByID(ctx, serverID)
	if err != nil {
		return Server{}, err
	}
	w := newWorker(srv, s.dal, s.registry, s.probeInterval, s.maxProbeTimeout, s.log)
	w.probe(ctx)
	return s.dal.GetServerByID(ctx, serverID)
}

// reconcile diffs the DB server list against running workers, starts new ones,
// and stops workers for servers that were deleted or disabled.
func (s *Supervisor) reconcile(ctx context.Context) {
	isLeader, err := s.leader.TryAcquire(ctx)
	if err != nil {
		s.log.Warn("supervisor: leader acquire error", "error", err)
		return
	}
	if !isLeader {
		// Not the leader — stop any workers we may have been running
		// (e.g. after a leadership handover).
		s.stopAll()
		return
	}

	servers, err := s.dal.ListEnabledServers(ctx)
	if err != nil {
		s.log.Error("supervisor: list servers", "error", err)
		return
	}

	s.registry.Populate(servers)

	// Build a set of active server IDs from the DB.
	active := make(map[string]Server, len(servers))
	for _, srv := range servers {
		active[srv.ID] = srv
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop workers for servers no longer in the active set.
	for id, cancel := range s.workers {
		if _, ok := active[id]; !ok {
			s.log.Info("supervisor: stopping worker — server removed or disabled", "server_id", id)
			cancel()
			delete(s.workers, id)
		}
	}

	// Start workers for newly discovered servers.
	for _, srv := range active {
		if _, running := s.workers[srv.ID]; running {
			continue
		}
		srv := srv // capture for goroutine
		workerCtx, cancel := context.WithCancel(ctx)
		s.workers[srv.ID] = cancel
		w := newWorker(srv, s.dal, s.registry, s.probeInterval, s.maxProbeTimeout, s.log)
		go w.run(workerCtx)
		s.log.Info("supervisor: started worker", "slug", srv.Slug, "server_id", srv.ID)
	}
}

// stopAll cancels all running workers. Called on context cancellation or
// when this replica loses leadership.
func (s *Supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.workers {
		cancel()
		delete(s.workers, id)
	}
}

// WorkerCount returns the number of currently running workers (for observability).
func (s *Supervisor) WorkerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}
