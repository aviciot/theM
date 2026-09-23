// Package debugcred stores per-node LLM credential overrides for App Canvas
// Debug Mode runs (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) — never through
// Temporal. Each entry is scoped by tenant + run + node, short-TTL, and
// intentionally does NOT get consumed/deleted on read: a Temporal activity
// retry re-runs its whole function body, including any credential lookup, so
// the value must survive repeated reads for the run's full lifetime.
//
// Key shape: them:debug:{tenant_id}:{run_id}:{node_id}:llm_override
//
// Absence-means-missing, not absence-means-never-requested: Store writes one
// entry for every node that had a declared llm_credential runtime param in
// the debug run's compiled spec, even when the caller left it at "use the
// tenant default key" (see MarkerOnly). This means a caller reading back an
// absent key, for a run/node pair known to need one, can conclude the
// credential is unavailable (expired, evicted) and must fail loudly rather
// than silently falling through to a different resolution path.
package debugcred

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/appflow"
)

// CleanupMargin is added on top of appflow.DebugRunMaxLifetime to compute
// TTL — headroom for the proactive delete-on-completion path (see
// Store.DeleteAllForRun) to actually run and for Redis/network latency,
// after the workflow's own enforced timeout has already fired. Not sized
// from any specific measurement — a deliberate round buffer.
const CleanupMargin = 10 * time.Minute

// TTL is the override entry's expiry. Derived FROM appflow.DebugRunMaxLifetime
// (the same enforced ceiling Lifecycle.StartAppFlow sets as the debug
// workflow's WorkflowRunTimeout) plus CleanupMargin — never an independently
// guessed number. This keeps "the run is still legitimately active" and "the
// credential hasn't expired" from ever disagreeing: Temporal kills the
// workflow at DebugRunMaxLifetime if it hasn't finished by then, and the
// credential outlives that boundary by exactly enough for the proactive
// cleanup (Store.DeleteAllForRun, called from FinalizeRunActivity on every
// terminal state) to run — TTL is strictly the fallback for a run that's
// abandoned, crashes, or is killed by the timeout before cleanup fires.
var TTL = appflow.DebugRunMaxLifetime + CleanupMargin

// Override is the stored credential for one node's llm_credential param.
// MarkerOnly means "this node needed a credential, the caller left it at the
// tenant default" — Provider/Model/APIKey are populated at write time from
// that default resolution either way, so a reader never needs to branch on
// MarkerOnly; it exists purely so callers/tests can tell a real override from
// a default-marker entry when inspecting the store directly.
type Override struct {
	UserID     int64  `json:"user_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model,omitempty"`
	APIKey     string `json:"api_key"`
	BaseURL    string `json:"base_url,omitempty"`
	MarkerOnly bool   `json:"marker_only,omitempty"`
}

// Store reads/writes per-node debug credential overrides in Redis.
type Store struct {
	client rueidis.Client
}

// New wraps a rueidis.Client.
func New(client rueidis.Client) *Store {
	return &Store{client: client}
}

func key(tenantID, runID, nodeID string) string {
	return fmt.Sprintf("them:debug:%s:%s:%s:llm_override", tenantID, runID, nodeID)
}

func keyPattern(tenantID, runID string) string {
	return fmt.Sprintf("them:debug:%s:%s:*:llm_override", tenantID, runID)
}

// Set writes ov for (tenantID, runID, nodeID), replacing any existing entry,
// with TTL. Does not consume on read — see the package doc for why.
func (s *Store) Set(ctx context.Context, tenantID, runID, nodeID string, ov Override) error {
	raw, err := json.Marshal(ov)
	if err != nil {
		return fmt.Errorf("debugcred: marshal override: %w", err)
	}
	cmd := s.client.B().Set().Key(key(tenantID, runID, nodeID)).Value(string(raw)).Ex(TTL).Build()
	return s.client.Do(ctx, cmd).Error()
}

// Get returns the override for (tenantID, runID, nodeID), and whether one
// was found. A read that returns found=false is NOT the same as "no
// credential was ever requested" — see the package doc's absence-means-
// missing note. Callers that know a node declared a required credential
// param must treat found=false as a hard failure, never a silent fallback.
func (s *Store) Get(ctx context.Context, tenantID, runID, nodeID string) (Override, bool, error) {
	cmd := s.client.B().Get().Key(key(tenantID, runID, nodeID)).Build()
	res := s.client.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return Override{}, false, nil
		}
		return Override{}, false, err
	}
	raw, err := res.AsBytes()
	if err != nil {
		return Override{}, false, err
	}
	var ov Override
	if err := json.Unmarshal(raw, &ov); err != nil {
		return Override{}, false, fmt.Errorf("debugcred: unmarshal override: %w", err)
	}
	return ov, true, nil
}

// Delete removes the override for (tenantID, runID, nodeID).
func (s *Store) Delete(ctx context.Context, tenantID, runID, nodeID string) error {
	cmd := s.client.B().Del().Key(key(tenantID, runID, nodeID)).Build()
	return s.client.Do(ctx, cmd).Error()
}

// DeleteAllForRun removes every node's override for (tenantID, runID) via a
// cursor-based SCAN + DEL (never KEYS, which blocks the whole Redis instance
// on a large keyspace) — called proactively once a debug run reaches a
// terminal state (AppFlowActivities.FinalizeRunActivity, gated on
// input.Debug). This is the primary cleanup path; TTL (see TTL) is strictly
// the fallback for a run that's abandoned or crashes before reaching one.
// Best-effort: a scan/delete failure is returned but the caller (the
// idempotent FinalizeRunActivity) must not fail the whole activity over it —
// see the call site's own reasoning for why the run's terminal status update
// takes priority over this cleanup succeeding.
func (s *Store) DeleteAllForRun(ctx context.Context, tenantID, runID string) error {
	pattern := keyPattern(tenantID, runID)
	var cursor uint64
	for {
		cmd := s.client.B().Scan().Cursor(cursor).Match(pattern).Count(100).Build()
		entry, err := s.client.Do(ctx, cmd).AsScanEntry()
		if err != nil {
			return fmt.Errorf("debugcred: scan for run cleanup: %w", err)
		}
		if len(entry.Elements) > 0 {
			delCmd := s.client.B().Del().Key(entry.Elements...).Build()
			if err := s.client.Do(ctx, delCmd).Error(); err != nil {
				return fmt.Errorf("debugcred: delete during run cleanup: %w", err)
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			return nil
		}
	}
}
