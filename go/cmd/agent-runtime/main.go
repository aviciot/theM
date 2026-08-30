// Package main is the them-agent-runtime — a generic stateless A2A agent server.
// It reads AgentSpec definitions from PostgreSQL (cached locally, TTL 60s) and
// serves any canvas-designed agent over the A2A JSON-RPC 2.0 and streaming wire
// protocol, backed by the official github.com/a2aproject/a2a-go/v2 SDK.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/temporal"
)

// agentParamEntry is the stored shape for a secret-type agent param.
type agentParamEntry struct {
	CT   string `json:"ct"`
	Hint string `json:"hint"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		logger.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		logger.Error("redis connect failed", "err", err)
		os.Exit(1)
	}

	taskRedis := cache.NewAuthRedisClient(redisCache.Client())
	cryptoKey := crypto.DeriveKey(cfg.SecretKey)

	interpBase := agentgen.NewInterpreter(
		&http.Client{Timeout: 60 * time.Second},
		&multiLLMFactory{platformKey: cfg.AnthropicAPIKey},
		cfg.AnthropicAPIKey,
	)
	if cfg.MCPServiceURL != "" {
		interpBase.WithMCPCaller(agentgen.NewHTTPMCPCaller(cfg.MCPServiceURL, &http.Client{Timeout: 30 * time.Second}))
	}

	// A2A inter-agent call support: resolve target endpoint from DB, decrypt auth token.
	a2aResolver := agentgen.NewDBAgentEndpointResolver(
		&pgxAgentEndpointQueryer{pool: database.Pool()},
		func(ct string) (string, error) { return crypto.DecryptStored(cryptoKey, ct) },
	)
	interpBase.WithA2ACaller(agentgen.NewHTTPA2ACaller(a2aResolver, &http.Client{Timeout: 5 * time.Minute}))

	rt := &Runtime{
		pool:      database.Pool(),
		cryptoKey: cryptoKey,
		taskStore: agentgen.NewRedisA2ATaskStore(taskRedis),
		hitlStore: agentgen.NewHITLStore(taskRedis),
		specCache: &specCache{entries: make(map[string]*cachedSpec)},
		logger:    logger,
		interp:    interpBase,
	}

	// When Temporal is enabled, create a TemporalExecutor so canvas agents with
	// execution_backend=="temporal" can be routed to the DAG worker.
	if cfg.TemporalEnabled {
		temporalCli, err := temporal.Connect(cfg.TemporalHostPort, logger)
		if err != nil {
			logger.Error("temporal connect failed", "err", err)
			os.Exit(1)
		}
		te := temporal.NewTemporalExecutor(temporalCli, 0, 0, logger)
		rt.temporalExecutor = te
		rt.canvasSubmitter = te
		rt.canvasSignaler = te
		rt.canvasAwaiter = te
		rt.canvasCanceler = te
		rt.canvasHITLQuerier = te
		logger.Info("temporal executor configured", "host_port", cfg.TemporalHostPort)
	}

	port := "9300"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz)
	// SDK card handler: NewStaticAgentCardHandler requires the spec to build the card.
	// We serve it via a thin wrapper that loads the spec then delegates to the SDK handler.
	r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	// A2A JSON-RPC endpoint: auth + spec + binding resolution happens here in middleware,
	// then the SDK's NewJSONRPCHandler dispatches message/send, tasks/get, tasks/cancel,
	// message/stream, tasks/resubscribe and all other A2A methods.
	r.Post("/agents/{slug}", rt.handle)

	logger.Info("them-agent-runtime starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}

// Runtime is the stateless request handler. One per process; all state in Redis/Postgres.
type Runtime struct {
	pool             *pgxpool.Pool
	cryptoKey        []byte
	taskStore        *agentgen.RedisA2ATaskStore // SDK-compatible task store backed by Redis
	hitlStore        *agentgen.HITLStore
	specCache        *specCache
	logger           *slog.Logger
	interp           *agentgen.Interpreter
	// temporalExecutor is non-nil when TEMPORAL_ENABLED=true. Canvas agents with
	// execution_backend=="temporal" are routed here; all others use LocalExecutor.
	temporalExecutor agentgen.ExecutionBackend
	// canvasSubmitter, canvasSignaler, canvasAwaiter, canvasCanceler and canvasHITLQuerier
	// are non-nil only when temporalExecutor is set.
	canvasSubmitter   temporal.CanvasSubmitter
	canvasSignaler    temporal.CanvasSignaler
	canvasAwaiter     temporal.CanvasAwaiter
	canvasCanceler    temporal.CanvasCanceler
	canvasHITLQuerier temporal.CanvasHITLQuerier
}

func (rt *Runtime) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}

// agentCard serves /.well-known/agent-card.json using the SDK's NewStaticAgentCardHandler.
func (rt *Runtime) agentCard(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	spec, err := rt.loadSpecBySlug(r.Context(), slug)
	if err != nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	card := buildSDKAgentCard(spec)
	a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
}

// handle is the A2A JSON-RPC endpoint. It resolves auth/spec/binding in our middleware,
// then creates a per-request AgentExecutor and delegates to the SDK's JSON-RPC handler.
// The SDK provides full method dispatch: message/send, tasks/get, tasks/cancel,
// message/stream, tasks/resubscribe, and push notification methods.
func (rt *Runtime) handle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	ic, err := rt.parseInvocationContext(r)
	if err != nil {
		rt.logger.Warn("agent-runtime: unauthorized request", "slug", slug, "err", err)
		writeJSONRPCError(w, nil, -32600, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Invariant 2: cross-check URL slug vs authoritative agent_id from invocation context.
	spec, err := rt.loadSpecByAgentID(r.Context(), ic.TenantID, ic.AgentID)
	if err != nil || spec.Slug != slug {
		writeJSONRPCError(w, nil, -32600, "forbidden", http.StatusForbidden)
		return
	}
	ic.Spec = spec

	binding, agentParamsJSON, err := rt.loadBinding(r.Context(), ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID)
	if err != nil {
		writeJSONRPCError(w, nil, -32600, "binding not found", http.StatusNotFound)
		return
	}

	// Invariant 1: assert binding definition_id matches spec when pinned.
	if binding.DefinitionID != nil && *binding.DefinitionID != spec.DefinitionID {
		writeJSONRPCError(w, nil, -32603, "binding stale — application must be re-published", http.StatusConflict)
		return
	}

	ic.ConfigOverrides = binding.ConfigOverrides
	ic.Policies = binding.Policies

	// Load per-app provider keys so the interpreter can prefer them over the platform env key.
	// Errors are non-fatal — the platform key fallback still works.
	ic.AppAPIKey = rt.loadAppAPIKey(r.Context(), ic.TenantID, ic.ApplicationID)

	// Resolve agent params from the binding (decrypt secrets, apply defaults).
	// ic.AgentParams is never nil — steps can safely read from it without nil checks.
	ic.AgentParams = rt.resolveAgentParams(agentParamsJSON, spec.RequiredParams)

	// Load app-level global params for HTTP app_param_ref injection.
	ic.AppGlobalParams = rt.loadAppGlobalParams(r.Context(), ic.TenantID, ic.ApplicationID)

	// Extract per-node LLM overrides from config_overrides["llm_nodes"].
	ic.NodeLLMOverrides = extractNodeLLMOverrides(binding.ConfigOverrides)

	// Build the SDK executor function. It is called by the SDK for each message/send
	// or message/stream request. The closure captures the fully-resolved InvocationContext.
	executor := a2asrv.AgentExecutorFunc(func(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return rt.executeSkill(ctx, ic, execCtx)
	})

	// NewHandler creates a RequestHandler backed by the SDK's local task manager.
	// WithTaskStore connects rt.taskStore so task state persists across in-process
	// handler instances and survives pod restarts (reads from Redis).
	inner := a2asrv.NewHandler(executor,
		a2asrv.WithLogger(rt.logger),
		a2asrv.WithTaskStore(rt.taskStore),
	)

	// HITLRequestHandler wraps the inner handler to intercept GetTask, SubscribeToTask,
	// and CancelTask for HITL tasks, polling the Temporal query handler for state sync.
	handler := &HITLRequestHandler{
		inner:      inner,
		hitlStore:  rt.hitlStore,
		querier:    rt.canvasHITLQuerier,
		awaiter:    rt.canvasAwaiter,
		canceler:   rt.canvasCanceler,
		signaler:   rt.canvasSignaler,
		taskStore:  rt.taskStore,
		logger:     rt.logger,
	}

	// NewJSONRPCHandler wraps the RequestHandler in a single POST endpoint that
	// dispatches all A2A JSON-RPC 2.0 methods, replacing our hand-rolled dispatch.
	a2asrv.NewJSONRPCHandler(handler).ServeHTTP(w, r)
}

// executeSkill runs the agent's first skill pipeline and emits SDK-compliant A2A events.
// It follows the template from agentexec.go:
//
//	Submitted → Working → ArtifactEvent → Completed  (success path)
//	Submitted → Working → Failed                     (error path)
//
// InvocationID is stamped from execCtx.TaskID — the SDK assigns this once per logical
// task and reuses it across retries, giving Temporal a stable workflow ID for re-attach.
func (rt *Runtime) executeSkill(ctx context.Context, ic *agentgen.InvocationContext, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	ic.InvocationID = string(execCtx.TaskID)
	return func(yield func(a2a.Event, error) bool) {
		// Emit Submitted only for new tasks (no prior StoredTask).
		if execCtx.StoredTask == nil {
			submitted := a2a.NewSubmittedTask(execCtx, execCtx.Message)
			if !yield(submitted, nil) {
				return
			}
		}

		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		if len(ic.Spec.Skills) == 0 {
			errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("agent has no skills"))
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) {
				return
			}
			return
		}

		// Skill selection: prefer skill matching the requested ID in message metadata,
		// fall back to first skill (single-skill agents have no skill ID in the message).
		skill := &ic.Spec.Skills[0]
		if execCtx.Message != nil {
			if requestedID, ok := execCtx.Message.Metadata["skill_id"].(string); ok && requestedID != "" {
				found := false
				for i := range ic.Spec.Skills {
					if ic.Spec.Skills[i].ID == requestedID {
						skill = &ic.Spec.Skills[i]
						found = true
						break
					}
				}
				if !found {
					errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("skill not found: "+requestedID))
					yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
					return
				}
			}
		}

		// Enforce AllowedSkillIDs policy from the binding (nil = all skills allowed).
		if len(ic.Policies.AllowedSkillIDs) > 0 {
			allowed := false
			for _, id := range ic.Policies.AllowedSkillIDs {
				if id == skill.ID {
					allowed = true
					break
				}
			}
			if !allowed {
				errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("skill not permitted by binding policy"))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
				return
			}
		}

		// Extract text and structured data from the incoming message parts.
		// text part  → vars["input"] (the raw caller message)
		// data part  → each top-level key becomes a named pipeline var
		inputText := ""
		dataVars := map[string]any{}
		if execCtx.Message != nil {
			for _, part := range execCtx.Message.Parts {
				if t := part.Text(); t != "" && inputText == "" {
					inputText = t
				} else if d := part.Data(); d != nil {
					raw, err := json.Marshal(d)
					if err == nil {
						var obj map[string]any
						if json.Unmarshal(raw, &obj) == nil {
							for k, v := range obj {
								dataVars[k] = v
							}
						}
					}
				}
			}
		}

		initial := agentgen.PipelineVars{"input": inputText}
		for k, v := range dataVars {
			initial[k] = v
		}

		if err := agentgen.ValidateLoopBodies(skill); err != nil {
				errMsg := a2a.NewMessage(a2a.MessageRoleAgent,
					a2a.NewTextPart("invalid agent definition: "+err.Error()))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
				return
			}

		plan := agentgen.CompileExecutionPlan(skill)

		// HITL detection: Temporal-based plans with a human_wait node use the async
		// submit path (no blocking on completion) when a CanvasSubmitter is available.
		// The "temporal not enabled" guard below only applies to non-HITL Temporal plans
		// because HITL only requires canvasSubmitter, not the blocking temporalExecutor.
		isHITL := ic.Spec.ExecutionBackend == "temporal" &&
			agentgen.PlanHasHumanWait(plan) &&
			rt.canvasSubmitter != nil

		// ── HITL async path ──────────────────────────────────────────────────────
		// For HITL plans we must NOT block on workflow completion (the HTTP request
		// ctx would be cancelled by a proxy timeout or client disconnect, which would
		// cancel the Temporal workflow). Instead:
		//   1. Submit the workflow on a detached background context.
		//   2. Store the workflow handle + step ID in Redis so signalHITL can route
		//      the human response back.
		//   3. Return TaskStateWorking immediately — the SDK's SubscribeToTask lets
		//      the caller reconnect later.
		if isHITL {
			// Detach from the HTTP request context so client disconnect does not
			// cancel the long-running HITL workflow.
			bgCtx, bgCancel := context.WithTimeout(context.Background(), agentgen.HITLHandleTTL)
			defer bgCancel()

			submitted, err := rt.canvasSubmitter.Submit(bgCtx, ic, plan, initial)
			if err != nil {
				rt.logger.Error("agent-runtime: HITL submit failed",
					"tenant_id", ic.TenantID,
					"agent_id", ic.AgentID,
					"err", err,
				)
				errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("failed to start HITL workflow"))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
				return
			}

			// Find the first human_wait step ID — this is what the workflow will
			// signal on when ready for human input.
			humanStepID := ""
			for _, n := range plan.Nodes {
				if n.Type == agentgen.StepHumanWait {
					humanStepID = n.StepID
					break
				}
			}

			storeCtx, storeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer storeCancel()
			if err := rt.hitlStore.Store(storeCtx, string(execCtx.TaskID), submitted.WorkflowID, submitted.RunID, ic.TenantID, humanStepID); err != nil {
				rt.logger.Warn("agent-runtime: HITL store failed (workflow is running but reconnect may not work)",
					"task_id", string(execCtx.TaskID),
					"workflow_id", submitted.WorkflowID,
					"err", err,
				)
			}

			// Return working — the caller polls via SubscribeToTask or GetTask.
			// InputRequired is emitted only when the workflow actually reaches a human_wait node
			// (detected via hitl_status query poll in HITLRequestHandler.SubscribeToTask).
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) //nolint:errcheck
			return
		}

		// ── Normal (non-HITL) execution path ────────────────────────────────────
		// Fail closed: if the agent requests Temporal but the executor is nil
		// (TEMPORAL_ENABLED=false on this pod), return a typed error rather than
		// silently falling back to Local execution.
		if ic.Spec.ExecutionBackend == "temporal" && rt.temporalExecutor == nil {
			errMsg := a2a.NewMessage(a2a.MessageRoleAgent,
				a2a.NewTextPart("execution_backend=temporal but Temporal is not enabled on this runtime"))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
			return
		}
		var backend agentgen.ExecutionBackend
		if ic.Spec.ExecutionBackend == "temporal" {
			backend = rt.temporalExecutor
		} else {
			backend = agentgen.NewLocalExecutor(rt.interp)
		}
		execResult, err := backend.Execute(ctx, ic, plan, initial)
		if err != nil {
			rt.logger.Error("agent-runtime: execution failed",
				"tenant_id", ic.TenantID,
				"application_id", ic.ApplicationID,
				"agent_id", ic.AgentID,
				"invocation_id", ic.InvocationID,
				"err", err,
			)
			errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("execution failed"))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
			return
		}

		// Emit the result as an artifact, then mark completed.
		artifactEvent := a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(execResult.Text))
		if !yield(artifactEvent, nil) {
			return
		}

		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint:errcheck
	}
}

// HITLRequestHandler wraps an inner a2asrv.RequestHandler to intercept GetTask,
// SubscribeToTask, and CancelTask for HITL tasks. Non-HITL tasks are delegated to
// the inner handler unchanged.
//
// For HITL tasks it:
//   - GetTask: polls hitl_status, syncs HITLStore state, then delegates.
//   - SubscribeToTask: polls hitl_status every ~1s while the connection is alive;
//     emits InputRequired when the workflow reaches a human_wait node; awaits the
//     final result when signalled; emits Completed. No permanent background goroutines.
//   - CancelTask: cancels the Temporal workflow, marks the handle done, then delegates.
type HITLRequestHandler struct {
	inner     a2asrv.RequestHandler
	hitlStore *agentgen.HITLStore
	querier   temporal.CanvasHITLQuerier
	awaiter   temporal.CanvasAwaiter
	canceler  temporal.CanvasCanceler
	signaler  temporal.CanvasSignaler
	taskStore *agentgen.RedisA2ATaskStore
	logger    *slog.Logger
}

// syncHITLState queries the Temporal workflow and updates HITLStore when the
// workflow has advanced (e.g., reached waiting state). Returns the updated handle
// and ok=false if no HITL handle exists for this task.
func (h *HITLRequestHandler) syncHITLState(ctx context.Context, taskID string) (agentgen.HITLHandle, bool) {
	handle, err := h.hitlStore.Get(ctx, taskID)
	if errors.Is(err, agentgen.ErrHITLNotFound) {
		return agentgen.HITLHandle{}, false
	}
	if err != nil || h.querier == nil {
		return handle, true
	}

	status, err := h.querier.QueryHITLStatus(ctx, handle.WorkflowID, handle.RunID)
	if err != nil {
		// Workflow may be terminal — mark done.
		_ = h.hitlStore.MarkDone(ctx, taskID)
		return agentgen.HITLHandle{}, false
	}

	// Sync state transitions: submitted→waiting, signalled→waiting (next step).
	if status.State == agentgen.HITLStateWaiting && handle.WaitToken != status.WaitToken {
		if uerr := h.hitlStore.UpdateWaitToken(ctx, taskID, status.WaitToken, status.StepID); uerr == nil {
			handle.State = agentgen.HITLStateWaiting
			handle.WaitToken = status.WaitToken
			handle.StepID = status.StepID
		}
	}
	return handle, true
}

// GetTask syncs HITL state before delegating to the inner handler.
func (h *HITLRequestHandler) GetTask(ctx context.Context, req *a2a.GetTaskRequest) (*a2a.Task, error) {
	h.syncHITLState(ctx, string(req.ID)) //nolint:errcheck
	return h.inner.GetTask(ctx, req)
}

// SubscribeToTask polls the Temporal hitl_status query while the connection is alive.
// Emits InputRequired when the workflow reaches a human_wait node.
// Emits Completed+Artifact when the workflow is done.
// Delegates to the inner handler for non-HITL tasks.
func (h *HITLRequestHandler) SubscribeToTask(ctx context.Context, req *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	taskID := string(req.ID)
	handle, isHITL := h.syncHITLState(ctx, taskID)
	if !isHITL || h.querier == nil {
		return h.inner.SubscribeToTask(ctx, req)
	}

	return func(yield func(a2a.Event, error) bool) {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		// Emit current state immediately when already waiting.
		if handle.State == agentgen.HITLStateWaiting {
			prompt := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("waiting for human input"))
			ev := &a2a.TaskStatusUpdateEvent{
				TaskID:    req.ID,
				ContextID: "",
				Status:    a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: prompt},
			}
			if !yield(ev, nil) {
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, qErr := h.querier.QueryHITLStatus(ctx, handle.WorkflowID, handle.RunID)
				if qErr != nil {
					// Workflow terminated — check for final result.
					if h.awaiter != nil {
						result, aErr := h.awaiter.AwaitResult(ctx, handle.WorkflowID, handle.RunID)
						if aErr == nil && result != nil {
							artEv := a2a.NewArtifactEvent(&fakeTaskInfo{id: req.ID}, a2a.NewTextPart(result.Text))
							if !yield(artEv, nil) {
								return
							}
							doneEv := &a2a.TaskStatusUpdateEvent{
								TaskID: req.ID, ContextID: "",
								Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
							}
							yield(doneEv, nil) //nolint:errcheck
						} else {
							failEv := &a2a.TaskStatusUpdateEvent{
								TaskID: req.ID, ContextID: "",
								Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
							}
							yield(failEv, nil) //nolint:errcheck
						}
					}
					_ = h.hitlStore.MarkDone(ctx, taskID)
					return
				}

				// Sync state — new human_wait occurrence.
				if status.State == agentgen.HITLStateWaiting && status.WaitToken != handle.WaitToken {
					_ = h.hitlStore.UpdateWaitToken(ctx, taskID, status.WaitToken, status.StepID)
					handle.WaitToken = status.WaitToken
					handle.StepID = status.StepID
					handle.State = agentgen.HITLStateWaiting
					prompt := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("waiting for human input"))
					ev := &a2a.TaskStatusUpdateEvent{
						TaskID: req.ID, ContextID: "",
						Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: prompt},
					}
					if !yield(ev, nil) {
						return
					}
				}
			}
		}
	}
}

// fakeTaskInfo is a minimal a2a.TaskInfoProvider used when constructing artifact events
// outside of an ExecutorContext (e.g., in SubscribeToTask reconnect path).
type fakeTaskInfo struct {
	id a2a.TaskID
}

func (f *fakeTaskInfo) TaskInfo() a2a.TaskInfo {
	return a2a.TaskInfo{TaskID: f.id}
}

// CancelTask cancels the Temporal workflow if a HITL handle exists, then delegates.
func (h *HITLRequestHandler) CancelTask(ctx context.Context, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
	taskID := string(req.ID)
	if handle, err := h.hitlStore.Get(ctx, taskID); err == nil && h.canceler != nil {
		if cerr := h.canceler.CancelWorkflow(ctx, handle.WorkflowID, handle.RunID); cerr != nil {
			h.logger.Warn("HITLRequestHandler: CancelWorkflow failed",
				"task_id", taskID, "workflow_id", handle.WorkflowID, "err", cerr)
		}
		_ = h.hitlStore.MarkDone(ctx, taskID)
	}
	return h.inner.CancelTask(ctx, req)
}

// Delegate all other methods to the inner handler.

func (h *HITLRequestHandler) ListTasks(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return h.inner.ListTasks(ctx, req)
}
func (h *HITLRequestHandler) SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return h.inner.SendMessage(ctx, req)
}
func (h *HITLRequestHandler) SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return h.inner.SendStreamingMessage(ctx, req)
}
func (h *HITLRequestHandler) GetTaskPushConfig(ctx context.Context, req *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return h.inner.GetTaskPushConfig(ctx, req)
}
func (h *HITLRequestHandler) ListTaskPushConfigs(ctx context.Context, req *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return h.inner.ListTaskPushConfigs(ctx, req)
}
func (h *HITLRequestHandler) CreateTaskPushConfig(ctx context.Context, cfg *a2a.PushConfig) (*a2a.PushConfig, error) {
	return h.inner.CreateTaskPushConfig(ctx, cfg)
}
func (h *HITLRequestHandler) DeleteTaskPushConfig(ctx context.Context, req *a2a.DeleteTaskPushConfigRequest) error {
	return h.inner.DeleteTaskPushConfig(ctx, req)
}
func (h *HITLRequestHandler) GetExtendedAgentCard(ctx context.Context, req *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return h.inner.GetExtendedAgentCard(ctx, req)
}

// compile-time check that HITLRequestHandler satisfies RequestHandler.
var _ a2asrv.RequestHandler = (*HITLRequestHandler)(nil)

// parseInvocationContext reads identity from X-Them-* headers.
// Phase 1 uses plain headers (internal Docker network only).
// Phase 3 upgrades to signed JWT (THE_M_INVOCATION_JWT_KEY).
// InvocationID is left empty here; executeSkill stamps it from execCtx.TaskID
// so retries of the same A2A task reuse the same Temporal workflow ID.
func (rt *Runtime) parseInvocationContext(r *http.Request) (*agentgen.InvocationContext, error) {
	tenantID := r.Header.Get("X-Them-Tenant-Id")
	appID := r.Header.Get("X-Them-Application-Id")
	agentID := r.Header.Get("X-Them-Agent-Id")
	bindingID := r.Header.Get("X-Them-Binding-Id")
	if tenantID == "" || appID == "" || agentID == "" {
		return nil, fmt.Errorf("missing required invocation context headers")
	}
	depth := 0
	if d := r.Header.Get("X-Them-A2A-Depth"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 0 {
			depth = n
		}
	}
	return &agentgen.InvocationContext{
		TenantID:      tenantID,
		ApplicationID: appID,
		AgentID:       agentID,
		BindingID:     bindingID,
		A2ACallDepth:  depth,
	}, nil
}

// specCache is an in-process AgentSpec cache with 60s TTL per entry.
type specCache struct {
	mu      sync.Mutex
	entries map[string]*cachedSpec
}

type cachedSpec struct {
	spec      *agentgen.AgentSpec
	expiresAt time.Time
}

// specCacheKey returns a cache key that includes the tenantID so specs from
// different tenants with coincidental agent UUIDs cannot cross-contaminate.
func specCacheKey(tenantID, agentID string) string {
	return tenantID + ":" + agentID
}

func (c *specCache) get(key string) *agentgen.AgentSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		return e.spec
	}
	return nil
}

func (c *specCache) set(key string, spec *agentgen.AgentSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cachedSpec{spec: spec, expiresAt: time.Now().Add(60 * time.Second)}
}

func (rt *Runtime) loadSpecByAgentID(ctx context.Context, tenantID, agentID string) (*agentgen.AgentSpec, error) {
	key := specCacheKey(tenantID, agentID)
	if spec := rt.specCache.get(key); spec != nil {
		return spec, nil
	}
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE s.agent_id = $1::uuid AND a.tenant_id = $2::uuid`, agentID, tenantID)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	rt.specCache.set(key, &spec)
	return &spec, nil
}

// loadAppAPIKey fetches and decrypts the provider_keys for the given application.
// Returns a map of provider→plaintext key (e.g. "anthropic"→"sk-ant-...").
// Returns an empty map on any error — callers fall back to the platform key.
// The decrypted keys are never logged. The tenant_id predicate prevents cross-tenant key reads.
func (rt *Runtime) loadAppAPIKey(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}

	// Try new structured format {"anthropic": {"ct": "...", "hint": "XXXX"}}.
	type entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var structured map[string]entry
	if err := json.Unmarshal(raw, &structured); err == nil {
		out := make(map[string]string, len(structured))
		for provider, e := range structured {
			if e.CT == "" && e.Hint == "" {
				continue
			}
			plain, err := crypto.DecryptStored(rt.cryptoKey, e.CT)
			if err != nil {
				// Legacy plaintext row (written before encryption): use CT directly.
				// This handles the migration window until keys are re-encrypted.
				if len(e.CT) > 6 && e.CT[:6] == "plain:" {
					out[provider] = e.CT[6:]
					continue
				}
				// Decryption failed for an encrypted entry — likely a key rotation mismatch.
				// Log at warn so operators can detect and re-save affected keys.
				slog.Warn("agent-runtime: provider key decryption failed; falling back to platform key",
					"app_id", appID, "provider", provider, "err", err)
				continue
			}
			out[provider] = plain
		}
		if len(out) > 0 {
			return out
		}
	}

	// Legacy flat format {"anthropic": "sk-ant-..."} — plaintext, pre-encryption.
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(flat))
	for provider, v := range flat {
		if v != "" {
			out[provider] = v
		}
	}
	return out
}

// loadAppGlobalParams fetches and decrypts app_params for the given application.
// Returns a name→plaintext map. Non-fatal: returns an empty map on any error.
// The decrypted values are never logged. The tenant_id predicate prevents cross-tenant reads.
func (rt *Runtime) loadAppGlobalParams(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}
	return decodeAppGlobalParams(raw, rt.cryptoKey, appID)
}

// decodeAppGlobalParams parses and decrypts the raw app_params JSONB blob.
// Exported for testing. Returns an empty map (never nil) on any decode error.
func decodeAppGlobalParams(raw []byte, cryptoKey []byte, appID string) map[string]string {
	type secretEntry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(top))
	for name, valRaw := range top {
		var entry secretEntry
		if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
			// Test/dev mode: service stores "plain:<plaintext>" when no crypto key is configured.
			if len(entry.CT) > 6 && entry.CT[:6] == "plain:" {
				out[name] = entry.CT[6:]
				continue
			}
			plain, err := crypto.DecryptStored(cryptoKey, entry.CT)
			if err != nil {
				slog.Warn("agent-runtime: app global param decryption failed",
					"app_id", appID, "name", name)
				continue
			}
			out[name] = plain
			continue
		}
		var s string
		if json.Unmarshal(valRaw, &s) == nil && s != "" {
			out[name] = s
		}
	}
	return out
}

func (rt *Runtime) loadSpecBySlug(ctx context.Context, slug string) (*agentgen.AgentSpec, error) {
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE a.slug = $1 AND a.enabled = true`, slug)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec by slug: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	// Note: we cannot pre-populate the agent-ID cache here because loadSpecBySlug
	// has no tenantID, and cache keys are tenant-scoped (specCacheKey).
	return &spec, nil
}

// extractNodeLLMOverrides reads the llm_nodes sub-map from config_overrides and
// returns a map of node_id → NodeLLMOverride. Safe to call with a nil map.
func extractNodeLLMOverrides(overrides map[string]any) map[string]agentgen.NodeLLMOverride {
	out := make(map[string]agentgen.NodeLLMOverride)
	if overrides == nil {
		return out
	}
	raw, ok := overrides["llm_nodes"]
	if !ok {
		return out
	}
	nodes, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for nodeID, v := range nodes {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := m["provider"].(string)
		model, _ := m["model"].(string)
		if provider != "" || model != "" {
			out[nodeID] = agentgen.NodeLLMOverride{Provider: provider, Model: model}
		}
	}
	return out
}

func (rt *Runtime) loadBinding(ctx context.Context, tenantID, appID, agentID, bindingID string) (*agentgen.AppAgentBinding, []byte, error) {
	var (
		query string
		args  []any
	)
	// Both query paths JOIN applications to assert tenant ownership and enforce all
	// four caller-supplied IDs. Without the applicationID + agentID predicates in the
	// bindingID path, a caller within the same tenant could supply a valid binding UUID
	// belonging to a different application or agent and bypass the ownership check.
	if bindingID != "" {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.id = $1::uuid
		            AND b.application_id = $2::uuid
		            AND b.agent_id = $3::uuid
		            AND a.tenant_id = $4::uuid`
		args = []any{bindingID, appID, agentID, tenantID}
	} else {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid AND a.tenant_id = $3::uuid`
		args = []any{appID, agentID, tenantID}
	}

	row := rt.pool.QueryRow(ctx, query, args...)
	var (
		id, appIDDB, agentIDDB string
		defID                  *string
		credJSON               []byte // selected but unused — column retained for compat
		cfgJSON, polJSON       []byte
		agentParamsJSON        []byte
	)
	if err := row.Scan(&id, &appIDDB, &agentIDDB, &defID, &credJSON, &cfgJSON, &polJSON, &agentParamsJSON); err != nil {
		return nil, nil, fmt.Errorf("load binding: %w", err)
	}
	_ = credJSON

	var overrides map[string]any
	_ = json.Unmarshal(cfgJSON, &overrides)
	var policies agentgen.InvocationPolicies
	_ = json.Unmarshal(polJSON, &policies)

	return &agentgen.AppAgentBinding{
		ID:              id,
		ApplicationID:   appIDDB,
		AgentID:         agentIDDB,
		DefinitionID:    defID,
		ConfigOverrides: overrides,
		Policies:        policies,
	}, agentParamsJSON, nil
}

// resolveAgentParams decrypts secret-type params and returns a plaintext map.
// Params absent from the stored JSON fall back to their declared default.
// Decryption failures are logged at Warn (key name only) and the param is omitted.
func (rt *Runtime) resolveAgentParams(raw []byte, decls []agentgen.AgentParamSpec) map[string]string {
	out := make(map[string]string, len(decls))
	if len(decls) == 0 {
		return out
	}

	var stored map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &stored)
	}

	for _, decl := range decls {
		rawVal, exists := stored[decl.Key]
		if !exists {
			if decl.DefaultValue != "" {
				out[decl.Key] = decl.DefaultValue
			}
			continue
		}

		if decl.Type == "secret" {
			var entry agentParamEntry
			if json.Unmarshal(rawVal, &entry) == nil && entry.CT != "" {
				plain, err := crypto.DecryptStored(rt.cryptoKey, entry.CT)
				if err != nil {
					rt.logger.Warn("agent-runtime: agent param decryption failed",
						"key", decl.Key)
					continue
				}
				out[decl.Key] = plain
			}
		} else {
			var s string
			if json.Unmarshal(rawVal, &s) == nil {
				out[decl.Key] = s
			}
		}
	}
	return out
}

// buildSDKAgentCard constructs a proper a2a.AgentCard from the AgentSpec.
// It sets InputModes and OutputModes per-skill, populates SupportedInterfaces
// (the SDK v2.5 replacement for the deprecated URL field), and uses value-typed
// AgentCapabilities as required by the struct definition.
func buildSDKAgentCard(spec *agentgen.AgentSpec) *a2a.AgentCard {
	skills := make([]a2a.AgentSkill, len(spec.Skills))
	for i, sk := range spec.Skills {
		skills[i] = a2a.AgentSkill{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
			InputModes:  []string{"text/plain", "application/json"},
			OutputModes: []string{"text/plain"},
		}
	}

	agentURL := fmt.Sprintf("http://them-agent-runtime:9300/agents/%s", spec.Slug)

	return &a2a.AgentCard{
		Name:        spec.Card.Name,
		Description: spec.Card.Description,
		Version:     spec.Card.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(agentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         spec.Card.Capabilities.Streaming,
			PushNotifications: spec.Card.Capabilities.PushNotifications,
		},
		Skills: skills,
	}
}

// writeJSONRPCError writes a JSON-RPC 2.0 error response before the SDK handler is reached
// (i.e., during our auth/spec/binding middleware phase).
func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// multiLLMFactory routes to the correct provider implementation.
// Currently only "anthropic" is fully implemented; other providers return a clear error.
type multiLLMFactory struct {
	platformKey string
}

func (f *multiLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for provider %q — set a key in App Runtime", provider)
	}
	switch provider {
	case "anthropic", "":
		p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
		return &anthropicProviderAdapter{p: p}, nil
	default:
		return nil, fmt.Errorf("provider %q is not yet supported in the agent runtime; only 'anthropic' is available", provider)
	}
}

// anthropicProviderAdapter adapts llm.AnthropicProvider to agentgen.LLMProvider.
// It calls Provider.Stream with a single user message and collects text deltas.
type anthropicProviderAdapter struct {
	p *llm.AnthropicProvider
}

func (a *anthropicProviderAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msgs := []domain.Message{
		{
			Role:  domain.RoleUser,
			Parts: []domain.ContentPart{{Type: "text", Text: userPrompt}},
		},
	}
	opts := llm.Options{SystemPrompt: systemPrompt}
	ch, err := a.p.Stream(ctx, msgs, nil, opts)
	if err != nil {
		return "", fmt.Errorf("LLM stream start: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Delta)
		case "error":
			return "", fmt.Errorf("LLM stream error: %w", ev.Error)
		}
	}
	return sb.String(), nil
}

var _ agentgen.LLMProvider = (*anthropicProviderAdapter)(nil)
var _ agentgen.LLMFactory = (*multiLLMFactory)(nil)

// pgxAgentEndpointQueryer implements agentgen.AgentEndpointQueryer using pgxpool.
// Returns endpoint_url and auth_token_encrypted for one agent by tenant+slug.
type pgxAgentEndpointQueryer struct {
	pool *pgxpool.Pool
}

type pgxSingleRow struct{ row interface{ Scan(...any) error } }

func (r pgxSingleRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

func (q *pgxAgentEndpointQueryer) QueryAgentEndpoint(ctx context.Context, tenantID, agentSlug string) agentgen.AgentEndpointRow {
	row := q.pool.QueryRow(ctx,
		`SELECT COALESCE(endpoint_url,''), COALESCE(auth_token_encrypted,'')
		   FROM them.agents
		  WHERE slug = $1 AND tenant_id = $2::uuid AND enabled = true`,
		agentSlug, tenantID)
	return pgxSingleRow{row: row}
}

var _ agentgen.AgentEndpointQueryer = (*pgxAgentEndpointQueryer)(nil)
