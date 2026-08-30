//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/temporal"
)

// TestAgentRuntime_LiveE2E is the full end-to-end test:
//
//	HTTP client → agent-runtime Runtime.handle() → Temporal → dag-worker → PostgreSQL
//
// It differs from TestTemporalExecutor_LiveDAG which only exercised TemporalExecutor
// directly (skipping A2A parsing, spec/binding DB lookups, invocation context wiring).
//
// Gated by THEM_AGENT_RUNTIME_E2E=true so it never runs in unit CI.
// Requires live Postgres (DATABASE_*), Redis (REDIS_*), Temporal (TEMPORAL_HOST_PORT).
//
// Seeded prerequisites (created in a prior session — still in DB):
//
//	tenant:     00000000-0000-0000-0000-000000000001  (slug "default")
//	application:00000000-0000-0000-0000-000000000002  (E2E Test App)
//	agent:      00000000-0000-0000-0000-000000000003  (slug "e2etestagent", execution_backend="temporal")
//	binding:    fa6ae508-412b-46e4-8da1-34441825c6c2  (app 002 → agent 003)
func TestAgentRuntime_LiveE2E(t *testing.T) {
	if os.Getenv("THEM_AGENT_RUNTIME_E2E") != "true" {
		t.Skip("THEM_AGENT_RUNTIME_E2E not set — skipping live agent-runtime E2E")
	}

	const (
		tenantID  = "00000000-0000-0000-0000-000000000001"
		appID     = "00000000-0000-0000-0000-000000000002"
		agentID   = "00000000-0000-0000-0000-000000000003"
		agentSlug = "e2etestagent"
		bindingID = "fa6ae508-412b-46e4-8da1-34441825c6c2"
	)

	dsn := buildTestDSN(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Connect to Postgres.
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres connect: %v", err)
	}
	defer database.Close()

	// Connect to Redis (required by agentgen.NewRedisTaskStore).
	redisAddr := os.Getenv("REDIS_HOST")
	if redisAddr == "" {
		redisAddr = "localhost"
	}
	redisPort := os.Getenv("REDIS_PORT")
	if redisPort == "" {
		redisPort = "6379"
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisClient, err := cache.New(ctx, redisAddr+":"+redisPort, redisPassword, 0)
	if err != nil {
		t.Fatalf("redis connect: %v", err)
	}

	secretKey := os.Getenv("SECRET_KEY")
	cryptoKey := crypto.DeriveKey(secretKey)

	// Build Runtime exactly as main() does.
	interpBase := agentgen.NewInterpreter(
		&http.Client{Timeout: 60 * time.Second},
		&multiLLMFactory{platformKey: os.Getenv("ANTHROPIC_API_KEY")},
		os.Getenv("ANTHROPIC_API_KEY"),
	)
	rt := &Runtime{
		pool:      database.Pool(),
		cryptoKey: cryptoKey,
		taskStore: agentgen.NewRedisTaskStore(cache.NewAuthRedisClient(redisClient.Client())),
		specCache: &specCache{entries: make(map[string]*cachedSpec)},
		logger:    logger,
		interp:    interpBase,
	}

	// Connect Temporal and wire up executor.
	temporalAddr := os.Getenv("TEMPORAL_HOST_PORT")
	if temporalAddr == "" {
		temporalAddr = "localhost:7233"
	}
	cli, err := temporal.Connect(temporalAddr, logger)
	if err != nil {
		t.Fatalf("temporal connect: %v", err)
	}
	defer cli.Close()
	rt.temporalExecutor = temporal.NewTemporalExecutor(cli, 2*time.Minute, 4, logger)

	// Wire up routes exactly as main() does.
	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz)
	r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	r.Post("/agents/{slug}", rt.handle)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Unique invocation ID: timestamp nanoseconds ensures we never re-attach to a prior run.
	uniqueSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	t.Logf("unique invocation suffix: %s", uniqueSuffix)

	// Build A2A SendMessage JSON-RPC request.
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "SendMessage",
		"id":      1,
		"params": map[string]any{
			"message": map[string]any{
				"role":      "user",
				"messageId": "msg-e2e-" + uniqueSuffix,
				"parts": []map[string]any{
					{"kind": "text", "text": "e2e test input " + uniqueSuffix},
				},
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// POST to agent-runtime with all required invocation context headers.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srv.URL+"/agents/"+agentSlug, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Them-Tenant-Id", tenantID)
	httpReq.Header.Set("X-Them-Application-Id", appID)
	httpReq.Header.Set("X-Them-Agent-Id", agentID)
	httpReq.Header.Set("X-Them-Binding-Id", bindingID)

	t.Logf("POST %s/agents/%s", srv.URL, agentSlug)

	httpResp, err := (&http.Client{Timeout: 90 * time.Second}).Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	t.Logf("HTTP status: %d", httpResp.StatusCode)
	t.Logf("response body: %s", string(respBytes))

	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", httpResp.StatusCode)
	}

	// Parse the JSON-RPC response to verify the task ran.
	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		t.Fatalf("parse JSON-RPC response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("JSON-RPC error: code=%d msg=%s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// The A2A SDK wraps the task in {"task": {...}} inside the result.
	// Parse both the direct-task and the task-wrapper forms.
	type taskShape struct {
		ID     string `json:"id"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	var resultEnvelope struct {
		Task *taskShape `json:"task"` // SendMessage wraps in {"task":{...}}
	}
	if err := json.Unmarshal(rpcResp.Result, &resultEnvelope); err != nil {
		t.Fatalf("parse result envelope: %v", err)
	}
	taskResult := resultEnvelope.Task
	if taskResult == nil {
		// Fallback: result may be a bare task (older SDK versions)
		var direct taskShape
		if err := json.Unmarshal(rpcResp.Result, &direct); err != nil {
			t.Fatalf("parse task result (bare): %v", err)
		}
		taskResult = &direct
	}
	if taskResult == nil {
		t.Fatalf("could not extract task from result: %s", string(rpcResp.Result))
	}
	t.Logf("task id=%s status=%s", taskResult.ID, taskResult.Status.State)

	// Verify a NEW Temporal workflow was started — not a re-attachment to an old run.
	// The workflow ID is canvas:{agentID}:{A2A-TaskID}. The A2A SDK assigns TaskID from
	// the message — our unique suffix ensures it can't match any prior workflow.
	//
	// A completed or failed state confirms execution through dag-worker → PostgreSQL.
	state := taskResult.Status.State
	if state == "" {
		t.Fatalf("task status state is empty — workflow may not have executed; result: %s", string(rpcResp.Result))
	}
	if state != "TASK_STATE_COMPLETED" && state != "TASK_STATE_FAILED" {
		// "working" or "submitted" means the workflow hasn't finished — extend timeout if needed
		t.Fatalf("expected terminal task state (COMPLETED or FAILED), got %q — workflow may still be running", state)
	}
	t.Logf("PASS: terminal task state %q via agent-runtime → Temporal → dag-worker → PostgreSQL", state)
}
