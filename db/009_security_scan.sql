-- Phase 12: Agent Security Scanner
-- Adds scan-result columns to them.agents + seeds the security_scanner agent row.
-- Safe to re-run.

-- ── Columns ──────────────────────────────────────────────────────────────────
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS last_scan_at     TIMESTAMPTZ;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS last_scan_result JSONB;

-- ── Seed the scanner agent ───────────────────────────────────────────────────
-- enabled=false: NOT an orchestrator tool — the scan endpoint calls it directly
-- by slug regardless of enabled state.
INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url, auth_token_encrypted,
    enabled, supports_streaming, timeout_seconds,
    input_schema, skills
)
VALUES (
    'security_scanner',
    'Security Scanner',
    'Internal agent that audits other registered agents for security posture: TLS, '
    'auth enforcement, reachability, and LLM analysis of agent card and skill scope. '
    'Invoked from the admin Agents page via the 🛡️ Scan action.',
    'a2a_async',
    'http://them-security-agent:9500',
    NULL,
    false,
    false,
    120,
    '{}'::jsonb,
    '[{"id":"scan_agent","name":"Scan Agent","description":"Audit a registered agent for security risk — HTTP probes + LLM card/skill analysis.","tags":["security","audit"],"inputModes":["application/json"],"outputModes":["application/json"]}]'::jsonb
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    transport       = EXCLUDED.transport,
    timeout_seconds = EXCLUDED.timeout_seconds,
    skills          = EXCLUDED.skills;
