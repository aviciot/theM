-- docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 1-2: PII redaction + prompt-injection
-- detection get their own middleware_defs rows, matching every other guard's
-- 1-def-per-processor pattern (av_scan/file-guard) -- confirmed with the
-- user 2026-09-27 rather than keeping the old combined "guard_default" row
-- (which bundled both checks under one config and predates the node-contract
-- shape / config_fields / middleware_wirings model entirely).
--
-- guard_default is left in place (never delete a row other code might still
-- reference) but disabled, with a description pointing at its replacements --
-- same non-destructive precedent as other superseded-row migrations in this
-- project.
--
-- middleware_defs.id has a real FK into component_definitions(id)
-- (fk_mw_defs_base_def) -- confirmed live: file-guard's own row exists in
-- BOTH tables under the same UUID. So each new guard needs a
-- component_definitions row inserted first, then a middleware_defs row
-- reusing that same id -- same two-table shape file-guard already uses.

UPDATE them.middleware_defs
SET enabled = false,
    description = 'Superseded by pii_redact + prompt_inject (docs/APPFLOW_TEXT_GUARDS_PLAN.md). Kept for FK history only.'
WHERE slug = 'guard_default';

DO $$
DECLARE
    pii_id  UUID := gen_random_uuid();
    inj_id  UUID := gen_random_uuid();
BEGIN

INSERT INTO them.component_definitions
    (id, kind, namespace, name, version, display_name, description,
     implementation_type, configuration_schema, default_config, scope, status, enabled, content_hash)
VALUES
(
    pii_id, 'middleware', 'builtin', 'pii_redact', 1,
    'PII Guard',
    'Detects and redacts personally-identifiable information (emails, phone numbers, credit-card-like numbers, SSNs) in text produced by an llm or agent node.',
    'builtin',
    '{"type": "object", "properties": {"mode": {"enum": ["block", "redact", "warn"], "type": "string"}, "enabled": {"type": "boolean"}, "direction": {"enum": ["output", "both"], "type": "string"}, "llm_assist": {"type": "boolean"}}}'::jsonb,
    '{"enabled": false, "mode": "redact", "direction": "output", "llm_assist": false}'::jsonb,
    'builtin', 'published', true, ''
),
(
    inj_id, 'middleware', 'builtin', 'prompt_inject', 1,
    'Prompt-Injection Guard',
    'Detects known prompt-injection phrasing (instruction-override attempts, role-override attempts, delimiter escapes) in text produced by an llm or agent node.',
    'builtin',
    '{"type": "object", "properties": {"mode": {"enum": ["block", "warn"], "type": "string"}, "enabled": {"type": "boolean"}, "direction": {"enum": ["output", "both"], "type": "string"}, "sensitivity": {"enum": ["low", "medium", "high"], "type": "string"}}}'::jsonb,
    '{"enabled": false, "mode": "block", "sensitivity": "medium", "direction": "output"}'::jsonb,
    'builtin', 'published', true, ''
)
ON CONFLICT DO NOTHING;

INSERT INTO them.middleware_defs
    (id, slug, kind, display_name, description, config, is_builtin, enabled,
     namespace, scope, status, edges, config_fields)
VALUES
(
    pii_id,
    'pii_redact',
    'guard',
    'PII Guard',
    'Detects and redacts personally-identifiable information (emails, phone numbers, credit-card-like numbers, SSNs) in text produced by an llm or agent node.',
    '{"enabled": false, "mode": "redact", "direction": "output"}'::jsonb,
    true,
    true,
    'them.builtin',
    'builtin',
    'published',
    '{"min_in": 1, "max_in": 1, "min_out": 1, "max_out": 1}'::jsonb,
    '[
      {"key": "enabled", "type": "bool", "required": true, "description": "Whether PII Guard is active on this wiring.", "example": "true"},
      {"key": "mode", "type": "string", "required": true, "options": ["block", "redact", "warn"], "description": "\"redact\" masks matched PII in place; \"block\" rejects the whole response; \"warn\" logs only.", "example": "redact"},
      {"key": "direction", "type": "string", "required": false, "options": ["output", "both"], "description": "\"output\" scans what the node produced (default); \"both\" also scans the incoming prompt before it is sent.", "example": "output"},
      {"key": "llm_assist", "type": "bool", "required": false, "description": "Reserved for a future LLM-judge-based detection pass. Not implemented yet -- leave false.", "example": "false"}
    ]'::jsonb
),
(
    inj_id,
    'prompt_inject',
    'guard',
    'Prompt-Injection Guard',
    'Detects known prompt-injection phrasing (instruction-override attempts, role-override attempts, delimiter escapes) in text produced by an llm or agent node.',
    '{"enabled": false, "mode": "block", "sensitivity": "medium", "direction": "output"}'::jsonb,
    true,
    true,
    'them.builtin',
    'builtin',
    'published',
    '{"min_in": 1, "max_in": 1, "min_out": 1, "max_out": 1}'::jsonb,
    '[
      {"key": "enabled", "type": "bool", "required": true, "description": "Whether Prompt-Injection Guard is active on this wiring.", "example": "true"},
      {"key": "mode", "type": "string", "required": true, "options": ["block", "warn"], "description": "\"block\" rejects text matching a known injection pattern; \"warn\" logs only.", "example": "block"},
      {"key": "sensitivity", "type": "string", "required": false, "options": ["low", "medium", "high"], "description": "Higher sensitivity checks more, less-certain patterns -- more false positives, fewer misses.", "example": "medium"},
      {"key": "direction", "type": "string", "required": false, "options": ["output", "both"], "description": "\"output\" scans what the node produced (default); \"both\" also scans the incoming prompt before it is sent.", "example": "output"}
    ]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;

END $$;
