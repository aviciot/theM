package dal

import (
	"context"
)

// orchSelectCols is the column list shared by List and Get orchestrator queries.
const orchSelectCols = `
	id::text, name, display_name, COALESCE(system_prompt, ''),
	COALESCE(allowed_agent_ids::text[], '{}'),
	COALESCE(llm_provider, ''), COALESCE(llm_model, ''),
	llm_base_url, max_iterations, max_parallel_tools,
	rate_limit_rpm, daily_budget_usd::text,
	enabled, voice_enabled,
	transcription_provider, transcription_model,
	tts_enabled, tts_provider, tts_voice,
	memory_enabled, summarize_every_n_calls, memory_raw_fallback_n,
	summarizer_provider, summarizer_model,
	history_window, budget_tokens`

// scanOrch scans one orchestrator row from s.
func scanOrch(s SingleRowScanner) (Orchestrator, error) {
	var o Orchestrator
	var agentIDs []string
	var dailyBudget *string
	if err := s.Scan(
		&o.ID, &o.Name, &o.DisplayName, &o.SystemPrompt,
		&agentIDs, &o.LLMProvider, &o.LLMModel,
		&o.LLMBaseURL, &o.MaxIterations, &o.MaxParallelTools,
		&o.RateLimitRPM, &dailyBudget,
		&o.Enabled, &o.VoiceEnabled,
		&o.TranscriptionProvider, &o.TranscriptionModel,
		&o.TTSEnabled, &o.TTSProvider, &o.TTSVoice,
		&o.MemoryEnabled, &o.SummarizeEveryNCalls, &o.MemoryRawFallbackN,
		&o.SummarizerProvider, &o.SummarizerModel,
		&o.HistoryWindow, &o.BudgetTokens,
	); err != nil {
		return o, err
	}
	o.AllowedAgentIDs = agentIDs
	o.DailyBudgetUSD = dailyBudget
	return o, nil
}

// rowToSingle adapts a RowScanner to SingleRowScanner so scanOrch can be used
// inside a multi-row loop without a second type-switch.
type rowToSingle struct{ r RowScanner }

func (a *rowToSingle) Scan(dest ...any) error { return a.r.Scan(dest...) }

// ListOrchestrators returns all orchestrators ordered by creation date.
func (d *DB) ListOrchestrators(ctx context.Context) ([]Orchestrator, error) {
	q := "SELECT " + orchSelectCols + " FROM them.orchestrators ORDER BY created_at"

	rows, err := d.q.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orchs := make([]Orchestrator, 0)
	for rows.Next() {
		o, err := scanOrch(&rowToSingle{r: rows})
		if err != nil {
			return nil, err
		}
		orchs = append(orchs, o)
	}
	return orchs, nil
}

// GetOrchestrator returns a single orchestrator by name.
func (d *DB) GetOrchestrator(ctx context.Context, name string) (Orchestrator, error) {
	q := "SELECT " + orchSelectCols + " FROM them.orchestrators WHERE name = $1"
	return scanOrch(d.q.QueryRow(ctx, q, name))
}

// CreateOrchestrator inserts a new orchestrator row and returns the new UUID.
func (d *DB) CreateOrchestrator(ctx context.Context, in OrchestratorInput, enabled bool) (string, error) {
	const q = `
		INSERT INTO them.orchestrators
		  (name, display_name, system_prompt, llm_provider, llm_model,
		   max_iterations, history_window, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text`

	row := d.q.ExecReturning(ctx, q,
		in.Name, in.DisplayName, in.SystemPrompt,
		in.LLMProvider, in.LLMModel,
		in.MaxIterations, in.HistoryWindow, enabled,
	)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateOrchestrator modifies an existing orchestrator row identified by name.
func (d *DB) UpdateOrchestrator(ctx context.Context, name string, in OrchestratorInput, enabled bool) error {
	const q = `
		UPDATE them.orchestrators
		SET display_name=$2, system_prompt=$3, llm_provider=$4, llm_model=$5,
		    max_iterations=$6, history_window=$7, enabled=$8, updated_at=now()
		WHERE name=$1`

	return d.q.Exec(ctx, q,
		name, in.DisplayName, in.SystemPrompt,
		in.LLMProvider, in.LLMModel,
		in.MaxIterations, in.HistoryWindow, enabled,
	)
}

// DeleteOrchestrator soft-deletes an orchestrator by setting enabled=false.
func (d *DB) DeleteOrchestrator(ctx context.Context, name string) error {
	return d.q.Exec(ctx,
		`UPDATE them.orchestrators SET enabled=false, updated_at=now() WHERE name=$1`,
		name)
}
