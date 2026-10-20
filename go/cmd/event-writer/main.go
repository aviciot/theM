// Event Writer — Redis Streams → ClickHouse consumer
// Design: docs/OBSERVABILITY_STORAGE_DESIGN.md Stage A3
//
// Reads events from Redis Streams (them:events) and writes to ClickHouse in batches.
// Consumer group: "event-writers" (allows N replicas for redundancy).
// At-least-once delivery: events are ACKed only after ClickHouse confirms write.
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
	streamKey     = "them:events"
	consumerGroup = "event-writers"
	consumerName  = "event-writer" // Instance ID appended at runtime
	batchSize     = 100             // Max events per ClickHouse batch
	batchTimeout  = 5 * time.Second // Max time between batches
	blockTimeout  = 2 * time.Second // XREADGROUP block time
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

	// Start consumer
	consumer := &EventConsumer{
		redis:         redisClient,
		sink:          sink,
		logger:        logger,
		streamKey:     streamKey,
		consumerGroup: consumerGroup,
		consumerName:  fmt.Sprintf("%s-%s", consumerName, os.Getenv("HOSTNAME")),
		batchSize:     batchSize,
		batchTimeout:  batchTimeout,
	}

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
	consumerGroup string
	consumerName  string
	batchSize     int
	batchTimeout  time.Duration
	stopChan      chan struct{}
	stopped       bool
	mu            sync.Mutex
}

// Run starts the consumer loop.
func (c *EventConsumer) Run(ctx context.Context) {
	c.stopChan = make(chan struct{})

	batch := make([]events.Event, 0, c.batchSize)
	messageIDs := make([]string, 0, c.batchSize)

	ticker := time.NewTicker(c.batchTimeout)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}

		// Write batch to ClickHouse
		for _, event := range batch {
			if err := c.sink.Emit(ctx, event); err != nil {
				c.logger.Error("failed to write event", "error", err, "event_id", event.EventID)
				// Don't ACK on error — will be redelivered
				return
			}
		}

		// ACK messages in Redis
		if err := c.ackMessages(ctx, messageIDs); err != nil {
			c.logger.Error("failed to ack messages", "error", err)
		}

		c.logger.Info("batch written", "count", len(batch))

		// Clear batch
		batch = batch[:0]
		messageIDs = messageIDs[:0]
	}

	for {
		select {
		case <-c.stopChan:
			// Flush remaining events
			flush()
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
					c.logger.Warn("message missing event field", "id", msg.ID)
					continue
				}

				var event events.Event
				if err := json.Unmarshal([]byte(eventData), &event); err != nil {
					c.logger.Error("failed to unmarshal event", "error", err, "id", msg.ID)
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

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
