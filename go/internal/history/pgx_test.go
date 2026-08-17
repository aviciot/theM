package history

import (
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
