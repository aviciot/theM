"""
Admin — Agents
CRUD for odin.agents. Publishes odin:agents:changed on any write.
auth_token is stored Fernet-encrypted; GET returns masked representation.
"""

import re
import time
import uuid
from typing import Any, Dict, List, Optional

import httpx
import websockets
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

VALID_TRANSPORTS = {"omni_ws", "a2a"}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

_SLUG_RE = re.compile(r'^[a-z0-9_]{1,48}$')


class AgentCreate(BaseModel):
    slug: str = Field(..., description="Unique slug — becomes agent__<slug> tool name. Pattern: [a-z0-9_], max 48 chars.")
    display_name: str
    description: str = Field(..., description="Used as the LLM tool description")
    transport: str = Field("omni_ws", description="Transport type: omni_ws | a2a")
    endpoint_url: str
    auth_token: Optional[str] = Field(None, description="Plaintext — stored encrypted")
    input_schema: Dict[str, Any] = Field(default_factory=dict)
    timeout_seconds: int = 120
    max_concurrency: int = 4
    enabled: bool = True
    tags: List[str] = Field(default_factory=list)

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

    class Config:
        from_attributes = True


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
        if row.transport == "a2a":
            base = row.endpoint_url.rstrip("/")
            card_url = f"{base}/.well-known/agent-card.json"
            async with httpx.AsyncClient(timeout=10) as client:
                resp = await client.get(card_url, headers=headers)
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

        elif row.transport == "omni_ws":
            try:
                async with websockets.connect(row.endpoint_url, additional_headers=headers, open_timeout=8):
                    pass  # connection accepted — that's enough
                latency_ms = int((time.monotonic() - t0) * 1000)
                return {"ok": True, "latency_ms": latency_ms, "detail": "WebSocket handshake succeeded"}
            except Exception as exc:
                latency_ms = int((time.monotonic() - t0) * 1000)
                return {"ok": False, "latency_ms": latency_ms, "detail": str(exc)}

        else:
            return {"ok": False, "latency_ms": 0, "detail": f"No test defined for transport '{row.transport}'"}

    except Exception as exc:
        latency_ms = int((time.monotonic() - t0) * 1000)
        return {"ok": False, "latency_ms": latency_ms, "detail": str(exc)}


@router.delete("/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, agent_id)
    slug = row.slug
    await db.delete(row)
    await db.commit()
    await invalidate_registry()
    logger.info("agent deleted", agent_id=str(agent_id), slug=slug)
