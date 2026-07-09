"""
Adapter factory — returns the correct AgentAdapter for an Agent row.
Only a2a_async transport is supported (Phase 8.1).
"""

from app.adapters.base import AgentAdapter
from app.adapters.a2a_async_adapter import A2aAsyncAdapter


def get_adapter(agent, *, context_id: str | None = None) -> AgentAdapter:
    """Return the adapter instance for the given Agent ORM row."""
    if agent.transport == "a2a_async":
        # Collect declared input_modes from all agent card skills (deduplicated, order preserved)
        skills = getattr(agent, "skills", None) or []
        seen: set[str] = set()
        input_modes: list[str] = []
        for s in skills:
            for mode in (s.get("input_modes") or []):
                if mode not in seen:
                    seen.add(mode)
                    input_modes.append(mode)

        return A2aAsyncAdapter(
            agent_slug=agent.slug,
            endpoint_url=agent.endpoint_url,
            auth_token_encrypted=agent.auth_token_encrypted,
            context_id=context_id,
            push_url=None,
            supports_streaming=getattr(agent, "supports_streaming", False),
            input_modes=input_modes or None,
        )

    raise ValueError(
        f"Unknown agent transport: {agent.transport!r}. "
        f"Only 'a2a_async' is supported (Phase 8.1+)."
    )
