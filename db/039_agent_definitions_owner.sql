-- db/039_agent_definitions_owner.sql
-- Add owner attribution and a denormalised display_name to agent_definitions.
-- owner_id: integer FK to auth_service.users.id — who created the definition.
-- display_name: cached from definition->'agent_root'->>'display_name' so the
--   library listing never needs to parse JSONB for every row.

ALTER TABLE them.agent_definitions
    ADD COLUMN IF NOT EXISTS owner_id     INTEGER REFERENCES auth_service.users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS display_name TEXT    GENERATED ALWAYS AS (
        (definition -> 'agent_root' ->> 'display_name')
    ) STORED;

CREATE INDEX IF NOT EXISTS agent_definitions_owner
    ON them.agent_definitions (owner_id)
    WHERE owner_id IS NOT NULL;
