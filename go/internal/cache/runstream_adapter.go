package cache

import (
	"context"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/runstream"
)

// RunStreamRedisClient adapts a rueidis client to satisfy runstream.Subscriber.
type RunStreamRedisClient struct {
	client rueidis.Client
}

// NewRunStreamRedisClient wraps a rueidis client.
func NewRunStreamRedisClient(c rueidis.Client) *RunStreamRedisClient {
	return &RunStreamRedisClient{client: c}
}

// Subscribe subscribes to the named channel and returns a channel of message
// payloads. The channel is closed when ctx is cancelled.
//
// Implementation note: rueidis.Receive is blocking, so it runs in a goroutine.
// Messages are delivered to a buffered output channel (buffer 256). The
// goroutine exits when ctx is cancelled and the underlying Receive call returns.
func (r *RunStreamRedisClient) Subscribe(ctx context.Context, channel string) (<-chan string, error) {
	out := make(chan string, 256)

	go func() {
		defer close(out)

		cmd := r.client.B().Subscribe().Channel(channel).Build()
		_ = r.client.Receive(ctx, cmd, func(msg rueidis.PubSubMessage) {
			select {
			case out <- msg.Message:
			case <-ctx.Done():
			}
		})
	}()

	return out, nil
}

// compile-time interface check — kept here for clarity, authoritative check is
// in runstream_adapter_test.go.
var _ runstream.Subscriber = (*RunStreamRedisClient)(nil)
