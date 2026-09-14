-- Migration 095: tenant role management
-- Adds tenant_roles, tenant_role_grants, tenant_role_mappings tables.

CREATE TABLE them.tenant_roles (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name         text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    description  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE them.tenant_role_grants (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id        uuid NOT NULL REFERENCES them.tenant_roles(id) ON DELETE CASCADE,
    application_id uuid NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (role_id, application_id)
);

CREATE TABLE them.tenant_role_mappings (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    source    text NOT NULL CHECK (source IN ('jwt_claim', 'header')),
    field     text NOT NULL,
    value     text NOT NULL,
    role_id   uuid NOT NULL REFERENCES them.tenant_roles(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source, field, value)
);

-- Indexes for runtime gate lookups
CREATE INDEX tenant_roles_tenant_idx         ON them.tenant_roles(tenant_id);
CREATE INDEX tenant_role_grants_role_idx     ON them.tenant_role_grants(role_id);
CREATE INDEX tenant_role_grants_app_idx      ON them.tenant_role_grants(application_id);
CREATE INDEX tenant_role_mappings_tenant_idx ON them.tenant_role_mappings(tenant_id);

-- RLS
ALTER TABLE them.tenant_roles         ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tenant_role_grants   ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tenant_role_mappings ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_roles_rls ON them.tenant_roles
    TO them_app
    USING (tenant_id = (NULLIF(current_setting('app.tenant_id', true), ''))::uuid);

CREATE POLICY tenant_role_grants_rls ON them.tenant_role_grants
    TO them_app
    USING (role_id IN (
        SELECT id FROM them.tenant_roles
        WHERE tenant_id = (NULLIF(current_setting('app.tenant_id', true), ''))::uuid
    ));

CREATE POLICY tenant_role_mappings_rls ON them.tenant_role_mappings
    TO them_app
    USING (tenant_id = (NULLIF(current_setting('app.tenant_id', true), ''))::uuid);

-- them_admin needs explicit grants (BYPASSRLS does not grant table access)
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_roles         TO them_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_role_grants   TO them_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_role_mappings TO them_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_roles         TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_role_grants   TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_role_mappings TO them_app;
