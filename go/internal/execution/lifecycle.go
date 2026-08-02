package execution

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
)

// RunCreator is the minimal recorder interface that Lifecycle requires.
// The production implementation is *runrecorder.Recorder.
// Defined here so that tests can inject a fake without needing a live DB.
type RunCreator interface {
	CreateRun(ctx context.Context, run domain.Run) error
	// UpdateRunStatus transitions a run to the given status. errMsg is the
	// failure reason for failed runs; use "" for non-error transitions.
	// Called by Start (admitted → running) and Release (admitted → failed).
	UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error
}

// Lifecycle executes the shared admission-and-run-start pipeline used by the WS,
// SSE, and A2A protocol handlers. It is constructed once at server startup and
// shared across all handlers via dependency injection.
//
// Callers MUST subscribe to the event bus between Admit and Start to guarantee
// that no event emitted by the workflow is missed (bootstrap ordering invariant).
type Lifecycle struct {
	auth     transport.Authenticator
	epLoader transport.EPConfigLoader
	gate     transport.GateStore
	sessions transport.SessionStore
	recorder RunCreator
	temporal transport.TemporalClientExecutor
	logger   *slog.Logger
}

// NewLifecycle constructs a production Lifecycle. epLoader, gateStore, sessions,
// recorder, and temporal are required and must not be nil; the function panics if
// any of them is nil. auth may be nil only if all entry points are public-mode.
// logger may be nil (falls back to slog.Default()).
func NewLifecycle(
	auth transport.Authenticator,
	epLoader transport.EPConfigLoader,
	gateStore transport.GateStore,
	sessions transport.SessionStore,
	recorder *runrecorder.Recorder,
	temporal transport.TemporalClientExecutor,
	logger *slog.Logger,
) *Lifecycle {
	switch {
	case epLoader == nil:
		panic("execution.NewLifecycle: epLoader must not be nil")
	case gateStore == nil:
		panic("execution.NewLifecycle: gateStore must not be nil")
	case sessions == nil:
		panic("execution.NewLifecycle: sessions must not be nil")
	case recorder == nil:
		panic("execution.NewLifecycle: recorder must not be nil")
	case temporal == nil:
		panic("execution.NewLifecycle: temporal must not be nil")
	}
	return newLifecycle(auth, epLoader, gateStore, sessions, recorder, temporal, logger)
}

// NewLifecycleWithRecorder constructs a Lifecycle using any RunCreator.
// Intended for tests that inject a fake recorder without a live database.
func NewLifecycleWithRecorder(
	auth transport.Authenticator,
	epLoader transport.EPConfigLoader,
	gateStore transport.GateStore,
	sessions transport.SessionStore,
	recorder RunCreator,
	temporal transport.TemporalClientExecutor,
	logger *slog.Logger,
) *Lifecycle {
	return newLifecycle(auth, epLoader, gateStore, sessions, recorder, temporal, logger)
}

func newLifecycle(
	auth transport.Authenticator,
	epLoader transport.EPConfigLoader,
	gateStore transport.GateStore,
	sessions transport.SessionStore,
	recorder RunCreator,
	temporal transport.TemporalClientExecutor,
	logger *slog.Logger,
) *Lifecycle {
	if logger == nil {
		logger = slog.Default()
	}
	return &Lifecycle{
		auth:     auth,
		epLoader: epLoader,
		gate:     gateStore,
		sessions: sessions,
		recorder: recorder,
		temporal: temporal,
		logger:   logger,
	}
}

// Admit runs the full admission pipeline:
//
//	tryAuthenticate → epLoader.Load → CheckAccess → gate.Check
//	→ session.Register → gate.Confirm → recorder.CreateRun
//
// On success it returns a handle the caller uses to subscribe to the run-stream
// before calling Start. All IDs in the handle are UUID v4.
//
// On failure it returns a *AdmitError — never a raw internal error string.
// All internal cleanup (gate.Rollback, etc.) is performed before returning.
func (lc *Lifecycle) Admit(ctx context.Context, req ExecutionRequest) (*ExecutionHandle, error) {
	// ── 1. Resolve token info ─────────────────────────────────────────────────
	tokenInfo := req.TokenInfo
	if tokenInfo == nil && req.RawToken != "" && lc.auth != nil {
		var authErr error
		tokenInfo, authErr = lc.auth.Validate(ctx, req.RawToken)
		if authErr != nil {
			lc.logger.Debug("execution: token validation failed", "ep_slug", req.EPSlug, "error", authErr)
			// Fall through: CheckAccess enforces token requirement per EP policy.
		}
	}

	// ── 2. EPConfig resolution ────────────────────────────────────────────────
	if lc.epLoader == nil {
		lc.logger.Warn("execution: no ep loader configured", "ep_slug", req.EPSlug)
		return nil, admitErr(AdmitErrInternal)
	}
	resolvedCfg, err := lc.epLoader.Load(ctx, req.EPSlug)
	if err != nil {
		if errors.Is(err, epconfig.ErrNotFound) {
			return nil, admitErr(AdmitErrNotFound)
		}
		lc.logger.Warn("execution: epconfig load failed", "ep_slug", req.EPSlug, "error", err)
		return nil, admitErr(AdmitErrDBUnavailable)
	}

	// ── 3. Voice EP check ────────────────────────────────────────────────────
	// Voice EPs require STT/TTS providers not available in the text orchestration
	// path. Return 501 before any gate or session resources are allocated.
	if resolvedCfg.EPType == "voice" {
		return nil, admitErr(AdmitErrNotImplemented)
	}

	// ── 4. Access mode enforcement ────────────────────────────────────────────
	// Token EP + no token presented → 401.
	if resolvedCfg.AccessMode == epconfig.AccessModeToken && req.RawToken == "" {
		return nil, admitErr(AdmitErrUnauthorized)
	}
	// Token EP + token presented but invalid/unknown → 401.
	if resolvedCfg.AccessMode == epconfig.AccessModeToken && tokenInfo == nil {
		return nil, admitErr(AdmitErrUnauthorized)
	}

	// ── 5. CheckAccess (EP/App enabled, blocked users/tokens) ─────────────────
	tokenHash := transport.TokenHash(req.RawToken)
	userID := int64(0)
	if tokenInfo != nil {
		userID = tokenInfo.TokenID
	}
	if checkErr := epconfig.CheckAccess(resolvedCfg, tokenHash, userID); checkErr != nil {
		lc.logger.Debug("execution: access denied", "ep_slug", req.EPSlug, "error", checkErr)
		return nil, admitErr(AdmitErrForbidden)
	}

	// ── 6. Generate IDs (UUID v4 — Python worker requires uuid.UUID() parsing) ─
	runID := newRunID()
	contextID := req.ContextID
	if contextID == "" {
		contextID = newRunID()
	}
	sessionID := newRunID()

	// ── 7. Gate.Check ─────────────────────────────────────────────────────────
	// Compute gate token hash: "" for anonymous sessions so gate.go skips
	// per-token rate limiting (sha256("") is not empty — must pass "" explicitly).
	gateTokenHash := ""
	if req.RawToken != "" {
		gateTokenHash = tokenHash
	}
	gateCfg := gate.Config{
		EPSlug:           req.EPSlug,
		AppID:            resolvedCfg.AppID,
		TokenHash:        gateTokenHash,
		SessionID:        sessionID,
		EPMaxConcurrent:  resolvedCfg.EPMaxConcurrent,
		AppMaxConcurrent: resolvedCfg.AppMaxConcurrent,
		RateLimitRPM:     resolvedCfg.RateLimitRPM,
		QueueTimeout:     resolvedCfg.QueueTimeout,
	}

	gateAdmitted := false
	if lc.gate != nil {
		if _, gateErr := lc.gate.Check(ctx, gateCfg); gateErr != nil {
			switch gateErr {
			case gate.ErrCapExceeded:
				return nil, admitErr(AdmitErrCapExceeded)
			case gate.ErrRateLimited:
				return nil, admitErr(AdmitErrRateLimited)
			case gate.ErrQueueFull:
				return nil, admitErr(AdmitErrQueueFull)
			default:
				lc.logger.Warn("execution: gate check failed", "ep_slug", req.EPSlug, "error", gateErr)
				return nil, admitErr(AdmitErrInternal)
			}
		}
		gateAdmitted = true
	}

	// ── 8. session.Register ───────────────────────────────────────────────────
	sessInfo := session.SessionInfo{
		SessionID:        sessionID,
		InstanceID:       req.InstanceID,
		UserID:           userID,
		OrchestratorName: req.EPSlug, // appSlug convention (matches WS handler)
		EPSlug:           req.EPSlug,
		AppID:            resolvedCfg.AppID,
		TenantID:         resolvedCfg.TenantID,
		ContextID:        contextID,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if lc.sessions != nil {
		if regErr := lc.sessions.Register(ctx, sessInfo); regErr != nil {
			lc.logger.Warn("execution: session register failed",
				"ep_slug", req.EPSlug,
				"app_id", resolvedCfg.AppID,
				"error", regErr)
			if gateAdmitted {
				_ = lc.gate.Rollback(context.Background(), gateCfg)
			}
			return nil, admitErr(AdmitErrInternal)
		}
	}

	// ── 9. Gate.Confirm ───────────────────────────────────────────────────────
	// Fatal: Confirm failure means the reservation cannot be extended to the
	// long-lived TTL. Clean up session and gate, skip CreateRun, return error.
	if gateAdmitted {
		if confErr := lc.gate.Confirm(ctx, gateCfg); confErr != nil {
			lc.logger.Warn("execution: gate confirm failed — rolling back",
				"ep_slug", req.EPSlug, "error", confErr)
			if lc.sessions != nil {
				_ = lc.sessions.End(context.Background(), sessionID, req.EPSlug, resolvedCfg.AppID)
			}
			_ = lc.gate.Release(context.Background(), gateCfg)
			return nil, admitErr(AdmitErrInternal)
		}
	}

	// ── 10. CreateRun ────────────────────────────────────────────────────────
	// Status is RunStatusAdmitted — not Running. The run transitions to Running
	// only when Lifecycle.Start successfully launches the Temporal workflow.
	// If Start never runs (WS upgrade failure, first-message error, stream
	// subscribe failure), Release will mark the run Failed.
	//
	// TenantID and ApplicationID come exclusively from resolvedCfg (server-side DB).
	// Never from request data — enforced by not reading those fields from req.
	eventsTransport := eventsTransportFromMode(req.RunEventsMode)
	run := domain.Run{
		ID:              runID,
		ContextID:       contextID,
		EntryPointSlug:  req.EPSlug,
		TenantID:        resolvedCfg.TenantID,
		ApplicationID:   resolvedCfg.AppID,
		Status:          domain.RunStatusAdmitted,
		EventsTransport: eventsTransport,
	}
	runCreated := false
	if lc.recorder != nil {
		if recErr := lc.recorder.CreateRun(ctx, run); recErr != nil {
			lc.logger.Warn("execution: create run failed",
				"run_id", runID,
				"ep_slug", req.EPSlug,
				"error", recErr)
			if lc.sessions != nil {
				_ = lc.sessions.End(context.Background(), sessionID, req.EPSlug, resolvedCfg.AppID)
			}
			if gateAdmitted {
				_ = lc.gate.Release(context.Background(), gateCfg)
			}
			return nil, admitErr(AdmitErrInternal)
		}
		runCreated = true
	}

	return &ExecutionHandle{
		RunID:           runID,
		ContextID:       contextID,
		SessionID:       sessionID,
		EPConfig:        resolvedCfg,
		EventsTransport: eventsTransport,
		gateCfg:         gateCfg,
		gateAdmitted:    gateAdmitted,
		runCreated:      runCreated,
	}, nil
}

// Start launches the Temporal workflow. The caller MUST have subscribed to the
// event bus (bus.Subscribe with h.ContextID) before calling Start — failure to
// do so may cause the first workflow events to be missed.
//
// Returns the WorkflowRun. Streaming handlers (WS, SSE) iterate events concurrently
// while A2A calls wfRun.Get() synchronously.
func (lc *Lifecycle) Start(ctx context.Context, h *ExecutionHandle, input temporal.WorkflowInput) (temporalclient.WorkflowRun, error) {
	if lc.temporal == nil {
		return nil, startErr("temporal client not configured")
	}

	// IDs and tenant identity come exclusively from the handle (server-resolved).
	// The caller provides UserMessage, History, and OrchestratorName via input;
	// we overwrite identity fields to prevent any caller-supplied values from leaking.
	input.RunID = h.RunID
	input.ContextID = h.ContextID
	input.EntryPointSlug = h.EPConfig.EPSlug
	input.TenantID = h.EPConfig.TenantID
	input.ApplicationID = h.EPConfig.AppID

	wfOpts := temporalclient.StartWorkflowOptions{
		ID:        "ctx-" + h.ContextID,
		TaskQueue: temporal.GoTaskQueue,
	}

	wfRun, wfErr := lc.temporal.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	if wfErr != nil {
		lc.logger.Warn("execution: start workflow failed",
			"run_id", h.RunID,
			"ep_slug", h.EPConfig.EPSlug,
			"error", wfErr)
		return nil, startErr(wfErr.Error())
	}

	// Workflow launched successfully — transition run admitted → running.
	if lc.recorder != nil && h.runCreated {
		updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer updateCancel()
		if updErr := lc.recorder.UpdateRunStatus(updateCtx, h.RunID, domain.RunStatusRunning, ""); updErr != nil {
			// Non-fatal: the run is already being executed. Log and continue.
			lc.logger.Warn("execution: update run to running failed",
				"run_id", h.RunID, "error", updErr)
		}
	}
	h.startedOK = true
	return wfRun, nil
}

// Release ends the session and releases the gate reservation. It must be called
// exactly once, always in a defer in the protocol handler.
//
// Release derives its own 5-second bounded context from context.Background() —
// callers must NOT pass a context. The request context is always cancelled before
// Release fires, so cleanup must be self-contained.
//
// Orphan-run prevention: if Admit created a run (runCreated) but Start never
// succeeded (startedOK is false), Release marks the run as Failed so it does not
// remain stuck in the "admitted" state indefinitely.
//
// Release(nil) is a no-op — safe to call even when Admit returned an error.
func (lc *Lifecycle) Release(h *ExecutionHandle) {
	if h == nil {
		return
	}
	// Always derive a fresh bounded context — the caller's request context is
	// cancelled before this defer fires, so we must not depend on it.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanCancel()

	appID := ""
	epSlug := ""
	if h.EPConfig != nil {
		appID = h.EPConfig.AppID
		epSlug = h.EPConfig.EPSlug
	}

	// Mark admitted run as failed when Start never completed successfully.
	if h.runCreated && !h.startedOK && lc.recorder != nil {
		if updErr := lc.recorder.UpdateRunStatus(cleanCtx, h.RunID, domain.RunStatusFailed, "startup failed"); updErr != nil {
			lc.logger.Warn("execution: mark run failed during release",
				"run_id", h.RunID, "ep_slug", epSlug, "error", updErr)
		}
	}

	if lc.sessions != nil && h.EPConfig != nil {
		if err := lc.sessions.End(cleanCtx, h.SessionID, epSlug, appID); err != nil {
			lc.logger.Warn("execution: session.End failed during release",
				"session_id", h.SessionID,
				"ep_slug", epSlug,
				"error", err)
		}
	}
	if h.gateAdmitted && lc.gate != nil {
		if err := lc.gate.Release(cleanCtx, h.gateCfg); err != nil {
			lc.logger.Warn("execution: gate.Release failed during release",
				"ep_slug", epSlug,
				"error", err)
		}
	}
}

// newRunID returns a new UUID v4 string. The Python Temporal worker parses run,
// context, and session IDs via uuid.UUID() — all IDs must use this format.
func newRunID() string { return uuid.New().String() }

// eventsTransportFromMode converts a RunEventsMode to the storage/routing value.
// "pubsub" → Pub/Sub channel; "streams"/"dual" → Redis Streams.
func eventsTransportFromMode(mode config.RunEventsMode) string {
	if mode == config.RunEventsModeDual || mode == config.RunEventsModeStreams {
		return "streams"
	}
	return "pubsub"
}
