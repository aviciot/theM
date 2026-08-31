-- Migration 049: per-EP synthesized A2A agent card
-- The card_synthesizer system agent role synthesizes this from the orchestrator
-- system_prompt + sub-agent skills. The Go A2A handler serves it directly.
ALTER TABLE them.entry_points
    ADD COLUMN IF NOT EXISTS agent_card          JSONB,
    ADD COLUMN IF NOT EXISTS card_synthesized_at TIMESTAMPTZ;
