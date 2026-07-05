"""
Admin — Agents
CRUD for them.agents. Publishes them:agents:changed on any write.
auth_token is stored Fernet-encrypted; GET returns masked representation.
"""

import re
import time
import uuid
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

import httpx
from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Agent
from app.services.agent_registry import invalidate_registry
from app.utils.crypto import decrypt_value, encrypt_value
from app.utils.logger import logger

router = APIRouter(prefix="/admin/agents", tags=["admin-agents"])

VALID_TRANSPORTS = {"a2a_async"}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

_SLUG_RE = re.compile(r'^[a-z0-9_]{1,48}$')


class AgentCreate(BaseModel):
    slug: str = Field(..., description="Unique slug — becomes agent__<slug> tool name. Pattern: [a-z0-9_], max 48 chars.")
    display_name: str
    description: str = Field(..., description="Used as the LLM tool description")
    transport: str = Field("a2a_async", description="Transport type: a2a_async")
    endpoint_url: str
    auth_token: Optional[str] = Field(None, description="Plaintext — stored encrypted")
    input_schema: Dict[str, Any] = Field(default_factory=dict)
    timeout_seconds: int = 120
    max_concurrency: int = 4
    enabled: bool = True
    tags: List[str] = Field(default_factory=list)
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False

    @field_validator('slug')
    @classmethod
    def slug_valid(cls, v: str) -> str:
        if not _SLUG_RE.match(v):
            raise ValueError('slug must match [a-z0-9_]{1,48} — no hyphens, no uppercase')
        return v


class AgentUpdate(BaseModel):
    display_name: Optional[str] = None
    description: Optional[str] = None
    transport: Optional[str] = None
    endpoint_url: Optional[str] = None
    auth_token: Optional[str] = Field(None, description="Set to rotate; omit to leave unchanged")
    input_schema: Optional[Dict[str, Any]] = None
    timeout_seconds: Optional[int] = None
    max_concurrency: Optional[int] = None
    enabled: Optional[bool] = None
    tags: Optional[List[str]] = None
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: Optional[List[Dict[str, Any]]] = None
    supports_streaming: Optional[bool] = None
    supports_push: Optional[bool] = None


class AgentOut(BaseModel):
    id: uuid.UUID
    slug: str
    display_name: str
    description: str
    transport: str
    endpoint_url: str
    auth_token_set: bool
    auth_token_masked: Optional[str]
    input_schema: Dict[str, Any]
    timeout_seconds: int
    max_concurrency: int
    enabled: bool
    tags: List[str]
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False
    card_fetched_at: Optional[datetime] = None

    class Config:
        from_attributes = True


class DiscoverRequest(BaseModel):
    endpoint_url: str
    auth_token: Optional[str] = None


class DiscoverResult(BaseModel):
    ok: bool
    detail: str = ""
    suggested_slug: str = ""
    display_name: str = ""
    description: str = ""
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: str = ""


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _mask_token(encrypted: Optional[str]) -> tuple[bool, Optional[str]]:
    if not encrypted:
        return False, None
    try:
        plain = decrypt_value(encrypted)
        if len(plain) <= 8:
            return True, "****"
        return True, plain[:4] + "..." + plain[-4:]
    except Exception:
        return True, "****"


def _row_to_out(row: Agent) -> AgentOut:
    token_set, masked = _mask_token(row.auth_token_encrypted)
    return AgentOut(
        id=row.id,
        slug=row.slug,
        display_name=row.display_name,
        description=row.description,
        transport=row.transport,
        endpoint_url=row.endpoint_url,
        auth_token_set=token_set,
        auth_token_masked=masked,
        input_schema=row.input_schema or {},
        timeout_seconds=row.timeout_seconds,
        max_concurrency=row.max_concurrency,
        enabled=row.enabled,
        tags=list(row.tags or []),
        agent_card=row.agent_card,
        agent_card_url=row.agent_card_url,
        skills=list(row.skills or []),
        supports_streaming=row.supports_streaming,
        supports_push=row.supports_push,
        card_fetched_at=getattr(row, "card_fetched_at", None),
    )


async def _get_or_404(db: AsyncSession, agent_id: uuid.UUID) -> Agent:
    row = await db.get(Agent, agent_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Agent not found")
    return row


def _validate_transport(transport: str) -> None:
    if transport not in VALID_TRANSPORTS:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=f"Invalid transport '{transport}'. Must be one of: {sorted(VALID_TRANSPORTS)}",
        )


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[AgentOut])
async def list_agents(
    enabled_only: bool = False,
    db: AsyncSession = Depends(get_db),
):
    q = select(Agent).order_by(Agent.slug)
    if enabled_only:
        q = q.where(Agent.enabled == True)
    result = await db.execute(q)
    return [_row_to_out(r) for r in result.scalars()]


@router.post("", response_model=AgentOut, status_code=status.HTTP_201_CREATED)
async def create_agent(body: AgentCreate, db: AsyncSession = Depends(get_db)):
    _validate_transport(body.transport)

    existing = await db.execute(select(Agent).where(Agent.slug == body.slug))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f"Agent '{body.slug}' already exists")

    row = Agent(
        slug=body.slug,
        display_name=body.display_name,
        description=body.description,
        transport=body.transport,
        endpoint_url=body.endpoint_url,
        auth_token_encrypted=encrypt_value(body.auth_token) if body.auth_token else None,
        input_schema=body.input_schema,
        timeout_seconds=body.timeout_seconds,
        max_concurrency=body.max_concurrency,
        enabled=body.enabled,
        tags=body.tags,
        agent_card=body.agent_card,
        agent_card_url=body.agent_card_url,
        skills=body.skills,
        supports_streaming=body.supports_streaming,
        supports_push=body.supports_push,
        card_fetched_at=datetime.now(timezone.utc) if body.agent_card else None,
    )
    db.add(row)
    await db.commit()
    await db.refresh(row)
    await invalidate_registry()
    logger.info("agent created", slug=body.slug)
    return _row_to_out(row)


@router.get("/{agent_id}", response_model=AgentOut)
async def get_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    return _row_to_out(await _get_or_404(db, agent_id))


@router.patch("/{agent_id}", response_model=AgentOut)
async def update_agent(
    agent_id: uuid.UUID,
    body: AgentUpdate,
    db: AsyncSession = Depends(get_db),
):
    row = await _get_or_404(db, agent_id)

    if body.transport is not None:
        _validate_transport(body.transport)
        row.transport = body.transport
    if body.display_name is not None:
        row.display_name = body.display_name
    if body.description is not None:
        row.description = body.description
    if body.endpoint_url is not None:
        row.endpoint_url = body.endpoint_url
    if body.auth_token is not None:
        row.auth_token_encrypted = encrypt_value(body.auth_token)
    if body.input_schema is not None:
        row.input_schema = body.input_schema
    if body.timeout_seconds is not None:
        row.timeout_seconds = body.timeout_seconds
    if body.max_concurrency is not None:
        row.max_concurrency = body.max_concurrency
    if body.enabled is not None:
        row.enabled = body.enabled
    if body.tags is not None:
        row.tags = body.tags
    if body.agent_card is not None:
        row.agent_card = body.agent_card
        row.card_fetched_at = datetime.now(timezone.utc)
    if body.agent_card_url is not None:
        row.agent_card_url = body.agent_card_url
    if body.skills is not None:
        row.skills = body.skills
    if body.supports_streaming is not None:
        row.supports_streaming = body.supports_streaming
    if body.supports_push is not None:
        row.supports_push = body.supports_push

    await db.commit()
    await db.refresh(row)
    await invalidate_registry()
    logger.info("agent updated", agent_id=str(agent_id), slug=row.slug)
    return _row_to_out(row)


@router.post("/{agent_id}/test")
async def test_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """
    Connectivity check:
    - a2a: GET agent card URL (.well-known/agent-card.json), return name + skill count
    - omni_ws: open WS connection, check it accepts, close immediately
    """
    row = await _get_or_404(db, agent_id)
    token = decrypt_value(row.auth_token_encrypted) if row.auth_token_encrypted else ""
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    t0 = time.monotonic()

    try:
        base = row.endpoint_url.rstrip("/")
        card_url = f"{base}/.well-known/agent-card.json"
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(card_url, headers={**headers, "A2A-Version": "1.0"})
        latency_ms = int((time.monotonic() - t0) * 1000)
        if resp.status_code == 200:
            card = resp.json()
            return {
                "ok": True,
                "latency_ms": latency_ms,
                "detail": f"Agent card OK — {card.get('name', '?')} · {len(card.get('skills', []))} skills",
            }
        return {
            "ok": False,
            "latency_ms": latency_ms,
            "detail": f"HTTP {resp.status_code}: {resp.text[:200]}",
        }

    except Exception as exc:
        latency_ms = int((time.monotonic() - t0) * 1000)
        return {"ok": False, "latency_ms": latency_ms, "detail": str(exc)}


@router.post("/discover", response_model=DiscoverResult)
async def discover_agent(body: DiscoverRequest) -> DiscoverResult:
    """
    Fetch /.well-known/agent-card.json from endpoint_url and return
    suggested form values. Does NOT create or modify any DB row.
    """
    base = body.endpoint_url.rstrip("/")
    card_url = f"{base}/.well-known/agent-card.json"
    headers: Dict[str, str] = {"A2A-Version": "1.0"}
    if body.auth_token:
        headers["Authorization"] = f"Bearer {body.auth_token}"

    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(card_url, headers=headers)
    except Exception as exc:
        return DiscoverResult(ok=False, detail=f"Connection failed: {exc}")

    if resp.status_code != 200:
        return DiscoverResult(ok=False, detail=f"HTTP {resp.status_code}: {resp.text[:200]}")

    try:
        card = resp.json()
    except Exception:
        return DiscoverResult(ok=False, detail="Response is not valid JSON")

    # Build suggested slug from card name
    raw_name = card.get("name", "") or ""
    suggested_slug = re.sub(r"[^a-z0-9]+", "_", raw_name.lower().strip()).strip("_")[:48] or "agent"

    # Parse skills (A2A SDK v1.1 shape)
    raw_skills = card.get("skills", []) or []
    skills: List[Dict[str, Any]] = []
    for s in raw_skills:
        if isinstance(s, dict):
            skills.append({
                "id": s.get("id", ""),
                "name": s.get("name", ""),
                "description": s.get("description", ""),
                "tags": s.get("tags", []),
            })

    # Build LLM tool description: card description + one line per skill
    description_parts = []
    if card.get("description"):
        description_parts.append(card["description"].strip())
    for s in skills:
        if s.get("name"):
            line = s["name"]
            if s.get("description"):
                line += f": {s['description']}"
            description_parts.append(line)
    description = "\n".join(description_parts) or raw_name

    # Detect capabilities
    caps = card.get("capabilities", {}) or {}
    supports_streaming = bool(caps.get("streaming", False))
    supports_push = bool(caps.get("pushNotifications", False))

    logger.info("discover: card fetched", endpoint=base, slug=suggested_slug, skills=len(skills))
    return DiscoverResult(
        ok=True,
        suggested_slug=suggested_slug,
        display_name=card.get("name", raw_name),
        description=description,
        skills=skills,
        supports_streaming=supports_streaming,
        supports_push=supports_push,
        agent_card=card,
        agent_card_url=card_url,
    )


@router.delete("/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, agent_id)
    slug = row.slug
    await db.delete(row)
    await db.commit()
    await invalidate_registry()
    logger.info("agent deleted", agent_id=str(agent_id), slug=slug)
