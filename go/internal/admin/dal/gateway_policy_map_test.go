package dal

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/governance"
)

func ip(i int) *int         { return &i }
func fp(f float64) *float64 { return &f }

// GP-MAP-01: legacy fields round-trip through the doc
func TestLegacyRoundTrip(t *testing.T) {
	d, err := applyLegacy(governance.Policy{}, legacyFields{
		AllowedModels: []string{"m1"}, ModelAliases: json.RawMessage(`{"fast":"m1"}`),
		MaxTokensPerRequest: ip(300), MonthlyBudgetUSD: fp(12.5), LoggingLevel: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	lf := toLegacy(d)
	if lf.AllowedModels[0] != "m1" || *lf.MaxTokensPerRequest != 300 || *lf.MonthlyBudgetUSD != 12.5 || string(lf.ModelAliases) != `{"fast":"m1"}` || lf.LoggingLevel != "debug" {
		t.Fatalf("round trip lost data: %+v", lf)
	}
}

// GP-MAP-02: unset legacy values map to nil / empty, never null
func TestLegacyEmpty(t *testing.T) {
	lf := toLegacy(governance.Policy{})
	if lf.AllowedModels == nil || lf.MaxTokensPerRequest != nil || lf.MonthlyBudgetUSD != nil || string(lf.ModelAliases) != "{}" || lf.LoggingLevel != "minimal" {
		t.Fatalf("bad empty mapping: %+v", lf)
	}
}

// GP-MAP-03: applyLegacy preserves logging and non-owned budgets; clearing budget removes only usd/month
func TestApplyLegacyPreserves(t *testing.T) {
	lvl := governance.Policy{Logging: governance.Logging{Level: "debug"}, Limits: governance.Limits{Budgets: []governance.Budget{
		{Metric: "tokens", Period: "day", Limit: 100, OnExceed: "warn"},
		{Metric: "usd", Period: "month", Limit: 9, OnExceed: "block"}}}}
	d, err := applyLegacy(lvl, legacyFields{LoggingLevel: "audit"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Logging.Level != "audit" || len(d.Limits.Budgets) != 1 || d.Limits.Budgets[0].Metric != "tokens" {
		t.Fatalf("not preserved: %+v", d)
	}
}

// GP-MAP-04: invalid input → ErrInvalidPolicy
func TestApplyLegacyInvalid(t *testing.T) {
	for name, lf := range map[string]legacyFields{
		"neg tokens":  {MaxTokensPerRequest: ip(-1)},
		"bad aliases": {ModelAliases: json.RawMessage(`[1]`)},
		"bad pattern": {AllowedModels: []string{"a*b"}},
	} {
		if _, err := applyLegacy(governance.Policy{}, lf); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("%s: want ErrInvalidPolicy, got %v", name, err)
		}
	}
}

// GP-MAP-05: mergeLegacy tolerates empty/null stored docs
func TestMergeLegacyEmptyExisting(t *testing.T) {
	if _, err := mergeLegacy(nil, []string{"x"}, nil, nil, nil, "minimal"); err != nil {
		t.Fatal(err)
	}
}
