-- Migration 033: user preferences store
-- Adds a jsonb preferences column to auth_service.users.
-- Designed as a general-purpose key/value bag so future UI settings
-- (theme, layout, notification prefs, etc.) can be added without schema changes.

ALTER TABLE auth_service.users
  ADD COLUMN IF NOT EXISTS preferences jsonb NOT NULL DEFAULT '{}';
