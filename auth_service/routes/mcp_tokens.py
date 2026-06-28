"""
MCP Access Tokens Routes
=========================
Generate and manage opaque tokens for MCP Gateway access.
"""

import logging
import secrets
import hmac
import hashlib
from datetime import datetime, timedelta
from fastapi import APIRouter, HTTPException, Depends, Request
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from pydantic import BaseModel

from models.schemas import User
from services.token_service import verify_token
from services.user_service import get_user_by_id
from services.audit_service import audit_log
from config.database import get_db_pool
from config.settings import settings

router = APIRouter()
security = HTTPBearer()
logger = logging.getLogger(__name__)


def _token_hmac(token: str) -> str:
    """HMAC-SHA256 of token for fast indexed lookup. Uses 'mcp:' prefix for domain separation."""
    return hmac.new(settings.JWT_SECRET.encode(), f"mcp:{token}".encode(), hashlib.sha256).hexdigest()


class GenerateMCPTokenRequest(BaseModel):
    name: str
    expires_days: int = 90


async def get_current_user(credentials: HTTPAuthorizationCredentials = Depends(security)) -> User:
    """Get current authenticated user from JWT token."""
    payload = await verify_token(credentials.credentials)
    user_id = int(payload.get("sub"))
    user = await get_user_by_id(user_id)
    if not user:
        raise HTTPException(401, "User not found")
    return user


async def get_user_from_header(request: Request, credentials: HTTPAuthorizationCredentials = Depends(security)) -> User:
    """Get user from X-User-Id header or JWT token."""
    user_id_header = request.headers.get("X-User-Id")
    if user_id_header:
        user = await get_user_by_id(int(user_id_header))
        if not user:
            raise HTTPException(401, "User not found")
        return user
    return await get_current_user(credentials)


@router.post("/mcp/tokens/generate")
async def generate_mcp_token(
    request: Request,
    req: GenerateMCPTokenRequest,
    credentials: HTTPAuthorizationCredentials = Depends(security)
):
    """Generate MCP access token for user."""
    user = await get_user_from_header(request, credentials)
    
    random_bytes = secrets.token_urlsafe(32)
    token = f"omni2_mcp_{random_bytes}"
    token_hash = _token_hmac(token)
    
    expires_at = datetime.utcnow() + timedelta(days=req.expires_days) if req.expires_days > 0 else None
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        token_id = await conn.fetchval("""
            INSERT INTO auth_service.mcp_access_tokens (user_id, token_hash, name, expires_at)
            VALUES ($1, $2, $3, $4)
            RETURNING id
        """, user.id, token_hash, req.name, expires_at)
    
    await audit_log(user.id, user.username, "mcp_token_generated", "success", details=f"name={req.name}, token_id={token_id}")
    
    logger.info(f"[MCP-TOKEN] Generated for user_id={user.id}, name={req.name}")
    
    return {
        "token": token,
        "token_id": token_id,
        "name": req.name,
        "expires_at": expires_at.isoformat() if expires_at else None
    }


@router.get("/mcp/tokens")
async def list_mcp_tokens(
    request: Request,
    credentials: HTTPAuthorizationCredentials = Depends(security)
):
    """List all MCP tokens for user."""
    user = await get_user_from_header(request, credentials)
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        rows = await conn.fetch("""
            SELECT id, name, created_at, last_used_at, expires_at
            FROM auth_service.mcp_access_tokens
            WHERE user_id = $1
            ORDER BY created_at DESC
        """, user.id)
    
    return {
        "tokens": [{
            "id": row["id"],
            "name": row["name"],
            "created_at": row["created_at"].isoformat(),
            "last_used_at": row["last_used_at"].isoformat() if row["last_used_at"] else None,
            "expires_at": row["expires_at"].isoformat() if row["expires_at"] else None
        } for row in rows]
    }


@router.delete("/mcp/tokens/{token_id}")
async def revoke_mcp_token(
    token_id: int,
    request: Request,
    credentials: HTTPAuthorizationCredentials = Depends(security)
):
    """Revoke MCP token."""
    user = await get_user_from_header(request, credentials)
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        row = await conn.fetchrow("""
            SELECT name FROM auth_service.mcp_access_tokens 
            WHERE id = $1 AND user_id = $2
        """, token_id, user.id)
        
        if not row:
            raise HTTPException(404, "Token not found")
        
        await conn.execute("""
            DELETE FROM auth_service.mcp_access_tokens WHERE id = $1
        """, token_id)
    
    await audit_log(user.id, user.username, "mcp_token_revoked", "success", details=f"token_id={token_id}, name={row['name']}")
    
    return {"message": "Token revoked successfully"}


class ValidateMCPTokenRequest(BaseModel):
    token: str


@router.post("/mcp/tokens/validate")
async def validate_mcp_token(req: ValidateMCPTokenRequest):
    """Validate MCP token and return user context."""
    token = req.token
    if not token or not token.startswith("omni2_mcp_"):
        raise HTTPException(401, "Invalid token format")
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        row = await conn.fetchrow("""
            SELECT t.id, t.user_id, t.expires_at,
                   u.username, u.email, u.active as user_active,
                   r.name as role, r.mcp_access, r.tool_restrictions, r.omni_services
            FROM auth_service.mcp_access_tokens t
            JOIN auth_service.users u ON t.user_id = u.id
            JOIN auth_service.roles r ON u.role_id = r.id
            WHERE t.token_hash = $1
        """, _token_hmac(token))

        if not row:
            raise HTTPException(401, "Invalid token")

        if row["expires_at"] and row["expires_at"] < datetime.utcnow():
            raise HTTPException(401, "Token expired")

        if not row["user_active"]:
            raise HTTPException(401, "User inactive")

        await conn.execute("""
            UPDATE auth_service.mcp_access_tokens
            SET last_used_at = CURRENT_TIMESTAMP
            WHERE id = $1
        """, row["id"])

        return {
            "user_id": row["user_id"],
            "username": row["username"],
            "email": row["email"],
            "role": row["role"],
            "mcp_access": row["mcp_access"] or [],
            "tool_restrictions": row["tool_restrictions"] or {},
            "omni_services": row["omni_services"] or []
        }
