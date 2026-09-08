-- Migration 085: add external_user_id to them.runs
-- Phase 1 of END_USER_AUTH_PLAN.md (companion to 084).
--
-- Tracks the end-user identity at the run level, mirroring tasks.external_user_id.
-- Set at CreateRun time from RuntimeIdentity.ExternalUserID; empty for
-- internal-user runs and service-token runs where no end-user was asserted.

ALTER TABLE them.runs
    ADD COLUMN IF NOT EXISTS external_user_id TEXT;

CREATE INDEX IF NOT EXISTS idx_runs_external_user
    ON them.runs(external_user_id)
    WHERE external_user_id IS NOT NULL;

COMMENT ON COLUMN them.runs.external_user_id IS
    'End-user identity for this run. Set server-side from JWKS JWT sub or '
    'X-External-User header (only when token.is_backend=true). '
    'Empty for internal-user and service-token runs.';
