package temporal

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/domain"
)

// Signaler wraps a Temporal client to implement admin.TemporalSignaler.
type Signaler struct {
	client client.Client
}

// NewSignaler creates a Signaler backed by the given Temporal client.
func NewSignaler(c client.Client) *Signaler {
	return &Signaler{client: c}
}

// SignalRun sends a human_input signal to the OrchestrationWorkflow identified
// by runID. payload should be a JSON-encoded domain.Message.
// Satisfies admin.TemporalSignaler.
func (s *Signaler) SignalRun(ctx context.Context, runID string, payload []byte) error {
	var msg domain.Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		// If not a valid Message, treat as plain text.
		msg = domain.TextMessage(domain.RoleUser, string(payload))
	}

	err := s.client.SignalWorkflow(ctx, runID, "", SignalHumanInput, msg)
	if err != nil {
		return fmt.Errorf("temporal: signal run %s: %w", runID, err)
	}
	return nil
}
