-- db/038_app_agent_params.sql
-- Adds agent_params JSONB to app_agent_bindings for structured, per-agent
-- runtime parameter storage. Supersedes credential_bindings (retained for compat).
--
-- Storage format:
--   Secrets:     {"key": {"ct": "enc:...", "hint": "XXXX"}}
--   Non-secrets: {"key": "plaintext_value"}
--
-- hint = last 4 chars of plaintext, extracted before encryption.
-- Secrets are encrypted with the platform Fernet key (same as provider_keys).

ALTER TABLE them.app_agent_bindings
    ADD COLUMN IF NOT EXISTS agent_params JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN them.app_agent_bindings.agent_params IS
    'Runtime parameters for this agent binding. Secrets encrypted via Fernet (enc: prefix). '
    'Format: {key: {ct: "enc:...", hint: "XXXX"}} for secrets, '
    '        {key: "value"} for non-secrets.';
