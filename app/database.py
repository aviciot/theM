"""
Database and Redis connection management for Odin.

Redis DB index 1 (Omni uses 0). Schema: odin.
"""

from typing import AsyncGenerator, Optional

import redis.asyncio as aioredis
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)
from sqlalchemy.orm import DeclarativeBase

from app.config import settings
from app.utils.logger import logger


class Base(DeclarativeBase):
    pass


engine: Optional[AsyncEngine] = None
AsyncSessionLocal: Optional[async_sessionmaker[AsyncSession]] = None
redis_client: Optional[aioredis.Redis] = None


async def init_db() -> None:
    global engine, AsyncSessionLocal, redis_client

    logger.info(
        "Initializing Odin database",
        host=settings.database.host,
        database=settings.database.database,
        redis_db=settings.redis.db,
        instance=settings.odin_instance_id,
    )

    engine = create_async_engine(
        settings.database.url,
        echo=settings.database.echo,
        pool_size=settings.database.pool_size,
        max_overflow=settings.database.max_overflow,
        pool_pre_ping=True,
        pool_recycle=3600,
        connect_args={"ssl": False, "prepared_statement_cache_size": 0},
    )

    AsyncSessionLocal = async_sessionmaker(
        engine,
        class_=AsyncSession,
        expire_on_commit=False,
        autocommit=False,
        autoflush=False,
    )

    async with engine.begin() as conn:
        await conn.execute(text("SELECT 1"))
    logger.info("Database connection OK")

    if settings.redis.enabled:
        redis_client = aioredis.Redis(
            host=settings.redis.host,
            port=settings.redis.port,
            password=settings.redis.password or None,
            db=settings.redis.db,   # always 1
            decode_responses=True,
        )
        await redis_client.ping()
        logger.info("Redis connection OK", db=settings.redis.db)
    else:
        logger.warning("Redis disabled — multi-replica features will not work")


async def close_db() -> None:
    global engine, redis_client
    if engine:
        await engine.dispose()
        logger.info("Database connections closed")
    if redis_client:
        await redis_client.aclose()
        logger.info("Redis connection closed")


async def get_db() -> AsyncGenerator[AsyncSession, None]:
    if AsyncSessionLocal is None:
        raise RuntimeError("Database not initialized")
    async with AsyncSessionLocal() as session:
        try:
            yield session
            await session.commit()
        except Exception:
            await session.rollback()
            raise
        finally:
            await session.close()


async def get_redis() -> aioredis.Redis:
    if redis_client is None:
        raise RuntimeError("Redis not initialized")
    return redis_client
