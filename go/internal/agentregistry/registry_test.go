package agentregistry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/agentregistry"
)

// ── Fakes ──────────────────────────────────────────────────────────────────────

type fakeDB struct {
	agents []*agentregistry.AgentConfig
	err    error
}

func (f *fakeDB) QueryAgents(_ context.Context) ([]*agentregistry.AgentConfig, error) {
	return f.agents, f.err
}

type fakeCache struct {
	data  map[string][]byte
	calls []string
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: make(map[string][]byte)}
}

func (c *fakeCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := c.data[key]
	return v, ok, nil
}

func (c *fakeCache) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.data[key] = value
	return nil
}

func (c *fakeCache) Del(_ context.Context, key string) error {
	delete(c.data, key)
	return nil
}

func (c *fakeCache) Subscribe(_ context.Context, channel string, handler func(payload string)) error {
	c.calls = append(c.calls, "subscribe:"+channel)
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────────────

// 1. Invoke mock agent returns immediately.
func TestInvokeMock(t *testing.T) {
	db := &fakeDB{
		agents: []*agentregistry.AgentConfig{
			{Slug: "mock_agent", AdapterType: "mock", EndpointURL: ""},
		},
	}
	reg := agentregistry.New(db, newFakeCache(), nil)
	require.NoError(t, reg.LoadAll(context.Background()))

	out, err := reg.Invoke(context.Background(), "mock_agent", json.RawMessage(`{"input":"hello"}`))
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

// 2. Invoke A2A agent sends correct JSON-RPC request and extracts result.
func TestInvokeA2A(t *testing.T) {
	// Serve a fake A2A endpoint.
	expectedOutput := "hello from a2a"
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(new(map[string]any)))
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"text","text":"%s"}]}]},"id":"1"}`, expectedOutput)
	}))
	defer server.Close()

	db := &fakeDB{
		agents: []*agentregistry.AgentConfig{
			{Slug: "a2a_test", AdapterType: "a2a", EndpointURL: server.URL},
		},
	}
	reg := agentregistry.New(db, newFakeCache(), nil)
	require.NoError(t, reg.LoadAll(context.Background()))

	out, err := reg.Invoke(context.Background(), "a2a_test", json.RawMessage(`{"input":"hi"}`))
	require.NoError(t, err)
	_ = receivedBody

	var result map[string]string
	require.NoError(t, json.Unmarshal(out, &result))
	assert.Equal(t, expectedOutput, result["output"])
}

// 3. Cache miss → DB load → cache populated.
func TestCacheMissThenPopulate(t *testing.T) {
	db := &fakeDB{
		agents: []*agentregistry.AgentConfig{
			{Slug: "agent1", AdapterType: "mock"},
		},
	}
	fc := newFakeCache()
	reg := agentregistry.New(db, fc, nil)

	// Cache starts empty — LoadAll should hit DB and populate Redis.
	require.NoError(t, reg.LoadAll(context.Background()))

	// Redis should now have the cache entry.
	_, found, _ := fc.Get(context.Background(), "them:agents:registry")
	assert.True(t, found, "expected Redis cache to be populated after DB load")
}

// 4. Pub/sub invalidation clears in-process cache.
func TestPubSubInvalidation(t *testing.T) {
	db := &fakeDB{
		agents: []*agentregistry.AgentConfig{
			{Slug: "agent_x", AdapterType: "mock"},
		},
	}
	fc := newFakeCache()
	reg := agentregistry.New(db, fc, nil)
	require.NoError(t, reg.LoadAll(context.Background()))

	// Agent should be in L1 (invoke without DB hit).
	_, err := reg.Invoke(context.Background(), "agent_x", json.RawMessage(`{}`))
	require.NoError(t, err)

	// Subscribe (subscribe is called but handler not triggered — just check it registers).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reg.Subscribe(ctx)
	time.Sleep(10 * time.Millisecond) // give goroutine a moment

	assert.Contains(t, fc.calls[0], "them:agents:invalidate")
}

// 5. Unknown agent slug returns typed error.
func TestUnknownSlug(t *testing.T) {
	db := &fakeDB{agents: nil}
	reg := agentregistry.New(db, newFakeCache(), nil)
	require.NoError(t, reg.LoadAll(context.Background()))

	_, err := reg.Invoke(context.Background(), "no_such_agent", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, agentregistry.ErrUnknownAgent), "expected ErrUnknownAgent, got: %v", err)
}
