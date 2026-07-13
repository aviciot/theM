-- Phase 12: Sub-orchestrator composition
-- Adds parent_run_id to them.runs so child workflow runs can be linked to their parent.

ALTER TABLE them.runs
    ADD COLUMN IF NOT EXISTS parent_run_id UUID NULL
        REFERENCES them.runs(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_runs_parent_run_id ON them.runs(parent_run_id);
