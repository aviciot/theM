package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/rueidis"
)

// RedisSink writes events to Redis Streams for async processing.
// Non-blocking: events are buffered in memory and flushed by a background goroutine.
//
// Reliability (Finding 3):
// - Bounded retries on Redis failure (3 attempts with backoff)
// - Observable counters: dropped (buffer full), failed (Redis write failed after retries)
// - Graceful Close: drains buffer + honors deadline, rejects Emit after Close
type RedisSink struct {
	client      rueidis.Client
	streamKey   string
	buffer      chan Event
	bufferSize  int
	flushEvery  time.Duration
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc

	// Counters (atomic for safe concurrent access)
	droppedCtr  atomic.Int64 // buffer full
	failedCtr   atomic.Int64 // Redis write failed after retries

	closed      atomic.Bool
	mu          sync.Mutex
}

// NewRedisSink creates a Redis Streams event sink.
// streamKey: Redis stream name (e.g., "them:events")
// bufferSize: max events to buffer in memory before blocking (default: 10000)
// flushEvery: max time between flushes (default: 1s)
func NewRedisSink(client rueidis.Client, streamKey string, bufferSize int, flushEvery time.Duration) *RedisSink {
	if bufferSize == 0 {
		bufferSize = 10000
	}
	if flushEvery == 0 {
		flushEvery = 1 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	sink := &RedisSink{
		client:     client,
		streamKey:  streamKey,
		buffer:     make(chan Event, bufferSize),
		bufferSize: bufferSize,
		flushEvery: flushEvery,
		ctx:        ctx,
		cancel:     cancel,
	}

	// Start background flusher
	sink.wg.Add(1)
	go sink.flushLoop()

	return sink
}

// Emit adds an event to the buffer. Non-blocking until buffer is full.
// If buffer is full, returns error (best-effort delivery, operational events).
// Returns error if sink is closed.
func (r *RedisSink) Emit(ctx context.Context, event Event) error {
	if r.closed.Load() {
		return fmt.Errorf("redis sink: closed")
	}

	select {
	case r.buffer <- event:
		return nil
	default:
		// Buffer full — drop event and increment counter
		dropped := r.droppedCtr.Add(1)
		return fmt.Errorf("redis sink: buffer full (dropped %d events total)", dropped)
	}
}

// Close stops the background flusher and drains remaining buffered events.
// Honors the deadline from ctx. After Close, Emit will reject new events.
func (r *RedisSink) Close(ctx context.Context) error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil // Already closed
	}

	// Signal flusher to stop accepting new events
	r.cancel()

	// Drain remaining buffered events with deadline
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Deadline exceeded — return partial drain
		return fmt.Errorf("redis sink: close deadline exceeded, some events may be lost")
	}
}

// DroppedCount returns the number of events dropped due to buffer overflow.
func (r *RedisSink) DroppedCount() int64 {
	return r.droppedCtr.Load()
}

// FailedCount returns the number of events that failed Redis write after retries.
func (r *RedisSink) FailedCount() int64 {
	return r.failedCtr.Load()
}

// flushLoop batches events and writes to Redis Streams.
func (r *RedisSink) flushLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()

	batch := make([]Event, 0, 100)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		// Write batch to Redis Streams with bounded retries
		if err := r.writeBatchWithRetry(batch); err != nil {
			// All retries exhausted — count as failed
			r.failedCtr.Add(int64(len(batch)))
		}

		// Clear batch (either succeeded or exhausted retries)
		batch = batch[:0]
	}

	for {
		select {
		case <-r.ctx.Done():
			// Drain remaining buffered events (deadline is in Close)
			draining := true
			for draining {
				select {
				case event := <-r.buffer:
					batch = append(batch, event)
					if len(batch) >= 100 {
						flush()
					}
				default:
					draining = false
				}
			}
			// Flush final batch
			flush()
			return

		case event := <-r.buffer:
			batch = append(batch, event)

			// Flush when batch reaches 100 events
			if len(batch) >= 100 {
				flush()
			}

		case <-ticker.C:
			// Flush on timer
			flush()
		}
	}
}

// writeBatchWithRetry writes a batch with bounded retries (max 3 attempts).
func (r *RedisSink) writeBatchWithRetry(events []Event) error {
	const maxRetries = 3
	backoff := 100 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := r.writeBatch(ctx, events)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err
		if attempt < maxRetries {
			time.Sleep(backoff * time.Duration(attempt))
		}
	}

	return fmt.Errorf("redis sink: write failed after %d attempts: %w", maxRetries, lastErr)
}

// writeBatch writes a batch of events to Redis Streams using XADD (single attempt).
func (r *RedisSink) writeBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		// Serialize event to JSON
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("redis sink: marshal event: %w", err)
		}

		// XADD stream_key * event <json>
		cmd := r.client.B().Xadd().
			Key(r.streamKey).
			Id("*").
			FieldValue().
			FieldValue("event", string(data)).
			Build()

		if err := r.client.Do(ctx, cmd).Error(); err != nil {
			return fmt.Errorf("redis sink: xadd: %w", err)
		}
	}

	return nil
}
