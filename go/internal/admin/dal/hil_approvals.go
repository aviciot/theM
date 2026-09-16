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
