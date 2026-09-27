// Package pii provides a regex-based PII detector/redactor.
// It implements middleware.Processor under the name "pii_redact".
//
// MVP scope (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 2): pattern-matching
// only — email addresses, phone numbers, credit-card-like digit sequences,
// SSN-like patterns. PIIRedactConfig.LLMAssist is a real field but NOT
// implemented here — that's a future, separate, slower LLM-judge pass, not
// silently built into this MVP.
package pii

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/aviciot/them/internal/middleware"
)

const Name = "pii_redact"

// category is one detectable PII kind, with its pattern and redaction mask.
type category struct {
	name    string
	pattern *regexp.Regexp
	mask    string
}

// categories is the fixed detection set for this MVP. Order matters only
// for redaction pass order (each pass operates on the previous pass's
// output), not for detection completeness.
var categories = []category{
	{
		name:    "email",
		pattern: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
		mask:    "[REDACTED_EMAIL]",
	},
	{
		// Loosely matches common phone formats: optional +country prefix, a
		// 3-3-4 digit grouping separated by space/dot/dash, optional
		// parens around the first group. Intentionally permissive (an MVP
		// false-positive on a phone-shaped number sequence is an acceptable
		// tradeoff against silently missing real phone numbers).
		name:    "phone",
		pattern: regexp.MustCompile(`(\+\d{1,3}[\s.\-])?\(?\d{3}\)?[\s.\-]\d{3}[\s.\-]\d{4}\b`),
		mask:    "[REDACTED_PHONE]",
	},
	{
		// Credit-card-like: 13-19 digits, optionally grouped by spaces/dashes
		// in 4s. Not a Luhn check (that would reject legitimate-looking but
		// invalid numbers as "not PII," which is the wrong failure mode for a
		// redaction guard — over-redact rather than under-redact).
		name:    "credit_card",
		pattern: regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),
		mask:    "[REDACTED_CARD]",
	},
	{
		name:    "ssn",
		pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		mask:    "[REDACTED_SSN]",
	},
}

// Detector implements middleware.Processor using regex pattern matching.
type Detector struct{}

func New() *Detector { return &Detector{} }

func (d *Detector) Name() string { return Name }

// Process implements middleware.Processor. Only processes parts with
// Kind == "text"; skips all others (files/data are av_scan's/schema_validate's
// concern respectively).
func (d *Detector) Process(_ context.Context, part middleware.Part, cfgRaw json.RawMessage) (middleware.Result, error) {
	if part.Kind != "text" {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	var cfg middleware.PIIRedactConfig
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return middleware.Result{Outcome: "error", Detail: map[string]any{"reason": "invalid config"}}, nil
	}
	if !cfg.Enabled {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	mode := cfg.Mode
	if mode == "" {
		mode = "redact"
	}

	found := map[string]int{}
	redacted := part.Text
	for _, c := range categories {
		matches := c.pattern.FindAllString(redacted, -1)
		if len(matches) == 0 {
			continue
		}
		found[c.name] = len(matches)
		redacted = c.pattern.ReplaceAllString(redacted, c.mask)
	}

	if len(found) == 0 {
		return middleware.Result{Outcome: "clean"}, nil
	}

	detail := map[string]any{"categories": found}

	switch mode {
	case "block":
		return middleware.Result{Outcome: "flagged", Block: true, Detail: detail}, nil
	case "warn":
		return middleware.Result{Outcome: "flagged", Detail: detail}, nil
	case "redact":
		modified := part
		modified.Text = redacted
		return middleware.Result{Outcome: "flagged", Modified: &modified, Detail: detail}, nil
	default:
		return middleware.Result{Outcome: "error", Detail: map[string]any{"reason": fmt.Sprintf("unknown mode %q", mode)}}, nil
	}
}
