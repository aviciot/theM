-- Seed default data — safe to re-run

INSERT INTO odin.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES (
    'anthropic',
    'Anthropic Claude',
    'claude-sonnet-4-6',
    '{"claude-sonnet-4-6": {"input": 3.00, "output": 15.00}, "claude-opus-4-8": {"input": 15.00, "output": 75.00}, "claude-haiku-4-5": {"input": 0.80, "output": 4.00}}',
    true
) ON CONFLICT (name) DO NOTHING;

INSERT INTO odin.config (config_key, config_value)
VALUES ('llm_routing', '{"provider": "anthropic", "model": "claude-sonnet-4-6", "max_tokens": 4096}')
ON CONFLICT (config_key) DO NOTHING;
