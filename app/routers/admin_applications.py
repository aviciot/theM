"""
Admin — Applications
CRUD for them.applications. No Redis cache needed (not on hot path).
"""

import re
import uuid
from datetime import datetime
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Application, Orchestrator
from app.utils.logger import logger

router = APIRouter(prefix="/admin/applications", tags=["admin-applications"])

_SLUG_RE = re.compile(r"^[a-z0-9_-]{1,64}$")

VALID_ENTRY_POINT_TYPES = {"websocket", "sse", "webrtc"}


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class ApplicationCreate(BaseModel):
    name: str
    slug: str = Field(..., description="Unique slug — ^[a-z0-9_-]{1,64}$")
    entry_point_type: str = Field(..., description="websocket | sse | webrtc")
    orchestrator_id: uuid.UUID
    access_policy: Dict[str, Any] = Field(default_factory=lambda: {"mode": "token"})
    presentation: Dict[str, Any] = Field(default_factory=dict)
    enabled: bool = True
    conversation_token_limit: Optional[int] = Field(None, description="Max tokens per conversation session. NULL = no limit.")


class ApplicationUpdate(BaseModel):
    name: Optional[str] = None
    slug: Optional[str] = None
    entry_point_type: Optional[str] = None
    orchestrator_id: Optional[uuid.UUID] = None
    access_policy: Optional[Dict[str, Any]] = None
    presentation: Optional[Dict[str, Any]] = None
    enabled: Optional[bool] = None
    conversation_token_limit: Optional[int] = None


class ApplicationOut(BaseModel):
    id: uuid.UUID
    name: str
    slug: str
    entry_point_type: str
    orchestrator_id: uuid.UUID
    orchestrator_name: Optional[str]
    access_policy: Dict[str, Any]
    presentation: Dict[str, Any]
    enabled: bool
    conversation_token_limit: Optional[int] = None
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


async def _get_or_404(db: AsyncSession, app_id: uuid.UUID) -> Application:
    row = await db.get(Application, app_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Application not found")
    return row


async def _orchestrator_name(db: AsyncSession, orchestrator_id: uuid.UUID) -> Optional[str]:
    row = await db.get(Orchestrator, orchestrator_id)
    return row.name if row else None


def _row_to_out(row: Application, orch_name: Optional[str]) -> ApplicationOut:
    return ApplicationOut(
        id=row.id,
        name=row.name,
        slug=row.slug,
        entry_point_type=row.entry_point_type,
        orchestrator_id=row.orchestrator_id,
        orchestrator_name=orch_name,
        access_policy=row.access_policy or {"mode": "token"},
        presentation=row.presentation or {},
        enabled=row.enabled,
        conversation_token_limit=getattr(row, "conversation_token_limit", None),
        created_at=row.created_at,
        updated_at=row.updated_at,
    )


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[ApplicationOut])
async def list_applications(
    enabled_only: bool = False,
    db: AsyncSession = Depends(get_db),
):
    q = select(Application).order_by(Application.name)
    if enabled_only:
        q = q.where(Application.enabled == True)  # noqa: E712
    result = await db.execute(q)
    rows = result.scalars().all()

    # Batch-load orchestrator names
    orch_ids = {r.orchestrator_id for r in rows}
    orch_map: Dict[uuid.UUID, str] = {}
    if orch_ids:
        oq = await db.execute(select(Orchestrator).where(Orchestrator.id.in_(orch_ids)))
        for o in oq.scalars():
            orch_map[o.id] = o.name

    return [_row_to_out(r, orch_map.get(r.orchestrator_id)) for r in rows]


@router.post("", response_model=ApplicationOut, status_code=status.HTTP_201_CREATED)
async def create_application(body: ApplicationCreate, db: AsyncSession = Depends(get_db)):
    _validate_slug(body.slug)
    _validate_entry_point_type(body.entry_point_type)

    # Check for duplicate slug
    existing = await db.execute(select(Application).where(Application.slug == body.slug))
    if existing.scalar_one_or_none():
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f"Application with slug '{body.slug}' already exists",
        )

    # Verify orchestrator exists
    orch = await db.get(Orchestrator, body.orchestrator_id)
    if orch is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=f"Orchestrator '{body.orchestrator_id}' not found",
        )

    row = Application(
        name=body.name,
        slug=body.slug,
        entry_point_type=body.entry_point_type,
        orchestrator_id=body.orchestrator_id,
        access_policy=body.access_policy,
        presentation=body.presentation,
        enabled=body.enabled,
        conversation_token_limit=body.conversation_token_limit,
    )
    db.add(row)
    await db.commit()
    await db.refresh(row)
    logger.info("application created", slug=body.slug, name=body.name)
    return _row_to_out(row, orch.name)


@router.get("/{app_id}", response_model=ApplicationOut)
async def get_application(app_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, app_id)
    orch_name = await _orchestrator_name(db, row.orchestrator_id)
    return _row_to_out(row, orch_name)


@router.patch("/{app_id}", response_model=ApplicationOut)
async def update_application(
    app_id: uuid.UUID,
    body: ApplicationUpdate,
    db: AsyncSession = Depends(get_db),
):
    row = await _get_or_404(db, app_id)

    if body.name is not None:
        row.name = body.name
    if body.slug is not None:
        _validate_slug(body.slug)
        # Check uniqueness only if slug is actually changing
        if body.slug != row.slug:
            existing = await db.execute(select(Application).where(Application.slug == body.slug))
            if existing.scalar_one_or_none():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail=f"Application with slug '{body.slug}' already exists",
                )
        row.slug = body.slug
    if body.entry_point_type is not None:
        _validate_entry_point_type(body.entry_point_type)
        row.entry_point_type = body.entry_point_type
    if body.orchestrator_id is not None:
        orch = await db.get(Orchestrator, body.orchestrator_id)
        if orch is None:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                detail=f"Orchestrator '{body.orchestrator_id}' not found",
            )
        row.orchestrator_id = body.orchestrator_id
    if body.access_policy is not None:
        row.access_policy = body.access_policy
    if body.presentation is not None:
        row.presentation = body.presentation
    if body.enabled is not None:
        row.enabled = body.enabled
    if body.conversation_token_limit is not None:
        row.conversation_token_limit = body.conversation_token_limit

    await db.commit()
    await db.refresh(row)
    orch_name = await _orchestrator_name(db, row.orchestrator_id)
    logger.info("application updated", app_id=str(app_id), slug=row.slug)
    return _row_to_out(row, orch_name)


@router.delete("/{app_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_application(app_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, app_id)
    slug = row.slug
    await db.delete(row)
    await db.commit()
    logger.info("application deleted", app_id=str(app_id), slug=slug)
