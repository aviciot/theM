"""
Configuration Settings
======================
All configuration loaded from environment variables with sensible defaults.
"""

import os
from typing import Optional


class Settings:
    """Application settings loaded from environment variables."""

    # Server Configuration
    HOST: str = os.getenv("HOST", "0.0.0.0")
    PORT: int = int(os.getenv("PORT", "8701"))
    LOG_LEVEL: str = os.getenv("LOG_LEVEL", "INFO")

    # Database Configuration
    DATABASE_URL: str = os.getenv(
        "DATABASE_URL",
        "postgresql://auth_service:auth_service@omni_pg_db:5432/omni?options=-c%20search_path=auth_service"
    )
    DB_POOL_MIN_SIZE: int = int(os.getenv("DB_POOL_MIN_SIZE", "5"))
    DB_POOL_MAX_SIZE: int = int(os.getenv("DB_POOL_MAX_SIZE", "20"))

    # JWT Configuration
    JWT_SECRET: str = os.getenv("JWT_SECRET", "your-super-secret-jwt-key-256-bit-change-this")
    JWT_ALGORITHM: str = "HS256"
    ACCESS_TOKEN_EXPIRY: int = int(os.getenv("ACCESS_TOKEN_EXPIRY", "3600"))  # 1 hour in seconds
    REFRESH_TOKEN_EXPIRY: int = int(os.getenv("REFRESH_TOKEN_EXPIRY", "604800"))  # 7 days in seconds

    # CORS Configuration
    CORS_ORIGINS: str = os.getenv("CORS_ORIGINS", "http://localhost:3000")

    # Authentication Configuration
    DEFAULT_ROLE: str = os.getenv("DEFAULT_ROLE", "viewer")

    # Caching Configuration
    CACHE_USER_TTL: int = int(os.getenv("CACHE_USER_TTL", "300"))  # 5 minutes

    # Rate Limiting Configuration
    RATE_LIMIT_WINDOW: int = int(os.getenv("RATE_LIMIT_WINDOW", "3600"))  # 1 hour

    # Application Metadata
    APP_TITLE: str = "Odin Auth Service"
    APP_DESCRIPTION: str = "Authentication and authorization for Odin"
    APP_VERSION: str = "1.0.0"

    @classmethod
    def validate(cls) -> None:
        """Validate critical settings."""
        # Check JWT secret is not default in production
        if os.getenv("ENVIRONMENT", "development") == "production":
            if cls.JWT_SECRET == "your-super-secret-jwt-key-256-bit-change-this":
                raise ValueError(
                    "JWT_SECRET must be set to a secure value in production! "
                    "Generate one with: python -c 'import secrets; print(secrets.token_urlsafe(32))'"
                )


# Create global settings instance
settings = Settings()
