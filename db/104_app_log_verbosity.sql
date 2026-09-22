-- 104: Per-app AppFlow trace log-verbosity setting (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4).
-- Controls how much detail persistTrace (internal/appflow/activities.go) writes to
-- them.run_steps for Graph-mode runs. No row = default "status". Debug-mode runs
-- always use "full" regardless of this setting (enforced in Go, not the DB).
--
-- off:    no them.run_steps writes at all (Redis live stream still publishes).
-- status: node_id/kind/status/latency only — no output/error detail persisted.
-- full:   status + output/error detail (today's unconditional Phase 3 behavior).

BEGIN;

CREATE TABLE IF NOT EXISTS them.app_debug_config (
    application_id UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    log_verbosity   TEXT        NOT NULL DEFAULT 'status' CHECK (log_verbosity IN ('off', 'status', 'full')),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_debug_config TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_debug_config TO them_admin;

COMMIT;
