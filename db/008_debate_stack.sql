-- Phase 8: Debate stack — 4 A2A debate agents + debate_flow orchestrator
-- Safe to re-run (all inserts use ON CONFLICT DO UPDATE).

-- ── Agents ───────────────────────────────────────────────────────────────────

INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url,
    enabled, supports_streaming, timeout_seconds, input_schema
)
VALUES (
    'agent_evidence',
    'Evidence Debater',
    'Debate agent: produces the strongest evidence-based argument for a given question and position. '
    'Always call with typed JSON: {question, position, round (1 or 2), opponent_arguments (array, round 2 only)}. '
    'Returns: {argument, key_evidence, confidence, main_point, approach, agent, round}.',
    'a2a_async',
    'http://agent-evidence:9401',
    true,
    false,
    120,
    '{}'::jsonb
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    enabled         = EXCLUDED.enabled,
    timeout_seconds = EXCLUDED.timeout_seconds,
    input_schema    = EXCLUDED.input_schema;

INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url,
    enabled, supports_streaming, timeout_seconds, input_schema
)
VALUES (
    'agent_logic',
    'Logic Debater',
    'Debate agent: constructs the strongest logic-based argument using reasoning, first principles, and deduction. '
    'Always call with typed JSON: {question, position, round (1 or 2), opponent_arguments (array, round 2 only)}. '
    'Returns: {argument, logical_chain, confidence, main_point, approach, agent, round}.',
    'a2a_async',
    'http://agent-logic:9402',
    true,
    false,
    120,
    '{}'::jsonb
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    enabled         = EXCLUDED.enabled,
    timeout_seconds = EXCLUDED.timeout_seconds,
    input_schema    = EXCLUDED.input_schema;

INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url,
    enabled, supports_streaming, timeout_seconds, input_schema
)
VALUES (
    'agent_creative',
    'Creative Debater',
    'Debate agent: finds the most surprising argument by drawing from an unexpected field of knowledge '
    '(e.g. evolutionary biology, game theory, neuroscience, anthropology). '
    'Always call with typed JSON: {question, position, round (1 or 2), '
    'fields (optional list of fields to draw from), opponent_arguments (array, round 2 only)}. '
    'Returns: {argument, field, insight, confidence, main_point, approach, agent, round}.',
    'a2a_async',
    'http://agent-creative:9403',
    true,
    false,
    120,
    '{}'::jsonb
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    enabled         = EXCLUDED.enabled,
    timeout_seconds = EXCLUDED.timeout_seconds,
    input_schema    = EXCLUDED.input_schema;

INSERT INTO them.agents (
    slug, display_name, description,
    transport, endpoint_url,
    enabled, supports_streaming, timeout_seconds, input_schema
)
VALUES (
    'agent_judge',
    'Debate Judge',
    'Impartial debate judge: scores all debater arguments on clarity, relevance, logic, evidence, and persuasiveness. '
    'Picks a winner and explains why. When final=true, synthesizes the best elements into a final combined answer. '
    'Always call with typed JSON: {question, arguments (array of argument objects from debaters), round (1 or 2), final (bool)}. '
    'Returns: {scores (per agent), winner, winner_reason, synthesis (when final=true)}.',
    'a2a_async',
    'http://agent-judge:9404',
    true,
    false,
    180,
    '{}'::jsonb
)
ON CONFLICT (slug) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    description     = EXCLUDED.description,
    endpoint_url    = EXCLUDED.endpoint_url,
    enabled         = EXCLUDED.enabled,
    timeout_seconds = EXCLUDED.timeout_seconds,
    input_schema    = EXCLUDED.input_schema;

-- ── Orchestrator ──────────────────────────────────────────────────────────────

WITH agent_ids AS (
    SELECT ARRAY_AGG(id ORDER BY slug) AS ids
    FROM them.agents
    WHERE slug IN ('agent_creative', 'agent_evidence', 'agent_judge', 'agent_logic', 'docu_writer')
)
INSERT INTO them.orchestrators (
    name, display_name, system_prompt,
    allowed_agent_ids, llm_provider, llm_model,
    llm_api_key_encrypted,
    max_iterations, max_parallel_tools, rate_limit_rpm, enabled
)
SELECT
    'debate_flow',
    'Debate Flow',
    $PROMPT$You are a debate orchestrator managing a structured 2-round debate between three specialist agents, a judge, and a documentation writer.

The five agents available to you:
- agent__agent_evidence: argues using empirical evidence, data, and documented facts
- agent__agent_logic: argues using reasoning, first principles, and logical deduction
- agent__agent_creative: argues from a surprising, unexpected field of knowledge (picks randomly unless you specify fields)
- agent__agent_judge: impartial judge — scores all debater arguments, picks a winner, explains why, synthesizes final answer
- agent__docu_writer: renders content into polished HTML — call with typed JSON: {format: "html", title: "<title>", content: "<markdown content>"}

## Argument summary format
Each debater returns a full argument object. For routing purposes, extract only the SUMMARY FIELDS:
  {agent, main_point, confidence, approach, round}
These are the only fields you need to pass between agents. Never forward the full argument text in your tool calls — it is available in artifacts.

## Debate Flow (strictly follow this sequence):

### Step 1 — Confirm the debate topic
If the user provides a clear question or topic, proceed. If ambiguous, ask one clarifying question. Once confirmed, tell the user: "Starting Round 1 — all three debaters arguing simultaneously..."

### Step 2 — Round 1 (parallel, all three debaters)
Call all three debate agents IN PARALLEL with:
- question: the debate topic
- position: "Yes" or whichever side makes the topic interesting
- round: 1
Do NOT call agent__agent_judge yet.

### Step 3 — Round 1 verdict
Call agent__agent_judge with:
- question: the debate topic
- arguments: array of SUMMARY FIELDS ONLY from each debater: [{agent, main_point, confidence, approach, round}, ...]
- round: 1
- final: false
Present the Round 1 verdict to the user: each agent's scores and who won.

### Step 4 — Round 2 (parallel, all three debaters)
Tell the user: "Starting Round 2 — debaters have seen each other's arguments..."
Call all three debate agents IN PARALLEL with:
- question: the debate topic
- position: same as Round 1
- round: 2
- opponent_arguments: array of SUMMARY FIELDS ONLY from the OTHER two agents: [{agent, main_point, approach, round}, ...]
Do NOT call agent__agent_judge yet.

### Step 5 — Final verdict
Call agent__agent_judge with:
- question: the debate topic
- arguments: array of SUMMARY FIELDS ONLY from each Round 2 debater: [{agent, main_point, confidence, approach, round}, ...]
- round: 2
- final: true
Present the final verdict to the user: scores, winner, winner reason, and synthesized answer.

### Step 6 — HTML report
After presenting the final verdict, tell the user: "Generating HTML debate report..."
Compose a comprehensive Markdown summary of the entire debate and call agent__docu_writer with:
- format: "html"
- title: "Debate Report: <the debate topic>"
- content: a Markdown document containing:
  ## The Question
  <debate topic>

  ## Round 1 Arguments
  For each debater: ### <Agent Name> — <main_point> (confidence: <value>)
  <full argument text from Round 1 artifacts>

  ## Round 1 Verdict
  <scores per agent, who won, why>

  ## Round 2 Arguments
  For each debater: ### <Agent Name> — <main_point> (confidence: <value>)
  <full argument text from Round 2 artifacts>

  ## Final Verdict
  **Winner: <winner>**
  <winner_reason>

  ## Synthesis
  <synthesis from the judge>
Tell the user the HTML report is ready and available as a file artifact.

## Rules:
1. In Steps 2 and 4, call all three debate agents in PARALLEL (one LLM turn, three tool calls).
2. Never skip rounds or collapse into one round.
3. Never call agent__agent_judge in the same turn as the debaters.
4. Present intermediate results to the user between rounds.
5. Pass ONLY summary fields in your tool calls to debaters and judge — never the full argument text.
6. Step 6 is the exception: compose full content for docu_writer using argument text from artifacts.
7. Keep your own commentary brief — the agents' arguments are the content.
8. If the user wants to debate a different topic, restart from Step 1.$PROMPT$,
    agent_ids.ids,
    'anthropic',
    'claude-haiku-4-5-20251001',
    'enc:gAAAAABqUSJr32nF5HO-0lGm6n1wr0CCrVaQOG02DNUw3w_-q_y7i9laqKyMvN8hDl4MwwsOEsiNh8Sh1Z18nB6fV_TZMBHjBNZ_rfpB9a23edYTuXYJtDLd5EtSrQ6gQxZYHQ89uGGMs7TqgF5AERKMAgdOM6k2xboCKzyxHTPbCeuKzBCElUe0pZG7B98ADFVBdmzUdZeabDN1bFvckvbCWnQgCLZcSA==',
    15, 3, 20, true
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
