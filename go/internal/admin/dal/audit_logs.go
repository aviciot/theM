package dal

import (
	"context"
	"encoding/json"
	"time"
)

// AuditLog is one row from them.audit_logs.
type AuditLog struct {
	ID         int64      `json:"id"`
	UserID     *int64     `json:"user_id,omitempty"`
	Action     string     `json:"action"`
	EntityType string     `json:"entity_type"`
	EntityID   *string    `json:"entity_id,omitempty"`
	Details    any        `json:"details"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListAuditLogs returns up to limit audit log rows for the given tenant,
// ordered by created_at DESC, starting at offset.
// Must be called with an admin-pool DB (BYPASSRLS) because them_app has
// INSERT-only access on them.audit_logs.
func (d *DB) ListAuditLogs(ctx context.Context, tenantID string, limit, offset int) ([]AuditLog, error) {
	const q = `
		SELECT id, user_id, action, entity_type, entity_id, details, created_at
		FROM them.audit_logs
		WHERE tenant_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := d.q.Query(ctx, q, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.Action, &a.EntityType, &a.EntityID, &a.Details, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []AuditLog{}
	}
	return out, nil
}

// AuditEntry holds the data to persist as one audit log row.
type AuditEntry struct {
	TenantID   string
	UserID     *int64         // nil for bearer-token-authenticated requests
	Action     string         // e.g. "agent.create", "app.delete"
	EntityType string         // e.g. "agent", "app", "tenant"
	EntityID   string
	Actor      string         // email or "user:{id}" or "token" for bearer calls
	Changes    map[string]any // non-nil on update actions: the fields that were changed
}

// WriteAuditLog inserts one row into them.audit_logs.
// Must be called with an admin-pool DB (BYPASSRLS).
// The details JSONB column stores {"actor": "...", "changes": {...}} where
// "changes" is omitted for create/delete actions and present for updates.
func (d *DB) WriteAuditLog(ctx context.Context, e AuditEntry) error {
	const q = `
		INSERT INTO them.audit_logs (tenant_id, user_id, action, entity_type, entity_id, details)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)`
	m := map[string]any{"actor": e.Actor}
	if len(e.Changes) > 0 {
		m["changes"] = e.Changes
	}
	details, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return d.q.Exec(ctx, q, e.TenantID, e.UserID, e.Action, e.EntityType, e.EntityID, details)
}
