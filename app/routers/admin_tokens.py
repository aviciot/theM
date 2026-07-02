"""
Admin — Access Tokens
Manage odin.access_tokens (bearer tokens for WS /ws/orchestrate/{name}).

Plaintext token returned ONCE on creation — never stored.
Only sha256(token) stored in DB.
"""

import hashlib
import secrets
import uuid
from datetime import datetime
from typing import List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import AccessToken, Orchestrator
from app.services.token_cache import invalidate_token
from app.utils.logger import logger

router = APIRouter(prefix="/admin/tokens", tags=["admin-tokens"])


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class TokenCreate(BaseModel):
    label: str = Field(..., description="Human-readable label for this token")
    user_id: int = Field(..., description="User this token belongs to")
    orchestrator_id: Optional[uuid.UUID] = Field(
        None,
        description="Scope to one orchestrator. Null = any orchestrator.",
    )
    expires_at: Optional[datetime] = Field(None, description="Optional expiry (UTC)")


class TokenOut(BaseModel):
    id: uuid.UUID
    label: str
    user_id: int
    orchestrator_id: Optional[uuid.UUID]
    enabled: bool
    expires_at: Optional[datetime]
    last_used_at: Optional[datetime]
    created_at: datetime

    class Config:
        from_attributes = True


class TokenCreatedOut(TokenOut):
    token: str = Field(..., description="Plaintext token — shown ONCE, store it now")


class TokenUpdate(BaseModel):
    label: Optional[str] = None
    enabled: Optional[bool] = None
    expires_at: Optional[datetime] = None


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _hash(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def _row_to_out(row: AccessToken) -> TokenOut:
    return TokenOut(
        id=row.id,
        label=row.label,
        user_id=row.user_id,
        orchestrator_id=row.orchestrator_id,
        enabled=row.enabled,
        expires_at=row.expires_at,
        last_used_at=row.last_used_at,
        created_at=row.created_at,
    )


async def _get_or_404(db: AsyncSession, token_id: uuid.UUID) -> AccessToken:
    row = await db.get(AccessToken, token_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Token not found")
    return row


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[TokenOut])
async def list_tokens(
    user_id: Optional[int] = None,
    db: AsyncSession = Depends(get_db),
):
    q = select(AccessToken).order_by(AccessToken.created_at.desc())
    if user_id is not None:
        q = q.where(AccessToken.user_id == user_id)
    result = await db.execute(q)
    return [_row_to_out(r) for r in result.scalars()]


@router.post("", response_model=TokenCreatedOut, status_code=status.HTTP_201_CREATED)
async def create_token(body: TokenCreate, db: AsyncSession = Depends(get_db)):
    # Validate orchestrator exists if scoped
    if body.orchestrator_id:
        orch = await db.get(Orchestrator, body.orchestrator_id)
        if orch is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=f"Orchestrator {body.orchestrator_id} not found",
            )

    plaintext = secrets.token_urlsafe(32)
    token_hash = _hash(plaintext)

    row = AccessToken(
        token_hash=token_hash,
        label=body.label,
        user_id=body.user_id,
        orchestrator_id=body.orchestrator_id,
        expires_at=body.expires_at,
        enabled=True,
    )
    db.add(row)
    await db.commit()
    await db.refresh(row)

    logger.info("access_token created", label=body.label, user_id=body.user_id)

    out = _row_to_out(row)
    return TokenCreatedOut(**out.model_dump(), token=plaintext)


@router.get("/{token_id}", response_model=TokenOut)
async def get_token(token_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    return _row_to_out(await _get_or_404(db, token_id))


@router.patch("/{token_id}", response_model=TokenOut)
async def update_token(
    token_id: uuid.UUID,
    body: TokenUpdate,
    db: AsyncSession = Depends(get_db),
):
    row = await _get_or_404(db, token_id)

    if body.label is not None:
        row.label = body.label
    if body.enabled is not None:
        row.enabled = body.enabled
    if body.expires_at is not None:
        row.expires_at = body.expires_at

    await db.commit()
    await db.refresh(row)

    # Invalidate cache so revoke takes effect as soon as possible
    await invalidate_token(row.token_hash)
    logger.info("access_token updated", token_id=str(token_id), enabled=row.enabled)
    return _row_to_out(row)


@router.delete("/{token_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_token(token_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, token_id)
    token_hash = row.token_hash
    await db.delete(row)
    await db.commit()
    await invalidate_token(token_hash)
    logger.info("access_token deleted", token_id=str(token_id))
