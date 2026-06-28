"""
Password Service
================
Password hashing and verification using bcrypt.
"""

import bcrypt
import logging
from typing import Optional

from config.database import get_db_pool
from models.schemas import User

logger = logging.getLogger(__name__)


def hash_password(password: str) -> str:
    """
    Hash password using bcrypt.

    Args:
        password: Raw password

    Returns:
        Hashed password (bcrypt hash)
    """
    salt = bcrypt.gensalt()
    password_hash = bcrypt.hashpw(password.encode('utf-8'), salt)
    return password_hash.decode('utf-8')


def verify_password(password: str, password_hash: str) -> bool:
    """
    Verify password against bcrypt hash.

    Args:
        password: Raw password
        password_hash: Stored bcrypt password hash

    Returns:
        True if password matches, False otherwise
    """
    try:
        return bcrypt.checkpw(password.encode('utf-8'), password_hash.encode('utf-8'))
    except Exception as e:
        logger.error(f"Password verification error: {e}")
        return False


async def get_user_by_email(email: str) -> Optional[User]:
    """
    Get user by email from database.

    Args:
        email: User email

    Returns:
        User object if found and active, None otherwise
    """
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            row = await conn.fetchrow("""
                SELECT u.id, u.username, u.name, u.email, r.name as role, u.active, u.created_at, u.updated_at
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE u.email = $1 AND u.active = true
            """, email)

            if row:
                return User(**dict(row))

        return None

    except Exception as e:
        logger.error(f"Error getting user by email: {e}")
        return None


async def get_password_hash(user_id: int) -> Optional[str]:
    """
    Get password hash for user.

    Args:
        user_id: User ID

    Returns:
        Password hash if exists, None otherwise
    """
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            password_hash = await conn.fetchval("""
                SELECT password_hash FROM auth_service.users WHERE id = $1
            """, user_id)

        return password_hash

    except Exception as e:
        logger.error(f"Error getting password hash: {e}")
        return None


async def authenticate_with_password(username_or_email: str, password: str) -> Optional[User]:
    """
    Authenticate user with username/email and password.

    Args:
        username_or_email: Username OR email (supports both)
        password: Raw password

    Returns:
        User object if authentication successful, None otherwise
    """
    # Try to get user by email first
    user = await get_user_by_email(username_or_email)

    # If not found by email, try by username
    if not user:
        try:
            pool = await get_db_pool()
            async with pool.acquire() as conn:
                row = await conn.fetchrow("""
                    SELECT u.id, u.username, u.name, u.email, r.name as role, u.active, u.created_at, u.updated_at
                    FROM auth_service.users u
                    JOIN auth_service.roles r ON u.role_id = r.id
                    WHERE u.username = $1 AND u.active = true
                """, username_or_email)

                if row:
                    user = User(**dict(row))
        except Exception as e:
            logger.error(f"Error getting user by username: {e}")
            return None

    if not user:
        return None

    password_hash = await get_password_hash(user.id)
    if not password_hash:
        return None

    if verify_password(password, password_hash):
        return user

    return None
