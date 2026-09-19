-- db/100_llm_gateway.sql
-- LLM Gateway Phase 1 — gateway_clients, gateway_requests, gateway_profiles,
-- gateway_profile_steps, gateway_request_bodies, gateway_policies tables.
--
-- Prerequisites: db/092_tenant_runtime_config.sql (RLS boilerplate pattern)
--
-- Design: docs/LLM_GATEWAY_DESIGN.md §12 (tables) and §7 (tenant isolation).
-- All tables use the standard ENABLE + FORCE RLS + explicit GRANTs pattern;
-- BYPASSRLS does NOT grant table privileges, so both them_app and them_admin
-- get explicit GRANTs on every table.
--
-- Note on token identity: them.access_tokens.id is UUID; the bearer token
-- cache uses token_hash (sha256-hex) as the lookup key. gateway_clients stores
-- the token_hash to identify which token a client registered with. There is no
-- FK (token_hash is not the PK of access_tokens), but the value is stable and
-- matches what the auth cache uses.

-- ── 1. Gateway clients (closed-agent registry) ────────────────────────────────
-- One row per "closed agent" (cron job, script, internal service) that routes
-- its LLM calls through the gateway. Identity is a bearer token; we store
-- the token_hash so the gateway can correlate calls to a named client.
CREATE TABLE IF NOT EXISTS them.gateway_clients (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,  -- sha256-hex of the bearer token
    label       TEXT        NOT NULL,         -- human-readable agent name
    profile_id  UUID,                         -- FK set in step 3 below
    last_seen   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_clients_tenant_idx ON them.gateway_clients(tenant_id);
CREATE INDEX IF NOT EXISTS gateway_clients_hash_idx   ON them.gateway_clients(token_hash);

GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_clients TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_clients TO them_admin;

ALTER TABLE them.gateway_clients ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.gateway_clients FORCE ROW LEVEL SECURITY;

CREATE POLICY gc_tenant_isolation ON them.gateway_clients
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── 2. Gateway requests (one row per LLM call) ────────────────────────────────
-- The metering table. Written once per call in a defer (§10 rule 1 — no
-- connection held across the LLM call). Drives the Requests screen, spend
-- reports, and budget totals.
CREATE TABLE IF NOT EXISTS them.gateway_requests (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    client_id       UUID        REFERENCES them.gateway_clients(id)  ON DELETE SET NULL,
    token_hash      TEXT,       -- sha256-hex; retained even when client is deleted
    provider        TEXT,       -- resolved provider name (e.g. 'anthropic', 'openai')
    model_requested TEXT,       -- model string from the client request
    model_served    TEXT,       -- model actually forwarded upstream
    status          TEXT        NOT NULL DEFAULT 'ok',  -- 'ok', 'error', 'blocked', 'rate_limited'
    http_status     INT,
    error_code      TEXT,
    tokens_in       INT         NOT NULL DEFAULT 0,
    tokens_out      INT         NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12,8) NOT NULL DEFAULT 0,
    latency_ms      INT,
    ttfb_ms         INT,        -- time-to-first-byte for streaming
    streamed        BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gw_req_tenant_created_idx ON them.gateway_requests(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS gw_req_client_idx         ON them.gateway_requests(client_id);

GRANT SELECT, INSERT ON them.gateway_requests TO them_app;
GRANT SELECT, INSERT ON them.gateway_requests TO them_admin;

ALTER TABLE them.gateway_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.gateway_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY gw_req_tenant_isolation ON them.gateway_requests
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── 3. Gateway profiles (named policy bundles) ────────────────────────────────
-- "Payments-Strict", "Internal-Basic", "Dev-Sandbox", etc.
-- Phase 1 ships with zero configured steps (plain metering only).
CREATE TABLE IF NOT EXISTS them.gateway_profiles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    enabled     BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_profiles TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_profiles TO them_admin;

ALTER TABLE them.gateway_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.gateway_profiles FORCE ROW LEVEL SECURITY;

CREATE POLICY gw_prof_tenant_isolation ON them.gateway_profiles
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── 4. Gateway profile steps (ordered components) ────────────────────────────
-- Each row is one component in a profile — position is load-bearing (PII filter
-- MUST be before the LLM call). def_id references them.middleware_defs.
-- Phase 1: table exists, no rows will be created (pipeline runs zero steps).
CREATE TABLE IF NOT EXISTS them.gateway_profile_steps (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  UUID        NOT NULL REFERENCES them.gateway_profiles(id) ON DELETE CASCADE,
    def_id      UUID        NOT NULL REFERENCES them.middleware_defs(id),
    position    INT         NOT NULL,
    config      JSONB       NOT NULL DEFAULT '{}',
    UNIQUE (profile_id, position)
);

-- No per-row tenant_id here — isolation is through gateway_profiles.
GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_profile_steps TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.gateway_profile_steps TO them_admin;

-- ── 5. Gateway policies (per-tenant model allow-list + budget) ────────────────
-- Phase 1: table exists. allowed_models NULL = allow all.
CREATE TABLE IF NOT EXISTS them.gateway_policies (
    tenant_id               UUID        PRIMARY KEY REFERENCES them.tenants(id) ON DELETE CASCADE,
    allowed_models          TEXT[],     -- NULL = allow all; non-null = whitelist
    model_aliases           JSONB       NOT NULL DEFAULT '{}', -- {"fast":"claude-haiku-4-5-20251001"}
    max_tokens_per_request  INT,        -- NULL = no per-request cap
    monthly_budget_usd      NUMERIC(10,2), -- NULL = no budget cap
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON them.gateway_policies TO them_app;
GRANT SELECT, INSERT, UPDATE ON them.gateway_policies TO them_admin;

ALTER TABLE them.gateway_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.gateway_policies FORCE ROW LEVEL SECURITY;

CREATE POLICY gw_pol_tenant_isolation ON them.gateway_policies
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── 6. Gateway request bodies (opt-in encrypted text capture) ─────────────────
-- Stored separately so the metering path (gateway_requests) never touches them
-- and so retention deletes are cheap. Phase 4 enables the UI toggles.
CREATE TABLE IF NOT EXISTS them.gateway_request_bodies (
    request_id      UUID        PRIMARY KEY REFERENCES them.gateway_requests(id) ON DELETE CASCADE,
    tenant_id       UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    request_text    TEXT,       -- Fernet-encrypted prompt (NULL = not captured)
    response_text   TEXT,       -- Fernet-encrypted completion (NULL = not captured)
    retain_until    TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '7 days'),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gw_req_bodies_retain_idx ON them.gateway_request_bodies(retain_until);

GRANT SELECT, INSERT, DELETE ON them.gateway_request_bodies TO them_app;
GRANT SELECT, INSERT, DELETE ON them.gateway_request_bodies TO them_admin;

ALTER TABLE them.gateway_request_bodies ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.gateway_request_bodies FORCE ROW LEVEL SECURITY;

CREATE POLICY gw_body_tenant_isolation ON them.gateway_request_bodies
    AS PERMISSIVE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── 7. Wire profile FK on gateway_clients ─────────────────────────────────────
ALTER TABLE them.gateway_clients
    ADD CONSTRAINT gateway_clients_profile_fk
    FOREIGN KEY (profile_id) REFERENCES them.gateway_profiles(id) ON DELETE SET NULL;
