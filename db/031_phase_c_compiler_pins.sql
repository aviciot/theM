-- Migration 031: phase_c_compiler_pins
-- Adds source_definition_id + source_definition_hash to app_orchestrators and entry_points.
-- These track which published application_definition revision compiled each projection row.

BEGIN;

ALTER TABLE them.app_orchestrators
    ADD COLUMN IF NOT EXISTS source_definition_id   UUID REFERENCES them.application_definitions(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS source_definition_hash TEXT;

ALTER TABLE them.entry_points
    ADD COLUMN IF NOT EXISTS source_definition_id   UUID REFERENCES them.application_definitions(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS source_definition_hash TEXT;

INSERT INTO them.schema_migrations (version, description)
VALUES ('031', 'phase_c_compiler_pins: source_definition_id/hash on app_orchestrators + entry_points');

COMMIT;
