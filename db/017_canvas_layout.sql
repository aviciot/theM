-- Phase 16: Replace app_nodes/app_edges with canvas JSONB on applications
-- Canvas positions are now stored as a layout map keyed by ref:
--   { "ep:<slug>": {x,y}, "orch:<ao_id>": {x,y}, "agent:<agent_id>_<ao_id>": {x,y},
--     "mw:<node_id>": {x,y}, "viewport": {x,y,zoom} }
-- The runtime never reads this column. Purely for canvas hydration.
-- Additive first: add column, then drop tables after code is deployed.
-- Idempotent: safe to re-run.
-- Apply:
--   docker cp db/017_canvas_layout.sql them-postgres:/tmp/them_017_canvas_layout.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_017_canvas_layout.sql

BEGIN;

-- Add canvas JSONB column to applications (null = no saved layout yet)
ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS canvas JSONB;

-- Drop the Phase 15 graph tables (they duplicate data now replaced by canvas JSONB)
-- CASCADE drops the indexes too
DROP TABLE IF EXISTS them.app_edges CASCADE;
DROP TABLE IF EXISTS them.app_nodes CASCADE;

COMMIT;
