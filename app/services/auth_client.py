"""
HTTP client for odin-auth-service (port 8701).
Adapted from Omni's auth_client.py.
"""

import httpx
from typing import Optional, Dict, Any

from app.config import settings
from app.utils.logger import logger

_client: Optional[httpx.AsyncClient] = None


def _get_client() -> httpx.AsyncClient:
    global _client
    if _client is None or _client.is_closed:
        _client = httpx.AsyncClient(
            base_url=settings.auth_service_url,
            timeout=10.0,
        )
    return _client


async def validate_token(token: str) -> Optional[Dict[str, Any]]:
    try:
        resp = await _get_client().post(
            "/api/v1/mcp/tokens/validate",
            json={"token": token},
        )
        if resp.status_code == 200:
            return resp.json()
        return None
    except Exception as e:
        logger.error("auth_client.validate_token failed", error=str(e))
        return None


async def get_user(user_id: int) -> Optional[Dict[str, Any]]:
    try:
        resp = await _get_client().get(f"/api/v1/users/{user_id}")
        if resp.status_code == 200:
            return resp.json()
        return None
    except Exception as e:
        logger.error("auth_client.get_user failed", error=str(e))
        return None


async def validate_jwt(token: str) -> Optional[Dict[str, Any]]:
    try:
        resp = await _get_client().post(
            "/api/v1/auth/verify",
            headers={"Authorization": f"Bearer {token}"},
        )
        if resp.status_code == 200:
            return resp.json()
        return None
    except Exception as e:
        logger.error("auth_client.validate_jwt failed", error=str(e))
        return None


async def close():
    global _client
    if _client and not _client.is_closed:
        await _client.aclose()
