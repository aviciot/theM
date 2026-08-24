-- db/040_agents_created_by.sql
-- Add created_by attribution to them.agents.
-- Stores the auth_service user id of whoever created the agent connector.

ALTER TABLE them.agents
    ADD COLUMN IF NOT EXISTS created_by INTEGER REFERENCES auth_service.users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_agents_created_by
    ON them.agents (created_by)
    WHERE created_by IS NOT NULL;
