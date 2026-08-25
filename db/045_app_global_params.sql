-- db/045_app_global_params.sql
-- Adds app_params JSONB to applications for app-level global named parameters.
-- Modelled after provider_keys: AES-GCM encrypted secrets, plaintext non-secrets.
--
-- Storage format (one key per named param):
--   Secrets:     {"geoapify_key": {"ct": "enc:...", "hint": "XXXX"}}
--   Non-secrets: {"target_city":  "Tel Aviv"}
--
-- hint = last 4 chars of plaintext (extracted before encryption, matching provider_keys convention).

ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS app_params JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN them.applications.app_params IS
    'App-level global named parameters. Secrets: {"name": {"ct": "enc:...", "hint": "XXXX"}}. '
    'Non-secrets: {"name": "value"}. Encrypted with the platform crypto key.';
