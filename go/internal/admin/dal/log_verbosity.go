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
// Returns DefaultLogVerbosity if the application has no app_debug_config row yet.
// Returns pgx.ErrNoRows when the application does not exist or does not belong
// to tenantID — app_debug_config has no tenant_id column of its own, so
// ownership is checked via a join to them.applications on every call.
func (d *DB) GetAppLogVerbosity(ctx context.Context, tenantID, appID string) (string, error) {
	const q = `
		SELECT COALESCE(c.log_verbosity, $3)
		  FROM them.applications a
		  LEFT JOIN them.app_debug_config c ON c.application_id = a.id
		 WHERE a.id = $1::uuid AND a.tenant_id = $2::uuid`
	var v string
	err := d.q.QueryRow(ctx, q, appID, tenantID, DefaultLogVerbosity).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

// UpsertAppLogVerbosity writes the per-app trace log-verbosity setting.
// Returns pgx.ErrNoRows when the application does not exist or does not
// belong to tenantID — the INSERT ... SELECT ... RETURNING matches zero rows
// in that case, same signal GetAppLogVerbosity uses.
func (d *DB) UpsertAppLogVerbosity(ctx context.Context, tenantID, appID, verbosity string) error {
	const q = `
		INSERT INTO them.app_debug_config (application_id, log_verbosity, updated_at)
		SELECT id, $3, now() FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid
		ON CONFLICT (application_id) DO UPDATE
		  SET log_verbosity = EXCLUDED.log_verbosity,
		      updated_at    = now()
		RETURNING application_id`
	var returnedID string
	return d.q.ExecReturning(ctx, q, appID, tenantID, verbosity).Scan(&returnedID)
}
