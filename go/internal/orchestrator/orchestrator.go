// Package orchestrator implements the core agentic loop: receive a user
// message, call the LLM, execute any tool calls, feed results back, and
// repeat until the LLM produces a stop event or max_iterations is reached.
// Events are streamed to the caller via the event bus as they arrive.
package orchestrator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/runrecorder"
)

// ErrBudgetExceeded is returned when the run exceeds the configured token budget.
var ErrBudgetExceeded = errors.New("orchestrator: token budget exceeded")

// artifactMaxBase64Bytes is the maximum allowed length of the data_base64 string
// before decoding. It is the base64 encoding of ArtifactMaxBytes (1 MiB) rounded
// up to the nearest multiple of 4. Checking this before decode prevents a large
// allocation from an oversized input string.
//
// Formula: ceil(ArtifactMaxBytes / 3) * 4  == (1048576 + 2) / 3 * 4 == 1398104.
const artifactMaxBase64Bytes = (runrecorder.ArtifactMaxBytes + 2) / 3 * 4

// artifactPayload is the JSON structure agents use to return a file artifact
// inside a tool result. The data_base64 field contains base64-encoded bytes.
// SECURITY: data_base64 must never be forwarded to the event bus or logs.
type artifactPayload struct {
	Artifact *artifactBody `json:"artifact,omitempty"`
}

type artifactBody struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}

// RunContext carries per-run identity metadata (tenant + session) that is
// not part of the core orchestration logic but is needed for artifact storage
// and tenant-scoped agent registry lookups (SEC-03).
type RunContext struct {
	TenantID      string // server-resolved; used to scope agent registry cache
	ApplicationID string
	SessionID     string
}

// Config holds the orchestrator configuration loaded from DB.
type Config struct {
	Name          string
	LLMProvider   string // "anthropic", "openai"
	Model         string
	MaxIterations int
	MaxTokens     int
	Temperature   float64
	SystemPrompt  string
	HistoryWindow int
	AllowedAgents []string // agent slugs

	// BudgetTokens is the maximum total tokens allowed for this run. 0 = unlimited.
	BudgetTokens int

	// MaxParallelTools limits concurrent agent tool invocations. 0 = unlimited.
	MaxParallelTools int

	// Memory / summarizer configuration.
	MemoryEnabled        bool
	SummarizeEveryNCalls int
	MemoryRawFallbackN   int
}

// AgentInvoker is the interface the orchestrator uses to call agents.
// Implemented by the agent registry (Phase 7).
// tenantID must be the server-resolved tenant ID from RunContext — never from
// client-supplied data (SEC-03).
type AgentInvoker interface {
	Invoke(ctx context.Context, tenantID, slug string, input json.RawMessage) (json.RawMessage, error)
}

// HistoryLoader loads prior conversation messages from persistent storage.
// tenantID is used to scope history to a single tenant; pass "" to skip the filter.
// The DB-level LIMIT ensures O(1) data transfer regardless of conversation length.
type HistoryLoader interface {
	LoadHistory(ctx context.Context, contextID, tenantID string, limit int) ([]domain.Message, error)
}

// CheckpointWriter persists individual messages to durable storage for crash recovery.
// tenantID is stored on the tasks row for history isolation.
type CheckpointWriter interface {
	WriteMessage(ctx context.Context, contextID, runID, tenantID string, msg domain.Message) error
}

// CardDiscoverer retrieves agent cards for dynamic tool definition enrichment.
type CardDiscoverer interface {
	GetCard(ctx context.Context, slug string) (AgentCard, error)
}

// AgentCard holds the public metadata for an A2A agent.
type AgentCard struct {
	Name        string
	Description string
	Skills      []AgentSkill
}

// AgentSkill describes a single capability exposed by an A2A agent.
type AgentSkill struct {
	ID          string
	Name        string
	Description string
}

// UsageRecorder persists token usage after each LLM call.
// provider and model identify the backend; costUSD is the per-call cost (may be 0 if unknown).
type UsageRecorder interface {
	RecordUsage(ctx context.Context, runID, provider, model string, inputTokens, outputTokens int, costUSD float64) error
}

// StepRecorder persists individual agent invocation steps and the final run output.
type StepRecorder interface {
	RecordAgentStep(ctx context.Context, runID, agentSlug string, iteration int, inputJSON []byte, output string, latencyMS int64, status, stepErr string) error
	SetFinalOutput(ctx context.Context, runID, text string) error
}

// TaskRecorder tracks child task rows for each agent invocation.
type TaskRecorder interface {
	CreateTask(ctx context.Context, tenantID, runID, contextID, agentSlug string) (string, error)
	CompleteTask(ctx context.Context, taskID string, success bool) error
	CompleteRootTask(ctx context.Context, runID string, success bool) error
}

// BudgetStore checkpoints cumulative token usage to the DB.
type BudgetStore interface {
	UpdateTokensUsed(ctx context.Context, runID string, tokensUsed int) error
}

// ArtifactRecorder persists file artifacts produced by agent tool calls.
type ArtifactRecorder interface {
	RecordArtifact(ctx context.Context, in runrecorder.ArtifactInput) (string, error)
}

// Orchestrator runs the agentic loop.
type Orchestrator struct {
	cfg      Config
	provider llm.Provider
	agents   AgentInvoker
	recorder *runrecorder.Recorder
	bus      event.Bus
	logger   *slog.Logger

	// Optional extension points (nil-safe).
	historyLoader    HistoryLoader
	checkpointer     CheckpointWriter
	cardDiscoverer   CardDiscoverer
	usageRecorder    UsageRecorder
	stepRecorder     StepRecorder
	taskRecorder     TaskRecorder
	budgetStore      BudgetStore
	artifactRecorder ArtifactRecorder

	// Summarizer fields (all optional; wired by WithSummarizer).
	summarizer   Summarizer
	summaryStore SummaryStore
	summaryCfg   SummaryConfig
}

// New creates a new Orchestrator. agents may be nil (tools disabled).
func New(cfg Config, provider llm.Provider, agents AgentInvoker, recorder *runrecorder.Recorder, bus event.Bus, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		cfg:      cfg,
		provider: provider,
		agents:   agents,
		recorder: recorder,
		bus:      bus,
		logger:   logger,
	}
}

// WithHistoryLoader attaches a history loader for crash recovery / conversation continuity.
func (o *Orchestrator) WithHistoryLoader(hl HistoryLoader) *Orchestrator {
	o.historyLoader = hl
	return o
}

// WithCheckpointer attaches a checkpoint writer for durable message persistence.
func (o *Orchestrator) WithCheckpointer(cw CheckpointWriter) *Orchestrator {
	o.checkpointer = cw
	return o
}

// WithCardDiscoverer attaches a card discoverer for A2A tool description enrichment.
func (o *Orchestrator) WithCardDiscoverer(cd CardDiscoverer) *Orchestrator {
	o.cardDiscoverer = cd
	return o
}

// WithUsageRecorder attaches a usage recorder for token accounting.
func (o *Orchestrator) WithUsageRecorder(ur UsageRecorder) *Orchestrator {
	o.usageRecorder = ur
	return o
}

// WithStepRecorder attaches a step recorder for agent invocation and final output persistence.
func (o *Orchestrator) WithStepRecorder(sr StepRecorder) *Orchestrator {
	o.stepRecorder = sr
	return o
}

// WithTaskRecorder attaches a task recorder for child task lifecycle tracking.
func (o *Orchestrator) WithTaskRecorder(tr TaskRecorder) *Orchestrator {
	o.taskRecorder = tr
	return o
}

// WithBudgetStore attaches a budget store for persisting cumulative token usage.
func (o *Orchestrator) WithBudgetStore(bs BudgetStore) *Orchestrator {
	o.budgetStore = bs
	return o
}

// WithArtifactRecorder attaches an artifact recorder for file artifact persistence.
func (o *Orchestrator) WithArtifactRecorder(ar ArtifactRecorder) *Orchestrator {
	o.artifactRecorder = ar
	return o
}

// Run executes one full agentic loop for a user message.
//
// runID:     unique ID for this run (already created in DB by caller)
// contextID: conversation thread ID (used for history lookup and event bus topic)
// userMsg:   the user's message
// history:   pre-loaded conversation history (loaded by caller with DB-level LIMIT)
// runCtx:    optional per-run identity (application_id, session_id) for artifact storage
//
// The loop:
//  1. Build message slice: system + history + userMsg
//  2. Call provider.Stream() with tools
//  3. Accumulate stream events, publish to bus as they arrive
//  4. If LLM requests tool calls, invoke agents in parallel (bounded by MaxParallelTools)
//  5. Repeat until: LLM produces a stop event OR max_iterations reached OR budget exceeded
//  6. Record run completion in DB
//
// Returns the final assistant text response.
func (o *Orchestrator) Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message, runCtx ...RunContext) (string, error) {
	var rctx RunContext
	if len(runCtx) > 0 {
		rctx = runCtx[0]
	}
	maxIter := o.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	// OD-3: Load history from DB if caller passed an empty slice and a HistoryLoader is wired.
	if len(history) == 0 && o.historyLoader != nil {
		limit := o.cfg.HistoryWindow
		if limit <= 0 {
			limit = 20
		}
		loaded, err := o.historyLoader.LoadHistory(ctx, contextID, rctx.TenantID, limit)
		if err != nil {
			o.logger.Warn("orchestrator: history load failed — proceeding without history",
				"context_id", contextID, "error", err)
		} else {
			history = loaded
		}
	}

	// Apply summarization if enabled.
	history = o.maybeSummarize(ctx, contextID, runID, rctx.TenantID, history)

	// Checkpoint the user message for crash recovery (non-fatal).
	if o.checkpointer != nil {
		if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, userMsg); cpErr != nil {
			o.logger.Warn("orchestrator: user message checkpoint failed", "run_id", runID, "error", cpErr)
		}
	}

	// Build initial message list: system + history + user.
	messages := o.buildMessages(history, userMsg)

	// Build tool definitions from allowed agents (enriched with A2A card descriptions).
	tools := o.buildTools(ctx)

	var (
		finalText  string
		tokensUsed int
	)

	// failRun marks the run and root task as failed then returns the given error.
	failRun := func(err error) error {
		_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
		if o.taskRecorder != nil {
			if cerr := o.taskRecorder.CompleteRootTask(ctx, runID, false); cerr != nil {
				o.logger.Warn("orchestrator: complete root task (fail) failed", "run_id", runID, "error", cerr)
			}
		}
		return err
	}

	for iter := 0; iter < maxIter; iter++ {
		// Budget check before each LLM call.
		if o.cfg.BudgetTokens > 0 && tokensUsed >= o.cfg.BudgetTokens {
			o.publishError(ctx, contextID, runID, ErrBudgetExceeded)
			return "", failRun(ErrBudgetExceeded)
		}

		evCh, err := o.provider.Stream(ctx, messages, tools, llm.Options{
			Model:        o.cfg.Model,
			MaxTokens:    o.cfg.MaxTokens,
			Temperature:  o.cfg.Temperature,
			SystemPrompt: o.cfg.SystemPrompt,
		})
		if err != nil {
			o.publishError(ctx, contextID, runID, err)
			return "", failRun(fmt.Errorf("orchestrator: stream: %w", err))
		}

		var (
			assistantText string
			toolCalls     []llm.ToolCall
			stop          bool
			stopReason    string
			inputTokens   int
			outputTokens  int
		)

		// Drain the stream, accumulate text and tool calls, publish to bus.
		for ev := range evCh {
			switch ev.Type {
			case "text_delta":
				assistantText += ev.Delta
				o.publishJSON(ctx, contextID, runID, "token", map[string]string{"content": ev.Delta})
			case "tool_calls":
				toolCalls = ev.ToolCalls
				for _, tc := range ev.ToolCalls {
					o.publishJSON(ctx, contextID, runID, "tool_call", map[string]any{
						"name":  tc.Name,
						"input": tc.Input,
					})
				}
				stopReason = ev.StopReason
				if ev.Usage != nil {
					inputTokens = ev.Usage.InputTokens
					outputTokens = ev.Usage.OutputTokens
				}
			case "stop":
				stop = true
				stopReason = ev.StopReason
				finalText = assistantText
				if ev.Usage != nil {
					inputTokens = ev.Usage.InputTokens
					outputTokens = ev.Usage.OutputTokens
				}
			case "error":
				o.publishError(ctx, contextID, runID, ev.Error)
				return "", failRun(fmt.Errorf("orchestrator: llm error: %v", ev.Error))
			}
		}

		// Update token tracking.
		iterTokens := inputTokens + outputTokens
		tokensUsed += iterTokens

		// Non-fatal: record token usage.
		if o.usageRecorder != nil && iterTokens > 0 {
			costUSD := estimateCost(o.cfg.Model, inputTokens, outputTokens)
			if recErr := o.usageRecorder.RecordUsage(ctx, runID, o.cfg.LLMProvider, o.cfg.Model, inputTokens, outputTokens, costUSD); recErr != nil {
				o.logger.Warn("orchestrator: usage record failed", "run_id", runID, "error", recErr)
			}
		}

		// Checkpoint the assistant turn for crash recovery (non-fatal).
		assistantMsg := buildAssistantMessage(assistantText, toolCalls)
		if (assistantText != "" || len(toolCalls) > 0) && o.checkpointer != nil {
			if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, assistantMsg); cpErr != nil {
				o.logger.Warn("orchestrator: checkpoint write failed", "run_id", runID, "error", cpErr)
			}
		}

		// Append the assistant turn to the message history.
		if assistantText != "" || len(toolCalls) > 0 {
			messages = append(messages, assistantMsg)
		}

		// Checkpoint tokens used to DB (non-fatal).
		if o.budgetStore != nil {
			if bsErr := o.budgetStore.UpdateTokensUsed(ctx, runID, tokensUsed); bsErr != nil {
				o.logger.Warn("orchestrator: budget checkpoint failed", "run_id", runID, "error", bsErr)
			}
		}

		// Budget check after accumulation.
		if o.cfg.BudgetTokens > 0 && tokensUsed >= o.cfg.BudgetTokens {
			o.publishError(ctx, contextID, runID, ErrBudgetExceeded)
			return "", failRun(ErrBudgetExceeded)
		}

		if stop || stopReason == "end_turn" || stopReason == "max_tokens" || len(toolCalls) == 0 {
			break
		}

		// Execute tool calls and append results.
		if len(toolCalls) > 0 && o.agents != nil {
			toolResults := o.executeTools(ctx, contextID, runID, iter, toolCalls, rctx)
			toolResultMsg := buildToolResultMessage(toolResults)

			// Checkpoint tool results (non-fatal).
			if o.checkpointer != nil {
				if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, toolResultMsg); cpErr != nil {
					o.logger.Warn("orchestrator: tool result checkpoint failed", "run_id", runID, "error", cpErr)
				}
			}

			messages = append(messages, toolResultMsg)
		} else if len(toolCalls) > 0 {
			// No agent invoker — treat as end of loop.
			break
		}
	}

	// Record completion.
	_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusCompleted)
	if o.taskRecorder != nil {
		if err := o.taskRecorder.CompleteRootTask(ctx, runID, true); err != nil {
			o.logger.Warn("orchestrator: complete root task failed", "run_id", runID, "error", err)
		}
	}

	// Persist the final LLM answer so the Answer tab can display it (non-fatal).
	if o.stepRecorder != nil && finalText != "" {
		if err := o.stepRecorder.SetFinalOutput(ctx, runID, finalText); err != nil {
			o.logger.Warn("orchestrator: set final output failed", "run_id", runID, "error", err)
		}
	}

	// Publish done event.
	o.publishJSON(ctx, contextID, runID, "done", map[string]string{"run_id": runID})

	return finalText, nil
}

// publishJSON marshals payload and publishes it on the bus.
func (o *Orchestrator) publishJSON(ctx context.Context, contextID, runID, evType string, payload any) {
	raw, _ := json.Marshal(payload)
	o.bus.Publish(ctx, event.Event{
		Topic:     contextID,
		Type:      evType,
		RunID:     runID,
		ContextID: contextID,
		Payload:   raw,
		Timestamp: time.Now(),
	})
}

func (o *Orchestrator) publishError(ctx context.Context, contextID, runID string, err error) {
	if err == nil {
		return
	}
	o.publishJSON(ctx, contextID, runID, "error", map[string]string{
		"run_id":  runID,
		"message": err.Error(),
	})
}

// buildMessages constructs the ordered message slice for the LLM.
func (o *Orchestrator) buildMessages(history []domain.Message, userMsg domain.Message) []domain.Message {
	var msgs []domain.Message
	if o.cfg.SystemPrompt != "" {
		msgs = append(msgs, domain.TextMessage(domain.RoleSystem, o.cfg.SystemPrompt))
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, userMsg)
	return msgs
}

// buildTools converts allowed agent slugs to LLM tool definitions.
// When agents is nil or AllowedAgents is empty, returns nil (no tools).
// If a CardDiscoverer is wired, enriches descriptions from agent cards.
func (o *Orchestrator) buildTools(ctx context.Context) []llm.ToolDef {
	if o.agents == nil || len(o.cfg.AllowedAgents) == 0 {
		return nil
	}
	tools := make([]llm.ToolDef, 0, len(o.cfg.AllowedAgents))
	for _, slug := range o.cfg.AllowedAgents {
		desc := "Invoke the " + slug + " agent."

		// Enrich description from agent card if discoverer is available.
		if o.cardDiscoverer != nil {
			card, err := o.cardDiscoverer.GetCard(ctx, slug)
			if err != nil {
				o.logger.Warn("orchestrator: card discovery failed — using static description",
					"slug", slug, "error", err)
			} else if card.Description != "" {
				desc = card.Description
			}
		}

		tools = append(tools, llm.ToolDef{
			Name:        "agent__" + slug,
			Description: desc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "The input to pass to the agent"},
				},
				"required": []string{"input"},
			},
		})
	}
	return tools
}

type toolResult struct {
	callID string
	name   string
	output json.RawMessage
	err    error
}

// emitArtifactEvent records a file artifact and publishes a "file" event to the
// bus. The event payload contains only metadata (no binary data, no paths).
// SECURITY: artifact data must never appear in any log line or event payload.
func (o *Orchestrator) emitArtifactEvent(ctx context.Context, contextID, runID string, rctx RunContext, body *artifactBody) {
	if o.artifactRecorder == nil {
		return
	}
	// Reject encoded input that cannot possibly decode below the 1 MiB limit.
	// This check avoids allocating a large buffer for an oversized payload.
	if len(body.DataBase64) > artifactMaxBase64Bytes {
		o.logger.Warn("orchestrator: artifact encoded input too large — skipping",
			"run_id", runID, "filename", body.Filename,
			"encoded_len", len(body.DataBase64), "max_encoded_len", artifactMaxBase64Bytes)
		o.publishJSON(ctx, contextID, runID, "error", map[string]string{
			"run_id":  runID,
			"message": "artifact exceeds 1 MiB limit: " + body.Filename,
		})
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataBase64)
	if err != nil {
		o.logger.Warn("orchestrator: artifact base64 decode failed — skipping",
			"run_id", runID, "filename", body.Filename, "error", err)
		o.publishJSON(ctx, contextID, runID, "error", map[string]string{
			"run_id":  runID,
			"message": "artifact decode failed: " + err.Error(),
		})
		return
	}

	artifactID, recErr := o.artifactRecorder.RecordArtifact(ctx, runrecorder.ArtifactInput{
		RunID:         runID,
		ApplicationID: rctx.ApplicationID,
		SessionID:     rctx.SessionID,
		Filename:      body.Filename,
		ContentType:   body.ContentType,
		Data:          data,
	})
	if recErr != nil {
		if errors.Is(recErr, runrecorder.ErrArtifactTooLarge) {
			o.logger.Warn("orchestrator: artifact too large — skipping",
				"run_id", runID, "filename", body.Filename)
			o.publishJSON(ctx, contextID, runID, "error", map[string]string{
				"run_id":  runID,
				"message": "artifact exceeds 1 MiB limit: " + body.Filename,
			})
		} else {
			o.logger.Warn("orchestrator: artifact record failed — skipping",
				"run_id", runID, "filename", body.Filename, "error", recErr)
		}
		return
	}

	// Publish file event — metadata only, no binary data, no internal paths.
	payload := map[string]any{
		"artifact_id":   artifactID,
		"filename":      body.Filename,
		"content_type":  body.ContentType,
		"size":          int64(len(data)),
		"run_id":        runID,
		"download_url":  "/api/v1/runs/" + runID + "/artifacts/" + artifactID,
	}
	if rctx.ApplicationID != "" {
		payload["application_id"] = rctx.ApplicationID
	}
	if rctx.SessionID != "" {
		payload["session_id"] = rctx.SessionID
	}
	o.publishJSON(ctx, contextID, runID, "file", payload)
}

// executeTools invokes all tool calls in parallel (bounded by MaxParallelTools),
// publishes results to the bus, and manages child task lifecycle.
// OD-4: parallel fan-out with semaphore.
// iter is the agentic loop iteration index — all parallel calls in one batch share the same iteration.
func (o *Orchestrator) executeTools(ctx context.Context, contextID, runID string, iter int, calls []llm.ToolCall, rctx RunContext) []toolResult {
	results := make([]toolResult, len(calls))

	// Build semaphore for concurrency control (0 = unlimited).
	var sem *semaphore.Weighted
	if o.cfg.MaxParallelTools > 0 {
		sem = semaphore.NewWeighted(int64(o.cfg.MaxParallelTools))
	}

	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, tc := range calls {
		i, tc := i, tc // capture loop variables
		go func() {
			defer wg.Done()

			// Acquire semaphore slot if bounded.
			if sem != nil {
				if acquireErr := sem.Acquire(ctx, 1); acquireErr != nil {
					results[i] = toolResult{callID: tc.ID, name: tc.Name, err: acquireErr}
					return
				}
				defer sem.Release(1)
			}

			slug := tc.Name
			// Strip "agent__" prefix.
			if len(slug) > 7 && slug[:7] == "agent__" {
				slug = slug[7:]
			}

			// Create child task row (non-fatal).
			var taskID string
			if o.taskRecorder != nil {
				var taskErr error
				taskID, taskErr = o.taskRecorder.CreateTask(ctx, rctx.TenantID, runID, contextID, slug)
				if taskErr != nil {
					o.logger.Warn("orchestrator: create task failed", "slug", slug, "error", taskErr)
				}
			}

			inputBytes, _ := json.Marshal(tc.Input)
			stepStart := time.Now()
			out, err := o.agents.Invoke(ctx, rctx.TenantID, slug, inputBytes)
			latencyMS := time.Since(stepStart).Milliseconds()

			// Complete child task row (non-fatal).
			if o.taskRecorder != nil && taskID != "" {
				if completeErr := o.taskRecorder.CompleteTask(ctx, taskID, err == nil); completeErr != nil {
					o.logger.Warn("orchestrator: complete task failed", "task_id", taskID, "error", completeErr)
				}
			}

			// Record agent step (non-fatal).
			if o.stepRecorder != nil {
				stepStatus := "completed"
				stepErrMsg := ""
				if err != nil {
					stepStatus = "failed"
					stepErrMsg = err.Error()
				}
				if recErr := o.stepRecorder.RecordAgentStep(ctx, runID, slug, iter, inputBytes, string(out), latencyMS, stepStatus, stepErrMsg); recErr != nil {
					o.logger.Warn("orchestrator: record agent step failed", "slug", slug, "error", recErr)
				}
			}

			results[i] = toolResult{callID: tc.ID, name: tc.Name, output: out, err: err}

			if err != nil {
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":  tc.Name,
					"error": err.Error(),
				})
			} else {
				// Check if the tool result contains an artifact payload.
				// If so, record it and emit a "file" event (non-fatal).
				// SECURITY: the artifact body is consumed here; it must not be
				// forwarded to the bus in the tool_result event.
				var ap artifactPayload
				if len(out) > 0 {
					if jsonErr := json.Unmarshal(out, &ap); jsonErr == nil && ap.Artifact != nil {
						o.emitArtifactEvent(ctx, contextID, runID, rctx, ap.Artifact)
					}
				}
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":   tc.Name,
					"output": string(out),
				})
			}
		}()
	}

	wg.Wait()
	return results
}

// buildAssistantMessage builds an assistant message containing text and/or tool_use parts.
func buildAssistantMessage(text string, calls []llm.ToolCall) domain.Message {
	var parts []domain.ContentPart
	if text != "" {
		parts = append(parts, domain.ContentPart{Type: "text", Text: text})
	}
	for _, tc := range calls {
		inputJSON, _ := json.Marshal(tc.Input)
		parts = append(parts, domain.ContentPart{
			Type:      "tool_use",
			ToolUseID: tc.ID,
			ToolName:  tc.Name,
			ToolInput: inputJSON,
		})
	}
	return domain.Message{Role: domain.RoleAssistant, Parts: parts}
}

// estimateCost returns the USD cost for one LLM call based on model pricing.
// Prices are per-token (not per-million) from Anthropic's published rate card.
func estimateCost(model string, inputTokens, outputTokens int) float64 {
	type pricing struct{ in, out float64 }
	// USD per token (divide published $/1M by 1,000,000).
	rates := map[string]pricing{
		"claude-opus-4-5":              {in: 15.0 / 1e6, out: 75.0 / 1e6},
		"claude-opus-4-8":              {in: 15.0 / 1e6, out: 75.0 / 1e6},
		"claude-sonnet-4-5":            {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-sonnet-4-6":            {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-haiku-4-5":             {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-haiku-4-5-20251001":    {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-3-5-sonnet-20241022":   {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-3-5-haiku-20241022":    {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-3-opus-20240229":       {in: 15.0 / 1e6, out: 75.0 / 1e6},
	}
	p, ok := rates[model]
	if !ok {
		// Default to Sonnet pricing for unknown models.
		p = pricing{in: 3.0 / 1e6, out: 15.0 / 1e6}
	}
	return p.in*float64(inputTokens) + p.out*float64(outputTokens)
}

// buildToolResultMessage builds a tool result message for all completed calls.
func buildToolResultMessage(results []toolResult) domain.Message {
	var parts []domain.ContentPart
	for _, r := range results {
		var outputJSON json.RawMessage
		if r.err != nil {
			outputJSON, _ = json.Marshal(map[string]string{"error": r.err.Error()})
		} else {
			outputJSON = r.output
		}
		parts = append(parts, domain.ContentPart{
			Type:       "tool_result",
			ToolUseID:  r.callID,
			ToolName:   r.name,
			ToolResult: outputJSON,
			IsError:    r.err != nil,
		})
	}
	return domain.Message{Role: domain.RoleTool, Parts: parts}
}
