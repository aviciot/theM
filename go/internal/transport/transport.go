// Package transport defines the shared interfaces and pure functions used by
// both the ws (WebSocket) and sse (Server-Sent Events) handler packages.
//
// Extracting them here avoids duplication while keeping each package's handler
// file free of interface declarations. No implementation lives here — only
// interfaces and pure helper functions.
package transport

import (
	"context"
	"crypto/sha256"
	"fmt"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/session"
)

// Authenticator validates bearer tokens and returns auth claims.
// Implemented by auth.Cache.
type Authenticator interface {
	Validate(ctx context.Context, token string) (*auth.TokenInfo, error)
}

// SessionStore manages session lifecycle in Redis.
// Implemented by session.Store.
type SessionStore interface {
	Register(ctx context.Context, info session.SessionInfo) error
	End(ctx context.Context, sessionID, epSlug, appID string) error
}

// GateStore performs admission control for incoming sessions.
// Implemented by gate.Gate.
type GateStore interface {
	Check(ctx context.Context, cfg gate.Config) (gate.Result, error)
	Confirm(ctx context.Context, cfg gate.Config) error
	Rollback(ctx context.Context, cfg gate.Config) error
	Release(ctx context.Context, cfg gate.Config) error
}

// EPConfigLoader resolves Entry Point and Application runtime config.
// Implemented by epconfig.Loader.
type EPConfigLoader interface {
	Load(ctx context.Context, epSlug string) (*epconfig.EPConfig, error)
}

// TemporalClientExecutor starts a Temporal workflow execution.
// Using an interface (rather than the full client.Client) allows tests to inject
// a fake without depending on a live Temporal server.
type TemporalClientExecutor interface {
	ExecuteWorkflow(ctx context.Context, options temporalclient.StartWorkflowOptions, workflow interface{}, args ...interface{}) (temporalclient.WorkflowRun, error)
}

// TokenHash returns the lowercase hex SHA-256 of rawToken, matching the hash
// stored in them.access_tokens by the Python platform (same as auth.tokenHash).
// This function is defined here (not in ws or sse) to ensure both packages use
// identical hashing logic — a divergence would break access-token blocking.
func TokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return fmt.Sprintf("%x", h)
}
