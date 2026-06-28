"""
IAM Schema Models
=================
Pydantic models representing database tables.
MUST stay in sync with init_iam.py schema!
"""

from pydantic import BaseModel, EmailStr
from typing import Optional, List, Dict, Any
from datetime import datetime
from decimal import Decimal


# ============================================================================
# ROLES
# ============================================================================

class RoleBase(BaseModel):
    name: str
    description: Optional[str] = None
    permissions: List[str] = []
    mcp_access: List[str] = []
    tool_restrictions: Dict[str, List[str]] = {}
    dashboard_access: str = "none"  # admin, view, none
    rate_limit: int = 100
    cost_limit_daily: Decimal = Decimal("10.00")
    token_expiry: int = 3600


class RoleCreate(RoleBase):
    pass


class RoleUpdate(BaseModel):
    description: Optional[str] = None
    permissions: Optional[List[str]] = None
    mcp_access: Optional[List[str]] = None
    tool_restrictions: Optional[Dict[str, List[str]]] = None
    dashboard_access: Optional[str] = None
    rate_limit: Optional[int] = None
    cost_limit_daily: Optional[Decimal] = None
    token_expiry: Optional[int] = None


class Role(RoleBase):
    id: int
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# TEAMS
# ============================================================================

class TeamBase(BaseModel):
    name: str
    description: Optional[str] = None
    mcp_access: List[str] = []
    resource_access: Dict[str, Any] = {}  # {databases: [], prompts: []}
    team_rate_limit: Optional[int] = None
    team_cost_limit: Optional[Decimal] = None


class TeamCreate(TeamBase):
    pass


class TeamUpdate(BaseModel):
    name: Optional[str] = None
    description: Optional[str] = None
    mcp_access: Optional[List[str]] = None
    resource_access: Optional[Dict[str, Any]] = None
    team_rate_limit: Optional[int] = None
    team_cost_limit: Optional[Decimal] = None


class Team(TeamBase):
    id: int
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# USERS
# ============================================================================

class UserBase(BaseModel):
    username: str
    name: str
    email: Optional[EmailStr] = None
    role_id: int
    active: bool = True


class UserCreate(UserBase):
    password: Optional[str] = None


class UserUpdate(BaseModel):
    name: Optional[str] = None
    email: Optional[EmailStr] = None
    role_id: Optional[int] = None
    active: Optional[bool] = None
    password: Optional[str] = None
    rate_limit_override: Optional[int] = None


class User(UserBase):
    id: int
    api_key_hash: Optional[str] = None
    rate_limit_override: Optional[int] = None
    last_login_at: Optional[datetime] = None
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


class UserWithRole(User):
    """User with role details"""
    role_name: str
    role_permissions: List[str]
    role_mcp_access: List[str]
    role_dashboard_access: str


# ============================================================================
# TEAM MEMBERS
# ============================================================================

class TeamMemberBase(BaseModel):
    team_id: int
    user_id: int


class TeamMemberCreate(TeamMemberBase):
    pass


class TeamMember(TeamMemberBase):
    id: int
    joined_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# USER OVERRIDES
# ============================================================================

class UserOverrideBase(BaseModel):
    user_id: int
    mcp_restrictions: List[str] = []
    tool_restrictions: Dict[str, List[str]] = {}
    custom_rate_limit: Optional[int] = None
    custom_cost_limit: Optional[Decimal] = None


class UserOverrideCreate(UserOverrideBase):
    pass


class UserOverrideUpdate(BaseModel):
    mcp_restrictions: Optional[List[str]] = None
    tool_restrictions: Optional[Dict[str, List[str]]] = None
    custom_rate_limit: Optional[int] = None
    custom_cost_limit: Optional[Decimal] = None


class UserOverride(UserOverrideBase):
    id: int
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# EFFECTIVE PERMISSIONS (Computed)
# ============================================================================

class EffectivePermissions(BaseModel):
    """User's effective permissions after role + team + overrides"""
    user_id: int
    username: str
    role_name: str
    teams: List[str] = []
    
    # Computed permissions
    permissions: List[str] = []
    mcp_access: List[str] = []
    tool_access: Dict[str, List[str]] = {}
    dashboard_access: str = "none"
    
    # Computed limits
    rate_limit: int
    cost_limit_daily: Decimal
    token_expiry: int
    
    # Sources
    from_role: bool = True
    from_teams: List[str] = []
    from_overrides: bool = False


# ============================================================================
# AUTH AUDIT
# ============================================================================

class AuthAuditBase(BaseModel):
    user_id: Optional[int] = None
    username: Optional[str] = None
    action: str
    resource: Optional[str] = None
    result: str  # success, failure
    ip_address: Optional[str] = None
    user_agent: Optional[str] = None
    details: Optional[str] = None


class AuthAuditCreate(AuthAuditBase):
    pass


class AuthAudit(AuthAuditBase):
    id: int
    created_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# USER SESSIONS
# ============================================================================

class UserSessionBase(BaseModel):
    user_id: int
    token_hash: str
    refresh_token_hash: Optional[str] = None
    expires_at: datetime


class UserSessionCreate(UserSessionBase):
    pass


class UserSession(UserSessionBase):
    id: int
    created_at: datetime

    class Config:
        from_attributes = True


# ============================================================================
# BLACKLISTED TOKENS
# ============================================================================

class BlacklistedTokenBase(BaseModel):
    token_hash: str
    expires_at: datetime


class BlacklistedTokenCreate(BlacklistedTokenBase):
    pass


class BlacklistedToken(BlacklistedTokenBase):
    id: int
    created_at: datetime

    class Config:
        from_attributes = True
