package a2a

// executor.go — bridges the A2A SDK executor interface to the orchestration
// Lifecycle (Start → drain run-stream events → Release).
//
// The orchExecutorFunc is constructed per HTTP request after pre-admission succeeds.
// It captures the already-admitted handle (h) and the extracted user text.
//
// Completion model — dual channel:
//
//	Bus channel:  streams token/file events from the run-stream (Redis Streams or
//	              in-process bus). Terminal signals ("done"/"error") are published
//	              by the Temporal worker when the workflow completes.
//	wfRun.Get(): blocks until the Temporal workflow completes and returns the
//	             FinalText. Used when no token events arrive via the bus (e.g.,
//	             unit tests without a real Redis run-stream).
//
// The executor emits FinalText as a text artifact only when no token events
// arrived via the bus (to avoid duplication in streaming scenarios where the bus
// delivers incremental tokens). It then emits a completed status event to signal
// the SDK to finalise the Task.

import (
	"context"
	"encoding/json"
	"iter"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
)

// orchExecutorFunc returns an a2asrv.AgentExecutorFunc that drives one
// orchestration run. h is already admitted; this function owns calling
// Lifecycle.Start and draining the run-stream.
func (s *Server) orchExecutorFunc(h *execution.ExecutionHandle, userText string) a2asrv.AgentExecutorFunc {
	return func(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			s.runWorkflow(ctx, execCtx, h, userText, yield)
		}
	}
}

// runWorkflow starts the Temporal workflow and either drains run-stream events
// (bus path) or blocks on wfRun.Get() (blocking path), translating each to SDK
// A2A events via yield.
func (s *Server) runWorkflow(
	ctx context.Context,
	execCtx *a2asrv.ExecutorContext,
	h *execution.ExecutionHandle,
	userText string,
	yield func(a2a.Event, error) bool,
) {
	// ── 0. Yield the initial Task (SDK protocol requirement) ──────────────────
	// The SDK's taskupdate.Manager requires the first event to be a *a2a.Task.
	// NewSubmittedTask creates a Task in "submitted" state from the request message.
	// Embed the internal run_id in metadata so the frontend can poll the correct
	// /runs/{run_id}/artifacts endpoint (the SDK task ID is a different UUID).
	initialTask := a2a.NewSubmittedTask(execCtx, execCtx.Message)
	initialTask.Metadata = map[string]any{"run_id": h.RunID}
	if !yield(initialTask, nil) {
		return
	}

	orchName := h.EPConfig.OrchestratorName
	if orchName == "" {
		s.logger.Warn("a2a: entry point has no orchestrator bound",
			"app_id", h.EPConfig.AppID)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
			a2a.NewMessage(a2a.MessageRoleAgent,
				a2a.NewTextPart("entry point has no orchestrator configured"))), nil)
		return
	}

	// ── 1. Determine event source ─────────────────────────────────────────────
	// Prefer Redis run-stream (cross-process). Fall back to in-process bus.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var evCh <-chan event.Event
	if s.runStreamer != nil {
		// Redis Streams path. Subscribe returns a channel; subscribe before Start.
		ch, rsErr := runstream.StreamFromRedis(streamCtx, s.runStreamer, h.RunID, runstream.StreamerOptions{})
		if rsErr != nil {
			s.logger.Warn("a2a: runstream subscribe failed", "run_id", h.RunID, "error", rsErr)
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) //nolint:errcheck
			return
		}
		evCh = ch
	} else {
		// In-process bus fallback (unit tests / same-process orchestrator).
		busEvCh, busTermCh, unsub := s.bus.Subscribe(streamCtx, h.ContextID, 256)
		defer unsub()
		merged := make(chan event.Event, 256)
		go mergeEventChannels(streamCtx, busEvCh, busTermCh, merged)
		evCh = merged
	}

	// ── 2. Start Temporal workflow ────────────────────────────────────────────
	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: h.EPConfig.AppOrchestratorID,
		UserMessage:       domain.TextMessage(domain.RoleUser, userText),
	}
	wfRun, startErr := s.lc.Start(ctx, h, input)
	if startErr != nil {
		s.logger.Warn("a2a: start workflow failed", "run_id", h.RunID, "error", startErr)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) //nolint:errcheck
		return
	}

	s.logger.Info("a2a: workflow started",
		"run_id", h.RunID, "workflow_id", wfRun.GetID())

	// ── 3. Await completion via wfRun.Get() in parallel ───────────────────────
	// The wfRun channel delivers the final WorkflowResult (including FinalText).
	// It is used when no token events arrive via the bus (unit-test / no Redis).
	type wfDone struct {
		result temporal.WorkflowResult
		err    error
	}
	wfCh := make(chan wfDone, 1)
	go func() {
		var result temporal.WorkflowResult
		err := wfRun.Get(streamCtx, &result)
		select {
		case wfCh <- wfDone{result: result, err: err}:
		case <-streamCtx.Done():
		}
	}()

	// ── 4. Drain events / await workflow completion ───────────────────────────
	// gotTokens tracks whether any token events have arrived. When true, we skip
	// the FinalText from wfRun.Get() to avoid duplicating content.
	var gotTokens bool
	var once sync.Once
	complete := func() {
		cancel() // stop the background goroutine
	}

	for {
		select {
		case ev, ok := <-evCh:
			if !ok {
				// Bus channel closed without a terminal signal. Fall through to wfRun.Get().
				evCh = nil
				continue
			}
			switch ev.Type {
			case "token":
				gotTokens = true
				var content string
				var p map[string]json.RawMessage
				if json.Unmarshal(ev.Payload, &p) == nil {
					json.Unmarshal(p["content"], &content) //nolint:errcheck
				}
				if !yield(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(content)), nil) {
					return
				}

			case "file":
				gotTokens = true // suppress FinalText emission when bus delivers content
				var p map[string]json.RawMessage
				var filename, contentType, downloadURL string
				if json.Unmarshal(ev.Payload, &p) == nil {
					json.Unmarshal(p["filename"], &filename)        //nolint:errcheck
					json.Unmarshal(p["content_type"], &contentType) //nolint:errcheck
					json.Unmarshal(p["download_url"], &downloadURL) //nolint:errcheck
				}

				// ── Security gate intercept ──────────────────────────────────
				// When a FileInterceptor is attached and the application has
				// security scanning enabled, the file is stored in run_artifacts
				// with scan_status='pending' and a middleware job is enqueued.
				// The user receives a gated download URL pointing at our artifact
				// endpoint instead of the raw agent URL.
				if s.fileGate != nil && h.EPConfig != nil {
					gateIn := FileInterceptInput{
						DownloadURL:   downloadURL,
						FileName:      filename,
						ContentType:   contentType,
						ApplicationID: h.EPConfig.AppID,
						RunID:         h.RunID,
						SessionID:     h.SessionID,
						TenantID:      h.EPConfig.TenantID,
					}
					if gr, err := s.fileGate.Intercept(ctx, gateIn); err == nil && gr.ArtifactID != "" {
						// Replace download URL with gated artifact endpoint.
						// URL format: /api/v1/runs/{run_id}/artifacts/{artifact_id}
						downloadURL = "/api/v1/runs/" + h.RunID + "/artifacts/" + gr.ArtifactID
					}
				}

				part := a2a.NewFileURLPart(a2a.URL(downloadURL), contentType)
				part.Filename = filename
				if !yield(a2a.NewArtifactEvent(execCtx, part), nil) {
					return
				}

			case "done":
				once.Do(complete)
				// If no token events came through the bus, get FinalText from workflow result.
				if !gotTokens {
					d := <-wfCh
					if d.err == nil && d.result.FinalText != "" {
						if !yield(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(d.result.FinalText)), nil) {
							return
						}
					}
				}
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint:errcheck
				return

			case "error":
				once.Do(complete)
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) //nolint:errcheck
				return
			}

		case d := <-wfCh:
			// wfRun.Get() completed before any bus "done"/"error" signal.
			// This happens in tests without a real Redis run-stream.
			once.Do(complete)
			if d.err != nil {
				s.logger.Warn("a2a: workflow error", "run_id", h.RunID, "error", d.err)
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) //nolint:errcheck
				return
			}
			if d.result.Status == domain.RunStatusFailed {
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) //nolint:errcheck
				return
			}
			// Emit the final text as a text artifact.
			if d.result.FinalText != "" && !gotTokens {
				if !yield(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(d.result.FinalText)), nil) {
					return
				}
			}
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint:errcheck
			return

		case <-streamCtx.Done():
			return
		}
	}
}

// mergeEventChannels fans in two event channels into a single merged output.
// Closes merged when both inputs are exhausted or ctx is cancelled.
func mergeEventChannels(ctx context.Context, evCh, termCh <-chan event.Event, merged chan<- event.Event) {
	defer close(merged)
	for {
		var ev event.Event
		var ok bool
		select {
		case ev, ok = <-evCh:
			if !ok {
				evCh = nil
				if termCh == nil {
					return
				}
				continue
			}
		case ev, ok = <-termCh:
			if !ok {
				termCh = nil
				if evCh == nil {
					return
				}
				continue
			}
		case <-ctx.Done():
			return
		}
		select {
		case merged <- ev:
		case <-ctx.Done():
			return
		}
	}
}
