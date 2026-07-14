"""
Serializable dataclasses that cross the Workflow ↔ Activity boundary.

Rules:
- No ORM objects (SQLAlchemy models are not pickle-safe across processes)
- No Decimal on the wire — use str; reconstruct with Decimal() in the consumer
- No uuid.UUID on the wire — use str; reconstruct with uuid.UUID() in the consumer
- All fields must be JSON-serializable (temporalio uses its own converter but we keep it safe)
"""

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class OrchestrationInput:
    orchestrator_name: str
    user_message: str
    user_id: int
    token_payload: dict
    session_id: str
    context_id: str
    # Carried across continue_as_new so budget/iteration counters are not reset
    tokens_used_carry: int = 0
    iteration_carry: int = 0
    # history_window passed in so the context loader uses the right value
    # before orch_config is available; default matches DB column default
    history_window: int = 20
    # Sub-orchestrator nesting depth (0 = top-level). Carried across continue_as_new.
    depth: int = 0
    parent_run_id: Optional[str] = None
    budget_tokens_carry: Optional[int] = None
    entry_point_slug: Optional[str] = None


@dataclass
class OrchestratorConfig:
    """Serializable snapshot of an orchestrator row — passed from loader to Workflow."""
    id: str
    name: str
    display_name: str
    system_prompt: str
    llm_provider: str
    llm_model: str
    llm_api_key_encrypted: Optional[str]
    llm_base_url: Optional[str]
    max_iterations: int
    max_parallel_tools: int
    rate_limit_rpm: int
    daily_budget_usd: str      # Decimal as str
    a2a_exposed: bool
    memory_enabled: bool
    summarize_every_n_calls: int
    memory_raw_fallback_n: int
    summarizer_provider: Optional[str]
    summarizer_model: Optional[str]
    summarizer_api_key_encrypted: Optional[str]
    history_window: int
    budget_tokens: Optional[int]
    # pricing (str to avoid Decimal on wire)
    price_in: str              # per-token USD as str
    price_out: str


@dataclass
class AgentConfig:
    """Serializable snapshot of an agent row."""
    id: str
    slug: str
    name: str
    description: str
    transport: str             # "omni_ws" | "a2a"
    endpoint_url: Optional[str]
    auth_token_encrypted: Optional[str]
    timeout_seconds: int
    max_concurrency: int
    input_schema: Optional[dict]
    skills: Optional[list]
    max_retries: int = 2
    is_sub_orchestrator: bool = False  # True when this AgentConfig represents an a2a_exposed orchestrator


@dataclass
class LoadContextResult:
    """Return value of load_orchestration_context_activity."""
    orch: OrchestratorConfig
    agents: list[AgentConfig]
    tools: list[dict]           # NeutralTool list (name, description, schema)
    prior_history: list[dict]   # serialized provider-native messages


@dataclass
class InitRunResult:
    """Return value of init_run_activity."""
    run_id: str
    root_task_id: str


@dataclass
class PlanTurnInput:
    run_id: str
    context_id: str
    root_task_id: str
    orchestrator_name: str
    system_prompt: str
    provider_name: str
    model: str
    api_key_encrypted: Optional[str]
    base_url: Optional[str]
    messages: list[dict]        # serialized provider-native message history
    tools: list[dict]
    max_tokens: int
    msg_seq: int
    price_in: str               # Decimal as str
    price_out: str
    user_id: int
    llm_provider: str           # for run_recorder.record_usage
    budget_tokens: Optional[int]
    tokens_used_so_far: int
    iteration: int = 0          # current loop iteration (for trace labels)


@dataclass
class PlanTurnResult:
    tool_calls: list[dict]          # [{id, name, input}]
    final_answer: Optional[str]
    serialized_assistant_turn: Optional[str]    # JSON-encoded turn (str to avoid nested type issues)
    input_tokens: int
    output_tokens: int
    msg_seq_after: int              # msg_seq incremented if turn was persisted


@dataclass
class InvokeAgentInput:
    run_id: str
    context_id: str
    root_task_id: str
    iteration: int
    agent_id: str
    agent_slug: str
    agent_name: str
    transport: str
    endpoint_url: Optional[str]
    auth_token_encrypted: Optional[str]
    timeout_seconds: int
    tool_call_id: str
    tool_call_name: str
    tool_input: dict
    injected_context: Optional[str]
    input_schema: Optional[dict]
    max_retries: int = 2


@dataclass
class InvokeAgentResult:
    status: str                 # "completed" | "failed" | "input-required"
    result_text: str
    file_parts: list[dict]
    latency_ms: int
    error: Optional[str] = None


@dataclass
class SummarizeContextInput:
    context_id: str
    root_task_id: str
    orch_id: str
    memory_enabled: bool
    summarize_every_n_calls: int
    memory_raw_fallback_n: int
    summarizer_provider: Optional[str]
    summarizer_model: Optional[str]
    summarizer_api_key_encrypted: Optional[str]
    llm_provider: str
    llm_model: str
    llm_api_key_encrypted: Optional[str]


@dataclass
class RecordToolResultsInput:
    root_task_id: str
    msg_seq: int
    tool_results: list[dict]    # [{tool_use_id, content}]


@dataclass
class FinalizeRunInput:
    run_id: str
    root_task_id: str
    context_id: str
    orchestrator_name: str
    status: str
    final_answer: Optional[str]
    iterations: int
    total_tokens_in: int
    total_tokens_out: int
    total_cost_usd: str        # Decimal as str
    error: Optional[str]
    user_id: int
