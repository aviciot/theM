-- db/122_governance_policies.sql
-- Governance policies: ONE table for limits + logging policy across all scopes.
-- Design: docs/_archive/GOVERNANCE_POLICY_DESIGN.md
--
-- Replaces (functionally) gateway_policies (tenant) and gateway_policy_profiles
-- (named client bundles). Rows are copied in below; the old tables are left in place,
-- unused, as a rollback net and will be dropped in a follow-up migration once stable.
--
-- scope_type:
--   tenant       scope_id = tenant_id
--   application  scope_id = application_id
--   client       scope_id = gateway_clients.id          (per-client override)
--   user         scope_id = 'internal:<id>' | 'external:<id>'
--   profile      scope_id = opaque key; reusable named bundle, assigned to clients
--                via gateway_clients.governance_profile_id (loaded as the client scope)
-- doc: governance.Policy JSON (schema_version 1).

CREATE TABLE IF NOT EXISTS them.governance_policies (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    scope_type  TEXT        NOT NULL
                CHECK (scope_type IN ('tenant','application','client','user','profile')),
    scope_id    TEXT        NOT NULL,
    name        TEXT,                                  -- profile display name (profile scope only)
    doc         JSONB       NOT NULL DEFAULT '{"schema_version":1}',
    version     INT         NOT NULL DEFAULT 1,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT,
    UNIQUE (tenant_id, scope_type, scope_id)
);

-- profile names unique per tenant
CREATE UNIQUE INDEX IF NOT EXISTS governance_policies_profile_name_uq
    ON them.governance_policies (tenant_id, name) WHERE scope_type = 'profile';

ALTER TABLE them.gateway_clients
    ADD COLUMN IF NOT EXISTS governance_profile_id UUID
        REFERENCES them.governance_policies(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_gateway_clients_governance_profile
    ON them.gateway_clients(governance_profile_id);

-- ── Ownership / grants / RLS ───────────────────────────────────────────────────
ALTER TABLE them.governance_policies OWNER TO them_owner;

GRANT SELECT, INSERT, UPDATE, DELETE ON them.governance_policies TO them_admin;
GRANT SELECT ON them.governance_policies TO them_app;

ALTER TABLE them.governance_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.governance_policies FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS governance_policies_tenant_isolation ON them.governance_policies;
CREATE POLICY governance_policies_tenant_isolation ON them.governance_policies
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Data copy (idempotent) ─────────────────────────────────────────────────────
-- Legacy: allowed_models empty/NULL = allow all; NULL max_tokens / budget = no cap.

-- 1. gateway_policies (tenant-wide) -> scope 'tenant'
INSERT INTO them.governance_policies (tenant_id, scope_type, scope_id, doc)
SELECT gp.tenant_id, 'tenant', gp.tenant_id::text,
       jsonb_build_object(
         'schema_version', 1,
         'limits', jsonb_build_object(
           'models', jsonb_build_object(
             'allow',   to_jsonb(COALESCE(gp.allowed_models, ARRAY[]::text[])),
             'aliases', COALESCE(gp.model_aliases, '{}'::jsonb)),
           'per_request', jsonb_build_object('max_tokens', COALESCE(gp.max_tokens_per_request, 0)),
           'budgets', CASE WHEN COALESCE(gp.monthly_budget_usd, 0) > 0
                      THEN jsonb_build_array(jsonb_build_object(
                             'metric','usd','period','month',
                             'limit', gp.monthly_budget_usd,'on_exceed','block'))
                      ELSE '[]'::jsonb END))
FROM them.gateway_policies gp
ON CONFLICT (tenant_id, scope_type, scope_id) DO NOTHING;

-- 2. gateway_policy_profiles -> scope 'profile' (scope_id = old bigint id, keeps this idempotent)
INSERT INTO them.governance_policies (tenant_id, scope_type, scope_id, name, doc)
SELECT pp.tenant_id, 'profile', pp.id::text, pp.name,
       jsonb_build_object(
         'schema_version', 1,
         'limits', jsonb_build_object(
           'models', jsonb_build_object(
             'allow',   COALESCE(pp.allowed_models, '[]'::jsonb),
             'aliases', COALESCE(pp.model_aliases, '{}'::jsonb)),
           'per_request', jsonb_build_object('max_tokens', COALESCE(pp.max_tokens_per_request, 0)),
           'budgets', CASE WHEN COALESCE(pp.monthly_budget_usd, 0) > 0
                      THEN jsonb_build_array(jsonb_build_object(
                             'metric','usd','period','month',
                             'limit', pp.monthly_budget_usd,'on_exceed','block'))
                      ELSE '[]'::jsonb END))
FROM them.gateway_policy_profiles pp
ON CONFLICT (tenant_id, scope_type, scope_id) DO NOTHING;

-- 3. Re-point clients at the copied profiles
UPDATE them.gateway_clients gc
SET governance_profile_id = g.id
FROM them.governance_policies g
WHERE gc.policy_profile_id IS NOT NULL
  AND gc.governance_profile_id IS NULL
  AND g.tenant_id = gc.tenant_id
  AND g.scope_type = 'profile'
  AND g.scope_id = gc.policy_profile_id::text;
