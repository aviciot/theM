-- Migration 010: agent icon column
-- Stores a Material Symbols icon name per agent (e.g. 'hub', 'visibility').
-- NULL = auto-detect from slug/category in the frontend.
-- Populated by Discover when the agent card contains an iconUrl mapping,
-- or set manually by the user via the Edit modal.

ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS icon TEXT;
