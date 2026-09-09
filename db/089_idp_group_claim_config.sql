-- Migration 089: Apply migration 081 (missed in original run) and note new JSONB fields.
-- Migration 081 restricted tenant_group_mappings.role to safe values.
-- Apply it now (idempotent — drops and re-adds the constraint).
ALTER TABLE them.tenant_group_mappings
    DROP CONSTRAINT IF EXISTS tenant_group_mappings_role_check;
ALTER TABLE them.tenant_group_mappings
    ADD CONSTRAINT tenant_group_mappings_role_check
        CHECK (role IN ('admin', 'member', 'viewer'));

-- The idp_config JSONB column on them.tenants now supports two optional fields:
--   "groups_claim"     text   — OIDC claim name to read group values from (default: "groups")
--   "unmatched_action" text   — what to do when no group mapping matches: "viewer" (default) or "deny"
-- No schema change needed (JSONB is schemaless); documented here for audit purposes.
