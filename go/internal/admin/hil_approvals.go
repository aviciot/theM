package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// HILApprovalsHandler exposes the HIL approval API for AppFlowWorkflow.
type HILApprovalsHandler struct {
	db       DBQuerier
	temporal TemporalSignaler
}

// NewHILApprovalsHandler creates a HILApprovalsHandler.
func NewHILApprovalsHandler(db DBQuerier, temporal TemporalSignaler) *HILApprovalsHandler {
	return &HILApprovalsHandler{db: db, temporal: temporal}
}

// Routes mounts the HIL approval endpoints under /runs/{run_id}/hil/{node_id}.
func (h *HILApprovalsHandler) Routes(r chi.Router) {
	r.Post("/runs/{run_id}/hil/{node_id}/approve", h.Approve)
	r.Post("/runs/{run_id}/hil/{node_id}/reject", h.Reject)
}

type hilDecisionRequest struct {
	Comment string `json:"comment"`
}

// Approve handles POST /api/v1/runs/{run_id}/hil/{node_id}/approve.
func (h *HILApprovalsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

// Reject handles POST /api/v1/runs/{run_id}/hil/{node_id}/reject.
func (h *HILApprovalsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

func (h *HILApprovalsHandler) decide(w http.ResponseWriter, r *http.Request, approved bool) {
	runID := chi.URLParam(r, "run_id")
	nodeID := chi.URLParam(r, "node_id")
	if runID == "" || nodeID == "" {
		writeError(w, http.StatusBadRequest, "run_id and node_id are required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	d := dal.NewDB(h.db)

	// Load the HIL approval row to verify existence and get the required approver_role.
	approval, err := d.GetHILApproval(r.Context(), tenantID, runID, nodeID)
	if err != nil {
		if dal.IsNoRows(err) {
			writeError(w, http.StatusNotFound, "HIL approval not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if approval.Status != "pending" {
		writeError(w, http.StatusConflict, "HIL gate already decided: "+approval.Status)
		return
	}

	// RBAC: caller must have admin, super_admin, or the configured approver_role.
	claims, ok := auth.ClaimsFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !hasHILApproverRole(claims.Roles, approval.ApproverRole) {
		writeError(w, http.StatusForbidden, "insufficient role for HIL approval")
		return
	}

	var req hilDecisionRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	decision := "rejected"
	if approved {
		decision = "approved"
	}

	// Update DB status first — durable record of the decision.
	// Idempotent: only updates rows with status='pending'.
	if dbErr := d.UpdateHILApprovalStatus(r.Context(), tenantID, runID, nodeID, decision, req.Comment); dbErr != nil {
		slog.WarnContext(r.Context(), "hil: db update failed", "run_id", runID, "node_id", nodeID)
	}

	// Send Temporal signal — best-effort; workflow may have already completed.
	if h.temporal != nil {
		signalName := appflow.AppFlowSignalHILApproval + ":" + nodeID
		workflowID := appflow.WorkflowIDForRun(tenantID, runID)
		payload := appflow.HILApprovalPayload{
			Approved: approved,
			Comment:  req.Comment,
		}
		if sigErr := h.temporal.SignalNamedWorkflow(r.Context(), workflowID, signalName, payload); sigErr != nil {
			// Non-fatal: the DB row is updated; the workflow may have already completed.
			slog.WarnContext(r.Context(), "hil: temporal signal failed (workflow may have completed)",
				"workflow_id", workflowID, "signal", signalName)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":  runID,
		"node_id": nodeID,
		"status":  decision,
	})
}

// hasHILApproverRole returns true when the caller has admin, super_admin, or
// the specific approver_role configured on the HIL node.
func hasHILApproverRole(callerRoles []string, approverRole string) bool {
	for _, r := range callerRoles {
		if r == "admin" || r == "super_admin" {
			return true
		}
		if approverRole != "" && r == approverRole {
			return true
		}
	}
	return false
}
