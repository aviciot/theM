-- Phase 2: user_id on tasks (history isolation) and runs (attribution).
-- Internal (the-M user) sessions store user_id so LoadHistory can filter
-- by user_id, preventing cross-user history leakage between sessions that
-- share the same context_id.
ALTER TABLE them.tasks ADD COLUMN IF NOT EXISTS user_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_tasks_user ON them.tasks(user_id) WHERE user_id IS NOT NULL;

ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS user_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_runs_user ON them.runs(user_id) WHERE user_id IS NOT NULL;
