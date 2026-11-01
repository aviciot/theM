package appflow

import (
	"context"
	"log/slog"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/runclose"
)

// closeRunChildren is best-effort: a failure is logged, never returned, so it cannot
// block the terminal status update or the done/error event the client is waiting for.
func (a *AppFlowActivities) closeRunChildren(ctx context.Context, runID string, status domain.RunStatus, reason string) {
	if a.DB == nil || runID == "" {
		return
	}
	var closed int64
	if err := a.DB.QueryRow(ctx, runclose.ChildrenQuery, runID, string(status), reason).Scan(&closed); err != nil {
		slog.Error("appflow: closing run children failed — steps/tasks/approvals may stay open",
			"run_id", runID, "status", string(status), "error", err)
		return
	}
	if closed > 0 {
		slog.Info("appflow: closed open children of finished run", "run_id", runID, "status", string(status), "closed", closed)
	}
}
