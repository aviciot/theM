package orchestrator

import (
	"context"

	"github.com/aviciot/them/internal/domain"
)

// Summarizer compresses older conversation turns into a summary string.
// Implemented by summarizer.Summarizer; defined locally to avoid import cycle.
type Summarizer interface {
	Summarize(ctx context.Context, prior string, msgs []domain.Message) (string, error)
}

// SummaryStore persists and retrieves conversation summaries.
type SummaryStore interface {
	LoadSummary(ctx context.Context, contextID, tenantID string) (string, error)
	SaveSummary(ctx context.Context, contextID, runID, tenantID, summary string) error
}

// SummaryConfig controls when and how summarization fires.
type SummaryConfig struct {
	// MemoryEnabled must be true for summarization to run.
	MemoryEnabled bool
	// SummarizeEveryN is the history-length threshold above which summarization fires.
	// Acts as the accumulation threshold (v1: length-based, not call-counter-based).
	SummarizeEveryN int
	// RawFallbackN is how many recent messages to keep verbatim after summarization.
	RawFallbackN int
	// HistoryWindow is the max messages loaded per run (same as orchestrator.Config.HistoryWindow).
	HistoryWindow int
}

// WithSummarizer attaches a summarizer, its storage, and its configuration.
func (o *Orchestrator) WithSummarizer(s Summarizer, store SummaryStore, cfg SummaryConfig) *Orchestrator {
	o.summarizer = s
	o.summaryStore = store
	o.summaryCfg = cfg
	return o
}

// maybeSummarize applies conversation compression when the history is long enough.
// When summarization fires it:
//  1. Loads the existing summary for contextID.
//  2. Splits history into older (to compress) and recent (to keep verbatim).
//  3. Calls the summarizer with prior + older.
//  4. Persists the new summary.
//  5. Returns [summaryMsg] ++ recent.
//
// When disabled, nil, or history is short, returns history unchanged.
func (o *Orchestrator) maybeSummarize(ctx context.Context, contextID, runID, tenantID string, history []domain.Message) []domain.Message {
	if !o.summaryCfg.MemoryEnabled {
		return history
	}
	if o.summarizer == nil || o.summaryStore == nil {
		return history
	}
	threshold := o.summaryCfg.SummarizeEveryN
	if threshold <= 0 {
		threshold = o.cfg.HistoryWindow
	}
	if len(history) <= threshold {
		return history
	}

	rawN := o.summaryCfg.RawFallbackN
	if rawN <= 0 {
		rawN = 5
	}
	if rawN >= len(history) {
		return history
	}

	older := history[:len(history)-rawN]
	recent := history[len(history)-rawN:]

	// Load existing summary (non-fatal).
	prior, err := o.summaryStore.LoadSummary(ctx, contextID, tenantID)
	if err != nil {
		o.logger.Warn("orchestrator: load summary failed — summarizing without prior",
			"context_id", contextID, "error", err)
		prior = ""
	}

	// Call the summarizer.
	summary, err := o.summarizer.Summarize(ctx, prior, older)
	if err != nil {
		o.logger.Warn("orchestrator: summarize failed — returning full history",
			"context_id", contextID, "error", err)
		return history
	}

	// Persist the new summary (non-fatal).
	if saveErr := o.summaryStore.SaveSummary(ctx, contextID, runID, tenantID, summary); saveErr != nil {
		o.logger.Warn("orchestrator: save summary failed",
			"context_id", contextID, "error", saveErr)
	}

	// Build compressed context: one summary message + recent verbatim turns.
	summaryMsg := domain.TextMessage(domain.RoleSystem, summary)
	result := make([]domain.Message, 0, 1+len(recent))
	result = append(result, summaryMsg)
	result = append(result, recent...)
	return result
}

