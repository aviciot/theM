//go:build integration

package temporal_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

// TestTemporalConnect_Unavailable verifies fail-closed behaviour:
// Connect returns a non-nil error when Temporal is unreachable.
// This test requires no live Temporal — it targets a port nothing listens on.
func TestTemporalConnect_Unavailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	_, err := temporal.Connect("localhost:19999", logger)
	if err == nil {
		t.Fatal("expected connection error for unreachable Temporal, got nil")
	}
	t.Logf("connect correctly refused: %v", err)
}

// TestTemporalExecutor_EmptyPlan_Integration verifies that Execute rejects a
// nil plan before making any Temporal RPC call, even when called with a nil
// client (nil client panics on any RPC call — so a clean error proves the
// guard runs first).
func TestTemporalExecutor_EmptyPlan_Integration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	exec := temporal.NewTemporalExecutor(nil, 5*time.Second, 0, logger)
	ic := &agentgen.InvocationContext{
		TenantID:      "00000000-0000-0000-0000-000000000001",
		ApplicationID: "00000000-0000-0000-0000-000000000002",
		AgentID:       "00000000-0000-0000-0000-000000000003",
		InvocationID:  "integration-test-invocation-1",
	}
	_, err := exec.Execute(context.Background(), ic, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil plan, got nil")
	}
	t.Logf("nil plan correctly rejected: %v", err)

	emptyPlan := &agentgen.ExecutionPlan{Nodes: nil}
	_, err = exec.Execute(context.Background(), ic, emptyPlan, nil)
	if err == nil {
		t.Fatal("expected error for empty plan, got nil")
	}
	t.Logf("empty plan correctly rejected: %v", err)
}

// TestTemporalExecutor_LiveDAG exercises TemporalExecutor.Execute directly against a
// live Temporal server + dag-worker. It does NOT go through the agent-runtime HTTP
// path (no A2A parsing, no spec/binding DB lookup, no invocation-context wiring).
// For the full agent-runtime → Temporal → dag-worker E2E, see:
//   cmd/agent-runtime/e2e_integration_test.go:TestAgentRuntime_LiveE2E
//
// This test is still useful for validating the Temporal executor wiring in isolation.
// Gated by THEM_TEMPORAL_E2E=true. Uses a timestamp-based InvocationID to ensure a
// NEW workflow is started on every run (never re-attached to a prior completed run).
func TestTemporalExecutor_LiveDAG(t *testing.T) {
	if os.Getenv("THEM_TEMPORAL_E2E") != "true" {
		t.Skip("THEM_TEMPORAL_E2E not set — skipping live Temporal executor test")
	}

	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = "localhost:7233"
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cli, err := temporal.Connect(hostPort, logger)
	if err != nil {
		t.Fatalf("Temporal connect failed: %v", err)
	}
	defer cli.Close()

	exec := temporal.NewTemporalExecutor(cli, 2*time.Minute, 4, logger)

	// Minimal single-response node plan (no LLM call — step type = "response").
	plan := &agentgen.ExecutionPlan{
		SkillID: "e2e-skill",
		StartID: "step-1",
		Nodes: []*agentgen.PlanNode{
			{StepID: "step-1", Type: "response", Outputs: []agentgen.VarRef{{Name: "output"}}},
		},
	}

	// Unique InvocationID per test run: prevents re-attachment to a prior completed
	// workflow that carries the same ID (ALLOW_DUPLICATE_FAILED_ONLY re-attaches on
	// AlreadyStarted, which also fires for a successfully completed workflow).
	uniqueID := fmt.Sprintf("e2e-executor-%d", time.Now().UnixNano())
	t.Logf("unique invocation ID: %s", uniqueID)

	ic := &agentgen.InvocationContext{
		TenantID:      "00000000-0000-0000-0000-000000000001",
		ApplicationID: "00000000-0000-0000-0000-000000000002",
		AgentID:       "00000000-0000-0000-0000-000000000003",
		InvocationID:  uniqueID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx, ic, plan, agentgen.PipelineVars{"input": "hello"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	t.Logf("TemporalExecutor E2E (direct, not via agent-runtime): text=%q mt=%q", result.Text, result.MediaType)
}
