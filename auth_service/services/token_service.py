"""
Token Service
=============
JWT token operations (create, verify, revoke).
"""

import logging
from datetime import datetime, timedelta
from typing import Dict, Any

import jwt
from fastapi import HTTPException

from config.settings import settings
from config.database import get_db_pool
from models.schemas import User
from services.user_service import get_user_permissions
from utils.hashing import hash_token

logger = logging.getLogger(__name__)


async def create_access_token(user: User) -> str:
    """
    Create JWT access token.

    Args:
        user: User object

    Returns:
        JWT access token string
    """
    permissions = await get_user_permissions(user)

    # Get role info for token expiry
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        role_row = await conn.fetchrow("""
            SELECT token_expiry FROM auth_service.roles WHERE name = $1
        """, user.role)

    token_expiry = role_row['token_expiry'] if role_row else settings.ACCESS_TOKEN_EXPIRY

    payload = {
        "sub": str(user.id),
        "username": user.username,
        "name": user.name,
        "role": user.role,
        "permissions": permissions,
        "exp": datetime.utcnow() + timedelta(seconds=token_expiry),
        "iat": datetime.utcnow(),
        "type": "access"
    }

    token = jwt.encode(payload, settings.JWT_SECRET, algorithm=settings.JWT_ALGORITHM)

    # Store session in database
    try:
        async with pool.acquire() as conn:
            await conn.execute("""
                INSERT INTO auth_service.user_sessions (user_id, token_hash, expires_at)
                VALUES ($1, $2, $3)
            """, user.id, hash_token(token), datetime.utcnow() + timedelta(seconds=token_expiry))
    except Exception as e:
        logger.warning(f"Failed to store session: {e}")

    return token


async def create_refresh_token(user: User) -> str:
    """
    Create JWT refresh token.

    Args:
        user: User object

    Returns:
        JWT refresh token string
    """
    payload = {
        "sub": str(user.id),
        "exp": datetime.utcnow() + timedelta(seconds=settings.REFRESH_TOKEN_EXPIRY),
        "iat": datetime.utcnow(),
        "type": "refresh"
    }

    token = jwt.encode(payload, settings.JWT_SECRET, algorithm=settings.JWT_ALGORITHM)

    # Update session with refresh token
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            await conn.execute("""
                UPDATE auth_service.user_sessions
                SET refresh_token_hash = $1
                WHERE user_id = $2 AND expires_at > $3
                AND id = (SELECT id FROM auth_service.user_sessions WHERE user_id = $2 AND expires_at > $3 ORDER BY created_at DESC LIMIT 1)
            """, hash_token(token), user.id, datetime.utcnow())
    except Exception as e:
        logger.warning(f"Failed to update refresh token: {e}")

    return token


async def verify_token(token: str) -> Dict[str, Any]:
    """
    Verify JWT token.

    Args:
        token: JWT token string

    Returns:
        Token payload dict

    Raises:
        HTTPException: If token is invalid, expired, or revoked
    """
    try:
        # Check if token is blacklisted
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            blacklisted = await conn.fetchval("""
                SELECT EXISTS(SELECT 1 FROM auth_service.blacklisted_tokens
                WHERE token_hash = $1 AND expires_at > $2)
            """, hash_token(token), datetime.utcnow())

            if blacklisted:
                logger.debug("Token has been revoked")
                raise HTTPException(401, "Token has been revoked")

        # Decode token
        payload = jwt.decode(token, settings.JWT_SECRET, algorithms=[settings.JWT_ALGORITHM])
        return payload

    except jwt.ExpiredSignatureError:
        logger.debug("Token has expired")
        raise HTTPException(401, "Token has expired")
    except jwt.InvalidTokenError as e:
        logger.debug(f"Invalid token: {e}")
        raise HTTPException(401, "Invalid token")
    except HTTPException:
        # Re-raise HTTP exceptions
        raise
    except Exception as e:
        logger.error(f"Token verification error: {e}")
        raise HTTPException(401, "Token verification failed")


async def revoke_token(token: str) -> None:
    """
    Revoke (blacklist) a token.

    Args:
        token: JWT token string to revoke
    """
    try:
        # Decode to get expiry
        payload = jwt.decode(
            token,
            settings.JWT_SECRET,
            algorithms=[settings.JWT_ALGORITHM],
            options={"verify_exp": False}
        )
        expires_at = datetime.fromtimestamp(payload['exp'])

        # Add to blacklist
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            await conn.execute("""
                INSERT INTO auth_service.blacklisted_tokens (token_hash, expires_at)
                VALUES ($1, $2)
                ON CONFLICT DO NOTHING
            """, hash_token(token), expires_at)

        logger.info(f"Token revoked for user {payload.get('sub')}")

    except Exception as e:
        logger.error(f"Failed to revoke token: {e}")
        raise
