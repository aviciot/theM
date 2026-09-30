package dal

import (
	"context"
	"time"
)

// HILApproval is a row from them.hil_approvals.
type HILApproval struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	ApplicationID string     `json:"application_id"`
	RunID         string     `json:"run_id"`
	NodeID        string     `json:"node_id"`
	ApproverRole  string     `json:"approver_role"`
	Prompt        string     `json:"prompt,omitempty"`
	FallbackAction string    `json:"fallback_action"`
	Status        string     `json:"status"`
	Comment       string     `json:"comment,omitempty"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// GetHILApproval fetches the HIL approval row for (tenant_id, run_id, node_id).
// Returns ErrNoRows when not found.
func (db *DB) GetHILApproval(ctx context.Context, tenantID, runID, nodeID string) (HILApproval, error) {
	const q = `
		SELECT id, tenant_id, application_id, run_id, node_id, approver_role,
		       COALESCE(prompt, ''), fallback_action, status,
		       COALESCE(comment, ''), decided_at, created_at
		  FROM them.hil_approvals
		 WHERE tenant_id = $1::uuid
		   AND run_id    = $2::uuid
		   AND node_id   = $3
		 LIMIT 1`
	row := db.q.QueryRow(ctx, q, tenantID, runID, nodeID)
	var a HILApproval
	err := row.Scan(
		&a.ID, &a.TenantID, &a.ApplicationID, &a.RunID, &a.NodeID,
		&a.ApproverRole, &a.Prompt, &a.FallbackAction, &a.Status,
		&a.Comment, &a.DecidedAt, &a.CreatedAt,
	)
	return a, err
}

// PendingHILRow is a row returned by ListPendingHIL.
type PendingHILRow struct {
	RunID         string    `json:"run_id"`
	NodeID        string    `json:"node_id"`
	ApplicationID string    `json:"application_id"`
	ApproverRole  string    `json:"approver_role"`
	Prompt        string    `json:"prompt,omitempty"`
	FallbackAction string   `json:"fallback_action"`
	CreatedAt     time.Time `json:"created_at"`
	RunStatus     string    `json:"run_status"`
	RunStartedAt  time.Time `json:"run_started_at"`
}

// ListPendingHIL returns all hil_approvals rows with status='pending' for the tenant,
// joined to their run for context. Uses the partial index idx_hil_approvals_status.
func (db *DB) ListPendingHIL(ctx context.Context, tenantID string) ([]PendingHILRow, error) {
	const q = `
		SELECT ha.run_id, ha.node_id, ha.application_id, ha.approver_role,
		       COALESCE(ha.prompt, ''), ha.fallback_action, ha.created_at,
		       r.status, r.started_at
		  FROM them.hil_approvals ha
		  JOIN them.runs r ON r.id = ha.run_id
		 WHERE ha.tenant_id = $1::uuid
		   AND ha.status    = 'pending'
		 ORDER BY ha.created_at DESC`
	rows, err := db.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingHILRow
	for rows.Next() {
		var row PendingHILRow
		if err := rows.Scan(
			&row.RunID, &row.NodeID, &row.ApplicationID, &row.ApproverRole,
			&row.Prompt, &row.FallbackAction, &row.CreatedAt,
			&row.RunStatus, &row.RunStartedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if out == nil {
		out = []PendingHILRow{}
	}
	return out, nil
}

// UpdateHILApprovalStatus marks the approval as approved/rejected.
// Only updates rows in 'pending' status — idempotent if already decided.
func (db *DB) UpdateHILApprovalStatus(ctx context.Context, tenantID, runID, nodeID, status, comment string) error {
	const q = `
		UPDATE them.hil_approvals
		   SET status     = $4,
		       comment    = NULLIF($5, ''),
		       decided_at = now(),
		       updated_at = now()
		 WHERE tenant_id = $1::uuid
		   AND run_id    = $2::uuid
		   AND node_id   = $3
		   AND status    = 'pending'`
	err := db.q.Exec(ctx, q, tenantID, runID, nodeID, status, comment)
	return err
}
