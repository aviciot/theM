"""
Admin — Agents
CRUD for them.agents. Publishes them:agents:changed on any write.
auth_token is stored Fernet-encrypted; GET returns masked representation.
"""

import asyncio
import json
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

import app.database as db_module
from app.database import get_db
from app.models import Agent
from app.adapters.a2a_async_adapter import A2aAsyncAdapter
from app.services import dashboard_broadcaster
from app.services.agent_registry import invalidate_registry
from app.services.system_agents import classify_agent
from app.utils.crypto import decrypt_value, encrypt_value
from app.utils.logger import logger

router = APIRouter(prefix="/admin/agents", tags=["admin-agents"])

VALID_TRANSPORTS = {"a2a_async"}
SCANNER_SLUG = "security_scanner"


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
    max_retries: int = 2
    enabled: bool = True
    tags: List[str] = Field(default_factory=list)
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False
    icon: Optional[str] = None

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
    max_retries: Optional[int] = None
    enabled: Optional[bool] = None
    tags: Optional[List[str]] = None
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: Optional[List[Dict[str, Any]]] = None
    supports_streaming: Optional[bool] = None
    supports_push: Optional[bool] = None
    icon: Optional[str] = None
    category: Optional[str] = None


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
    max_retries: int
    enabled: bool
    tags: List[str]
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: Optional[str] = None
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False
    icon: Optional[str] = None
    category: Optional[str] = None
    card_fetched_at: Optional[datetime] = None
    last_scan_at: Optional[datetime] = None
    last_scan_result: Optional[Dict[str, Any]] = None

    class Config:
        from_attributes = True


class DiscoverRequest(BaseModel):
    endpoint_url: str
    auth_token: Optional[str] = None
    agent_id: Optional[uuid.UUID] = None  # when set, use stored token from DB


class DiscoverResult(BaseModel):
    ok: bool
    detail: str = ""
    suggested_slug: str = ""
    display_name: str = ""
    description: str = ""
    skills: List[Dict[str, Any]] = Field(default_factory=list)
    supports_streaming: bool = False
    supports_push: bool = False
    icon: Optional[str] = None
    agent_card: Optional[Dict[str, Any]] = None
    agent_card_url: str = ""
    category: Optional[str] = None


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
        max_retries=row.max_retries,
        enabled=row.enabled,
        tags=list(row.tags or []),
        agent_card=row.agent_card,
        agent_card_url=row.agent_card_url,
        skills=list(row.skills or []),
        supports_streaming=row.supports_streaming,
        supports_push=row.supports_push,
        icon=getattr(row, "icon", None),
        category=getattr(row, "category", None),
        card_fetched_at=getattr(row, "card_fetched_at", None),
        last_scan_at=getattr(row, "last_scan_at", None),
        last_scan_result=getattr(row, "last_scan_result", None),
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
        max_retries=body.max_retries,
        enabled=body.enabled,
        tags=body.tags,
        agent_card=body.agent_card,
        agent_card_url=body.agent_card_url,
        skills=body.skills,
        supports_streaming=body.supports_streaming,
        supports_push=body.supports_push,
        icon=body.icon,
        card_fetched_at=datetime.now(timezone.utc) if body.agent_card else None,
    )
    db.add(row)

    if row.icon is None or row.category is None:
        try:
            classification = await classify_agent(
                db,
                display_name=body.display_name,
                description=body.description or "",
                skills=[s.model_dump() for s in (body.skills or [])],
            )
            if classification:
                if not row.icon:
                    row.icon = classification.get("icon")
                if not row.category:
                    row.category = classification.get("category")
        except Exception:
            pass

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
    if body.max_retries is not None:
        row.max_retries = max(1, body.max_retries)
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
    if body.icon is not None:
        row.icon = body.icon
    if body.category is not None:
        row.category = body.category or None

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
async def discover_agent(body: DiscoverRequest, db: AsyncSession = Depends(get_db)) -> DiscoverResult:
    """
    Fetch /.well-known/agent-card.json from endpoint_url and return
    suggested form values. Does NOT create or modify any DB row.
    If agent_id is provided, uses the stored encrypted token from that agent row.
    """
    base = body.endpoint_url.rstrip("/")
    card_url = f"{base}/.well-known/agent-card.json"
    headers: Dict[str, str] = {"A2A-Version": "1.0"}

    # Resolve auth token: explicit > stored in DB row
    token = body.auth_token
    if not token and body.agent_id:
        row = await db.get(Agent, body.agent_id)
        if row and row.auth_token_encrypted:
            try:
                token = decrypt_value(row.auth_token_encrypted)
            except Exception:
                pass
    if token:
        headers["Authorization"] = f"Bearer {token}"

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

    # Parse iconUrl from card — Material Symbols name expected (e.g. "hub", "visibility")
    icon: Optional[str] = card.get("iconUrl") or card.get("icon_url") or None
    if icon:
        icon = str(icon).strip() or None

    # Best-effort classifier enrichment
    category: Optional[str] = None
    try:
        classification = await classify_agent(
            db,
            display_name=card.get("name", raw_name),
            description=description,
            skills=skills,
        )
        if classification:
            category = classification.get("category")
            if not icon:
                icon = classification.get("icon")
    except Exception:
        pass

    logger.info("discover: card fetched", endpoint=base, slug=suggested_slug, skills=len(skills))
    return DiscoverResult(
        ok=True,
        suggested_slug=suggested_slug,
        display_name=card.get("name", raw_name),
        description=description,
        skills=skills,
        supports_streaming=supports_streaming,
        supports_push=supports_push,
        icon=icon,
        agent_card=card,
        agent_card_url=card_url,
        category=category,
    )


@router.delete("/{agent_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_agent(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, agent_id)
    slug = row.slug
    await db.delete(row)
    await db.commit()
    await invalidate_registry()
    logger.info("agent deleted", agent_id=str(agent_id), slug=slug)


# ------------------------------------------------------------------ #
# Security scan                                                        #
# ------------------------------------------------------------------ #

class ScanResponse(BaseModel):
    job_id: str
    agent_id: uuid.UUID


@router.post("/{agent_id}/security-scan", response_model=ScanResponse, status_code=202)
async def security_scan(agent_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """
    Trigger a security scan for the given agent. Returns 202 immediately.
    Progress and result are delivered via WS dashboard channel agent:<agent_id>.
    """
    target = await _get_or_404(db, agent_id)

    scanner = (
        await db.execute(select(Agent).where(Agent.slug == SCANNER_SLUG))
    ).scalar_one_or_none()
    if scanner is None:
        raise HTTPException(
            status_code=503,
            detail="Security scanner agent not registered — run db/009_security_scan.sql migration",
        )

    # Snapshot payload before session closes — never send auth token plaintext
    payload = {
        "agent_id": str(target.id),
        "slug": target.slug,
        "display_name": target.display_name,
        "description": target.description,
        "endpoint_url": target.endpoint_url,
        "agent_card": target.agent_card or {},
        "skills": list(target.skills or []),
        "supports_streaming": target.supports_streaming,
        "supports_push": target.supports_push,
        "has_auth_token": bool(target.auth_token_encrypted),
    }
    scanner_endpoint = scanner.endpoint_url
    scanner_token_enc = scanner.auth_token_encrypted
    scanner_timeout = float(scanner.timeout_seconds or 120)

    job_id = str(uuid.uuid4())
    asyncio.create_task(
        _run_scan_job(agent_id, payload, scanner_endpoint, scanner_token_enc, scanner_timeout)
    )
    logger.info("security scan started", agent_id=str(agent_id), slug=target.slug, job_id=job_id)
    return ScanResponse(job_id=job_id, agent_id=agent_id)


async def _run_scan_job(
    agent_id: uuid.UUID,
    payload: dict,
    scanner_endpoint: str,
    scanner_token_enc: Optional[str],
    timeout: float,
) -> None:
    """Background task: calls security scanner agent, persists result, publishes to WS."""
    aid = str(agent_id)
    await dashboard_broadcaster.publish_scan_started(aid)

    # Emit progress steps on a schedule while the scan runs (best-effort, cancelled on finish).
    _STEPS = [
        (0.8, "Submitting to scanner…"),
        (1.5, "Probing endpoint…"),
        (4.0, "Analyzing agent card…"),
        (7.0, "Computing risk score…"),
    ]
    scan_done = asyncio.Event()

    async def _emit_steps() -> None:
        for delay, label in _STEPS:
            try:
                await asyncio.wait_for(asyncio.shield(scan_done.wait()), timeout=delay)
                return  # scan finished before this step — stop emitting
            except asyncio.TimeoutError:
                pass
            if scan_done.is_set():
                return
            await dashboard_broadcaster.publish_scan_step(aid, label)

    step_task = asyncio.create_task(_emit_steps())

    adapter = A2aAsyncAdapter(
        agent_slug=SCANNER_SLUG,
        endpoint_url=scanner_endpoint,
        auth_token_encrypted=scanner_token_enc,
        input_modes=["application/json"],
        max_poll_seconds=timeout,
    )

    result_text: Optional[str] = None
    err: Optional[str] = None
    try:
        async for ev in adapter.stream_invoke(payload, timeout=timeout):
            if ev.type == "done":
                result_text = ev.result
            elif ev.type == "error":
                err = ev.error
    except Exception as exc:
        err = str(exc)
    finally:
        scan_done.set()
        step_task.cancel()
        try:
            await step_task
        except asyncio.CancelledError:
            pass

    if err or not result_text:
        await dashboard_broadcaster.publish_scan_failed(aid, err or "scanner returned no result")
        return

    try:
        result = json.loads(result_text)
    except Exception:
        await dashboard_broadcaster.publish_scan_failed(aid, "scanner returned non-JSON result")
        return

    # Persist result in a fresh session (request session is already closed)
    if db_module.AsyncSessionLocal is not None:
        try:
            async with db_module.AsyncSessionLocal() as db:
                row = await db.get(Agent, agent_id)
                if row is not None:
                    row.last_scan_result = result
                    row.last_scan_at = datetime.now(timezone.utc)
                    await db.commit()
        except Exception as exc:
            logger.warning("scan result persist failed", agent_id=aid, error=str(exc))

    await dashboard_broadcaster.publish_scan_complete(aid, result)
    logger.info("security scan complete", agent_id=aid, score=result.get("score"), risk=result.get("risk"))
