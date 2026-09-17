-- 099: Temporal execution controls
-- Platform defaults stored as JSONB row in them.config (key = 'temporal_config').
-- Per-app overrides stored in new app_temporal_config table.

BEGIN;

CREATE TABLE IF NOT EXISTS them.app_temporal_config (
    application_id           UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    max_concurrent_workflows INT,
    workflow_timeout_s       INT,
    activity_timeout_s       INT,
    retry_max_attempts       INT,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_temporal_config TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_temporal_config TO them_admin;

COMMIT;
