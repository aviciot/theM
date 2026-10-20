package events

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ContractTests runs the same test suite against any EventStore implementation.
// This ensures all backends (postgres, clickhouse, memory) behave identically.
func ContractTests(t *testing.T, store EventStore, setup func() (EventSink, func())) {
	t.Run("ListGatewayRequests", func(t *testing.T) {
		testListGatewayRequests(t, store, setup)
	})
	t.Run("GetGatewayRequestByID", func(t *testing.T) {
		testGetGatewayRequestByID(t, store, setup)
	})
	t.Run("UsageByDay", func(t *testing.T) {
		testUsageByDay(t, store, setup)
	})
	t.Run("TenantIsolation", func(t *testing.T) {
		testTenantIsolation(t, store, setup)
	})
	t.Run("Ping", func(t *testing.T) {
		testPing(t, store)
	})
}

func testListGatewayRequests(t *testing.T, store EventStore, setup func() (EventSink, func())) {
	sink, cleanup := setup()
	defer cleanup()

	ctx := context.Background()
	tenantID := uuid.New().String()
	appID := uuid.New().String()

	// Emit 3 events
	events := []Event{
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    time.Now().Add(-2 * time.Hour),
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				Provider:      "anthropic",
				ModelRequested: "claude-3-5-sonnet-20241022",
				ModelServed:   "claude-3-5-sonnet-20241022",
				Status:        "ok",
				HTTPStatus:    200,
				TokensIn:      100,
				TokensOut:     200,
				CostUSD:       0.0015,
			},
		},
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    time.Now().Add(-1 * time.Hour),
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				Provider:      "anthropic",
				ModelRequested: "claude-3-5-sonnet-20241022",
				ModelServed:   "claude-3-5-sonnet-20241022",
				Status:        "error",
				HTTPStatus:    500,
				ErrorCode:     "rate_limit_exceeded",
				TokensIn:      50,
				TokensOut:     0,
				CostUSD:       0,
			},
		},
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    time.Now(),
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				Provider:      "openai",
				ModelRequested: "gpt-4",
				ModelServed:   "gpt-4",
				Status:        "ok",
				HTTPStatus:    200,
				TokensIn:      150,
				TokensOut:     300,
				CostUSD:       0.0045,
			},
		},
	}

	for _, e := range events {
		if err := sink.Emit(ctx, e); err != nil {
			t.Fatalf("emit event: %v", err)
		}
	}

	// Test: list all
	results, err := store.ListGatewayRequests(ctx, tenantID, GatewayRequestFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list gateway requests: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// Test: filter by status
	results, err = store.ListGatewayRequests(ctx, tenantID, GatewayRequestFilter{Status: "ok", Limit: 100})
	if err != nil {
		t.Fatalf("list with status filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 'ok' results, got %d", len(results))
	}

	// Test: filter by application
	results, err = store.ListGatewayRequests(ctx, tenantID, GatewayRequestFilter{ApplicationID: appID, Limit: 100})
	if err != nil {
		t.Fatalf("list with app filter: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results for app, got %d", len(results))
	}

	// Test: limit
	results, err = store.ListGatewayRequests(ctx, tenantID, GatewayRequestFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list with limit: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected max 2 results with limit=2, got %d", len(results))
	}
}

func testGetGatewayRequestByID(t *testing.T, store EventStore, setup func() (EventSink, func())) {
	sink, cleanup := setup()
	defer cleanup()

	ctx := context.Background()
	tenantID := uuid.New().String()
	requestID := uuid.New().String()

	event := Event{
		EventID:      uuid.New().String(),
		SchemaVersion: 1,
		Timestamp:    time.Now(),
		TenantID:     tenantID,
		Kind:         EventKindGatewayRequest,
		GatewayRequest: &GatewayRequestEvent{
			RequestID:     requestID,
			Provider:      "anthropic",
			ModelRequested: "claude-3-5-sonnet-20241022",
			ModelServed:   "claude-3-5-sonnet-20241022",
			Status:        "ok",
			HTTPStatus:    200,
			TokensIn:      100,
			TokensOut:     200,
			CostUSD:       0.0015,
		},
	}

	if err := sink.Emit(ctx, event); err != nil {
		t.Fatalf("emit event: %v", err)
	}

	// Test: get by ID
	result, err := store.GetGatewayRequestByID(ctx, tenantID, requestID)
	if err != nil {
		t.Fatalf("get gateway request by ID: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.RequestID != requestID {
		t.Errorf("expected request ID %s, got %s", requestID, result.RequestID)
	}

	// Test: not found
	result, err = store.GetGatewayRequestByID(ctx, tenantID, uuid.New().String())
	if err != nil {
		t.Fatalf("get non-existent request: %v", err)
	}
	if result != nil {
		t.Error("expected nil for non-existent request")
	}
}

func testUsageByDay(t *testing.T, store EventStore, setup func() (EventSink, func())) {
	sink, cleanup := setup()
	defer cleanup()

	ctx := context.Background()
	tenantID := uuid.New().String()
	appID := uuid.New().String()

	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)

	// Emit events across 2 days
	events := []Event{
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    yesterday,
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				TokensIn:      100,
				TokensOut:     200,
				CostUSD:       0.001,
			},
		},
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    yesterday,
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				TokensIn:      150,
				TokensOut:     250,
				CostUSD:       0.002,
			},
		},
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    now,
			TenantID:     tenantID,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				ApplicationID: appID,
				RequestID:     uuid.New().String(),
				TokensIn:      200,
				TokensOut:     300,
				CostUSD:       0.003,
			},
		},
	}

	for _, e := range events {
		if err := sink.Emit(ctx, e); err != nil {
			t.Fatalf("emit event: %v", err)
		}
	}

	// Test: aggregate by day
	results, err := store.UsageByDay(ctx, tenantID, UsageFilter{
		ApplicationID: appID,
		GroupBy:       UsageGroupByDay,
	})
	if err != nil {
		t.Fatalf("usage by day: %v", err)
	}

	if len(results) < 1 {
		t.Error("expected at least 1 day of usage")
	}

	// Verify totals
	var totalRequests int64
	var totalTokensIn int64
	var totalTokensOut int64
	var totalCost float64
	for _, r := range results {
		totalRequests += r.Requests
		totalTokensIn += r.TokensIn
		totalTokensOut += r.TokensOut
		totalCost += r.CostUSD
	}

	if totalRequests != 3 {
		t.Errorf("expected 3 total requests, got %d", totalRequests)
	}
	if totalTokensIn != 450 {
		t.Errorf("expected 450 total tokens in, got %d", totalTokensIn)
	}
	if totalTokensOut != 750 {
		t.Errorf("expected 750 total tokens out, got %d", totalTokensOut)
	}
	if totalCost < 0.005 || totalCost > 0.007 {
		t.Errorf("expected total cost ~0.006, got %f", totalCost)
	}
}

func testTenantIsolation(t *testing.T, store EventStore, setup func() (EventSink, func())) {
	sink, cleanup := setup()
	defer cleanup()

	ctx := context.Background()
	tenant1 := uuid.New().String()
	tenant2 := uuid.New().String()

	// Emit events for two tenants
	events := []Event{
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    time.Now(),
			TenantID:     tenant1,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				RequestID: uuid.New().String(),
				Status:    "ok",
			},
		},
		{
			EventID:      uuid.New().String(),
			SchemaVersion: 1,
			Timestamp:    time.Now(),
			TenantID:     tenant2,
			Kind:         EventKindGatewayRequest,
			GatewayRequest: &GatewayRequestEvent{
				RequestID: uuid.New().String(),
				Status:    "ok",
			},
		},
	}

	for _, e := range events {
		if err := sink.Emit(ctx, e); err != nil {
			t.Fatalf("emit event: %v", err)
		}
	}

	// Test: tenant1 sees only their events
	results, err := store.ListGatewayRequests(ctx, tenant1, GatewayRequestFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list for tenant1: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("tenant1 expected 1 result, got %d", len(results))
	}

	// Test: tenant2 sees only their events
	results, err = store.ListGatewayRequests(ctx, tenant2, GatewayRequestFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list for tenant2: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("tenant2 expected 1 result, got %d", len(results))
	}
}

func testPing(t *testing.T, store EventStore) {
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Errorf("ping failed: %v", err)
	}
}
