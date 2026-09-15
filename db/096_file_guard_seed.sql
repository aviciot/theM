-- Migration 096: Seed builtin file-guard middleware definition.
-- middleware_defs inherits from component_definitions via FK on id.
-- Must insert component_definitions row first, then middleware_defs with same UUID.
DO $$
DECLARE
    v_id UUID := gen_random_uuid();
BEGIN
    -- Skip if already seeded
    IF EXISTS (SELECT 1 FROM them.middleware_defs WHERE slug = 'file-guard') THEN
        RETURN;
    END IF;

    INSERT INTO them.component_definitions (
        id, kind, namespace, name, version, display_name, description,
        implementation_type, configuration_schema, default_config,
        capabilities, credential_schema, scope, tenant_id, status, content_hash
    ) VALUES (
        v_id,
        'middleware',
        'builtin',
        'file-guard',
        1,
        'File Guard',
        'Intercepts file artifacts produced by A2A agents, quarantines them, and runs the configured processor pipeline before delivery.',
        'builtin',
        '{
            "type": "object",
            "properties": {
                "enabled":          { "type": "boolean" },
                "mode":             { "type": "string", "enum": ["warn", "block"] },
                "max_file_size_mb": { "type": "integer", "minimum": 1 },
                "allowed_types":    { "type": "array", "items": { "type": "string" } },
                "blocked_types":    { "type": "array", "items": { "type": "string" } },
                "notify_on_fail":   { "type": "boolean" }
            }
        }'::jsonb,
        '{
            "enabled": false,
            "mode": "block",
            "max_file_size_mb": 5,
            "allowed_types": [],
            "blocked_types": ["exe", "sh", "bat", "ps1", "cmd"],
            "notify_on_fail": true,
            "processors": {
                "av_scan":       { "enabled": true, "max_bytes": 5242880, "block_on_infected": true },
                "audit_capture": { "enabled": true }
            }
        }'::jsonb,
        '[]'::jsonb,
        '[]'::jsonb,
        'builtin',
        NULL,
        'published',
        ''
    );

    INSERT INTO them.middleware_defs (
        id, slug, kind, display_name, description, config,
        is_builtin, enabled, namespace, version, scope, status, content_hash
    ) VALUES (
        v_id,
        'file-guard',
        'guard',
        'File Guard',
        'Intercepts file artifacts produced by A2A agents, quarantines them, and runs the configured processor pipeline before delivery.',
        '{
            "enabled": false,
            "mode": "block",
            "max_file_size_mb": 5,
            "allowed_types": [],
            "blocked_types": ["exe", "sh", "bat", "ps1", "cmd"],
            "notify_on_fail": true,
            "processors": {
                "av_scan":       { "enabled": true, "max_bytes": 5242880, "block_on_infected": true },
                "audit_capture": { "enabled": true }
            }
        }'::jsonb,
        true,
        true,
        'builtin',
        1,
        'builtin',
        'published',
        ''
    );
END $$;
