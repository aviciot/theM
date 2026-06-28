"""
Database Connection Management
===============================
Manages PostgreSQL connection pool.
"""

import asyncpg
import logging
from typing import Optional

from .settings import settings

logger = logging.getLogger(__name__)

# Global connection pool
_db_pool: Optional[asyncpg.Pool] = None


async def get_db_pool() -> asyncpg.Pool:
    """
    Get or create database connection pool.

    Returns:
        asyncpg.Pool: Database connection pool
    """
    global _db_pool

    if _db_pool is None:
        try:
            _db_pool = await asyncpg.create_pool(
                settings.DATABASE_URL,
                min_size=settings.DB_POOL_MIN_SIZE,
                max_size=settings.DB_POOL_MAX_SIZE
            )
            logger.info(f"Database pool created: {settings.DB_POOL_MIN_SIZE}-{settings.DB_POOL_MAX_SIZE} connections")
        except Exception as e:
            logger.error(f"Failed to create database pool: {e}")
            raise

    return _db_pool


async def close_db_pool() -> None:
    """Close database connection pool."""
    global _db_pool

    if _db_pool:
        await _db_pool.close()
        _db_pool = None
        logger.info("Database pool closed")
