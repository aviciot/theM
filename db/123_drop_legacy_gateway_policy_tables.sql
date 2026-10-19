-- db/123_drop_legacy_gateway_policy_tables.sql
-- Drop legacy gateway policy tables after governance_policies migration (slice 1b/1c)
--
-- Prerequisites: db/122_governance_policies.sql (applied, data migrated)
--
-- Context: Slice 1b (commit 3c1134ee) migrated all policy data to governance_policies
-- and updated all code to read from the new table. The old tables are unused since then.
-- This migration cleans up the legacy schema.
--
-- Safety: Once applied, rollback to pre-slice-1b code will fail (old tables gone).
-- Verify first: no rows in old tables, no recent reads in logs.

-- ── 1. Drop foreign key from gateway_clients ─────────────────────────────────
-- The policy_profile_id column references gateway_policy_profiles (legacy).
-- Must drop the FK before dropping the referenced table.
ALTER TABLE them.gateway_clients
    DROP CONSTRAINT IF EXISTS gateway_clients_policy_profile_fk;

-- ── 2. Drop the policy_profile_id column ─────────────────────────────────────
-- This column is unused since slice 1b. All new assignments use governance_profile_id.
ALTER TABLE them.gateway_clients
    DROP COLUMN IF EXISTS policy_profile_id;

-- ── 3. Drop legacy policy tables ─────────────────────────────────────────────
-- gateway_policy_profiles: named policy bundles (replaced by governance_policies scope_type='profile')
DROP TABLE IF EXISTS them.gateway_policy_profiles CASCADE;

-- gateway_policies: per-tenant policy (replaced by governance_policies scope_type='tenant')
DROP TABLE IF EXISTS them.gateway_policies CASCADE;

-- Note: gateway_clients.governance_profile_id FK to governance_policies remains intact.
-- That FK was added in db/122_governance_policies.sql and is the active reference.
