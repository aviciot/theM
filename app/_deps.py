"""
FastAPI dependency injectors for auth.

Two auth paths:
  - require_jwt()      → validates JWT via auth-service (dashboard / admin REST)
  - require_bearer()   → validates opaque bearer token via token_cache (WS orchestrator)
"""

from typing import Optional

from fastapi import Depends, HTTPException, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.services import auth_client
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

_bearer_scheme = HTTPBearer(auto_error=False)


# ------------------------------------------------------------------ #
# JWT — for admin REST endpoints                                       #
# ------------------------------------------------------------------ #

async def require_jwt(
    credentials: Optional[HTTPAuthorizationCredentials] = Depends(_bearer_scheme),
) -> dict:
    """Validate JWT issued by odin-auth-service. Returns user payload."""
    if credentials is None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Authorization header required",
            headers={"WWW-Authenticate": "Bearer"},
        )
    payload = await auth_client.validate_jwt(credentials.credentials)
    if payload is None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Invalid or expired token",
            headers={"WWW-Authenticate": "Bearer"},
        )
    return payload


async def require_admin(user: dict = Depends(require_jwt)) -> dict:
    """Require JWT + admin role."""
    role = user.get("role", "")
    if role not in ("admin", "superadmin"):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Admin role required",
        )
    return user


# ------------------------------------------------------------------ #
# Bearer token — for WS orchestrator endpoint                         #
# ------------------------------------------------------------------ #

async def require_bearer(
    credentials: Optional[HTTPAuthorizationCredentials] = Depends(_bearer_scheme),
    db: AsyncSession = Depends(get_db),
) -> dict:
    """
    Validate opaque bearer token (odin.access_tokens).
    L1 cache → L2 Redis → DB.
    Returns token payload: {token_id, user_id, label, orchestrator_id, enabled}
    """
    if credentials is None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Authorization header required",
            headers={"WWW-Authenticate": "Bearer"},
        )
    payload = await validate_bearer_token(credentials.credentials, db)
    if payload is None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Invalid, disabled, or expired token",
            headers={"WWW-Authenticate": "Bearer"},
        )
    return payload
