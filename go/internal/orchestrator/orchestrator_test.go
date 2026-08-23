package orchestrator_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeDBQuerier struct{}

func (f *fakeDBQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (f *fakeDBQuerier) QueryRow(_ context.Context, _ string, _ ...any) runrecorder.SingleRowScanner {
	return &fakeRow{}
}

type fakeRow struct{}

func (f *fakeRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if sp, ok := dest[0].(*string); ok {
			*sp = "00000000-0000-0000-0000-000000000001"
		}
	}
	return nil
}

func newRecorder() *runrecorder.Recorder {
	return runrecorder.New(&fakeDBQuerier{})
}

// fakeArtifactRecorder records RecordArtifact calls and can be configured to
// return specific IDs or errors.
type fakeArtifactRecorder struct {
	mu    sync.Mutex
	calls []runrecorder.ArtifactInput
	retID string
	err   error
}

func (f *fakeArtifactRecorder) RecordArtifact(_ context.Context, in runrecorder.ArtifactInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// SECURITY: never store Data in a way that would be logged — just count.
	f.calls = append(f.calls, runrecorder.ArtifactInput{
		RunID:         in.RunID,
		ApplicationID: in.ApplicationID,
		SessionID:     in.SessionID,
		Filename:      in.Filename,
		ContentType:   in.ContentType,
		// Data intentionally omitted — we only track metadata.
	})
	if f.err != nil {
		return "", f.err
	}
	id := f.retID
	if id == "" {
		id = "artifact-uuid-001"
	}
	return id, nil
}

func (f *fakeArtifactRecorder) getCalls() []runrecorder.ArtifactInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runrecorder.ArtifactInput, len(f.calls))
	copy(out, f.calls)
	return out
}

// multiCallMockProvider returns different event sequences on each call.
// If all sequences are exhausted, it returns an empty (stop-only) sequence.
type multiCallMockProvider struct {
	mu       sync.Mutex
	sequences [][]llm.StreamEvent
	callIdx  int
}

func newMultiCallMockProvider(seqs ...[]llm.StreamEvent) *multiCallMockProvider {
	return &multiCallMockProvider{sequences: seqs}
}

func (m *multiCallMockProvider) Stream(ctx context.Context, _ []domain.Message, _ []llm.ToolDef, _ llm.Options) (<-chan llm.StreamEvent, error) {
	m.mu.Lock()
	var events []llm.StreamEvent
	if m.callIdx < len(m.sequences) {
		events = m.sequences[m.callIdx]
	} else {
		events = []llm.StreamEvent{{Type: "stop", StopReason: "end_turn"}}
	}
	m.callIdx++
	m.mu.Unlock()

	out := make(chan llm.StreamEvent, len(events)+1)
	go func() {
		defer close(out)
		for _, e := range events {
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// fakeHistoryLoader records Load calls and returns pre-configured history.
type fakeHistoryLoader struct {
	mu       sync.Mutex
	calls    int
	history  []domain.Message
	err      error
}

func (f *fakeHistoryLoader) LoadHistory(_ context.Context, _, _ string, _ int) ([]domain.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.history, f.err
}

func (f *fakeHistoryLoader) getCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeCheckpointWriter records messages written for crash recovery.
type fakeCheckpointWriter struct {
	mu       sync.Mutex
	messages []domain.Message
}

func (f *fakeCheckpointWriter) WriteMessage(_ context.Context, _, _, _ string, msg domain.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, msg)
	return nil
}

func (f *fakeCheckpointWriter) getMessages() []domain.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Message, len(f.messages))
	copy(out, f.messages)
	return out
}

// fakeAgentInvoker records invocation slugs.
type fakeAgentInvoker struct {
	mu      sync.Mutex
	slugs   []string
	delay   time.Duration
	// maxConcurrent tracks the max observed concurrent executions.
	active     int32
	maxActive  int32
}

func (f *fakeAgentInvoker) Invoke(ctx context.Context, _ string, slug string, _ json.RawMessage) (json.RawMessage, error) {
	// Track concurrency.
	cur := atomic.AddInt32(&f.active, 1)
	defer atomic.AddInt32(&f.active, -1)
	for {
		max := atomic.LoadInt32(&f.maxActive)
		if cur <= max || atomic.CompareAndSwapInt32(&f.maxActive, max, cur) {
			break
		}
	}

	f.mu.Lock()
	f.slugs = append(f.slugs, slug)
	f.mu.Unlock()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return json.RawMessage(`{"result":"ok"}`), nil
}

func (f *fakeAgentInvoker) InvokeForRun(ctx context.Context, tenantID, _ string, slug string, input json.RawMessage) (json.RawMessage, error) {
	return f.Invoke(ctx, tenantID, slug, input)
}

func (f *fakeAgentInvoker) InvokeForRunStreaming(ctx context.Context, tenantID, appID, slug string, input json.RawMessage, _ func(string, string, string)) (json.RawMessage, error) {
	return f.InvokeForRun(ctx, tenantID, appID, slug, input)
}

func (f *fakeAgentInvoker) getSlugs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.slugs))
	copy(out, f.slugs)
	return out
}

func (f *fakeAgentInvoker) getMaxConcurrent() int {
	return int(atomic.LoadInt32(&f.maxActive))
}

// ── Stage 1 Tests ─────────────────────────────────────────────────────────────

// TestOrchestrator_HistoryLoaded verifies that when history is empty, the
// HistoryLoader is called to populate history from the DB.
func TestOrchestrator_HistoryLoaded(t *testing.T) {
	priorMsg := domain.TextMessage(domain.RoleAssistant, "prior response")
	loader := &fakeHistoryLoader{
		history: []domain.Message{priorMsg},
	}

	bus := event.New()
	mock := llm.NewMockProvider([]llm.StreamEvent{
		{Type: "text_delta", Delta: "hello"},
		{Type: "stop", StopReason: "end_turn"},
	})
	cfg := orchestrator.Config{MaxIterations: 1}
	orch := orchestrator.New(cfg, mock, nil, newRecorder(), bus, nil).
		WithHistoryLoader(loader)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-1", "ctx-1", domain.TextMessage(domain.RoleUser, "hi"), nil)
	require.NoError(t, err)

	assert.Equal(t, 1, loader.getCalls(), "LoadHistory must be called when history is nil/empty")
}

// TestOrchestrator_CheckpointRecovery verifies that messages are checkpointed
// after each LLM turn so a restart can recover from the persisted state.
func TestOrchestrator_CheckpointRecovery(t *testing.T) {
	cp := &fakeCheckpointWriter{}

	bus := event.New()
	mock := llm.NewMockProvider([]llm.StreamEvent{
		{Type: "text_delta", Delta: "assistant reply"},
		{Type: "stop", StopReason: "end_turn"},
	})
	cfg := orchestrator.Config{MaxIterations: 1}
	orch := orchestrator.New(cfg, mock, nil, newRecorder(), bus, nil).
		WithCheckpointer(cp)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-cp", "ctx-cp", domain.TextMessage(domain.RoleUser, "hello"), nil)
	require.NoError(t, err)

	msgs := cp.getMessages()
	require.NotEmpty(t, msgs, "at least one message must be checkpointed after LLM response")
	// The assistant message should be checkpointed.
	found := false
	for _, m := range msgs {
		if m.Role == domain.RoleAssistant {
			found = true
			break
		}
	}
	assert.True(t, found, "checkpoint must include the assistant message")
}

// TestOrchestrator_BudgetEnforcement verifies that when the budget is exceeded,
// the run stops and ErrBudgetExceeded is returned.
func TestOrchestrator_BudgetEnforcement(t *testing.T) {
	bus := event.New()
	// Configure a mock that reports usage in the stop event.
	mock := llm.NewMockProvider([]llm.StreamEvent{
		{Type: "text_delta", Delta: "some text"},
		{Type: "stop", StopReason: "end_turn", Usage: &llm.Usage{InputTokens: 50, OutputTokens: 60}},
	})
	// Budget is smaller than what the single LLM call reports.
	cfg := orchestrator.Config{MaxIterations: 5, BudgetTokens: 1}
	orch := orchestrator.New(cfg, mock, nil, newRecorder(), bus, nil)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-budget", "ctx-budget", domain.TextMessage(domain.RoleUser, "hi"), nil)
	// After the first LLM call completes, tokensUsed > BudgetTokens → ErrBudgetExceeded.
	assert.ErrorIs(t, err, orchestrator.ErrBudgetExceeded, "must return ErrBudgetExceeded when budget exceeded")
}

// TestOrchestrator_ParallelFanOut verifies that tool calls are executed in
// parallel with at most MaxParallelTools concurrent executions.
func TestOrchestrator_ParallelFanOut(t *testing.T) {
	const (
		numAgents   = 5
		maxParallel = 2
	)

	// Build tool call events for 5 agents.
	toolCalls := make([]llm.ToolCall, numAgents)
	for i := range toolCalls {
		toolCalls[i] = llm.ToolCall{
			ID:    fmt.Sprintf("call-%d", i),
			Name:  fmt.Sprintf("agent__agent%d", i),
			Input: map[string]any{"input": "test"},
		}
	}

	bus := event.New()
	// First call: LLM requests 5 tool calls.
	// Second call: LLM returns a stop event.
	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{Type: "tool_calls", ToolCalls: toolCalls, StopReason: "tool_use"},
		},
		[]llm.StreamEvent{
			{Type: "stop", StopReason: "end_turn"},
		},
	)

	agents := &fakeAgentInvoker{delay: 20 * time.Millisecond}
	allowedAgents := make([]string, numAgents)
	for i := range allowedAgents {
		allowedAgents[i] = fmt.Sprintf("agent%d", i)
	}

	cfg := orchestrator.Config{
		MaxIterations:    5,
		AllowedAgents:    allowedAgents,
		MaxParallelTools: maxParallel,
	}
	orch := orchestrator.New(cfg, mock, agents, newRecorder(), bus, nil)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-parallel", "ctx-parallel", domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err)

	// Verify all 5 agents were invoked.
	slugs := agents.getSlugs()
	assert.Len(t, slugs, numAgents, "all %d agents must be invoked", numAgents)

	// Verify concurrency was bounded to MaxParallelTools.
	maxSeen := agents.getMaxConcurrent()
	assert.LessOrEqual(t, maxSeen, maxParallel,
		"max concurrent executions (%d) must not exceed MaxParallelTools (%d)", maxSeen, maxParallel)
	assert.GreaterOrEqual(t, maxSeen, 1, "at least 1 agent must run concurrently")
}

// TestOrchestrator_ParallelFanOut_Unlimited verifies that when MaxParallelTools=0,
// all tool calls can run concurrently (no semaphore).
func TestOrchestrator_ParallelFanOut_Unlimited(t *testing.T) {
	const numAgents = 4

	toolCalls := make([]llm.ToolCall, numAgents)
	for i := range toolCalls {
		toolCalls[i] = llm.ToolCall{
			ID:    fmt.Sprintf("call-%d", i),
			Name:  fmt.Sprintf("agent__agent%d", i),
			Input: map[string]any{"input": "test"},
		}
	}

	bus := event.New()
	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{Type: "tool_calls", ToolCalls: toolCalls, StopReason: "tool_use"},
		},
		[]llm.StreamEvent{
			{Type: "stop", StopReason: "end_turn"},
		},
	)

	agents := &fakeAgentInvoker{delay: 30 * time.Millisecond}
	allowedAgents := make([]string, numAgents)
	for i := range allowedAgents {
		allowedAgents[i] = fmt.Sprintf("agent%d", i)
	}

	cfg := orchestrator.Config{
		MaxIterations:    5,
		AllowedAgents:    allowedAgents,
		MaxParallelTools: 0, // unlimited
	}
	orch := orchestrator.New(cfg, mock, agents, newRecorder(), bus, nil)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-unlimited", "ctx-unlimited", domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err)

	assert.Len(t, agents.getSlugs(), numAgents, "all agents must be invoked")
}

// TestOrchestrator_HistoryNotLoadedWhenProvided verifies that when history is
// non-empty, the HistoryLoader is NOT called (avoids unnecessary DB round-trip).
func TestOrchestrator_HistoryNotLoadedWhenProvided(t *testing.T) {
	loader := &fakeHistoryLoader{}

	bus := event.New()
	mock := llm.NewMockProvider([]llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	})
	cfg := orchestrator.Config{MaxIterations: 1}
	orch := orchestrator.New(cfg, mock, nil, newRecorder(), bus, nil).
		WithHistoryLoader(loader)

	// Provide non-empty history → loader should NOT be called.
	existingHistory := []domain.Message{domain.TextMessage(domain.RoleUser, "prior")}
	ctx := context.Background()
	_, err := orch.Run(ctx, "run-skip", "ctx-skip", domain.TextMessage(domain.RoleUser, "hi"), existingHistory)
	require.NoError(t, err)

	assert.Equal(t, 0, loader.getCalls(), "LoadHistory must NOT be called when history is already provided")
}

// TestOrchestrator_NilOptionals verifies that all optional interfaces can be nil
// without causing panics.
func TestOrchestrator_NilOptionals(t *testing.T) {
	bus := event.New()
	mock := llm.NewMockProvider([]llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	})
	cfg := orchestrator.Config{MaxIterations: 1}
	// No optional interfaces attached — all nil.
	orch := orchestrator.New(cfg, mock, nil, newRecorder(), bus, nil)

	ctx := context.Background()
	result, err := orch.Run(ctx, "run-nil", "ctx-nil", domain.TextMessage(domain.RoleUser, "hi"), nil)
	require.NoError(t, err)
	assert.Equal(t, "hi", result)
}

// ── Artifact tests ────────────────────────────────────────────────────────────

// artifactToolCall builds a tool call whose result contains an artifact payload.
func artifactToolCall(id, filename, ct string, data []byte) llm.ToolCall {
	encoded := base64.StdEncoding.EncodeToString(data)
	payload := map[string]any{
		"artifact": map[string]any{
			"filename":     filename,
			"content_type": ct,
			"data_base64":  encoded,
		},
	}
	input, _ := json.Marshal(payload)
	return llm.ToolCall{
		ID:   id,
		Name: "agent__doc-writer",
		Input: func() map[string]any {
			var m map[string]any
			_ = json.Unmarshal(input, &m)
			return m
		}(),
	}
}

// artifactAgentInvoker returns a tool result containing an artifact payload.
type artifactAgentInvoker struct {
	filename string
	ct       string
	data     []byte
}

func (a *artifactAgentInvoker) Invoke(_ context.Context, _ string, _ string, _ json.RawMessage) (json.RawMessage, error) {
	encoded := base64.StdEncoding.EncodeToString(a.data)
	payload := map[string]any{
		"artifact": map[string]any{
			"filename":     a.filename,
			"content_type": a.ct,
			"data_base64":  encoded,
		},
	}
	b, _ := json.Marshal(payload)
	return json.RawMessage(b), nil
}

func (a *artifactAgentInvoker) InvokeForRun(ctx context.Context, tenantID, _ string, slug string, input json.RawMessage) (json.RawMessage, error) {
	return a.Invoke(ctx, tenantID, slug, input)
}

func (a *artifactAgentInvoker) InvokeForRunStreaming(ctx context.Context, tenantID, appID, slug string, input json.RawMessage, _ func(string, string, string)) (json.RawMessage, error) {
	return a.InvokeForRun(ctx, tenantID, appID, slug, input)
}

// TestOrchestrator_ArtifactEmitted verifies that when a tool returns an artifact
// payload, RecordArtifact is called exactly once and a "file" event is published.
func TestOrchestrator_ArtifactEmitted(t *testing.T) {
	ar := &fakeArtifactRecorder{}
	bus := event.New()

	// Subscribe to the bus to capture events.
	allEvents := make([]event.Event, 0)
	var evMu sync.Mutex
	sub, _, unsub := bus.Subscribe(context.Background(), "ctx-art", 256)
	go func() {
		for ev := range sub {
			evMu.Lock()
			allEvents = append(allEvents, ev)
			evMu.Unlock()
		}
	}()
	defer unsub()

	// Agent returns an artifact payload.
	agentInvoker := &artifactAgentInvoker{
		filename: "report.pdf",
		ct:       "application/pdf",
		data:     []byte("PDF content"),
	}

	// First call: LLM requests tool use; second call: stop.
	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{
				Type: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-1",
					Name:  "agent__doc-writer",
					Input: map[string]any{"input": "generate"},
				}},
				StopReason: "tool_use",
			},
		},
		[]llm.StreamEvent{
			{Type: "stop", StopReason: "end_turn"},
		},
	)

	cfg := orchestrator.Config{
		MaxIterations: 5,
		AllowedAgents: []string{"doc-writer"},
	}
	orch := orchestrator.New(cfg, mock, agentInvoker, newRecorder(), bus, nil).
		WithArtifactRecorder(ar)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-art", "ctx-art", domain.TextMessage(domain.RoleUser, "generate a report"), nil)
	require.NoError(t, err)

	// Give the subscriber goroutine a moment to process events.
	time.Sleep(50 * time.Millisecond)

	// Verify RecordArtifact was called exactly once.
	calls := ar.getCalls()
	require.Len(t, calls, 1, "RecordArtifact must be called exactly once")
	assert.Equal(t, "run-art", calls[0].RunID)
	assert.Equal(t, "report.pdf", calls[0].Filename)
	assert.Equal(t, "application/pdf", calls[0].ContentType)

	// Verify a "file" event was published.
	evMu.Lock()
	defer evMu.Unlock()
	fileEventCount := 0
	for _, ev := range allEvents {
		if ev.Type == "file" {
			fileEventCount++
		}
	}
	assert.Equal(t, 1, fileEventCount, "exactly one 'file' event must be published")
}

// TestOrchestrator_ArtifactEventContainsNoPayload verifies that the "file" event
// payload does NOT contain the raw binary data or data_base64 field.
func TestOrchestrator_ArtifactEventContainsNoPayload(t *testing.T) {
	ar := &fakeArtifactRecorder{}
	bus := event.New()

	fileEvents := make([]event.Event, 0)
	var evMu sync.Mutex
	sub, _, unsub := bus.Subscribe(context.Background(), "ctx-nodata", 256)
	go func() {
		for ev := range sub {
			if ev.Type == "file" {
				evMu.Lock()
				fileEvents = append(fileEvents, ev)
				evMu.Unlock()
			}
		}
	}()
	defer unsub()

	secretData := []byte("TOP SECRET BINARY CONTENT DO NOT LEAK")
	agentInvoker := &artifactAgentInvoker{
		filename: "secret.bin",
		ct:       "application/octet-stream",
		data:     secretData,
	}

	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{
				Type: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-2",
					Name:  "agent__secret-agent",
					Input: map[string]any{"input": "generate"},
				}},
				StopReason: "tool_use",
			},
		},
		[]llm.StreamEvent{
			{Type: "stop", StopReason: "end_turn"},
		},
	)

	cfg := orchestrator.Config{
		MaxIterations: 5,
		AllowedAgents: []string{"secret-agent"},
	}
	orch := orchestrator.New(cfg, mock, agentInvoker, newRecorder(), bus, nil).
		WithArtifactRecorder(ar)

	ctx := context.Background()
	_, err := orch.Run(ctx, "run-nodata", "ctx-nodata", domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	evMu.Lock()
	defer evMu.Unlock()
	require.NotEmpty(t, fileEvents, "at least one file event must be published")

	for _, ev := range fileEvents {
		payloadStr := string(ev.Payload)
		// The event payload must not contain the raw data or its base64 encoding.
		assert.NotContains(t, payloadStr, "data_base64",
			"file event must not contain data_base64 field")
		assert.NotContains(t, payloadStr, string(secretData),
			"file event must not contain raw binary data")
		assert.NotContains(t, payloadStr, base64.StdEncoding.EncodeToString(secretData),
			"file event must not contain base64-encoded data")
		// The event must contain the download URL.
		assert.Contains(t, payloadStr, "download_url",
			"file event must contain download_url")
		assert.Contains(t, payloadStr, "artifact_id",
			"file event must contain artifact_id")
	}
}

// TestOrchestrator_ArtifactTooLarge_ErrorEvent verifies that an oversized artifact
// causes an "error" event to be published, not a panic.
func TestOrchestrator_ArtifactTooLarge_ErrorEvent(t *testing.T) {
	ar := &fakeArtifactRecorder{
		err: runrecorder.ErrArtifactTooLarge,
	}
	bus := event.New()

	var errorEvents []event.Event
	var evMu sync.Mutex
	sub, _, unsub := bus.Subscribe(context.Background(), "ctx-toolarge", 256)
	go func() {
		for ev := range sub {
			if ev.Type == "error" {
				evMu.Lock()
				errorEvents = append(errorEvents, ev)
				evMu.Unlock()
			}
		}
	}()
	defer unsub()

	// Agent returns a small artifact but our fake recorder simulates the size error.
	agentInvoker := &artifactAgentInvoker{
		filename: "big.bin",
		ct:       "application/octet-stream",
		data:     []byte("small data but recorder says too large"),
	}

	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{
				Type: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-3",
					Name:  "agent__big-agent",
					Input: map[string]any{"input": "go"},
				}},
				StopReason: "tool_use",
			},
		},
		[]llm.StreamEvent{
			{Type: "stop", StopReason: "end_turn"},
		},
	)

	cfg := orchestrator.Config{
		MaxIterations: 5,
		AllowedAgents: []string{"big-agent"},
	}
	orch := orchestrator.New(cfg, mock, agentInvoker, newRecorder(), bus, nil).
		WithArtifactRecorder(ar)

	ctx := context.Background()
	// Must not panic — run completes normally despite the artifact error.
	_, err := orch.Run(ctx, "run-toolarge", "ctx-toolarge",
		domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err, "oversized artifact must not abort the run")

	time.Sleep(50 * time.Millisecond)

	evMu.Lock()
	defer evMu.Unlock()
	// At least one error event about the artifact must be published.
	found := false
	for _, ev := range errorEvents {
		if strings.Contains(string(ev.Payload), "1 MiB") {
			found = true
			break
		}
	}
	assert.True(t, found, "an error event mentioning the size limit must be published")
}

// errNotFoundSentinel is used to test ErrArtifactTooLarge sentinel propagation.
var _ = errors.New("test sentinel")

// ── Base64 pre-decode size guard tests ────────────────────────────────────────

// TestOrchestrator_ArtifactExactBoundaryEncoded verifies that an artifact whose
// base64-encoded input is exactly at the maximum allowed length is accepted and
// RecordArtifact is called. This is the exact-boundary case for the encoded-input
// guard (not the decoded-bytes guard in RecordArtifact itself).
func TestOrchestrator_ArtifactExactBoundaryEncoded(t *testing.T) {
	ar := &fakeArtifactRecorder{}
	bus := event.New()

	// Build exactly ArtifactMaxBytes of decoded data — its base64 encoding is
	// exactly artifactMaxBase64Bytes chars, which must pass the pre-decode guard.
	exactData := make([]byte, runrecorder.ArtifactMaxBytes)
	for i := range exactData {
		exactData[i] = 0xAB
	}

	agentInvoker := &artifactAgentInvoker{
		filename: "exact.bin",
		ct:       "application/octet-stream",
		data:     exactData,
	}
	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{
				Type: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-exact",
					Name:  "agent__doc-writer",
					Input: map[string]any{"input": "go"},
				}},
				StopReason: "tool_use",
			},
		},
		[]llm.StreamEvent{{Type: "stop", StopReason: "end_turn"}},
	)

	cfg := orchestrator.Config{
		MaxIterations: 5,
		AllowedAgents: []string{"doc-writer"},
	}
	orch := orchestrator.New(cfg, mock, agentInvoker, newRecorder(), bus, nil).
		WithArtifactRecorder(ar)

	_, err := orch.Run(context.Background(), "run-exact", "ctx-exact",
		domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	calls := ar.getCalls()
	require.Len(t, calls, 1, "RecordArtifact must be called for exact-boundary input")
	assert.Equal(t, "exact.bin", calls[0].Filename)
}

// TestOrchestrator_ArtifactOversizedEncodedInput verifies that an artifact whose
// base64-encoded string exceeds the pre-decode guard is rejected with an error
// event and without allocating or decoding the oversized payload.
func TestOrchestrator_ArtifactOversizedEncodedInput(t *testing.T) {
	ar := &fakeArtifactRecorder{}
	bus := event.New()

	var errorEvents []event.Event
	var evMu sync.Mutex
	sub, _, unsub := bus.Subscribe(context.Background(), "ctx-oversized", 256)
	go func() {
		for ev := range sub {
			if ev.Type == "error" {
				evMu.Lock()
				errorEvents = append(errorEvents, ev)
				evMu.Unlock()
			}
		}
	}()
	defer unsub()

	// Build a data_base64 string that is one byte longer than the maximum allowed
	// encoded length. We don't need valid base64 — the guard fires on length alone.
	oversizedEncoded := strings.Repeat("A", (runrecorder.ArtifactMaxBytes+2)/3*4+1)

	agentInvokerOversized := &rawBase64AgentInvoker{
		filename:   "oversized.bin",
		ct:         "application/octet-stream",
		dataBase64: oversizedEncoded,
	}
	mock := newMultiCallMockProvider(
		[]llm.StreamEvent{
			{
				Type: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-oversized",
					Name:  "agent__doc-writer",
					Input: map[string]any{"input": "go"},
				}},
				StopReason: "tool_use",
			},
		},
		[]llm.StreamEvent{{Type: "stop", StopReason: "end_turn"}},
	)

	cfg := orchestrator.Config{
		MaxIterations: 5,
		AllowedAgents: []string{"doc-writer"},
	}
	orch := orchestrator.New(cfg, mock, agentInvokerOversized, newRecorder(), bus, nil).
		WithArtifactRecorder(ar)

	_, err := orch.Run(context.Background(), "run-oversized", "ctx-oversized",
		domain.TextMessage(domain.RoleUser, "go"), nil)
	require.NoError(t, err, "oversized encoded input must not abort the run")

	time.Sleep(50 * time.Millisecond)

	// RecordArtifact must NOT have been called — the guard must fire before decode.
	calls := ar.getCalls()
	assert.Empty(t, calls, "RecordArtifact must not be called for oversized encoded input")

	// An error event must be published.
	evMu.Lock()
	defer evMu.Unlock()
	found := false
	for _, ev := range errorEvents {
		if strings.Contains(string(ev.Payload), "1 MiB") {
			found = true
			break
		}
	}
	assert.True(t, found, "an error event mentioning the size limit must be published")
}

// rawBase64AgentInvoker is like artifactAgentInvoker but accepts a pre-built
// base64 string so tests can inject oversized or malformed encoded input.
type rawBase64AgentInvoker struct {
	filename   string
	ct         string
	dataBase64 string
}

func (a *rawBase64AgentInvoker) Invoke(_ context.Context, _ string, _ string, _ json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{
		"artifact": map[string]any{
			"filename":     a.filename,
			"content_type": a.ct,
			"data_base64":  a.dataBase64,
		},
	}
	return json.Marshal(payload)
}

func (a *rawBase64AgentInvoker) InvokeForRun(ctx context.Context, tenantID, _ string, slug string, input json.RawMessage) (json.RawMessage, error) {
	return a.Invoke(ctx, tenantID, slug, input)
}

func (a *rawBase64AgentInvoker) InvokeForRunStreaming(ctx context.Context, tenantID, appID, slug string, input json.RawMessage, _ func(string, string, string)) (json.RawMessage, error) {
	return a.InvokeForRun(ctx, tenantID, appID, slug, input)
}
