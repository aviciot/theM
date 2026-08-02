package execution

import (
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/gate"
	temporalclient "go.temporal.io/sdk/client"
)

// ExecutionRequest carries the per-call inputs that the caller (protocol handler)
// has already resolved before calling Admit. The Lifecycle does not parse HTTP.
type ExecutionRequest struct {
	EPSlug        string           // entry-point slug from URL path
	RawToken      string           // bearer token string; empty if none presented
	TokenInfo     *auth.TokenInfo  // nil if public EP or token absent/invalid
	UserMessage   domain.Message   // parsed user message (content + role)
	ContextID     string           // caller-supplied; empty → Lifecycle generates UUID v4
	RunEventsMode config.RunEventsMode
	InstanceID    string // pod/replica identity for session record
}

// ExecutionHandle is the admission ticket returned by Admit on success.
// It carries all IDs and config needed for the caller to subscribe to the
// run-stream before calling Start.
//
// The gateAdmitted/gateCfg fields are unexported — Release uses them internally;
// callers should not access gate state directly.
type ExecutionHandle struct {
	RunID          string
	ContextID      string
	SessionID      string
	EPConfig       *epconfig.EPConfig
	EventsTransport string // "pubsub" or "streams" — derived from RunEventsMode at admit time

	// internal gate state — used by Release only
	gateAdmitted bool
	gateCfg      gate.Config

	// internal run state — used by Release to prevent orphan/stuck runs.
	// runCreated is set to true when CreateRun succeeds in Admit.
	// startedOK is set to true when ExecuteWorkflow succeeds in Start.
	// If runCreated && !startedOK at Release time, Release marks the run Failed.
	runCreated bool
	startedOK  bool
}

// ExecutionResult is the workflow outcome for callers that block synchronously (e.g. A2A).
// Streaming handlers (WS, SSE) iterate events while calling wfRun.Get themselves.
type ExecutionResult struct {
	WorkflowRun temporalclient.WorkflowRun
}
