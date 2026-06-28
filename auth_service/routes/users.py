"""
User Management Routes
======================
User CRUD operations
"""

import logging
from fastapi import APIRouter, HTTPException, status, Header
from pydantic import BaseModel, EmailStr
from typing import Optional

from config.database import get_db_pool
from services.password_service import hash_password

router = APIRouter()
logger = logging.getLogger(__name__)


class UserCreate(BaseModel):
    """User creation request."""
    username: str
    email: EmailStr
    name: str
    password: Optional[str] = None
    role: str = "viewer"
    active: bool = True
    user_type: str = "internal"
    source: Optional[str] = None


class UserUpdate(BaseModel):
    name: Optional[str] = None
    email: Optional[EmailStr] = None
    role_id: Optional[int] = None
    active: Optional[bool] = None
    password: Optional[str] = None
    rate_limit_override: Optional[int] = None


@router.get("/users")
async def list_users(
    page: int = 1,
    per_page: int = 20,
    search: str = None,
    role_id: int = None,
    active: bool = None,
    user_type: str = None,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """
    List all users with pagination and filters.
    """
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            # Build WHERE clause
            where_clauses = []
            params = []
            param_count = 1
            
            if search:
                where_clauses.append(f"(u.name ILIKE ${param_count} OR u.email ILIKE ${param_count} OR u.username ILIKE ${param_count})")
                params.append(f"%{search}%")
                param_count += 1
            
            if role_id:
                where_clauses.append(f"u.role_id = ${param_count}")
                params.append(role_id)
                param_count += 1
            
            if active is not None:
                where_clauses.append(f"u.active = ${param_count}")
                params.append(active)
                param_count += 1

            if user_type:
                where_clauses.append(f"u.user_type = ${param_count}")
                params.append(user_type)
                param_count += 1

            where_sql = "WHERE " + " AND ".join(where_clauses) if where_clauses else ""
            
            # Get total count
            total = await conn.fetchval(f"""
                SELECT COUNT(*) FROM auth_service.users u {where_sql}
            """, *params)
            
            # Get paginated results
            offset = (page - 1) * per_page
            params.extend([per_page, offset])
            
            rows = await conn.fetch(f"""
                SELECT u.id, u.username, u.name, u.email, u.active, u.rate_limit_override,
                       u.last_login_at, u.created_at, u.updated_at, u.user_type, u.source,
                       r.id as role_id, r.name as role_name,
                       r.mcp_access, r.tool_restrictions
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                {where_sql}
                ORDER BY u.name
                LIMIT ${param_count} OFFSET ${param_count + 1}
            """, *params)
            
            return {
                "users": [dict(row) for row in rows],
                "total": total,
                "page": page,
                "per_page": per_page
            }

    except Exception as e:
        logger.error(f"Error listing users: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to list users: {str(e)}")


@router.get("/users/{user_id}")
async def get_user(user_id: int):
    """
    Get user by ID with role details.
    """
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            row = await conn.fetchrow("""
                SELECT u.id, u.username, u.name, u.email, u.active, u.rate_limit_override,
                       u.last_login_at, u.created_at, u.updated_at, u.user_type, u.source,
                       r.id as role_id, r.name as role_name, r.mcp_access
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE u.id = $1
            """, user_id)
            
            if not row:
                raise HTTPException(status_code=404, detail="User not found")
            
            return dict(row)

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error getting user: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to get user: {str(e)}")


@router.put("/users/{user_id}")
async def update_user(
    user_id: int,
    user_data: UserUpdate,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """
    Update user details (admin only).
    """
    if x_user_role != "super_admin":
        raise HTTPException(status_code=403, detail="Admin access required")
    
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            # Build update query
            updates = []
            values = []
            param_count = 1
            
            if user_data.name:
                updates.append(f"name = ${param_count}")
                values.append(user_data.name)
                param_count += 1
            
            if user_data.email:
                updates.append(f"email = ${param_count}")
                values.append(user_data.email)
                param_count += 1
            
            if user_data.role_id:
                updates.append(f"role_id = ${param_count}")
                values.append(user_data.role_id)
                param_count += 1
            
            if user_data.active is not None:
                updates.append(f"active = ${param_count}")
                values.append(user_data.active)
                param_count += 1
            
            if user_data.rate_limit_override is not None:
                updates.append(f"rate_limit_override = ${param_count}")
                values.append(user_data.rate_limit_override)
                param_count += 1
            
            if user_data.password:
                password_hash = hash_password(user_data.password)
                updates.append(f"password_hash = ${param_count}")
                values.append(password_hash)
                param_count += 1
            
            if not updates:
                raise HTTPException(status_code=400, detail="No fields to update")
            
            updates.append(f"updated_at = CURRENT_TIMESTAMP")
            values.append(user_id)
            
            query = f"""
                UPDATE auth_service.users
                SET {', '.join(updates)}
                WHERE id = ${param_count}
            """
            
            result = await conn.execute(query, *values)
            
            if result == "UPDATE 0":
                raise HTTPException(status_code=404, detail="User not found")
            
            logger.info(f"Updated user ID: {user_id}")
            return {"message": "User updated"}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error updating user: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to update user: {str(e)}")


@router.post("/users/{user_id}/reset-password")
async def reset_password(
    user_id: int,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """
    Reset user password (admin only).
    """
    if x_user_role != "super_admin":
        raise HTTPException(status_code=403, detail="Admin access required")
    
    import secrets
    import string
    
    # Generate random password
    alphabet = string.ascii_letters + string.digits + "!@#$%"
    temp_password = ''.join(secrets.choice(alphabet) for _ in range(12))
    
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            password_hash = hash_password(temp_password)
            
            result = await conn.execute("""
                UPDATE auth_service.users
                SET password_hash = $1, updated_at = CURRENT_TIMESTAMP
                WHERE id = $2
            """, password_hash, user_id)
            
            if result == "UPDATE 0":
                raise HTTPException(status_code=404, detail="User not found")
            
            logger.info(f"Reset password for user ID: {user_id}")
            return {"temporary_password": temp_password}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error resetting password: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to reset password: {str(e)}")


@router.get("/users/{user_id}/activity")
async def get_user_activity(
    user_id: int,
    limit: int = 50,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """
    Get user activity log (admin only).
    """
    if x_user_role != "super_admin":
        raise HTTPException(status_code=403, detail="Admin access required")
    
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            rows = await conn.fetch("""
                SELECT id, user_id, username, action, result,
                       ip_address, user_agent, details, created_at
                FROM auth_service.auth_audit
                WHERE user_id = $1
                ORDER BY created_at DESC
                LIMIT $2
            """, user_id, limit)
            
            return {"activity": [dict(row) for row in rows]}

    except Exception as e:
        logger.error(f"Error getting user activity: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to get user activity: {str(e)}")
@router.post("/users", status_code=status.HTTP_201_CREATED)
async def create_user(user_data: UserCreate):
    """
    Create new user in auth_service.

    This is the single source of truth for user identity.
    """
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            # Check if user exists
            existing = await conn.fetchval(
                "SELECT id FROM auth_service.users WHERE email = $1 OR username = $2",
                user_data.email, user_data.username
            )

            if existing:
                raise HTTPException(
                    status_code=400,
                    detail="User with this email or username already exists"
                )

            # Hash password if provided
            password_hash = None
            if user_data.password:
                password_hash = hash_password(user_data.password)

            # Create user
            user_id = await conn.fetchval("""
                INSERT INTO auth_service.users (username, email, name, password_hash, role_id, active, user_type, source, created_at)
                VALUES ($1, $2, $3, $4, (SELECT id FROM auth_service.roles WHERE name = $5), $6, $7, $8, CURRENT_TIMESTAMP)
                RETURNING id
            """, user_data.username, user_data.email, user_data.name, password_hash, user_data.role, user_data.active, user_data.user_type, user_data.source)

            logger.info(f"Created user: {user_data.username} (ID: {user_id})")

            return {
                "user_id": user_id,
                "username": user_data.username,
                "email": user_data.email,
                "name": user_data.name,
                "role": user_data.role,
                "active": user_data.active
            }

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error creating user: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to create user: {str(e)}")


@router.delete("/users/{user_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_user(user_id: int):
    """
    Delete user from auth_service.

    Used for rollback compensation when omni2-admin fails to create business data.
    """
    pool = await get_db_pool()

    try:
        async with pool.acquire() as conn:
            result = await conn.execute(
                "DELETE FROM auth_service.users WHERE id = $1",
                user_id
            )

            if result == "DELETE 0":
                raise HTTPException(status_code=404, detail="User not found")

            logger.info(f"Deleted user ID: {user_id}")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error deleting user: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to delete user: {str(e)}")
