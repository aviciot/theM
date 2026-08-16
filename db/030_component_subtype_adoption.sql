-- Migration 030: agent + middleware subtype adoption into component_definitions
-- Phase A of Application v2: Registry-Backed Application Component Model
-- Adds namespace/version/scope/status/content_hash to agents + middleware_defs,
-- inserts base rows into component_definitions (same UUID), adds FK constraints,
-- seeds builtin llm-orchestrator definition and 5 builtin entry_point palette rows.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. Add subtype contract columns to agents (nullable first for backfill)
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.agents
    ADD COLUMN namespace     TEXT,
    ADD COLUMN version       INTEGER DEFAULT 1,
    ADD COLUMN scope         TEXT DEFAULT 'tenant',
    ADD COLUMN status        TEXT DEFAULT 'published',
    ADD COLUMN content_hash  TEXT DEFAULT '';

-- Backfill namespace: 'them.tenant.<tenant_id>'
UPDATE them.agents
SET namespace = 'them.tenant.' || tenant_id::text;

-- Backfill content_hash: deterministic per agent
UPDATE them.agents
SET content_hash = encode(sha256((id::text || ':agent:v1')::bytea), 'hex');

-- Make non-null now that backfill is done
ALTER TABLE them.agents
    ALTER COLUMN namespace    SET NOT NULL,
    ALTER COLUMN version      SET NOT NULL,
    ALTER COLUMN scope        SET NOT NULL,
    ALTER COLUMN status       SET NOT NULL,
    ALTER COLUMN content_hash SET NOT NULL;

-- Add CHECK constraints
ALTER TABLE them.agents
    ADD CONSTRAINT ck_agents_scope  CHECK (scope IN ('builtin','tenant')),
    ADD CONSTRAINT ck_agents_status CHECK (status IN ('draft','published','deprecated'));

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. Add subtype contract columns to middleware_defs (nullable first for backfill)
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE them.middleware_defs
    ADD COLUMN namespace     TEXT,
    ADD COLUMN version       INTEGER DEFAULT 1,
    ADD COLUMN scope         TEXT DEFAULT 'tenant',
    ADD COLUMN status        TEXT DEFAULT 'published',
    ADD COLUMN content_hash  TEXT DEFAULT '';

-- Backfill namespace: builtin → 'them.builtin', otherwise bootstrap tenant namespace
UPDATE them.middleware_defs
SET namespace = CASE
    WHEN is_builtin THEN 'them.builtin'
    ELSE 'them.tenant.00000000-0000-0000-0000-000000000001'
END;

-- Backfill content_hash: deterministic per middleware_def
UPDATE them.middleware_defs
SET content_hash = encode(sha256((id::text || ':middleware:v1')::bytea), 'hex');

-- Builtin middleware → scope = 'builtin'
UPDATE them.middleware_defs
SET scope = CASE WHEN is_builtin THEN 'builtin' ELSE 'tenant' END;

-- Make non-null now that backfill is done
ALTER TABLE them.middleware_defs
    ALTER COLUMN namespace    SET NOT NULL,
    ALTER COLUMN version      SET NOT NULL,
    ALTER COLUMN scope        SET NOT NULL,
    ALTER COLUMN status       SET NOT NULL,
    ALTER COLUMN content_hash SET NOT NULL;

-- Add CHECK constraints
ALTER TABLE them.middleware_defs
    ADD CONSTRAINT ck_mw_defs_scope  CHECK (scope IN ('builtin','tenant')),
    ADD CONSTRAINT ck_mw_defs_status CHECK (status IN ('draft','published','deprecated'));

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Insert component_definitions base rows for each existing agent
--    (same UUID as agents.id — the subtype FK will reference these)
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.component_definitions (
    id, kind, namespace, name, version,
    display_name, description, implementation_type,
    configuration_schema, default_config, capabilities,
    input_schema, output_schema, credential_schema,
    scope, tenant_id, status, content_hash, enabled,
    created_at, published_at
)
SELECT
    a.id,
    'agent',
    a.namespace,
    a.slug,
    a.version,
    a.display_name,
    NULLIF(a.description, ''),
    'a2a_async',
    COALESCE(a.input_schema, '{}'::jsonb),   -- use input_schema as configuration_schema proxy
    '{}'::jsonb,
    CASE WHEN a.skills IS NOT NULL THEN a.skills ELSE '[]'::jsonb END,
    a.input_schema,
    NULL,                                    -- no output_schema on agents yet
    '[]'::jsonb,
    a.scope::text,
    CASE WHEN a.scope = 'builtin' THEN NULL ELSE a.tenant_id END,
    a.status::text,
    a.content_hash,
    a.enabled,
    a.created_at,
    a.created_at                             -- treat created_at as published_at for existing agents
FROM them.agents a;

-- Add FK: agents.id → component_definitions.id (subtype pattern)
ALTER TABLE them.agents
    ADD CONSTRAINT fk_agents_base_def FOREIGN KEY (id) REFERENCES them.component_definitions(id);

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Insert component_definitions base rows for each existing middleware_def
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.component_definitions (
    id, kind, namespace, name, version,
    display_name, description, implementation_type,
    configuration_schema, default_config, capabilities,
    input_schema, output_schema, credential_schema,
    scope, tenant_id, status, content_hash, enabled,
    created_at, published_at
)
SELECT
    m.id,
    'middleware',
    m.namespace,
    m.slug,
    m.version,
    m.display_name,
    NULLIF(m.description, ''),
    m.kind,       -- 'guard' or 'cache' — this is the middleware implementation type
    COALESCE(m.config, '{}'::jsonb),
    '{}'::jsonb,
    '[]'::jsonb,
    NULL,
    NULL,
    '[]'::jsonb,
    m.scope::text,
    CASE WHEN m.scope = 'builtin' THEN NULL
         ELSE '00000000-0000-0000-0000-000000000001'::uuid
    END,
    m.status::text,
    m.content_hash,
    m.enabled,
    m.created_at,
    m.created_at
FROM them.middleware_defs m;

-- Add FK: middleware_defs.id → component_definitions.id (subtype pattern)
ALTER TABLE them.middleware_defs
    ADD CONSTRAINT fk_mw_defs_base_def FOREIGN KEY (id) REFERENCES them.component_definitions(id);

-- ─────────────────────────────────────────────────────────────────────────────
-- 5. Seed builtin llm-orchestrator definition
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.component_definitions (
    id, kind, namespace, name, version,
    display_name, description, implementation_type,
    configuration_schema, default_config, capabilities,
    input_schema, output_schema, credential_schema,
    scope, tenant_id, status, content_hash, enabled, published_at
)
VALUES (
    gen_random_uuid(),
    'orchestrator', 'them.builtin', 'llm-orchestrator', 1,
    'LLM Orchestrator', 'Standard LLM-loop orchestrator (plan→act→observe)',
    'llm_loop',
    '{"type":"object","properties":{"system_prompt":{"type":"string"},"llm":{"type":"object"},"max_iterations":{"type":"integer","default":10},"max_parallel_tools":{"type":"integer","default":3},"history_window":{"type":"integer","default":20},"budget_tokens":{"type":"integer"},"role":{"type":"string","enum":["root","sub"],"default":"root"}}}'::jsonb,
    '{"max_iterations":10,"max_parallel_tools":3,"history_window":20,"role":"root"}'::jsonb,
    '["delegation.target","tool.host"]'::jsonb,
    NULL, NULL, '[]'::jsonb,
    'builtin', NULL, 'published',
    encode(sha256('llm-orchestrator-v1-builtin'::bytea), 'hex'),
    true, now()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- 6. Seed 5 builtin entry_point palette rows (Canvas metadata)
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.component_definitions (
    kind, namespace, name, version,
    display_name, description, implementation_type,
    configuration_schema, default_config, capabilities,
    input_schema, output_schema, credential_schema,
    scope, tenant_id, status, content_hash, enabled, published_at
)
VALUES
    ('entry_point','them.builtin','websocket',1,
     'WebSocket','Bidirectional real-time connection','websocket',
     '{"type":"object"}'::jsonb, '{}'::jsonb, '["streaming","bidirectional"]'::jsonb,
     NULL, NULL, '[]'::jsonb,
     'builtin', NULL, 'published', encode(sha256('ep-websocket-v1'::bytea),'hex'), true, now()),

    ('entry_point','them.builtin','sse',1,
     'Server-Sent Events','Server-to-client streaming','sse',
     '{"type":"object"}'::jsonb, '{}'::jsonb, '["streaming"]'::jsonb,
     NULL, NULL, '[]'::jsonb,
     'builtin', NULL, 'published', encode(sha256('ep-sse-v1'::bytea),'hex'), true, now()),

    ('entry_point','them.builtin','a2a',1,
     'A2A (Agent-to-Agent)','JSON-RPC 2.0 A2A protocol','a2a',
     '{"type":"object"}'::jsonb, '{}'::jsonb, '["a2a"]'::jsonb,
     NULL, NULL, '[]'::jsonb,
     'builtin', NULL, 'published', encode(sha256('ep-a2a-v1'::bytea),'hex'), true, now()),

    ('entry_point','them.builtin','webrtc',1,
     'WebRTC','WebRTC voice/video connection','webrtc',
     '{"type":"object"}'::jsonb, '{}'::jsonb, '["streaming","voice","webrtc"]'::jsonb,
     NULL, NULL, '[]'::jsonb,
     'builtin', NULL, 'published', encode(sha256('ep-webrtc-v1'::bytea),'hex'), true, now()),

    ('entry_point','them.builtin','voice',1,
     'Voice','Voice/TTS protocol','voice',
     '{"type":"object"}'::jsonb, '{}'::jsonb, '["voice"]'::jsonb,
     NULL, NULL, '[]'::jsonb,
     'builtin', NULL, 'published', encode(sha256('ep-voice-v1'::bytea),'hex'), true, now());

-- ─────────────────────────────────────────────────────────────────────────────
-- 7. Record migration
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO them.schema_migrations (version, description)
VALUES ('030', 'agent+middleware subtype adoption into component_definitions, builtin llm-orchestrator + 5 entry_point palette rows seeded');

COMMIT;
