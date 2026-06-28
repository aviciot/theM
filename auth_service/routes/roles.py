"""
Roles Management Routes
=======================
CRUD operations for roles (IAM)
"""

import logging
import json
from fastapi import APIRouter, HTTPException, Header
from pydantic import BaseModel
from typing import Optional, List, Dict
from decimal import Decimal

from config.database import get_db_pool

router = APIRouter()
logger = logging.getLogger(__name__)


class RoleCreate(BaseModel):
    name: str
    description: Optional[str] = None
    mcp_access: List[str] = []
    tool_restrictions: Dict[str, Dict[str, List[str]]] = {}
    dashboard_access: str = "none"
    rate_limit: int = 100
    cost_limit_daily: Decimal = Decimal("10.00")
    token_expiry: int = 3600


class RoleUpdate(BaseModel):
    description: Optional[str] = None
    mcp_access: Optional[List[str]] = None
    tool_restrictions: Optional[Dict[str, Dict[str, List[str]]]] = None
    dashboard_access: Optional[str] = None
    rate_limit: Optional[int] = None
    cost_limit_daily: Optional[Decimal] = None
    token_expiry: Optional[int] = None


@router.get("/roles")
async def list_roles(x_user_role: str = Header(None, alias="X-User-Role")):
    """List all roles"""
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            rows = await conn.fetch("""
                SELECT id, name, description, mcp_access, 
                       tool_restrictions, dashboard_access, rate_limit, 
                       cost_limit_daily, token_expiry, created_at, updated_at
                FROM auth_service.roles
                ORDER BY name
            """)
            
            # Parse tool_restrictions if it's a string
            roles = []
            for row in rows:
                role_dict = dict(row)
                if isinstance(role_dict.get('tool_restrictions'), str):
                    try:
                        role_dict['tool_restrictions'] = json.loads(role_dict['tool_restrictions'])
                    except:
                        role_dict['tool_restrictions'] = {}
                roles.append(role_dict)
            
            return {"roles": roles}
    
    except Exception as e:
        logger.error(f"Error listing roles: {e}")
        raise HTTPException(500, f"Failed to list roles: {str(e)}")


@router.get("/roles/{role_id}")
async def get_role(role_id: int):
    """Get role by ID"""
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            row = await conn.fetchrow("""
                SELECT id, name, description, mcp_access, 
                       tool_restrictions, dashboard_access, rate_limit, 
                       cost_limit_daily, token_expiry, created_at, updated_at
                FROM auth_service.roles
                WHERE id = $1
            """, role_id)
            
            if not row:
                raise HTTPException(404, "Role not found")
            
            role_dict = dict(row)
            # Parse tool_restrictions if it's a string
            if isinstance(role_dict.get('tool_restrictions'), str):
                try:
                    role_dict['tool_restrictions'] = json.loads(role_dict['tool_restrictions'])
                except:
                    role_dict['tool_restrictions'] = {}
            
            return role_dict
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error getting role: {e}")
        raise HTTPException(500, f"Failed to get role: {str(e)}")


@router.post("/roles")
async def create_role(
    role: RoleCreate,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Create new role (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            # Check if role exists
            existing = await conn.fetchval(
                "SELECT id FROM auth_service.roles WHERE name = $1",
                role.name
            )
            
            if existing:
                raise HTTPException(400, "Role already exists")
            
            # Create role
            role_id = await conn.fetchval("""
                INSERT INTO auth_service.roles 
                (name, description, mcp_access, tool_restrictions, 
                 dashboard_access, rate_limit, cost_limit_daily, token_expiry)
                VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8)
                RETURNING id
            """, role.name, role.description, role.mcp_access,
                json.dumps(role.tool_restrictions), role.dashboard_access, role.rate_limit,
                role.cost_limit_daily, role.token_expiry)
            
            logger.info(f"Created role: {role.name} (ID: {role_id})")
            return {"role_id": role_id, "name": role.name}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error creating role: {e}")
        raise HTTPException(500, f"Failed to create role: {str(e)}")


@router.put("/roles/{role_id}")
async def update_role(
    role_id: int,
    role: RoleUpdate,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Update role (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            # Build update query dynamically
            updates = []
            values = []
            param_count = 1
            
            if role.description is not None:
                updates.append(f"description = ${param_count}")
                values.append(role.description)
                param_count += 1
            
            if role.mcp_access is not None:
                updates.append(f"mcp_access = ${param_count}")
                values.append(role.mcp_access)
                param_count += 1
            
            if role.tool_restrictions is not None:
                updates.append(f"tool_restrictions = ${param_count}::jsonb")
                values.append(json.dumps(role.tool_restrictions))
                param_count += 1
            
            if role.dashboard_access is not None:
                updates.append(f"dashboard_access = ${param_count}")
                values.append(role.dashboard_access)
                param_count += 1
            
            if role.rate_limit is not None:
                updates.append(f"rate_limit = ${param_count}")
                values.append(role.rate_limit)
                param_count += 1
            
            if role.cost_limit_daily is not None:
                updates.append(f"cost_limit_daily = ${param_count}")
                values.append(role.cost_limit_daily)
                param_count += 1
            
            if role.token_expiry is not None:
                updates.append(f"token_expiry = ${param_count}")
                values.append(role.token_expiry)
                param_count += 1
            
            if not updates:
                raise HTTPException(400, "No fields to update")
            
            updates.append(f"updated_at = CURRENT_TIMESTAMP")
            values.append(role_id)
            
            query = f"""
                UPDATE auth_service.roles
                SET {', '.join(updates)}
                WHERE id = ${param_count}
            """
            
            result = await conn.execute(query, *values)
            
            if result == "UPDATE 0":
                raise HTTPException(404, "Role not found")
            
            logger.info(f"Updated role ID: {role_id}")
            return {"message": "Role updated"}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error updating role: {e}")
        raise HTTPException(500, f"Failed to update role: {str(e)}")


@router.delete("/roles/{role_id}")
async def delete_role(
    role_id: int,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Delete role (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            # Check if role is in use
            users_count = await conn.fetchval(
                "SELECT COUNT(*) FROM auth_service.users WHERE role_id = $1",
                role_id
            )
            
            if users_count > 0:
                raise HTTPException(400, f"Cannot delete role: {users_count} users assigned")
            
            result = await conn.execute(
                "DELETE FROM auth_service.roles WHERE id = $1",
                role_id
            )
            
            if result == "DELETE 0":
                raise HTTPException(404, "Role not found")
            
            logger.info(f"Deleted role ID: {role_id}")
            return {"message": "Role deleted"}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error deleting role: {e}")
        raise HTTPException(500, f"Failed to delete role: {str(e)}")
