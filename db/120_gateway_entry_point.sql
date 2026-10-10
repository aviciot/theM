-- Migration 120: gateway entry point type + app_id on gateway_clients
--
-- Profiles are being redesigned as App Canvas applications with a "gateway"
-- entry point type. This migration:
--   1. Adds "gateway" to the entry_point_type CHECK constraint so the App
--      Canvas UI can create gateway-type entry points.
--   2. Adds app_id to gateway_clients so a client can be linked to the
--      App Canvas application that acts as its profile flow.
--
-- profile_id is retained (nullable, existing data untouched) and will be
-- dropped in a later migration once the transition is complete.
--
-- Design: docs/LLM_GATEWAY_DESIGN.md §11 — "gateway" entry point type.

-- 1. Extend the entry_point_type CHECK constraint to allow "gateway".
ALTER TABLE them.entry_points
    DROP CONSTRAINT IF EXISTS entry_points_entry_point_type_check;

ALTER TABLE them.entry_points
    ADD CONSTRAINT entry_points_entry_point_type_check
    CHECK (entry_point_type IN ('websocket', 'sse', 'webrtc', 'a2a', 'voice', 'gateway'));

-- 2. Add app_id to gateway_clients — the App Canvas application that
--    acts as the profile flow for this client.
ALTER TABLE them.gateway_clients
    ADD COLUMN IF NOT EXISTS app_id UUID REFERENCES them.applications(id) ON DELETE SET NULL;
