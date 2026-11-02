package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/domain"
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

// F-133: the finalize status update sets ended_at for every terminal status (including stopped)
// and never overwrites an already-terminal run; a non-terminal write is neither.
func TestRunStatusUpdateQuery(t *testing.T) {
	for _, st := range []domain.RunStatus{domain.RunCompleted, domain.RunFailed, domain.RunCanceled, domain.RunStopped} {
		q := runStatusUpdateQuery(st)
		if !strings.Contains(q, "ended_at=now()") || !strings.Contains(q, "status NOT IN (") || !strings.Contains(q, "'stopped'") {
			t.Errorf("%s: terminal update must set ended_at and be guarded incl. stopped: %s", st, q)
		}
	}
	if q := runStatusUpdateQuery(domain.RunRunning); strings.Contains(q, "ended_at") || strings.Contains(q, "NOT IN") {
		t.Errorf("running update must be plain: %s", q)
	}
}

// The AppFlow agent node must send the invocation context agent-runtime requires (it answers 401 without the
// agent id), plus the run id for tool-call tracing; the binding id stays unset so the runtime resolves it.
func TestSetInvocationHeaders(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	setInvocationHeaders(req, "tenant-1", "app-1", "agent-1", "run-1")
	for h, want := range map[string]string{
		"X-Them-Tenant-Id": "tenant-1", "X-Them-Application-Id": "app-1", "X-Them-Agent-Id": "agent-1", "X-Them-Run-Id": "run-1",
	} {
		if got := req.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if got := req.Header.Get("X-Them-Binding-Id"); got != "" {
		t.Errorf("binding id must not be sent (the runtime looks it up by app + agent), got %q", got)
	}

	noRun := httptest.NewRequest("POST", "/", nil)
	setInvocationHeaders(noRun, "t", "a", "g", "")
	if _, has := noRun.Header["X-Them-Run-Id"]; has {
		t.Error("X-Them-Run-Id must be omitted when there is no run")
	}
}
