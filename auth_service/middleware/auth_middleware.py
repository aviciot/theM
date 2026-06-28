"""
Authentication Middleware
=========================
Dependency functions for protected routes.
"""

from fastapi import Depends, HTTPException
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials

from models.schemas import User
from services.token_service import verify_token
from services.user_service import get_user_by_id, get_user_permissions, check_permission
from services.audit_service import audit_log

security = HTTPBearer(auto_error=False)


async def get_current_user(credentials: HTTPAuthorizationCredentials = Depends(security)) -> User:
    """
    Get current authenticated user from JWT token.

    Args:
        credentials: Bearer token credentials

    Returns:
        User object

    Raises:
        HTTPException: If authentication fails
    """
    if not credentials:
        raise HTTPException(401, "Authorization header required")

    payload = await verify_token(credentials.credentials)
    user_id = int(payload.get("sub"))

    user = await get_user_by_id(user_id)
    if not user:
        raise HTTPException(401, "User not found or inactive")

    return user


def require_permission(resource: str, action: str = "read"):
    """
    Dependency to require specific permission.

    Args:
        resource: Resource name
        action: Action name (default: "read")

    Returns:
        Dependency function
    """
    async def permission_checker(user: User = Depends(get_current_user)) -> User:
        permissions = await get_user_permissions(user)
        if not check_permission(permissions, resource, action):
            await audit_log(
                user.id,
                user.username,
                "permission_denied",
                "failed",
                resource=f"{resource}:{action}"
            )
            raise HTTPException(403, f"Permission denied: {resource}:{action}")
        return user

    return permission_checker
