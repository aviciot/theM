"""
System-agents service — classifier role.
classify_agent() returns {"category": str, "icon": str} or None.
MUST NEVER raise; all failures return None.
"""

import json
import re
from typing import Any, Dict, List, Optional

from sqlalchemy.ext.asyncio import AsyncSession

from app.models import Config
from app.utils.crypto import decrypt_value

_VALID_CATEGORIES = frozenset(
    {"Research", "Coding", "Vision", "Security", "A2A", "Data", "Communication", "Agent"}
)
_ICON_RE = re.compile(r"^[a-zA-Z0-9_]{1,40}$")

_CLASSIFIER_SYSTEM_PROMPT = (
    "You are an agent classifier. Given an agent's name, description, and skills, "
    "return ONLY valid JSON:\n"
    '{"category": "<one of: Research|Coding|Vision|Security|A2A|Data|Communication|Agent>", '
    '"icon": "<Material Symbols name, e.g. hub, code, search, visibility>"}\n'
    "No explanation, no markdown, just JSON."
)


async def classify_agent(
    db: AsyncSession,
    *,
    display_name: str,
    description: str,
    skills: List[Any],
) -> Optional[Dict[str, str]]:
    """Return {"category": str, "icon": str} or None if classifier disabled or on any failure."""
    try:
        return await _classify(db, display_name=display_name, description=description, skills=skills)
    except Exception:
        return None


async def _classify(
    db: AsyncSession,
    *,
    display_name: str,
    description: str,
    skills: List[Any],
) -> Optional[Dict[str, str]]:
    config_row: Optional[Config] = await db.get(Config, "system_agents")
    if config_row is None:
        return None

    roles = (config_row.config_value or {}).get("roles", {})
    classifier = roles.get("classifier", {})
    if not classifier.get("enabled"):
        return None

    provider = classifier.get("provider")
    model = classifier.get("model")
    api_key_enc = classifier.get("api_key_encrypted")
    base_url = classifier.get("base_url")
    system_prompt = classifier.get("system_prompt") or _CLASSIFIER_SYSTEM_PROMPT

    if not provider or not model:
        return None

    api_key = decrypt_value(api_key_enc) if api_key_enc else ""
    if not api_key:
        return None

    skill_names = [s.get("name", "") for s in skills if isinstance(s, dict)]
    user_msg = (
        f"Name: {display_name}\n"
        f"Description: {description}\n"
        f"Skills: {', '.join(skill_names) or 'none'}"
    )

    from app.services.llm_probe import probe_llm

    result = await probe_llm(
        provider=provider,
        model=model,
        api_key=api_key,
        base_url=base_url or None,
        prompt=f"{system_prompt}\n\n{user_msg}",
        max_tokens=60,
    )

    if not result.ok or not result.response_text:
        return None

    try:
        parsed = json.loads(result.response_text.strip())
    except Exception:
        # Try to extract the first JSON object from the text
        m = re.search(r"\{[^{}]+\}", result.response_text, re.DOTALL)
        if not m:
            return None
        try:
            parsed = json.loads(m.group(0))
        except Exception:
            return None

    category = parsed.get("category", "Agent")
    if category not in _VALID_CATEGORIES:
        category = "Agent"

    icon = parsed.get("icon", "smart_toy")
    if not icon or not _ICON_RE.match(str(icon)):
        icon = "smart_toy"

    return {"category": str(category), "icon": str(icon)}
