// Package governance defines the multi-scope policy document that controls what a
// caller may spend / use (limits) and what the platform records about it (logging).
// See docs/_archive/GOVERNANCE_POLICY_DESIGN.md.
package governance

import (
	"fmt"
	"strings"
)

// SchemaVersion is the current policy document version.
const SchemaVersion = 1

// Budget metrics and periods.
const (
	MetricUSD      = "usd"
	MetricTokens   = "tokens"
	MetricRequests = "requests"

	PeriodMinute = "minute"
	PeriodHour   = "hour"
	PeriodDay    = "day"
	PeriodMonth  = "month"
)

// On-exceed actions. New actions are registered with RegisterAction.
const (
	ActionBlock    = "block"
	ActionWarn     = "warn"
	ActionThrottle = "throttle"
)

// Logging capture levels, least to most data.
const (
	LevelMinimal = "minimal"
	LevelMetrics = "metrics"
	LevelDebug   = "debug"
	LevelAudit   = "audit"
)

var (
	metrics = map[string]bool{MetricUSD: true, MetricTokens: true, MetricRequests: true}
	periods = map[string]bool{PeriodMinute: true, PeriodHour: true, PeriodDay: true, PeriodMonth: true}
	actions = map[string]bool{ActionBlock: true, ActionWarn: true, ActionThrottle: true}
	levels  = map[string]bool{LevelMinimal: true, LevelMetrics: true, LevelDebug: true, LevelAudit: true}
)

// RegisterAction makes an additional on_exceed action valid. Call from init().
func RegisterAction(name string) { actions[name] = true }

// Policy is the document stored in governance_policies.doc.
type Policy struct {
	SchemaVersion int     `json:"schema_version"`
	Limits        Limits  `json:"limits"`
	Logging       Logging `json:"logging"`
}

// Limits controls whether a call may proceed.
type Limits struct {
	Models     ModelRules `json:"models"`
	PerRequest PerRequest `json:"per_request"`
	Budgets    []Budget   `json:"budgets"`
	// WarnAt is the fraction of a budget (0,1] at which a warning is emitted. 0 = no early warning.
	WarnAt float64 `json:"warn_at"`
}

// ModelRules restricts and renames models. Allow entries may end in "*" (prefix match).
type ModelRules struct {
	Allow   []string          `json:"allow"`
	Aliases map[string]string `json:"aliases"`
}

// PerRequest caps a single call.
type PerRequest struct {
	MaxTokens int `json:"max_tokens"` // 0 = no cap
}

// Budget is one counter-backed limit.
type Budget struct {
	Metric   string  `json:"metric"`
	Period   string  `json:"period"`
	Limit    float64 `json:"limit"`
	OnExceed string  `json:"on_exceed"`
}

// Logging controls capture. Nil/empty fields mean "inherit from a less specific scope".
type Logging struct {
	Level               string   `json:"level,omitempty"`
	SampleRate          *float64 `json:"sample_rate,omitempty"`
	CaptureBodies       *bool    `json:"capture_bodies,omitempty"`
	CapturePIIDecisions *bool    `json:"capture_pii_decisions,omitempty"`
	CaptureFileGuard    *bool    `json:"capture_file_guard,omitempty"`
	RetentionDays       *int     `json:"retention_days,omitempty"`
}

// Normalize fills defaults in place: schema version and on_exceed=block.
func (p *Policy) Normalize() {
	if p.SchemaVersion == 0 {
		p.SchemaVersion = SchemaVersion
	}
	for i := range p.Limits.Budgets {
		if p.Limits.Budgets[i].OnExceed == "" {
			p.Limits.Budgets[i].OnExceed = ActionBlock
		}
	}
}

// Validate reports the first problem with the document. Call Normalize first.
func (p *Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("governance: unsupported schema_version %d", p.SchemaVersion)
	}
	l := p.Limits
	if l.PerRequest.MaxTokens < 0 {
		return fmt.Errorf("governance: per_request.max_tokens must be >= 0")
	}
	if l.WarnAt < 0 || l.WarnAt > 1 {
		return fmt.Errorf("governance: warn_at must be between 0 and 1")
	}
	for _, m := range l.Models.Allow {
		if strings.TrimSpace(m) == "" || strings.Contains(strings.TrimSuffix(m, "*"), "*") {
			return fmt.Errorf("governance: invalid models.allow entry %q (only a trailing * is supported)", m)
		}
	}
	seen := map[string]bool{}
	for _, b := range l.Budgets {
		if !metrics[b.Metric] {
			return fmt.Errorf("governance: unknown budget metric %q", b.Metric)
		}
		if !periods[b.Period] {
			return fmt.Errorf("governance: unknown budget period %q", b.Period)
		}
		if b.Limit <= 0 {
			return fmt.Errorf("governance: budget %s/%s limit must be > 0", b.Metric, b.Period)
		}
		if !actions[b.OnExceed] {
			return fmt.Errorf("governance: unknown on_exceed action %q", b.OnExceed)
		}
		k := b.Metric + "/" + b.Period
		if seen[k] {
			return fmt.Errorf("governance: duplicate budget %s", k)
		}
		seen[k] = true
	}
	lg := p.Logging
	if lg.Level != "" && !levels[lg.Level] {
		return fmt.Errorf("governance: unknown logging level %q", lg.Level)
	}
	if lg.SampleRate != nil && (*lg.SampleRate < 0 || *lg.SampleRate > 1) {
		return fmt.Errorf("governance: sample_rate must be between 0 and 1")
	}
	if lg.RetentionDays != nil && *lg.RetentionDays < 0 {
		return fmt.Errorf("governance: retention_days must be >= 0")
	}
	return nil
}
