"""
Pydantic Models
===============
Data validation models for API requests and responses.
"""

from datetime import datetime
from typing import List, Optional
from pydantic import BaseModel


class User(BaseModel):
    """User model."""
    id: int
    username: str
    name: str
    email: Optional[str]
    role: str
    active: bool
    rate_limit: Optional[int] = None
    created_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None


class Role(BaseModel):
    """Role model."""
    name: str
    description: str
    permissions: List[str]
    rate_limit: int
    token_expiry: int


class TokenPair(BaseModel):
    """JWT token pair response."""
    access_token: str
    refresh_token: str
    token_type: str = "bearer"
    expires_in: int


class AuthRequest(BaseModel):
    """Authentication request."""
    api_key: Optional[str] = None
    username: Optional[str] = None
    password: Optional[str] = None


class HealthResponse(BaseModel):
    """Health check response."""
    status: str
    service: str
    version: str
    timestamp: str
    database: str
    active_users: Optional[int] = None
