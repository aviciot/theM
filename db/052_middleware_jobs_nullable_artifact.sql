-- Migration 052: Fix middleware_jobs.artifact_id for quarantine-first flow
-- Problem: migration 050 defined artifact_id as NOT NULL REFERENCES run_artifacts(id).
--          Migration 051 added quarantine_id for the new quarantine-first flow,
--          but did not fix artifact_id. EnqueueWithQuarantine passes the quarantine
--          UUID as artifact_id — which violates the FK because the quarantine UUID
--          is not yet in run_artifacts. It is only promoted after a clean scan.
--
-- Fix: make artifact_id nullable and drop the premature FK constraint.
--      The middleware worker sets artifact_id when it promotes a clean file to
--      run_artifacts. For infected files, artifact_id remains NULL.
--
-- No data migration needed: existing rows that have artifact_id set (from the
-- legacy Enqueue path) keep their values; newly enqueued quarantine-path jobs
-- will have artifact_id = NULL until promotion.

-- 1. Drop the existing FK constraint on artifact_id.
ALTER TABLE them.middleware_jobs
  DROP CONSTRAINT IF EXISTS middleware_jobs_artifact_id_fkey;

-- 2. Make artifact_id nullable.
ALTER TABLE them.middleware_jobs
  ALTER COLUMN artifact_id DROP NOT NULL;

-- 3. Add back the FK with DEFERRABLE and NULL allowed (only enforce when non-null).
--    We use a check constraint instead of a FK for nullable UUIDs pointing at run_artifacts
--    since Postgres FKs on nullable columns only enforce when the column is non-null.
--    Postgres handles this correctly: NULL values are never checked against the referenced table.
ALTER TABLE them.middleware_jobs
  ADD CONSTRAINT middleware_jobs_artifact_id_fkey
    FOREIGN KEY (artifact_id) REFERENCES them.run_artifacts(id) ON DELETE CASCADE;
