"""
MCP Gateway Authentication Service
===================================
Main application entry point.
"""

import logging
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
import uvicorn

from config.settings import settings
from config.database import get_db_pool, close_db_pool
from routes import health, auth, users, roles, teams, permissions, api_keys, mcp_tokens

# Configure logging
logging.basicConfig(
    level=getattr(logging, settings.LOG_LEVEL),
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)

# Create FastAPI app
app = FastAPI(
    title=settings.APP_TITLE,
    description=settings.APP_DESCRIPTION,
    version=settings.APP_VERSION
)

# Add CORS middleware — restrict to configured origins only
app.add_middleware(
    CORSMiddleware,
    allow_origins=[o.strip() for o in settings.CORS_ORIGINS.split(",") if o.strip()],
    allow_credentials=True,
    allow_methods=["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
    allow_headers=["Authorization", "Content-Type", "X-Request-ID"],
)

# Register routes
app.include_router(health.router, tags=["Health"])
app.include_router(auth.router, prefix="/api/v1/auth", tags=["Authentication"])
app.include_router(users.router, prefix="/api/v1/users", tags=["Users"])
app.include_router(roles.router, prefix="/api/v1", tags=["Roles"])
app.include_router(teams.router, prefix="/api/v1", tags=["Teams"])
app.include_router(permissions.router, prefix="/api/v1", tags=["Permissions"])
app.include_router(api_keys.router, prefix="/api/v1", tags=["API Keys"])
app.include_router(mcp_tokens.router, prefix="/api/v1", tags=["MCP Tokens"])

# Mount same routers under /auth prefix for Traefik routing
app.include_router(health.router, prefix="/auth", tags=["Health-Traefik"], include_in_schema=False)
app.include_router(auth.router, prefix="/auth/api/v1/auth", tags=["Auth-Traefik"], include_in_schema=False)
app.include_router(users.router, prefix="/auth/api/v1/users", tags=["Users-Traefik"], include_in_schema=False)
app.include_router(roles.router, prefix="/auth/api/v1", tags=["Roles-Traefik"], include_in_schema=False)
app.include_router(teams.router, prefix="/auth/api/v1", tags=["Teams-Traefik"], include_in_schema=False)
app.include_router(permissions.router, prefix="/auth/api/v1", tags=["Permissions-Traefik"], include_in_schema=False)
app.include_router(api_keys.router, prefix="/auth/api/v1", tags=["API Keys-Traefik"], include_in_schema=False)
app.include_router(mcp_tokens.router, prefix="/auth/api/v1", tags=["MCP Tokens-Traefik"], include_in_schema=False)


@app.on_event("startup")
async def startup_event():
    """Initialize on startup."""
    logger.info(f"Starting {settings.APP_TITLE} v{settings.APP_VERSION}")

    # Validate settings
    try:
        settings.validate()
    except ValueError as e:
        logger.error(f"Configuration error: {e}")
        raise

    # Initialize database pool
    try:
        await get_db_pool()
        logger.info("Database pool initialized")
    except Exception as e:
        logger.error(f"Failed to initialize database: {e}")
        raise

    logger.info(f"Service started successfully on {settings.HOST}:{settings.PORT}")


@app.on_event("shutdown")
async def shutdown_event():
    """Cleanup on shutdown."""
    logger.info("Shutting down service...")
    await close_db_pool()
    logger.info("Service shutdown complete")


if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host=settings.HOST,
        port=settings.PORT,
        reload=False
    )
