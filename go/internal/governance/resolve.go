package governance

import "strings"

// ScopeType identifies who a policy applies to.
type ScopeType string

const (
	ScopeTenant      ScopeType = "tenant"
	ScopeApplication ScopeType = "application"
	ScopeClient      ScopeType = "client"
	ScopeUser        ScopeType = "user"
)

// specificity orders scopes for "most specific wins" fields (higher = more specific).
func (s ScopeType) specificity() int {
	switch s {
	case ScopeTenant:
		return 1
	case ScopeApplication:
		return 2
	case ScopeClient:
		return 3
	case ScopeUser:
		return 4
	}
	return 0
}

// ScopedPolicy is a policy together with the scope it was loaded for.
type ScopedPolicy struct {
	Type   ScopeType
	ID     string
	Policy Policy
}

// ScopedBudget is a budget tagged with the scope whose counter it uses.
type ScopedBudget struct {
	ScopeType ScopeType
	ScopeID   string
	Budget
}

// EffectiveLogging is the merged logging config with all defaults applied.
type EffectiveLogging struct {
	Level               string
	SampleRate          float64
	CaptureBodies       bool
	CapturePIIDecisions bool
	CaptureFileGuard    bool
	RetentionDays       int // 0 = platform default
}

// Effective is the result of merging every applicable scope for one request.
type Effective struct {
	// ModelAllow holds one allow-list per scope that set one; a model must pass all of them.
	ModelAllow [][]string
	// Aliases merges alias maps; the most specific scope wins on conflict.
	Aliases map[string]string
	// MaxTokens is the tightest non-zero per-request cap; 0 = no cap.
	MaxTokens int
	// Budgets lists every budget from every scope; all are enforced.
	Budgets []ScopedBudget
	// WarnAt is the lowest non-zero warn fraction; 0 = none.
	WarnAt  float64
	Logging EffectiveLogging
}

// Resolve merges policies. Limits: every scope applies, tightest wins.
// Logging: most specific scope wins per field, then defaults (minimal, sample 1.0).
// Input order does not matter; policies should already be Normalized.
func Resolve(policies ...ScopedPolicy) Effective {
	eff := Effective{
		Aliases: map[string]string{},
		Logging: EffectiveLogging{Level: LevelMinimal, SampleRate: 1.0},
	}
	logSpec := map[string]int{} // field -> specificity of the value currently held
	set := func(field string, spec int) bool {
		if spec >= logSpec[field] {
			logSpec[field] = spec
			return true
		}
		return false
	}
	aliasSpec := map[string]int{}

	for _, sp := range policies {
		spec := sp.Type.specificity()
		l := sp.Policy.Limits

		if len(l.Models.Allow) > 0 {
			eff.ModelAllow = append(eff.ModelAllow, l.Models.Allow)
		}
		for k, v := range l.Models.Aliases {
			if s, ok := aliasSpec[k]; !ok || spec >= s {
				eff.Aliases[k] = v
				aliasSpec[k] = spec
			}
		}
		if mt := l.PerRequest.MaxTokens; mt > 0 && (eff.MaxTokens == 0 || mt < eff.MaxTokens) {
			eff.MaxTokens = mt
		}
		if l.WarnAt > 0 && (eff.WarnAt == 0 || l.WarnAt < eff.WarnAt) {
			eff.WarnAt = l.WarnAt
		}
		for _, b := range l.Budgets {
			if b.OnExceed == "" {
				b.OnExceed = ActionBlock
			}
			eff.Budgets = append(eff.Budgets, ScopedBudget{ScopeType: sp.Type, ScopeID: sp.ID, Budget: b})
		}

		lg := sp.Policy.Logging
		if lg.Level != "" && set("level", spec) {
			eff.Logging.Level = lg.Level
		}
		if lg.SampleRate != nil && set("sample_rate", spec) {
			eff.Logging.SampleRate = *lg.SampleRate
		}
		if lg.CaptureBodies != nil && set("bodies", spec) {
			eff.Logging.CaptureBodies = *lg.CaptureBodies
		}
		if lg.CapturePIIDecisions != nil && set("pii", spec) {
			eff.Logging.CapturePIIDecisions = *lg.CapturePIIDecisions
		}
		if lg.CaptureFileGuard != nil && set("fileguard", spec) {
			eff.Logging.CaptureFileGuard = *lg.CaptureFileGuard
		}
		if lg.RetentionDays != nil && set("retention", spec) {
			eff.Logging.RetentionDays = *lg.RetentionDays
		}
	}
	return eff
}

// ModelAllowed reports whether model passes every scope's allow-list.
// An empty model list (no scope restricts) allows everything.
func (e Effective) ModelAllowed(model string) bool {
	for _, list := range e.ModelAllow {
		ok := false
		for _, pat := range list {
			if pat == model || (strings.HasSuffix(pat, "*") && strings.HasPrefix(model, strings.TrimSuffix(pat, "*"))) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// ResolveAlias returns the concrete model for an alias, or the input if none.
func (e Effective) ResolveAlias(model string) string {
	if v, ok := e.Aliases[model]; ok {
		return v
	}
	return model
}
