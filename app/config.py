"""
Configuration for Odin — Multi-Agent Orchestration Platform

All config from environment variables. No YAML files.
Redis DB index 1 (Omni uses 0). DB name "odin".
"""

from typing import List, Optional
from pydantic import BaseModel, Field
from pydantic_settings import BaseSettings


class AppConfig(BaseModel):
    name: str = "Odin"
    environment: str = "development"
    host: str = "0.0.0.0"
    port: int = 8001
    debug: bool = False


class DatabaseConfig(BaseModel):
    host: str
    port: int = 5432
    database: str = "odin"
    user: str = "odin"
    password: str
    schema: str = "odin"
    pool_size: int = 20
    max_overflow: int = 10
    echo: bool = False

    @property
    def url(self) -> str:
        return f"postgresql+asyncpg://{self.user}:{self.password}@{self.host}:{self.port}/{self.database}"


class RedisConfig(BaseModel):
    enabled: bool = True
    host: str = "localhost"
    port: int = 6379
    password: Optional[str] = None
    db: int = 1          # Odin always uses DB 1; Omni uses 0
    ssl: bool = False


class LLMConfig(BaseModel):
    api_key: str = ""
    model: str = "claude-sonnet-4-6"
    max_tokens: int = 4096
    timeout: int = 30


class SecurityConfig(BaseModel):
    secret_key: str
    cors_enabled: bool = True
    cors_origins: List[str] = Field(default_factory=lambda: ["*"])


class LoggingConfig(BaseModel):
    level: str = "INFO"
    format: str = "json"
    file: str = "logs/odin.log"


class Settings(BaseSettings):
    model_config = {
        "env_file": ".env",
        "case_sensitive": True,
        "extra": "ignore",
    }

    # App
    APP_ENV: str = "development"
    APP_DEBUG: bool = False
    APP_HOST: str = "0.0.0.0"
    APP_PORT: int = 8001
    ODIN_INSTANCE_ID: str = "bridge-1"

    # Database
    DATABASE_HOST: str = "localhost"
    DATABASE_PORT: int = 5432
    DATABASE_NAME: str = "odin"
    DATABASE_USER: str = "odin"
    DATABASE_PASSWORD: str = "change_me"
    DATABASE_POOL_SIZE: int = 20
    DATABASE_MAX_OVERFLOW: int = 10

    # Redis — DB 1
    REDIS_ENABLED: bool = True
    REDIS_HOST: str = "localhost"
    REDIS_PORT: int = 6379
    REDIS_PASSWORD: Optional[str] = None
    REDIS_DB: int = 1
    REDIS_SSL: bool = False

    # Auth service
    AUTH_SERVICE_URL: str = "http://odin-auth-service:8701"

    # Security
    SECRET_KEY: str = "change-this-in-production"
    CORS_ENABLED: bool = True
    CORS_ORIGINS: str = "http://localhost:3111"

    # LLM
    ANTHROPIC_API_KEY: str = ""
    ANTHROPIC_MODEL: str = "claude-sonnet-4-6"
    ANTHROPIC_MAX_TOKENS: int = 4096
    ANTHROPIC_TIMEOUT: int = 30

    # Logging
    LOG_LEVEL: str = "INFO"
    LOG_FORMAT: str = "json"


class GlobalConfig:
    def __init__(self):
        env = Settings()

        self.app = AppConfig(
            environment=env.APP_ENV,
            host=env.APP_HOST,
            port=env.APP_PORT,
            debug=env.APP_DEBUG,
        )
        self.odin_instance_id: str = env.ODIN_INSTANCE_ID

        self.database = DatabaseConfig(
            host=env.DATABASE_HOST,
            port=env.DATABASE_PORT,
            database=env.DATABASE_NAME,
            user=env.DATABASE_USER,
            password=env.DATABASE_PASSWORD,
            pool_size=env.DATABASE_POOL_SIZE,
            max_overflow=env.DATABASE_MAX_OVERFLOW,
        )

        self.redis = RedisConfig(
            enabled=env.REDIS_ENABLED,
            host=env.REDIS_HOST,
            port=env.REDIS_PORT,
            password=env.REDIS_PASSWORD,
            db=env.REDIS_DB,
            ssl=env.REDIS_SSL,
        )

        self.llm = LLMConfig(
            api_key=env.ANTHROPIC_API_KEY,
            model=env.ANTHROPIC_MODEL,
            max_tokens=env.ANTHROPIC_MAX_TOKENS,
            timeout=env.ANTHROPIC_TIMEOUT,
        )

        self.security = SecurityConfig(
            secret_key=env.SECRET_KEY,
            cors_enabled=env.CORS_ENABLED,
            cors_origins=[o.strip() for o in env.CORS_ORIGINS.split(",") if o.strip()],
        )

        self.auth_service_url: str = env.AUTH_SERVICE_URL

        self.logging = LoggingConfig(
            level=env.LOG_LEVEL,
            format=env.LOG_FORMAT,
        )


settings = GlobalConfig()
