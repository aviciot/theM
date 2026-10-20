package events

import (
	"context"
	"testing"
)

// TestMemoryBackend runs the full contract suite against MemoryStore.
// MemoryStore is unique: the sink and store are the same instance, so events
// are immediately queryable (no async flush needed).
func TestMemoryBackend(t *testing.T) {
	store := NewMemoryStore()

	setup := func() (EventSink, func()) {
		// Use MemoryStore as BOTH sink and store (it implements EventSink via the wrapper below)
		sink := &memoryStoreAsSink{store: store}
		cleanup := func() {
			// No-op: events are already in the store
		}
		return sink, cleanup
	}

	ContractTests(t, store, setup)
}

// memoryStoreAsSink wraps MemoryStore to implement EventSink.
type memoryStoreAsSink struct {
	store *MemoryStore
}

func (m *memoryStoreAsSink) Emit(ctx context.Context, event Event) error {
	m.store.Add(event)
	return nil
}

func (m *memoryStoreAsSink) Close(ctx context.Context) error {
	return nil
}
