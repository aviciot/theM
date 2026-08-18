-- 032_ep_memory_config.sql
-- Per-EP memory / history configuration.
-- Memory config lives on entry_points (not app_orchestrators) because history
-- is scoped to a session/EP boundary, not to the LLM/agent wiring layer.
-- Also adds tenant_id to tasks for efficient history isolation.

-- Memory columns on entry_points
ALTER TABLE them.entry_points
    ADD COLUMN IF NOT EXISTS memory_enabled          BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS history_window          INTEGER     NOT NULL DEFAULT 20,
    ADD COLUMN IF NOT EXISTS summarize_every_n_calls INTEGER     NOT NULL DEFAULT 10,
    ADD COLUMN IF NOT EXISTS memory_raw_fallback_n   INTEGER     NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS summarizer_provider     TEXT,
    ADD COLUMN IF NOT EXISTS summarizer_model        TEXT;

-- Tenant isolation for history queries (avoids deep JOIN through runs→applications)
ALTER TABLE them.tasks
    ADD COLUMN IF NOT EXISTS tenant_id UUID;

CREATE INDEX IF NOT EXISTS idx_tasks_tenant_context ON them.tasks(tenant_id, context_id);
