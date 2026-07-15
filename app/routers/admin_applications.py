"""
Admin — Applications
CRUD for them.applications + them.entry_points + them.app_orchestrators.
One app = parent row (name, orchestrator, presentation).
N entry points = child rows (slug, type, access_policy, token_limit, enabled).
Each entry point owns one AppOrchestrator instance (canvas node).
PATCH sends full desired entry_points array; server diffs atomically.
No Redis cache — not on hot path.
"""

import re
import re as _re
import secrets as _secrets
import uuid
from datetime import datetime
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select, delete, union_all
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

import app.database as db_module
from app.database import get_db
from app.models import Application, EntryPoint, Orchestrator, MiddlewareWiring, AppOrchestrator
from app.utils.logger import logger

router = APIRouter(prefix="/admin/applications", tags=["admin-applications"])

_SLUG_RE = re.compile(r"^[a-z0-9_-]{1,64}$")
_NAME_RE = _re.compile(r"^[a-z0-9_-]{1,64}$")
VALID_ENTRY_POINT_TYPES = {"websocket", "sse", "webrtc", "a2a"}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class AppOrchestratorIn(BaseModel):
    """Inline orchestrator instance config carried by each entry point.

    All fields are Optional so that a PATCH carrying only changed fields never
    silently resets unchanged ones to their defaults.  _update_app_orchestrator
    guards every assignment with `if value is not None`.
    """
    name: Optional[str] = None          # auto-generated if omitted
    display_name: Optional[str] = None
    system_prompt: Optional[str] = None
    llm_provider: Optional[str] = None
    llm_model: Optional[str] = None
    llm_api_key_encrypted: Optional[str] = None
    llm_base_url: Optional[str] = None
    max_iterations: Optional[int] = None
    max_parallel_tools: Optional[int] = None
    rate_limit_rpm: Optional[int] = None
    daily_budget_usd: Optional[float] = None
    allowed_agent_ids: Optional[List[uuid.UUID]] = None
    delegatable: Optional[bool] = None
    history_window: Optional[int] = None
    budget_tokens: Optional[int] = None
    enabled: Optional[bool] = None
    node_id: Optional[str] = None       # canvas node identity
    kind: Optional[str] = None
    # voice/memory fields optional, pass-through
    voice_enabled: Optional[bool] = None
    transcription_provider: Optional[str] = None
    transcription_model: Optional[str] = None
    tts_enabled: Optional[bool] = None
    tts_provider: Optional[str] = None
    tts_voice: Optional[str] = None
    memory_enabled: Optional[bool] = None
    summarize_every_n_calls: Optional[int] = None
    memory_raw_fallback_n: Optional[int] = None
    summarizer_provider: Optional[str] = None
    summarizer_model: Optional[str] = None


class AppOrchestratorOut(BaseModel):
    id: uuid.UUID
    name: str
    display_name: Optional[str]
    system_prompt: Optional[str]
    llm_provider: Optional[str]
    llm_model: Optional[str]
    max_iterations: int
    max_parallel_tools: int
    allowed_agent_ids: List[uuid.UUID]
    delegatable: bool
    kind: str
    node_id: Optional[str]
    enabled: bool
    history_window: int
    budget_tokens: Optional[int]
    voice_enabled: bool
    tts_enabled: bool
    memory_enabled: bool

    class Config:
        from_attributes = True


class EntryPointIn(BaseModel):
    id: Optional[uuid.UUID] = None          # present = update existing, absent = create new
    slug: str = Field(..., description="Unique slug — ^[a-z0-9_-]{1,64}$")
    entry_point_type: str = Field(..., description="websocket | sse | webrtc | a2a")
    access_policy: Dict[str, Any] = Field(default_factory=lambda: {"mode": "token"})
    conversation_token_limit: Optional[int] = None
    enabled: bool = True
    orchestrator: Optional[AppOrchestratorIn] = None


class EntryPointOut(BaseModel):
    id: uuid.UUID
    slug: str
    entry_point_type: str
    access_policy: Dict[str, Any]
    conversation_token_limit: Optional[int] = None
    enabled: bool
    created_at: datetime
    updated_at: datetime
    app_orchestrator: Optional[AppOrchestratorOut] = None

    class Config:
        from_attributes = True


class ApplicationCreate(BaseModel):
    name: str
    orchestrator_id: uuid.UUID
    presentation: Dict[str, Any] = Field(default_factory=dict)
    enabled: bool = True
    entry_points: List[EntryPointIn] = Field(..., min_length=1)


class ApplicationUpdate(BaseModel):
    name: Optional[str] = None
    orchestrator_id: Optional[uuid.UUID] = None
    presentation: Optional[Dict[str, Any]] = None
    enabled: Optional[bool] = None
    entry_points: Optional[List[EntryPointIn]] = None  # None = don't touch; [] = rejected


class ApplicationOut(BaseModel):
    id: uuid.UUID
    name: str
    orchestrator_id: uuid.UUID
    orchestrator_name: Optional[str]
    presentation: Dict[str, Any]
    enabled: bool
    entry_points: List[EntryPointOut]
    app_orchestrators: List[AppOrchestratorOut] = Field(default_factory=list)
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _validate_slug(slug: str) -> None:
    if not _SLUG_RE.match(slug):
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=f"Invalid slug '{slug}' — must match ^[a-z0-9_-]{{1,64}}$",
        )


def _validate_entry_point_type(ept: str) -> None:
    if ept not in VALID_ENTRY_POINT_TYPES:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=f"Invalid entry_point_type '{ept}' — must be one of {sorted(VALID_ENTRY_POINT_TYPES)}",
        )


def _validate_entry_points(eps: List[EntryPointIn]) -> None:
    """Validate all entry points in a list, checking for duplicate slugs within the body."""
    seen_slugs: set[str] = set()
    for ep in eps:
        _validate_slug(ep.slug)
        _validate_entry_point_type(ep.entry_point_type)
        if ep.slug in seen_slugs:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                detail=f"Duplicate slug '{ep.slug}' within entry_points",
            )
        seen_slugs.add(ep.slug)


async def _check_slug_conflicts(
    db: AsyncSession,
    slugs: List[str],
    exclude_app_id: Optional[uuid.UUID] = None,
) -> None:
    """Raise 409 if any slug is already owned by a different app."""
    q = select(EntryPoint).where(EntryPoint.slug.in_(slugs))
    if exclude_app_id:
        q = q.where(EntryPoint.application_id != exclude_app_id)
    result = await db.execute(q)
    conflict = result.scalars().first()
    if conflict:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f"Slug '{conflict.slug}' is already used by another application",
        )


async def _get_or_404(db: AsyncSession, app_id: uuid.UUID) -> Application:
    result = await db.execute(
        select(Application)
        .where(Application.id == app_id)
        .options(
            selectinload(Application.entry_points).selectinload(EntryPoint.app_orchestrator),
            selectinload(Application.app_orchestrators),
        )
    )
    row = result.scalar_one_or_none()
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Application not found")
    return row


async def _batch_orch_names(
    db: AsyncSession, orch_ids: set[uuid.UUID]
) -> Dict[uuid.UUID, str]:
    if not orch_ids:
        return {}
    result = await db.execute(select(Orchestrator).where(Orchestrator.id.in_(orch_ids)))
    return {o.id: o.name for o in result.scalars()}


async def _load_all_orch_names(db: AsyncSession) -> set[str]:
    """Load all orchestrator names from both tables (shared namespace)."""
    q1 = select(Orchestrator.name)
    q2 = select(AppOrchestrator.name)
    result = await db.execute(union_all(q1, q2))
    return {row[0] for row in result}


def _generate_orch_name(proposed: Optional[str], hint: str, db_names: set[str]) -> str:
    """Generate unique app_orchestrator name. Immutable after creation."""
    if proposed:
        name = proposed.strip().lower()
        if not _NAME_RE.match(name):
            raise HTTPException(422, f"Invalid orchestrator name '{name}': must match ^[a-z0-9_-]{{1,64}}$")
        if name.startswith("orch__"):
            raise HTTPException(422, "Orchestrator name may not start with 'orch__'")
        if name in db_names:
            raise HTTPException(409, f"Orchestrator name '{name}' already taken")
        return name
    # Auto-derive from hint
    base = _re.sub(r"[^a-z0-9_-]", "-", hint.strip().lower()).strip("-_")[:55] or "orch"
    if base.startswith("orch__"):
        base = base[6:] or "orch"
    if base not in db_names:
        return base
    for _ in range(10):
        cand = f"{base}-{_secrets.token_hex(3)}"[:64]
        if cand not in db_names:
            return cand
    raise HTTPException(500, "Could not allocate unique orchestrator name")


def _app_orch_out(ao: AppOrchestrator) -> AppOrchestratorOut:
    return AppOrchestratorOut(
        id=ao.id,
        name=ao.name,
        display_name=ao.display_name,
        system_prompt=ao.system_prompt,
        llm_provider=ao.llm_provider,
        llm_model=ao.llm_model,
        max_iterations=ao.max_iterations,
        max_parallel_tools=ao.max_parallel_tools,
        allowed_agent_ids=list(ao.allowed_agent_ids or []),
        delegatable=ao.delegatable,
        kind=ao.kind,
        node_id=ao.node_id,
        enabled=ao.enabled,
        history_window=ao.history_window,
        budget_tokens=ao.budget_tokens,
        voice_enabled=ao.voice_enabled,
        tts_enabled=ao.tts_enabled,
        memory_enabled=ao.memory_enabled,
    )


def _to_out(app: Application, orch_name: Optional[str]) -> ApplicationOut:
    ep_outs: List[EntryPointOut] = []
    for ep in app.entry_points:
        ao_out: Optional[AppOrchestratorOut] = None
        if ep.app_orchestrator is not None:
            ao_out = _app_orch_out(ep.app_orchestrator)
        ep_outs.append(EntryPointOut(
            id=ep.id,
            slug=ep.slug,
            entry_point_type=ep.entry_point_type,
            access_policy=ep.access_policy or {"mode": "token"},
            conversation_token_limit=ep.conversation_token_limit,
            enabled=ep.enabled,
            created_at=ep.created_at,
            updated_at=ep.updated_at,
            app_orchestrator=ao_out,
        ))
    ao_list = [_app_orch_out(ao) for ao in (app.app_orchestrators or [])]
    return ApplicationOut(
        id=app.id,
        name=app.name,
        orchestrator_id=app.orchestrator_id,
        orchestrator_name=orch_name,
        presentation=app.presentation or {},
        enabled=app.enabled,
        entry_points=ep_outs,
        app_orchestrators=ao_list,
        created_at=app.created_at,
        updated_at=app.updated_at,
    )


async def _create_app_orchestrator(
    db: AsyncSession,
    app_id: uuid.UUID,
    ep_in: EntryPointIn,
    hint: str,
    all_names: set[str],
) -> AppOrchestrator:
    """Create an AppOrchestrator for a new entry point. Mutates all_names in-place."""
    orch_cfg = ep_in.orchestrator  # may be None → use defaults
    proposed_name = orch_cfg.name if orch_cfg else None
    generated_name = _generate_orch_name(proposed_name, hint, all_names)
    all_names.add(generated_name)  # reserve immediately within this batch

    ao = AppOrchestrator(
        application_id=app_id,
        name=generated_name,
        display_name=orch_cfg.display_name if orch_cfg else None,
        system_prompt=orch_cfg.system_prompt if orch_cfg else None,
        llm_provider=orch_cfg.llm_provider if orch_cfg else None,
        llm_model=orch_cfg.llm_model if orch_cfg else None,
        llm_api_key_encrypted=orch_cfg.llm_api_key_encrypted if orch_cfg else None,
        llm_base_url=orch_cfg.llm_base_url if orch_cfg else None,
        max_iterations=(orch_cfg.max_iterations if (orch_cfg and orch_cfg.max_iterations is not None) else 10),
        max_parallel_tools=(orch_cfg.max_parallel_tools if (orch_cfg and orch_cfg.max_parallel_tools is not None) else 3),
        rate_limit_rpm=orch_cfg.rate_limit_rpm if orch_cfg else None,
        daily_budget_usd=orch_cfg.daily_budget_usd if orch_cfg else None,
        allowed_agent_ids=([str(i) for i in orch_cfg.allowed_agent_ids] if (orch_cfg and orch_cfg.allowed_agent_ids is not None) else []),
        delegatable=(orch_cfg.delegatable if (orch_cfg and orch_cfg.delegatable is not None) else False),
        history_window=(orch_cfg.history_window if (orch_cfg and orch_cfg.history_window is not None) else 20),
        budget_tokens=orch_cfg.budget_tokens if orch_cfg else None,
        enabled=(orch_cfg.enabled if (orch_cfg and orch_cfg.enabled is not None) else True),
        node_id=orch_cfg.node_id if orch_cfg else None,
        kind=(orch_cfg.kind if (orch_cfg and orch_cfg.kind is not None) else "standard"),
        voice_enabled=(orch_cfg.voice_enabled if (orch_cfg and orch_cfg.voice_enabled is not None) else False),
        transcription_provider=orch_cfg.transcription_provider if orch_cfg else None,
        transcription_model=orch_cfg.transcription_model if orch_cfg else None,
        tts_enabled=(orch_cfg.tts_enabled if (orch_cfg and orch_cfg.tts_enabled is not None) else False),
        tts_provider=orch_cfg.tts_provider if orch_cfg else None,
        tts_voice=orch_cfg.tts_voice if orch_cfg else None,
        memory_enabled=(orch_cfg.memory_enabled if (orch_cfg and orch_cfg.memory_enabled is not None) else False),
        summarize_every_n_calls=(orch_cfg.summarize_every_n_calls if (orch_cfg and orch_cfg.summarize_every_n_calls is not None) else 3),
        memory_raw_fallback_n=(orch_cfg.memory_raw_fallback_n if (orch_cfg and orch_cfg.memory_raw_fallback_n is not None) else 5),
        summarizer_provider=orch_cfg.summarizer_provider if orch_cfg else None,
        summarizer_model=orch_cfg.summarizer_model if orch_cfg else None,
    )
    db.add(ao)
    return ao


def _update_app_orchestrator(ao: AppOrchestrator, orch_cfg: AppOrchestratorIn) -> None:
    """Apply mutable config fields to an existing AppOrchestrator. Name is immutable.

    Every field is guarded with `if value is not None` so that a PATCH that omits
    a field never silently resets it to a default.
    """
    if orch_cfg.display_name is not None:
        ao.display_name = orch_cfg.display_name
    if orch_cfg.system_prompt is not None:
        ao.system_prompt = orch_cfg.system_prompt
    if orch_cfg.llm_provider is not None:
        ao.llm_provider = orch_cfg.llm_provider
    if orch_cfg.llm_model is not None:
        ao.llm_model = orch_cfg.llm_model
    if orch_cfg.llm_api_key_encrypted is not None:
        ao.llm_api_key_encrypted = orch_cfg.llm_api_key_encrypted
    if orch_cfg.llm_base_url is not None:
        ao.llm_base_url = orch_cfg.llm_base_url
    if orch_cfg.max_iterations is not None:
        ao.max_iterations = orch_cfg.max_iterations
    if orch_cfg.max_parallel_tools is not None:
        ao.max_parallel_tools = orch_cfg.max_parallel_tools
    if orch_cfg.rate_limit_rpm is not None:
        ao.rate_limit_rpm = orch_cfg.rate_limit_rpm
    if orch_cfg.daily_budget_usd is not None:
        ao.daily_budget_usd = orch_cfg.daily_budget_usd
    if orch_cfg.allowed_agent_ids is not None:
        ao.allowed_agent_ids = [str(i) for i in orch_cfg.allowed_agent_ids]
    if orch_cfg.delegatable is not None:
        ao.delegatable = orch_cfg.delegatable
    if orch_cfg.history_window is not None:
        ao.history_window = orch_cfg.history_window
    if orch_cfg.budget_tokens is not None:
        ao.budget_tokens = orch_cfg.budget_tokens
    if orch_cfg.enabled is not None:
        ao.enabled = orch_cfg.enabled
    if orch_cfg.node_id is not None:
        ao.node_id = orch_cfg.node_id
    if orch_cfg.kind is not None:
        ao.kind = orch_cfg.kind
    if orch_cfg.voice_enabled is not None:
        ao.voice_enabled = orch_cfg.voice_enabled
    if orch_cfg.transcription_provider is not None:
        ao.transcription_provider = orch_cfg.transcription_provider
    if orch_cfg.transcription_model is not None:
        ao.transcription_model = orch_cfg.transcription_model
    if orch_cfg.tts_enabled is not None:
        ao.tts_enabled = orch_cfg.tts_enabled
    if orch_cfg.tts_provider is not None:
        ao.tts_provider = orch_cfg.tts_provider
    if orch_cfg.tts_voice is not None:
        ao.tts_voice = orch_cfg.tts_voice
    if orch_cfg.memory_enabled is not None:
        ao.memory_enabled = orch_cfg.memory_enabled
    if orch_cfg.summarize_every_n_calls is not None:
        ao.summarize_every_n_calls = orch_cfg.summarize_every_n_calls
    if orch_cfg.memory_raw_fallback_n is not None:
        ao.memory_raw_fallback_n = orch_cfg.memory_raw_fallback_n
    if orch_cfg.summarizer_provider is not None:
        ao.summarizer_provider = orch_cfg.summarizer_provider
    if orch_cfg.summarizer_model is not None:
        ao.summarizer_model = orch_cfg.summarizer_model


async def _apply_entry_point_diff(
    db: AsyncSession,
    app: Application,
    desired: List[EntryPointIn],
    all_names: set[str],
) -> None:
    """
    Diff desired entry_points against current children, keyed by slug.
    Slug is globally unique and immutable — a rename is delete + create.
    - slug in both current and desired → update mutable fields + AppOrchestrator config
    - slug only in desired → create EP + new AppOrchestrator
    - slug only in current → delete EP (AppOrchestrator deleted if no other EP references it)
    All within the caller's transaction.
    """
    existing = {ep.slug: ep for ep in app.entry_points}
    desired_slugs = {ep.slug for ep in desired}

    # Delete rows whose slug is no longer desired; also delete orphaned AppOrchestrators
    for slug, ep in existing.items():
        if slug not in desired_slugs:
            # Check if this AppOrchestrator is referenced by any surviving EP
            if ep.app_orchestrator_id is not None:
                sibling_uses = sum(
                    1 for other_ep in app.entry_points
                    if other_ep.slug != slug
                    and other_ep.app_orchestrator_id == ep.app_orchestrator_id
                    and other_ep.slug in desired_slugs
                )
                if sibling_uses == 0:
                    # Safe to delete the AppOrchestrator — no other EP references it
                    ao = ep.app_orchestrator
                    if ao is not None:
                        await db.delete(ao)
            await db.delete(ep)

    # Update existing (by slug) or create new
    for ep_in in desired:
        ep = existing.get(ep_in.slug)
        if ep is not None:
            # Update mutable EP fields
            ep.entry_point_type = ep_in.entry_point_type
            ep.access_policy = ep_in.access_policy
            ep.conversation_token_limit = ep_in.conversation_token_limit
            ep.enabled = ep_in.enabled
            # Update linked AppOrchestrator config if provided
            if ep_in.orchestrator is not None and ep.app_orchestrator is not None:
                _update_app_orchestrator(ep.app_orchestrator, ep_in.orchestrator)
            elif ep_in.orchestrator is not None and ep.app_orchestrator is None:
                # EP exists but has no orchestrator yet — create one and link it
                ao = await _create_app_orchestrator(db, app.id, ep_in, ep_in.slug, all_names)
                await db.flush()
                ep.app_orchestrator_id = ao.id
        else:
            # Create AppOrchestrator first, then EP
            ao = await _create_app_orchestrator(db, app.id, ep_in, ep_in.slug, all_names)
            await db.flush()  # get ao.id
            db.add(EntryPoint(
                application_id=app.id,
                slug=ep_in.slug,
                entry_point_type=ep_in.entry_point_type,
                access_policy=ep_in.access_policy,
                conversation_token_limit=ep_in.conversation_token_limit,
                enabled=ep_in.enabled,
                app_orchestrator_id=ao.id,
            ))


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[ApplicationOut])
async def list_applications(
    enabled_only: bool = False,
    db: AsyncSession = Depends(get_db),
):
    q = (
        select(Application)
        .options(
            selectinload(Application.entry_points).selectinload(EntryPoint.app_orchestrator),
            selectinload(Application.app_orchestrators),
        )
        .order_by(Application.name)
    )
    if enabled_only:
        q = q.where(Application.enabled == True)  # noqa: E712
    rows = (await db.execute(q)).scalars().all()
    orch_names = await _batch_orch_names(db, {r.orchestrator_id for r in rows})
    return [_to_out(r, orch_names.get(r.orchestrator_id)) for r in rows]


@router.post("", response_model=ApplicationOut, status_code=status.HTTP_201_CREATED)
async def create_application(body: ApplicationCreate, db: AsyncSession = Depends(get_db)):
    _validate_entry_points(body.entry_points)

    orch = await db.get(Orchestrator, body.orchestrator_id)
    if orch is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=f"Orchestrator '{body.orchestrator_id}' not found",
        )

    await _check_slug_conflicts(db, [ep.slug for ep in body.entry_points])

    app = Application(
        name=body.name,
        orchestrator_id=body.orchestrator_id,
        presentation=body.presentation,
        enabled=body.enabled,
    )
    db.add(app)
    await db.flush()  # get app.id before inserting children

    # Load existing orchestrator names for uniqueness checks (shared namespace)
    all_names = await _load_all_orch_names(db)

    for ep_in in body.entry_points:
        # Create AppOrchestrator instance for this entry point
        ao = await _create_app_orchestrator(db, app.id, ep_in, ep_in.slug, all_names)
        await db.flush()  # get ao.id
        db.add(EntryPoint(
            application_id=app.id,
            slug=ep_in.slug,
            entry_point_type=ep_in.entry_point_type,
            access_policy=ep_in.access_policy,
            conversation_token_limit=ep_in.conversation_token_limit,
            enabled=ep_in.enabled,
            app_orchestrator_id=ao.id,
        ))

    await db.commit()
    await db.refresh(app)
    # reload with children (eager load AppOrchestrators + EP.app_orchestrator)
    app = await _get_or_404(db, app.id)
    logger.info("application created", app_id=str(app.id), name=body.name,
                slugs=[ep.slug for ep in app.entry_points])
    return _to_out(app, orch.name)


@router.get("/{app_id}", response_model=ApplicationOut)
async def get_application(app_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    app = await _get_or_404(db, app_id)
    orch_names = await _batch_orch_names(db, {app.orchestrator_id})
    return _to_out(app, orch_names.get(app.orchestrator_id))


@router.patch("/{app_id}", response_model=ApplicationOut)
async def update_application(
    app_id: uuid.UUID,
    body: ApplicationUpdate,
    db: AsyncSession = Depends(get_db),
):
    app = await _get_or_404(db, app_id)

    if body.name is not None:
        app.name = body.name
    if body.presentation is not None:
        app.presentation = body.presentation
    if body.enabled is not None:
        app.enabled = body.enabled
    if body.orchestrator_id is not None:
        orch = await db.get(Orchestrator, body.orchestrator_id)
        if orch is None:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                detail=f"Orchestrator '{body.orchestrator_id}' not found",
            )
        app.orchestrator_id = body.orchestrator_id

    if body.entry_points is not None:
        if len(body.entry_points) == 0:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                detail="entry_points must not be empty — an app requires at least one entry point",
            )
        _validate_entry_points(body.entry_points)
        await _check_slug_conflicts(db, [ep.slug for ep in body.entry_points], exclude_app_id=app_id)
        all_names = await _load_all_orch_names(db)
        await _apply_entry_point_diff(db, app, body.entry_points, all_names)

    await db.commit()
    app = await _get_or_404(db, app_id)
    orch_names = await _batch_orch_names(db, {app.orchestrator_id})
    logger.info("application updated", app_id=str(app_id), name=app.name,
                slugs=[ep.slug for ep in app.entry_points])
    return _to_out(app, orch_names.get(app.orchestrator_id))


@router.delete("/{app_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_application(app_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    app = await _get_or_404(db, app_id)
    name = app.name
    await db.delete(app)
    await db.commit()
    logger.info("application deleted", app_id=str(app_id), name=name)


# ------------------------------------------------------------------ #
# Middleware wiring endpoint                                           #
# ------------------------------------------------------------------ #

class MiddlewareWiringIn(BaseModel):
    def_id: uuid.UUID
    agent_id: uuid.UUID
    position: int = 0
    config_override: Dict[str, Any] = Field(default_factory=dict)
    node_id: str = ""
    enabled: bool = True


class MiddlewareWiringsBody(BaseModel):
    wirings: List[MiddlewareWiringIn] = Field(default_factory=list)


async def _flush_mw_chain_cache(app_id: uuid.UUID) -> None:
    """Bust Redis middleware chain cache for this app."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        pattern = f"them:mw:chain:{app_id}:*"
        cursor = 0
        while True:
            cursor, keys = await redis.scan(cursor=cursor, match=pattern, count=200)
            if keys:
                await redis.delete(*keys)
            if cursor == 0:
                break
    except Exception as exc:
        logger.warning("mw chain cache flush failed", app_id=str(app_id), error=str(exc))


@router.put("/{app_id}/middleware-wirings", status_code=status.HTTP_204_NO_CONTENT)
async def put_middleware_wirings(
    app_id: uuid.UUID,
    body: MiddlewareWiringsBody,
    db: AsyncSession = Depends(get_db),
):
    """
    Idempotent full-replace of middleware wirings for an application.
    Deletes all existing wirings then bulk-inserts the new ones.
    """
    await _get_or_404(db, app_id)  # 404 if app not found

    # Delete all existing wirings for this app
    await db.execute(
        delete(MiddlewareWiring).where(MiddlewareWiring.application_id == app_id)
    )

    # Bulk insert new wirings
    for w in body.wirings:
        db.add(MiddlewareWiring(
            application_id=app_id,
            agent_id=w.agent_id,
            def_id=w.def_id,
            position=w.position,
            config_override=w.config_override,
            node_id=w.node_id or None,
            enabled=w.enabled,
        ))

    await db.commit()
    await _flush_mw_chain_cache(app_id)
    logger.info("middleware wirings replaced", app_id=str(app_id), count=len(body.wirings))
