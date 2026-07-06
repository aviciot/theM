-- Phase 10: SSE edge + entry_point_type constraint update
-- Idempotent: safe to re-run
-- Apply with:
--   docker cp db/005_phase10.sql them-postgres:/tmp/them_005_phase10.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_005_phase10.sql
BEGIN;

-- Drop old check constraint and replace with updated allowed values.
-- Old: ('websocket_chat','rest','voice','webrtc')
-- New: ('websocket','sse','webrtc')
ALTER TABLE them.applications
    DROP CONSTRAINT IF EXISTS applications_entry_point_type_check;

ALTER TABLE them.applications
    ADD CONSTRAINT applications_entry_point_type_check
    CHECK (entry_point_type IN ('websocket','sse','webrtc'));

-- Migrate any existing rows from old type names to new ones
UPDATE them.applications SET entry_point_type = 'websocket' WHERE entry_point_type = 'websocket_chat';
UPDATE them.applications SET entry_point_type = 'sse'       WHERE entry_point_type IN ('voice','rest');

COMMIT;
