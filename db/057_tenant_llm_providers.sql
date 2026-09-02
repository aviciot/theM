-- Migration 057: Per-tenant LLM provider key management
-- Adds tenant_id (nullable) to them.llm_providers.
-- NULL tenant_id = platform default; non-NULL = tenant override.
-- Unique constraint: (name, tenant_id) with NULLs treated as distinct
-- via two partial unique indexes (one for platform rows, one for tenant rows).

ALTER TABLE them.llm_providers
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE CASCADE;

-- Drop the old plain UNIQUE(name) constraint if it exists.
-- The constraint name in 001_schema.sql is the PG default: llm_providers_name_key.
ALTER TABLE them.llm_providers
    DROP CONSTRAINT IF EXISTS llm_providers_name_key;

-- Platform defaults: one row per name with tenant_id IS NULL.
CREATE UNIQUE INDEX IF NOT EXISTS llm_providers_name_platform_uq
    ON them.llm_providers (name)
    WHERE tenant_id IS NULL;

-- Tenant overrides: one row per (name, tenant_id) pair.
CREATE UNIQUE INDEX IF NOT EXISTS llm_providers_name_tenant_uq
    ON them.llm_providers (name, tenant_id)
    WHERE tenant_id IS NOT NULL;

-- Index for efficient per-tenant lookups at run resolution time.
CREATE INDEX IF NOT EXISTS llm_providers_tenant_id_idx
    ON them.llm_providers (tenant_id)
    WHERE tenant_id IS NOT NULL;
