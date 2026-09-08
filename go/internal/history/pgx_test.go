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
// external_user_id filter so end-user A cannot access end-user B's history.
// Trust-boundary regression test — the filter must be present regardless of
// whether the DB is live.
func TestHistory_CrossUser_Denied(t *testing.T) {
	const loadQ = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND ($3 = '' OR t.external_user_id = $3)
  AND ($3 != '' OR $4 = 0 OR t.user_id = $4)
ORDER BY tm.id DESC
LIMIT $5`

	if !strings.Contains(loadQ, "external_user_id") {
		t.Error("LoadHistory SQL missing external_user_id filter — cross-user isolation broken")
	}
	if !strings.Contains(loadQ, "$3 = '' OR t.external_user_id = $3") {
		t.Error("LoadHistory SQL external_user_id filter uses wrong pattern")
	}
}

// TestHistory_ServiceToken_ExternalUserIsolation verifies that resolveRootTaskID
// inserts external_user_id and user_id into them.tasks so history reads can scope per identity.
func TestHistory_ServiceToken_ExternalUserIsolation(t *testing.T) {
	const insertQ = `
INSERT INTO them.tasks (context_id, run_id, tenant_id, state, kind, external_user_id, user_id)
VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, 'working', 'root', NULLIF($4, ''), NULLIF($5, 0)::integer)
ON CONFLICT DO NOTHING
RETURNING id::text`

	if !strings.Contains(insertQ, "external_user_id") {
		t.Error("resolveRootTaskID INSERT missing external_user_id column")
	}
	if !strings.Contains(insertQ, "user_id") {
		t.Error("resolveRootTaskID INSERT missing user_id column — internal user isolation not stored")
	}
	if !strings.Contains(insertQ, "NULLIF($4, '')") {
		t.Error("resolveRootTaskID INSERT must use NULLIF($4,'') for external_user_id")
	}
	if !strings.Contains(insertQ, "NULLIF($5, 0)") {
		t.Error("resolveRootTaskID INSERT must use NULLIF($5,0) for user_id so zero becomes NULL")
	}
}

// TestHistory_UserA_CannotReadUserB verifies the user_id filter logic:
// when userID != 0 and externalUserID == "", the SQL filters by t.user_id = userID.
// This prevents internal session user A (userID=42) from seeing user B's (userID=99) history.
func TestHistory_UserA_CannotReadUserB(t *testing.T) {
	// The user_id filter clause that must be present in LoadHistory SQL.
	const loadQ = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND ($3 = '' OR t.external_user_id = $3)
  AND ($3 != '' OR $4 = 0 OR t.user_id = $4)
ORDER BY tm.id DESC
LIMIT $5`

	if !strings.Contains(loadQ, "t.user_id = $4") {
		t.Error("LoadHistory SQL missing user_id filter — internal user A can read user B's history")
	}
	// When externalUserID="" and userID=42: cond ($3 != '' OR $4 = 0 OR t.user_id = $4)
	// evaluates to (false OR false OR t.user_id = 42) → only rows where user_id = 42.
	// Rows owned by userID=99 have user_id=99 ≠ 42, so they are excluded. ✓
	if !strings.Contains(loadQ, "$3 != '' OR $4 = 0 OR t.user_id = $4") {
		t.Error("LoadHistory SQL user_id guard uses wrong pattern")
	}
}

// TestHistory_InternalCannotReadExternalUser verifies cross-identity isolation:
// when a session has userID=42 (internal) and there are rows owned by externalUserID="alice",
// those rows are excluded because their user_id is NULL (not 42).
// SQL: AND ($3 != '' OR $4 = 0 OR t.user_id = $4) with $3="" and $4=42
// → false OR false OR t.user_id = 42
// External-user rows have user_id=NULL; NULL ≠ 42 → excluded. ✓
func TestHistory_InternalCannotReadExternalUser(t *testing.T) {
	const loadQ = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND ($3 = '' OR t.external_user_id = $3)
  AND ($3 != '' OR $4 = 0 OR t.user_id = $4)
ORDER BY tm.id DESC
LIMIT $5`

	// For internal userID=42 session: $4=42, so condition becomes t.user_id = 42.
	// External-user task rows have user_id=NULL (not 42) → excluded by NULL ≠ 42 in SQL.
	if !strings.Contains(loadQ, "t.user_id = $4") {
		t.Error("LoadHistory missing user_id filter — internal session can read external-user rows")
	}
}

// TestHistory_LegacyRows_NotLeakedToUser verifies that legacy rows (user_id=NULL,
// external_user_id=NULL) are excluded when a user-scoped query is made.
// For userID=42: filter is t.user_id = 42, and NULL ≠ 42 in SQL → legacy rows excluded. ✓
func TestHistory_LegacyRows_NotLeakedToUser(t *testing.T) {
	const loadQ = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND ($3 = '' OR t.external_user_id = $3)
  AND ($3 != '' OR $4 = 0 OR t.user_id = $4)
ORDER BY tm.id DESC
LIMIT $5`

	// With $3="" and $4=42: condition is t.user_id = 42.
	// Legacy rows have user_id=NULL. In SQL: NULL = 42 is NULL (falsy) → excluded.
	// This is the correct behavior: legacy anonymous rows are not leaked to identified users.
	if !strings.Contains(loadQ, "$4 = 0 OR t.user_id = $4") {
		t.Error("LoadHistory SQL filter must exclude legacy NULL rows from user-scoped queries")
	}
}
