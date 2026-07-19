package llm

import (
	"context"

	"github.com/aviciot/them/internal/domain"
)

// MockProvider is an in-process LLM provider for use in tests.
// It returns a canned sequence of events when Stream is called.
type MockProvider struct {
	// events is the sequence of StreamEvents to send on each call.
	events []StreamEvent
	// Calls records the messages passed to each Stream invocation.
	Calls [][]domain.Message
}

// NewMockProvider creates a MockProvider that emits the given events.
// If events is nil, a single "stop" event is sent.
func NewMockProvider(events []StreamEvent) *MockProvider {
	return &MockProvider{events: events}
}

// Stream sends the configured events on a channel and closes it.
// It respects ctx cancellation — when ctx is cancelled the goroutine stops
// and closes the channel.
func (m *MockProvider) Stream(ctx context.Context, messages []domain.Message, _ []ToolDef, _ Options) (<-chan StreamEvent, error) {
	m.Calls = append(m.Calls, messages)

	events := m.events
	if events == nil {
		// No events configured — close the channel immediately.
		ch := make(chan StreamEvent)
		close(ch)
		return ch, nil
	}

	// Use a buffered channel only for the default stop case; otherwise
	// use an unbuffered channel so ctx cancellation takes effect promptly.
	out := make(chan StreamEvent)
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
