-- db/092_tenant_runtime_config.sql
-- Per-tenant runtime identity provider configuration for external JWT validation.
--
-- Enables an entry point with access_mode="external_jwt" to validate JWTs
-- issued by the tenant's own IdP (e.g. bank Keycloak, Auth0) via JWKS.
-- This is separate from them.tenants.idp_config (employee SSO for dashboard login).
--
-- Prerequisites: db/088_allowed_principals.sql applied.

CREATE TABLE IF NOT EXISTS them.tenant_runtime_config (
    tenant_id   UUID        PRIMARY KEY REFERENCES them.tenants(id) ON DELETE CASCADE,
    jwks_uri    TEXT        NOT NULL,
    issuer      TEXT        NOT NULL,
    audience    TEXT,                        -- NULL = skip aud validation
    sub_claim   TEXT        NOT NULL DEFAULT 'sub',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- them_app needs full CRUD (tenant self-service routes run via TenantTx / them_app role).
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_runtime_config TO them_app;

-- Row-level security: each tenant can only see and modify its own row.
ALTER TABLE them.tenant_runtime_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tenant_runtime_config FORCE ROW LEVEL SECURITY;

CREATE POLICY trc_tenant_isolation ON them.tenant_runtime_config
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- them_admin has BYPASSRLS — no policy needed for admin reads.
