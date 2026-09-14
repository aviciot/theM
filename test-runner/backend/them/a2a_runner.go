package them

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// A2A JSON-RPC 2.0 wire types (matches the-M agentregistry wire format).

type a2aRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  a2aParams `json:"params"`
	ID      string    `json:"id"`
}

type a2aParams struct {
	Message       a2aMessage       `json:"message"`
	Configuration a2aConfiguration `json:"configuration,omitempty"`
}

type a2aMessage struct {
	Role      string     `json:"role"`
	Parts     []a2aPart  `json:"parts"`
	MessageID string     `json:"messageId"`
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aConfiguration struct {
	ReturnImmediately bool `json:"returnImmediately"`
}

type a2aResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *a2aRPCError    `json:"error,omitempty"`
}

type a2aRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// extractA2AText pulls the first text part from an A2A result payload.
// The result may be a Task object: {"task":{"artifacts":[{"parts":[{"text":"..."}]}]}}
// or a flat Task:               {"artifacts":[{"parts":[{"text":"..."}]}]}
func extractA2AText(raw json.RawMessage) string {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return string(raw)
	}

	// Unwrap {"task": {...}} envelope if present.
	if taskRaw, ok := top["task"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(taskRaw, &inner) == nil {
			top = inner
		}
	}

	// Try artifacts[0].parts[0].text
	artifactsRaw, ok := top["artifacts"]
	if !ok {
		return string(raw)
	}
	var artifacts []map[string]json.RawMessage
	if json.Unmarshal(artifactsRaw, &artifacts) != nil || len(artifacts) == 0 {
		return string(raw)
	}
	partsRaw, ok := artifacts[0]["parts"]
	if !ok {
		return string(raw)
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(partsRaw, &parts) != nil || len(parts) == 0 {
		return string(raw)
	}
	textRaw, ok := parts[0]["text"]
	if !ok {
		return string(raw)
	}
	var text string
	if json.Unmarshal(textRaw, &text) == nil && text != "" {
		return text
	}
	return string(raw)
}

// RunUserA2A sends each message as a synchronous A2A JSON-RPC 2.0 SendMessage call.
// One HTTP POST per message; no streaming. Results are sent to out.
func RunUserA2A(ctx context.Context, baseURL, tenantSlug, appSlug, epSlug, bearerToken string, userIndex int, messages []string, out chan<- UserResult) {
	start := time.Now()
	result := UserResult{
		UserIndex: userIndex,
		Status:    "running",
	}
	log := slog.With("user", userIndex, "ep", epSlug, "app", appSlug, "tenant", tenantSlug, "mode", "a2a")

	// A2A route: /{tenant_slug}/a2a/{app_slug}/{ep_slug}
	epURL := fmt.Sprintf("%s/%s/a2a/%s/%s", baseURL, tenantSlug, appSlug, epSlug)
	log.Info("user: a2a start", "url", epURL, "auth", bearerToken != "")

	// Mark connected = true since A2A is stateless HTTP (no persistent connection).
	result.Connected = true

	emit := func() {
		select {
		case out <- result:
		default:
		}
	}

	httpClient := &http.Client{Timeout: 120 * time.Second}

	for i, msg := range messages {
		if ctx.Err() != nil {
			log.Warn("user: context cancelled", "step", i)
			break
		}

		step := StepResult{Sent: msg}
		stepStart := time.Now()
		log.Info("user: sending", "step", i, "msg_preview", truncate(msg, 80))

		reqBody := a2aRequest{
			JSONRPC: "2.0",
			Method:  "SendMessage",
			Params: a2aParams{
				Message: a2aMessage{
					Role:      "ROLE_USER",
					Parts:     []a2aPart{{Kind: "text", Text: msg}},
					MessageID: uuid.New().String(),
				},
				Configuration: a2aConfiguration{ReturnImmediately: false},
			},
			ID: uuid.New().String(),
		}

		body, _ := json.Marshal(reqBody)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, epURL, bytes.NewReader(body))
		if err != nil {
			step.Error = fmt.Sprintf("build request: %v", err)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: request build failed", "step", i, "error", err)
			emit()
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("A2A-Version", "1.0")
		if bearerToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+bearerToken)
		}

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			step.Error = fmt.Sprintf("http: %v", err)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: http failed", "step", i, "error", err)
			emit()
			continue
		}

		respBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			step.Error = fmt.Sprintf("http %d: %s", resp.StatusCode, truncate(string(respBytes), 200))
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: http error status", "step", i, "status", resp.StatusCode)
			emit()
			continue
		}

		var rpcResp a2aResponse
		if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
			step.Error = fmt.Sprintf("decode response: %v", err)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: decode failed", "step", i, "error", err)
			emit()
			continue
		}

		if rpcResp.Error != nil {
			step.Error = fmt.Sprintf("a2a error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
			step.LatencyMs = time.Since(stepStart).Milliseconds()
			result.Steps = append(result.Steps, step)
			result.Status = "failed"
			log.Error("user: a2a rpc error", "step", i, "code", rpcResp.Error.Code, "msg", rpcResp.Error.Message)
			emit()
			continue
		}

		step.Received = extractA2AText(rpcResp.Result)
		step.LatencyMs = time.Since(stepStart).Milliseconds()
		step.OK = true
		result.Steps = append(result.Steps, step)
		log.Info("user: step complete", "step", i, "latency_ms", step.LatencyMs, "reply_preview", truncate(step.Received, 120))
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
