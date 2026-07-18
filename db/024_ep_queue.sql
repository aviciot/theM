-- Migration 024: Entry point queue configuration
-- Adds queue_timeout_seconds and queue_message to them.entry_points.
-- max_concurrent_sessions already exists from migration 022.

BEGIN;

ALTER TABLE them.entry_points
  ADD COLUMN IF NOT EXISTS queue_timeout_seconds INTEGER,
  ADD COLUMN IF NOT EXISTS queue_message TEXT;

COMMENT ON COLUMN them.entry_points.queue_timeout_seconds IS 'Seconds to wait for a slot before rejecting. NULL = immediate reject (no queue).';
COMMENT ON COLUMN them.entry_points.queue_message IS 'Message sent to client while waiting for a slot. NULL = default.';

COMMIT;
