package appflow

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// PgxLogVerbosityLoader reads the per-app AppFlow trace log-verbosity setting from
// PostgreSQL. Used by Lifecycle.StartAppFlow to resolve it at workflow start time
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4).
type PgxLogVerbosityLoader struct {
	pools *db.Pools
}

// NewPgxLogVerbosityLoader creates a loader backed by the given db.Pools.
func NewPgxLogVerbosityLoader(pools *db.Pools) *PgxLogVerbosityLoader {
	return &PgxLogVerbosityLoader{pools: pools}
}

// Load returns the effective log-verbosity string ("off"|"status"|"full") for
// the given applicationID, which the caller (Lifecycle.StartAppFlow) has
// already resolved via a trusted, server-side lookup (h.EPConfig), not a
// user-supplied per-request path — tenantID is passed through only to satisfy
// GetAppLogVerbosity's ownership check. Returns dal.DefaultLogVerbosity, error
// on DB failure — caller falls back to the default (fail-open).
func (l *PgxLogVerbosityLoader) Load(ctx context.Context, tenantID, applicationID string) (string, error) {
	d := dal.NewDBFromAdminQuerier(l.pools.NewAdminQuerier())
	return d.GetAppLogVerbosity(ctx, tenantID, applicationID)
}
