-- db/107_seed_gemini_groq_providers.sql
-- Platform-default llm_providers rows only ever seeded anthropic (002) and
-- openai (003) — gemini and groq were fully supported everywhere else
-- (settingsConstants.ts PROVIDER_MODELS, dispatchLLMText, probeLLMWithBase,
-- fetchGeminiModels/fetchOpenAICompatModels) but never had a row to enable,
-- so they never appeared in the LLM Providers tab. Seeds both, disabled by
-- default (tenant/super_admin must opt in), matching the openai row's
-- enabled=false precedent from 003_phase8.sql.

-- Platform-default rows have tenant_id IS NULL, enforced by the partial unique
-- index llm_providers_name_platform_uq (added 057) rather than a plain
-- UNIQUE(name) — ON CONFLICT must target that index explicitly.
INSERT INTO them.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES (
    'gemini',
    'Google Gemini',
    'gemini-2.0-flash',
    '{"gemini-2.0-flash": {"input": 0.10, "output": 0.40}, "gemini-1.5-pro": {"input": 1.25, "output": 5.00}, "gemini-1.5-flash": {"input": 0.075, "output": 0.30}}',
    false
) ON CONFLICT (name) WHERE tenant_id IS NULL DO NOTHING;

INSERT INTO them.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES (
    'groq',
    'Groq',
    'llama-3.3-70b-versatile',
    '{"llama-3.3-70b-versatile": {"input": 0.59, "output": 0.79}, "llama-3.1-8b-instant": {"input": 0.05, "output": 0.08}, "mixtral-8x7b-32768": {"input": 0.24, "output": 0.24}}',
    false
) ON CONFLICT (name) WHERE tenant_id IS NULL DO NOTHING;

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('107_seed_gemini_groq_providers', 'Seed missing platform-default gemini and groq llm_providers rows', NOW())
ON CONFLICT (version) DO NOTHING;
