-- Migration 082: add is_bootstrap to them.tenants
-- Marks the bootstrap tenant so it cannot be deleted via the API.
-- Apply: docker cp db/082_tenant_is_bootstrap.sql them-postgres:/tmp/082.sql
--        docker exec them-postgres psql -U them -d them -f /tmp/082.sql

ALTER TABLE them.tenants
  ADD COLUMN IF NOT EXISTS is_bootstrap boolean NOT NULL DEFAULT false;

-- Mark the well-known bootstrap tenant.
UPDATE them.tenants
  SET is_bootstrap = true
  WHERE id = '00000000-0000-0000-0000-000000000001';
