-- Step 6: Managed Apps foundation
-- Adds app_type/version to applications; creates managed_app_params and managed_app_bindings.

ALTER TABLE them.applications
  ADD COLUMN IF NOT EXISTS app_type TEXT NOT NULL DEFAULT 'tenant'
    CHECK (app_type IN ('tenant', 'managed')),
  ADD COLUMN IF NOT EXISTS version TEXT NOT NULL DEFAULT '1.0.0',
  ADD COLUMN IF NOT EXISTS changelog TEXT;

CREATE TABLE IF NOT EXISTS them.managed_app_params (
  id            UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  app_id        UUID    NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  key           TEXT    NOT NULL,
  label         TEXT    NOT NULL,
  description   TEXT,
  param_type    TEXT    NOT NULL CHECK (param_type IN ('string','secret','integer','enum','boolean')),
  enum_values   TEXT[],
  required      BOOLEAN NOT NULL DEFAULT true,
  default_value TEXT,
  sort_order    INTEGER NOT NULL DEFAULT 0,
  UNIQUE (app_id, key)
);

CREATE TABLE IF NOT EXISTS them.managed_app_bindings (
  id          UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  app_id      UUID    NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  tenant_id   UUID    NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  config      JSONB   NOT NULL DEFAULT '{}',
  secrets_enc BYTEA,
  app_version TEXT    NOT NULL DEFAULT 'latest',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (app_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_managed_app_bindings_tenant ON them.managed_app_bindings (tenant_id, enabled);
CREATE INDEX IF NOT EXISTS idx_managed_app_params_app      ON them.managed_app_params (app_id, sort_order);
