-- Phase: allow multiple DIFFERENT guards on the same canvas node.
-- Found live 2026-09-28: uq_mw_wiring_app_node (db/018_graph_compiler.sql) was
-- written back when File Guard was the only guard kind that existed, so
-- "one wiring per node" and "one guard per node" were accidentally the same
-- rule. Once PII Guard and Prompt-Injection Guard were added later, this
-- silently became a real product limitation: a node could never have more
-- than one guard type wired to it at once, with zero code anywhere ever
-- attempting or testing the multi-guard case (confirmed: no existing app in
-- this database has ever had 2 wirings on the same node_id).
--
-- Fix: widen the unique key to (application_id, node_id, def_id) so a node
-- can carry one wiring PER GUARD TYPE — re-saving the SAME guard on the same
-- node still upserts in place (the real, exercised case,
-- docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 4's CreateMiddlewareWiring), while
-- two DIFFERENT guards on one node now coexist instead of colliding.
--
-- Apply live:
--   docker cp db/117_middleware_wirings_multi_guard_per_node.sql them-postgres:/tmp/them_117.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_117.sql

BEGIN;

DROP INDEX IF EXISTS them.uq_mw_wiring_app_node;

CREATE UNIQUE INDEX IF NOT EXISTS uq_mw_wiring_app_node_def
    ON them.middleware_wirings (application_id, node_id, def_id)
    WHERE node_id IS NOT NULL AND node_id != '';

COMMIT;
