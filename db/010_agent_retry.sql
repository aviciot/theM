-- Phase 10: per-agent Temporal retry policy
-- Adds max_retries to them.agents (default 2 = same as the previous hardcoded value).
-- 1 = no retry (one attempt total), 2 = one retry, etc.
-- Temporal's maximum_attempts=0 means unlimited — we clamp to min 1 in application code.

ALTER TABLE them.agents
    ADD COLUMN IF NOT EXISTS max_retries INTEGER NOT NULL DEFAULT 2;
