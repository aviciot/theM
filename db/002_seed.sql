-- Seed default data — safe to re-run (all inserts use ON CONFLICT DO NOTHING)

-- ── LLM Providers ────────────────────────────────────────────────────────────

INSERT INTO them.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES (
    'anthropic',
    'Anthropic Claude',
    'claude-sonnet-4-6',
    '{"claude-sonnet-4-6": {"input": 3.00, "output": 15.00}, "claude-opus-4-8": {"input": 15.00, "output": 75.00}, "claude-haiku-4-5-20251001": {"input": 0.80, "output": 4.00}}',
    true
) ON CONFLICT (name) DO NOTHING;

INSERT INTO them.config (config_key, config_value)
VALUES ('llm_routing', '{"provider": "anthropic", "model": "claude-sonnet-4-6", "max_tokens": 4096}')
ON CONFLICT (config_key) DO NOTHING;

-- ── Vision Agent ─────────────────────────────────────────────────────────────
-- Always-on agent (no profile). Container: vision-agent on port 9100.

INSERT INTO them.agents (slug, display_name, description, transport, endpoint_url, enabled, supports_streaming)
VALUES (
    'vision_agent',
    'Vision Agent',
    'Takes a real-world address and building description, fetches a street-level photo via Google Street View, then uses FLUX.1 Kontext AI to render the described building into the real photo. Returns a photorealistic composite image.',
    'a2a_async',
    'http://vision-agent:9100',
    true,
    false
)
ON CONFLICT (slug) DO UPDATE SET
    display_name       = EXCLUDED.display_name,
    description        = EXCLUDED.description,
    endpoint_url       = EXCLUDED.endpoint_url,
    enabled            = EXCLUDED.enabled,
    supports_streaming = EXCLUDED.supports_streaming;

-- ── A2A Test Agents (enable profile: test-agents) ────────────────────────────
-- These match the a2a-* containers in docker-compose.yml under profile: test-agents.
-- transport=a2a_async — handled by A2aAsyncAdapter.

INSERT INTO them.agents (slug, display_name, description, transport, endpoint_url, enabled, supports_streaming)
VALUES
    (
        'a2a_echo',
        'A2A Echo',
        'Echoes the input message verbatim. A2A v1.0 test agent for basic task lifecycle validation.',
        'a2a_async',
        'http://a2a-echo:9200',
        false,
        false
    ),
    (
        'a2a_slow',
        'A2A Slow',
        'Waits 5 seconds before completing. Tests deadline enforcement and async delegation.',
        'a2a_async',
        'http://a2a-slow:9201',
        false,
        false
    ),
    (
        'a2a_stream',
        'A2A Stream',
        'Streams a response word by word via artifact chunks. Tests SSE streaming and artifact assembly.',
        'a2a_async',
        'http://a2a-stream:9202',
        false,
        true
    )
ON CONFLICT (slug) DO UPDATE SET
    display_name       = EXCLUDED.display_name,
    description        = EXCLUDED.description,
    endpoint_url       = EXCLUDED.endpoint_url,
    supports_streaming = EXCLUDED.supports_streaming;

-- ── Default Orchestrator ──────────────────────────────────────────────────────
-- Wires the vision agent as the default always-on agent.

WITH agent_ids AS (
    SELECT ARRAY_AGG(id) AS ids
    FROM them.agents
    WHERE slug IN ('vision_agent')
)
INSERT INTO them.orchestrators (
    name, display_name, system_prompt,
    allowed_agent_ids, llm_provider, llm_model,
    max_iterations, max_parallel_tools, rate_limit_rpm, daily_budget_usd, enabled
)
SELECT
    'default',
    'Default Orchestrator',
    'You are a helpful orchestrator with access to a Vision Agent that can visualize real-world locations. Use it when the user provides an address and a building description to generate a photorealistic composite image.',
    agent_ids.ids,
    'anthropic',
    'claude-sonnet-4-6',
    10, 4, 30, 0, true
FROM agent_ids
ON CONFLICT (name) DO UPDATE SET
    display_name       = EXCLUDED.display_name,
    system_prompt      = EXCLUDED.system_prompt,
    allowed_agent_ids  = EXCLUDED.allowed_agent_ids,
    llm_provider       = EXCLUDED.llm_provider,
    llm_model          = EXCLUDED.llm_model,
    max_iterations     = EXCLUDED.max_iterations,
    max_parallel_tools = EXCLUDED.max_parallel_tools,
    rate_limit_rpm     = EXCLUDED.rate_limit_rpm,
    daily_budget_usd   = EXCLUDED.daily_budget_usd,
    enabled            = EXCLUDED.enabled;
