-- Migration 081: Restrict tenant_group_mappings.role to safe tenant membership roles.
-- super_admin must not be assignable via OIDC group mappings — it is a platform
-- role and would bypass the two-role model enforced in UpsertOIDCUser.
ALTER TABLE them.tenant_group_mappings
    DROP CONSTRAINT IF EXISTS tenant_group_mappings_role_check;
ALTER TABLE them.tenant_group_mappings
    ADD CONSTRAINT tenant_group_mappings_role_check
        CHECK (role IN ('admin', 'member', 'viewer'));
