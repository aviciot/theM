//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
)

// docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 4: an llm canvas node has no agent
// at all, so middleware_wirings.agent_id had to become nullable (db/114) —
// these tests prove a wiring with no agent_id can actually be created,
// listed, and re-saved without a real Postgres constraint rejecting it or
// an inner JOIN silently hiding it, none of which a fake Querier can verify.

func TestDAL_CreateMiddlewareWiring_NoAgentID_Succeeds(t *testing.T) {
	pool := integrationPool(t)
	_, appID := setupAppScopedConfigApp(t, pool, "inttest-mw-noagent")
	q := newPgxQuerier(pool)

	w, err := dal.CreateMiddlewareWiring(context.Background(), q, appID, dal.MiddlewareWiringInput{
		DefSlug: "pii_redact",
		NodeID:  "llm_1",
	})
	if err != nil {
		t.Fatalf("CreateMiddlewareWiring with no AgentID: %v", err)
	}
	if w.AgentID != "" {
		t.Errorf("AgentID = %q, want empty for an llm-node wiring", w.AgentID)
	}
	if w.NodeID != "llm_1" {
		t.Errorf("NodeID = %q, want %q", w.NodeID, "llm_1")
	}
}

func TestDAL_ListMiddlewareWirings_IncludesNoAgentRows(t *testing.T) {
	pool := integrationPool(t)
	_, appID := setupAppScopedConfigApp(t, pool, "inttest-mw-list-noagent")
	q := newPgxQuerier(pool)

	created, err := dal.CreateMiddlewareWiring(context.Background(), q, appID, dal.MiddlewareWiringInput{
		DefSlug: "prompt_inject",
		NodeID:  "llm_2",
	})
	if err != nil {
		t.Fatalf("CreateMiddlewareWiring: %v", err)
	}

	list, err := dal.ListMiddlewareWirings(context.Background(), q, appID)
	if err != nil {
		t.Fatalf("ListMiddlewareWirings: %v", err)
	}
	found := false
	for _, w := range list {
		if w.ID == created.ID {
			found = true
			if w.AgentID != "" {
				t.Errorf("listed wiring AgentID = %q, want empty", w.AgentID)
			}
		}
	}
	if !found {
		t.Fatal("the no-agent wiring was not present in ListMiddlewareWirings — an inner JOIN would silently exclude it")
	}
}

// TestDAL_CreateMiddlewareWiring_ResaveSameNode_UpdatesInPlace proves the
// ON CONFLICT ON CONSTRAINT uq_mw_wiring_app_node upsert (keyed on node_id,
// not agent_id — db/114 changed this) actually resolves to the same row on
// a second Create call for the same node, rather than erroring or silently
// creating a duplicate.
func TestDAL_CreateMiddlewareWiring_ResaveSameNode_UpdatesInPlace(t *testing.T) {
	pool := integrationPool(t)
	_, appID := setupAppScopedConfigApp(t, pool, "inttest-mw-resave")
	q := newPgxQuerier(pool)

	first, err := dal.CreateMiddlewareWiring(context.Background(), q, appID, dal.MiddlewareWiringInput{
		DefSlug: "pii_redact",
		NodeID:  "llm_3",
		Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("first CreateMiddlewareWiring: %v", err)
	}

	second, err := dal.CreateMiddlewareWiring(context.Background(), q, appID, dal.MiddlewareWiringInput{
		DefSlug: "pii_redact",
		NodeID:  "llm_3",
		Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("second CreateMiddlewareWiring (re-save) should upsert, not error: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-save created a new row (id %q) instead of updating the existing one (id %q)", second.ID, first.ID)
	}
	if !second.Enabled {
		t.Error("re-save should have updated enabled=true")
	}
}

// TestDAL_UpdateMiddlewareWiring_ConfigOnlySave_PreservesNodeID is a
// regression test for a real bug found live this session: a config-only
// update (the App Canvas Guards panel's "Save Guard Config" never sends
// node_id at all — see AgentGuardsSection.tsx's handleSaveConfig) was
// silently wiping the wiring's node_id to NULL on every single save,
// because UpdateMiddlewareWiring's SQL unconditionally set
// node_id = CASE WHEN $6 = '' THEN NULL ELSE $6 END regardless of whether
// the caller actually intended to clear it. Confirmed live: creating a
// wiring, then updating only its config_override, then re-reading it
// showed node_id had become empty.
func TestDAL_UpdateMiddlewareWiring_ConfigOnlySave_PreservesNodeID(t *testing.T) {
	pool := integrationPool(t)
	_, appID := setupAppScopedConfigApp(t, pool, "inttest-mw-update-preserve")
	q := newPgxQuerier(pool)

	created, err := dal.CreateMiddlewareWiring(context.Background(), q, appID, dal.MiddlewareWiringInput{
		DefSlug: "pii_redact",
		NodeID:  "llm_1",
		Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateMiddlewareWiring: %v", err)
	}
	if created.NodeID != "llm_1" {
		t.Fatalf("sanity check failed: created.NodeID = %q, want %q", created.NodeID, "llm_1")
	}

	// Config-only update — deliberately does NOT set NodeID, matching what
	// the real frontend call site actually sends.
	updated, err := dal.UpdateMiddlewareWiring(context.Background(), q, appID, created.ID, dal.MiddlewareWiringInput{
		ConfigOverride: []byte(`{"mode":"redact","direction":"both"}`),
		Enabled:        boolPtr(true),
	})
	if err != nil {
		t.Fatalf("UpdateMiddlewareWiring: %v", err)
	}
	if updated.NodeID != "llm_1" {
		t.Errorf("NodeID = %q after a config-only update, want it preserved as %q", updated.NodeID, "llm_1")
	}
}

func boolPtr(b bool) *bool { return &b }
