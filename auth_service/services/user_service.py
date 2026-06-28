"""
User Service
============
User management operations.
"""

import logging
from typing import Optional, List

from config.database import get_db_pool
from models.schemas import User
from utils.hashing import hash_api_key

logger = logging.getLogger(__name__)


async def get_user_by_api_key(api_key: str) -> Optional[User]:
    """
    Get user by API key from database.

    Args:
        api_key: Raw API key

    Returns:
        User object if found and active, None otherwise
    """
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            row = await conn.fetchrow("""
                SELECT u.id, u.email, u.name, u.email as username, r.name as role, u.active, u.created_at, u.updated_at
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE u.api_key_hash = $1 AND u.active = true
            """, hash_api_key(api_key))

            if row:
                return User(**dict(row))

        return None

    except Exception as e:
        logger.error(f"Error getting user by API key: {e}")
        return None


async def get_user_by_id(user_id: int) -> Optional[User]:
    """
    Get user by ID from database.

    Args:
        user_id: User ID

    Returns:
        User object if found and active, None otherwise
    """
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            row = await conn.fetchrow("""
                SELECT u.id, u.email, u.name, u.email as username, r.name as role, u.active, u.created_at, u.updated_at
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE u.id = $1 AND u.active = true
            """, user_id)

            if row:
                return User(**dict(row))

        return None

    except Exception as e:
        logger.error(f"Error getting user by ID: {e}")
        return None


async def get_user_permissions(user: User) -> List[str]:
    """
    Get user permissions from role - DEPRECATED, roles table has no permissions column.
    Returns empty list.
    """
    return []


def check_permission(permissions: List[str], resource: str, action: str = "read") -> bool:
    """
    Check if permissions allow resource:action.

    Args:
        permissions: List of permission strings
        resource: Resource name
        action: Action name (default: "read")

    Returns:
        True if permission granted, False otherwise
    """
    # Check for exact match
    if f"{resource}:{action}" in permissions:
        return True

    # Check for resource wildcard
    if f"{resource}:*" in permissions:
        return True

    # Check for global wildcard
    if "*" in permissions:
        return True

    # Check for global action
    if f"*:{action}" in permissions:
        return True

    return False
