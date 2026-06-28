"""
Admin — LLM Providers
CRUD for odin.llm_providers + llm_routing config key.

All API keys are stored Fernet-encrypted. The GET responses return a masked
key ("sk-...****") so the caller can tell whether a key is set, without ever
returning the plaintext.
"""

from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Config, LLMProvider
from app.utils.crypto import decrypt_value, encrypt_value
from app.utils.logger import logger

router = APIRouter(prefix="/admin/llm-providers", tags=["admin-llm-providers"])


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class LLMProviderCreate(BaseModel):
    name: str = Field(..., description="Unique slug, e.g. 'anthropic'")
    display_name: str
    api_key: Optional[str] = Field(None, description="Plaintext API key — stored encrypted")
    base_url: Optional[str] = None
    default_model: str
    model_pricing: Dict[str, Any] = Field(default_factory=dict)
    enabled: bool = True


class LLMProviderUpdate(BaseModel):
    display_name: Optional[str] = None
    api_key: Optional[str] = Field(None, description="Set to rotate the key; omit to leave unchanged")
    base_url: Optional[str] = None
    default_model: Optional[str] = None
    model_pricing: Optional[Dict[str, Any]] = None
    enabled: Optional[bool] = None


class LLMProviderOut(BaseModel):
    id: int
    name: str
    display_name: str
    api_key_set: bool
    api_key_masked: Optional[str]
    base_url: Optional[str]
    default_model: str
    model_pricing: Dict[str, Any]
    enabled: bool

    class Config:
        from_attributes = True


class LLMRoutingConfig(BaseModel):
    default_provider: str = "anthropic"
    default_model: str = "claude-sonnet-4-6"
    fallback_provider: Optional[str] = None
    fallback_model: Optional[str] = None


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _mask_key(encrypted: Optional[str]) -> tuple[bool, Optional[str]]:
    """Return (key_is_set, masked_representation)."""
    if not encrypted:
        return False, None
    try:
        plain = decrypt_value(encrypted)
        if len(plain) <= 8:
            return True, "****"
        return True, plain[:4] + "..." + plain[-4:]
    except Exception:
        return True, "****"


def _row_to_out(row: LLMProvider) -> LLMProviderOut:
    key_set, masked = _mask_key(row.api_key_encrypted)
    return LLMProviderOut(
        id=row.id,
        name=row.name,
        display_name=row.display_name,
        api_key_set=key_set,
        api_key_masked=masked,
        base_url=row.base_url,
        default_model=row.default_model,
        model_pricing=row.model_pricing or {},
        enabled=row.enabled,
    )


async def _get_or_404(db: AsyncSession, provider_id: int) -> LLMProvider:
    row = await db.get(LLMProvider, provider_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="LLM provider not found")
    return row


# ------------------------------------------------------------------ #
# Routes — provider CRUD                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[LLMProviderOut])
async def list_providers(db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(LLMProvider).order_by(LLMProvider.id))
    return [_row_to_out(r) for r in result.scalars()]


@router.post("", response_model=LLMProviderOut, status_code=status.HTTP_201_CREATED)
async def create_provider(body: LLMProviderCreate, db: AsyncSession = Depends(get_db)):
    existing = await db.execute(select(LLMProvider).where(LLMProvider.name == body.name))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f"Provider '{body.name}' already exists")

    encrypted_key = encrypt_value(body.api_key) if body.api_key else None
    row = LLMProvider(
        name=body.name,
        display_name=body.display_name,
        api_key_encrypted=encrypted_key,
        base_url=body.base_url,
        default_model=body.default_model,
        model_pricing=body.model_pricing,
        enabled=body.enabled,
    )
    db.add(row)
    await db.commit()
    await db.refresh(row)
    logger.info("LLM provider created", name=body.name)
    return _row_to_out(row)


@router.get("/{provider_id}", response_model=LLMProviderOut)
async def get_provider(provider_id: int, db: AsyncSession = Depends(get_db)):
    return _row_to_out(await _get_or_404(db, provider_id))


@router.patch("/{provider_id}", response_model=LLMProviderOut)
async def update_provider(
    provider_id: int,
    body: LLMProviderUpdate,
    db: AsyncSession = Depends(get_db),
):
    row = await _get_or_404(db, provider_id)

    if body.display_name is not None:
        row.display_name = body.display_name
    if body.api_key is not None:
        row.api_key_encrypted = encrypt_value(body.api_key)
    if body.base_url is not None:
        row.base_url = body.base_url
    if body.default_model is not None:
        row.default_model = body.default_model
    if body.model_pricing is not None:
        row.model_pricing = body.model_pricing
    if body.enabled is not None:
        row.enabled = body.enabled

    await db.commit()
    await db.refresh(row)
    logger.info("LLM provider updated", id=provider_id, name=row.name)
    return _row_to_out(row)


@router.delete("/{provider_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_provider(provider_id: int, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, provider_id)
    await db.delete(row)
    await db.commit()
    logger.info("LLM provider deleted", id=provider_id, name=row.name)


# ------------------------------------------------------------------ #
# Routes — llm_routing config                                          #
# ------------------------------------------------------------------ #

@router.get("/routing/config", response_model=LLMRoutingConfig)
async def get_routing(db: AsyncSession = Depends(get_db)):
    row = await db.get(Config, "llm_routing")
    if row is None:
        return LLMRoutingConfig()
    return LLMRoutingConfig(**row.config_value)


@router.put("/routing/config", response_model=LLMRoutingConfig)
async def set_routing(body: LLMRoutingConfig, db: AsyncSession = Depends(get_db)):
    row = await db.get(Config, "llm_routing")
    if row is None:
        row = Config(config_key="llm_routing", config_value=body.model_dump())
        db.add(row)
    else:
        row.config_value = body.model_dump()
    await db.commit()
    logger.info("llm_routing config updated", config=body.model_dump())
    return body
