// Package promptguard provides a heuristic prompt-injection detector.
// It implements middleware.Processor under the name "prompt_inject".
//
// MVP scope (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 2): known-phrase
// pattern matching only, tuned by Sensitivity. NOT an LLM-judge call — that
// is a real, separate, slower design decision, flagged in the plan doc as
// out of scope for this phase; do not add it silently later without
// re-confirming with the user.
package promptguard

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/aviciot/them/internal/middleware"
)

const Name = "prompt_inject"

// phrase is one detectable injection pattern, tagged with the minimum
// Sensitivity level at which it triggers ("low" patterns are the most
// unambiguous; "high" sensitivity also catches "medium" and "low" ones).
type phrase struct {
	pattern     *regexp.Regexp
	minSeverity string // "low" | "medium" | "high" — see rank()
}

var phrases = []phrase{
	// Unambiguous override attempts — always checked, even at "low" sensitivity.
	{regexp.MustCompile(`(?i)ignore (all )?(previous|prior|above) instructions`), "low"},
	{regexp.MustCompile(`(?i)disregard (all )?(previous|prior|the above)`), "low"},
	{regexp.MustCompile(`(?i)you are now (in )?(developer|debug|dan|jailbreak) mode`), "low"},
	{regexp.MustCompile(`(?i)reveal (your |the )?(system prompt|instructions)`), "low"},
	// Role-override attempts — plausible in legitimate text at a lower rate,
	// gated to "medium" sensitivity and above.
	{regexp.MustCompile(`(?i)act as (if you (were|are) )?(an?|the) (unrestricted|uncensored)`), "medium"},
	{regexp.MustCompile(`(?i)pretend (you have no|there are no) (rules|restrictions|guidelines)`), "medium"},
	// Delimiter-escape attempts — more prone to false positives (legitimate
	// text about code/config can contain these tokens), gated to "high".
	{regexp.MustCompile(`(?i)</?(system|instructions|prompt)>`), "high"},
	{regexp.MustCompile("```\\s*(system|instructions)"), "high"},
}

// rank maps a sensitivity level to the set of phrase severities it enables.
// "low" sensitivity enables only "low"-severity phrases (most conservative,
// fewest false positives); "high" enables all three.
func rank(sensitivity string) map[string]bool {
	switch sensitivity {
	case "high":
		return map[string]bool{"low": true, "medium": true, "high": true}
	case "medium", "":
		return map[string]bool{"low": true, "medium": true}
	default: // "low"
		return map[string]bool{"low": true}
	}
}

// Detector implements middleware.Processor using heuristic phrase matching.
type Detector struct{}

func New() *Detector { return &Detector{} }

func (d *Detector) Name() string { return Name }

// Process implements middleware.Processor. Only processes parts with
// Kind == "text"; skips all others.
func (d *Detector) Process(_ context.Context, part middleware.Part, cfgRaw json.RawMessage) (middleware.Result, error) {
	if part.Kind != "text" {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	var cfg middleware.PromptInjectConfig
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return middleware.Result{Outcome: "error", Detail: map[string]any{"reason": "invalid config"}}, nil
	}
	if !cfg.Enabled {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	mode := cfg.Mode
	if mode == "" {
		mode = "block"
	}

	enabled := rank(cfg.Sensitivity)
	var matched []string
	for _, p := range phrases {
		if !enabled[p.minSeverity] {
			continue
		}
		if p.pattern.MatchString(part.Text) {
			matched = append(matched, p.pattern.String())
		}
	}

	if len(matched) == 0 {
		return middleware.Result{Outcome: "clean"}, nil
	}

	detail := map[string]any{"matched_patterns": len(matched)}

	switch mode {
	case "block":
		return middleware.Result{Outcome: "flagged", Block: true, Detail: detail}, nil
	case "warn":
		return middleware.Result{Outcome: "flagged", Detail: detail}, nil
	default:
		return middleware.Result{Outcome: "error", Detail: map[string]any{"reason": fmt.Sprintf("unknown mode %q", mode)}}, nil
	}
}
