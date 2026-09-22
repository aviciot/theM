package dal

import "context"

// LogVerbosityOff disables AppFlow trace persistence entirely (Redis live stream still publishes).
const LogVerbosityOff = "off"

// LogVerbosityStatus persists node_id/kind/status/latency only, no output/error detail.
const LogVerbosityStatus = "status"

// LogVerbosityFull persists status plus output/error detail. Debug-mode runs always use this.
const LogVerbosityFull = "full"

// DefaultLogVerbosity is used when an application has no app_debug_config row.
const DefaultLogVerbosity = LogVerbosityStatus

// IsValidLogVerbosity reports whether v is one of the three recognized levels.
func IsValidLogVerbosity(v string) bool {
	switch v {
	case LogVerbosityOff, LogVerbosityStatus, LogVerbosityFull:
		return true
	}
	return false
}

// GetAppLogVerbosity reads the per-app trace log-verbosity setting.
// Returns DefaultLogVerbosity if no row exists yet.
func (d *DB) GetAppLogVerbosity(ctx context.Context, appID string) (string, error) {
	const q = `SELECT log_verbosity FROM them.app_debug_config WHERE application_id = $1::uuid`
	var v string
	err := d.q.QueryRow(ctx, q, appID).Scan(&v)
	if IsNoRows(err) {
		return DefaultLogVerbosity, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// UpsertAppLogVerbosity writes the per-app trace log-verbosity setting.
func (d *DB) UpsertAppLogVerbosity(ctx context.Context, appID, verbosity string) error {
	const q = `
		INSERT INTO them.app_debug_config (application_id, log_verbosity, updated_at)
		VALUES ($1::uuid, $2, now())
		ON CONFLICT (application_id) DO UPDATE
		  SET log_verbosity = EXCLUDED.log_verbosity,
		      updated_at    = now()`
	return d.q.Exec(ctx, q, appID, verbosity)
}
