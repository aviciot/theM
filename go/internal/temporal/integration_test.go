//go:build integration

package temporal_test

import (
	"context"
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

// TestTemporalExecutor_LiveDAG is a full E2E test that requires a running
// Temporal server and dag-worker.  Set TEMPORAL_HOST_PORT to point at the
// Temporal frontend (e.g. localhost:7233) and THEM_TEMPORAL_E2E=true to enable.
// Skipped unless explicitly enabled so it never runs in unit CI.
func TestTemporalExecutor_LiveDAG(t *testing.T) {
	if os.Getenv("THEM_TEMPORAL_E2E") != "true" {
		t.Skip("THEM_TEMPORAL_E2E not set — skipping live Temporal E2E")
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
			{StepID: "step-1", Type: "response", Outputs: []string{"output"}},
		},
	}
	ic := &agentgen.InvocationContext{
		TenantID:      "00000000-0000-0000-0000-000000000001",
		ApplicationID: "00000000-0000-0000-0000-000000000002",
		AgentID:       "00000000-0000-0000-0000-000000000003",
		InvocationID:  "e2e-live-dag-" + t.Name(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx, ic, plan, agentgen.PipelineVars{"input": "hello"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	t.Logf("E2E result: text=%q mt=%q", result.Text, result.MediaType)
}
