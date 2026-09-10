-- Migration 091: add logo_url to them.tenants
ALTER TABLE them.tenants ADD COLUMN IF NOT EXISTS logo_url TEXT;
