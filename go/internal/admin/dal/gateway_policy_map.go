package dal

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aviciot/them/internal/governance"
)

// ErrInvalidPolicy is returned when a policy payload fails validation (maps to HTTP 400).
var ErrInvalidPolicy = errors.New("invalid governance policy")

// legacyFields is the flat gateway-policy shape the admin REST API and UI still use.
type legacyFields struct {
	AllowedModels       []string
	ModelAliases        json.RawMessage
	MaxTokensPerRequest *int
	MonthlyBudgetUSD    *float64
	LoggingLevel        string // minimal | metrics | debug | audit
}

// applyLegacy overlays the legacy flat fields onto doc, leaving everything the legacy
// shape cannot express (logging, other budgets, warn_at) untouched. The usd/month budget
// is the only budget entry the legacy field owns.
func applyLegacy(doc governance.Policy, lf legacyFields) (governance.Policy, error) {
	aliases := map[string]string{}
	if len(lf.ModelAliases) > 0 {
		if err := json.Unmarshal(lf.ModelAliases, &aliases); err != nil {
			return doc, fmt.Errorf("%w: model_aliases must be a JSON object of strings", ErrInvalidPolicy)
		}
		if aliases == nil {
			aliases = map[string]string{}
		}
	}
	allow := lf.AllowedModels
	if allow == nil {
		allow = []string{}
	}
	doc.Limits.Models = governance.ModelRules{Allow: allow, Aliases: aliases}

	doc.Limits.PerRequest.MaxTokens = 0
	if lf.MaxTokensPerRequest != nil {
		doc.Limits.PerRequest.MaxTokens = *lf.MaxTokensPerRequest
	}

	kept := make([]governance.Budget, 0, len(doc.Limits.Budgets)+1)
	for _, b := range doc.Limits.Budgets {
		if b.Metric == governance.MetricUSD && b.Period == governance.PeriodMonth {
			continue
		}
		kept = append(kept, b)
	}
	if lf.MonthlyBudgetUSD != nil && *lf.MonthlyBudgetUSD > 0 {
		kept = append(kept, governance.Budget{
			Metric: governance.MetricUSD, Period: governance.PeriodMonth,
			Limit: *lf.MonthlyBudgetUSD, OnExceed: governance.ActionBlock,
		})
	}
	doc.Limits.Budgets = kept

	// Wire logging level from flat field into doc.Logging.Level
	if lf.LoggingLevel != "" {
		doc.Logging.Level = lf.LoggingLevel
	} else if doc.Logging.Level == "" {
		doc.Logging.Level = "minimal" // default
	}

	doc.Normalize()
	if err := doc.Validate(); err != nil {
		return doc, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	return doc, nil
}

// toLegacy projects a policy doc onto the legacy flat fields. Zero values map to nil
// (no cap), matching the old nullable columns.
func toLegacy(doc governance.Policy) legacyFields {
	lf := legacyFields{AllowedModels: doc.Limits.Models.Allow, ModelAliases: json.RawMessage("{}")}
	if lf.AllowedModels == nil {
		lf.AllowedModels = []string{}
	}
	if len(doc.Limits.Models.Aliases) > 0 {
		if b, err := json.Marshal(doc.Limits.Models.Aliases); err == nil {
			lf.ModelAliases = b
		}
	}
	if mt := doc.Limits.PerRequest.MaxTokens; mt > 0 {
		lf.MaxTokensPerRequest = &mt
	}
	for _, b := range doc.Limits.Budgets {
		if b.Metric == governance.MetricUSD && b.Period == governance.PeriodMonth {
			limit := b.Limit
			lf.MonthlyBudgetUSD = &limit
			break
		}
	}
	// Project logging level from doc to flat field
	lf.LoggingLevel = doc.Logging.Level
	if lf.LoggingLevel == "" {
		lf.LoggingLevel = "minimal" // default
	}
	return lf
}

// parseDoc decodes a stored doc; an empty or null value yields a normalized empty policy.
func parseDoc(raw []byte) (governance.Policy, error) {
	var p governance.Policy
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, err
		}
	}
	p.Normalize()
	return p, nil
}
