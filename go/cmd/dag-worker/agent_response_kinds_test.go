package main

import (
	"strings"
	"testing"
)

// Tests for decodeAgentSendMessageResponse — Phase 1 of
// docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md. Before this phase, the response's
// parts were decoded into a hand-rolled struct with only a `Text string`
// field — a file/data/raw part silently decoded to an empty string, no
// error, nothing logged. These tests confirm all 4 real A2A part kinds
// (confirmed against the vendored SDK,
// go/vendor/github.com/a2aproject/a2a-go/v2/a2a/core.go) are now correctly
// recognized, and that the overwhelmingly common real-world case — plain
// text — is completely unaffected by this change.
//
// REVISED for Phase 2's raw-bytes File Guard fix: the function now returns
// (result, rawBytes, err) — rawBytes is non-nil only for a PartKind=="raw"
// result (confirmed via docu_writer's PDF output and a2a-stream's zip
// artifact that real agents genuinely send files this way).

// AF-DW-01: a plain text part resolves to ResponseText, PartKind empty.
func TestDecodeAgentSendMessageResponse_TextPart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [{"text": "Hello from agent!"}]}]}}
	}`)
	result, rawBytes, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResponseText != "Hello from agent!" {
		t.Errorf("ResponseText = %q, want %q", result.ResponseText, "Hello from agent!")
	}
	if result.PartKind != "" {
		t.Errorf("PartKind = %q, want empty for a text part", result.PartKind)
	}
	if rawBytes != nil {
		t.Errorf("rawBytes = %v, want nil for a text part", rawBytes)
	}
}

// AF-DW-02: a file (url) part is now recognized instead of silently dropped.
func TestDecodeAgentSendMessageResponse_FilePart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"url": "https://example.com/report.pdf", "filename": "report.pdf", "mediaType": "application/pdf"}
		]}]}}
	}`)
	result, rawBytes, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "file" {
		t.Fatalf("PartKind = %q, want %q", result.PartKind, "file")
	}
	if result.FileURL != "https://example.com/report.pdf" {
		t.Errorf("FileURL = %q, want the report URL", result.FileURL)
	}
	if result.FileName != "report.pdf" {
		t.Errorf("FileName = %q, want %q", result.FileName, "report.pdf")
	}
	if result.FileContentType != "application/pdf" {
		t.Errorf("FileContentType = %q, want %q", result.FileContentType, "application/pdf")
	}
	if result.ResponseText != "" {
		t.Errorf("ResponseText = %q, want empty for a file-only part", result.ResponseText)
	}
	if rawBytes != nil {
		t.Errorf("rawBytes = %v, want nil for a URL-kind file part", rawBytes)
	}
}

// AF-DW-03: a data (structured JSON) part is recognized, not silently dropped —
// though not yet given any special handling beyond the PartKind marker
// (explicitly out of scope per the plan doc).
func TestDecodeAgentSendMessageResponse_DataPart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"data": {"score": 0.9, "label": "positive"}}
		]}]}}
	}`)
	result, _, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "data" {
		t.Errorf("PartKind = %q, want %q", result.PartKind, "data")
	}
}

// AF-DW-04: a raw (bytes) part is recognized AND its actual bytes are
// returned — Phase 2 needs the real bytes (not just the PartKind marker) to
// run a File Guard InterceptInline scan inline, before they're discarded.
// Confirmed via docu_writer's (PDF) and a2a-stream's (zip) real source that
// agents genuinely send files this way, not just as a URL.
func TestDecodeAgentSendMessageResponse_RawPart(t *testing.T) {
	// base64 "aGVsbG8=" decodes to "hello"
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"raw": "aGVsbG8=", "filename": "output.zip", "mediaType": "application/zip"}
		]}]}}
	}`)
	result, rawBytes, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "raw" {
		t.Fatalf("PartKind = %q, want %q", result.PartKind, "raw")
	}
	if result.FileName != "output.zip" || result.FileContentType != "application/zip" {
		t.Errorf("FileName/FileContentType not captured: %+v", result)
	}
	if string(rawBytes) != "hello" {
		t.Errorf("rawBytes = %q, want the decoded bytes %q", rawBytes, "hello")
	}
}

// AF-DW-05: a JSON-RPC error response is still surfaced as a Go error,
// unaffected by the part-decoding change.
func TestDecodeAgentSendMessageResponse_RPCError(t *testing.T) {
	body := strings.NewReader(`{"error": {"message": "agent unavailable"}}`)
	_, _, err := decodeAgentSendMessageResponse(body)
	if err == nil {
		t.Fatal("expected an error for an RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "agent unavailable") {
		t.Errorf("error = %v, want it to mention the RPC error message", err)
	}
}

// AF-DW-06: an empty artifacts list resolves to a zero-value result, no error —
// matches the pre-existing behavior for "agent said nothing".
func TestDecodeAgentSendMessageResponse_NoArtifacts(t *testing.T) {
	body := strings.NewReader(`{"result": {"task": {"artifacts": []}}}`)
	result, rawBytes, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResponseText != "" || result.PartKind != "" {
		t.Errorf("result = %+v, want a zero-value result for no artifacts", result)
	}
	if rawBytes != nil {
		t.Errorf("rawBytes = %v, want nil for no artifacts", rawBytes)
	}
}

// AF-DW-07: a file/raw part in a LATER part of the SAME artifact is never
// masked by an earlier text part — REVISED from the original assumption
// (text always wins if seen first). Found live this session testing
// against a2a-stream, a real multi-artifact agent: the original
// "return on the first non-empty part" logic stopped at the first text
// chunk and never even looked at the real zip file two artifacts later,
// so File Guard silently never fired for a genuinely file-producing agent.
func TestDecodeAgentSendMessageResponse_FilePartWinsOverEarlierText(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"text": "some preceding text"},
			{"url": "https://example.com/report.pdf"}
		]}]}}
	}`)
	result, _, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "file" {
		t.Fatalf("PartKind = %q, want %q — a file part must win even if text appeared first", result.PartKind, "file")
	}
	if result.FileURL != "https://example.com/report.pdf" {
		t.Errorf("FileURL = %q, want the report URL", result.FileURL)
	}
}

// AF-DW-08: real regression test for the exact shape a2a-stream (a genuine
// multi-artifact streaming test agent) sends — confirmed via a direct probe
// against the live agent this session: many streamed text-chunk parts in
// artifact[0], an HTML report (text+filename) in artifact[1], then a real
// zip file (raw bytes) in artifact[2]. The raw part must be found and its
// bytes returned, not lost behind 17 preceding text parts.
func TestDecodeAgentSendMessageResponse_MultiArtifactStreamShape(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [
			{"parts": [{"text": "The "}, {"text": "quick "}, {"text": "fox"}]},
			{"parts": [{"text": "<html>...</html>", "filename": "stream_report.html", "mediaType": "text/html"}]},
			{"parts": [{"raw": "UEsDBA==", "filename": "stream_output.zip", "mediaType": "application/zip"}]}
		]}}
	}`)
	result, rawBytes, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "raw" {
		t.Fatalf("PartKind = %q, want %q", result.PartKind, "raw")
	}
	if result.FileName != "stream_output.zip" {
		t.Errorf("FileName = %q, want %q", result.FileName, "stream_output.zip")
	}
	if len(rawBytes) == 0 {
		t.Error("rawBytes is empty, want the decoded zip bytes")
	}
}
