package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMockProvider_streamsAllEventsInOrder verifies that a MockProvider emits
// all configured events in the order they were provided, then closes the
// channel.
func TestMockProvider_streamsAllEventsInOrder(t *testing.T) {
	want := []StreamEvent{
		{Type: "text_delta", Delta: "Hello"},
		{Type: "text_delta", Delta: ", world"},
		{Type: "stop"},
	}

	p := NewMockProvider(want)
	ch, err := p.Stream(context.Background(), nil, nil, Options{})
	require.NoError(t, err)

	var got []StreamEvent
	for ev := range ch {
		got = append(got, ev)
	}

	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, w.Type, got[i].Type, "event %d type", i)
		assert.Equal(t, w.Delta, got[i].Delta, "event %d delta", i)
	}
}

// TestMockProvider_respectsContextCancellation verifies that the MockProvider
// stops streaming and closes the channel when ctx is cancelled, without
// hanging.
func TestMockProvider_respectsContextCancellation(t *testing.T) {
	// Use a large response set so cancellation fires before all events are sent.
	responses := make([]StreamEvent, 1000)
	for i := range responses {
		responses[i] = StreamEvent{Type: "text_delta", Delta: "x"}
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := NewMockProvider(responses)
	ch, err := p.Stream(ctx, nil, nil, Options{})
	require.NoError(t, err)

	// Receive a few events then cancel.
	received := 0
	for ev := range ch {
		received++
		_ = ev
		if received >= 5 {
			cancel()
		}
	}

	// Channel must have closed (range exited). We don't check the exact count
	// because scheduling is non-deterministic, but we require it to be far
	// fewer than 1000 (cancel fired at 5).
	assert.Less(t, received, 100, "expected cancellation to stop streaming well before 1000 events")
}

// TestToolDef_emptyNameReturnsError verifies that Validate rejects a ToolDef
// with an empty Name.
func TestToolDef_emptyNameReturnsError(t *testing.T) {
	td := ToolDef{
		Name:        "",
		Description: "does something useful",
	}
	err := td.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrToolDefInvalid), "expected ErrToolDefInvalid, got: %v", err)
}

// TestToolDef_emptyDescriptionReturnsError verifies that Validate rejects a
// ToolDef with an empty Description.
func TestToolDef_emptyDescriptionReturnsError(t *testing.T) {
	td := ToolDef{
		Name:        "my_tool",
		Description: "",
	}
	err := td.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrToolDefInvalid), "expected ErrToolDefInvalid, got: %v", err)
}

// TestToolDef_validDoesNotReturnError verifies that a fully-specified ToolDef
// passes validation.
func TestToolDef_validDoesNotReturnError(t *testing.T) {
	td := ToolDef{
		Name:        "search",
		Description: "Search the internet for information",
	}
	assert.NoError(t, td.Validate())
}

// TestMockProvider_emptyResponsesClosesChannelImmediately verifies that a
// MockProvider with no configured responses closes the channel without sending
// anything.
func TestMockProvider_emptyResponsesClosesChannelImmediately(t *testing.T) {
	p := NewMockProvider(nil)
	ch, err := p.Stream(context.Background(), nil, nil, Options{})
	require.NoError(t, err)

	// The channel should close promptly.
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel did not close promptly for empty provider")
	}
}
