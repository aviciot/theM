package summarizer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
)

func TestSummarize_DrainsDeltaEvents(t *testing.T) {
	events := []llm.StreamEvent{
		{Type: "text_delta", Delta: "Summary: "},
		{Type: "text_delta", Delta: "user wants X."},
		{Type: "stop"},
	}
	mock := llm.NewMockProvider(events)
	s := New(mock, "claude-test", nil)

	got, err := s.Summarize(context.Background(), "", []domain.Message{
		domain.TextMessage(domain.RoleUser, "I want X"),
		domain.TextMessage(domain.RoleAssistant, "OK"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Summary:") {
		t.Errorf("expected summary text, got: %q", got)
	}
}

func TestSummarize_IncludesPriorSummary(t *testing.T) {
	events := []llm.StreamEvent{
		{Type: "text_delta", Delta: "new summary"},
		{Type: "stop"},
	}
	mock := llm.NewMockProvider(events)
	s := New(mock, "claude-test", nil)

	_, err := s.Summarize(context.Background(), "prior context here", []domain.Message{
		domain.TextMessage(domain.RoleUser, "continue"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.Calls) == 0 {
		t.Fatal("provider was not called")
	}
	// The first (and only) message to the provider should contain the prior summary.
	sent := mock.Calls[0]
	if len(sent) == 0 {
		t.Fatal("no messages sent to provider")
	}
	userMsg := sent[0].Text()
	if !strings.Contains(userMsg, "prior context here") {
		t.Errorf("prior summary not included in prompt: %q", userMsg)
	}
}

func TestSummarize_PropagatesLLMError(t *testing.T) {
	events := []llm.StreamEvent{
		{Type: "error", Error: fmt.Errorf("llm unavailable")},
	}
	mock := llm.NewMockProvider(events)
	s := New(mock, "claude-test", nil)

	_, err := s.Summarize(context.Background(), "", []domain.Message{
		domain.TextMessage(domain.RoleUser, "test"),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSummarize_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events := []llm.StreamEvent{
		{Type: "text_delta", Delta: "should not arrive"},
		{Type: "stop"},
	}
	mock := llm.NewMockProvider(events)
	s := New(mock, "claude-test", nil)

	// Should not block or panic — may return empty or error.
	_, _ = s.Summarize(ctx, "", []domain.Message{
		domain.TextMessage(domain.RoleUser, "hello"),
	})
}
