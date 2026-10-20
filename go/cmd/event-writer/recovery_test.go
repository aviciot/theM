package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aviciot/them/internal/events"
	"github.com/redis/rueidis"
)

// Test 1: A dead consumer's pending event is persisted and ACKed by its replacement
func TestRecoverPending_DeadConsumerReplacement(t *testing.T) {
	// Setup: Real Redis instance (integration test)
	redisAddr := os.Getenv("REDIS_HOST")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{redisAddr},
		SelectDB:    1, // Use DB 1 for tests
	})
	if err != nil {
		t.Fatalf("failed to connect to redis: %v", err)
	}
	defer client.Close()

	streamKey := "test:recovery:stream1"
	groupName := "test-group"
	dlqKey := "test:recovery:dlq1"

	// Cleanup before test
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())

	// Step 1: Create consumer group
	client.Do(ctx, client.B().XgroupCreate().Key(streamKey).Group(groupName).Id("0").Mkstream().Build())

	// Step 2: Add an event to stream
	testEvent := events.Event{
		EventID:   "test-event-001",
		Kind:      events.EventKindGatewayRequest,
		Timestamp: time.Now(),
	}
	eventJSON, _ := json.Marshal(testEvent)
	addCmd := client.B().Xadd().Key(streamKey).Id("*").FieldValue().FieldValue("event", string(eventJSON)).Build()
	_, err = client.Do(ctx, addCmd).ToString()
	if err != nil {
		t.Fatalf("failed to add event: %v", err)
	}

	// Step 3: Consumer1 reads but crashes before ACK (simulate by claiming without ACK)
	readCmd := client.B().Xreadgroup().Group(groupName, "consumer1").Count(1).Block(100).Streams().Key(streamKey).Id(">").Build()
	_, err = client.Do(ctx, readCmd).AsXRead()
	if err != nil && !rueidis.IsRedisNil(err) {
		t.Fatalf("failed to read: %v", err)
	}

	// Step 4: Verify message is pending
	pendingCmd := client.B().Xpending().Key(streamKey).Group(groupName).Start("-").End("+").Count(10).Build()
	pending, err := client.Do(ctx, pendingCmd).ToArray()
	if err != nil {
		t.Fatalf("XPENDING failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending message, got %d", len(pending))
	}

	// Step 5: Consumer2 starts and runs RecoverPending after idle threshold
	// Wait for message to become idle (>30s threshold)
	t.Log("Waiting 31 seconds for message to become idle...")
	time.Sleep(31 * time.Second)

	// Create mock sink that records emitted events
	mockSink := &mockEventSink{events: make([]events.Event, 0)}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	consumer2 := &EventConsumer{
		redis:         client,
		streamKey:     streamKey,
		dlqStreamKey:  dlqKey,
		consumerGroup: groupName,
		consumerName:  "consumer2",
		sink:          mockSink,
		logger:        logger,
		batchSize:     10,
		batchTimeout:  time.Second,
	}

	// Step 6: Run RecoverPending (this should claim and process the message)
	err = consumer2.RecoverPending(ctx)
	if err != nil {
		t.Fatalf("RecoverPending failed: %v", err)
	}

	// Step 7: Verify event was persisted to sink
	if len(mockSink.events) != 1 {
		t.Fatalf("expected 1 event in sink, got %d", len(mockSink.events))
	}
	if mockSink.events[0].EventID != "test-event-001" {
		t.Errorf("expected event_id test-event-001, got %s", mockSink.events[0].EventID)
	}

	// Step 8: Verify message was ACKed (pending count should be 0)
	pending, err = client.Do(ctx, pendingCmd).ToArray()
	if err != nil {
		t.Fatalf("XPENDING check failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending messages after recovery, got %d", len(pending))
	}

	// Cleanup
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())
}

// Test 2: Restart before the idle threshold still eventually recovers it
func TestRecoverPending_BeforeIdleThreshold(t *testing.T) {
	redisAddr := os.Getenv("REDIS_HOST")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{redisAddr},
		SelectDB:    1,
	})
	if err != nil {
		t.Fatalf("failed to connect to redis: %v", err)
	}
	defer client.Close()

	streamKey := "test:recovery:stream2"
	groupName := "test-group2"
	dlqKey := "test:recovery:dlq2"

	// Cleanup
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())

	// Create group and add event
	client.Do(ctx, client.B().XgroupCreate().Key(streamKey).Group(groupName).Id("0").Mkstream().Build())
	testEvent := events.Event{
		EventID:   "test-event-002",
		Kind:      events.EventKindGatewayRequest,
		Timestamp: time.Now(),
	}
	eventJSON, _ := json.Marshal(testEvent)
	client.Do(ctx, client.B().Xadd().Key(streamKey).Id("*").FieldValue().FieldValue("event", string(eventJSON)).Build())

	// Consumer reads but crashes
	readCmd := client.B().Xreadgroup().Group(groupName, "consumer1").Count(1).Block(100).Streams().Key(streamKey).Id(">").Build()
	client.Do(ctx, readCmd).AsXRead()

	// Immediately restart new consumer (before idle threshold)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	mockSink := &mockEventSink{events: make([]events.Event, 0)}
	consumer := &EventConsumer{
		redis:         client,
		streamKey:     streamKey,
		dlqStreamKey:  dlqKey,
		consumerGroup: groupName,
		consumerName:  "consumer2",
		sink:          mockSink,
		logger:        logger,
		batchSize:     10,
		batchTimeout:  time.Second,
	}

	// First recovery attempt (message not idle yet if threshold > 0s)
	err = consumer.RecoverPending(ctx)
	if err != nil {
		t.Fatalf("RecoverPending failed: %v", err)
	}

	// Message may not be claimed yet if idle < threshold
	// But with periodic recovery (60s interval), it will eventually be claimed

	// For test: wait for message to become idle (>30s), then retry
	t.Log("Waiting 31 seconds for message to become idle...")
	time.Sleep(31 * time.Second)
	err = consumer.RecoverPending(ctx)
	if err != nil {
		t.Fatalf("Second RecoverPending failed: %v", err)
	}

	// Now message should be recovered
	if len(mockSink.events) == 0 {
		t.Errorf("expected event to be recovered after idle threshold, got 0 events")
	}

	// Cleanup
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())
}

// Test 3: ClickHouse outage followed by recovery delivers all valid events
func TestRecoverPending_ClickHouseOutageRecovery(t *testing.T) {
	redisAddr := os.Getenv("REDIS_HOST")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{redisAddr},
		SelectDB:    1,
	})
	if err != nil {
		t.Fatalf("failed to connect to redis: %v", err)
	}
	defer client.Close()

	streamKey := "test:recovery:stream3"
	groupName := "test-group3"
	dlqKey := "test:recovery:dlq3"

	// Cleanup
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())

	// Create group and add event
	client.Do(ctx, client.B().XgroupCreate().Key(streamKey).Group(groupName).Id("0").Mkstream().Build())
	testEvent := events.Event{
		EventID:   "test-event-003",
		Kind:      events.EventKindGatewayRequest,
		Timestamp: time.Now(),
	}
	eventJSON, _ := json.Marshal(testEvent)
	client.Do(ctx, client.B().Xadd().Key(streamKey).Id("*").FieldValue().FieldValue("event", string(eventJSON)).Build())

	// Consumer reads event
	readCmd := client.B().Xreadgroup().Group(groupName, "consumer1").Count(1).Block(100).Streams().Key(streamKey).Id(">").Build()
	client.Do(ctx, readCmd).AsXRead()

	// Simulate ClickHouse outage: Use a failing sink
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	failingSink := &mockEventSink{failCount: 3} // Fail first 3 attempts
	consumer := &EventConsumer{
		redis:         client,
		streamKey:     streamKey,
		dlqStreamKey:  dlqKey,
		consumerGroup: groupName,
		consumerName:  "consumer2",
		sink:          failingSink,
		logger:        logger,
		batchSize:     10,
		batchTimeout:  time.Second,
	}

	// Wait for message to become idle (>30s threshold)
	t.Log("Waiting 31 seconds for message to become idle...")
	time.Sleep(31 * time.Second)

	// Run recovery - should fail initially
	err = consumer.RecoverPending(ctx)
	if err != nil {
		t.Fatalf("RecoverPending failed: %v", err)
	}

	// Message should NOT be ACKed (sink failed)
	pendingCmd := client.B().Xpending().Key(streamKey).Group(groupName).Start("-").End("+").Count(10).Build()
	pending, err := client.Do(ctx, pendingCmd).ToArray()
	if err != nil {
		t.Fatalf("XPENDING failed: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending message after failed sink, got %d", len(pending))
	}

	// Simulate ClickHouse recovery: Use working sink
	workingSink := &mockEventSink{events: make([]events.Event, 0)}
	consumer.sink = workingSink

	// Run recovery again - should succeed
	err = consumer.RecoverPending(ctx)
	if err != nil {
		t.Fatalf("RecoverPending after recovery failed: %v", err)
	}

	// Event should now be persisted and ACKed
	if len(workingSink.events) != 1 {
		t.Fatalf("expected 1 event after recovery, got %d", len(workingSink.events))
	}

	pending, err = client.Do(ctx, pendingCmd).ToArray()
	if err != nil {
		t.Fatalf("XPENDING check failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending messages after successful recovery, got %d", len(pending))
	}

	// Cleanup
	client.Do(ctx, client.B().Del().Key(streamKey).Build())
	client.Do(ctx, client.B().Del().Key(dlqKey).Build())
}

// Mock event sink for testing
type mockEventSink struct {
	events    []events.Event
	failCount int
	attempts  int
}

func (m *mockEventSink) Emit(ctx context.Context, event events.Event) error {
	m.attempts++
	if m.attempts <= m.failCount {
		return fmt.Errorf("mock sink failure (attempt %d/%d)", m.attempts, m.failCount)
	}
	m.events = append(m.events, event)
	return nil
}

func (m *mockEventSink) Close(ctx context.Context) error {
	return nil
}
