package admin

// runScanJob executes a security scan in the background. It is launched as a
// goroutine by the security-scan handler immediately after the 202 response
// is written. It communicates progress via Redis pub/sub (them:dash:agent:{id})
// and persists the final result via UpdateAgentScanResult.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aviciot/them/internal/crypto"
	"github.com/redis/rueidis"
)

// scanJobDAL is the minimal DAL surface runScanJob needs.
type scanJobDAL interface {
	UpdateAgentScanResult(ctx context.Context, agentID string, result []byte) error
}

// scanRedis is the minimal Redis surface runScanJob needs.
type scanRedis interface {
	Do(ctx context.Context, cmd rueidis.Completed) rueidis.RedisResult
	B() rueidis.Builder
}

// scanAgentPayload holds the target agent data sent to the scanner.
type scanAgentPayload struct {
	AgentID          string `json:"agent_id"`
	Slug             string `json:"slug"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
	EndpointURL      string `json:"endpoint_url"`
	AgentCard        any    `json:"agent_card"`
	Skills           any    `json:"skills"`
	SupportsStreaming bool   `json:"supports_streaming"`
	SupportsPush     bool   `json:"supports_push"`
	HasAuthToken     bool   `json:"has_auth_token"`
}

// scanStateHashKey returns the tenant-scoped Redis Hash key for scan state.
func scanStateHashKey(tenantID, agentID string) string {
	return fmt.Sprintf("them:%s:scan:state:%s", tenantID, agentID)
}

// scanDashChannel returns the Redis pub/sub channel for dashboard events.
func scanDashChannel(agentID string) string {
	return fmt.Sprintf("them:dash:agent:%s", agentID)
}

// hsetExpire writes fields to a Redis Hash and sets the TTL.
func hsetExpire(ctx context.Context, rc scanRedis, key string, ttlSeconds int64, fields map[string]string) {
	b := rc.B().Hset().Key(key).FieldValue()
	for k, v := range fields {
		b = b.FieldValue(k, v)
	}
	_ = rc.Do(ctx, b.Build())
	_ = rc.Do(ctx, rc.B().Expire().Key(key).Seconds(ttlSeconds).Build())
}

// pubJSON publishes a JSON-marshalled event to a Redis pub/sub channel.
func pubJSON(ctx context.Context, rc scanRedis, channel string, event map[string]any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = rc.Do(ctx, rc.B().Publish().Channel(channel).Message(string(data)).Build())
}

// runScanJob is launched as a goroutine. It must not panic — all errors are
// silently handled and surfaced as scan_failed events.
func runScanJob(
	tenantID string,
	agentID string,
	payload scanAgentPayload,
	scannerEndpoint string,
	scannerTokenEncrypted string,
	fernetKey []byte,
	timeoutSec float64,
	rc scanRedis,
	d scanJobDAL,
	log *slog.Logger,
) {
	ctx := context.Background()
	hashKey := scanStateHashKey(tenantID, agentID)
	dashCh := scanDashChannel(agentID)

	// Step 1 — Announce scan started.
	startEvent := map[string]any{"type": "scan_started", "agent_id": agentID}
	hsetExpire(ctx, rc, hashKey, 300, map[string]string{
		"type":     "scan_started",
		"agent_id": agentID,
	})
	pubJSON(ctx, rc, dashCh, startEvent)

	// Step 3 — Timed progress steps in a separate goroutine.
	done := make(chan struct{})
	go func() {
		type step struct {
			delay time.Duration
			msg   string
		}
		steps := []step{
			{800 * time.Millisecond, "Submitting to scanner…"},
			{1500 * time.Millisecond, "Probing endpoint…"},
			{4000 * time.Millisecond, "Analyzing agent card…"},
			{7000 * time.Millisecond, "Computing risk score…"},
		}
		for _, s := range steps {
			timer := time.NewTimer(s.delay)
			select {
			case <-done:
				timer.Stop()
				return
			case <-timer.C:
				ev := map[string]any{
					"type":     "scan_step",
					"agent_id": agentID,
					"step":     s.msg,
				}
				hsetExpire(ctx, rc, hashKey, 300, map[string]string{
					"type":     "scan_step",
					"agent_id": agentID,
					"step":     s.msg,
				})
				pubJSON(ctx, rc, dashCh, ev)
			}
		}
	}()

	// Resolve scanner auth token.
	var scannerToken string
	if scannerTokenEncrypted != "" {
		t, err := crypto.DecryptStored(fernetKey, scannerTokenEncrypted)
		if err == nil {
			scannerToken = t
		}
	}

	// Step 4 — Call security scanner via A2A JSON-RPC 2.0.
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	timeout := time.Duration(float64(time.Second) * timeoutSec)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		close(done)
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "marshal payload: "+err.Error())
		return
	}

	msgID := newUUID()
	rpcBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "SendMessage",
		"params": map[string]any{
			"message": map[string]any{
				"role":      1, // ROLE_USER
				"messageId": msgID,
				"parts": []any{
					map[string]any{"text": string(payloadBytes)},
				},
			},
		},
	}
	rpcBytes, err := json.Marshal(rpcBody)
	if err != nil {
		close(done)
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "marshal rpc: "+err.Error())
		return
	}

	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(scanCtx, http.MethodPost,
		scannerEndpoint+"/", bytes.NewReader(rpcBytes))
	if err != nil {
		close(done)
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "create request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	if scannerToken != "" {
		req.Header.Set("Authorization", "Bearer "+scannerToken)
	}

	resp, err := http.DefaultClient.Do(req)
	// Cancel progress goroutine.
	close(done)
	if err != nil {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "http: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "read response: "+err.Error())
		return
	}

	// Step 5 — Parse A2A v1.0 SendMessage response: result.task.artifacts[0].parts[0].text
	var rpcResp struct {
		Result *struct {
			Task *struct {
				Artifacts []struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"artifacts"`
			} `json:"task"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "parse rpc response: "+err.Error())
		return
	}
	if rpcResp.Error != nil {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "rpc error: "+rpcResp.Error.Message)
		return
	}
	if rpcResp.Result == nil || rpcResp.Result.Task == nil ||
		len(rpcResp.Result.Task.Artifacts) == 0 ||
		len(rpcResp.Result.Task.Artifacts[0].Parts) == 0 {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "empty result in rpc response")
		return
	}
	resultText := rpcResp.Result.Task.Artifacts[0].Parts[0].Text
	if resultText == "" {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "empty result text")
		return
	}

	// Step 6 — Parse the result JSON.
	var scanResult map[string]any
	if err := json.Unmarshal([]byte(resultText), &scanResult); err != nil {
		publishScanFailed(ctx, rc, hashKey, dashCh, agentID, "parse result json: "+err.Error())
		return
	}

	// Step 8 — Persist result to DB.
	resultBytes, _ := json.Marshal(scanResult)
	if dbErr := d.UpdateAgentScanResult(ctx, agentID, resultBytes); dbErr != nil {
		if log != nil {
			log.Warn("scan: failed to persist result", "agent_id", agentID, "error", dbErr)
		}
	}

	// Step 8 — Publish scan_complete event.
	scannedAt, _ := scanResult["scanned_at"].(string)
	completeEvent := map[string]any{
		"type":        "scan_complete",
		"agent_id":    agentID,
		"score":       scanResult["score"],
		"risk":        scanResult["risk"],
		"summary":     scanResult["summary"],
		"findings":    scanResult["findings"],
		"http_probes": scanResult["http_probes"],
		"scanned_at":  scannedAt,
	}
	completeFields := map[string]string{
		"type":     "scan_complete",
		"agent_id": agentID,
	}
	if v, ok := scanResult["score"]; ok {
		completeFields["score"] = fmt.Sprintf("%v", v)
	}
	if v, ok := scanResult["risk"]; ok {
		completeFields["risk"] = fmt.Sprintf("%v", v)
	}
	if v, ok := scanResult["summary"]; ok {
		completeFields["summary"] = fmt.Sprintf("%v", v)
	}
	if v, ok := scanResult["scanned_at"]; ok {
		completeFields["scanned_at"] = fmt.Sprintf("%v", v)
	}
	if v, ok := scanResult["findings"]; ok {
		if b, err := json.Marshal(v); err == nil {
			completeFields["findings"] = string(b)
		}
	}
	if v, ok := scanResult["http_probes"]; ok {
		if b, err := json.Marshal(v); err == nil {
			completeFields["http_probes"] = string(b)
		}
	}
	hsetExpire(ctx, rc, hashKey, 300, completeFields)
	pubJSON(ctx, rc, dashCh, completeEvent)
}

// publishScanFailed emits a scan_failed event and sets the hash TTL to 30s.
func publishScanFailed(ctx context.Context, rc scanRedis, hashKey, dashCh, agentID, errMsg string) {
	ev := map[string]any{
		"type":     "scan_failed",
		"agent_id": agentID,
		"error":    errMsg,
	}
	hsetExpire(ctx, rc, hashKey, 30, map[string]string{
		"type":     "scan_failed",
		"agent_id": agentID,
		"error":    errMsg,
	})
	pubJSON(ctx, rc, dashCh, ev)
}
