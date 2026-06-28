"""
Health Check Routes
===================
Service health and status endpoints.
"""

from datetime import datetime
from fastapi import APIRouter

from config.database import get_db_pool
from models.schemas import HealthResponse

router = APIRouter()


@router.get("/health", response_model=HealthResponse)
async def health_check():
    """Health check endpoint."""
    try:
        # Check database connection
        pool = await get_db_pool()
        async with pool.acquire() as conn:
            await conn.fetchval("SELECT 1")
            active_users = await conn.fetchval("SELECT COUNT(*) FROM users WHERE active = true")

        return HealthResponse(
            status="healthy",
            service="mcp-auth-service",
            version="1.0.0",
            timestamp=datetime.utcnow().isoformat(),
            database="connected",
            active_users=active_users
        )
    except Exception as e:
        return HealthResponse(
            status="unhealthy",
            service="mcp-auth-service",
            version="1.0.0",
            timestamp=datetime.utcnow().isoformat(),
            database=f"error: {str(e)}"
        )


@router.get("/info")
async def service_info():
    """Service information endpoint."""
    return {
        "service": "MCP Authentication Service",
        "version": "1.0.0",
        "description": "Role-based authentication for MCP Gateway",
        "endpoints": {
            "health": "/health",
            "login": "/login",
            "validate": "/validate",
            "refresh": "/refresh",
            "logout": "/logout",
            "users": "/users",
            "roles": "/roles"
        }
    }
