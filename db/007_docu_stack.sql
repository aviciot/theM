-- Phase 7: Documentation stack — code_agent + docu_writer agents + docu_orchestrator
-- Safe to re-run (all inserts use ON CONFLICT DO UPDATE).

-- ── Agents ───────────────────────────────────────────────────────────────────

-- code_agent: external A2A agent on omni2-bridge (reads and analyzes code)
INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url, auth_token_encrypted,
    enabled, supports_streaming, timeout_seconds
)
VALUES (
    'code_agent',
    'Code Agent',
    'Reads and analyzes source code files or repositories. Returns structured explanations of business logic, architecture, data flows, and component relationships, including Mermaid diagrams where relevant.',
    'a2a_async',
    'http://10.55.125.43:3111/a2a/codeagent/',
    'omni2_mcp_BOkrx6jGd2YyU3CLQ7MohlBHphde-140mHQvPgNkumI',
    true,
    false,
    300
)
ON CONFLICT (slug) DO UPDATE SET
    display_name         = EXCLUDED.display_name,
    description          = EXCLUDED.description,
    endpoint_url         = EXCLUDED.endpoint_url,
    auth_token_encrypted = EXCLUDED.auth_token_encrypted,
    enabled              = EXCLUDED.enabled,
    timeout_seconds      = EXCLUDED.timeout_seconds;

-- docu_writer: local A2A agent that renders content into documentation files
INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url,
    enabled, supports_streaming, timeout_seconds
)
VALUES (
    'docu_writer',
    'Documentation Writer',
    'Renders technical content into polished documentation files. Given a structured explanation and a desired format (html, markdown, or slides), produces a complete ready-to-use file artifact with Mermaid diagrams and syntax highlighting.',
    'a2a_async',
    'http://docu-writer:9300',
    true,
    false,
    300
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    enabled         = EXCLUDED.enabled,
    timeout_seconds = EXCLUDED.timeout_seconds;

-- ── Orchestrator ──────────────────────────────────────────────────────────────

WITH agent_ids AS (
    SELECT ARRAY_AGG(id ORDER BY slug) AS ids
    FROM them.agents
    WHERE slug IN ('code_agent', 'docu_writer')
)
INSERT INTO them.orchestrators (
    name, display_name, system_prompt,
    allowed_agent_ids, llm_provider, llm_model,
    llm_api_key_encrypted,
    max_iterations, max_parallel_tools, rate_limit_rpm, enabled
)
SELECT
    'docu_orchestrator',
    'Documentation Orchestrator',
    $PROMPT$You are a documentation orchestrator. Your job is to help users understand code and produce professional documentation from it.

You have two agents:

## agent__code_agent
A code intelligence gateway. To use it, send a JSON message in this exact format:
{"tool": "<tool_name>", "arguments": {<args>}}

Available tools:
- read_file: {"path": "/absolute/path/to/file"}
- list_directory: {"path": "/absolute/path"}
- get_file_tree: {"path": "/absolute/path", "max_depth": 3}
- search_code: {"pattern": "keyword", "path": "/absolute/path"}
- explain_code: {"code": "<code string>", "context": "optional context"}
- find_dependencies: {"path": "/absolute/path/to/file"}
- trace_data_flow: {"entry_point": "function_name", "path": "/absolute/path"}
- get_function_signature: {"function_name": "name", "path": "/absolute/path"}

## agent__docu_writer
Takes structured content and renders it into a documentation file. Send a plain text message in this format:
FORMAT: html
TITLE: <descriptive title>
CONTENT:
<paste the full explanation from code_agent here>

Supported formats: html, markdown, slides

## Workflow
1. When the user asks to document or explain code, use agent__code_agent to read and analyze it. Start with read_file or get_file_tree to understand the structure, then explain_code or trace_data_flow for deeper analysis.
2. Ask the user which format they want (or default to html).
3. Pass the full analysis to agent__docu_writer with the FORMAT/TITLE/CONTENT structure above.
4. Tell the user the documentation is ready and to check the Artifacts tab to view it.$PROMPT$,
    agent_ids.ids,
    'anthropic',
    'claude-sonnet-4-6',
    'enc:gAAAAABqTmEx50vKT7362BBLE6FwrB9Vzc7V20GiqBflrx0tezM_54WP5edXHC1niwdhC-QKpv04NPeLpOaK167lJMHKOKpIdhyRURpiw9rup_ly9VGAXyviDr0Edx22CecPqVahHDn2Ngbb_aqC_ll5fZ8UoahT5FEd_vqbMl6wxQBpszKM7hjAiiDWrzgpMNcwYtcfipTC9PXkaRp__TTRB0mRPLll4g==',
    15, 2, 30, true
FROM agent_ids
ON CONFLICT (name) DO UPDATE SET
    display_name          = EXCLUDED.display_name,
    system_prompt         = EXCLUDED.system_prompt,
    allowed_agent_ids     = EXCLUDED.allowed_agent_ids,
    llm_provider          = EXCLUDED.llm_provider,
    llm_model             = EXCLUDED.llm_model,
    llm_api_key_encrypted = EXCLUDED.llm_api_key_encrypted,
    max_iterations        = EXCLUDED.max_iterations,
    max_parallel_tools    = EXCLUDED.max_parallel_tools,
    rate_limit_rpm        = EXCLUDED.rate_limit_rpm,
    enabled               = EXCLUDED.enabled;

-- ── Application ───────────────────────────────────────────────────────────────

INSERT INTO them.applications (
    name, slug, entry_point_type,
    orchestrator_id, access_policy, presentation, enabled
)
SELECT
    'Documentation Assistant',
    'docu-assistant',
    'websocket',
    o.id,
    '{"mode": "token"}'::jsonb,
    '{
        "title": "Documentation Assistant",
        "description": "Analyze code and generate polished HTML, Markdown, or slide documentation powered by AI.",
        "icon": "📄"
    }'::jsonb,
    true
FROM them.orchestrators o
WHERE o.name = 'docu_orchestrator'
ON CONFLICT (slug) DO UPDATE SET
    name             = EXCLUDED.name,
    entry_point_type = EXCLUDED.entry_point_type,
    orchestrator_id  = EXCLUDED.orchestrator_id,
    presentation     = EXCLUDED.presentation,
    enabled          = EXCLUDED.enabled;
