-- Phase 3 (docs/APP_CANVAS_DEBUG_PLAN.md): durable per-node trace storage for
-- AppFlow (Graph-mode) runs, reusing them.run_steps instead of a new sibling
-- table (per the plan's "round 2" decision #3).
--
-- Also fixes a pre-existing bug found while researching this phase: tool_call_id
-- is TEXT NOT NULL with no default, but internal/runrecorder.RecordAgentStep
-- (the only writer for orchestrator-mode runs) never included it in its INSERT
-- column list — every orchestrator-mode run_steps insert should have been
-- failing against a real Postgres instance. Not caught before now because the
-- unit test mocks the DB. The column is dropped rather than made nullable: no
-- reader ever needs an LLM tool_use ID for orchestrator-mode steps in practice
-- (confirmed: GetRunDetail only ever COALESCEs it to '' for frontend display,
-- never used to correlate anything), so keeping a permanently-empty nullable
-- column would be dead weight.

ALTER TABLE them.run_steps
  DROP COLUMN IF EXISTS tool_call_id;

-- node_id/node_kind identify an AppFlow DAG node execution. NULL for
-- orchestrator-mode rows (agent_slug/iteration identify those instead).
-- A node fires node_start then node_done/node_error — represented as ONE row,
-- inserted on node_start and updated in place on completion (same
-- pending -> completed/failed lifecycle run_steps already uses for
-- orchestrator steps), not two separate rows.
ALTER TABLE them.run_steps
  ADD COLUMN IF NOT EXISTS node_id   TEXT,
  ADD COLUMN IF NOT EXISTS node_kind TEXT;

-- iteration is meaningless for a DAG node (no loop iteration concept) but is
-- NOT NULL today. AppFlow rows use 0 as a sentinel "not applicable" value —
-- cheaper than making the column nullable and updating every existing reader/
-- writer that assumes it's always present.
ALTER TABLE them.run_steps
  ALTER COLUMN iteration SET DEFAULT 0;

-- One row per (run_id, node_id) — lets the node_start insert and the
-- node_done/node_error update target the same row via ON CONFLICT. Partial
-- (node_id IS NOT NULL) so it never constrains orchestrator-mode rows, which
-- have node_id NULL and can legitimately repeat run_id across many rows.
CREATE UNIQUE INDEX IF NOT EXISTS idx_run_steps_run_node
  ON them.run_steps(run_id, node_id)
  WHERE node_id IS NOT NULL;

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('103_run_steps_appflow_trace', 'Phase 3: run_steps gains node_id/node_kind for AppFlow traces; drops broken NOT NULL tool_call_id', NOW())
ON CONFLICT (version) DO NOTHING;
