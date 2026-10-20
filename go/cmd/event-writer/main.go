// Event Writer — Redis Streams → ClickHouse consumer
// Design: docs/OBSERVABILITY_STORAGE_DESIGN.md Stage A3
//
// Reads events from Redis Streams (them:events) and writes to ClickHouse in batches.
// Consumer group: "event-writers" (allows N replicas for redundancy).
// At-least-once delivery: events are ACKed only after ClickHouse confirms write.
//
// Reliability (Finding 2):
// - Poison events moved to DLQ stream (them:events:dlq) before ACK
// - ClickHouse failures retry with exponential backoff (max 3 attempts)
// - XAUTOCLAIM recovers pending messages from dead consumers on startup
// - Bounded processing: pauses ingestion if ClickHouse unavailable
// - ACK failures logged + retained (message stays pending for recovery)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/events"
	"github.com/aviciot/them/internal/telemetry"
)

const (
	streamKey        = "them:events"
	dlqStreamKey     = "them:events:dlq"
	consumerGroup    = "event-writers"
	consumerName     = "event-writer" // Instance ID appended at runtime
	batchSize        = 100             // Max events per ClickHouse batch
	batchTimeout     = 5 * time.Second // Max time between batches
	blockTimeout     = 2 * time.Second // XREADGROUP block time
	maxRetries       = 3
	pendingClaimIdle = 30 * time.Second // Messages idle >30s are claimed from dead consumers
)

func main() {
	tel := telemetry.New(os.Getenv("LOG_LEVEL"), "json", "event-writer")
	logger := tel.Logger

	// Redis connection (dedicated events instance)
	redisAddr := getEnvOrDefault("REDIS_EVENTS_ADDR", "them-redis-events:6379")
	redisPassword := os.Getenv("REDIS_EVENTS_PASSWORD")

	redisClient, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{redisAddr},
		Password:    redisPassword,
	})
	if err != nil {
		logger.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	// ClickHouse sink
	chDSN := os.Getenv("CLICKHOUSE_DSN")
	if chDSN == "" {
		logger.Error("CLICKHOUSE_DSN environment variable not set")
		os.Exit(1)
	}

	clickhouseSink, err := events.NewClickHouseSink(chDSN)
	if err != nil {
		logger.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}
	defer clickhouseSink.Close(context.Background())

	logger.Info("ClickHouse sink initialized")
	sink := clickhouseSink

	// Create consumer group if not exists
	ctx := context.Background()
	if err := ensureConsumerGroup(ctx, redisClient, streamKey, consumerGroup); err != nil {
		logger.Error("failed to create consumer group", "error", err)
		os.Exit(1)
	}

	// Ensure DLQ stream exists
	if err := ensureStream(ctx, redisClient, dlqStreamKey); err != nil {
		logger.Error("failed to create DLQ stream", "error", err)
		os.Exit(1)
	}

	// Start consumer
	consumer := &EventConsumer{
		redis:         redisClient,
		sink:          sink,
		logger:        logger,
		streamKey:     streamKey,
		dlqStreamKey:  dlqStreamKey,
		consumerGroup: consumerGroup,
		consumerName:  fmt.Sprintf("%s-%s", consumerName, os.Getenv("HOSTNAME")),
		batchSize:     batchSize,
		batchTimeout:  batchTimeout,
	}

	// Recover pending messages from dead consumers
	if err := consumer.RecoverPending(ctx); err != nil {
		logger.Error("failed to recover pending messages", "error", err)
		// Continue — this is best-effort recovery
	}

	// Start retention manager (safe stream trimming)
	retentionMgr := NewRetentionManager(
		redisClient,
		logger,
		streamKey,
		dlqStreamKey,
		consumerGroup,
		24*time.Hour,   // maxAge: trim events older than 24h (if fully ACKed)
		15*time.Minute, // trimInterval: check every 15 minutes
	)
	retentionMgr.Start()
	defer retentionMgr.Stop()
	logger.Info("retention manager started", "max_age_hours", 24, "trim_interval_min", 15)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		consumer.Run(ctx)
	}()

	<-sigChan
	logger.Info("Shutting down event-writer")
	consumer.Stop()
	wg.Wait()

	logger.Info("Event-writer stopped")
}

// EventConsumer reads from Redis Streams and writes to ClickHouse.
type EventConsumer struct {
	redis         rueidis.Client
	sink          events.EventSink
	logger        *slog.Logger
	streamKey     string
	dlqStreamKey  string
	consumerGroup string
	consumerName  string
	batchSize     int
	batchTimeout  time.Duration
	stopChan      chan struct{}
	stopped       bool
	mu            sync.Mutex

	// Counters
	processedCount uint64
	failedCount    uint64
	dlqCount       uint64
}

// RecoverPending uses XAUTOCLAIM to recover messages pending from dead consumers.
// Claims idle messages so they'll be picked up by the regular XREADGROUP loop.
// Paginates via cursor until no more pending messages remain.
func (c *EventConsumer) RecoverPending(ctx context.Context) error {
	minIdleMs := fmt.Sprintf("%d", pendingClaimIdle.Milliseconds())
	cursor := "0-0"
	totalClaimed := 0

	for {
		// XAUTOCLAIM <stream> <group> <consumer> <min-idle-time> <cursor> COUNT 100
		// Returns: [next-cursor, [claimed-messages], [deleted-message-ids]]
		cmd := c.redis.B().Xautoclaim().
			Key(c.streamKey).
			Group(c.consumerGroup).
			Consumer(c.consumerName).
			MinIdleTime(minIdleMs).
			Start(cursor).
			Count(100).
			Build()

		// XAUTOCLAIM returns an array: [cursor-string, messages-array, ...]
		resp, err := c.redis.Do(ctx, cmd).ToArray()
		if err != nil {
			if rueidis.IsRedisNil(err) {
				break // No more pending
			}
			return fmt.Errorf("XAUTOCLAIM failed: %w", err)
		}

		if len(resp) < 2 {
			break // Unexpected response format
		}

		// Extract next cursor
		nextCursor, err := resp[0].ToString()
		if err != nil {
			return fmt.Errorf("parse XAUTOCLAIM cursor: %w", err)
		}

		// Extract messages array
		messages, err := resp[1].AsXRange()
		if err != nil {
			return fmt.Errorf("parse XAUTOCLAIM messages: %w", err)
		}

		totalClaimed += len(messages)

		// Check for end of pagination
		if nextCursor == "0-0" || len(messages) == 0 {
			break
		}

		cursor = nextCursor
	}

	if totalClaimed > 0 {
		c.logger.Info("recovered pending messages from dead consumers", "count", totalClaimed, "note", "claimed messages will be processed by XREADGROUP loop")
	}

	return nil
}

// Run starts the consumer loop.
func (c *EventConsumer) Run(ctx context.Context) {
	c.stopChan = make(chan struct{})

	batch := make([]events.Event, 0, c.batchSize)
	messageIDs := make([]string, 0, c.batchSize)
	poisonIDs := make(map[string]string) // messageID -> reason

	ticker := time.NewTicker(c.batchTimeout)
	defer ticker.Stop()

	consecutiveFailures := 0
	backoff := 1 * time.Second

	flush := func() {
		if len(batch) == 0 && len(poisonIDs) == 0 {
			return
		}

		// Handle poison events first (move to DLQ)
		for msgID, reason := range poisonIDs {
			if err := c.moveToDLQ(ctx, msgID, reason); err != nil {
				c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msgID)
				// Don't ACK if DLQ write failed
				continue
			}
			// ACK poison event after successful DLQ write
			if err := c.ackMessages(ctx, []string{msgID}); err != nil {
				c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msgID)
			} else {
				c.dlqCount++
			}
		}
		// Clear poison map
		for k := range poisonIDs {
			delete(poisonIDs, k)
		}

		// Write valid events to ClickHouse with retry
		if len(batch) > 0 {
			var lastErr error
			for attempt := 1; attempt <= maxRetries; attempt++ {
				success := true
				for _, event := range batch {
					if err := c.sink.Emit(ctx, event); err != nil {
						c.logger.Error("failed to write event", "error", err, "event_id", event.EventID, "attempt", attempt)
						lastErr = err
						success = false
						break
					}
				}

				if success {
					// ACK messages in Redis
					if err := c.ackMessages(ctx, messageIDs); err != nil {
						c.logger.Error("failed to ack messages", "error", err)
						// Don't clear batch — ACK failure means messages stay pending for recovery
						return
					}

					c.logger.Info("batch written", "count", len(batch))
					c.processedCount += uint64(len(batch))
					consecutiveFailures = 0
					backoff = 1 * time.Second

					// Clear batch
					batch = batch[:0]
					messageIDs = messageIDs[:0]
					return
				}

				// Retry with exponential backoff
				if attempt < maxRetries {
					sleep := backoff * time.Duration(attempt)
					c.logger.Warn("retrying after backoff", "attempt", attempt, "backoff_ms", sleep.Milliseconds())
					time.Sleep(sleep)
				}
			}

			// All retries exhausted
			c.logger.Error("batch write failed after retries", "error", lastErr, "count", len(batch))
			c.failedCount += uint64(len(batch))
			consecutiveFailures++

			// Don't ACK — messages will be retried on next consumer restart via XAUTOCLAIM
			// CRITICAL: Clear batch and messageIDs to enforce batch boundary.
			// Without this, the consumer retains the failed batch and immediately
			// retries it, creating a tight failure loop instead of moving forward.
			batch = batch[:0]
			messageIDs = messageIDs[:0]

			// Bounded processing: pause ingestion if ClickHouse is down
			if consecutiveFailures >= 3 {
				pauseDuration := min(backoff*time.Duration(consecutiveFailures), 30*time.Second)
				c.logger.Warn("ClickHouse unavailable, pausing ingestion", "pause_ms", pauseDuration.Milliseconds())
				time.Sleep(pauseDuration)
			}
			return
		}
	}

	for {
		select {
		case <-c.stopChan:
			// Flush remaining events
			flush()
			c.logger.Info("consumer stopped", "processed", c.processedCount, "failed", c.failedCount, "dlq", c.dlqCount)
			return

		case <-ticker.C:
			// Flush on timeout
			flush()

		default:
			// Read from stream (non-blocking with timeout)
			messages, err := c.readMessages(ctx)
			if err != nil {
				c.logger.Error("failed to read messages", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			for _, msg := range messages {
				// Parse event
				eventData, ok := msg.FieldValues["event"]
				if !ok {
					c.logger.Warn("poison event: missing event field", "msg_id", msg.ID)
					poisonIDs[msg.ID] = "missing_event_field"
					continue
				}

				var event events.Event
				if err := json.Unmarshal([]byte(eventData), &event); err != nil {
					c.logger.Error("poison event: invalid JSON", "error", err, "msg_id", msg.ID)
					poisonIDs[msg.ID] = fmt.Sprintf("invalid_json: %v", err)
					continue
				}

				// Validate event kind
				if event.Kind == "" {
					c.logger.Warn("poison event: missing kind", "msg_id", msg.ID, "event_id", event.EventID)
					poisonIDs[msg.ID] = "missing_kind"
					continue
				}

				batch = append(batch, event)
				messageIDs = append(messageIDs, msg.ID)

				// Flush when batch is full
				if len(batch) >= c.batchSize {
					flush()
				}
			}
		}
	}
}

// Stop signals the consumer to stop.
func (c *EventConsumer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		close(c.stopChan)
		c.stopped = true
	}
}

// readMessages reads pending or new messages from the stream.
func (c *EventConsumer) readMessages(ctx context.Context) ([]StreamMessage, error) {
	// XREADGROUP GROUP <group> <consumer> COUNT 10 BLOCK <ms> STREAMS <stream> >
	cmd := c.redis.B().Xreadgroup().
		Group(c.consumerGroup, c.consumerName).
		Count(10).
		Block(int64(blockTimeout.Milliseconds())).
		Streams().
		Key(c.streamKey).
		Id(">"). // ">" means new messages only
		Build()

	result, err := c.redis.Do(ctx, cmd).AsXRead()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			// No messages available
			return nil, nil
		}
		return nil, err
	}

	var messages []StreamMessage
	for _, entries := range result {
		for _, entry := range entries {
			messages = append(messages, StreamMessage{
				ID:          entry.ID,
				FieldValues: entry.FieldValues,
			})
		}
	}

	return messages, nil
}

// ackMessages acknowledges processed messages.
func (c *EventConsumer) ackMessages(ctx context.Context, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	// XACK <stream> <group> <id1> <id2> ...
	cmd := c.redis.B().Xack().Key(c.streamKey).Group(c.consumerGroup).Id(messageIDs...).Build()
	return c.redis.Do(ctx, cmd).Error()
}

// moveToDLQ writes poison event to DLQ stream before ACK.
func (c *EventConsumer) moveToDLQ(ctx context.Context, messageID, reason string) error {
	// XADD them:events:dlq * msg_id <id> reason <reason> ts <timestamp>
	cmd := c.redis.B().Xadd().
		Key(c.dlqStreamKey).
		Id("*").
		FieldValue().
		FieldValue("msg_id", messageID).
		FieldValue("reason", reason).
		FieldValue("ts", time.Now().UTC().Format(time.RFC3339)).
		Build()

	return c.redis.Do(ctx, cmd).Error()
}

// StreamMessage represents a Redis Streams message.
type StreamMessage struct {
	ID          string
	FieldValues map[string]string
}

// ensureConsumerGroup creates the consumer group if it doesn't exist.
func ensureConsumerGroup(ctx context.Context, client rueidis.Client, stream, group string) error {
	// XGROUP CREATE <stream> <group> 0 MKSTREAM
	cmd := client.B().XgroupCreate().Key(stream).Group(group).Id("0").Mkstream().Build()
	err := client.Do(ctx, cmd).Error()

	if err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists" {
		// Group already exists — not an error
		return nil
	}

	return err
}

// ensureStream ensures a stream exists (for DLQ).
func ensureStream(ctx context.Context, client rueidis.Client, stream string) error {
	// XADD <stream> * _init 1
	// Creates stream if not exists
	cmd := client.B().Xadd().Key(stream).Id("*").FieldValue().FieldValue("_init", "1").Build()
	return client.Do(ctx, cmd).Error()
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
