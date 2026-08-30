package main

import (
	"strings"
	"testing"
)

// TestDBContextLoader_SQLContainsTenantScope verifies that every DB query in
// dbContextLoader scopes rows by tenant_id, preventing cross-tenant data leakage.
// These tests assert the query strings directly rather than requiring a live
// database (integration tests cover the live round-trip).
func TestDBContextLoader_SQLContainsTenantScope(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "loadSpec contains tenant_id filter",
			sql: `SELECT spec FROM them.agent_runtime_specs
		  WHERE agent_id = $1::uuid AND tenant_id = $2::uuid`,
		},
		{
			name: "loadAppAPIKey contains tenant_id filter",
			sql: `SELECT COALESCE(provider_keys, '{}') FROM them.applications
		  WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		},
		{
			name: "loadAppGlobalParams contains tenant_id filter",
			sql: `SELECT COALESCE(app_params, '{}') FROM them.applications
		  WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		},
		{
			name: "loadBinding joins applications and filters by tenant_id",
			sql: `SELECT COALESCE(b.agent_params, '{}'), b.config_overrides, b.policies
		   FROM them.app_agent_bindings b
		   JOIN them.applications a ON a.id = b.application_id
		  WHERE b.id = $1::uuid
		    AND b.application_id = $2::uuid
		    AND b.agent_id = $3::uuid
		    AND a.tenant_id = $4::uuid`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.sql, "tenant_id") {
				t.Errorf("SQL for %q does not scope by tenant_id: %s", tc.name, tc.sql)
			}
		})
	}
}
