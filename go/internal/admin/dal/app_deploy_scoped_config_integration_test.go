//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
)

// Phase 1 of docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md — DeployApplication
// previously silently dropped middleware_wirings (File Guard/PII/prompt-inject
// config), app_agent_bindings, app_mcp_credentials, app_flow_llm_overrides,
// app_temporal_config, and app_debug_config on every cross-tenant deploy.
// These tests prove copyAppScopedConfigForDeploy (called from within
// DeployApplication) actually copies each one, remaps agent_id where present,
// and never copies the credential columns that must stay behind.

func TestDAL_DeployApplication_CopiesScopedConfig(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	srcTenantID, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-deploy-scoped-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-deploy-scoped-target")

	// Seed an agent in the source tenant, reused (not copied) in the target —
	// simulates the common case where CopyAgentsForDeploy already resolved an
	// existing target-tenant agent and produced an agentIDMap entry.
	// agents.id shares its PK with component_definitions.id (fk_agents_base_def),
	// so a minimal agent needs a matching component_definitions row first.
	// slug carries a random suffix (not just the fixed test name) so a leftover
	// row from a previous failed/killed run — under the same fixed tenant, per
	// setupAppScopedConfigApp's ON CONFLICT DO UPDATE reuse — can never collide
	// with component_definitions_tenant_unique on this test's own insert.
	//
	// Cleanup for these agent rows is registered explicitly AFTER the deployed
	// app's own cleanup further below (see agentIDsToClean), not inside this
	// helper — t.Cleanup runs LIFO, and middleware_wirings/app_agent_bindings
	// rows referencing srcAgentID/targetAgentID must be gone (via the apps'
	// cascade deletes) BEFORE the agents themselves are deleted, or the DELETE
	// silently no-ops on an FK violation that a bare //nolint:errcheck swallows
	// — found live: agent rows were leaking on every single run until this was
	// fixed, confirmed by re-running 3x and checking for leftover rows after.
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
	srcAgentID := seedAgent(srcTenantID, "inttest_deploy_scoped_agent_src")
	targetAgentID := seedAgent(targetTenantID, "inttest_deploy_scoped_agent_target")
	agentIDMap := map[string]string{srcAgentID: targetAgentID}

	// A fixed platform middleware_defs row — file-guard.
	var defID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM them.middleware_defs WHERE slug = 'file-guard' LIMIT 1`,
	).Scan(&defID); err != nil {
		t.Fatalf("look up file-guard def: %v", err)
	}

	// Seed middleware_wirings: one agent-scoped row (needs remap), one llm-node
	// row with no agent_id at all (must not error on remap).
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.middleware_wirings (application_id, agent_id, def_id, node_id, enabled, config_override)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'agent_1', true, '{"mode":"block"}'::jsonb)`,
		srcAppID, srcAgentID, defID); err != nil {
		t.Fatalf("seed middleware_wirings (agent-scoped): %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.middleware_wirings (application_id, def_id, node_id, enabled, config_override)
		 VALUES ($1::uuid, $2::uuid, 'llm_1', true, '{"mode":"warn"}'::jsonb)`,
		srcAppID, defID); err != nil {
		t.Fatalf("seed middleware_wirings (llm-node, no agent): %v", err)
	}

	// app_agent_bindings: credential_bindings must NOT travel.
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_agent_bindings (application_id, agent_id, credential_bindings, config_overrides, policies)
		 VALUES ($1::uuid, $2::uuid, '{"api_key":{"ct":"enc:secret"}}'::jsonb, '{"timeout":30}'::jsonb, '{"rate_limit":10}'::jsonb)`,
		srcAppID, srcAgentID); err != nil {
		t.Fatalf("seed app_agent_bindings: %v", err)
	}

	// app_mcp_credentials: credential_encrypted must NOT travel, binding must.
	var mcpServerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM them.mcp_servers LIMIT 1`).Scan(&mcpServerID); err != nil {
		t.Skipf("no mcp_servers row available to seed app_mcp_credentials: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id, credential_encrypted, auth_header_name)
		 VALUES ($1::uuid, $2::uuid, 'enc:supersecret', 'X-Custom-Auth')`,
		srcAppID, mcpServerID); err != nil {
		t.Fatalf("seed app_mcp_credentials: %v", err)
	}

	// app_flow_llm_overrides.
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_flow_llm_overrides (application_id, node_id, provider, model)
		 VALUES ($1::uuid, 'llm_1', 'anthropic', 'claude-sonnet-5')`,
		srcAppID); err != nil {
		t.Fatalf("seed app_flow_llm_overrides: %v", err)
	}

	// app_temporal_config.
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_temporal_config (application_id, max_concurrent_workflows, workflow_timeout_s, activity_timeout_s, retry_max_attempts)
		 VALUES ($1::uuid, 5, 300, 60, 3)`,
		srcAppID); err != nil {
		t.Fatalf("seed app_temporal_config: %v", err)
	}

	// app_debug_config.
	if _, err := pool.Exec(ctx,
		`INSERT INTO them.app_debug_config (application_id, log_verbosity) VALUES ($1::uuid, 'full')`,
		srcAppID); err != nil {
		t.Fatalf("seed app_debug_config: %v", err)
	}

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)
	deployed, err := d.DeployApplication(ctx, srcAppID, targetTenantID, agentIDMap)
	if err != nil {
		t.Fatalf("DeployApplication: %v", err)
	}
	// Registered LAST among this test's own cleanups, so — per t.Cleanup's LIFO
	// order — it runs FIRST, deleting the deployed app (cascading away its
	// copied middleware_wirings/app_agent_bindings rows) before the agent
	// cleanup below runs. srcAppID's own referencing rows are removed here too
	// (not just cascade — explicit, since srcAppID itself is cleaned up by
	// setupAppScopedConfigApp separately and that cleanup is registered even
	// earlier, meaning it would otherwise run LAST — after the agents are
	// already gone).
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, deployed.ID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM them.middleware_wirings WHERE application_id = $1::uuid`, srcAppID)   //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM them.app_agent_bindings WHERE application_id = $1::uuid`, srcAppID) //nolint:errcheck
		for _, id := range agentIDsToClean {
			pool.Exec(context.Background(), `DELETE FROM them.agents WHERE id = $1::uuid`, id)                //nolint:errcheck
			pool.Exec(context.Background(), `DELETE FROM them.component_definitions WHERE id = $1::uuid`, id) //nolint:errcheck
		}
	})

	// middleware_wirings: 2 rows, agent_id remapped, node_id preserved, def_id unchanged.
	rows, err := pool.Query(ctx,
		`SELECT node_id, agent_id, def_id, enabled, config_override FROM them.middleware_wirings WHERE application_id = $1::uuid ORDER BY node_id`,
		deployed.ID)
	if err != nil {
		t.Fatalf("query deployed middleware_wirings: %v", err)
	}
	type wiringRow struct {
		nodeID  string
		agentID *string
		defID   string
		enabled bool
		cfg     []byte
	}
	var wirings []wiringRow
	for rows.Next() {
		var w wiringRow
		if err := rows.Scan(&w.nodeID, &w.agentID, &w.defID, &w.enabled, &w.cfg); err != nil {
			t.Fatalf("scan middleware_wirings: %v", err)
		}
		wirings = append(wirings, w)
	}
	rows.Close()
	if len(wirings) != 2 {
		t.Fatalf("deployed middleware_wirings count = %d, want 2", len(wirings))
	}
	if wirings[0].nodeID != "agent_1" || wirings[0].agentID == nil || *wirings[0].agentID != targetAgentID {
		t.Errorf("agent_1 wiring agent_id not remapped to target: %+v", wirings[0])
	}
	if wirings[1].nodeID != "llm_1" || wirings[1].agentID != nil {
		t.Errorf("llm_1 wiring should have no agent_id (llm node), got: %+v", wirings[1])
	}

	// app_agent_bindings: credential_bindings must be empty, config/policies preserved.
	var credJSON, cfgJSON, policiesJSON []byte
	var boundAgentID string
	if err := pool.QueryRow(ctx,
		`SELECT agent_id::text, credential_bindings, config_overrides, policies FROM them.app_agent_bindings WHERE application_id = $1::uuid`,
		deployed.ID).Scan(&boundAgentID, &credJSON, &cfgJSON, &policiesJSON); err != nil {
		t.Fatalf("query deployed app_agent_bindings: %v", err)
	}
	if boundAgentID != targetAgentID {
		t.Errorf("app_agent_bindings.agent_id = %s, want remapped %s", boundAgentID, targetAgentID)
	}
	if string(credJSON) != "{}" {
		t.Errorf("app_agent_bindings.credential_bindings = %s, want {} (secrets must not travel)", credJSON)
	}
	if string(cfgJSON) != `{"timeout": 30}` && string(cfgJSON) != `{"timeout":30}` {
		t.Errorf("app_agent_bindings.config_overrides = %s, want {\"timeout\":30} preserved", cfgJSON)
	}

	// app_mcp_credentials: credential_encrypted must be NULL, auth_header_name preserved.
	var authHeader string
	var credEnc *string
	if err := pool.QueryRow(ctx,
		`SELECT auth_header_name, credential_encrypted FROM them.app_mcp_credentials WHERE application_id = $1::uuid`,
		deployed.ID).Scan(&authHeader, &credEnc); err != nil {
		t.Fatalf("query deployed app_mcp_credentials: %v", err)
	}
	if authHeader != "X-Custom-Auth" {
		t.Errorf("app_mcp_credentials.auth_header_name = %q, want preserved", authHeader)
	}
	if credEnc != nil {
		t.Errorf("app_mcp_credentials.credential_encrypted = %v, want NULL (secret must not travel)", *credEnc)
	}

	// app_flow_llm_overrides.
	var provider, model string
	if err := pool.QueryRow(ctx,
		`SELECT provider, model FROM them.app_flow_llm_overrides WHERE application_id = $1::uuid AND node_id = 'llm_1'`,
		deployed.ID).Scan(&provider, &model); err != nil {
		t.Fatalf("query deployed app_flow_llm_overrides: %v", err)
	}
	if provider != "anthropic" || model != "claude-sonnet-5" {
		t.Errorf("app_flow_llm_overrides = (%s, %s), want (anthropic, claude-sonnet-5)", provider, model)
	}

	// app_temporal_config.
	var maxConc int
	if err := pool.QueryRow(ctx,
		`SELECT max_concurrent_workflows FROM them.app_temporal_config WHERE application_id = $1::uuid`,
		deployed.ID).Scan(&maxConc); err != nil {
		t.Fatalf("query deployed app_temporal_config: %v", err)
	}
	if maxConc != 5 {
		t.Errorf("app_temporal_config.max_concurrent_workflows = %d, want 5", maxConc)
	}

	// app_debug_config.
	var verbosity string
	if err := pool.QueryRow(ctx,
		`SELECT log_verbosity FROM them.app_debug_config WHERE application_id = $1::uuid`,
		deployed.ID).Scan(&verbosity); err != nil {
		t.Fatalf("query deployed app_debug_config: %v", err)
	}
	if verbosity != "full" {
		t.Errorf("app_debug_config.log_verbosity = %q, want %q", verbosity, "full")
	}
}

func TestDAL_DeployApplication_NoScopedConfig_DoesNotError(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	_, srcAppID := setupAppScopedConfigApp(t, pool, "inttest-deploy-scoped-empty-src")
	targetTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-deploy-scoped-empty-target")

	d := dal.NewDBWithPool(newPgxQuerier(pool), pool)
	deployed, err := d.DeployApplication(ctx, srcAppID, targetTenantID, nil)
	if err != nil {
		t.Fatalf("DeployApplication with no scoped config rows: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, deployed.ID) //nolint:errcheck
	})
}
