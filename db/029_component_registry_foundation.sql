-- Migration 029: component_definitions base table + application_definitions + column additions
-- Phase A of Application v2: Registry-Backed Application Component Model
-- Spec: docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. component_definitions — universal definition registry (base table)
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE them.component_definitions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                 TEXT NOT NULL CHECK (kind IN ('agent','orchestrator','middleware','tool','entry_point')),
    namespace            TEXT NOT NULL,
    name                 TEXT NOT NULL,
    version              INTEGER NOT NULL,
    display_name         TEXT NOT NULL,
    description          TEXT,
    implementation_type  TEXT NOT NULL,
    configuration_schema JSONB NOT NULL DEFAULT '{}'::jsonb,
    default_config       JSONB NOT NULL DEFAULT '{}'::jsonb,
    capabilities         JSONB NOT NULL DEFAULT '[]'::jsonb,
    input_schema         JSONB,
    output_schema        JSONB,
    credential_schema    JSONB NOT NULL DEFAULT '[]'::jsonb,
    scope                TEXT NOT NULL CHECK (scope IN ('builtin','tenant')),
    tenant_id            UUID REFERENCES them.tenants(id),    -- NULL for builtin
    status               TEXT NOT NULL CHECK (status IN ('draft','published','deprecated')),
    content_hash         TEXT NOT NULL,
    enabled              BOOLEAN NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at         TIMESTAMPTZ,
    UNIQUE (kind, namespace, name, version)
);

-- Indexes on component_definitions
CREATE INDEX idx_cd_kind_scope ON them.component_definitions (kind, scope);
CREATE INDEX idx_cd_tenant ON them.component_definitions (tenant_id) WHERE tenant_id IS NOT NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. application_definitions — immutable revision history per application
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE them.application_definitions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL REFERENCES them.tenants(id),
    revision        INTEGER NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('draft','published')),
    definition      JSONB NOT NULL,
    definition_hash TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    UNIQUE (application_id, revision)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Add active_definition_id to applications (nullable — legacy apps start NULL)
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.applications
    ADD COLUMN active_definition_id UUID REFERENCES them.application_definitions(id) ON DELETE SET NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Add definition_id to runs (nullable — legacy runs start NULL)
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.runs
    ADD COLUMN definition_id UUID REFERENCES them.application_definitions(id) ON DELETE SET NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- 5. Add component definition pin columns to app_orchestrators
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.app_orchestrators
    ADD COLUMN component_definition_id UUID REFERENCES them.component_definitions(id) ON DELETE SET NULL,
    ADD COLUMN component_version       INTEGER;

-- ─────────────────────────────────────────────────────────────────────────────
-- 6. Add component definition pin columns to middleware_wirings
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.middleware_wirings
    ADD COLUMN component_definition_id UUID REFERENCES them.component_definitions(id) ON DELETE SET NULL,
    ADD COLUMN component_version       INTEGER;

-- ─────────────────────────────────────────────────────────────────────────────
-- 7. Record migration
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.schema_migrations (version, description)
VALUES ('029', 'component_definitions base table, application_definitions, active_definition_id/definition_id/component pin columns');

COMMIT;
