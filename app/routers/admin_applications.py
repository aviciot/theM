"""
Admin — Applications
CRUD for them.applications + them.entry_points.
One app = parent row (name, orchestrator, presentation).
N entry points = child rows (slug, type, access_policy, token_limit, enabled).
PATCH sends full desired entry_points array; server diffs atomically.
No Redis cache — not on hot path.
"""

import re
import uuid
from datetime import datetime
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.database import get_db
from app.models import Application, EntryPoint, Orchestrator
from app.utils.logger import logger

router = APIRouter(prefix="/admin/applications", tags=["admin-applications"])

_SLUG_RE = re.compile(r"^[a-z0-9_-]{1,64}$")
VALID_ENTRY_POINT_TYPES = {"websocket", "sse", "webrtc"}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class EntryPointIn(BaseModel):
    id: Optional[uuid.UUID] = None          # present = update existing, absent = create new
    slug: str = Field(..., description="Unique slug — ^[a-z0-9_-]{1,64}$")
    entry_point_type: str = Field(..., description="websocket | sse | webrtc")
    access_policy: Dict[str, Any] = Field(default_factory=lambda: {"mode": "token"})
    conversation_token_limit: Optional[int] = None
    enabled: bool = True


class EntryPointOut(BaseModel):
    id: uuid.UUID
    slug: str
    entry_point_type: str
    access_policy: Dict[str, Any]
    conversation_token_limit: Optional[int] = None
    enabled: bool
    created_at: datetime
    updated_at: datetime

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
        .options(selectinload(Application.entry_points))
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


def _to_out(app: Application, orch_name: Optional[str]) -> ApplicationOut:
    return ApplicationOut(
        id=app.id,
        name=app.name,
        orchestrator_id=app.orchestrator_id,
        orchestrator_name=orch_name,
        presentation=app.presentation or {},
        enabled=app.enabled,
        entry_points=[
            EntryPointOut(
                id=ep.id,
                slug=ep.slug,
                entry_point_type=ep.entry_point_type,
                access_policy=ep.access_policy or {"mode": "token"},
                conversation_token_limit=ep.conversation_token_limit,
                enabled=ep.enabled,
                created_at=ep.created_at,
                updated_at=ep.updated_at,
            )
            for ep in app.entry_points
        ],
        created_at=app.created_at,
        updated_at=app.updated_at,
    )


async def _apply_entry_point_diff(
    db: AsyncSession,
    app: Application,
    desired: List[EntryPointIn],
) -> None:
    """
    Diff desired entry_points against current children.
    - EP in desired with matching id → update
    - EP in desired without id (or unknown id) → create
    - EP in current not referenced in desired → delete
    All within the caller's transaction.
    """
    existing = {ep.id: ep for ep in app.entry_points}
    desired_ids = {ep.id for ep in desired if ep.id and ep.id in existing}

    # Delete missing
    for ep_id, ep in existing.items():
        if ep_id not in desired_ids:
            await db.delete(ep)

    # Update or create
    for ep_in in desired:
        if ep_in.id and ep_in.id in existing:
            ep = existing[ep_in.id]
            ep.slug = ep_in.slug
            ep.entry_point_type = ep_in.entry_point_type
            ep.access_policy = ep_in.access_policy
            ep.conversation_token_limit = ep_in.conversation_token_limit
            ep.enabled = ep_in.enabled
        else:
            db.add(EntryPoint(
                application_id=app.id,
                slug=ep_in.slug,
                entry_point_type=ep_in.entry_point_type,
                access_policy=ep_in.access_policy,
                conversation_token_limit=ep_in.conversation_token_limit,
                enabled=ep_in.enabled,
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
        .options(selectinload(Application.entry_points))
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

    for ep_in in body.entry_points:
        db.add(EntryPoint(
            application_id=app.id,
            slug=ep_in.slug,
            entry_point_type=ep_in.entry_point_type,
            access_policy=ep_in.access_policy,
            conversation_token_limit=ep_in.conversation_token_limit,
            enabled=ep_in.enabled,
        ))

    await db.commit()
    await db.refresh(app)
    # reload with children
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
        await _apply_entry_point_diff(db, app, body.entry_points)

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
