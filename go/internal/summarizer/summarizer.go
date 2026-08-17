// Package summarizer provides an in-process LLM-based conversation summarizer.
// It calls the configured LLM provider to produce a condensed factual summary
// of older conversation turns, allowing the orchestrator to keep its active
// context window small without discarding conversation history from the DB.
package summarizer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
)

const systemPrompt = `You are a conversation summarizer. Produce a concise factual summary preserving user goals, decisions, entities, open questions, and state an assistant needs to continue. Output only the summary text — no preamble, no markdown headers.`

// Summarizer calls an LLM to compress older conversation turns into a summary.
type Summarizer struct {
	provider llm.Provider
	model    string
	logger   *slog.Logger
}

// New creates a Summarizer backed by the given provider and model.
func New(provider llm.Provider, model string, logger *slog.Logger) *Summarizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Summarizer{provider: provider, model: model, logger: logger}
}

// Summarize condenses prior (existing summary) and msgs (older turns) into a
// new summary string. It calls provider.Stream with no tools and drains all
// text_delta events.
func (s *Summarizer) Summarize(ctx context.Context, prior string, msgs []domain.Message) (string, error) {
	// Build the user-turn prompt from prior summary + older messages.
	var userText string
	if prior != "" {
		userText = "Prior summary:\n" + prior + "\n\n"
	}
	userText += "Messages to compress:\n"
	for _, m := range msgs {
		text := m.Text()
		if text == "" {
			continue
		}
		userText += m.Role + ": " + text + "\n"
	}

	messages := []domain.Message{
		domain.TextMessage(domain.RoleUser, userText),
	}

	evCh, err := s.provider.Stream(ctx, messages, nil, llm.Options{
		Model:        s.model,
		MaxTokens:    1024,
		SystemPrompt: systemPrompt,
	})
	if err != nil {
		return "", fmt.Errorf("summarizer: stream: %w", err)
	}

	var result string
	for ev := range evCh {
		switch ev.Type {
		case "text_delta":
			result += ev.Delta
		case "error":
			return "", fmt.Errorf("summarizer: llm error: %v", ev.Error)
		}
	}
	return result, nil
}
