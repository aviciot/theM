package them

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// StepResult is the result of one message exchange.
type StepResult struct {
	Sent      string `json:"sent"`
	Received  string `json:"received"`
	LatencyMs int64  `json:"latency_ms"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// UserResult is the full result for one virtual user.
type UserResult struct {
	UserIndex  int          `json:"user_index"`
	TokenID    string       `json:"token_id,omitempty"`
	Connected  bool         `json:"connected"`
	Steps      []StepResult `json:"steps"`
	Error      string       `json:"error,omitempty"`
	DurationMs int64        `json:"duration_ms"`
	Status     string       `json:"status"` // "running" | "passed" | "failed"
}

// RunUser connects as an end-user to the given WS EP and runs the message script.
// Results are sent to the out channel as they happen.
func RunUser(ctx context.Context, baseURL, tenantSlug, appSlug, epSlug, bearerToken string, userIndex int, messages []string, out chan<- UserResult) {
	start := time.Now()
	result := UserResult{
		UserIndex: userIndex,
		Status:    "running",
	}

	emit := func() {
		select {
		case out <- result:
		default:
		}
	}

	// Build WS URL: replace http(s) with ws(s).
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += fmt.Sprintf("/apps/%s/%s/%s", tenantSlug, appSlug, epSlug)

	log := slog.With("user", userIndex, "ep", epSlug, "app", appSlug, "tenant", tenantSlug)
	log.Info("user: connecting", "url", wsURL, "auth", bearerToken != "")

	headers := http.Header{}
	if bearerToken != "" {
		headers.Set("Authorization", "Bearer "+bearerToken)
	}

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		result.Error = fmt.Sprintf("connect: %v", err)
		result.Status = "failed"
		result.DurationMs = time.Since(start).Milliseconds()
		log.Error("user: connect failed", "error", err)
		emit()
		return
	}
	defer conn.Close()

	result.Connected = true
	log.Info("user: connected")
	emit()

	for i, msg := range messages {
		if ctx.Err() != nil {
			log.Warn("user: context cancelled, stopping", "step", i)
			break
		}

		step := StepResult{Sent: msg}
		stepStart := time.Now()
		log.Info("user: sending message", "step", i, "msg_preview", truncate(msg, 80))

		// Send the message.
		payload, _ := json.Marshal(map[string]any{
			"type":    "message",
			"content": msg,
		})
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			step.Error = fmt.Sprintf("send: %v", err)
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: send failed", "step", i, "error", err)
			emit()
			continue
		}

		// Read until we get a final assistant message or error.
		step.Received, step.Error = readUntilFinal(ctx, conn)
		step.LatencyMs = time.Since(stepStart).Milliseconds()
		step.OK = step.Error == ""
		result.Steps = append(result.Steps, step)
		if step.OK {
			log.Info("user: step complete", "step", i, "latency_ms", step.LatencyMs, "reply_preview", truncate(step.Received, 120))
		} else {
			log.Error("user: step failed", "step", i, "latency_ms", step.LatencyMs, "error", step.Error)
		}
		emit()
	}

	result.DurationMs = time.Since(start).Milliseconds()
	allOK := result.Connected && len(result.Steps) > 0
	for _, s := range result.Steps {
		if !s.OK {
			allOK = false
		}
	}
	if allOK {
		result.Status = "passed"
	} else {
		result.Status = "failed"
	}
	log.Info("user: done", "status", result.Status, "duration_ms", result.DurationMs, "steps", len(result.Steps))
	emit()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// readUntilFinal reads WS messages until a final assistant message arrives.
func readUntilFinal(ctx context.Context, conn *websocket.Conn) (string, string) {
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	var lastContent string
	for {
		if ctx.Err() != nil {
			return lastContent, "context cancelled"
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if lastContent != "" {
				return lastContent, ""
			}
			return "", fmt.Sprintf("read: %v", err)
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "message":
			if content, ok := msg["content"].(string); ok {
				lastContent = content
			}
		case "run.complete", "done":
			return lastContent, ""
		case "error":
			errMsg, _ := msg["message"].(string)
			return lastContent, fmt.Sprintf("server error: %s", errMsg)
		}
	}
}
