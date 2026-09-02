-- Migration 053: tenant memberships for auth_service users
-- Adds auth_service.tenant_memberships so every user belongs to exactly one
-- tenant (multi-tenant memberships added in a later migration).
-- Backfills all existing users to the bootstrap tenant.

-- Step 1: tenant_memberships join table
CREATE TABLE IF NOT EXISTS auth_service.tenant_memberships (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    INTEGER     NOT NULL REFERENCES auth_service.users(id) ON DELETE CASCADE,
    tenant_id  UUID        NOT NULL,
    role       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_tenant_memberships_user_id
    ON auth_service.tenant_memberships (user_id);

-- Step 2: backfill — every existing user becomes a member of the bootstrap tenant
-- with the role they already hold (joined from auth_service.roles).
INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
SELECT u.id,
       '00000000-0000-0000-0000-000000000001'::uuid,
       r.name
FROM   auth_service.users u
JOIN   auth_service.roles r ON u.role_id = r.id
ON CONFLICT (user_id, tenant_id) DO NOTHING;
