package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

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
		var n int
		if _, err := fmt.Sscanf(d, "%d", &n); err == nil && n >= 0 {
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
