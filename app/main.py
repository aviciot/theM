"""
Odin — Multi-Agent Orchestration Platform
Entry point. Lifespan handles DB/Redis init and background tasks.
"""

from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.config import settings
from app.database import init_db, close_db
from app.utils.logger import logger, setup_logging
from app.routers import health
from app.routers import admin_llm_providers


@asynccontextmanager
async def lifespan(app: FastAPI):
    setup_logging()
    logger.info(
        "Odin starting",
        instance_id=settings.odin_instance_id,
        env=settings.app.environment,
        redis_db=settings.redis.db,
        db=settings.database.database,
    )

    await init_db()

    logger.info(
        "Odin ready",
        instance_id=settings.odin_instance_id,
        port=settings.app.port,
    )

    yield

    logger.info("Odin shutting down", instance_id=settings.odin_instance_id)
    await close_db()
    logger.info("Odin shutdown complete")


app = FastAPI(
    title="Odin",
    description="Multi-Agent Orchestration Platform",
    version="0.1.0",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.security.cors_origins,
    allow_credentials=True,
    allow_methods=["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
    allow_headers=["Authorization", "Content-Type", "X-Request-ID"],
)

app.include_router(health.router, tags=["health"])
app.include_router(admin_llm_providers.router)
