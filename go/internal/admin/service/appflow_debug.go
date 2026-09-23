package service

import (
	"context"
	"fmt"
	"time"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/debugcred"
	"github.com/aviciot/them/internal/execution"
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
}

// NewAppFlowDebugService creates an AppFlowDebugService. fernetKey decrypts
// General-mode LLM provider keys — pass the same key internal/admin's
// resolveSystemAgentRole already uses (see appflow_debug_credentials.go).
func NewAppFlowDebugService(db AppFlowDebugDAL, lc AppFlowDebugStarter, credStore AppFlowDebugCredentialStore, fernetKey []byte) *AppFlowDebugService {
	return &AppFlowDebugService{dal: db, lc: lc, credStore: credStore, fernetKey: fernetKey}
}

// DebugStartResult is returned to the caller on a successful debug start.
type DebugStartResult struct {
	RunID string
	// ExpiresAt is when this debug run will be forcibly terminated by
	// Temporal (WorkflowRunTimeout = appflow.DebugRunMaxLifetime, set in
	// Lifecycle.StartAppFlow) — surfaced so the debug panel can display the
	// bound to the user, not just enforce it silently.
	ExpiresAt time.Time
}

// Start compiles the application's latest draft definition, validates it, and
// launches an AppFlowWorkflow on the debug worker pool. userMessage is the
// seed input for the flow's entry point (mirrors the agent builder's
// "__test_input" debug param). llmOverrides maps canvas node_id -> the
// per-node LLM credential choice for THIS debug run only (never persisted to
// the app's saved Runtime settings) — see docs/APPFLOW_RUNTIME_PARAMS_PLAN.md.
// Returns ErrNotFound when the application has no draft saved yet, or the
// entry point slug doesn't exist on it.
func (s *AppFlowDebugService) Start(ctx context.Context, tenantID, appID, epSlug, userMessage string, userID int64, llmOverrides map[string]LLMOverrideInput) (DebugStartResult, error) {
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
	}
	if _, err := s.lc.StartAppFlow(ctx, handle, input, true); err != nil {
		return DebugStartResult{}, fmt.Errorf("start appflow workflow: %w", err)
	}

	return DebugStartResult{RunID: handle.RunID, ExpiresAt: startedAt.Add(appflow.DebugRunMaxLifetime)}, nil
}
