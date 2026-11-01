package appflow

import "context"

// HILTimedOutInput is the input to AppFlowMarkHILTimedOutActivity.
type HILTimedOutInput struct {
	RunID   string `json:"run_id"`
	NodeID  string `json:"node_id"`
	Comment string `json:"comment,omitempty"`
}

// MarkHILTimedOutActivity marks a still-pending approval as timed_out when the HIL node's timer
// fired before anyone decided (F-124). Idempotent (only touches 'pending' rows); a nil DB is a no-op.
// Without it the approval would stay 'pending' — and approvable in the UI — long after the run moved on.
func (a *AppFlowActivities) MarkHILTimedOutActivity(ctx context.Context, input HILTimedOutInput) error {
	if a.DB == nil {
		return nil
	}
	_, err := a.DB.Exec(ctx, `
		UPDATE them.hil_approvals
		   SET status = 'timed_out', comment = COALESCE(NULLIF(comment, ''), NULLIF($3, '')),
		       decided_at = now(), updated_at = now()
		 WHERE run_id = $1::uuid AND node_id = $2 AND status = 'pending'`,
		input.RunID, input.NodeID, input.Comment)
	return err
}
