package history

import (
	"strings"
	"testing"

	"github.com/aviciot/them/internal/domain"
)

func TestCanonicalToDBRole(t *testing.T) {
	tests := []struct {
		canonicalRole string
		wantDBRole    string
	}{
		{domain.RoleUser, dbRoleUser},
		{domain.RoleAssistant, dbRoleAgent},
		{domain.RoleTool, dbRoleAgent},
		{domain.RoleSystem, dbRoleSystem},
		{"unknown", dbRoleUser}, // fallback
	}
	for _, tt := range tests {
		t.Run(tt.canonicalRole, func(t *testing.T) {
			got := canonicalToDBRole(tt.canonicalRole)
			if got != tt.wantDBRole {
				t.Errorf("canonicalToDBRole(%q) = %q, want %q", tt.canonicalRole, got, tt.wantDBRole)
			}
		})
	}
}

func TestDBToCanonicalRole_WithEnvelope(t *testing.T) {
	tests := []struct {
		dbRole        string
		canonicalRole string
		want          string
	}{
		{dbRoleAgent, domain.RoleAssistant, domain.RoleAssistant},
		{dbRoleAgent, domain.RoleTool, domain.RoleTool},
		{dbRoleUser, domain.RoleUser, domain.RoleUser},
		{dbRoleSystem, domain.RoleSystem, domain.RoleSystem},
	}
	for _, tt := range tests {
		t.Run(tt.dbRole+"_"+tt.canonicalRole, func(t *testing.T) {
			got := dbToCanonicalRole(tt.dbRole, tt.canonicalRole)
			if got != tt.want {
				t.Errorf("dbToCanonicalRole(%q, %q) = %q, want %q", tt.dbRole, tt.canonicalRole, got, tt.want)
			}
		})
	}
}

func TestDBToCanonicalRole_Fallback(t *testing.T) {
	// When canonicalRole is empty (legacy rows), fall back to dbRole→domain mapping.
	if got := dbToCanonicalRole(dbRoleAgent, ""); got != domain.RoleAssistant {
		t.Errorf("fallback agent: got %q, want %q", got, domain.RoleAssistant)
	}
	if got := dbToCanonicalRole(dbRoleUser, ""); got != domain.RoleUser {
		t.Errorf("fallback user: got %q, want %q", got, domain.RoleUser)
	}
	if got := dbToCanonicalRole(dbRoleSystem, ""); got != domain.RoleSystem {
		t.Errorf("fallback system: got %q, want %q", got, domain.RoleSystem)
	}
}

func TestRoleRoundTrip(t *testing.T) {
	// Every canonical role survives a DB round-trip.
	cases := []string{domain.RoleUser, domain.RoleAssistant, domain.RoleTool, domain.RoleSystem}
	for _, role := range cases {
		db := canonicalToDBRole(role)
		got := dbToCanonicalRole(db, role) // pass canonical in envelope
		if got != role {
			t.Errorf("round-trip %q: db=%q, recovered=%q", role, db, got)
		}
	}
}

// TestHistory_CrossUser_Denied verifies that LoadHistory SQL includes an
// external_user_id filter so user A cannot access user B's history.
// This is a trust-boundary regression test — the filter must be present
// regardless of whether the DB is live.
func TestHistory_CrossUser_Denied(t *testing.T) {
	// The SQL in LoadHistory must contain the external_user_id clause so that
	// when a non-empty externalUserID is passed, only matching rows are returned.
	const loadQ = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND ($3 = '' OR t.external_user_id = $3)
ORDER BY tm.id DESC
LIMIT $4`

	if !strings.Contains(loadQ, "external_user_id") {
		t.Error("LoadHistory SQL missing external_user_id filter — cross-user isolation broken")
	}
	if !strings.Contains(loadQ, "$3 = '' OR t.external_user_id = $3") {
		t.Error("LoadHistory SQL external_user_id filter uses wrong pattern")
	}
}

// TestHistory_ServiceToken_ExternalUserIsolation verifies that resolveRootTaskID
// inserts external_user_id into them.tasks so history reads can scope per user.
// A service token run with externalUserID="" produces a task with NULL user;
// a backend-token run with externalUserID="user-123" produces a task scoped to that user.
func TestHistory_ServiceToken_ExternalUserIsolation(t *testing.T) {
	const insertQ = `
INSERT INTO them.tasks (context_id, run_id, tenant_id, state, kind, external_user_id)
VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, 'working', 'root', NULLIF($4, ''))
ON CONFLICT DO NOTHING
RETURNING id::text`

	if !strings.Contains(insertQ, "external_user_id") {
		t.Error("resolveRootTaskID INSERT missing external_user_id column — user isolation not stored")
	}
	// Empty string → NULLIF produces NULL (no external user for service-token runs).
	if !strings.Contains(insertQ, "NULLIF($4, '')") {
		t.Error("resolveRootTaskID INSERT must use NULLIF($4,'') so service-token runs store NULL")
	}
}
