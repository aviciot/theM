-- Migration 126: entry_points.required_role holds a tenant role name (them.tenant_roles.name),
-- not one of the old platform role names. Drop the CHECK added in 124 — it rejected every
-- tenant role (e.g. 'jwt-premium'); PATCH .../required-role surfaced that as a 404.
ALTER TABLE them.entry_points DROP CONSTRAINT IF EXISTS entry_points_required_role_check;
