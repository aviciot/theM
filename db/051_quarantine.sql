-- Migration 051: Quarantine-first file storage
-- Adds: them.quarantine_artifacts table (pre-scan file isolation)
-- Changes: them.run_artifacts.data becomes nullable (infected rows have data=NULL)
--          them.run_artifacts gets storage_key column (MinIO artifact key for clean files)
--          them.middleware_jobs gets quarantine_id to link job to quarantine row

-- ── them.run_artifacts: allow data to be NULL (infected / not-yet-promoted) ──

ALTER TABLE them.run_artifacts
  ALTER COLUMN data DROP NOT NULL;

ALTER TABLE them.run_artifacts
  ADD COLUMN IF NOT EXISTS storage_key TEXT;

-- ── New: them.quarantine_artifacts ────────────────────────────────────────────
-- Holds uploaded bytes in isolation before the virus scan completes.
-- Bytes are deleted from MinIO (not here) once scan completes; this row
-- is kept as an audit trail.

CREATE TABLE IF NOT EXISTS them.quarantine_artifacts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  run_id          UUID NOT NULL,
  session_id      UUID,
  tenant_id       UUID NOT NULL,
  filename        TEXT NOT NULL,
  content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',
  size            BIGINT NOT NULL,
  -- storage_key is the MinIO quarantine bucket object key.
  -- Null after the bytes have been promoted or scrubbed.
  storage_key     TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '1 hour'
);

-- Reaper index: find expired quarantine rows to clean up
CREATE INDEX IF NOT EXISTS idx_quarantine_expires
  ON them.quarantine_artifacts (expires_at)
  WHERE storage_key IS NOT NULL;

-- ── them.middleware_jobs: add quarantine_id ───────────────────────────────────

ALTER TABLE them.middleware_jobs
  ADD COLUMN IF NOT EXISTS quarantine_id UUID
    REFERENCES them.quarantine_artifacts(id) ON DELETE SET NULL;
