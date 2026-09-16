-- HIL approval requests written by AppFlowWorkflow before pausing for signal.
-- Each row represents one pending or resolved human-in-the-loop gate.
CREATE TABLE IF NOT EXISTS them.hil_approvals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    application_id  UUID NOT NULL,
    run_id          UUID NOT NULL,
    node_id         TEXT NOT NULL,
    approver_role   TEXT NOT NULL DEFAULT 'admin',
    prompt          TEXT,
    fallback_action TEXT NOT NULL DEFAULT 'reject'
                    CHECK (fallback_action IN ('reject','approve','abort')),
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','approved','rejected','timed_out')),
    decided_at      TIMESTAMPTZ,
    comment         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_hil_approvals_tenant_app
    ON them.hil_approvals(tenant_id, application_id);
CREATE INDEX IF NOT EXISTS idx_hil_approvals_run
    ON them.hil_approvals(run_id);
CREATE INDEX IF NOT EXISTS idx_hil_approvals_status
    ON them.hil_approvals(status)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE ON them.hil_approvals TO them_app;
GRANT SELECT, INSERT, UPDATE ON them.hil_approvals TO them_admin;
