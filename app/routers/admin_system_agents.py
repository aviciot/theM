"""
Admin — System Agents
GET/PUT config for system agent roles (classifier, etc.).
POST /{role}/test-llm to validate the stored key.
Config stored under them.config key "system_agents".
"""

import copy
from typing import Dict, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Config
from app.services.llm_probe import probe_llm
from app.utils.crypto import decrypt_value, encrypt_value, key_hint
from app.utils.logger import logger

router = APIRouter(prefix="/admin/system-agents", tags=["admin-system-agents"])

_CONFIG_KEY = "system_agents"
_DEFAULT_SYSTEM_PROMPT = (
    "You are an agent classifier. Given an agent's name, description, and skills, "
    "return ONLY valid JSON:\n"
    '{"category": "<one of: Research|Coding|Vision|Security|A2A|Data|Communication|Agent>", '
    '"icon": "<Material Symbols name, e.g. hub, code, search, visibility>"}\n'
    "No explanation, no markdown, just JSON."
)

_DEFAULT_CONFIG: Dict = {
    "roles": {
        "classifier": {
            "enabled": False,
            "provider": None,
            "model": None,
            "base_url": None,
            "system_prompt": None,
            "api_key_encrypted": None,
        }
    }
}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class RoleConfigOut(BaseModel):
    enabled: bool
    provider: Optional[str]
    model: Optional[str]
    base_url: Optional[str]
    system_prompt: Optional[str]
    api_key_hint: Optional[str]  # masked, never plaintext


class RoleConfigIn(BaseModel):
    enabled: Optional[bool] = None
    provider: Optional[str] = None
    model: Optional[str] = None
    base_url: Optional[str] = None
    system_prompt: Optional[str] = None
    api_key: Optional[str] = None  # plaintext write-only; blank = keep existing


class SystemAgentsOut(BaseModel):
    roles: Dict[str, RoleConfigOut]


class SystemAgentsIn(BaseModel):
    roles: Dict[str, RoleConfigIn]


class LLMTestRequest(BaseModel):
    provider: Optional[str] = None
    model: Optional[str] = None
    api_key: Optional[str] = None   # plaintext; if omitted, use stored key
    base_url: Optional[str] = None


class LLMTestResult(BaseModel):
    ok: bool
    latency_ms: Optional[int] = None
    error: Optional[str] = None


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _load_config(row: Optional[Config]) -> Dict:
    if row is None:
        import copy
        return copy.deepcopy(_DEFAULT_CONFIG)
    return row.config_value or {}


def _role_to_out(role_data: Dict) -> RoleConfigOut:
    enc_key = role_data.get("api_key_encrypted")
    return RoleConfigOut(
        enabled=bool(role_data.get("enabled", False)),
        provider=role_data.get("provider"),
        model=role_data.get("model"),
        base_url=role_data.get("base_url"),
        system_prompt=role_data.get("system_prompt"),
        api_key_hint=key_hint(enc_key) if enc_key else None,
    )


def _config_to_out(config: Dict) -> SystemAgentsOut:
    roles_raw = config.get("roles", {})
    return SystemAgentsOut(
        roles={name: _role_to_out(data) for name, data in roles_raw.items()}
    )


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=SystemAgentsOut)
async def get_system_agents(db: AsyncSession = Depends(get_db)) -> SystemAgentsOut:
    row = await db.get(Config, _CONFIG_KEY)
    return _config_to_out(_load_config(row))


@router.put("", response_model=SystemAgentsOut)
async def put_system_agents(body: SystemAgentsIn, db: AsyncSession = Depends(get_db)) -> SystemAgentsOut:
    row = await db.get(Config, _CONFIG_KEY)
    config = copy.deepcopy(_load_config(row))  # deep-copy so SQLAlchemy detects mutation
    stored_roles: Dict = config.setdefault("roles", {})

    for role_name, incoming in body.roles.items():
        existing = stored_roles.get(role_name, {})

        if incoming.enabled is not None:
            existing["enabled"] = incoming.enabled
        if incoming.provider is not None:
            existing["provider"] = incoming.provider or None
        if incoming.model is not None:
            existing["model"] = incoming.model or None
        if incoming.base_url is not None:
            existing["base_url"] = incoming.base_url or None
        if incoming.system_prompt is not None:
            existing["system_prompt"] = incoming.system_prompt or None
        if incoming.api_key:  # non-blank = update; blank = preserve existing
            existing["api_key_encrypted"] = encrypt_value(incoming.api_key)

        stored_roles[role_name] = existing

    config["roles"] = stored_roles

    if row is None:
        row = Config(config_key=_CONFIG_KEY, config_value=config)
        db.add(row)
    else:
        row.config_value = config

    await db.commit()
    logger.info("system_agents config updated", roles=list(body.roles.keys()))
    return _config_to_out(config)


@router.post("/{role}/test-llm", response_model=LLMTestResult)
async def test_role_llm(
    role: str,
    body: LLMTestRequest,
    db: AsyncSession = Depends(get_db),
) -> LLMTestResult:
    """Test LLM connectivity for a system agent role.

    Request body fields override the stored config; stored config fills any gaps.
    This allows testing unsaved form values without requiring a Save first.
    """
    row = await db.get(Config, _CONFIG_KEY)
    stored_role: Dict = {}
    if row is not None:
        stored_role = (row.config_value or {}).get("roles", {}).get(role, {})

    # Inline request body takes priority; fall back to stored config
    provider = body.provider or stored_role.get("provider")
    model    = body.model    or stored_role.get("model")
    base_url = body.base_url or stored_role.get("base_url")

    # API key: use plaintext from body if provided, else decrypt stored key
    if body.api_key:
        api_key = body.api_key
    else:
        enc_key = stored_role.get("api_key_encrypted")
        api_key = decrypt_value(enc_key) if enc_key else ""

    if not provider or not model:
        raise HTTPException(status_code=400, detail="Provider and model are required")
    if not api_key:
        raise HTTPException(status_code=400, detail="No API key provided or stored for this role")

    result = await probe_llm(
        provider=provider,
        model=model,
        api_key=api_key,
        base_url=base_url or None,
    )
    return LLMTestResult(ok=result.ok, latency_ms=result.latency_ms, error=result.error)
