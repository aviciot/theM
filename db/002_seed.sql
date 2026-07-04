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

-- ── Mock Agents (dev/test only) ───────────────────────────────────────────────
-- These match the mock-agent-* containers in docker-compose.yml.
-- endpoint_url points to container-internal hostnames on port 9000.

INSERT INTO them.agents (slug, display_name, description, transport, endpoint_url, enabled)
VALUES
    (
        'assistant',
        'Mock Assistant',
        'A general-purpose assistant agent. Use for answering questions, summarising content, and general tasks.',
        'omni_ws',
        'ws://mock-agent-assistant:9000',
        true
    ),
    (
        'researcher',
        'Mock Researcher',
        'A research agent that retrieves and analyses information on a given topic.',
        'omni_ws',
        'ws://mock-agent-researcher:9000',
        true
    ),
    (
        'coder',
        'Mock Coder',
        'A coding agent that writes, reviews, and debugs code across multiple languages.',
        'omni_ws',
        'ws://mock-agent-coder:9000',
        true
    )
ON CONFLICT (slug) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    description  = EXCLUDED.description,
    endpoint_url = EXCLUDED.endpoint_url,
    enabled      = EXCLUDED.enabled;

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
-- Wires all three mock agents together.

WITH agent_ids AS (
    SELECT ARRAY_AGG(id) AS ids
    FROM them.agents
    WHERE slug IN ('assistant', 'researcher', 'coder')
)
INSERT INTO them.orchestrators (
    name, display_name, system_prompt,
    allowed_agent_ids, llm_provider, llm_model,
    max_iterations, max_parallel_tools, rate_limit_rpm, enabled
)
SELECT
    'default',
    'Default Orchestrator',
    'You are a helpful orchestrator with access to assistant, researcher, and coder agents. Route tasks to the most appropriate agent. For multi-step tasks, use agents sequentially. You may call multiple agents in parallel when tasks are independent.',
    agent_ids.ids,
    'anthropic',
    'claude-sonnet-4-6',
    10, 4, 30, true
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
    enabled            = EXCLUDED.enabled;
