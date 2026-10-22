-- Migration 124: Add required_role to entry_points
-- Date: 2026-10-22
-- Purpose: Enable role-based access control per Entry Point

-- Add required_role column (nullable - null means no role requirement)
ALTER TABLE them.entry_points
ADD COLUMN required_role TEXT
CHECK (required_role IN ('super_admin', 'developer', 'analyst', 'viewer', 'end_user'));

-- Index for filtering by required role
CREATE INDEX idx_entry_points_required_role ON them.entry_points(required_role);

-- Comment
COMMENT ON COLUMN them.entry_points.required_role IS
'Minimum platform role required to access this Entry Point (from auth_service.roles). NULL = no role requirement (any authenticated user for user_jwt mode, or token-based access for token mode).';

-- Example usage:
-- UPDATE them.entry_points SET required_role = 'developer' WHERE slug = 'admin-api';
-- This restricts the EP to users with role_id pointing to auth_service.roles.name = 'developer'
