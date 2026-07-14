"""
the-M — Multi-Agent Orchestration Platform
Entry point. Lifespan handles DB/Redis init and background tasks.
"""

import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timezone

from fastapi import Depends, FastAPI
from fastapi.middleware.cors import CORSMiddleware

from sqlalchemy import select

from app._deps import require_admin
from app.config import settings
import app.database as db_module
from app.database import init_db, close_db
from app.utils.logger import logger, setup_logging
from app.routers import health
from app.routers import admin_llm_providers
from app.routers import admin_agents
from app.routers import admin_orchestrators
from app.routers import admin_tokens
from app.routers import admin_applications
from app.routers import ws_orchestrator
from app.routers import ws_dashboard
from app.routers import runs
from app.routers import transcription
from app.routers import tts
from app.routers import a2a_server
from app.routers import apps as apps_router
from app.routers import webrtc as webrtc_router
from app.services.agent_registry import start_change_listener
from app.services import task_store
from app.services.dashboard_broadcaster import publish_app_status
from app.models import Task, Application, EntryPoint


async def _app_liveness_loop() -> None:
    """Background coroutine: probe all enabled apps every 30s, publish to them:dash:apps."""
    import time
    import httpx
    _INTERVAL = 30
    _TIMEOUT  = 5.0

    try:
        while True:
            await asyncio.sleep(_INTERVAL)
            if db_module.AsyncSessionLocal is None:
                continue
            try:
                async with db_module.AsyncSessionLocal() as db:
                    result = await db.execute(
                        select(EntryPoint)
                        .join(Application, EntryPoint.application_id == Application.id)
                        .where(EntryPoint.enabled == True, Application.enabled == True)
                    )
                    eps = list(result.scalars().all())

                statuses: dict[str, dict] = {}
                async with httpx.AsyncClient(timeout=_TIMEOUT) as client:
                    async def _probe(slug: str, base_url: str) -> None:
                        t0 = time.monotonic()
                        try:
                            r = await client.get(f"{base_url}/apps/{slug}")
                            ok = r.status_code < 500
                        except Exception:
                            ok = False
                        latency_ms = int((time.monotonic() - t0) * 1000)
                        statuses[slug] = {"reachable": ok, "latency_ms": latency_ms if ok else None}

                    bridge_url = settings.bridge_url or "http://localhost:8001"
                    await asyncio.gather(*[_probe(ep.slug, bridge_url) for ep in eps])

                if statuses:
                    await publish_app_status(statuses)
                    logger.debug("app_liveness: probed apps", count=len(statuses))
            except Exception as exc:
                logger.error("app_liveness: iteration error", error=str(exc))
    except asyncio.CancelledError:
        pass


async def _reaper_loop() -> None:
    """Background coroutine: expire tasks that have passed their deadline."""
    try:
        while True:
            await asyncio.sleep(60)
            if db_module.AsyncSessionLocal is None:
                continue
            try:
                async with db_module.AsyncSessionLocal() as db:
                    now = datetime.now(timezone.utc)
                    result = await db.execute(
                        select(Task).where(
                            Task.state.in_(["submitted", "working"]),
                            Task.deadline.isnot(None),
                            Task.deadline < now,
                        )
                    )
                    expired = list(result.scalars().all())
                    for task in expired:
                        await task_store.transition(
                            db, task.id, "failed", error="deadline exceeded"
                        )
                    if expired:
                        logger.info("reaper: expired tasks", count=len(expired))
            except Exception as exc:
                logger.error("reaper: iteration error", error=str(exc))
    except asyncio.CancelledError:
        pass


@asynccontextmanager
async def lifespan(app: FastAPI):
    setup_logging()
    logger.info(
        "the-M starting",
        instance_id=settings.instance_id,
        env=settings.app.environment,
        redis_db=settings.redis.db,
        db=settings.database.database,
    )

    await init_db()

    # Background task: listen for agent registry invalidation signals
    listener_task = asyncio.create_task(start_change_listener())
    # Background task: reap tasks that have exceeded their deadline
    reaper_task = asyncio.create_task(_reaper_loop())
    # Background task: probe app liveness and broadcast to them:dash:apps
    liveness_task = asyncio.create_task(_app_liveness_loop())

    logger.info(
        "the-M ready",
        instance_id=settings.instance_id,
        port=settings.app.port,
    )

    yield

    listener_task.cancel()
    reaper_task.cancel()
    liveness_task.cancel()
    logger.info("the-M shutting down", instance_id=settings.instance_id)
    await close_db()
    logger.info("the-M shutdown complete")


app = FastAPI(
    title="the-M",
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
app.include_router(admin_llm_providers.router, prefix="/api/v1", dependencies=[Depends(require_admin)])
app.include_router(admin_agents.router, prefix="/api/v1", dependencies=[Depends(require_admin)])
app.include_router(admin_orchestrators.router, prefix="/api/v1", dependencies=[Depends(require_admin)])
app.include_router(admin_tokens.router, prefix="/api/v1", dependencies=[Depends(require_admin)])
app.include_router(admin_applications.router, prefix="/api/v1", dependencies=[Depends(require_admin)])
app.include_router(ws_orchestrator.router)
app.include_router(ws_dashboard.router)
app.include_router(runs.router, prefix="/api/v1")
app.include_router(transcription.router, prefix="/api/v1")
app.include_router(tts.router, prefix="/api/v1")
app.include_router(a2a_server.router)
app.include_router(apps_router.router)
app.include_router(webrtc_router.router)
