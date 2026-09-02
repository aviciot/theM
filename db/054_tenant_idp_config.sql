-- Migration 054: add idp_config JSONB column to them.tenants
-- Stores per-tenant OIDC provider configuration (discovery URL, client_id, client_secret).
-- NULL means no OIDC configured for this tenant (password login only).

ALTER TABLE them.tenants
    ADD COLUMN IF NOT EXISTS idp_config JSONB DEFAULT NULL;

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('054_tenant_idp_config', 'Step 5: add idp_config JSONB to them.tenants for per-tenant OIDC', now())
ON CONFLICT (version) DO NOTHING;
