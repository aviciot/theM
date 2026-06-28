"""
Audit Service
=============
Audit logging for security events.
"""

import logging
from typing import Optional, Dict
from datetime import datetime

from config.database import get_db_pool

logger = logging.getLogger(__name__)


async def audit_log(
    user_id: Optional[int],
    username: Optional[str],
    action: str,
    result: str,
    ip_address: Optional[str] = None,
    user_agent: Optional[str] = None,
    details: Optional[str] = None
) -> None:
    """
    Log authentication events for audit.

    Args:
        user_id: User ID (if known)
        username: Username (if known)
        action: Action performed (e.g., "login", "logout", "validate")
        result: Result of action ("success" or "failed")
        ip_address: Client IP address (optional)
        user_agent: User agent string (optional)
        details: Additional details as text (optional)
    """
    try:
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            await conn.execute("""
                INSERT INTO auth_service.auth_audit (user_id, username, action, result, ip_address, user_agent, details)
                VALUES ($1, $2, $3, $4, $5, $6, $7)
            """, user_id, username, action, result, ip_address, user_agent, details)
    except Exception as e:
        logger.error(f"Audit log error: {e}")


async def check_rate_limit(user_id: int, rate_limit: int) -> tuple:
    """
    Check if user is within rate limit.

    Args:
        user_id: User ID
        rate_limit: Maximum requests per hour

    Returns:
        Tuple of (allowed: bool, remaining: int)
    """
    try:
        pool = await get_db_pool()
        current_hour = datetime.utcnow().replace(minute=0, second=0, microsecond=0)

        async with pool.acquire() as conn:
            # Get or create rate limit record
            await conn.execute("""
                INSERT INTO rate_limits (user_id, window_start, request_count)
                VALUES ($1, $2, 1)
                ON CONFLICT (user_id, window_start)
                DO UPDATE SET
                    request_count = rate_limits.request_count + 1,
                    last_request_at = CURRENT_TIMESTAMP
            """, user_id, current_hour)

            # Get current count
            current_count = await conn.fetchval("""
                SELECT request_count FROM rate_limits
                WHERE user_id = $1 AND window_start = $2
            """, user_id, current_hour)

            allowed = current_count <= rate_limit
            remaining = max(0, rate_limit - current_count)

            return allowed, remaining

    except Exception as e:
        logger.error(f"Rate limit check error: {e}")
        return True, rate_limit  # Allow on error
