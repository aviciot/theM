package orchestrator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/runrecorder"
)

type toolResult struct {
	callID string
	name   string
	output json.RawMessage
	err    error
}

// buildTools converts allowed agent slugs and MCP server attachments to LLM tool definitions.
// When agents is nil or AllowedAgents is empty and no MCP servers are attached, returns nil.
// If a CardDiscoverer is wired, enriches descriptions from agent cards.
func (o *Orchestrator) buildTools(ctx context.Context) []llm.ToolDef {
	var tools []llm.ToolDef

	// Agent tools.
	if o.agents != nil && len(o.cfg.AllowedAgents) > 0 {
		for _, slug := range o.cfg.AllowedAgents {
			desc := "Invoke the " + slug + " agent."

			// Enrich description from agent card if discoverer is available.
			if o.cardDiscoverer != nil {
				card, err := o.cardDiscoverer.GetCard(ctx, slug)
				if err != nil {
					o.logger.Warn("orchestrator: card discovery failed — using static description",
						"slug", slug, "error", err)
				} else if card.Description != "" {
					desc = card.Description
				}
			}

			tools = append(tools, llm.ToolDef{
				Name:        "agent__" + slug,
				Description: desc,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{"type": "string", "description": "The input to pass to the agent"},
					},
					"required": []string{"input"},
				},
			})
		}
	}

	// MCP tools — pre-fetched ToolDefs stored on each attachment.
	for i := range o.cfg.MCPServers {
		tools = append(tools, o.cfg.MCPServers[i].ToolDefs...)
	}

	if len(tools) == 0 {
		return nil
	}
	return tools
}

// executeTools invokes all tool calls in parallel (bounded by MaxParallelTools),
// publishes results to the bus, and manages child task lifecycle.
// OD-4: parallel fan-out with semaphore.
// iter is the agentic loop iteration index — all parallel calls in one batch share the same iteration.
func (o *Orchestrator) executeTools(ctx context.Context, contextID, runID string, iter int, calls []llm.ToolCall, rctx RunContext) []toolResult {
	results := make([]toolResult, len(calls))

	// Build semaphore for concurrency control (0 = unlimited).
	var sem *semaphore.Weighted
	if o.cfg.MaxParallelTools > 0 {
		sem = semaphore.NewWeighted(int64(o.cfg.MaxParallelTools))
	}

	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, tc := range calls {
		i, tc := i, tc // capture loop variables
		go func() {
			defer wg.Done()

			// Acquire semaphore slot if bounded.
			if sem != nil {
				if acquireErr := sem.Acquire(ctx, 1); acquireErr != nil {
					results[i] = toolResult{callID: tc.ID, name: tc.Name, err: acquireErr}
					return
				}
				defer sem.Release(1)
			}

			// Route mcp__<server>__<tool> calls to them-mcp-service.
			if len(tc.Name) > 5 && tc.Name[:5] == "mcp__" {
				out, err := o.invokeMCPTool(ctx, rctx.ApplicationID, tc.Name, tc.Input)
				results[i] = toolResult{callID: tc.ID, name: tc.Name, output: out, err: err}
				if err != nil {
					o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
						"name":  tc.Name,
						"error": err.Error(),
					})
				} else {
					o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
						"name":   tc.Name,
						"output": string(out),
					})
				}
				return
			}

			slug := tc.Name
			// Strip "agent__" prefix.
			if len(slug) > 7 && slug[:7] == "agent__" {
				slug = slug[7:]
			}

			// Guard: if agents invoker is nil, fail gracefully.
			if o.agents == nil {
				results[i] = toolResult{callID: tc.ID, name: tc.Name, err: fmt.Errorf("no agent invoker configured for tool %q", tc.Name)}
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":  tc.Name,
					"error": results[i].err.Error(),
				})
				return
			}

			// Create child task row (non-fatal).
			var taskID string
			if o.taskRecorder != nil {
				var taskErr error
				taskID, taskErr = o.taskRecorder.CreateTask(ctx, rctx.TenantID, runID, contextID, slug, rctx.UserID)
				if taskErr != nil {
					o.logger.Warn("orchestrator: create task failed", "slug", slug, "error", taskErr)
				}
			}

			inputBytes, _ := json.Marshal(tc.Input)
			stepStart := time.Now()
			// Use InvokeForRunStreaming so streaming agents can emit artifacts
			// progressively via the callback while the SSE stream is still open.
			// Non-streaming agents fall back to InvokeForRun (callback is never called).
			out, err := o.agents.InvokeForRunStreaming(ctx, rctx.TenantID, rctx.ApplicationID, slug, inputBytes,
				func(filename, contentType, dataBase64 string) {
					body := &artifactBody{Filename: filename, ContentType: contentType, DataBase64: dataBase64}
					o.emitArtifactEvent(ctx, contextID, runID, rctx, body)
				},
			)
			latencyMS := time.Since(stepStart).Milliseconds()

			// Complete child task row (non-fatal).
			if o.taskRecorder != nil && taskID != "" {
				if completeErr := o.taskRecorder.CompleteTask(ctx, taskID, err == nil); completeErr != nil {
					o.logger.Warn("orchestrator: complete task failed", "task_id", taskID, "error", completeErr)
				}
			}

			// Record agent step (non-fatal).
			if o.stepRecorder != nil {
				stepStatus := "completed"
				stepErrMsg := ""
				if err != nil {
					stepStatus = "failed"
					stepErrMsg = err.Error()
				}
				if recErr := o.stepRecorder.RecordAgentStep(ctx, runID, slug, iter, inputBytes, string(out), latencyMS, stepStatus, stepErrMsg); recErr != nil {
					o.logger.Warn("orchestrator: record agent step failed", "slug", slug, "error", recErr)
				}
			}

			results[i] = toolResult{callID: tc.ID, name: tc.Name, output: out, err: err}

			if err != nil {
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":  tc.Name,
					"error": err.Error(),
				})
			} else {
				// Check if the tool result contains artifact payload(s).
				// Record each artifact and emit a "file" event (non-fatal).
				// Strip artifact keys before forwarding to the LLM — base64
				// must never appear in LLM context or event payloads.
				var ap artifactPayload
				if len(out) > 0 {
					if jsonErr := json.Unmarshal(out, &ap); jsonErr == nil {
						// Normalise: singular "artifact" + plural "artifacts" → one slice.
						bodies := ap.Artifacts
						if ap.Artifact != nil {
							bodies = append([]artifactBody{*ap.Artifact}, bodies...)
						}
						if len(bodies) > 0 {
							for i := range bodies {
								o.emitArtifactEvent(ctx, contextID, runID, rctx, &bodies[i])
							}
							// Replace out with artifact-stripped version for LLM tool_result.
							var stripped map[string]any
							if jsonErr2 := json.Unmarshal(out, &stripped); jsonErr2 == nil {
								delete(stripped, "artifact")
								delete(stripped, "artifacts")
								if clean, jsonErr3 := json.Marshal(stripped); jsonErr3 == nil {
									out = clean
								}
							}
						}
					}
				}
				o.publishJSON(ctx, contextID, runID, "tool_result", map[string]any{
					"name":   tc.Name,
					"output": string(out),
				})
			}
		}()
	}

	wg.Wait()
	return results
}

// emitArtifactEvent records a file artifact and publishes a "file" event to the
// bus. The event payload contains only metadata (no binary data, no paths).
// SECURITY: artifact data must never appear in any log line or event payload.
func (o *Orchestrator) emitArtifactEvent(ctx context.Context, contextID, runID string, rctx RunContext, body *artifactBody) {
	if o.artifactRecorder == nil {
		return
	}
	// Reject encoded input that cannot possibly decode below the 1 MiB limit.
	// This check avoids allocating a large buffer for an oversized payload.
	if len(body.DataBase64) > artifactMaxBase64Bytes {
		o.logger.Warn("orchestrator: artifact encoded input too large — skipping",
			"run_id", runID, "filename", body.Filename,
			"encoded_len", len(body.DataBase64), "max_encoded_len", artifactMaxBase64Bytes)
		o.publishJSON(ctx, contextID, runID, "error", map[string]string{
			"run_id":  runID,
			"message": "artifact exceeds 1 MiB limit: " + body.Filename,
		})
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataBase64)
	if err != nil {
		o.logger.Warn("orchestrator: artifact base64 decode failed — skipping",
			"run_id", runID, "filename", body.Filename, "error", err)
		o.publishJSON(ctx, contextID, runID, "error", map[string]string{
			"run_id":  runID,
			"message": "artifact decode failed: " + err.Error(),
		})
		return
	}

	var artifactID string
	gated := false
	// Route through security gate when available (stores artifact with scan_status='pending'
	// and enqueues an AV scan job). Falls back to plain RecordArtifact when gate is
	// disabled or not configured for this application.
	if o.fileGateInliner != nil && rctx.ApplicationID != "" {
		gatedID, gateErr := o.fileGateInliner.InterceptInlineArtifact(
			ctx, rctx.ApplicationID, runID, rctx.SessionID,
			body.Filename, body.ContentType, data,
		)
		if gateErr == nil && gatedID != "" {
			artifactID = gatedID
			gated = true
		}
	}
	if artifactID == "" {
		var recErr error
		artifactID, recErr = o.artifactRecorder.RecordArtifact(ctx, runrecorder.ArtifactInput{
			RunID:         runID,
			ApplicationID: rctx.ApplicationID,
			SessionID:     rctx.SessionID,
			Filename:      body.Filename,
			ContentType:   body.ContentType,
			Data:          data,
		})
		if recErr != nil {
			if errors.Is(recErr, runrecorder.ErrArtifactTooLarge) {
				o.logger.Warn("orchestrator: artifact too large — skipping",
					"run_id", runID, "filename", body.Filename)
				o.publishJSON(ctx, contextID, runID, "error", map[string]string{
					"run_id":  runID,
					"message": "artifact exceeds 1 MiB limit: " + body.Filename,
				})
			} else {
				o.logger.Warn("orchestrator: artifact record failed — skipping",
					"run_id", runID, "filename", body.Filename, "error", recErr)
			}
			return
		}
	}

	// Base metadata shared by all event types.
	basePayload := map[string]any{
		"artifact_id":  artifactID,
		"filename":     body.Filename,
		"content_type": body.ContentType,
		"size":         int64(len(data)),
		"run_id":       runID,
		"download_url": "/api/v1/runs/" + runID + "/artifacts/" + artifactID,
	}
	if rctx.ApplicationID != "" {
		basePayload["application_id"] = rctx.ApplicationID
	}
	if rctx.SessionID != "" {
		basePayload["session_id"] = rctx.SessionID
	}

	if gated && o.scanSubscriber != nil {
		// Scanning in progress — emit file_scanning so the frontend can show a spinner,
		// then wait for the scan result in a goroutine and emit the final event.
		o.publishJSON(ctx, contextID, runID, "file_scanning", basePayload)

		capturedContextID := contextID
		capturedRunID := runID
		capturedArtifactID := artifactID
		capturedPayload := copyMap(basePayload)
		go o.waitAndEmitScanResult(capturedContextID, capturedRunID, capturedArtifactID, capturedPayload)
		return
	}

	// Not gated or no subscriber — emit file event directly (legacy / disabled path).
	o.publishJSON(ctx, contextID, runID, "file", basePayload)
}

// waitAndEmitScanResult waits up to scanResultTimeout for the middleware worker to
// publish an artifact_scan_result, then emits "file" (clean) or "file_blocked"
// (infected/error). Uses a background context so a user disconnect does not cancel
// the goroutine — the scan continues regardless.
const scanResultTimeout = 5 * time.Minute

func (o *Orchestrator) waitAndEmitScanResult(contextID, runID, artifactID string, payload map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), scanResultTimeout)
	defer cancel()

	res, ok := o.scanSubscriber.WaitForScanResult(ctx, runID, artifactID, scanResultTimeout)
	if !ok {
		o.logger.Warn("orchestrator: scan result timeout — emitting file event without scan gate",
			"run_id", runID, "artifact_id", artifactID)
		o.publishJSON(ctx, contextID, runID, "file", payload)
		return
	}

	switch res.ScanStatus {
	case "clean", "disabled":
		o.publishJSON(ctx, contextID, runID, "file", payload)
	case "infected":
		blocked := copyMap(payload)
		blocked["threat"] = res.Threat
		o.publishJSON(ctx, contextID, runID, "file_blocked", blocked)
	default:
		// error/failed — emit the file event so the artifact is still accessible.
		o.publishJSON(ctx, contextID, runID, "file", payload)
	}
}

// copyMap shallow-copies a map[string]any so the goroutine has its own copy.
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// buildAssistantMessage builds an assistant message containing text and/or tool_use parts.
func buildAssistantMessage(text string, calls []llm.ToolCall) domain.Message {
	var parts []domain.ContentPart
	if text != "" {
		parts = append(parts, domain.ContentPart{Type: "text", Text: text})
	}
	for _, tc := range calls {
		inputJSON, _ := json.Marshal(tc.Input)
		parts = append(parts, domain.ContentPart{
			Type:      "tool_use",
			ToolUseID: tc.ID,
			ToolName:  tc.Name,
			ToolInput: inputJSON,
		})
	}
	return domain.Message{Role: domain.RoleAssistant, Parts: parts}
}

// buildToolResultMessage builds a tool result message for all completed calls.
func buildToolResultMessage(results []toolResult) domain.Message {
	var parts []domain.ContentPart
	for _, r := range results {
		var outputJSON json.RawMessage
		if r.err != nil {
			outputJSON, _ = json.Marshal(map[string]string{"error": r.err.Error()})
		} else {
			outputJSON = r.output
		}
		parts = append(parts, domain.ContentPart{
			Type:       "tool_result",
			ToolUseID:  r.callID,
			ToolName:   r.name,
			ToolResult: outputJSON,
			IsError:    r.err != nil,
		})
	}
	return domain.Message{Role: domain.RoleTool, Parts: parts}
}
