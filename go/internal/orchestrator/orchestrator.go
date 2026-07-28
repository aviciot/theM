// Package orchestrator implements the core agentic loop: receive a user
// message, call the LLM, execute any tool calls, feed results back, and
// repeat until the LLM produces a stop event or max_iterations is reached.
// Events are streamed to the caller via the event bus as they arrive.
package orchestrator

import (
	"context"
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
}

// AgentInvoker is the interface the orchestrator uses to call agents.
// Implemented by the agent registry (Phase 7).
type AgentInvoker interface {
	Invoke(ctx context.Context, slug string, input json.RawMessage) (json.RawMessage, error)
}

// HistoryLoader loads prior conversation messages from persistent storage.
// The DB-level LIMIT ensures O(1) data transfer regardless of conversation length.
type HistoryLoader interface {
	LoadHistory(ctx context.Context, contextID string, limit int) ([]domain.Message, error)
}

// CheckpointWriter persists individual messages to durable storage for crash recovery.
// Implementations write to the run_steps table. Failure is non-fatal (log + continue).
type CheckpointWriter interface {
	WriteMessage(ctx context.Context, contextID, runID string, msg domain.Message) error
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
type UsageRecorder interface {
	RecordUsage(ctx context.Context, runID string, inputTokens, outputTokens int) error
}

// TaskRecorder tracks child task rows for each agent invocation.
type TaskRecorder interface {
	CreateTask(ctx context.Context, runID, contextID, agentSlug string) (string, error)
	CompleteTask(ctx context.Context, taskID string, success bool) error
}

// BudgetStore checkpoints cumulative token usage to the DB.
type BudgetStore interface {
	UpdateTokensUsed(ctx context.Context, runID string, tokensUsed int) error
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
	taskRecorder     TaskRecorder
	budgetStore      BudgetStore
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

// Run executes one full agentic loop for a user message.
//
// runID:     unique ID for this run (already created in DB by caller)
// contextID: conversation thread ID (used for history lookup and event bus topic)
// userMsg:   the user's message
// history:   pre-loaded conversation history (loaded by caller with DB-level LIMIT)
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
func (o *Orchestrator) Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message) (string, error) {
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
		loaded, err := o.historyLoader.LoadHistory(ctx, contextID, limit)
		if err != nil {
			o.logger.Warn("orchestrator: history load failed — proceeding without history",
				"context_id", contextID, "error", err)
		} else {
			history = loaded
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

	for iter := 0; iter < maxIter; iter++ {
		// Budget check before each LLM call.
		if o.cfg.BudgetTokens > 0 && tokensUsed >= o.cfg.BudgetTokens {
			o.publishError(ctx, contextID, runID, ErrBudgetExceeded)
			_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
			return "", ErrBudgetExceeded
		}

		evCh, err := o.provider.Stream(ctx, messages, tools, llm.Options{
			Model:        o.cfg.Model,
			MaxTokens:    o.cfg.MaxTokens,
			Temperature:  o.cfg.Temperature,
			SystemPrompt: o.cfg.SystemPrompt,
		})
		if err != nil {
			o.publishError(ctx, contextID, runID, err)
			_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
			return "", fmt.Errorf("orchestrator: stream: %w", err)
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
			case "stop":
				stop = true
				stopReason = ev.StopReason
				finalText = assistantText
				// Extract token usage from the stop event.
				if ev.Usage != nil {
					inputTokens = ev.Usage.InputTokens
					outputTokens = ev.Usage.OutputTokens
				}
			case "error":
				o.publishError(ctx, contextID, runID, ev.Error)
				_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
				return "", fmt.Errorf("orchestrator: llm error: %v", ev.Error)
			}
		}

		// Update token tracking.
		iterTokens := inputTokens + outputTokens
		tokensUsed += iterTokens

		// Non-fatal: record token usage.
		if o.usageRecorder != nil && iterTokens > 0 {
			if recErr := o.usageRecorder.RecordUsage(ctx, runID, inputTokens, outputTokens); recErr != nil {
				o.logger.Warn("orchestrator: usage record failed", "run_id", runID, "error", recErr)
			}
		}

		// Checkpoint the assistant turn for crash recovery (non-fatal).
		assistantMsg := buildAssistantMessage(assistantText, toolCalls)
		if (assistantText != "" || len(toolCalls) > 0) && o.checkpointer != nil {
			if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, assistantMsg); cpErr != nil {
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
			_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
			return "", ErrBudgetExceeded
		}

		if stop || stopReason == "end_turn" || stopReason == "max_tokens" || len(toolCalls) == 0 {
			break
		}

		// Execute tool calls and append results.
		if len(toolCalls) > 0 && o.agents != nil {
			toolResults := o.executeTools(ctx, contextID, runID, toolCalls)
			toolResultMsg := buildToolResultMessage(toolResults)

			// Checkpoint tool results (non-fatal).
			if o.checkpointer != nil {
				if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, toolResultMsg); cpErr != nil {
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

// executeTools invokes all tool calls in parallel (bounded by MaxParallelTools),
// publishes results to the bus, and manages child task lifecycle.
// OD-4: parallel fan-out with semaphore.
func (o *Orchestrator) executeTools(ctx context.Context, contextID, runID string, calls []llm.ToolCall) []toolResult {
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
				taskID, taskErr = o.taskRecorder.CreateTask(ctx, runID, contextID, slug)
				if taskErr != nil {
					o.logger.Warn("orchestrator: create task failed", "slug", slug, "error", taskErr)
				}
			}

			inputBytes, _ := json.Marshal(tc.Input)
			out, err := o.agents.Invoke(ctx, slug, inputBytes)

			// Complete child task row (non-fatal).
			if o.taskRecorder != nil && taskID != "" {
				if completeErr := o.taskRecorder.CompleteTask(ctx, taskID, err == nil); completeErr != nil {
					o.logger.Warn("orchestrator: complete task failed", "task_id", taskID, "error", completeErr)
				}
			}

			results[i] = toolResult{callID: tc.ID, name: tc.Name, output: out, err: err}

			if err != nil {
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":  tc.Name,
					"error": err.Error(),
				})
			} else {
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
