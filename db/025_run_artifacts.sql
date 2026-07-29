-- Run file artifacts — Phase R-3
-- Separate from them.artifacts (A2A task artifacts, JSONB parts)
-- This table stores binary file artifacts produced by Go orchestrator runs.
CREATE TABLE IF NOT EXISTS them.run_artifacts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL,
    application_id  UUID,
    session_id      UUID,
    filename        TEXT NOT NULL,
    content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',
    size            BIGINT NOT NULL,
    data            BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_run_artifacts_run ON them.run_artifacts(run_id);
CREATE INDEX IF NOT EXISTS idx_run_artifacts_app ON them.run_artifacts(application_id) WHERE application_id IS NOT NULL;
