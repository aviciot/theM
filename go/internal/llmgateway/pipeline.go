package llmgateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/middleware/pii"
	"github.com/aviciot/them/internal/middleware/promptguard"
)

// ErrPipelineBlocked is returned when a pre-step blocks the request.
var ErrPipelineBlocked = errors.New("llmgateway: request blocked by pipeline step")

// runPreSteps runs all profile steps with position < 100 (pre-LLM) against the
// last user message text. Returns the (possibly redacted) text, or
// ErrPipelineBlocked if any step blocks the request.
func runPreSteps(ctx context.Context, steps []ProfileStep, messages []ChatMessage) ([]ChatMessage, error) {
	if len(steps) == 0 {
		return messages, nil
	}
	// Find the last user message — that's what we scan/redact.
	lastIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastIdx = i
			break
		}
	}
	if lastIdx < 0 {
		return messages, nil
	}

	text := messages[lastIdx].Content
	for _, step := range steps {
		if step.Position >= 100 {
			continue // post-LLM step
		}
		result, err := runStep(ctx, step.DefSlug, text, step.Config)
		if err != nil {
			// Fail-open: processor error does not block the call.
			continue
		}
		if result.Block {
			return nil, ErrPipelineBlocked
		}
		if result.Modified != nil && result.Modified.Text != "" {
			text = result.Modified.Text
		}
	}

	out := make([]ChatMessage, len(messages))
	copy(out, messages)
	out[lastIdx].Content = text
	return out, nil
}

// runPostSteps runs all profile steps with position >= 100 (post-LLM) against
// the response text. Returns the (possibly modified) text.
func runPostSteps(ctx context.Context, steps []ProfileStep, responseText string) (string, error) {
	if len(steps) == 0 {
		return responseText, nil
	}
	text := responseText
	for _, step := range steps {
		if step.Position < 100 {
			continue // pre-LLM step
		}
		result, err := runStep(ctx, step.DefSlug, text, step.Config)
		if err != nil {
			continue // fail-open
		}
		if result.Block {
			return "", ErrPipelineBlocked
		}
		if result.Modified != nil && result.Modified.Text != "" {
			text = result.Modified.Text
		}
	}
	return text, nil
}

// runStep dispatches to the correct processor by slug.
// Unknown slugs are silently skipped (fail-open).
func runStep(ctx context.Context, defSlug, text string, cfg json.RawMessage) (middleware.Result, error) {
	part := middleware.Part{Kind: "text", Text: text}
	switch defSlug {
	case "pii_redact":
		return pii.New().Process(ctx, part, cfg)
	case "prompt_inject":
		return promptguard.New().Process(ctx, part, cfg)
	case "guard_default":
		// Combined PII + prompt injection: run both, return block if either blocks.
		piiRes, err := pii.New().Process(ctx, part, cfg)
		if err != nil {
			return middleware.Result{}, err
		}
		if piiRes.Block {
			return piiRes, nil
		}
		modifiedText := text
		if piiRes.Modified != nil && piiRes.Modified.Text != "" {
			modifiedText = piiRes.Modified.Text
		}
		injPart := middleware.Part{Kind: "text", Text: modifiedText}
		injRes, err := promptguard.New().Process(ctx, injPart, cfg)
		if err != nil {
			return middleware.Result{}, err
		}
		if injRes.Block {
			return injRes, nil
		}
		// Return PII result (carries redaction) with injection outcome merged.
		if piiRes.Modified != nil {
			return piiRes, nil
		}
		return injRes, nil
	default:
		// file-guard, cache_default, unknown: skip on hot path (file guard is async).
		return middleware.Result{Outcome: "skipped"}, nil
	}
}
