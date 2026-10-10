package them

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type gwMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type gwRequest struct {
	Model     string      `json:"model"`
	Messages  []gwMessage `json:"messages"`
	MaxTokens int         `json:"max_tokens"`
	Stream    bool        `json:"stream"`
}

type gwChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
}

type gwResponse struct {
	Choices []gwChoice `json:"choices"`
}

// RunUserGateway sends the messages array to the gateway OpenAI-compat endpoint.
// All virtual users share the same gateway token (it's a service credential).
// If stream=true, uses SSE streaming and accumulates chunks.
// messages are sent as a full conversation array (multi-turn history passthrough).
func RunUserGateway(ctx context.Context, baseURL, tenantSlug, gatewayToken string, stream bool, userIndex int, messages []string, out chan<- UserResult) {
	start := time.Now()
	result := UserResult{
		UserIndex: userIndex,
		Connected: true,
		Status:    "running",
	}
	log := slog.With("user", userIndex, "tenant", tenantSlug, "mode", "gateway", "stream", stream)

	emit := func() {
		select {
		case out <- result:
		default:
		}
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/" + tenantSlug + "/llm/v1/chat/completions"
	log.Info("user: gateway start", "url", endpoint)

	httpClient := &http.Client{Timeout: 120 * time.Second}

	// Build conversation history incrementally across messages.
	// Each message in the scenario is a new user turn; we accumulate history.
	history := []gwMessage{}
	for i, msg := range messages {
		if ctx.Err() != nil {
			log.Warn("user: context cancelled", "step", i)
			break
		}

		step := StepResult{Sent: msg}
		stepStart := time.Now()
		log.Info("user: sending", "step", i, "msg_preview", truncate(msg, 80))

		history = append(history, gwMessage{Role: "user", Content: msg})

		reqBody := gwRequest{
			Model:     "claude-3-5-haiku-20241022",
			Messages:  history,
			MaxTokens: 256,
			Stream:    stream,
		}
		body, _ := json.Marshal(reqBody)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			step.Error = fmt.Sprintf("build request: %v", err)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			emit()
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+gatewayToken)
		if stream {
			httpReq.Header.Set("Accept", "text/event-stream")
		}

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			step.Error = fmt.Sprintf("http: %v", err)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			emit()
			continue
		}

		var reply string
		if stream {
			reply, err = readSSEStream(resp.Body)
		} else {
			reply, err = readFullResponse(resp)
		}
		resp.Body.Close()

		step.LatencyMs = time.Since(stepStart).Milliseconds()
		if err != nil {
			step.Error = err.Error()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: step failed", "step", i, "error", err)
			emit()
			continue
		}

		step.Received = reply
		step.OK = true
		result.Steps = append(result.Steps, step)

		// Append assistant reply to history for next turn.
		history = append(history, gwMessage{Role: "assistant", Content: reply})

		log.Info("user: step complete", "step", i, "latency_ms", step.LatencyMs, "reply_preview", truncate(reply, 120))
		emit()
	}

	result.DurationMs = time.Since(start).Milliseconds()
	allOK := len(result.Steps) > 0
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
	log.Info("user: done", "status", result.Status, "duration_ms", result.DurationMs)
	emit()
}

func readFullResponse(resp *http.Response) (string, error) {
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %v", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var gwr gwResponse
	if err := json.Unmarshal(b, &gwr); err != nil {
		return "", fmt.Errorf("decode: %v", err)
	}
	if len(gwr.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return gwr.Choices[0].Message.Content, nil
}

func readSSEStream(body io.Reader) (string, error) {
	var buf strings.Builder
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var gwr gwResponse
		if json.Unmarshal([]byte(data), &gwr) != nil {
			continue
		}
		if len(gwr.Choices) > 0 {
			buf.WriteString(gwr.Choices[0].Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return buf.String(), fmt.Errorf("stream read: %v", err)
	}
	return buf.String(), nil
}
