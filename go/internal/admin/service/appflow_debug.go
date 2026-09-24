package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/debugcred"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/registry"
)

// AppFlowDebugStarter is the minimal execution.Lifecycle surface this service
// needs — defined here so tests can inject a fake without a live Temporal
// client (docs/APP_CANVAS_DEBUG_PLAN.md Phase 5).
type AppFlowDebugStarter interface {
	AdmitDebug(ctx context.Context, tenantID, appSlug, epSlug string, userID int64) (*execution.ExecutionHandle, error)
	StartAppFlow(ctx context.Context, h *execution.ExecutionHandle, input appflow.AppFlowWorkflowInput, debug bool) (temporalclient.WorkflowRun, error)
}

// AppFlowDebugDAL is the minimal DAL surface this service needs — defined
// here (rather than depending on the full ~80-method service.Dal interface,
// or the concrete *dal.DB) so tests can inject a small hand-rolled fake
// without reproducing dal.DB's SQL row-scan shapes.
type AppFlowDebugDAL interface {
	GetApplication(ctx context.Context, tenantID, id string) (dal.Application, error)
	GetLatestDraftDefinition(ctx context.Context, tenantID, appID string) (dal.AppDefinition, error)
	// AgentExists backs resolveDraftAgentIDs' live agent lookup — same check
	// PublishDefinition uses before trusting a resolved component_definitions
	// row also has a matching agents row.
	AgentExists(ctx context.Context, id string) (bool, error)
	// GetRunDetail backs GetResult's structured, LLM-readable debug summary —
	// the exact same per-node status/output/error/timing data the human
	// inspector already reads, just reshaped with an up-front verdict.
	GetRunDetail(ctx context.Context, tenantID, runID string) (dal.RunDetail, error)
	AppFlowDebugCredentialDAL
}

// AppFlowDebugCredentialStore is the minimal debugcred.Store surface this
// service needs to write per-node overrides — defined here (not the concrete
// *debugcred.Store) so tests can inject a fake without a live Redis.
type AppFlowDebugCredentialStore interface {
	Set(ctx context.Context, tenantID, runID, nodeID string, ov debugcred.Override) error
}

// AppFlowDebugService starts a debug run of an application's unpublished draft
// canvas definition — never the published active_definition_id. Debug is an
// opt-in, per-session toggle: it does not require publishing first, and always
// dispatches to the isolated debug Temporal task queue (Lifecycle.StartAppFlow
// with debug=true handles that routing).
type AppFlowDebugService struct {
	dal       AppFlowDebugDAL
	lc        AppFlowDebugStarter
	credStore AppFlowDebugCredentialStore
	fernetKey []byte
	// registry resolves agent instance_ids that have no _resolved_agent_ids
	// stamp yet (i.e. the draft has never been published) — see
	// resolveDraftAgentIDs. nil is tolerated (tests, or a deployment with no
	// registry wired) — an unpublished draft with agent nodes will then still
	// report unresolved_agent, same as before this field existed.
	registry RegistryResolver
}

// NewAppFlowDebugService creates an AppFlowDebugService. fernetKey decrypts
// General-mode LLM provider keys — pass the same key internal/admin's
// resolveSystemAgentRole already uses (see appflow_debug_credentials.go).
// reg is the same RegistryResolver DefinitionService uses to resolve agent
// components at publish time — passing nil disables live agent resolution
// for debug (unpublished drafts with agent nodes will report unresolved_agent).
func NewAppFlowDebugService(db AppFlowDebugDAL, lc AppFlowDebugStarter, credStore AppFlowDebugCredentialStore, fernetKey []byte, reg RegistryResolver) *AppFlowDebugService {
	return &AppFlowDebugService{dal: db, lc: lc, credStore: credStore, fernetKey: fernetKey, registry: reg}
}

// DebugStartResult is returned to the caller on a successful debug start.
type DebugStartResult struct {
	RunID string
	// ExpiresAt is when this debug run will be forcibly terminated by
	// Temporal (WorkflowRunTimeout = appflow.DebugRunMaxLifetime, set in
	// Lifecycle.StartAppFlow) — surfaced so the debug panel can display the
	// bound to the user, not just enforce it silently.
	ExpiresAt time.Time
	// WorkflowID is the Temporal workflow ID for this run
	// (appflow.WorkflowIDForRun's deterministic "appflow:{tenant}:{run}"
	// format) — surfaced so the frontend can deep-link straight to this
	// run in the Temporal Web UI without duplicating that ID format
	// client-side.
	WorkflowID string
}

// Start compiles the application's latest draft definition, validates it, and
// launches an AppFlowWorkflow on the debug worker pool. userMessage is the
// seed input for the flow's entry point (mirrors the agent builder's
// "__test_input" debug param). llmOverrides maps canvas node_id -> the
// per-node LLM credential choice for THIS debug run only (never persisted to
// the app's saved Runtime settings) — see docs/APPFLOW_RUNTIME_PARAMS_PLAN.md.
// Returns ErrNotFound when the application has no draft saved yet, or the
// entry point slug doesn't exist on it.
func (s *AppFlowDebugService) Start(ctx context.Context, tenantID, appID, epSlug, userMessage string, userID int64, llmOverrides map[string]LLMOverrideInput, stepMode bool) (DebugStartResult, error) {
	app, err := s.dal.GetApplication(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return DebugStartResult{}, ErrNotFound
		}
		return DebugStartResult{}, fmt.Errorf("get application: %w", err)
	}

	epFound := false
	for _, ep := range app.EntryPoints {
		if ep.Slug == epSlug {
			epFound = true
			break
		}
	}
	if !epFound {
		return DebugStartResult{}, ErrNotFound
	}

	draft, err := s.dal.GetLatestDraftDefinition(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return DebugStartResult{}, unprocessable("no draft definition saved for this application yet")
		}
		return DebugStartResult{}, fmt.Errorf("get latest draft definition: %w", err)
	}

	agentByInstanceID, err := appflow.ResolveAgentByInstanceID(draft.Definition)
	if err != nil {
		return DebugStartResult{}, unprocessable(fmt.Sprintf("resolve agents: %v", err))
	}
	// A draft that has never been published has no _resolved_agent_ids stamp
	// (that's only written by PublishDefinition) — resolve any agent-kind
	// component live, the same server-side registry lookup publish uses, so
	// debug never requires publishing first (docs/APP_CANVAS_DEBUG_PLAN.md's
	// "known limitation, found while writing Phase 5's service-layer tests").
	if err := s.resolveDraftAgentIDs(ctx, tenantID, draft.Definition, agentByInstanceID); err != nil {
		return DebugStartResult{}, unprocessable(fmt.Sprintf("resolve agents: %v", err))
	}
	spec, err := appflow.Compile(draft.Definition, agentByInstanceID)
	if err != nil {
		return DebugStartResult{}, unprocessable(fmt.Sprintf("compile: %v", err))
	}
	if errs := appflow.Validate(spec); len(errs) > 0 {
		return DebugStartResult{}, unprocessable(fmt.Sprintf("validate: %v", errs[0]))
	}

	var epFlow *appflow.EPFlow
	for i := range spec.EntryPoints {
		if spec.EntryPoints[i].Slug == epSlug {
			epFlow = &spec.EntryPoints[i]
			break
		}
	}
	if epFlow == nil {
		return DebugStartResult{}, unprocessable(fmt.Sprintf("no entry point %q in draft definition", epSlug))
	}
	singleEPSpec := &appflow.AppFlowSpec{
		ExecutionBackend: spec.ExecutionBackend,
		EntryPoints:      []appflow.EPFlow{*epFlow},
	}

	// Validate + resolve every node's llm_credential requirement BEFORE
	// admitting a run — a validation failure here must never consume a debug
	// run slot or start a Temporal workflow (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md
	// requirement: "validate required settings server-side before starting
	// the run"). Every llm-kind node needs an entry in llmOverrides — there is
	// no "fall through to the app's Runtime settings" default for a required
	// param left unset, per that same plan's explicit resolution.
	nodeIDs := llmCredentialNodeIDs(singleEPSpec)
	resolved := make(map[string]debugcred.Override, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		in, ok := llmOverrides[nodeID]
		if !ok {
			return DebugStartResult{}, unprocessable(fmt.Sprintf("node %q requires an llm_overrides entry (provider/model/key for this debug run)", nodeID))
		}
		ov, err := resolveLLMOverride(ctx, s.dal, s.fernetKey, tenantID, in)
		if err != nil {
			return DebugStartResult{}, err
		}
		resolved[nodeID] = ov
	}

	handle, err := s.lc.AdmitDebug(ctx, tenantID, app.Slug, epSlug, userID)
	if err != nil {
		return DebugStartResult{}, fmt.Errorf("admit debug run: %w", err)
	}

	// Write every resolved override AFTER AdmitDebug gives us the real run_id
	// — the store key is (tenant_id, run_id, node_id), and run_id doesn't
	// exist before this point. handle.EPConfig.TenantID is server-derived via
	// AdmitDebug's own epconfig load, not the caller-supplied tenantID param
	// (same value in practice, but this is the authoritative source).
	for nodeID, ov := range resolved {
		ov.UserID = userID
		if err := s.credStore.Set(ctx, handle.EPConfig.TenantID, handle.RunID, nodeID, ov); err != nil {
			return DebugStartResult{}, fmt.Errorf("store debug credential for node %q: %w", nodeID, err)
		}
	}

	startedAt := time.Now()
	input := appflow.AppFlowWorkflowInput{
		Spec:        singleEPSpec,
		UserMessage: userMessage,
		StepMode:    stepMode,
	}
	if _, err := s.lc.StartAppFlow(ctx, handle, input, true); err != nil {
		return DebugStartResult{}, fmt.Errorf("start appflow workflow: %w", err)
	}

	return DebugStartResult{
		RunID:      handle.RunID,
		ExpiresAt:  startedAt.Add(appflow.DebugRunMaxLifetime),
		WorkflowID: appflow.WorkflowIDForRun(handle.EPConfig.TenantID, handle.RunID),
	}, nil
}

// DebugStepResult is one node's outcome in a debug run, in execution order —
// the building block of DebugResultSummary.NodeResults. Deliberately a flat,
// self-describing shape (no nested run_steps SQL types) so it reads the same
// whether the consumer is a human-facing UI or an LLM assistant helping a
// user debug their own app (docs/PLATFORM_AS_TENANT_PLAN.md Phase 6 —
// "smart debug log" request): a coding agent building an AppFlow app via a
// future MCP tool needs to read exactly this — which node, what it did,
// what it produced or failed with — without inferring anything from a
// human-oriented color/icon scheme.
type DebugStepResult struct {
	NodeID    string `json:"node_id"`
	NodeKind  string `json:"node_kind"`
	Status    string `json:"status"` // "completed" | "failed" | "running"
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	LatencyMS *int64 `json:"latency_ms,omitempty"`
}

// DebugResultSummary is the structured result of a finished (or in-progress)
// debug run — a single up-front verdict plus the full ordered node list, so
// a caller never has to scan every step itself to answer "did this work,
// and if not, where." Ok is true only when every node in NodeResults
// completed successfully; FailedNodeID/FailedError are set together, only
// when Ok is false and a node actually reported an error (a run that never
// even reached its first node has Ok=false with both left empty).
type DebugResultSummary struct {
	RunID        string            `json:"run_id"`
	Ok           bool              `json:"ok"`
	FailedNodeID string            `json:"failed_node_id,omitempty"`
	FailedError  string            `json:"failed_error,omitempty"`
	NodeResults  []DebugStepResult `json:"node_results"`
}

// GetResult builds a DebugResultSummary for a finished or in-progress debug
// run — the same underlying them.run_steps data the human inspector already
// reads (via GetRunDetail), reshaped with an up-front pass/fail verdict so a
// caller (human or LLM) never has to scan every step to answer "what broke."
// Returns ErrNotFound when the run doesn't exist or belongs to another
// tenant (GetRunDetail is already tenant-scoped).
func (s *AppFlowDebugService) GetResult(ctx context.Context, tenantID, runID string) (DebugResultSummary, error) {
	detail, err := s.dal.GetRunDetail(ctx, tenantID, runID)
	if err != nil {
		if dal.IsNoRows(err) {
			return DebugResultSummary{}, ErrNotFound
		}
		return DebugResultSummary{}, fmt.Errorf("get run detail: %w", err)
	}

	summary := DebugResultSummary{
		RunID:       runID,
		Ok:          true,
		NodeResults: make([]DebugStepResult, 0, len(detail.Steps)),
	}
	for _, step := range detail.Steps {
		sr := DebugStepResult{
			NodeID:    step.NodeID,
			NodeKind:  step.NodeKind,
			Status:    step.Status,
			Output:    step.Output,
			Error:     step.Error,
			LatencyMS: step.LatencyMS,
		}
		summary.NodeResults = append(summary.NodeResults, sr)
		if step.Status == "failed" {
			summary.Ok = false
			if summary.FailedNodeID == "" {
				summary.FailedNodeID = step.NodeID
				summary.FailedError = step.Error
			}
		}
	}
	return summary, nil
}

// resolveDraftAgentIDs fills agentByInstanceID with a live registry lookup for
// every agent-kind component in defJSON that ResolveAgentByInstanceID's
// publish-time stamp didn't already cover (i.e. this draft has never been
// published, or was edited since). Mutates agentByInstanceID in place;
// existing entries are never overwritten, so a stamp from a real prior
// publish still wins if present.
//
// This intentionally reuses the exact same server-side lookup
// (RegistryResolver.ResolveForPublish) that PublishDefinition uses to build
// _resolved_agent_ids — never a client-supplied ID — so a debug run gets the
// identical tamper-proof guarantee a published run has, just computed now
// instead of at a publish that may never happen.
func (s *AppFlowDebugService) resolveDraftAgentIDs(ctx context.Context, tenantID string, defJSON []byte, agentByInstanceID map[string]string) error {
	if s.registry == nil {
		return nil
	}
	var doc struct {
		Components []componentInstance `json:"components"`
	}
	if err := json.Unmarshal(defJSON, &doc); err != nil {
		return fmt.Errorf("parse definition: %w", err)
	}
	for _, comp := range doc.Components {
		if comp.DefinitionRef.Kind != registry.KindAgent {
			continue
		}
		if _, already := agentByInstanceID[comp.InstanceID]; already {
			continue
		}
		cd, err := s.registry.ResolveForPublish(ctx, tenantID, comp.DefinitionRef, comp.DefinitionID)
		if err != nil {
			// Leave unresolved — appflow.Validate will report unresolved_agent
			// with a clear message, same as an actually-missing agent today.
			continue
		}
		exists, err := s.dal.AgentExists(ctx, cd.ID)
		if err != nil {
			return fmt.Errorf("check agent %q: %w", comp.InstanceID, err)
		}
		if !exists {
			continue
		}
		agentByInstanceID[comp.InstanceID] = cd.ID
	}
	return nil
}
