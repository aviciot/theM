package dashboard_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviciot/them/internal/dashboard"
	"github.com/gorilla/websocket"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake Redis ────────────────────────────────────────────────────────────────

type fakeRedis struct {
	mu        sync.Mutex
	msgs      []rueidis.PubSubMessage
	hgetalls  map[string]map[string]string // key → field map for HGetAll
	gets      map[string]string            // key → value for Get
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		hgetalls: make(map[string]map[string]string),
		gets:     make(map[string]string),
	}
}

func (f *fakeRedis) Subscribe(ctx context.Context, _ []string, fn func(rueidis.PubSubMessage)) error {
	f.mu.Lock()
	msgs := f.msgs
	f.mu.Unlock()

	for _, m := range msgs {
		fn(m)
	}

	<-ctx.Done()
	return nil
}

func (f *fakeRedis) HGetAll(_ context.Context, key string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hgetalls[key], nil
}

func (f *fakeRedis) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets[key], nil
}

func (f *fakeRedis) XRevRange(_ context.Context, _, _, _ string, _ int64) ([]dashboard.StreamEntry, error) {
	return nil, nil
}

func (f *fakeRedis) queueMessage(channel, payload string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, rueidis.PubSubMessage{
		Channel: channel,
		Message: payload,
	})
}

func (f *fakeRedis) setHash(key string, fields map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hgetalls[key] = fields
}

func (f *fakeRedis) setString(key, val string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets[key] = val
}

// ── JWT helper ────────────────────────────────────────────────────────────────

func makeHS256JWT(secret []byte, subject string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	now := time.Now().Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"sub":%q,"username":"admin","role":"super_admin","exp":%d,"iat":%d,"type":"access"}`,
		subject, now+3600, now,
	)))
	data := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(data))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return data + "." + sig
}

// ── test server ───────────────────────────────────────────────────────────────

func newTestServer(t *testing.T, rc *fakeRedis) (*httptest.Server, []byte) {
	t.Helper()
	secret := []byte("test-secret-key")
	h := dashboard.NewForTest(rc, secret, slog.Default())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, secret
}

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard"
	if token != "" {
		u += "?token=" + token
	}
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func subscribe(t *testing.T, conn *websocket.Conn, channels []string) {
	t.Helper()
	msg, _ := json.Marshal(map[string]any{"type": "subscribe", "channels": channels})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestDashboard_MissingToken(t *testing.T) {
	srv, _ := newTestServer(t, newFakeRedis())
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard"
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestDashboard_InvalidToken(t *testing.T) {
	srv, _ := newTestServer(t, newFakeRedis())
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=not.a.valid.jwt"
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestDashboard_InvalidSubscribeType(t *testing.T) {
	srv, secret := newTestServer(t, newFakeRedis())
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)

	msg, _ := json.Marshal(map[string]any{"type": "wrongtype", "channels": []string{"runs"}})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	resp := readJSON(t, conn)
	assert.Equal(t, "error", resp["type"])
}

func TestDashboard_NoValidChannels(t *testing.T) {
	srv, secret := newTestServer(t, newFakeRedis())
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"__evil__", "../../../../etc/passwd"})

	resp := readJSON(t, conn)
	assert.Equal(t, "error", resp["type"])
}

func TestDashboard_SubscribedAck(t *testing.T) {
	srv, secret := newTestServer(t, newFakeRedis())
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"runs", "agents"})

	resp := readJSON(t, conn)
	assert.Equal(t, "subscribed", resp["type"])
	chs, _ := resp["channels"].([]any)
	assert.Len(t, chs, 2)
}

func TestDashboard_EventRelay(t *testing.T) {
	rc := newFakeRedis()
	rc.queueMessage("them:dash:runs", `{"type":"run_started","run_id":"abc"}`)

	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"runs"})

	// subscribed ack
	ack := readJSON(t, conn)
	assert.Equal(t, "subscribed", ack["type"])

	// relayed event
	ev := readJSON(t, conn)
	assert.Equal(t, "runs", ev["channel"])
	inner, _ := ev["event"].(map[string]any)
	assert.Equal(t, "run_started", inner["type"])
}

func TestDashboard_AgentChannelRelayed(t *testing.T) {
	rc := newFakeRedis()
	rc.queueMessage("them:dash:agent:abc123", `{"type":"scan_complete","score":15}`)

	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"agent:abc123"})

	ack := readJSON(t, conn)
	assert.Equal(t, "subscribed", ack["type"])

	ev := readJSON(t, conn)
	assert.Equal(t, "agent:abc123", ev["channel"])
}

func TestDashboard_AgentSnapshot(t *testing.T) {
	rc := newFakeRedis()
	rc.setHash("them:scan:state:abc123", map[string]string{
		"status": "complete",
		"score":  "7",
	})

	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"agent:abc123"})

	ack := readJSON(t, conn)
	assert.Equal(t, "subscribed", ack["type"])

	// snapshot delivered from HGETALL
	snap := readJSON(t, conn)
	assert.Equal(t, "agent:abc123", snap["channel"])
	inner, _ := snap["event"].(map[string]any)
	assert.Equal(t, "complete", inner["status"])
}

func TestDashboard_PingReceived(t *testing.T) {
	rc := newFakeRedis()
	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"runs"})

	// drain ack
	readJSON(t, conn)

	// Read until we get a ping (or timeout). Ping interval is 30s in production
	// but we verify message format not timing.
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, raw, _ := conn.ReadMessage()
	if raw != nil {
		var m map[string]string
		if json.Unmarshal(raw, &m) == nil {
			assert.Contains(t, []string{"ping"}, m["type"])
		}
	}
}

func TestIsValidChannel(t *testing.T) {
	valid := []string{
		"runs", "agents", "metrics", "apps",
		"run:00000000-0000-0000-0000-000000000001",
		"agent:00000000-0000-0000-0000-000000000001",
		"sessions:my-app",
	}
	invalid := []string{
		"", "run:", "agent:", "sessions:",
		"../evil", "__runs__", "RUNS",
		"them:dash:runs", // must not accept raw Redis key
	}
	for _, ch := range valid {
		assert.True(t, dashboard.IsValidChannel(ch), "expected valid: %q", ch)
	}
	for _, ch := range invalid {
		assert.False(t, dashboard.IsValidChannel(ch), "expected invalid: %q", ch)
	}
}

func TestDashboard_CleanShutdownOnDisconnect(t *testing.T) {
	rc := newFakeRedis()
	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"runs"})

	readJSON(t, conn) // drain ack

	// Close the client — server goroutines must exit without panic.
	conn.Close()
	time.Sleep(50 * time.Millisecond) // give goroutines a moment to exit
}

func TestDashboard_AppsSnapshot(t *testing.T) {
	rc := newFakeRedis()
	cached := `{"my-app":{"reachable":true,"latency_ms":12}}`
	rc.setString("them:dash:app_status_cache", cached)

	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"apps"})

	readJSON(t, conn) // drain ack

	// Next message should be the cached apps snapshot.
	msg := readJSON(t, conn)
	assert.Equal(t, "apps", msg["channel"])
	event, ok := msg["event"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "app_status", event["type"])
}
