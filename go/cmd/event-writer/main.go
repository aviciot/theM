// Event Writer — Redis Streams → ClickHouse consumer
// Design: docs/_archive/OBSERVABILITY_STORAGE_DESIGN.md Stage A3
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
	"net/http"
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

	// Health check HTTP server
	healthAddr := getEnvOrDefault("HEALTH_ADDR", ":8080")
	healthServer := &http.Server{
		Addr: healthAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}

			// Get consumer lag
			lag, pending, err := consumer.GetLag(r.Context())
			if err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprintf(w, `{"status":"error","error":"%s"}`, err.Error())
				return
			}

			// Health check fails if lag > 1000 or pending > 100
			status := "healthy"
			code := http.StatusOK
			if lag > 1000 || pending > 100 {
				status = "degraded"
				code = http.StatusServiceUnavailable
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			fmt.Fprintf(w, `{"status":"%s","lag":%d,"pending":%d,"processed":%d,"failed":%d,"dlq":%d}`,
				status, lag, pending, consumer.processedCount, consumer.failedCount, consumer.dlqCount)
		}),
	}

	// Start health server
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("health check listening", "addr", healthAddr)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("health server error", "error", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	wg.Add(1)
	go func() {
		defer wg.Done()
		consumer.Run(ctx)
	}()

	<-sigChan
	logger.Info("Shutting down event-writer")

	// Shutdown health server
	healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer healthCancel()
	if err := healthServer.Shutdown(healthCtx); err != nil {
		logger.Error("health server shutdown error", "error", err)
	}

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
// Processes reclaimed messages immediately through validation, persistence/DLQ, and ACK.
// Paginates via cursor until cursor returns "0-0".
func (c *EventConsumer) RecoverPending(ctx context.Context) error {
	minIdleMs := fmt.Sprintf("%d", pendingClaimIdle.Milliseconds())
	cursor := "0-0"
	totalRecovered := 0
	totalPoison := 0
	totalFailed := 0

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

		// Process each reclaimed message immediately
		for _, msg := range messages {
			// Parse event
			eventData, ok := msg.FieldValues["event"]
			if !ok {
				c.logger.Warn("poison event during recovery: missing event field", "msg_id", msg.ID)
				if err := c.moveToDLQ(ctx, msg.ID, "missing_event_field"); err != nil {
					c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msg.ID)
					totalFailed++
					continue
				}
				if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
					c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msg.ID)
					totalFailed++
				} else {
					totalPoison++
				}
				continue
			}

			var event events.Event
			if err := json.Unmarshal([]byte(eventData), &event); err != nil {
				c.logger.Error("poison event during recovery: invalid JSON", "error", err, "msg_id", msg.ID)
				if err := c.moveToDLQ(ctx, msg.ID, fmt.Sprintf("invalid_json: %v", err)); err != nil {
					c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msg.ID)
					totalFailed++
					continue
				}
				if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
					c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msg.ID)
					totalFailed++
				} else {
					totalPoison++
				}
				continue
			}

			// Validate event kind
			if event.Kind == "" {
				c.logger.Warn("poison event during recovery: missing kind", "msg_id", msg.ID, "event_id", event.EventID)
				if err := c.moveToDLQ(ctx, msg.ID, "missing_kind"); err != nil {
					c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msg.ID)
					totalFailed++
					continue
				}
				if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
					c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msg.ID)
					totalFailed++
				} else {
					totalPoison++
				}
				continue
			}

			// Validate and write event
			var lastErr error
			success := false

			for attempt := 1; attempt <= maxRetries; attempt++ {
				if err := c.sink.Emit(ctx, event); err != nil {
					// Check if malformed (validation error)
					if events.IsMalformedEvent(err) {
						// Don't retry validation errors
						c.logger.Warn("poison event during recovery", "msg_id", msg.ID, "event_id", event.EventID, "error", err)
						lastErr = err
						break // Stop retrying
					}

					// Transient error → retry
					c.logger.Error("failed to write recovered event (transient)", "error", err, "event_id", event.EventID, "msg_id", msg.ID, "attempt", attempt)
					lastErr = err
					if attempt < maxRetries {
						time.Sleep(time.Second * time.Duration(attempt))
					}
				} else {
					success = true
					break
				}
			}

			// Handle result
			if success {
				// ACK after successful write
				if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
					c.logger.Error("failed to ACK recovered event", "error", err, "msg_id", msg.ID)
					totalFailed++
				} else {
					totalRecovered++
				}
			} else if events.IsMalformedEvent(lastErr) {
				// Move poison to DLQ
				if err := c.moveToDLQ(ctx, msg.ID, fmt.Sprintf("validation_failed: %v", lastErr)); err != nil {
					c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msg.ID)
					totalFailed++
					continue // Leave pending for manual intervention
				}
				// ACK poison event after DLQ write
				if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
					c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msg.ID)
					totalFailed++
				} else {
					totalPoison++
				}
			} else {
				// Transient failure after retries → leave pending for next recovery
				c.logger.Error("recovered event write failed after retries (transient)", "error", lastErr, "msg_id", msg.ID)
				totalFailed++
				// Don't ACK - will retry in next RecoverPending call
			}
		}

		// Continue pagination until cursor is "0-0"
		if nextCursor == "0-0" {
			break
		}

		cursor = nextCursor
	}

	if totalRecovered > 0 || totalPoison > 0 || totalFailed > 0 {
		c.logger.Info("recovery complete",
			"recovered", totalRecovered,
			"poison_to_dlq", totalPoison,
			"failed", totalFailed)
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

	// Periodic recovery ticker - check for stale pending messages every 60s
	recoveryTicker := time.NewTicker(60 * time.Second)
	defer recoveryTicker.Stop()

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
			// First pass: separate valid from malformed (validation only, no write yet)
			validBatch := []events.Event{}
			validMsgIDs := []string{}
			poisonBatch := []struct {
				msgID string
				err   error
			}{}

			for i, event := range batch {
				if err := events.ValidateEvent(event); err != nil {
					c.logger.Warn("poison event in batch", "msg_id", messageIDs[i], "event_id", event.EventID, "error", err)
					poisonBatch = append(poisonBatch, struct {
						msgID string
						err   error
					}{messageIDs[i], fmt.Errorf("%w: %v", events.ErrMalformedEvent, err)})
				} else {
					validBatch = append(validBatch, event)
					validMsgIDs = append(validMsgIDs, messageIDs[i])
				}
			}

			// Second pass: write valid events with retry
			if len(validBatch) > 0 {
				var lastErr error
				for attempt := 1; attempt <= maxRetries; attempt++ {
					success := true
					for _, event := range validBatch {
						if err := c.sink.Emit(ctx, event); err != nil {
							c.logger.Error("failed to write valid event", "error", err, "event_id", event.EventID, "attempt", attempt)
							lastErr = err
							success = false
							break
						}
					}

					if success {
						if err := c.ackMessages(ctx, validMsgIDs); err != nil {
							c.logger.Error("failed to ack valid messages", "error", err)
							return
						}
						c.processedCount += uint64(len(validBatch))
						c.logger.Info("batch written", "count", len(validBatch))
						consecutiveFailures = 0
						backoff = 1 * time.Second
						break
					}

					if attempt < maxRetries {
						sleep := backoff * time.Duration(attempt)
						c.logger.Warn("retrying after backoff", "attempt", attempt, "backoff_ms", sleep.Milliseconds())
						time.Sleep(sleep)
					}
				}

				if lastErr != nil {
					c.logger.Error("batch write failed after retries (transient)", "error", lastErr, "count", len(validBatch))
					c.failedCount += uint64(len(validBatch))
					consecutiveFailures++
					batch = batch[:0]
					messageIDs = messageIDs[:0]

					if consecutiveFailures >= 3 {
						pauseDuration := min(backoff*time.Duration(consecutiveFailures), 30*time.Second)
						c.logger.Warn("ClickHouse unavailable, pausing ingestion", "pause_ms", pauseDuration.Milliseconds())
						time.Sleep(pauseDuration)
					}
					return
				}
			}

			// Third pass: move poison to DLQ
			for _, poison := range poisonBatch {
				if err := c.moveToDLQ(ctx, poison.msgID, fmt.Sprintf("validation_failed: %v", poison.err)); err != nil {
					c.logger.Error("failed to move poison to DLQ", "error", err, "msg_id", poison.msgID)
					continue
				}
				if err := c.ackMessages(ctx, []string{poison.msgID}); err != nil {
					c.logger.Error("failed to ACK poison event", "error", err, "msg_id", poison.msgID)
				} else {
					c.dlqCount++
				}
			}

			if len(poisonBatch) > 0 {
				c.logger.Info("poison events to DLQ", "count", len(poisonBatch))
			}

			batch = batch[:0]
			messageIDs = messageIDs[:0]
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

		case <-recoveryTicker.C:
			// Periodic recovery of stale pending messages
			if err := c.RecoverPending(ctx); err != nil {
				c.logger.Error("periodic recovery failed", "error", err)
			}

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

// GetLag returns the consumer group lag and pending count.
func (c *EventConsumer) GetLag(ctx context.Context) (lag int64, pending int64, err error) {
	// XINFO GROUPS <stream>
	cmd := c.redis.B().XinfoGroups().Key(c.streamKey).Build()
	groups, err := c.redis.Do(ctx, cmd).ToArray()
	if err != nil {
		return 0, 0, fmt.Errorf("XINFO GROUPS failed: %w", err)
	}

	// Parse group info (returns array of maps)
	for _, groupData := range groups {
		groupMap, err := groupData.AsMap()
		if err != nil {
			continue
		}

		nameMsg, ok := groupMap["name"]
		if !ok {
			continue
		}
		name, _ := nameMsg.ToString()
		if name != c.consumerGroup {
			continue
		}

		// Extract lag and pending
		if lagMsg, ok := groupMap["lag"]; ok {
			lagVal, _ := lagMsg.AsInt64()
			lag = lagVal
		}
		if pendingMsg, ok := groupMap["pending"]; ok {
			pendingVal, _ := pendingMsg.AsInt64()
			pending = pendingVal
		}
		return lag, pending, nil
	}

	return 0, 0, fmt.Errorf("consumer group %s not found", c.consumerGroup)
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
