"""
WebRTC room token endpoint — Workstream B LiveKit integration.

Routes:
  GET /apps/{slug}/webrtc/token  → mint a signed LiveKit JWT for the caller

Auth mirrors apps.py: access_policy.mode controls whether Bearer is required.
"""

import json
import uuid
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, HTTPException, Query, Request
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.config import settings
from app.models import Application, Orchestrator
from app.services.auth_client import validate_jwt
from app.services.token_cache import validate_bearer_token

router = APIRouter(tags=["apps"])


# ─────────────────────────────────────────────────────────────────────────────
# Auth helpers (duplicated from apps.py — do not import from there)
# ─────────────────────────────────────────────────────────────────────────────

async def _resolve_bearer(request: Request) -> dict | None:
    auth = request.headers.get("authorization", "")
    if not auth.lower().startswith("bearer "):
        return None
    raw_token = auth[7:].strip()
    if db_module.AsyncSessionLocal is None:
        return None
    async with db_module.AsyncSessionLocal() as db:
        payload = await validate_bearer_token(raw_token, db)
        if payload is not None:
            expires_at_raw = payload.get("expires_at")
            if expires_at_raw:
                try:
                    if datetime.fromisoformat(expires_at_raw) < datetime.now(timezone.utc):
                        return None
                except (ValueError, TypeError):
                    pass
            return payload
        jwt_payload = await validate_jwt(raw_token)
        if jwt_payload and jwt_payload.get("role") in ("admin", "super_admin"):
            return {"user_id": jwt_payload.get("user_id", 0), "orchestrator_id": None, "expires_at": None}
    return None


def _is_valid_uuid(value: str) -> bool:
    try:
        uuid.UUID(value)
        return True
    except (ValueError, AttributeError):
        return False


# ─────────────────────────────────────────────────────────────────────────────
# Application loader
# ─────────────────────────────────────────────────────────────────────────────

async def _load_app(db: AsyncSession, slug: str) -> Application:
    result = await db.execute(
        select(Application).where(Application.slug == slug, Application.enabled == True)
    )
    app_row = result.scalar_one_or_none()
    if app_row is None:
        raise HTTPException(status_code=404, detail=f"Application '{slug}' not found")
    return app_row


# ─────────────────────────────────────────────────────────────────────────────
# WebRTC token endpoint
# ─────────────────────────────────────────────────────────────────────────────

@router.get("/apps/{slug}/webrtc/token", tags=["apps"])
async def webrtc_token(
    slug: str,
    request: Request,
    context_id: Optional[str] = Query(default=None),
):
    """
    Mint a signed LiveKit JWT room token for the named application.

    Returns:
      token      — LiveKit JWT signed with LIVEKIT_API_KEY / LIVEKIT_API_SECRET
      url        — browser-facing LiveKit URL (LIVEKIT_PUBLIC_URL)
      room       — room name: "{slug}-{context_id}"
      context_id — UUID that scopes the conversation
    """
    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Service not ready")

    if not settings.livekit.api_key or not settings.livekit.api_secret:
        raise HTTPException(status_code=503, detail="LiveKit not configured")

    async with db_module.AsyncSessionLocal() as db:
        app_row = await _load_app(db, slug)

        if app_row.entry_point_type != "webrtc":
            raise HTTPException(status_code=400, detail="Application entry point is not webrtc")

        policy = app_row.access_policy or {}
        if policy.get("mode") != "public":
            token_payload = await _resolve_bearer(request)
            if token_payload is None:
                raise HTTPException(status_code=401, detail="Authorization required")
        else:
            token_payload = {"user_id": 0, "orchestrator_id": None, "expires_at": None}

        orch = await db.get(Orchestrator, app_row.orchestrator_id)
        if orch is None or not orch.enabled:
            raise HTTPException(status_code=503, detail="Bound orchestrator unavailable")

        token_orch_id = token_payload.get("orchestrator_id")
        if token_orch_id and str(orch.id) != token_orch_id:
            raise HTTPException(status_code=403, detail="Token not authorized for this application")

        orch_name = orch.name

    ctx_id = (
        uuid.UUID(context_id)
        if context_id and _is_valid_uuid(context_id)
        else uuid.uuid4()
    )

    room_name = f"{slug}-{ctx_id}"
    room_metadata = json.dumps({"slug": slug, "orchestrator": orch_name, "context_id": str(ctx_id)})

    from livekit.api import AccessToken, VideoGrants

    lk_token = (
        AccessToken(settings.livekit.api_key, settings.livekit.api_secret)
        .with_identity(f"user-{ctx_id}")
        .with_name(f"user-{ctx_id}")
        .with_grants(VideoGrants(room_join=True, room=room_name, can_publish=True, can_subscribe=True))
        .with_metadata(room_metadata)
        .to_jwt()
    )

    return {
        "token": lk_token,
        "url": settings.livekit.public_url,
        "room": room_name,
        "context_id": str(ctx_id),
    }
