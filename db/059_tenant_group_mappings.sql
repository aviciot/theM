-- Step 18: OIDC group claim → tenant role mapping
-- When a user authenticates via OIDC and the id_token includes a "groups" claim,
-- the platform can automatically assign a tenant role based on group membership
-- rather than defaulting all OIDC users to the "viewer" role.
--
-- Multiple mappings per tenant are allowed. When a user belongs to more than one
-- mapped group, the mapping with the highest priority (lowest number wins — 0 is
-- highest priority) is used. Ties are broken by group_claim alphabetically.

CREATE TABLE IF NOT EXISTS them.tenant_group_mappings (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    group_claim  TEXT        NOT NULL,
    role         TEXT        NOT NULL CHECK (role IN ('viewer', 'member', 'admin', 'super_admin')),
    priority     INTEGER     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, group_claim)
);

CREATE INDEX IF NOT EXISTS tenant_group_mappings_tenant_id_idx
    ON them.tenant_group_mappings (tenant_id);
