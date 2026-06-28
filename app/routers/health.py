"""Health check endpoints."""

from fastapi import APIRouter
from sqlalchemy import text

from app.config import settings
from app.database import engine, redis_client

router = APIRouter()


@router.get("/health")
async def health():
    db_status, redis_status = "ok", "ok"

    try:
        async with engine.begin() as conn:
            await conn.execute(text("SELECT 1"))
    except Exception:
        db_status = "error"

    try:
        await redis_client.ping()
    except Exception:
        redis_status = "error"

    overall = "healthy" if db_status == "ok" and redis_status == "ok" else "degraded"
    return {
        "status": overall,
        "db": db_status,
        "redis": redis_status,
        "redis_db": settings.redis.db,
        "instance_id": settings.odin_instance_id,
    }


@router.get("/health/ready")
async def health_ready():
    try:
        async with engine.begin() as conn:
            await conn.execute(text("SELECT 1"))
        return {"status": "ready"}
    except Exception as e:
        from fastapi import Response
        return Response(content=f"db error: {e}", status_code=503)


@router.get("/health/live")
async def health_live():
    return {"status": "alive", "instance_id": settings.odin_instance_id}
