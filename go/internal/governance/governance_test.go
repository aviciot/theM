package governance

import "testing"

func ptr[T any](v T) *T { return &v }

func valid() Policy {
	p := Policy{Limits: Limits{Budgets: []Budget{{Metric: MetricUSD, Period: PeriodMonth, Limit: 100}}}}
	p.Normalize()
	return p
}

func TestNormalizeDefaults(t *testing.T) {
	p := valid()
	if p.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d", p.SchemaVersion)
	}
	if p.Limits.Budgets[0].OnExceed != ActionBlock {
		t.Fatalf("on_exceed = %q, want block", p.Limits.Budgets[0].OnExceed)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(p *Policy){
		"bad version":    func(p *Policy) { p.SchemaVersion = 9 },
		"bad metric":     func(p *Policy) { p.Limits.Budgets[0].Metric = "gold" },
		"bad period":     func(p *Policy) { p.Limits.Budgets[0].Period = "decade" },
		"zero limit":     func(p *Policy) { p.Limits.Budgets[0].Limit = 0 },
		"bad action":     func(p *Policy) { p.Limits.Budgets[0].OnExceed = "explode" },
		"duplicate":      func(p *Policy) { p.Limits.Budgets = append(p.Limits.Budgets, p.Limits.Budgets[0]) },
		"bad level":      func(p *Policy) { p.Logging.Level = "verbose" },
		"bad sample":     func(p *Policy) { p.Logging.SampleRate = ptr(1.5) },
		"neg retention":  func(p *Policy) { p.Logging.RetentionDays = ptr(-1) },
		"bad warn":       func(p *Policy) { p.Limits.WarnAt = 2 },
		"neg max tokens": func(p *Policy) { p.Limits.PerRequest.MaxTokens = -1 },
		"mid wildcard":   func(p *Policy) { p.Limits.Models.Allow = []string{"cl*ude"} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			p := valid()
			mut(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRegisterAction(t *testing.T) {
	p := valid()
	p.Limits.Budgets[0].OnExceed = "downgrade"
	if p.Validate() == nil {
		t.Fatal("unregistered action accepted")
	}
	RegisterAction("downgrade")
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaults(t *testing.T) {
	e := Resolve()
	if e.Logging.Level != LevelMinimal || e.Logging.SampleRate != 1.0 {
		t.Fatalf("defaults wrong: %+v", e.Logging)
	}
	if !e.ModelAllowed("anything") || e.MaxTokens != 0 || len(e.Budgets) != 0 {
		t.Fatal("empty resolve must not restrict")
	}
}

func TestResolveLimitsTightestWins(t *testing.T) {
	tenant := ScopedPolicy{Type: ScopeTenant, ID: "t", Policy: Policy{Limits: Limits{
		Models:     ModelRules{Allow: []string{"claude-*", "gpt-4o"}},
		PerRequest: PerRequest{MaxTokens: 8000},
		Budgets:    []Budget{{Metric: MetricUSD, Period: PeriodMonth, Limit: 5000}},
		WarnAt:     0.9,
	}}}
	user := ScopedPolicy{Type: ScopeUser, ID: "external:42", Policy: Policy{Limits: Limits{
		Models:     ModelRules{Allow: []string{"claude-haiku*"}},
		PerRequest: PerRequest{MaxTokens: 2000},
		Budgets:    []Budget{{Metric: MetricUSD, Period: PeriodMonth, Limit: 50, OnExceed: ActionWarn}},
		WarnAt:     0.8,
	}}}
	e := Resolve(user, tenant) // order must not matter

	if e.MaxTokens != 2000 {
		t.Fatalf("MaxTokens = %d, want 2000", e.MaxTokens)
	}
	if e.WarnAt != 0.8 {
		t.Fatalf("WarnAt = %v, want 0.8", e.WarnAt)
	}
	if len(e.Budgets) != 2 {
		t.Fatalf("want both budgets enforced, got %d", len(e.Budgets))
	}
	for _, b := range e.Budgets {
		if b.ScopeType == ScopeTenant && b.OnExceed != ActionBlock {
			t.Fatalf("tenant budget on_exceed = %q, want default block", b.OnExceed)
		}
	}
	if !e.ModelAllowed("claude-haiku-4-5") {
		t.Fatal("haiku passes both lists")
	}
	if e.ModelAllowed("claude-opus-5-5") {
		t.Fatal("opus blocked by user list")
	}
	if e.ModelAllowed("gpt-4o") {
		t.Fatal("gpt-4o blocked by user list")
	}
}

func TestResolveAliasesSpecificWins(t *testing.T) {
	tenant := ScopedPolicy{Type: ScopeTenant, Policy: Policy{Limits: Limits{Models: ModelRules{Aliases: map[string]string{"fast": "a", "big": "b"}}}}}
	client := ScopedPolicy{Type: ScopeClient, Policy: Policy{Limits: Limits{Models: ModelRules{Aliases: map[string]string{"fast": "c"}}}}}
	e := Resolve(client, tenant)
	if e.ResolveAlias("fast") != "c" || e.ResolveAlias("big") != "b" || e.ResolveAlias("x") != "x" {
		t.Fatalf("aliases wrong: %+v", e.Aliases)
	}
}

func TestResolveLoggingMostSpecificWins(t *testing.T) {
	tenant := ScopedPolicy{Type: ScopeTenant, Policy: Policy{Logging: Logging{
		Level: LevelMetrics, SampleRate: ptr(0.5), CaptureBodies: ptr(true), RetentionDays: ptr(30),
	}}}
	client := ScopedPolicy{Type: ScopeClient, Policy: Policy{Logging: Logging{Level: LevelDebug}}}
	e := Resolve(client, tenant)

	if e.Logging.Level != LevelDebug {
		t.Fatalf("level = %q, client should win", e.Logging.Level)
	}
	// fields the client left unset are inherited from the tenant
	if e.Logging.SampleRate != 0.5 || !e.Logging.CaptureBodies || e.Logging.RetentionDays != 30 {
		t.Fatalf("inheritance broken: %+v", e.Logging)
	}
	if e.Logging.CapturePIIDecisions || e.Logging.CaptureFileGuard {
		t.Fatal("unset bools must default to false")
	}
}

func TestResolveExplicitFalseOverridesInherited(t *testing.T) {
	tenant := ScopedPolicy{Type: ScopeTenant, Policy: Policy{Logging: Logging{CaptureBodies: ptr(true)}}}
	app := ScopedPolicy{Type: ScopeApplication, Policy: Policy{Logging: Logging{CaptureBodies: ptr(false)}}}
	if Resolve(tenant, app).Logging.CaptureBodies {
		t.Fatal("explicit false at app scope must override tenant true")
	}
}
