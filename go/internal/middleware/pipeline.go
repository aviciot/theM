package middleware

import (
	"context"
	"encoding/json"
	"time"
)

// PipelineResult holds the per-processor outcomes of one pipeline run.
type PipelineResult struct {
	FinalStatus string   // "clean" | "infected" | "flagged" | "error" | "disabled"
	Threat      string   // non-empty when infected
	Results     []Result // one entry per processor that ran
	TotalMS     int64
	// FinalPart is the part after every processor's Modified (if any) has
	// been applied in sequence — e.g. a pii_redact processor's redacted
	// text. Equal to the input part when nothing was modified. Added
	// for TextGate (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 1) — a synchronous
	// caller needs the redacted text back, unlike av_scan's async path
	// (cmd/middleware-worker/main.go), which only needed the status.
	FinalPart Part
}

// Pipeline executes an ordered list of processors against a Part.
type Pipeline struct {
	reg *Registry
}

// NewPipeline creates a Pipeline backed by the given Registry.
func NewPipeline(reg *Registry) *Pipeline {
	return &Pipeline{reg: reg}
}

// Run executes the processors named by names in order against part, using cfg
// to look up per-processor config. It publishes progress via pub after each
// processor completes. pub may be nil (no-op).
//
// Returns PipelineResult. Never returns an error — processor failures are
// recorded as outcome "error" and the pipeline continues unless Block is set.
func (p *Pipeline) Run(
	ctx context.Context,
	part Part,
	names []string,
	cfg SecurityConfig,
	pub ProgressPublisher,
) PipelineResult {
	if len(names) == 0 {
		return PipelineResult{FinalStatus: "disabled", FinalPart: part}
	}

	start := time.Now()
	var results []Result
	current := part
	hadError := false
	hadFlag := false

	for _, name := range names {
		proc := p.reg.Get(name)
		if proc == nil {
			continue
		}
		procCfg := ProcessorConfig(cfg, name)
		if procCfg == nil {
			procCfg = json.RawMessage(`{}`)
		}

		if pub != nil {
			pub.PublishProgress(ctx, name, "running", nil, 0)
		}

		t0 := time.Now()
		r, err := proc.Process(ctx, current, procCfg)
		r.DurationMS = time.Since(t0).Milliseconds()

		if err != nil {
			r.Outcome = "error"
			r.Block = false // don't block on processor failure — warn only
		}
		if r.Outcome == "error" {
			hadError = true
		}
		if r.Outcome == "flagged" {
			hadFlag = true
		}

		results = append(results, r)

		if pub != nil {
			pub.PublishProgress(ctx, name, r.Outcome, r.Detail, r.DurationMS)
		}

		if r.Modified != nil {
			current = *r.Modified
		}

		if r.Block {
			// Propagate threat info
			threat := ""
			if t, ok := r.Detail["threat"].(string); ok {
				threat = t
			}
			return PipelineResult{
				FinalStatus: r.Outcome,
				Threat:      threat,
				Results:     results,
				TotalMS:     time.Since(start).Milliseconds(),
				FinalPart:   current,
			}
		}
	}

	// finalStatus was, for a long time, only ever "clean" or "error" here —
	// a non-blocking "flagged" outcome (pii_redact/prompt_inject's warn
	// mode, or redact mode's own non-blocking flag) fell through to "clean"
	// silently, even though this struct's own doc comment always claimed
	// FinalStatus could be "flagged". Found live testing a PII Guard demo:
	// mode=warn genuinely detected PII (confirmed via the processor's own
	// Result.Outcome=="flagged") but TextGate's Categories string showed
	// "pii_redact:clean" — no way to tell a real "nothing found" apart from
	// "found it, but the outcome got lost on the way out."
	finalStatus := "clean"
	if hadFlag {
		finalStatus = "flagged"
	}
	if hadError {
		finalStatus = "error"
	}
	return PipelineResult{
		FinalStatus: finalStatus,
		Results:     results,
		TotalMS:     time.Since(start).Milliseconds(),
		FinalPart:   current,
	}
}

// ProgressPublisher is implemented by the Redis publisher so the pipeline can
// emit per-processor progress events without importing Redis directly.
type ProgressPublisher interface {
	PublishProgress(ctx context.Context, processor, status string, detail map[string]any, durationMS int64)
}
