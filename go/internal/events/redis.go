package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/rueidis"
)

// RedisSink writes events to Redis Streams for async processing.
// Non-blocking: events are buffered in memory and flushed by a background goroutine.
type RedisSink struct {
	client      rueidis.Client
	streamKey   string
	buffer      chan Event
	bufferSize  int
	flushEvery  time.Duration
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	droppedCtr  int64 // counter for dropped events (buffer full)
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
func (r *RedisSink) Emit(ctx context.Context, event Event) error {
	select {
	case r.buffer <- event:
		return nil
	default:
		// Buffer full — drop event and increment counter
		r.mu.Lock()
		r.droppedCtr++
		r.mu.Unlock()
		return fmt.Errorf("redis sink: buffer full (dropped %d events total)", r.droppedCtr)
	}
}

// Close stops the background flusher and flushes remaining buffered events.
func (r *RedisSink) Close(ctx context.Context) error {
	r.cancel()
	r.wg.Wait()
	return nil
}

// DroppedCount returns the number of events dropped due to buffer overflow.
func (r *RedisSink) DroppedCount() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.droppedCtr
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

		// Write batch to Redis Streams
		if err := r.writeBatch(batch); err != nil {
			// Log error but don't fail — best effort
			// TODO: emit metric for failed writes
			_ = err
		}

		// Clear batch
		batch = batch[:0]
	}

	for {
		select {
		case <-r.ctx.Done():
			// Flush remaining events before exit
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

// writeBatch writes a batch of events to Redis Streams using XADD.
func (r *RedisSink) writeBatch(events []Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
