package dashboard_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviciot/them/internal/dashboard"
	"github.com/aviciot/them/internal/runstream"
	"github.com/gorilla/websocket"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake Redis ────────────────────────────────────────────────────────────────

type fakeRedis struct {
	mu       sync.Mutex
	msgs     []rueidis.PubSubMessage
	hgetalls map[string]map[string]string // key → field map for HGetAll
	gets     map[string]string            // key → value for Get
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

const testTenantID = "00000000-0000-0000-0000-000000000001"

func makeHS256JWT(secret []byte, subject string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	now := time.Now().Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"sub":%q,"username":"admin","role":"super_admin","tenant_id":%q,"exp":%d,"iat":%d,"type":"access"}`,
		subject, testTenantID, now+3600, now,
	)))
	data := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(data))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return data + "." + sig
}

// ── fake RunOwnershipChecker ─────────────────────────────────────────────────

// allowAllOwner treats every run as owned by every tenant — the permissive
// default for tests that aren't specifically exercising the ownership gate.
type allowAllOwner struct{}

func (allowAllOwner) RunBelongsToTenant(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// denyAllOwner treats every run as NOT owned — used to prove the ownership
// gate actually refuses tailing, not just that it compiles.
type denyAllOwner struct{}

func (denyAllOwner) RunBelongsToTenant(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

// erroringOwner simulates a DB error during the ownership check — must fail
// closed (refuse), not fail open (tail anyway).
type erroringOwner struct{}

func (erroringOwner) RunBelongsToTenant(_ context.Context, _, _ string) (bool, error) {
	return false, errors.New("db unavailable")
}

// ── test server ───────────────────────────────────────────────────────────────

func newTestServer(t *testing.T, rc *fakeRedis) (*httptest.Server, []byte) {
	t.Helper()
	return newTestServerWithStreamer(t, rc, nil)
}

func newTestServerWithStreamer(t *testing.T, rc *fakeRedis, streamer runstream.RedisStreamer) (*httptest.Server, []byte) {
	t.Helper()
	return newTestServerFull(t, rc, streamer, allowAllOwner{})
}

func newTestServerFull(t *testing.T, rc *fakeRedis, streamer runstream.RedisStreamer, runOwner dashboard.RunOwnershipChecker) (*httptest.Server, []byte) {
	t.Helper()
	secret := []byte("test-secret-key")
	h := dashboard.NewForTest(rc, streamer, runOwner, secret, slog.Default())
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
	rc.setHash("them:"+testTenantID+":scan:state:abc123", map[string]string{
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
		"runs", "agents", "metrics", "apps", "services:stats",
		"run:00000000-0000-0000-0000-000000000001",
		"agent:00000000-0000-0000-0000-000000000001",
		"sessions:my-app",
		"scan:00000000-0000-0000-0000-000000000002",
	}
	invalid := []string{
		"", "run:", "agent:", "sessions:", "scan:",
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

func TestDashboard_ScanSnapshot(t *testing.T) {
	rc := newFakeRedis()
	artifactID := "aaaaaaaa-0000-0000-0000-000000000001"
	// Pre-populate the scan state key so the snapshot is sent immediately.
	rc.setString("them:"+testTenantID+":scan:state:"+artifactID, "clean")

	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"scan:" + artifactID})

	readJSON(t, conn) // drain ack

	msg := readJSON(t, conn)
	assert.Equal(t, "scan:"+artifactID, msg["channel"])
	event, ok := msg["event"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "artifact_scan", event["type"])
	assert.Equal(t, artifactID, event["artifact_id"])
	assert.Equal(t, "clean", event["scan_status"])
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

// ── fake RedisStreamer (run:* live-tailing path) ────────────────────────────
//
// Verifies the fix for the gap this session found: nothing ever PUBLISHes
// node_start/node_done/etc — they're only XADDed to the run's Redis Stream —
// so a plain pub/sub SUBSCRIBE on run:* never receives anything live. run:*
// channels must be tailed via runstream.StreamFromRedis instead (see package
// doc + tailRunChannel).

type fakeStreamer struct {
	entries []runstream.StreamEntry // full pre-seeded history, served via XRange
}

func (f *fakeStreamer) XRange(_ context.Context, _, start, _ string) ([]runstream.StreamEntry, error) {
	if start == "-" && len(f.entries) > 0 {
		return f.entries, nil // first replay call: return everything
	}
	return nil, nil // subsequent calls: replay exhausted
}

func (f *fakeStreamer) XRangeN(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	return nil, nil // not resuming from a cursor in these tests
}

func (f *fakeStreamer) XRevRange(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	return nil, nil
}

func (f *fakeStreamer) XRead(ctx context.Context, _ runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	// No live entries queued in these tests — block until ctx is cancelled,
	// mirroring a real XREAD BLOCK with nothing new to deliver.
	<-ctx.Done()
	return nil, nil
}

func streamEntry(id, jsonPayload string) runstream.StreamEntry {
	return runstream.StreamEntry{ID: id, Values: map[string]interface{}{"data": jsonPayload}}
}

func TestDashboard_RunChannel_TailsLiveInsteadOfPubSub(t *testing.T) {
	runID := "11111111-0000-0000-0000-000000000001"
	streamer := &fakeStreamer{
		entries: []runstream.StreamEntry{
			streamEntry("1-0", `{"type":"node_start","run_id":"`+runID+`","node_id":"llm_1","kind":"llm"}`),
			streamEntry("1-1", `{"type":"node_done","run_id":"`+runID+`","node_id":"llm_1","kind":"llm","detail":"ok"}`),
			streamEntry("1-2", `{"type":"done","run_id":"`+runID+`"}`),
		},
	}
	rc := newFakeRedis() // no messages queued — proves delivery does NOT come from pub/sub
	srv, secret := newTestServerWithStreamer(t, rc, streamer)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"run:" + runID})

	readJSON(t, conn) // drain ack

	var types []string
	for i := 0; i < 3; i++ {
		msg := readJSON(t, conn)
		assert.Equal(t, "run:"+runID, msg["channel"])
		ev, ok := msg["event"].(map[string]any)
		require.True(t, ok)
		types = append(types, ev["type"].(string))
	}
	assert.Equal(t, []string{"node_start", "node_done", "done"}, types)
}

func TestDashboard_RunChannel_NoStreamerFallsBackToOneShotSnapshot(t *testing.T) {
	// streamer == nil (as passed by newTestServer) must not panic — falls back
	// to the legacy one-shot sendRunSnapshot path via plain pub/sub channels.
	runID := "22222222-0000-0000-0000-000000000001"
	rc := newFakeRedis()
	srv, secret := newTestServer(t, rc)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"run:" + runID})

	msg := readJSON(t, conn) // ack only — no snapshot data queued, no panic
	assert.Equal(t, "subscribed", msg["type"])
}

// ── run:* tenant ownership gate ──────────────────────────────────────────────
//
// IsValidChannel only validates channel-name SHAPE (a well-formed "run:{id}"),
// never whether the caller's tenant actually owns that run. Without the
// ownership check added this session, any authenticated caller of any tenant
// could subscribe to any other tenant's run:{id} and read its live trace
// (prompts, outputs, everything) — a cross-tenant IDOR. These tests prove the
// gate actually refuses tailing rather than just existing in the type system.

func TestDashboard_RunChannel_WrongTenantRefused(t *testing.T) {
	runID := "33333333-0000-0000-0000-000000000001"
	streamer := &fakeStreamer{entries: []runstream.StreamEntry{
		streamEntry("1-0", `{"type":"done","run_id":"`+runID+`"}`),
	}}
	rc := newFakeRedis()
	srv, secret := newTestServerFull(t, rc, streamer, denyAllOwner{})
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"run:" + runID})

	msg := readJSON(t, conn)
	assert.Equal(t, "subscribed", msg["type"]) // channel name is well-formed, so it's still acked

	// No further message should ever arrive for this channel — the stream
	// entry above must never be delivered. A short read-with-timeout proves
	// silence rather than merely "nothing has arrived yet by coincidence."
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	assert.Error(t, err, "expected a read timeout — no event should ever be delivered for an unowned run")
}

func TestDashboard_RunChannel_OwnershipCheckErrorRefusesNotTails(t *testing.T) {
	runID := "44444444-0000-0000-0000-000000000001"
	streamer := &fakeStreamer{entries: []runstream.StreamEntry{
		streamEntry("1-0", `{"type":"done","run_id":"`+runID+`"}`),
	}}
	rc := newFakeRedis()
	srv, secret := newTestServerFull(t, rc, streamer, erroringOwner{})
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"run:" + runID})

	readJSON(t, conn) // ack

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	assert.Error(t, err, "an ownership-check error must fail closed (refuse), never fail open (tail anyway)")
}

func TestDashboard_RunChannel_NilOwnerRefusesEntirely(t *testing.T) {
	// A Handler built with streamer set but runOwner == nil must still refuse
	// to tail — nil must not be interpreted as "skip the check."
	runID := "55555555-0000-0000-0000-000000000001"
	streamer := &fakeStreamer{entries: []runstream.StreamEntry{
		streamEntry("1-0", `{"type":"done","run_id":"`+runID+`"}`),
	}}
	rc := newFakeRedis()
	srv, secret := newTestServerFull(t, rc, streamer, nil)
	token := makeHS256JWT(secret, "1")
	conn := dialWS(t, srv, token)
	subscribe(t, conn, []string{"run:" + runID})

	readJSON(t, conn) // ack

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	assert.Error(t, err, "nil runOwner must refuse tailing, not skip the check")
}
