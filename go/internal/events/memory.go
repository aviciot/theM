package events

import (
	"context"
	"sync"
)

// MemorySink stores events in memory (for testing only).
// Not safe for production — unbounded growth, lost on restart.
type MemorySink struct {
	mu     sync.RWMutex
	events []Event
}

// NewMemorySink creates an in-memory event sink.
func NewMemorySink() *MemorySink {
	return &MemorySink{
		events: make([]Event, 0, 100),
	}
}

// Emit appends the event to the in-memory slice.
func (m *MemorySink) Emit(ctx context.Context, event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

// Close is a no-op for MemorySink.
func (m *MemorySink) Close(ctx context.Context) error {
	return nil
}

// Events returns a copy of all stored events (for testing).
func (m *MemorySink) Events() []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Clear removes all events (for test cleanup).
func (m *MemorySink) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = m.events[:0]
}

// MemoryStore stores events in memory and implements EventStore (for testing).
type MemoryStore struct {
	mu     sync.RWMutex
	events []Event
}

// NewMemoryStore creates an in-memory event store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		events: make([]Event, 0, 100),
	}
}

// Add appends an event (for test setup).
func (m *MemoryStore) Add(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

// ListGatewayRequests filters events by tenant and other criteria.
func (m *MemoryStore) ListGatewayRequests(ctx context.Context, tenantID string, filter GatewayRequestFilter) ([]GatewayRequestEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []GatewayRequestEvent
	for _, e := range m.events {
		if e.Kind != EventKindGatewayRequest || e.TenantID != tenantID {
			continue
		}
		if e.GatewayRequest == nil {
			continue
		}

		gw := e.GatewayRequest
		// Apply filters
		if filter.ApplicationID != "" && gw.ApplicationID != filter.ApplicationID {
			continue
		}
		if filter.ClientID != "" && gw.ClientID != filter.ClientID {
			continue
		}
		if filter.UserID != "" && gw.UserID != filter.UserID {
			continue
		}
		if filter.Status != "" && gw.Status != filter.Status {
			continue
		}
		if !filter.StartTime.IsZero() && e.Timestamp.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && !e.Timestamp.Before(filter.EndTime) {
			continue
		}

		results = append(results, *gw)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

// GetGatewayRequestByID finds a single gateway request by requestID.
func (m *MemoryStore) GetGatewayRequestByID(ctx context.Context, tenantID string, requestID string) (*GatewayRequestEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, e := range m.events {
		if e.Kind == EventKindGatewayRequest && e.TenantID == tenantID && e.GatewayRequest != nil && e.GatewayRequest.RequestID == requestID {
			gw := *e.GatewayRequest
			return &gw, nil
		}
	}
	return nil, nil
}

// UsageByDay aggregates usage data.
func (m *MemoryStore) UsageByDay(ctx context.Context, tenantID string, filter UsageFilter) ([]UsagePoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Simple implementation — group by day
	dayMap := make(map[string]*UsagePoint)

	for _, e := range m.events {
		if e.Kind != EventKindGatewayRequest || e.TenantID != tenantID {
			continue
		}
		if e.GatewayRequest == nil {
			continue
		}

		gw := e.GatewayRequest
		if filter.ApplicationID != "" && gw.ApplicationID != filter.ApplicationID {
			continue
		}
		if !filter.StartTime.IsZero() && e.Timestamp.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && !e.Timestamp.Before(filter.EndTime) {
			continue
		}

		// Group by day
		day := e.Timestamp.UTC().Format("2006-01-02")
		if dayMap[day] == nil {
			dayMap[day] = &UsagePoint{
				Timestamp:     e.Timestamp.UTC().Truncate(24 * 3600 * 1e9), // truncate to day
				ApplicationID: gw.ApplicationID,
			}
		}

		dayMap[day].Requests++
		dayMap[day].TokensIn += int64(gw.TokensIn)
		dayMap[day].TokensOut += int64(gw.TokensOut)
		dayMap[day].CostUSD += gw.CostUSD
	}

	// Convert map to slice
	var results []UsagePoint
	for _, point := range dayMap {
		results = append(results, *point)
	}
	return results, nil
}

// ToolCallsForRun returns all tool calls for a given run.
func (m *MemoryStore) ToolCallsForRun(ctx context.Context, tenantID string, runID string) ([]ToolCallEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []ToolCallEvent
	for _, e := range m.events {
		if e.Kind == EventKindToolCall && e.TenantID == tenantID && e.ToolCall != nil && e.ToolCall.RunID == runID {
			results = append(results, *e.ToolCall)
		}
	}
	return results, nil
}

// UserActivity returns user activity events.
func (m *MemoryStore) UserActivity(ctx context.Context, tenantID string, filter ActivityFilter) ([]UserActivityEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []UserActivityEvent
	for _, e := range m.events {
		if e.Kind != EventKindUserActivity || e.TenantID != tenantID {
			continue
		}
		if e.UserActivity == nil {
			continue
		}

		act := e.UserActivity
		if filter.UserID != "" && act.UserID != filter.UserID {
			continue
		}
		if filter.Action != "" && act.Action != filter.Action {
			continue
		}
		if filter.ResourceType != "" && act.ResourceType != filter.ResourceType {
			continue
		}
		if !filter.StartTime.IsZero() && e.Timestamp.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && !e.Timestamp.Before(filter.EndTime) {
			continue
		}

		results = append(results, *act)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

// Ping always succeeds for MemoryStore.
func (m *MemoryStore) Ping(ctx context.Context) error {
	return nil
}
