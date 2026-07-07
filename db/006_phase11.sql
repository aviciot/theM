-- Phase 11: history_window on orchestrators
-- history_window: max prior turns to include in LLM context (-1 = unlimited)
BEGIN;
ALTER TABLE them.orchestrators
    ADD COLUMN IF NOT EXISTS history_window INTEGER NOT NULL DEFAULT 20;
COMMIT;
