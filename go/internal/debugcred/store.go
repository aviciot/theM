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
)

// TTL is the override entry's expiry — long enough for any debug run's
// expected lifetime, short enough that a stale entry from an abandoned debug
// session can't outlive it by much. Proactive delete-on-terminal-event (see
// Delete) is the primary cleanup path; this TTL is the fallback.
const TTL = 10 * time.Minute

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

// Delete removes the override for (tenantID, runID, nodeID) — called
// proactively on run completion (the primary cleanup path; TTL is the
// fallback for runs that never reach a terminal event).
func (s *Store) Delete(ctx context.Context, tenantID, runID, nodeID string) error {
	cmd := s.client.B().Del().Key(key(tenantID, runID, nodeID)).Build()
	return s.client.Do(ctx, cmd).Error()
}
