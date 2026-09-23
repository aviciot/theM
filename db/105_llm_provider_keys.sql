-- db/105_llm_provider_keys.sql
-- Tenant LLM provider config: allowed models per provider, and multiple named
-- API keys per (provider, tenant) with per-key usage attribution.
--
-- Context: them.llm_providers was one row per (name, tenant_id) with a single
-- api_key_encrypted column — cannot represent "multiple named keys per provider".
-- This migration adds:
--   1. allowed_models on llm_providers (which models a tenant permits for that provider)
--   2. them.llm_provider_keys — many named keys per (provider, tenant)
--   3. run_usage.llm_provider_key_id — attributes usage/cost to a specific named key
--
-- Existing tenant_id / platform-default semantics on llm_providers are unchanged.
-- api_key_encrypted on llm_providers is frozen to legacy/platform-row use; new
-- tenant key management goes exclusively through llm_provider_keys.

ALTER TABLE them.llm_providers
    ADD COLUMN IF NOT EXISTS allowed_models JSONB NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS them.llm_provider_keys (
    id                BIGSERIAL PRIMARY KEY,
    llm_provider_id   INTEGER NOT NULL REFERENCES them.llm_providers(id) ON DELETE CASCADE,
    tenant_id         UUID NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    api_key_encrypted TEXT NOT NULL,
    is_default        BOOLEAN NOT NULL DEFAULT false,
    last_tested_at    TIMESTAMPTZ,
    last_test_ok      BOOLEAN,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (llm_provider_id, tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_llm_provider_keys_provider_tenant
    ON them.llm_provider_keys(llm_provider_id, tenant_id);

-- Only one default key per (provider, tenant).
CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_one_default_uq
    ON them.llm_provider_keys(llm_provider_id, tenant_id) WHERE is_default;

ALTER TABLE them.run_usage
    ADD COLUMN IF NOT EXISTS llm_provider_key_id BIGINT
        REFERENCES them.llm_provider_keys(id) ON DELETE SET NULL;

-- ── Ownership / grants ─────────────────────────────────────────────────────────
-- them.llm_provider_keys is queried exclusively via the admin pool (them_admin,
-- BYPASSRLS) — same pattern as them.llm_providers (see go/internal/admin/router.go,
-- NewLLMProvidersHandler uses the admin DBQuerier, tenant isolation enforced in the
-- WHERE clause, not RLS). RLS is still added below for defense-in-depth in case a
-- future caller queries this table on the them_app pool.

ALTER TABLE them.llm_provider_keys OWNER TO them_owner;
ALTER SEQUENCE them.llm_provider_keys_id_seq OWNER TO them_owner;

GRANT SELECT, INSERT, UPDATE, DELETE ON them.llm_provider_keys TO them_admin;
GRANT SELECT ON them.llm_provider_keys TO them_app;

-- New tables created after db/070_rls_roles.sql are NOT covered by its one-time
-- "GRANT ... ON ALL SEQUENCES IN SCHEMA them" — that only affected sequences that
-- existed at the time it ran. Every table with a SERIAL/BIGSERIAL PK created in a
-- later migration must explicitly grant its sequence, or INSERT fails with
-- "permission denied for sequence" (42501) even though the table grant succeeded.
GRANT USAGE, SELECT ON SEQUENCE them.llm_provider_keys_id_seq TO them_admin;
GRANT USAGE, SELECT ON SEQUENCE them.llm_provider_keys_id_seq TO them_app;

ALTER TABLE them.llm_provider_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.llm_provider_keys FORCE ROW LEVEL SECURITY;

CREATE POLICY llm_provider_keys_tenant_isolation ON them.llm_provider_keys
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('105_llm_provider_keys', 'Tenant LLM provider allowed_models + multi-key llm_provider_keys table + run_usage key attribution', NOW())
ON CONFLICT (version) DO NOTHING;
