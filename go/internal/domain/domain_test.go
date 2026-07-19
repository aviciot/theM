package domain

import "testing"

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
