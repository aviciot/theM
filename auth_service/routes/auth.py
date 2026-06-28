"""
Authentication Routes
=====================
Login, logout, token validation, and refresh endpoints.
"""

import logging
from fastapi import APIRouter, HTTPException, Request, Depends
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials

from models.schemas import AuthRequest, TokenPair, User
from services.user_service import get_user_by_api_key, get_user_by_id
from services.password_service import authenticate_with_password
from services.token_service import create_access_token, create_refresh_token, verify_token, revoke_token
from services.audit_service import audit_log
from config.database import get_db_pool
from config.settings import settings

router = APIRouter()
security = HTTPBearer(auto_error=False)
logger = logging.getLogger(__name__)


@router.post("/login", response_model=TokenPair)
async def login(auth_request: AuthRequest, request: Request):
    """
    Login with API key OR email/password and get JWT tokens.

    Supports two authentication methods:
    1. API Key: { "api_key": "ak_..." }
    2. Email/Password: { "username": "email@example.com", "password": "..." }
    """
    logger.info(f"[LOGIN] Request received from {request.client.host if hasattr(request, 'client') else 'unknown'}")
    logger.info(f"[LOGIN] Username: {auth_request.username}, Has password: {bool(auth_request.password)}, Has API key: {bool(auth_request.api_key)}")
    
    ip_address = request.client.host if hasattr(request, 'client') else None
    user = None

    # Method 1: API Key Authentication
    if auth_request.api_key:
        logger.info(f"[LOGIN] Attempting API key authentication")
        user = await get_user_by_api_key(auth_request.api_key)
        if not user:
            logger.warning(f"[LOGIN] API key authentication failed")
            await audit_log(None, None, "login", "failed", ip_address=ip_address,
                           details="invalid_api_key")
            raise HTTPException(401, "Invalid API key")

    # Method 2: Email/Password Authentication
    elif auth_request.username and auth_request.password:
        logger.info(f"[LOGIN] Attempting password authentication for {auth_request.username}")
        user = await authenticate_with_password(auth_request.username, auth_request.password)
        if not user:
            logger.warning(f"[LOGIN] Password authentication failed for {auth_request.username}")
            await audit_log(None, auth_request.username, "login", "failed", ip_address=ip_address,
                           details="invalid_credentials")
            raise HTTPException(401, "Invalid email or password")
        logger.info(f"[LOGIN] Password authentication successful for {auth_request.username} (user_id={user.id})")

    else:
        logger.error(f"[LOGIN] No authentication method provided")
        raise HTTPException(400, "Either api_key or username+password required")

    # Check dashboard access
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        dashboard_access = await conn.fetchval("""
            SELECT r.dashboard_access
            FROM auth_service.roles r
            JOIN auth_service.users u ON u.role_id = r.id
            WHERE u.id = $1
        """, user.id)
    if not dashboard_access or dashboard_access == "none":
        logger.warning(f"[LOGIN] REJECTED - role has no dashboard access | user_id={user.id} | dashboard_access={dashboard_access}")
        await audit_log(user.id, user.username, "login", "failed", ip_address=ip_address,
                       details="dashboard_access_denied")
        raise HTTPException(403, "Your role does not have access to the dashboard")

    # Create tokens
    logger.info(f"[LOGIN] Creating tokens for user_id={user.id} | dashboard_access={dashboard_access}")
    access_token = await create_access_token(user)
    refresh_token = await create_refresh_token(user)

    # Get token expiry from role
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        expires_in = await conn.fetchval("""
            SELECT r.token_expiry 
            FROM auth_service.roles r
            JOIN auth_service.users u ON u.role_id = r.id
            WHERE u.id = $1
        """, user.id)

    if not expires_in:
        expires_in = settings.ACCESS_TOKEN_EXPIRY

    # Update last login
    async with pool.acquire() as conn:
        await conn.execute("""
            UPDATE auth_service.users SET last_login_at = CURRENT_TIMESTAMP WHERE id = $1
        """, user.id)

    # Audit log
    await audit_log(user.id, user.username, "login", "success", ip_address=ip_address)

    logger.info(f"[LOGIN] Success for user_id={user.id}, expires_in={expires_in}s")
    return TokenPair(
        access_token=access_token,
        refresh_token=refresh_token,
        expires_in=expires_in
    )


@router.get("/validate")
async def validate(request: Request, credentials: HTTPAuthorizationCredentials = Depends(security)):
    """
    Validate JWT token (used by Traefik forwardAuth).

    Returns 200 with headers for Traefik to forward:
    - X-User-Id
    - X-User-Username
    - X-User-Role
    """
    from fastapi.responses import Response
    
    # Get request path and client info for logging
    path = request.url.path if hasattr(request, 'url') else 'unknown'
    client_ip = request.client.host if hasattr(request, 'client') else 'unknown'
    
    # CRITICAL: Log every validation attempt to prove auth is enforced
    logger.info(f"[VALIDATE] Auth validation request | path={path} | client_ip={client_ip} | has_credentials={bool(credentials)}")
    
    if not credentials:
        # This is expected for public endpoints - don't log as error
        logger.warning(f"[VALIDATE] REJECTED - No auth header | path={path} | client_ip={client_ip}")
        raise HTTPException(401, "Authorization header required")

    try:
        # Log token validation attempt
        token_preview = credentials.credentials[:20] + "..." if len(credentials.credentials) > 20 else credentials.credentials
        logger.info(f"[VALIDATE] Validating token | path={path} | token_preview={token_preview}")
        
        payload = await verify_token(credentials.credentials)
        user_id = int(payload.get("sub"))

        # Get fresh user data
        user = await get_user_by_id(user_id)
        if not user:
            logger.warning(f"[VALIDATE] REJECTED - User not found | user_id={user_id} | path={path}")
            raise HTTPException(401, "User not found or inactive")

        # SUCCESS: Log successful validation with user details
        logger.info(f"[VALIDATE] SUCCESS - Token valid | user_id={user.id} | username={user.username} | role={user.role} | path={path}")
        
        # Return 200 with headers (Traefik will forward these)
        return Response(
            status_code=200,
            headers={
                "X-User-Id": str(user.id),
                "X-User-Username": user.username,
                "X-User-Role": user.role
            }
        )
    except HTTPException:
        # Re-raise HTTP exceptions (already logged by verify_token)
        logger.warning(f"[VALIDATE] REJECTED - Token validation failed | path={path} | client_ip={client_ip}")
        raise
    except Exception as e:
        logger.error(f"[VALIDATE] ERROR - Unexpected validation error | path={path} | error={str(e)}")
        raise HTTPException(401, "Token validation failed")


@router.post("/refresh", response_model=TokenPair)
async def refresh(credentials: HTTPAuthorizationCredentials = Depends(security)):
    """
    Refresh access token using refresh token.
    """
    if not credentials:
        raise HTTPException(401, "Authorization header required")

    # Verify refresh token
    payload = await verify_token(credentials.credentials)

    if payload.get("type") != "refresh":
        raise HTTPException(401, "Invalid token type")

    user_id = int(payload.get("sub"))
    user = await get_user_by_id(user_id)

    if not user:
        raise HTTPException(401, "User not found or inactive")

    # Create new tokens
    access_token = await create_access_token(user)
    refresh_token = await create_refresh_token(user)

    # Get expires_in
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        expires_in = await conn.fetchval("""
            SELECT r.token_expiry 
            FROM auth_service.roles r
            JOIN auth_service.users u ON u.role_id = r.id
            WHERE u.id = $1
        """, user.id)

    if not expires_in:
        expires_in = settings.ACCESS_TOKEN_EXPIRY

    return TokenPair(
        access_token=access_token,
        refresh_token=refresh_token,
        expires_in=expires_in
    )


@router.post("/logout")
async def logout(credentials: HTTPAuthorizationCredentials = Depends(security)):
    """
    Logout and revoke tokens.
    """
    if not credentials:
        raise HTTPException(401, "Authorization header required")

    # Revoke the token
    await revoke_token(credentials.credentials)

    # Get user for audit log
    try:
        payload = await verify_token(credentials.credentials)
        user_id = int(payload.get("sub"))
        await audit_log(user_id, payload.get("username"), "logout", "success")
    except:
        pass  # Token might be expired

    return {"message": "Logged out successfully"}
