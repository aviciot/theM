//go:build integration

package dal_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
)

// Phase 2 of docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md — ExportApplication/
// ImportApplication round-trip: export a fully-populated source app to an
// in-memory envelope (the same shape that would be written to a JSON file),
// then import that envelope into a DIFFERENT tenant with no live source app
// reachable — proves the envelope alone carries everything needed, not just
// that DeployApplication's live DB-to-DB path (Phase 1) works.

func TestDAL_ExportImportApplication_RoundTrip(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	srcTenantID, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-export-import-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-export-import-target")

	var agentIDsToClean []string
	seedAgent := func(tenantID, slugPrefix string) string {
		t.Helper()
		var id, slug string
		if err := pool.QueryRow(ctx,
			`INSERT INTO them.component_definitions
			    (kind, namespace, name, version, display_name, implementation_type, scope, tenant_id, status, content_hash)
			 VALUES ('agent', 'default', $1 || '_' || substr(md5(random()::text), 1, 8), 1, $1, 'a2a_async', 'tenant', $2::uuid, 'published', 'inttest-hash')
			 RETURNING id::text, name`,
			slugPrefix, tenantID).Scan(&id, &slug); err != nil {
			t.Fatalf("seed component_definitions for agent %q: %v", slugPrefix, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO them.agents (id, tenant_id, slug, namespace, transport) VALUES ($1::uuid, $2::uuid, $3, 'default', 'a2a_async')`,
			id, tenantID, slug); err != nil {
			t.Fatalf("seed agent %q: %v", slug, err)
		}
		agentIDsToClean = append(agentIDsToClean, id)
		return id
	}
	srcAgentID := seedAgent(srcTenantID, "inttest_export_import_agent")

	// A real orchestrator referencing the agent, and an entry point pointing
	// at that orchestrator — proves node_id-keyed re-linking survives the
	// export/import round trip, not just table-by-table copying.
	var orchID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO them.app_orchestrators (application_id, node_id, kind, name, display_name, system_prompt, allowed_agent_ids)
		 VALUES ($1::uuid, 'orch_1', 'standard', 'test-orchestrator', 'Test Orchestrator', 'you are a test orchestrator', $2::uuid[])
		 RETURNING id::text`,
		srcAppID, []string{srcAgentID}).Scan(&orchID); err != nil {
		t.Fatalf("seed app_orchestrators: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.entry_points (application_id, tenant_id, slug, entry_point_type, app_orchestrator_id)
		 VALUES ($1::uuid, $2::uuid, 'inttest-ep', 'websocket', $3::uuid)`,
		srcAppID, srcTenantID, orchID); err != nil {
		t.Fatalf("seed entry_points: %v", err)
	}

	var defID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM them.middleware_defs WHERE slug = 'file-guard' LIMIT 1`,
	).Scan(&defID); err != nil {
		t.Fatalf("look up file-guard def: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.middleware_wirings (application_id, agent_id, def_id, node_id, enabled, config_override)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'agent_1', true, '{"mode":"block"}'::jsonb)`,
		srcAppID, srcAgentID, defID); err != nil {
		t.Fatalf("seed middleware_wirings: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_agent_bindings (application_id, agent_id, credential_bindings, config_overrides, policies)
		 VALUES ($1::uuid, $2::uuid, '{"api_key":{"ct":"enc:secret"}}'::jsonb, '{"timeout":30}'::jsonb, '{"rate_limit":10}'::jsonb)`,
		srcAppID, srcAgentID); err != nil {
		t.Fatalf("seed app_agent_bindings: %v", err)
	}

	var mcpServerID, mcpServerSlug string
	if err := pool.QueryRow(ctx, `SELECT id::text, slug FROM them.mcp_servers LIMIT 1`).Scan(&mcpServerID, &mcpServerSlug); err != nil {
		t.Skipf("no mcp_servers row available: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id, credential_encrypted, auth_header_name)
		 VALUES ($1::uuid, $2::uuid, 'enc:supersecret', 'X-Custom-Auth')`,
		srcAppID, mcpServerID); err != nil {
		t.Fatalf("seed app_mcp_credentials: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_flow_llm_overrides (application_id, node_id, provider, model)
		 VALUES ($1::uuid, 'llm_1', 'anthropic', 'claude-sonnet-5')`,
		srcAppID); err != nil {
		t.Fatalf("seed app_flow_llm_overrides: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_temporal_config (application_id, max_concurrent_workflows, workflow_timeout_s, activity_timeout_s, retry_max_attempts)
		 VALUES ($1::uuid, 5, 300, 60, 3)`,
		srcAppID); err != nil {
		t.Fatalf("seed app_temporal_config: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_debug_config (application_id, log_verbosity) VALUES ($1::uuid, 'full')`,
		srcAppID); err != nil {
		t.Fatalf("seed app_debug_config: %v", err)
	}

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)

	// ── Export ──────────────────────────────────────────────────────────────
	env, err := d.ExportApplication(ctx, srcAppID)
	if err != nil {
		t.Fatalf("ExportApplication: %v", err)
	}
	if env.ExportVersion != dal.ExportFileFormatVersion {
		t.Errorf("ExportVersion = %d, want %d", env.ExportVersion, dal.ExportFileFormatVersion)
	}
	if len(env.Agents) != 1 || env.Agents[0].OldID != srcAgentID {
		t.Fatalf("env.Agents = %+v, want exactly 1 entry for %s", env.Agents, srcAgentID)
	}
	if len(env.AppOrchestrators) != 1 {
		t.Fatalf("env.AppOrchestrators count = %d, want 1", len(env.AppOrchestrators))
	}
	if len(env.EntryPoints) != 1 {
		t.Fatalf("env.EntryPoints count = %d, want 1", len(env.EntryPoints))
	}

	// Round-trip through JSON marshal/unmarshal — proves the envelope is
	// actually a valid, serializable file shape, not just a Go-side struct
	// that happens to work in-process.
	fileBytes, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var envFromFile dal.ExportedApp
	if err := json.Unmarshal(fileBytes, &envFromFile); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	// Secrets must never appear in the file bytes at all.
	fileStr := string(fileBytes)
	for _, secret := range []string{"enc:secret", "enc:supersecret"} {
		if strings.Contains(fileStr, secret) {
			t.Errorf("exported file contains secret ciphertext %q — must never be written to the export file", secret)
		}
	}

	// ── Import into a DIFFERENT tenant, with the source app's tenant/app
	// deleted first so there is genuinely no live source to fall back to. ──
	if _, err := pool.Exec(ctx, `DELETE FROM them.applications WHERE id = $1::uuid`, srcAppID); err != nil {
		t.Fatalf("delete source app before import (to prove no live source is needed): %v", err)
	}

	imported, err := d.ImportApplication(ctx, envFromFile, targetTenantID)
	if err != nil {
		t.Fatalf("ImportApplication: %v", err)
	}
	// ImportApplication inserts a brand-new agent (+ component_definition) in
	// the target tenant, since it has no matching agent to reuse — that new
	// agent's id is captured here (not just agentIDsToClean, which only holds
	// the ORIGINAL seeded source-side agent) so cleanup doesn't leak it, same
	// LIFO-ordering fix as docs/LESSONS.md's 2026-09-28 "t.Cleanup" entry:
	// delete the referencing application row(s) FIRST, then the agents.
	var importedAgentIDs []string
	rows, err := pool.Query(ctx, `SELECT id::text FROM them.agents WHERE tenant_id = $1::uuid`, targetTenantID)
	if err != nil {
		t.Fatalf("query target tenant agents for cleanup tracking: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			importedAgentIDs = append(importedAgentIDs, id)
		}
	}
	rows.Close()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, imported.ID) //nolint:errcheck
		for _, id := range agentIDsToClean {
			pool.Exec(context.Background(), `DELETE FROM them.agents WHERE id = $1::uuid`, id)                //nolint:errcheck
			pool.Exec(context.Background(), `DELETE FROM them.component_definitions WHERE id = $1::uuid`, id) //nolint:errcheck
		}
		for _, id := range importedAgentIDs {
			pool.Exec(context.Background(), `DELETE FROM them.agents WHERE id = $1::uuid`, id)                //nolint:errcheck
			pool.Exec(context.Background(), `DELETE FROM them.component_definitions WHERE id = $1::uuid`, id) //nolint:errcheck
		}
	})

	// ── Verify the imported app carries everything ─────────────────────────
	var gotName string
	if err := pool.QueryRow(ctx, `SELECT name FROM them.applications WHERE id = $1::uuid`, imported.ID).Scan(&gotName); err != nil {
		t.Fatalf("query imported application: %v", err)
	}

	var orchCount int
	var newAgentID *string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), (array_agg(agent_id::text))[1] FROM (SELECT unnest(allowed_agent_ids) AS agent_id FROM them.app_orchestrators WHERE application_id = $1::uuid) t`,
		imported.ID,
	).Scan(&orchCount, &newAgentID); err != nil {
		t.Fatalf("query imported app_orchestrators: %v", err)
	}
	if orchCount != 1 || newAgentID == nil {
		t.Fatalf("imported orchestrator's allowed_agent_ids = count %d, %v — want exactly 1 remapped agent id", orchCount, newAgentID)
	}
	if *newAgentID == srcAgentID {
		t.Errorf("imported orchestrator still references the OLD agent id %s — remap did not happen", srcAgentID)
	}

	var epCount int
	var epOrchID *string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), (array_agg(app_orchestrator_id::text))[1] FROM them.entry_points WHERE application_id = $1::uuid`,
		imported.ID,
	).Scan(&epCount, &epOrchID); err != nil {
		t.Fatalf("query imported entry_points: %v", err)
	}
	if epCount != 1 || epOrchID == nil {
		t.Fatalf("imported entry_points = count %d, app_orchestrator_id %v — want 1 row correctly re-linked to the new orchestrator", epCount, epOrchID)
	}

	var wiringCount int
	var wiringAgentID *string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), (array_agg(agent_id::text))[1] FROM them.middleware_wirings WHERE application_id = $1::uuid`,
		imported.ID,
	).Scan(&wiringCount, &wiringAgentID); err != nil {
		t.Fatalf("query imported middleware_wirings: %v", err)
	}
	if wiringCount != 1 || wiringAgentID == nil || *wiringAgentID == srcAgentID {
		t.Errorf("imported middleware_wirings = count %d, agent_id %v — want 1 row with agent_id remapped away from %s", wiringCount, wiringAgentID, srcAgentID)
	}

	var credCount int
	var credEnc *string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), (array_agg(credential_encrypted))[1] FROM them.app_mcp_credentials WHERE application_id = $1::uuid`,
		imported.ID,
	).Scan(&credCount, &credEnc); err != nil {
		t.Fatalf("query imported app_mcp_credentials: %v", err)
	}
	if credCount != 1 {
		t.Fatalf("imported app_mcp_credentials count = %d, want 1 (matched by mcp_server_slug %q)", credCount, mcpServerSlug)
	}
	if credEnc != nil {
		t.Errorf("imported app_mcp_credentials.credential_encrypted = %v, want NULL (secret must not travel)", *credEnc)
	}

	var provider, model string
	if err := pool.QueryRow(ctx,
		`SELECT provider, model FROM them.app_flow_llm_overrides WHERE application_id = $1::uuid AND node_id = 'llm_1'`,
		imported.ID).Scan(&provider, &model); err != nil {
		t.Fatalf("query imported app_flow_llm_overrides: %v", err)
	}
	if provider != "anthropic" || model != "claude-sonnet-5" {
		t.Errorf("imported app_flow_llm_overrides = (%s, %s), want (anthropic, claude-sonnet-5)", provider, model)
	}

	var verbosity string
	if err := pool.QueryRow(ctx,
		`SELECT log_verbosity FROM them.app_debug_config WHERE application_id = $1::uuid`,
		imported.ID).Scan(&verbosity); err != nil {
		t.Fatalf("query imported app_debug_config: %v", err)
	}
	if verbosity != "full" {
		t.Errorf("imported app_debug_config.log_verbosity = %q, want %q", verbosity, "full")
	}
}
