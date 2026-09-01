-- Migration 050: A2A middleware pipeline
-- Adds: them.run_artifacts scan columns, them.middleware_jobs, them.middleware_audit,
--       applications.security_config column.

-- ── Extend them.run_artifacts ─────────────────────────────────────────────────

ALTER TABLE them.run_artifacts
  ADD COLUMN IF NOT EXISTS scan_status TEXT NOT NULL DEFAULT 'disabled'
    CONSTRAINT run_artifacts_scan_status_check
      CHECK (scan_status IN
        ('disabled','pending','scanning','clean','infected','flagged','error','failed')),
  ADD COLUMN IF NOT EXISTS scan_result  JSONB,
  ADD COLUMN IF NOT EXISTS scanned_at   TIMESTAMPTZ;

-- Partial index: only rows that need worker attention
CREATE INDEX IF NOT EXISTS run_artifacts_scan_pending_idx
  ON them.run_artifacts (scan_status, created_at)
  WHERE scan_status IN ('pending','scanning');

-- ── New: them.middleware_jobs ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_jobs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id     UUID NOT NULL REFERENCES them.run_artifacts(id) ON DELETE CASCADE,
  application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  run_id          UUID,
  session_id      UUID,
  processors      TEXT[] NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending'
    CONSTRAINT middleware_jobs_status_check
      CHECK (status IN ('pending','claimed','done','failed')),
  attempt_count   INT NOT NULL DEFAULT 0,
  max_attempts    INT NOT NULL DEFAULT 3,
  claimed_at      TIMESTAMPTZ,
  retry_after     TIMESTAMPTZ,
  result          JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for the claim query: claimable pending rows ordered by arrival
CREATE INDEX IF NOT EXISTS middleware_jobs_claim_idx
  ON them.middleware_jobs (created_at)
  WHERE status = 'pending' AND attempt_count < max_attempts;

-- ── Extend them.applications: security_config ────────────────────────────────

ALTER TABLE them.applications
  ADD COLUMN IF NOT EXISTS security_config JSONB NOT NULL DEFAULT '{}';

-- ── Audit log ────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_audit (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id    UUID NOT NULL REFERENCES them.run_artifacts(id) ON DELETE CASCADE,
  application_id UUID NOT NULL,
  session_id     UUID,
  run_id         UUID,
  processor      TEXT NOT NULL,
  outcome        TEXT NOT NULL,
  detail         JSONB,
  duration_ms    INT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS middleware_audit_app_time_idx
  ON them.middleware_audit (application_id, created_at DESC);
CREATE INDEX IF NOT EXISTS middleware_audit_artifact_idx
  ON them.middleware_audit (artifact_id);
