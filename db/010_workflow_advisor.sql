-- Phase 13: Workflow Advisor Agent
-- Seeds the workflow_advisor A2A agent + workflow_advisor orchestrator.
-- The orchestrator has exactly one tool: the advisor agent.
-- The orchestrator is tagged 'internal' so the canvas NodeLibrary hides it.
-- Safe to re-run (ON CONFLICT DO UPDATE).

-- ── Seed advisor agent ────────────────────────────────────────────────────────

INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url, auth_token_encrypted,
    enabled, supports_streaming, timeout_seconds,
    input_schema, skills, tags
)
VALUES (
    'workflow_advisor',
    'Workflow Advisor',
    'Analyzes a the-M workflow canvas (orchestrators, agents, entry points, '
    'connections) and provides actionable advisory: missing configuration, '
    'routing logic gaps, orchestrator prompt quality, agent description clarity, '
    'structural issues, and security considerations. Supports multi-turn '
    'follow-up for prompt suggestions and detailed explanations.',
    'a2a_async',
    'http://them-workflow-advisor:9600',
    NULL,
    true,
    true,
    180,
    '{}'::jsonb,
    '[{
        "id": "advise_workflow",
        "name": "Advise Workflow",
        "description": "Given a serialized workflow graph (nodes, edges, orchestrator system prompts, agent descriptions, entry point config), analyze it for completeness, routing logic, prompt quality, structural issues, and security posture.",
        "tags": ["advisor", "analysis", "workflow", "orchestration"],
        "inputModes": ["text/plain"],
        "outputModes": ["text/plain"]
    }]'::jsonb,
    ARRAY['internal', 'advisor']
)
ON CONFLICT (slug) DO UPDATE SET
    display_name       = EXCLUDED.display_name,
    description        = EXCLUDED.description,
    endpoint_url       = EXCLUDED.endpoint_url,
    transport          = EXCLUDED.transport,
    enabled            = EXCLUDED.enabled,
    supports_streaming = EXCLUDED.supports_streaming,
    timeout_seconds    = EXCLUDED.timeout_seconds,
    skills             = EXCLUDED.skills,
    tags               = EXCLUDED.tags;

-- ── Seed advisor orchestrator ─────────────────────────────────────────────────

INSERT INTO them.orchestrators (
    name, display_name, system_prompt,
    llm_provider, llm_model,
    max_iterations, max_parallel_tools,
    rate_limit_rpm, daily_budget_usd,
    enabled, edges, history_window,
    memory_enabled
)
VALUES (
    'workflow_advisor',
    'Workflow Advisor',
    E'You are a workflow routing assistant for the-M''s internal Workflow Advisor.\n\nYour ONLY job is to call the workflow_advisor agent tool with the user''s message and return its response verbatim.\n\nRules:\n- Always call the agent__workflow_advisor tool immediately.\n- Never answer from your own knowledge — always delegate to the agent.\n- Return the agent''s response exactly as received, without summarizing or adding commentary.\n- If the user asks a follow-up question, call the agent again with that question.\n- Do not ask clarifying questions.',
    'anthropic',
    'claude-haiku-4-5-20251001',
    4,
    1,
    120,
    0,
    true,
    ARRAY['websocket'],
    10,
    false
)
ON CONFLICT (name) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    system_prompt   = EXCLUDED.system_prompt,
    llm_model       = EXCLUDED.llm_model,
    max_iterations  = EXCLUDED.max_iterations,
    enabled         = EXCLUDED.enabled,
    history_window  = EXCLUDED.history_window;

-- Wire the advisor agent to the advisor orchestrator
UPDATE them.orchestrators
SET allowed_agent_ids = ARRAY[(
    SELECT id FROM them.agents WHERE slug = 'workflow_advisor'
)]
WHERE name = 'workflow_advisor';
