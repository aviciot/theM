package dal

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Runtime/canvas UI reads ep.access_policy.mode; it must be in the EntryPoint JSON.
func TestEntryPointJSON_IncludesAccessPolicy(t *testing.T) {
	role := "jwt-premium"
	b, err := json.Marshal(EntryPoint{Slug: "ws", AccessPolicy: json.RawMessage(`{"mode":"external_jwt"}`), RequiredRole: &role})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"access_policy":{"mode":"external_jwt"}`, `"required_role":"jwt-premium"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}
