package main

import (
	"net/http/httptest"
	"testing"
)

// A2A calls from an AppFlow agent node carry X-Them-Run-Id; the runtime puts it on the invocation context
// so the agent's tool calls can be tied back to the run. It is optional (direct calls have none).
func TestParseInvocationContext_ReadsRunID(t *testing.T) {
	rt := &Runtime{}
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Them-Tenant-Id", "t1")
	req.Header.Set("X-Them-Application-Id", "a1")
	req.Header.Set("X-Them-Agent-Id", "g1")

	ic, err := rt.parseInvocationContext(req)
	if err != nil || ic.RunID != "" {
		t.Fatalf("no header: err=%v run=%q, want an empty run id", err, ic.RunID)
	}

	req.Header.Set("X-Them-Run-Id", "run-123")
	ic, err = rt.parseInvocationContext(req)
	if err != nil || ic.RunID != "run-123" {
		t.Fatalf("with header: err=%v run=%q, want run-123", err, ic.RunID)
	}
}
