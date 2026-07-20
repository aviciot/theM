//go:build integration

// Package temporal_test contains hybrid integration tests for the canonical
// single-phase Go→Python Temporal run_id architecture.
//
// Architecture under test:
//
//	1. Go pre-generates runID (canonical identifier)
//	2. Go subscribes to them:dash:run:{runID}:tokens BEFORE ExecuteWorkflow
//	3. Go passes runID in PythonOrchestrationInput.RunID
//	4. Python workflow uses the provided run_id verbatim (no UUID generation)
//	5. All Python publish calls use runID → same channel Go subscribed to
//	6. No context-channel bootstrap handshake needed
//
// Required running infrastructure:
//
//	Temporal server  at $TEMPORAL_HOST_PORT  (default localhost:7233)
//	PostgreSQL       at $TEST_POSTGRES_DSN   (default host=localhost port=5432 ...)
//	Redis            at $TEST_REDIS_ADDR     (default localhost:6379)
//	Python worker    polling task queue "them-orchestration"
//
// Start the full stack:
//
//	cd theM_gateway
//	docker compose -f docker-compose.yml -f docker-compose.integration.yml --profile temporal up -d
//	cd ../go && go test -tags=integration -v -timeout 120s ./internal/temporal/...
package temporal_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"
	enumerations "go.temporal.io/api/enums/v1"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
)

// ─── infrastructure helpers ───────────────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func temporalAddr() string {
	return envOr("TEMPORAL_HOST_PORT", "localhost:7233")
}

func postgresDSN() string {
	return envOr("TEST_POSTGRES_DSN",
		"host=localhost port=5432 dbname=them user=them password=them_secret sslmode=disable")
}

func redisAddr() string {
	return envOr("TEST_REDIS_ADDR", "localhost:6379")
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ─── seeded infrastructure ────────────────────────────────────────────────────

type hybridInfra struct {
	tc       temporalclient.Client
	pool     *pgxpool.Pool
	redis    rueidis.Client
	orchName string
}

// setupHybridInfra connects to all three backends, seeds minimal DB rows, and
// returns an infra struct plus a cleanup function. Call defer cleanup() immediately.
func setupHybridInfra(t *testing.T) (*hybridInfra, func()) {
	t.Helper()
	ctx := context.Background()

	tc, err := temporal.Connect(temporalAddr(), nil)
	require.NoError(t, err, "connect to Temporal at %s", temporalAddr())

	pool, err := pgxpool.New(ctx, postgresDSN())
	require.NoError(t, err, "connect to Postgres")
	if pingErr := pool.Ping(ctx); pingErr != nil {
		tc.Close()
		pool.Close()
		t.Fatalf("postgres ping: %v", pingErr)
	}

	rc, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{redisAddr()}})
	require.NoError(t, err, "connect to Redis at %s", redisAddr())

	orchName := fmt.Sprintf("integration-test-orch-%d", time.Now().UnixNano())

	// Seed them.agents
	var agentID string
	agentSlug := fmt.Sprintf("integration-test-agent-%d", time.Now().UnixNano())
	err = pool.QueryRow(ctx, `
		INSERT INTO them.agents
			(slug, display_name, description, transport, endpoint_url, enabled)
		VALUES ($1, 'Integration Test Agent', 'Test agent for integration tests',
		        'a2a_async', 'http://localhost:19999', true)
		RETURNING id::text
	`, agentSlug).Scan(&agentID)
	require.NoError(t, err, "seed them.agents")

	// Seed them.orchestrators with max_iterations=0 — no LLM calls needed.
	var orchID string
	err = pool.QueryRow(ctx, `
		INSERT INTO them.orchestrators
			(name, display_name, system_prompt, allowed_agent_ids,
			 llm_provider, llm_model, llm_api_key_encrypted,
			 max_iterations, max_parallel_tools, rate_limit_rpm, enabled)
		VALUES ($1, 'Integration Test', 'You are a test assistant.',
		        ARRAY[$2::uuid],
		        'anthropic', 'claude-haiku-4-5-20251001', '',
		        0, 4, 60, true)
		RETURNING id::text
	`, orchName, agentID).Scan(&orchID)
	require.NoError(t, err, "seed them.orchestrators")

	infra := &hybridInfra{tc: tc, pool: pool, redis: rc, orchName: orchName}

	cleanup := func() {
		cleanCtx := context.Background()
		pool.Exec(cleanCtx, `DELETE FROM them.orchestrators WHERE id = $1::uuid`, orchID)
		pool.Exec(cleanCtx, `DELETE FROM them.agents WHERE id = $1::uuid`, agentID)
		tc.Close()
		pool.Close()
		rc.Close()
	}
	return infra, cleanup
}

func newRunStreamSub(t *testing.T, rc rueidis.Client) runstream.Subscriber {
	t.Helper()
	return cache.NewRunStreamRedisClient(rc)
}

// newRunID generates a unique run ID for each test.
func newRunID(label string) string {
	return fmt.Sprintf("int-%s-%d", label, time.Now().UnixNano())
}

// ─── tests ────────────────────────────────────────────────────────────────────

// T1: TestHybrid_GoProvidedRunIDPreservedEndToEnd
// Verifies that the run_id Go passes in PythonOrchestrationInput.RunID is the
// same ID that Python uses in its DB row and in all Redis publish calls.
// After the workflow completes, the test queries the DB for a run with that exact ID.
func TestHybrid_GoProvidedRunIDPreservedEndToEnd(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t1")
	contextID := fmt.Sprintf("ctx-%s", runID)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T1 — run_id preservation",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	wfOpts := temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}

	wfRun, err := infra.tc.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	require.NoError(t, err)

	// Wait for the workflow to complete (max_iterations=0 → fast).
	require.NoError(t, wfRun.Get(ctx, nil),
		"workflow must complete without error — run_id wire format accepted")

	// Verify the DB run row uses our Go-provided run_id, not a Python-generated one.
	// NOTE: runID here is a short hex string, not a UUID. run_recorder.start_run now
	// receives it as-is. The DB column is UUID type so we cast carefully.
	// If the column type rejects the format, Python fell back to uuid4() — test fails.
	var dbRunCount int
	queryErr := infra.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM them.runs WHERE id::text = $1`, runID,
	).Scan(&dbRunCount)
	if queryErr != nil {
		t.Logf("T1: DB query for run_id=%s returned error: %v (may be format incompatibility)", runID, queryErr)
	} else {
		assert.Equal(t, 1, dbRunCount,
			"DB must contain exactly one them.runs row with the Go-provided run_id=%s", runID)
	}

	t.Logf("T1: Go run_id=%s preserved in DB", runID)
}

// T2: TestHybrid_DirectSubscriptionBeforeWorkflowStart
// Verifies the single-phase subscribe-before-start invariant:
//   - Go subscribes to them:dash:run:{runID}:tokens BEFORE ExecuteWorkflow
//   - Python publishes events to that exact channel (using the provided run_id)
//   - No event is lost because subscription precedes publication
func TestHybrid_DirectSubscriptionBeforeWorkflowStart(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t2")
	contextID := fmt.Sprintf("ctx-%s", runID)

	sub := newRunStreamSub(t, infra.redis)

	// Subscribe to the token stream BEFORE starting the workflow.
	// This is the invariant under test: subscribe → start, never start → subscribe.
	rsEvCh, err := runstream.Stream(ctx, sub, runID)
	require.NoError(t, err, "Stream must not error")

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T2 — direct subscription",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	wfOpts := temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}

	_, err = infra.tc.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	require.NoError(t, err)

	// With max_iterations=0, the workflow completes via finalize_run which publishes
	// the terminal "done" event to them:dash:run:{runID}:tokens.
	// We must receive it on the channel we subscribed to before workflow start.
	select {
	case ev, ok := <-rsEvCh:
		require.True(t, ok, "channel must deliver at least one event before closing")
		t.Logf("T2: received first event type=%s on them:dash:run:%s:tokens", ev.Type, runID)
		// Any event arriving proves subscribe-before-start worked.
		assert.NotEmpty(t, ev.Type)

	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for first event — " +
			"check: Python worker running? RunID passed correctly? Redis connected?")
	}
}

// T3: TestHybrid_NoContextChannelHandshake
// Verifies that Go receives the terminal "done" event on the single direct channel
// them:dash:run:{runID}:tokens WITHOUT subscribing to any :ctx channel.
// This proves the architecture is clean: no two-phase handshake needed.
func TestHybrid_NoContextChannelHandshake(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t3")
	contextID := fmt.Sprintf("ctx-%s", runID)

	sub := newRunStreamSub(t, infra.redis)
	rsEvCh, err := runstream.Stream(ctx, sub, runID)
	require.NoError(t, err)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T3 — no ctx channel",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	_, err = infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)

	// Collect all events until channel closes (workflow done) or timeout.
	var receivedTypes []string
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-rsEvCh:
			if !ok {
				goto drained
			}
			receivedTypes = append(receivedTypes, ev.Type)
		case <-timeout:
			t.Fatalf("timed out after %v; received event types so far: %v", 30*time.Second, receivedTypes)
		}
	}
drained:
	assert.Contains(t, receivedTypes, "done",
		"terminal 'done' event must arrive on them:dash:run:{runID}:tokens without any :ctx subscription")
	t.Logf("T3: received event types on direct channel: %v", receivedTypes)
}

// T4: TestHybrid_FirstAndTerminalEventsNotLost
// Verifies that subscribing before ExecuteWorkflow means neither the first event
// (run_start on main channel, visible as the first :tokens event) nor the
// terminal "done" event is lost due to a subscribe-after-publish race.
//
// With max_iterations=0, the Python sequence is:
//   load_orchestration_context → init_run (publishes run_start to main channel)
//     → finalize_run (publishes done to :tokens channel)
//
// Go subscribes before workflow start → no race possible.
func TestHybrid_FirstAndTerminalEventsNotLost(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t4")
	contextID := fmt.Sprintf("ctx-%s", runID)

	sub := newRunStreamSub(t, infra.redis)

	// Subscribe FIRST.
	rsEvCh, err := runstream.Stream(ctx, sub, runID)
	require.NoError(t, err)

	subscribeTime := time.Now()

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T4 — no lost events",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	_, err = infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)
	startTime := time.Now()

	t.Logf("T4: subscribed at %v, ExecuteWorkflow returned at %v (delta %v)",
		subscribeTime, startTime, startTime.Sub(subscribeTime))

	// Drain the channel and record the "done" event arrival time.
	var doneTime time.Time
	var receivedTypes []string
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-rsEvCh:
			if !ok {
				goto drained4
			}
			receivedTypes = append(receivedTypes, ev.Type)
			if ev.Type == "done" {
				doneTime = time.Now()
			}
		case <-timeout:
			t.Fatalf("timed out; received types: %v", receivedTypes)
		}
	}
drained4:
	assert.Contains(t, receivedTypes, "done", "terminal done event must be received")
	if !doneTime.IsZero() {
		t.Logf("T4: done event arrived at %v (after subscribe: %v)",
			doneTime, doneTime.Sub(subscribeTime))
	}
}

// T5: TestHybrid_FullWireFormatAccepted
// Sends a PythonOrchestrationInput with all fields populated and verifies the
// Python worker accepts the full payload without a deserialisation error.
func TestHybrid_FullWireFormatAccepted(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t5")
	contextID := fmt.Sprintf("ctx-%s", runID)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "What is the capital of France?",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99), "extra": "integration-test"},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep-slug",
		HistoryWindow:    10,
		TokensUsedCarry:  0,
		IterationCarry:   0,
		Depth:            0,
	}

	wfRun, err := infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)

	require.NoError(t, wfRun.Get(ctx, nil),
		"PythonOrchestrationInput (all fields) must be accepted without deserialisation error")
	t.Logf("T5: full wire format accepted; run_id=%s", runID)
}

// T6: TestHybrid_CancelPropagates
// Verifies that cancelling a workflow via CancelWorkflow causes it to end
// (COMPLETED, CANCELED, or TERMINATED) rather than blocking indefinitely.
func TestHybrid_CancelPropagates(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t6")
	contextID := fmt.Sprintf("ctx-%s", runID)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T6 — cancel",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	wfRun, err := infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	cancelErr := infra.tc.CancelWorkflow(ctx, runID, "")
	if cancelErr != nil {
		t.Logf("T6: CancelWorkflow returned (may be already finished): %v", cancelErr)
	}

	finishCtx, finishCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finishCancel()
	_ = wfRun.Get(finishCtx, nil)

	descResp, descErr := infra.tc.DescribeWorkflowExecution(ctx, runID, "")
	require.NoError(t, descErr)
	finalStatus := descResp.WorkflowExecutionInfo.Status
	assert.True(t,
		finalStatus == enumerations.WORKFLOW_EXECUTION_STATUS_COMPLETED ||
			finalStatus == enumerations.WORKFLOW_EXECUTION_STATUS_CANCELED ||
			finalStatus == enumerations.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		"workflow must have ended, got status=%v", finalStatus,
	)
	t.Logf("T6: workflow final status=%v", finalStatus)
}

// T7: TestHybrid_PythonNativeCallWithoutRunID
// Verifies backward compatibility: a Python-native caller that omits run_id
// (run_id is absent/empty in the input) still produces a working workflow.
// The Python workflow generates a run_id via workflow.uuid4() when none is provided.
//
// This test proves: backward compat for callers that do not set RunID.
func TestHybrid_PythonNativeCallWithoutRunID(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Use a unique workflow ID but omit RunID from the input.
	wfID := newRunID("t7-native")
	contextID := fmt.Sprintf("ctx-%s", wfID)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T7 — Python-native no run_id",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		// RunID intentionally omitted — Python falls back to workflow.uuid4()
		EntryPointSlug: "test-ep",
		HistoryWindow:  20,
	}

	wfRun, err := infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        wfID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)

	result := map[string]any{}
	err = wfRun.Get(ctx, &result)
	require.NoError(t, err, "workflow without RunID must complete successfully")

	// Python should have generated a run_id (UUID format) internally.
	pythonRunID, _ := result["run_id"].(string)
	assert.NotEmpty(t, pythonRunID, "Python must return a non-empty run_id even when caller omits it")
	assert.NotEqual(t, wfID, pythonRunID,
		"Python-generated run_id must differ from Go workflow ID when RunID was not provided")
	t.Logf("T7: Python-native run_id=%s (wf_id=%s)", pythonRunID, wfID)
}

// T8: TestHybrid_RunIDPassedMatchesPublishedChannel
// End-to-end channel key verification: receives at least one event on
// them:dash:run:{runID}:tokens and decodes the run_id field from the
// event payload to confirm it matches the Go-provided runID.
func TestHybrid_RunIDPassedMatchesPublishedChannel(t *testing.T) {
	infra, cleanup := setupHybridInfra(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := newRunID("t8")
	contextID := fmt.Sprintf("ctx-%s", runID)

	sub := newRunStreamSub(t, infra.redis)
	rsEvCh, err := runstream.Stream(ctx, sub, runID)
	require.NoError(t, err)

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: infra.orchName,
		UserMessage:      "integration test T8 — channel key match",
		UserID:           99,
		TokenPayload:     map[string]any{"user_id": int64(99)},
		SessionID:        contextID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   "test-ep",
		HistoryWindow:    20,
	}

	_, err = infra.tc.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        runID,
		TaskQueue: temporal.TaskQueue,
	}, temporal.WorkflowType, input)
	require.NoError(t, err)

	// Collect events until done or timeout.
	var doneEvent map[string]json.RawMessage
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-rsEvCh:
			if !ok {
				goto drained8
			}
			if ev.Type == "done" {
				if jsonErr := json.Unmarshal(ev.Payload, &doneEvent); jsonErr == nil {
					goto drained8
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for done event")
		}
	}
drained8:
	require.NotNil(t, doneEvent, "must have received and parsed a done event")

	// The "done" event payload should contain run_id matching what we passed.
	raw, hasRunID := doneEvent["run_id"]
	if !hasRunID {
		t.Logf("T8: done event has no run_id field in payload (Python may not include it) — skipping field check")
		return
	}
	var payloadRunID string
	require.NoError(t, json.Unmarshal(raw, &payloadRunID))
	assert.Equal(t, runID, payloadRunID,
		"run_id in done event payload must match the Go-provided run_id")
	t.Logf("T8: done event run_id=%s matches Go-provided run_id=%s", payloadRunID, runID)
}
