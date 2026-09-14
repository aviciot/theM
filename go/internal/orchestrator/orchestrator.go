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
	"time"

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

// artifactPayload is the JSON structure agents use to return file artifacts
// inside a tool result. The data_base64 field contains base64-encoded bytes.
// SECURITY: data_base64 must never be forwarded to the event bus or logs.
// Single-file agents use "artifact" (legacy); multi-file agents use "artifacts".
type artifactPayload struct {
	Artifact  *artifactBody  `json:"artifact,omitempty"`  // single-file (legacy, backward-compat)
	Artifacts []artifactBody `json:"artifacts,omitempty"` // multi-file (A2A compliant)
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
	TenantID       string // server-resolved; used to scope agent registry cache
	ApplicationID  string
	SessionID      string
	ExternalUserID string // end-user identity; empty for internal and service-token runs
	UserID         int64  // the-M internal user ID from AccessModeUser JWT; 0 otherwise
}

// MCPServerAttachment is one entry in the orchestrator's mcp_servers list.
// Slug must match a row in them.mcp_servers. ToolDefs is pre-fetched at
// run start; Tools is an allowlist (empty = all tools).
type MCPServerAttachment struct {
	Slug     string   `json:"slug"`
	Tools    []string `json:"tools,omitempty"` // allowlist; empty = all
	ToolDefs []llm.ToolDef                     // populated at run-start from DB manifest
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

	// MCPServers lists MCP server attachments for this orchestrator.
	// Tool definitions are pre-fetched and stored in MCPServerAttachment.ToolDefs.
	MCPServers []MCPServerAttachment

	// MCPServiceURL is the base URL of them-mcp-service (e.g. "http://them-mcp-service:8010").
	// Required to dispatch mcp__* tool calls. Empty disables MCP tool dispatch.
	MCPServiceURL string
}

// AgentInvoker is the interface the orchestrator uses to call agents.
// Implemented by the agent registry.
// tenantID must be the server-resolved tenant ID from RunContext — never from
// client-supplied data (SEC-03).
type AgentInvoker interface {
	Invoke(ctx context.Context, tenantID, slug string, input json.RawMessage) (json.RawMessage, error)

	// InvokeForRun is the preferred call path during an orchestrated run.
	// It carries applicationID so canvas_a2a agents can have their invocation
	// context headers (X-Them-*) populated from the app_agent_binding row.
	// For non-canvas agents it behaves identically to Invoke.
	InvokeForRun(ctx context.Context, tenantID, applicationID, slug string, input json.RawMessage) (json.RawMessage, error)

	// InvokeForRunStreaming is like InvokeForRun but accepts an ArtifactCallback
	// for streaming A2A agents. onArtifact is called once per complete artifact
	// as it arrives on the SSE stream. Non-streaming agents fall back to
	// InvokeForRun (callback ignored).
	InvokeForRunStreaming(ctx context.Context, tenantID, applicationID, slug string, input json.RawMessage, onArtifact func(filename, contentType, dataBase64 string)) (json.RawMessage, error)
}

// HistoryLoader loads prior conversation messages from persistent storage.
// tenantID scopes to a single tenant; externalUserID takes precedence over userID for
// end-user isolation; pass "" and 0 to skip all user filters.
type HistoryLoader interface {
	LoadHistory(ctx context.Context, contextID, tenantID, externalUserID string, userID int64, limit int) ([]domain.Message, error)
}

// CheckpointWriter persists individual messages to durable storage for crash recovery.
// tenantID, externalUserID, and userID are stored on the tasks row for history isolation.
type CheckpointWriter interface {
	WriteMessage(ctx context.Context, contextID, runID, tenantID, externalUserID string, userID int64, msg domain.Message) error
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
	CreateTask(ctx context.Context, tenantID, runID, contextID, agentSlug string, userID int64) (string, error)
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

// FileGateInliner intercepts inline (already-decoded) file bytes through the
// security pipeline. Returns the artifact ID stored by the gate (scan_status='pending')
// when scanning is enabled, or ("", nil) when disabled/not-configured.
type FileGateInliner interface {
	InterceptInlineArtifact(ctx context.Context, appID, runID, sessionID, filename, contentType string, data []byte) (artifactID string, err error)
}

// ScanResult carries the outcome of a completed security scan.
type ScanResult struct {
	ArtifactID    string
	ScanStatus    string // "clean", "infected", "error", "failed"
	Threat        string // non-empty when infected
	FileName      string
	FileSizeBytes int64
}

// ScanSubscriber waits for the async artifact_scan_result event published by the
// middleware worker to them:run:<runID> when a scan completes.
// WaitForScanResult blocks until the result arrives, the context is cancelled, or
// the timeout elapses. It returns (result, true) on success, ("", false) on timeout
// or cancellation.
type ScanSubscriber interface {
	WaitForScanResult(ctx context.Context, runID, artifactID string, timeout time.Duration) (ScanResult, bool)
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
	fileGateInliner  FileGateInliner
	scanSubscriber   ScanSubscriber

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

// WithFileGateInliner attaches a security gate for inline file bytes.
// When set, base64-decoded artifacts from agent tool calls are routed through
// the gate before storage, enabling AV scanning on the orchestrator path.
func (o *Orchestrator) WithFileGateInliner(fg FileGateInliner) *Orchestrator {
	o.fileGateInliner = fg
	return o
}

// WithScanSubscriber attaches a subscriber that waits for artifact_scan_result
// events from the middleware worker. When set, gated artifacts emit "file_scanning"
// immediately and then "file" or "file_blocked" once the scan completes.
func (o *Orchestrator) WithScanSubscriber(ss ScanSubscriber) *Orchestrator {
	o.scanSubscriber = ss
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
	// HistoryWindow == 0 means history is explicitly disabled for this entry point.
	if len(history) == 0 && o.historyLoader != nil && o.cfg.HistoryWindow != 0 {
		limit := o.cfg.HistoryWindow
		if limit < 0 {
			limit = 20
		}
		loaded, err := o.historyLoader.LoadHistory(ctx, contextID, rctx.TenantID, rctx.ExternalUserID, rctx.UserID, limit)
		if err != nil {
			o.logger.Warn("orchestrator: history load failed — proceeding without history",
				"context_id", contextID, "error", err)
		} else {
			history = loaded
		}
	}

	// Apply summarization if enabled.
	history = o.maybeSummarize(ctx, contextID, runID, rctx.TenantID, rctx.ExternalUserID, rctx.UserID, history)

	// Checkpoint the user message for crash recovery (non-fatal).
	if o.checkpointer != nil {
		if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, rctx.ExternalUserID, rctx.UserID, userMsg); cpErr != nil {
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
			if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, rctx.ExternalUserID, rctx.UserID, assistantMsg); cpErr != nil {
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
		// Call executeTools when agents or MCP servers are available; otherwise end the loop.
		hasMCPServers := len(o.cfg.MCPServers) > 0 || o.cfg.MCPServiceURL != ""
		if len(toolCalls) > 0 && (o.agents != nil || hasMCPServers) {
			toolResults := o.executeTools(ctx, contextID, runID, iter, toolCalls, rctx)
			toolResultMsg := buildToolResultMessage(toolResults)

			// Checkpoint tool results (non-fatal).
			if o.checkpointer != nil {
				if cpErr := o.checkpointer.WriteMessage(ctx, contextID, runID, rctx.TenantID, rctx.ExternalUserID, rctx.UserID, toolResultMsg); cpErr != nil {
					o.logger.Warn("orchestrator: tool result checkpoint failed", "run_id", runID, "error", cpErr)
				}
			}

			messages = append(messages, toolResultMsg)
		} else if len(toolCalls) > 0 {
			// No agent invoker and no MCP — treat as end of loop.
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
