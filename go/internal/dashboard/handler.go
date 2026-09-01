// Package dashboard implements the /ws/dashboard WebSocket endpoint.
//
// It is a multiplexed Redis pub/sub relay: the client sends one subscribe
// message naming one or more logical channels, the server maps them to
// them:dash:{channel} Redis pub/sub keys and relays events as they arrive.
//
// Protocol:
//
//	Client → Server (first message):
//	  {"type":"subscribe","channels":["runs","agent:abc123","sessions:app1"]}
//
//	Server → Client (handshake ack):
//	  {"type":"subscribed","channels":["runs","agent:abc123","sessions:app1"]}
//
//	Server → Client (snapshot + live events):
//	  {"channel":"agent:abc123","event":{"type":"scan_complete",...}}
//
//	Server → Client (keepalive):
//	  {"type":"ping"}
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/gorilla/websocket"
	"github.com/redis/rueidis"
)

const (
	pingInterval        = 30 * time.Second
	subscribeDeadline   = 10 * time.Second
	dashPrefix          = "them:dash:"
	scanStatePrefix     = "them:scan:state:"
	sessionStatePrefix  = "them:dash:sessions:state:"
	appStatusCacheKey   = "them:dash:app_status_cache"
)

// dashRedis is the minimal Redis surface needed by the dashboard handler.
type dashRedis interface {
	// Subscribe blocks and calls fn for every pub/sub message on the given
	// channels until ctx is cancelled.
	Subscribe(ctx context.Context, channels []string, fn func(rueidis.PubSubMessage)) error
	// HGetAll returns all fields of the hash at key, or nil if not found.
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// Get returns the string value at key, or "" if not found.
	Get(ctx context.Context, key string) (string, error)
	// XRevRange returns at most count stream entries newest-first.
	// end="+" start="-" scans the full stream newest-first.
	XRevRange(ctx context.Context, key, end, start string, count int64) ([]StreamEntry, error)
}

// StreamEntry is a minimal Redis stream entry (ID + data field).
type StreamEntry struct {
	ID   string
	Data string
}

// Handler is the /ws/dashboard WebSocket handler.
type Handler struct {
	redis     dashRedis
	jwtSecret []byte
	logger    *slog.Logger
	upgrader  websocket.Upgrader
}

// rueidisAdapter wraps a rueidis.Client to satisfy dashRedis.
type rueidisAdapter struct{ client rueidis.Client }

func (a *rueidisAdapter) Subscribe(ctx context.Context, channels []string, fn func(rueidis.PubSubMessage)) error {
	cmd := a.client.B().Subscribe().Channel(channels...).Build()
	err := a.client.Receive(ctx, cmd, fn)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (a *rueidisAdapter) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	res := a.client.Do(ctx, a.client.B().Hgetall().Key(key).Build())
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	return res.AsStrMap()
}

func (a *rueidisAdapter) Get(ctx context.Context, key string) (string, error) {
	res := a.client.Do(ctx, a.client.B().Get().Key(key).Build())
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return "", nil
		}
		return "", err
	}
	return res.ToString()
}

func (a *rueidisAdapter) XRevRange(ctx context.Context, key, end, start string, count int64) ([]StreamEntry, error) {
	cmd := a.client.B().Xrevrange().Key(key).End(end).Start(start).Count(count).Build()
	entries, err := a.client.Do(ctx, cmd).AsXRange()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]StreamEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, StreamEntry{ID: e.ID, Data: e.FieldValues["data"]})
	}
	return out, nil
}

// New creates a Handler wrapping a rueidis.Client.
func New(redisClient rueidis.Client, jwtSecret []byte, logger *slog.Logger) *Handler {
	return newWithDashRedis(&rueidisAdapter{redisClient}, jwtSecret, logger)
}

// NewForTest creates a Handler with a custom dashRedis implementation.
// Exported so tests in the dashboard_test package can inject fakes.
func NewForTest(rc dashRedis, jwtSecret []byte, logger *slog.Logger) *Handler {
	return newWithDashRedis(rc, jwtSecret, logger)
}

// newWithDashRedis creates a Handler with a custom dashRedis implementation (for testing).
func newWithDashRedis(rc dashRedis, jwtSecret []byte, logger *slog.Logger) *Handler {
	return &Handler{
		redis:     rc,
		jwtSecret: jwtSecret,
		logger:    logger,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// ServeHTTP is the http.Handler entry point. Registered at GET /ws/dashboard.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// ── 1. Auth ──────────────────────────────────────────────────────────────
	rawToken := h.extractToken(r)
	if rawToken == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	if _, err := auth.ValidateHS256JWT(rawToken, h.jwtSecret); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// ── 2. Upgrade to WebSocket ───────────────────────────────────────────────
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// upgrader already wrote HTTP error
		return
	}
	defer conn.Close()

	cw := &connWriter{conn: conn}

	// ── 3. Read subscribe message ─────────────────────────────────────────────
	_ = conn.SetReadDeadline(time.Now().Add(subscribeDeadline))
	_, raw, err := conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = cw.writeJSON(map[string]string{"type": "error", "message": "expected subscribe message"})
		return
	}

	var subMsg struct {
		Type     string   `json:"type"`
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(raw, &subMsg); err != nil || subMsg.Type != "subscribe" {
		_ = cw.writeJSON(map[string]string{"type": "error", "message": "expected {type:subscribe,channels:[...]}"})
		return
	}

	// ── 4. Validate channels ──────────────────────────────────────────────────
	var channels []string
	for _, ch := range subMsg.Channels {
		if IsValidChannel(ch) {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		_ = cw.writeJSON(map[string]string{"type": "error", "message": "no valid channels"})
		return
	}

	// ── 5. Ack ────────────────────────────────────────────────────────────────
	_ = cw.writeJSON(map[string]any{"type": "subscribed", "channels": channels})

	// ── 6. Build Redis channel list ───────────────────────────────────────────
	redisChannels := make([]string, len(channels))
	for i, ch := range channels {
		redisChannels[i] = dashPrefix + ch
	}

	// ── 7. Connection context — cancelled on WS disconnect ───────────────────
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// ── 8. Subscribe to Redis FIRST (no gap before snapshot) ─────────────────
	readyCh := make(chan struct{})
	go h.subscribeRedis(ctx, cw, redisChannels, readyCh)

	// Wait for Redis to confirm subscription before sending snapshot.
	select {
	case <-readyCh:
	case <-ctx.Done():
		return
	}

	// ── 9. Send snapshots ─────────────────────────────────────────────────────
	h.sendSnapshots(ctx, cw, channels)

	// ── 10. Ping loop ─────────────────────────────────────────────────────────
	go h.pingLoop(ctx, cw)

	// ── 11. Read loop — detect client disconnect ──────────────────────────────
	// Any read error (including clean close) cancels the connection context,
	// which terminates the subscribe goroutine and ping goroutine.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			cancel()
			return
		}
	}
}

// subscribeRedis opens a Redis pub/sub subscription and relays messages to the
// WS client. Closes readyCh before blocking so the caller can safely read
// snapshots with no event gap. Blocks until ctx is cancelled.
func (h *Handler) subscribeRedis(ctx context.Context, cw *connWriter, redisChannels []string, readyCh chan<- struct{}) {
	// Signal ready immediately — the Subscribe call below will block, but
	// rueidis issues the SUBSCRIBE command synchronously before the first fn
	// callback, so closing readyCh here is safe.
	close(readyCh)

	err := h.redis.Subscribe(ctx, redisChannels, func(msg rueidis.PubSubMessage) {
		logicalCh := strings.TrimPrefix(msg.Channel, dashPrefix)
		event := json.RawMessage(msg.Message)
		_ = cw.writeJSON(map[string]any{"channel": logicalCh, "event": event})
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		h.logger.Warn("dashboard: redis receive error", "error", err)
	}
}

// sendSnapshots delivers current state snapshots for channels that support them.
// Must be called after subscribeRedis signals ready (subscribe-before-snapshot ordering).
func (h *Handler) sendSnapshots(ctx context.Context, cw *connWriter, channels []string) {
	for _, ch := range channels {
		switch {
		case ch == "apps":
			h.sendAppsSnapshot(ctx, cw)
		case strings.HasPrefix(ch, "agent:"):
			agentID := ch[len("agent:"):]
			h.sendAgentSnapshot(ctx, cw, ch, agentID)
		case strings.HasPrefix(ch, "sessions:"):
			appID := ch[len("sessions:"):]
			h.sendSessionsSnapshot(ctx, cw, ch, appID)
		case strings.HasPrefix(ch, "run:"):
			runID := ch[len("run:"):]
			h.sendRunSnapshot(ctx, cw, ch, runID)
		case strings.HasPrefix(ch, "scan:"):
			artifactID := ch[len("scan:"):]
			h.sendScanSnapshot(ctx, cw, ch, artifactID)
		}
	}
}

// sendAppsSnapshot delivers the last known app liveness statuses from the
// Python bridge cache key (them:dash:app_status_cache). This lets new WS
// subscribers see "live/unreachable" immediately instead of waiting up to 30s
// for the next liveness probe publish.
func (h *Handler) sendAppsSnapshot(ctx context.Context, cw *connWriter) {
	raw, err := h.redis.Get(ctx, appStatusCacheKey)
	if err != nil || raw == "" {
		return
	}
	event := map[string]any{
		"type":     "app_status",
		"statuses": json.RawMessage(raw),
	}
	eventJSON, _ := json.Marshal(event)
	_ = cw.writeJSON(map[string]any{"channel": "apps", "event": json.RawMessage(eventJSON)})
}

// sendAgentSnapshot delivers the current scan state for an agent channel.
// Source: HGETALL them:scan:state:{agentID}
func (h *Handler) sendAgentSnapshot(ctx context.Context, cw *connWriter, ch, agentID string) {
	m, err := h.redis.HGetAll(ctx, scanStatePrefix+agentID)
	if err != nil || len(m) == 0 {
		return
	}
	event := make(map[string]any, len(m))
	for k, v := range m {
		event[k] = v
	}
	eventJSON, _ := json.Marshal(event)
	_ = cw.writeJSON(map[string]any{"channel": ch, "event": json.RawMessage(eventJSON)})
}

// sendSessionsSnapshot delivers the current session state snapshot.
// Source: HGETALL them:dash:sessions:state:{appID}
func (h *Handler) sendSessionsSnapshot(ctx context.Context, cw *connWriter, ch, appID string) {
	m, err := h.redis.HGetAll(ctx, sessionStatePrefix+appID)
	if err != nil || len(m) == 0 {
		return
	}
	sessions := make([]json.RawMessage, 0, len(m))
	for _, v := range m {
		sessions = append(sessions, json.RawMessage(v))
	}
	snapshot := map[string]any{
		"type":     "session_snapshot",
		"app_id":   appID,
		"sessions": sessions,
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	_ = cw.writeJSON(map[string]any{"channel": ch, "event": json.RawMessage(snapshotJSON)})
}

// sendRunSnapshot replays the last 100 events from the run's Redis Stream so
// late subscribers (e.g. the Monitor tab opened mid-run) catch up immediately.
// Source: XREVRANGE them:dash:run:{runID}:stream + - COUNT 100
// Events are reversed back to chronological order before sending.
func (h *Handler) sendRunSnapshot(ctx context.Context, cw *connWriter, ch, runID string) {
	key := "them:dash:run:" + runID + ":stream"
	entries, err := h.redis.XRevRange(ctx, key, "+", "-", 100)
	if err != nil || len(entries) == 0 {
		return
	}
	// XRevRange returns newest-first — reverse to chronological order.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	for _, e := range entries {
		if e.Data == "" {
			continue
		}
		_ = cw.writeJSON(map[string]any{"channel": ch, "event": json.RawMessage(e.Data)})
	}
}

// sendScanSnapshot delivers the current scan status for an artifact.
// Source: GET them:scan:state:{artifactID} (string value = scan_status).
// Clients subscribing after a scan completes receive the final status immediately.
func (h *Handler) sendScanSnapshot(ctx context.Context, cw *connWriter, ch, artifactID string) {
	val, err := h.redis.Get(ctx, scanStatePrefix+artifactID)
	if err != nil || val == "" {
		return
	}
	event := map[string]any{
		"type":        "artifact_scan",
		"artifact_id": artifactID,
		"scan_status": val,
	}
	eventJSON, _ := json.Marshal(event)
	_ = cw.writeJSON(map[string]any{"channel": ch, "event": json.RawMessage(eventJSON)})
}

// pingLoop sends a JSON ping every 30s until ctx is cancelled.
func (h *Handler) pingLoop(ctx context.Context, cw *connWriter) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cw.writeJSON(map[string]string{"type": "ping"}); err != nil {
				return
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// extractToken returns the raw JWT from ?token= query param or Authorization header.
func (h *Handler) extractToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// staticChannels is the whitelist of fixed channel names.
var staticChannels = map[string]bool{
	"runs": true, "agents": true, "metrics": true, "apps": true,
}

// IsValidChannel returns true if ch is a permitted channel name.
// Exported for testing.
func IsValidChannel(ch string) bool {
	if staticChannels[ch] {
		return true
	}
	if strings.HasPrefix(ch, "run:") && len(ch) > 4 {
		return true
	}
	if strings.HasPrefix(ch, "agent:") && len(ch) > 6 {
		return true
	}
	if strings.HasPrefix(ch, "sessions:") && len(ch) > 9 {
		return true
	}
	if strings.HasPrefix(ch, "scan:") && len(ch) > 5 {
		return true
	}
	return false
}

// connWriter serialises all WebSocket writes under a single mutex.
// gorilla/websocket does not allow concurrent writers.
type connWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (cw *connWriter) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.conn.WriteMessage(websocket.TextMessage, data)
}
