"""
Adapter factory — returns the correct AgentAdapter for an Agent row.
"""

from app.adapters.base import AgentAdapter
from app.adapters.omni_ws_adapter import OmniWsAdapter
from app.adapters.a2a_adapter import A2aAdapter


def get_adapter(agent) -> AgentAdapter:
    """Return the adapter instance for the given Agent ORM row."""
    transport = agent.transport

    if transport == "omni_ws":
        return OmniWsAdapter(
            agent_slug=agent.slug,
            endpoint_url=agent.endpoint_url,
            auth_token_encrypted=agent.auth_token_encrypted,
        )
    if transport == "a2a":
        return A2aAdapter(
            agent_slug=agent.slug,
            endpoint_url=agent.endpoint_url,
            auth_token_encrypted=agent.auth_token_encrypted,
        )

    raise ValueError(f"Unknown agent transport: {transport!r}")
