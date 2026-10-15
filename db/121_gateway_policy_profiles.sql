-- db/121_gateway_policy_profiles.sql
-- Gateway Policy Profiles: per-client policy assignment instead of tenant-wide
--
-- Context: gateway_policies table has one row per tenant — every client shares
-- the same policy (allowlist, aliases, budgets). This doesn't scale when you have
-- production, dev, and reporting clients that need different rules.
--
-- This migration adds:
--   1. gateway_policy_profiles — named, reusable policies per tenant
--   2. gateway_clients.policy_profile_id — each client picks a profile (nullable)
--   3. Deprecates (but keeps) gateway_policies for backward compatibility during migration
--
-- Usage:
--   - Create profiles: "Production", "Development", "Unrestricted"
--   - Assign profile to each client via dropdown in UI
--   - Enforcement checks client's assigned profile; null = no policy (fail-open)

CREATE TABLE IF NOT EXISTS them.gateway_policy_profiles (
    id                      BIGSERIAL PRIMARY KEY,
    tenant_id               UUID NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name                    TEXT NOT NULL,
    allowed_models          JSONB NOT NULL DEFAULT '[]',
    model_aliases           JSONB NOT NULL DEFAULT '{}',
    max_tokens_per_request  INTEGER,
    monthly_budget_usd      NUMERIC(12, 4),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_gateway_policy_profiles_tenant
    ON them.gateway_policy_profiles(tenant_id);

-- Add policy_profile_id to gateway_clients (nullable — no profile = no policy enforcement)
ALTER TABLE them.gateway_clients
    ADD COLUMN IF NOT EXISTS policy_profile_id BIGINT
        REFERENCES them.gateway_policy_profiles(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_gateway_clients_policy_profile
    ON them.gateway_clients(policy_profile_id);

-- ── Ownership / grants ─────────────────────────────────────────────────────────

ALTER TABLE them.gateway_policy_profiles OWNER TO them_owner;
ALTER SEQUENCE them.gateway_policy_profiles_id_seq OWNER TO them_owner;

GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_policy_profiles TO them_admin;
GRANT SELECT ON them.gateway_policy_profiles TO them_app;

GRANT USAGE, SELECT ON SEQUENCE them.gateway_policy_profiles_id_seq TO them_admin;

-- RLS (defense-in-depth — queries go via admin pool with BYPASSRLS, but add RLS anyway)
ALTER TABLE them.gateway_policy_profiles ENABLE ROW LEVEL SECURITY;

CREATE POLICY gateway_policy_profiles_tenant_isolation ON them.gateway_policy_profiles
    USING (tenant_id = current_setting('app.current_tenant_id', true)::uuid);
