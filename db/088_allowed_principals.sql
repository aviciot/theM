-- Migration 088: allowed_principals on entry_points
-- Restricts which principal types may call each entry point.
-- 'internal' = opaque bearer token or the-M user JWT (default, safe for all existing EPs)
-- 'external' = backend service token with X-External-User / bank JWKS JWT (Phase 4)
-- 'both'     = no restriction

ALTER TABLE them.entry_points
  ADD COLUMN IF NOT EXISTS allowed_principals TEXT NOT NULL DEFAULT 'internal'
  CHECK (allowed_principals IN ('internal', 'external', 'both'));
