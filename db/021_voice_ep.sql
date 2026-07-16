-- Migration 021: add 'voice' entry point type
ALTER TABLE them.entry_points DROP CONSTRAINT IF EXISTS entry_points_entry_point_type_check;
ALTER TABLE them.entry_points ADD CONSTRAINT entry_points_entry_point_type_check
  CHECK (entry_point_type IN ('websocket','sse','webrtc','a2a','voice'));
