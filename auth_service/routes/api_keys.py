"""
API Keys Routes
===============
Manage API keys for MCP Hub authentication with multi-team/user support.
"""

import logging
import secrets
import bcrypt
from datetime import datetime, timedelta
from fastapi import APIRouter, HTTPException, Depends
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from typing import List, Optional
from pydantic import BaseModel

from models.schemas import User
from services.token_service import verify_token
from services.user_service import get_user_by_id
from services.audit_service import audit_log
from config.database import get_db_pool

router = APIRouter()
security = HTTPBearer()
logger = logging.getLogger(__name__)


class CreateApiKeyRequest(BaseModel):
    name: str
    team_ids: Optional[List[int]] = []
    user_ids: Optional[List[int]] = []
    expires_days: Optional[int] = None


async def get_current_user(credentials: HTTPAuthorizationCredentials = Depends(security)) -> User:
    """Get current authenticated user from JWT token."""
    payload = await verify_token(credentials.credentials)
    user_id = int(payload.get("sub"))
    user = await get_user_by_id(user_id)
    if not user:
        raise HTTPException(401, "User not found")
    return user


@router.post("/api-keys")
async def create_api_key(
    request: CreateApiKeyRequest,
    current_user: User = Depends(get_current_user)
):
    """Create API key assigned to multiple teams/users."""
    if not request.team_ids and not request.user_ids:
        raise HTTPException(400, "Must assign to at least one team or user")
    
    random_bytes = secrets.token_urlsafe(32)
    api_key = f"omni_{random_bytes}"
    key_prefix = api_key[:12]
    key_hash = bcrypt.hashpw(api_key.encode(), bcrypt.gensalt()).decode()
    
    expires_at = None
    if request.expires_days:
        expires_at = datetime.utcnow() + timedelta(days=request.expires_days)
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        async with conn.transaction():
            key_id = await conn.fetchval("""
                INSERT INTO auth_service.api_keys (
                    key_hash, key_prefix, name, expires_at, created_by_user_id
                ) VALUES ($1, $2, $3, $4, $5)
                RETURNING id
            """, key_hash, key_prefix, request.name, expires_at, current_user.id)
            
            for team_id in request.team_ids:
                await conn.execute("""
                    INSERT INTO auth_service.api_key_teams (api_key_id, team_id)
                    VALUES ($1, $2)
                """, key_id, team_id)
            
            for user_id in request.user_ids:
                await conn.execute("""
                    INSERT INTO auth_service.api_key_users (api_key_id, user_id)
                    VALUES ($1, $2)
                """, key_id, user_id)
    
    await audit_log(
        current_user.id,
        current_user.username,
        "api_key_created",
        "success",
        details=f"name={request.name}, key_prefix={key_prefix}, teams={request.team_ids}, users={request.user_ids}"
    )
    
    logger.info(f"[API-KEY] Created by user_id={current_user.id}, name={request.name}, prefix={key_prefix}")
    
    return {
        "api_key": api_key,
        "key_prefix": key_prefix,
        "name": request.name,
        "team_ids": request.team_ids,
        "user_ids": request.user_ids,
        "created_at": datetime.utcnow().isoformat(),
        "expires_at": expires_at.isoformat() if expires_at else None
    }


@router.get("/api-keys")
async def list_api_keys(current_user: User = Depends(get_current_user)):
    """List all API keys with assigned teams/users."""
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        rows = await conn.fetch("""
            SELECT k.id, k.key_prefix, k.name, k.created_at, k.last_used_at, 
                   k.expires_at, k.is_active, creator.username as created_by_name
            FROM auth_service.api_keys k
            LEFT JOIN auth_service.users creator ON k.created_by_user_id = creator.id
            ORDER BY k.created_at DESC
        """)
        
        result = []
        for row in rows:
            teams = await conn.fetch("""
                SELECT t.id, t.name
                FROM auth_service.api_key_teams akt
                JOIN auth_service.teams t ON akt.team_id = t.id
                WHERE akt.api_key_id = $1
            """, row["id"])
            
            users = await conn.fetch("""
                SELECT u.id, u.username, u.email
                FROM auth_service.api_key_users aku
                JOIN auth_service.users u ON aku.user_id = u.id
                WHERE aku.api_key_id = $1
            """, row["id"])
            
            result.append({
                "id": row["id"],
                "key_prefix": row["key_prefix"],
                "name": row["name"],
                "created_at": row["created_at"].isoformat(),
                "last_used_at": row["last_used_at"].isoformat() if row["last_used_at"] else None,
                "expires_at": row["expires_at"].isoformat() if row["expires_at"] else None,
                "is_active": row["is_active"],
                "is_expired": row["expires_at"] and row["expires_at"] < datetime.utcnow() if row["expires_at"] else False,
                "created_by_name": row["created_by_name"],
                "teams": [{"id": t["id"], "name": t["name"]} for t in teams],
                "users": [{"id": u["id"], "username": u["username"], "email": u["email"]} for u in users]
            })
        
        return result


@router.delete("/api-keys/{key_id}")
async def revoke_api_key(key_id: int, current_user: User = Depends(get_current_user)):
    """Revoke API key."""
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        row = await conn.fetchrow("""
            SELECT key_prefix, name FROM auth_service.api_keys WHERE id = $1
        """, key_id)
        
        if not row:
            raise HTTPException(404, "API key not found")
        
        await conn.execute("""
            UPDATE auth_service.api_keys SET is_active = false WHERE id = $1
        """, key_id)
    
    await audit_log(
        current_user.id,
        current_user.username,
        "api_key_revoked",
        "success",
        details=f"key_id={key_id}, key_prefix={row['key_prefix']}, name={row['name']}"
    )
    
    logger.info(f"[API-KEY] Revoked key_id={key_id} by user_id={current_user.id}")
    
    return {"message": "API key revoked successfully"}


@router.post("/api-keys/validate")
async def validate_api_key(api_key: str):
    """Validate API key and return assigned teams/users."""
    if not api_key or not api_key.startswith("omni_"):
        raise HTTPException(401, "Invalid API key format")
    
    key_prefix = api_key[:12]
    
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        row = await conn.fetchrow("""
            SELECT k.id, k.key_hash, k.expires_at, k.is_active
            FROM auth_service.api_keys k
            WHERE k.key_prefix = $1 AND k.is_active = true
        """, key_prefix)
        
        if not row:
            raise HTTPException(401, "Invalid API key")
        
        if not bcrypt.checkpw(api_key.encode(), row["key_hash"].encode()):
            raise HTTPException(401, "Invalid API key")
        
        if row["expires_at"] and row["expires_at"] < datetime.utcnow():
            raise HTTPException(401, "API key expired")
        
        teams = await conn.fetch("""
            SELECT t.id, t.name
            FROM auth_service.api_key_teams akt
            JOIN auth_service.teams t ON akt.team_id = t.id
            WHERE akt.api_key_id = $1
        """, row["id"])
        
        users = await conn.fetch("""
            SELECT u.id, u.username, u.email, u.role_id, r.name as role_name
            FROM auth_service.api_key_users aku
            JOIN auth_service.users u ON aku.user_id = u.id
            JOIN auth_service.roles r ON u.role_id = r.id
            WHERE aku.api_key_id = $1 AND u.active = true
        """, row["id"])
        
        await conn.execute("""
            UPDATE auth_service.api_keys
            SET last_used_at = CURRENT_TIMESTAMP
            WHERE id = $1
        """, row["id"])
    
    logger.info(f"[API-KEY] Validated prefix={key_prefix}")
    
    return {
        "teams": [{"id": t["id"], "name": t["name"]} for t in teams],
        "users": [{"id": u["id"], "username": u["username"], "email": u["email"], "role_id": u["role_id"], "role_name": u["role_name"]} for u in users]
    }
