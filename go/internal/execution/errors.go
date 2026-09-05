// Package execution implements the shared admission-and-run-start pipeline
// used by the WS, SSE and A2A inbound handlers.
package execution

import "fmt"

// AdmitErrorKind classifies admission failures. Protocol handlers map each kind
// to their wire-format error (HTTP status, WS close code, JSON-RPC code).
type AdmitErrorKind int

const (
	// AdmitErrNotFound — EP slug not registered.
	AdmitErrNotFound AdmitErrorKind = iota
	// AdmitErrUnauthorized — token required but absent or invalid.
	AdmitErrUnauthorized
	// AdmitErrForbidden — EP/App disabled or token/user on block-list.
	AdmitErrForbidden
	// AdmitErrCapExceeded — gate session cap full, no queue.
	AdmitErrCapExceeded
	// AdmitErrRateLimited — per-token rate limit exceeded.
	AdmitErrRateLimited
	// AdmitErrQueueFull — gate queue full.
	AdmitErrQueueFull
	// AdmitErrDBUnavailable — DB/Redis unavailable during EPConfig load.
	AdmitErrDBUnavailable
	// AdmitErrNotImplemented — EP type not supported on this transport (e.g. voice on SSE).
	AdmitErrNotImplemented
	// AdmitErrInternal — unexpected internal error (logged internally; static string to client).
	AdmitErrInternal
	// AdmitErrQuotaConcurrentRuns — tenant max_concurrent_runs quota exceeded.
	AdmitErrQuotaConcurrentRuns
	// AdmitErrQuotaRunsPerMinute — tenant runs_per_minute quota exceeded.
	AdmitErrQuotaRunsPerMinute
	// AdmitErrQuotaMonthlyRuns — tenant monthly_runs quota exceeded.
	AdmitErrQuotaMonthlyRuns
	// AdmitErrQuotaAPIRPM — tenant api_requests_per_minute quota exceeded.
	AdmitErrQuotaAPIRPM
	// AdmitErrQuotaMonthlyLLMTokens — tenant monthly_llm_tokens quota exceeded.
	AdmitErrQuotaMonthlyLLMTokens
)

// AdmitError is the typed error returned by Lifecycle.Admit on failure.
// The internal cause is logged by Lifecycle; this struct never exposes it
// so callers cannot accidentally forward raw error strings to clients.
type AdmitError struct {
	Kind       AdmitErrorKind
	HTTPStatus int
}

// Error returns a static client-safe string — never a raw internal error.
func (e *AdmitError) Error() string {
	switch e.Kind {
	case AdmitErrNotFound:
		return "entry point not found"
	case AdmitErrUnauthorized:
		return "unauthorized"
	case AdmitErrForbidden:
		return "access denied"
	case AdmitErrCapExceeded:
		return "session cap exceeded"
	case AdmitErrRateLimited:
		return "rate limited"
	case AdmitErrQueueFull:
		return "queue full"
	case AdmitErrDBUnavailable:
		return "service unavailable"
	case AdmitErrNotImplemented:
		return "not implemented"
	case AdmitErrQuotaConcurrentRuns:
		return "concurrent run limit exceeded"
	case AdmitErrQuotaRunsPerMinute:
		return "run rate limit exceeded"
	case AdmitErrQuotaMonthlyRuns:
		return "monthly run limit exceeded"
	case AdmitErrQuotaAPIRPM:
		return "api rate limit exceeded"
	case AdmitErrQuotaMonthlyLLMTokens:
		return "monthly token limit exceeded"
	default:
		return "internal error"
	}
}

// admitErr is the internal constructor — HTTPStatus is set per-kind.
func admitErr(kind AdmitErrorKind) *AdmitError {
	status := httpStatusForKind(kind)
	return &AdmitError{Kind: kind, HTTPStatus: status}
}

func httpStatusForKind(k AdmitErrorKind) int {
	switch k {
	case AdmitErrNotFound:
		return 404
	case AdmitErrUnauthorized:
		return 401
	case AdmitErrForbidden:
		return 403
	case AdmitErrCapExceeded, AdmitErrRateLimited, AdmitErrQuotaConcurrentRuns, AdmitErrQuotaRunsPerMinute, AdmitErrQuotaMonthlyRuns, AdmitErrQuotaAPIRPM, AdmitErrQuotaMonthlyLLMTokens:
		return 429
	case AdmitErrQueueFull, AdmitErrDBUnavailable:
		return 503
	case AdmitErrNotImplemented:
		return 501
	default:
		return 500
	}
}

// StartError is returned by Lifecycle.Start when ExecuteWorkflow fails.
// Like AdmitError, it carries no raw internal error string.
type StartError struct {
	cause string // logged only; never sent to client
}

func (e *StartError) Error() string { return "internal error" }

func startErr(cause string) *StartError { return &StartError{cause: cause} }

// Cause is for internal logging only — never pass to http.Error or WriteRPC.
func (e *StartError) Cause() string { return fmt.Sprintf("start workflow: %s", e.cause) }
