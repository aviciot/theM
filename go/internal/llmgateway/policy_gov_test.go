package llmgateway

import "testing"

func row(scope, id, doc string) policyRow {
	return policyRow{ScopeType: scope, ScopeID: id, Doc: []byte(doc)}
}

// GW-GOV-01: no rows → nil policy (no enforcement)
func TestPolicyFromRows_None(t *testing.T) {
	if policyFromRows(nil) != nil {
		t.Fatal("want nil")
	}
}

// GW-GOV-02: profile doc maps to runtime fields
func TestPolicyFromRows_Profile(t *testing.T) {
	p := policyFromRows([]policyRow{row("profile", "1", `{"schema_version":1,"limits":{
		"models":{"allow":["mock-model-v1"],"aliases":{"fast":"mock-model-v1"}},
		"per_request":{"max_tokens":500},
		"budgets":[{"metric":"usd","period":"month","limit":0.1,"on_exceed":"block"}]}}`)})
	if p == nil || p.MaxTokensPerRequest != 500 || p.MonthlyBudgetUSD != 0.1 || p.ModelAliases["fast"] != "mock-model-v1" {
		t.Fatalf("bad mapping: %+v", p)
	}
	if !p.ModelAllowed("mock-model-v1") || p.ModelAllowed("other") {
		t.Fatal("allowlist not enforced")
	}
}

// GW-GOV-03: tenant + profile — model must pass both lists, tightest cap/budget wins
func TestPolicyFromRows_TightestWins(t *testing.T) {
	p := policyFromRows([]policyRow{
		row("tenant", "t", `{"schema_version":1,"limits":{"models":{"allow":["claude-*"]},"per_request":{"max_tokens":1000},
			"budgets":[{"metric":"usd","period":"month","limit":50}]}}`),
		row("profile", "p", `{"schema_version":1,"limits":{"models":{"allow":["claude-haiku-5-5","gpt-4"]},"per_request":{"max_tokens":200},
			"budgets":[{"metric":"usd","period":"month","limit":500}]}}`),
	})
	if p.MaxTokensPerRequest != 200 || p.MonthlyBudgetUSD != 50 {
		t.Fatalf("want cap 200 / budget 50, got %+v", p)
	}
	if !p.ModelAllowed("claude-haiku-5-5") {
		t.Fatal("haiku passes both")
	}
	if p.ModelAllowed("claude-sonnet-5") || p.ModelAllowed("gpt-4") {
		t.Fatal("must fail one of the lists")
	}
}

// GW-GOV-04: non-block budgets are not enforced as the monthly cap
func TestPolicyFromRows_WarnBudgetIgnored(t *testing.T) {
	p := policyFromRows([]policyRow{row("tenant", "t", `{"schema_version":1,"limits":{
		"budgets":[{"metric":"usd","period":"month","limit":5,"on_exceed":"warn"}]}}`)})
	if p == nil || p.MonthlyBudgetUSD != 0 {
		t.Fatalf("warn budget must not block: %+v", p)
	}
}

// GW-GOV-05: corrupt doc is skipped, not fatal
func TestPolicyFromRows_BadDocSkipped(t *testing.T) {
	if p := policyFromRows([]policyRow{row("tenant", "t", `not json`)}); p != nil {
		t.Fatalf("want nil, got %+v", p)
	}
}

// GW-GOV-06: legacy AllowedModels still honoured by ModelAllowed
func TestModelAllowed_LegacyField(t *testing.T) {
	p := &Policy{AllowedModels: []string{"a"}}
	if !p.ModelAllowed("a") || p.ModelAllowed("b") {
		t.Fatal("legacy allowlist broken")
	}
}
