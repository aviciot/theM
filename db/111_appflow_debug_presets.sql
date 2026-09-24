-- db/111_appflow_debug_presets.sql
-- Per-user, per-application saved presets for the AppFlow debug panel
-- (docs/APP_CANVAS_DEBUG_PLAN.md) — lets a user save the entry point, test
-- message, step mode, and per-node LLM overrides they typed into the debug
-- panel, and reload them next time instead of re-entering everything.
--
-- Scope: tenant_id + user_id + application_id. Presets are personal — a
-- user only ever sees their own presets, even within the same tenant/app.
--
-- llm_overrides mirrors debugStartBody.LLMOverrides (go/internal/admin/appflow_debug.go)
-- keyed by canvas node_id: {mode, provider, key_id, model, api_key_encrypted,
-- base_url}. Custom-mode api_key is Fernet-encrypted before storage (same
-- scheme as them.llm_provider_keys / them.app_mcp_credentials) — the field
-- is named api_key_encrypted inside the JSONB to make clear at the storage
-- layer that a raw key must never be written there.

CREATE TABLE IF NOT EXISTS them.appflow_debug_presets (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    user_id          BIGINT      NOT NULL,
    application_id   UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    name             TEXT        NOT NULL,
    entry_point_slug TEXT        NOT NULL,
    user_message     TEXT        NOT NULL DEFAULT '',
    step_mode        BOOLEAN     NOT NULL DEFAULT false,
    llm_overrides    JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id, application_id, name)
);

CREATE INDEX IF NOT EXISTS appflow_debug_presets_lookup_idx
    ON them.appflow_debug_presets (tenant_id, user_id, application_id);

-- ── Ownership / grants ─────────────────────────────────────────────────────────

ALTER TABLE them.appflow_debug_presets OWNER TO them_owner;

GRANT SELECT, INSERT, UPDATE, DELETE ON them.appflow_debug_presets TO them_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.appflow_debug_presets TO them_app;

ALTER TABLE them.appflow_debug_presets ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.appflow_debug_presets FORCE ROW LEVEL SECURITY;

CREATE POLICY appflow_debug_presets_tenant_isolation ON them.appflow_debug_presets
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('111_appflow_debug_presets', 'Per-user, per-application saved presets for the AppFlow debug panel', NOW())
ON CONFLICT (version) DO NOTHING;
