// Package orchestrator implements the core agentic loop: receive a user
// message, call the LLM, execute any tool calls, feed results back, and
// repeat until the LLM produces a stop event or max_iterations is reached.
// Events are streamed to the caller via the event bus as they arrive.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/runrecorder"
)

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

// Orchestrator runs the agentic loop.
type Orchestrator struct {
	cfg      Config
	provider llm.Provider
	agents   AgentInvoker
	recorder *runrecorder.Recorder
	bus      event.Bus
	logger   *slog.Logger
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
//  4. If LLM requests tool calls, invoke agents, feed results back
//  5. Repeat until: LLM produces a stop event OR max_iterations reached
//  6. Record run completion in DB
//
// Returns the final assistant text response.
func (o *Orchestrator) Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message) (string, error) {
	maxIter := o.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	// Build initial message list: system + history + user.
	messages := o.buildMessages(history, userMsg)

	// Build tool definitions from allowed agents.
	tools := o.buildTools()

	var finalText string

	for iter := 0; iter < maxIter; iter++ {
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
			case "error":
				o.publishError(ctx, contextID, runID, ev.Error)
				_ = o.recorder.UpdateStatus(ctx, runID, domain.RunStatusFailed)
				return "", fmt.Errorf("orchestrator: llm error: %v", ev.Error)
			}
		}

		// Append the assistant turn to the message history.
		if assistantText != "" || len(toolCalls) > 0 {
			messages = append(messages, buildAssistantMessage(assistantText, toolCalls))
		}

		if stop || stopReason == "end_turn" || stopReason == "max_tokens" || len(toolCalls) == 0 {
			break
		}

		// Execute tool calls and append results.
		if len(toolCalls) > 0 && o.agents != nil {
			toolResults := o.executeTools(ctx, contextID, runID, toolCalls)
			messages = append(messages, buildToolResultMessage(toolResults))
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
func (o *Orchestrator) buildTools() []llm.ToolDef {
	if o.agents == nil || len(o.cfg.AllowedAgents) == 0 {
		return nil
	}
	tools := make([]llm.ToolDef, 0, len(o.cfg.AllowedAgents))
	for _, slug := range o.cfg.AllowedAgents {
		tools = append(tools, llm.ToolDef{
			Name:        "agent__" + slug,
			Description: "Invoke the " + slug + " agent.",
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

// executeTools invokes all tool calls, publishes results to the bus.
func (o *Orchestrator) executeTools(ctx context.Context, contextID, runID string, calls []llm.ToolCall) []toolResult {
	results := make([]toolResult, len(calls))
	for i, tc := range calls {
		slug := tc.Name
		// Strip "agent__" prefix.
		if len(slug) > 7 && slug[:7] == "agent__" {
			slug = slug[7:]
		}
		inputBytes, _ := json.Marshal(tc.Input)
		out, err := o.agents.Invoke(ctx, slug, inputBytes)
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
	}
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
