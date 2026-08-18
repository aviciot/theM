-- Migration 034: tag built-in system agents as internal + locked
-- Ensures security_scanner (and any future system agents) carry the internal
-- and locked tags so the UI shows the lock indicator and disables deletion.
-- Safe to re-run.

UPDATE them.agents
SET tags = (
    SELECT array_agg(DISTINCT t ORDER BY t)
    FROM unnest(tags || ARRAY['internal', 'locked']) AS t
)
WHERE slug IN ('security_scanner');
