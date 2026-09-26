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

// AF-DW-01: a plain text part resolves to ResponseText, PartKind empty.
func TestDecodeAgentSendMessageResponse_TextPart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [{"text": "Hello from agent!"}]}]}}
	}`)
	result, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResponseText != "Hello from agent!" {
		t.Errorf("ResponseText = %q, want %q", result.ResponseText, "Hello from agent!")
	}
	if result.PartKind != "" {
		t.Errorf("PartKind = %q, want empty for a text part", result.PartKind)
	}
}

// AF-DW-02: a file (url) part is now recognized instead of silently dropped.
func TestDecodeAgentSendMessageResponse_FilePart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"url": "https://example.com/report.pdf", "filename": "report.pdf", "mediaType": "application/pdf"}
		]}]}}
	}`)
	result, err := decodeAgentSendMessageResponse(body)
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
}

// AF-DW-03: a data (structured JSON) part is recognized, not silently dropped —
// though not yet given any special handling beyond the PartKind marker
// (explicitly out of scope per the plan doc, only the file case gets
// downstream handling in Phase 2).
func TestDecodeAgentSendMessageResponse_DataPart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"data": {"score": 0.9, "label": "positive"}}
		]}]}}
	}`)
	result, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "data" {
		t.Errorf("PartKind = %q, want %q", result.PartKind, "data")
	}
}

// AF-DW-04: a raw (bytes) part is recognized, not silently dropped.
func TestDecodeAgentSendMessageResponse_RawPart(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"raw": "aGVsbG8="}
		]}]}}
	}`)
	result, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PartKind != "raw" {
		t.Errorf("PartKind = %q, want %q", result.PartKind, "raw")
	}
}

// AF-DW-05: a JSON-RPC error response is still surfaced as a Go error,
// unaffected by the part-decoding change.
func TestDecodeAgentSendMessageResponse_RPCError(t *testing.T) {
	body := strings.NewReader(`{"error": {"message": "agent unavailable"}}`)
	_, err := decodeAgentSendMessageResponse(body)
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
	result, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResponseText != "" || result.PartKind != "" {
		t.Errorf("result = %+v, want a zero-value result for no artifacts", result)
	}
}

// AF-DW-07: text takes priority when a part somehow carries both a text
// value and other fields — matches the documented priority order
// (text > file > data > raw) so a text-first real agent response is never
// mistaken for something else.
func TestDecodeAgentSendMessageResponse_TextTakesPriorityWhenFirst(t *testing.T) {
	body := strings.NewReader(`{
		"result": {"task": {"artifacts": [{"parts": [
			{"text": "first part is text"},
			{"url": "https://example.com/ignored.pdf"}
		]}]}}
	}`)
	result, err := decodeAgentSendMessageResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResponseText != "first part is text" {
		t.Errorf("ResponseText = %q, want the first part's text", result.ResponseText)
	}
	if result.PartKind != "" {
		t.Errorf("PartKind = %q, want empty — first part was text, second part should not be reached", result.PartKind)
	}
}
