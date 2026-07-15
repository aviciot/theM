-- Phase 15: Canvas graph storage (app_nodes + app_edges)
-- Source-of-truth for the visual builder. The typed tables
-- (entry_points, app_orchestrators, middleware_wirings) remain the compiled
-- projection consumed by the runtime; these two tables are canvas-only.
-- Additive + idempotent: safe to re-run.
-- Apply:
--   docker cp db/016_graph_storage.sql them-postgres:/tmp/them_016_graph_storage.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_016_graph_storage.sql

BEGIN;

CREATE TABLE IF NOT EXISTS them.app_nodes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    node_id         TEXT        NOT NULL,
    node_type       TEXT        NOT NULL,
    ref_id          UUID,
    position_x      DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_y      DOUBLE PRECISION NOT NULL DEFAULT 0,
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_nodes_app_node UNIQUE (application_id, node_id),
    CONSTRAINT ck_app_nodes_type CHECK (node_type IN ('entry_point','orchestrator','agent','middleware'))
);
CREATE INDEX IF NOT EXISTS idx_app_nodes_application_id ON them.app_nodes(application_id);

CREATE TABLE IF NOT EXISTS them.app_edges (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    edge_id         TEXT        NOT NULL,
    source_node_id  TEXT        NOT NULL,
    target_node_id  TEXT        NOT NULL,
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_edges_app_edge UNIQUE (application_id, edge_id)
);
CREATE INDEX IF NOT EXISTS idx_app_edges_application_id ON them.app_edges(application_id);

COMMIT;
