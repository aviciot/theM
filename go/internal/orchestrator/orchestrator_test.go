package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func newRecorder() *runrecorder.Recorder {
	return runrecorder.New(&fakeDBQuerier{})
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

func (f *fakeHistoryLoader) LoadHistory(_ context.Context, _ string, _ int) ([]domain.Message, error) {
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

func (f *fakeCheckpointWriter) WriteMessage(_ context.Context, _, _ string, msg domain.Message) error {
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

func (f *fakeAgentInvoker) Invoke(ctx context.Context, slug string, _ json.RawMessage) (json.RawMessage, error) {
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
