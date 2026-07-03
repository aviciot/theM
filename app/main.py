"""
Odin — Multi-Agent Orchestration Platform
Entry point. Lifespan handles DB/Redis init and background tasks.
"""

import asyncio
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.config import settings
from app.database import init_db, close_db
from app.utils.logger import logger, setup_logging
from app.routers import health
from app.routers import admin_llm_providers
from app.routers import admin_agents
from app.routers import admin_orchestrators
from app.routers import admin_tokens
from app.routers import ws_orchestrator
from app.routers import ws_dashboard
from app.routers import runs
from app.routers import transcription
from app.routers import tts
from app.services.agent_registry import start_change_listener


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

    # Background task: listen for agent registry invalidation signals
    listener_task = asyncio.create_task(start_change_listener())

    logger.info(
        "Odin ready",
        instance_id=settings.odin_instance_id,
        port=settings.app.port,
    )

    yield

    listener_task.cancel()
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
app.include_router(admin_llm_providers.router, prefix="/api/v1")
app.include_router(admin_agents.router, prefix="/api/v1")
app.include_router(admin_orchestrators.router, prefix="/api/v1")
app.include_router(admin_tokens.router, prefix="/api/v1")
app.include_router(ws_orchestrator.router)
app.include_router(ws_dashboard.router)
app.include_router(runs.router, prefix="/api/v1")
app.include_router(transcription.router, prefix="/api/v1")
app.include_router(tts.router, prefix="/api/v1")
