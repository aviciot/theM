-- Migration 020: agent category column
-- Stores the classifier-assigned category per agent (e.g. 'Research', 'Coding', 'A2A').
-- Populated by Discover and agent creation when the classifier system agent is enabled.
-- NULL means not yet classified; frontend falls back to slug-based heuristics.

ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS category TEXT;
