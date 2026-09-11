package service

import "testing"

func TestValidateEPAuthCombination_DeadCombinations(t *testing.T) {
	dead := []struct {
		mode       string
		principals string
	}{
		{"external_jwt", "internal"},
		{"user_jwt", "external"},
		{"public", "external"},
	}
	for _, d := range dead {
		if err := validateEPAuthCombination(d.mode, d.principals); err == nil {
			t.Errorf("expected error for dead combination %s+%s, got nil", d.mode, d.principals)
		}
	}
}

func TestValidateEPAuthCombination_ValidCombinations(t *testing.T) {
	valid := []struct {
		mode       string
		principals string
	}{
		{"token", "internal"},
		{"token", "external"},
		{"token", "both"},
		{"user_jwt", "internal"},
		{"user_jwt", "both"},
		{"external_jwt", "external"},
		{"external_jwt", "both"},
		{"public", "internal"},
		{"public", "both"},
		{"", ""},         // defaults to token+internal
		{"token", ""},    // defaults allowed
		{"", "internal"}, // defaults allowed
	}
	for _, v := range valid {
		if err := validateEPAuthCombination(v.mode, v.principals); err != nil {
			t.Errorf("unexpected error for %q+%q: %v", v.mode, v.principals, err)
		}
	}
}
