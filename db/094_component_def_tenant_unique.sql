-- Migration 094: allow per-tenant component definitions
-- The old unique constraint (kind, namespace, name, version) was global,
-- preventing the same agent from being deployed to multiple tenants.
-- Replace it with (kind, namespace, name, version, tenant_id) so each
-- tenant can own their own copy of a definition.
-- builtin definitions have tenant_id = NULL; they remain globally unique
-- via the partial index below.

ALTER TABLE them.component_definitions
    DROP CONSTRAINT component_definitions_kind_namespace_name_version_key;

-- Unique across all tenant-scoped definitions per tenant.
CREATE UNIQUE INDEX component_definitions_tenant_unique
    ON them.component_definitions (kind, namespace, name, version, tenant_id)
    WHERE tenant_id IS NOT NULL;

-- Builtin definitions (tenant_id IS NULL) remain globally unique.
CREATE UNIQUE INDEX component_definitions_builtin_unique
    ON them.component_definitions (kind, namespace, name, version)
    WHERE tenant_id IS NULL;
