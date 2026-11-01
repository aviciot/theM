package domain

import (
	"strings"
	"testing"
)

// TestRoleConstants ensures each Role constant is a non-empty string.
// This acts as a compile-time guard against accidental blank-string renames.
func TestRoleConstants(t *testing.T) {
	roles := []struct {
		name  string
		value string
	}{
		{"RoleUser", RoleUser},
		{"RoleAssistant", RoleAssistant},
		{"RoleTool", RoleTool},
		{"RoleSystem", RoleSystem},
	}
	for _, r := range roles {
		if r.value == "" {
			t.Errorf("%s must not be an empty string", r.name)
		}
	}
}

// TestTaskStatusConstants ensures each TaskStatus constant is a non-empty string.
func TestTaskStatusConstants(t *testing.T) {
	statuses := []struct {
		name  string
		value TaskStatus
	}{
		{"TaskSubmitted", TaskSubmitted},
		{"TaskWorking", TaskWorking},
		{"TaskCompleted", TaskCompleted},
		{"TaskFailed", TaskFailed},
		{"TaskInputRequired", TaskInputRequired},
	}
	for _, s := range statuses {
		if string(s.value) == "" {
			t.Errorf("%s must not be an empty string", s.name)
		}
	}
}

// TestRunStatusConstants ensures each RunStatus constant is a non-empty string.
func TestRunStatusConstants(t *testing.T) {
	statuses := []struct {
		name  string
		value RunStatus
	}{
		{"RunRunning", RunRunning},
		{"RunCompleted", RunCompleted},
		{"RunFailed", RunFailed},
		{"RunCanceled", RunCanceled},
		{"RunStopped", RunStopped},
	}
	for _, s := range statuses {
		if string(s.value) == "" {
			t.Errorf("%s must not be an empty string", s.name)
		}
	}
}

// F-133: stopped must count as terminal; every run status the DB allows is classified.
func TestRunStatus_IsTerminal(t *testing.T) {
	cases := map[RunStatus]bool{
		RunCompleted: true, RunFailed: true, RunCanceled: true, RunStopped: true, "cancelled": true,
		RunRunning: false, RunStatusAdmitted: false, RunStatusInputRequired: false, "": false,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%q.IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

// The SQL guard list must name exactly the terminal statuses.
func TestTerminalRunStatusesSQL_MatchesIsTerminal(t *testing.T) {
	for _, s := range []RunStatus{RunCompleted, RunFailed, RunCanceled, RunStopped, "cancelled"} {
		if !strings.Contains(TerminalRunStatusesSQL, "'"+string(s)+"'") {
			t.Errorf("TerminalRunStatusesSQL is missing %q", s)
		}
	}
	for _, s := range []RunStatus{RunRunning, RunStatusAdmitted} {
		if strings.Contains(TerminalRunStatusesSQL, "'"+string(s)+"'") {
			t.Errorf("TerminalRunStatusesSQL must not contain %q", s)
		}
	}
}
