BEGIN;
INSERT INTO them.config (config_key, config_value, updated_at)
VALUES (
    'system_agents',
    '{"roles": {"classifier": {"enabled": false, "provider": null, "model": null, "base_url": null, "system_prompt": null, "api_key_encrypted": null}}}',
    NOW()
)
ON CONFLICT (config_key) DO NOTHING;
COMMIT;
