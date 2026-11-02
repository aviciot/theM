package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/aviciot/them/internal/events"
)

type recordingSink struct{ got []events.Event }

func (r *recordingSink) Emit(_ context.Context, e events.Event) error {
	r.got = append(r.got, e)
	return nil
}
func (r *recordingSink) Close(context.Context) error { return nil }

// Tool-call events carry the run id (as RunID and as the TraceID) so they can be tied to the App Canvas run.
func TestEmitToolCallEvent_CarriesRunIDAsTrace(t *testing.T) {
	sink := &recordingSink{}
	e := (&Executor{}).WithEventSink(sink)

	e.emitToolCallEvent(context.Background(), ExecuteRequest{ApplicationID: "app-1", ToolName: "lookup", RunID: "run-5"}, "tenant-1", "srv", "ok", 1500*time.Millisecond)
	e.emitToolCallEvent(context.Background(), ExecuteRequest{ApplicationID: "app-1", ToolName: "lookup"}, "tenant-1", "srv", "error", time.Second)

	if len(sink.got) != 2 {
		t.Fatalf("events = %d, want 2", len(sink.got))
	}
	a, b := sink.got[0].ToolCall, sink.got[1].ToolCall
	if a.RunID != "run-5" || a.TraceID != "run-5" || a.ToolName != "lookup" || a.DurationMS != 1500 {
		t.Errorf("event with a run: %+v", a)
	}
	if b.RunID != "" || b.TraceID != "" {
		t.Errorf("a call without a run must leave RunID/TraceID empty: %+v", b)
	}
}
