"""
Teams Management Routes
=======================
CRUD operations for teams (IAM)
"""

import logging
import json
from fastapi import APIRouter, HTTPException, Header
from pydantic import BaseModel
from typing import Optional, List, Dict, Any
from decimal import Decimal

from config.database import get_db_pool

router = APIRouter()
logger = logging.getLogger(__name__)


class TeamCreate(BaseModel):
    name: str
    description: Optional[str] = None
    mcp_access: List[str] = []
    resource_access: Dict[str, Any] = {}
    team_rate_limit: Optional[int] = None
    team_cost_limit: Optional[Decimal] = None


class TeamUpdate(BaseModel):
    name: Optional[str] = None
    description: Optional[str] = None
    mcp_access: Optional[List[str]] = None
    resource_access: Optional[Dict[str, Any]] = None
    team_rate_limit: Optional[int] = None
    team_cost_limit: Optional[Decimal] = None


@router.get("/teams")
async def list_teams():
    """List all teams"""
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            rows = await conn.fetch("""
                SELECT t.id, t.name, t.description, t.mcp_access, t.resource_access,
                       t.team_rate_limit, t.team_cost_limit, t.created_at, t.updated_at,
                       COUNT(tm.user_id) as member_count
                FROM auth_service.teams t
                LEFT JOIN auth_service.team_members tm ON t.id = tm.team_id
                GROUP BY t.id
                ORDER BY t.name
            """)
            
            return {"teams": [dict(row) for row in rows]}
    
    except Exception as e:
        logger.error(f"Error listing teams: {e}")
        raise HTTPException(500, f"Failed to list teams: {str(e)}")


@router.post("/teams")
async def create_team(
    team: TeamCreate,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Create new team (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            existing = await conn.fetchval(
                "SELECT id FROM auth_service.teams WHERE name = $1",
                team.name
            )
            
            if existing:
                raise HTTPException(400, "Team already exists")
            
            team_id = await conn.fetchval("""
                INSERT INTO auth_service.teams 
                (name, description, mcp_access, resource_access, team_rate_limit, team_cost_limit)
                VALUES ($1, $2, $3, $4::jsonb, $5, $6)
                RETURNING id
            """, team.name, team.description, team.mcp_access, json.dumps(team.resource_access),
                team.team_rate_limit, team.team_cost_limit)
            
            logger.info(f"Created team: {team.name} (ID: {team_id})")
            return {"team_id": team_id, "name": team.name}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error creating team: {e}")
        raise HTTPException(500, f"Failed to create team: {str(e)}")


@router.get("/teams/{team_id}")
async def get_team(team_id: int):
    """Get team by ID with members"""
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            team = await conn.fetchrow("""
                SELECT id, name, description, mcp_access, resource_access,
                       team_rate_limit, team_cost_limit, created_at, updated_at
                FROM auth_service.teams
                WHERE id = $1
            """, team_id)
            
            if not team:
                raise HTTPException(404, "Team not found")
            
            members = await conn.fetch("""
                SELECT u.id, u.username, u.name, u.email, r.name as role, tm.joined_at
                FROM auth_service.team_members tm
                JOIN auth_service.users u ON tm.user_id = u.id
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE tm.team_id = $1
                ORDER BY u.name
            """, team_id)
            
            result = dict(team)
            result['members'] = [dict(m) for m in members]
            return result
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error getting team: {e}")
        raise HTTPException(500, f"Failed to get team: {str(e)}")


@router.put("/teams/{team_id}")
async def update_team(
    team_id: int,
    team: TeamUpdate,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Update team (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            updates = []
            values = []
            param_count = 1
            
            if team.name:
                updates.append(f"name = ${param_count}")
                values.append(team.name)
                param_count += 1
            
            if team.description is not None:
                updates.append(f"description = ${param_count}")
                values.append(team.description)
                param_count += 1
            
            if team.mcp_access is not None:
                updates.append(f"mcp_access = ${param_count}")
                values.append(team.mcp_access)
                param_count += 1
            
            if team.resource_access is not None:
                updates.append(f"resource_access = ${param_count}")
                values.append(team.resource_access)
                param_count += 1
            
            if team.team_rate_limit is not None:
                updates.append(f"team_rate_limit = ${param_count}")
                values.append(team.team_rate_limit)
                param_count += 1
            
            if team.team_cost_limit is not None:
                updates.append(f"team_cost_limit = ${param_count}")
                values.append(team.team_cost_limit)
                param_count += 1
            
            if not updates:
                raise HTTPException(400, "No fields to update")
            
            updates.append(f"updated_at = CURRENT_TIMESTAMP")
            values.append(team_id)
            
            query = f"""
                UPDATE auth_service.teams
                SET {', '.join(updates)}
                WHERE id = ${param_count}
            """
            
            result = await conn.execute(query, *values)
            
            if result == "UPDATE 0":
                raise HTTPException(404, "Team not found")
            
            logger.info(f"Updated team ID: {team_id}")
            return {"message": "Team updated"}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error updating team: {e}")
        raise HTTPException(500, f"Failed to update team: {str(e)}")


@router.delete("/teams/{team_id}")
async def delete_team(
    team_id: int,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Delete team (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            result = await conn.execute(
                "DELETE FROM auth_service.teams WHERE id = $1",
                team_id
            )
            
            if result == "DELETE 0":
                raise HTTPException(404, "Team not found")
            
            logger.info(f"Deleted team ID: {team_id}")
            return {"message": "Team deleted"}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error deleting team: {e}")
        raise HTTPException(500, f"Failed to delete team: {str(e)}")


@router.delete("/teams/{team_id}/members/{user_id}")
async def remove_team_member(
    team_id: int,
    user_id: int,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Remove user from team (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            result = await conn.execute("""
                DELETE FROM auth_service.team_members
                WHERE team_id = $1 AND user_id = $2
            """, team_id, user_id)
            
            if result == "DELETE 0":
                raise HTTPException(404, "Team member not found")
            
            logger.info(f"Removed user {user_id} from team {team_id}")
            return {"message": "User removed from team"}
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error removing team member: {e}")
        raise HTTPException(500, f"Failed to remove team member: {str(e)}")

@router.post("/teams/{team_id}/members/{user_id}")
async def add_team_member(
    team_id: int,
    user_id: int,
    x_user_role: str = Header(None, alias="X-User-Role")
):
    """Add user to team (admin only)"""
    if x_user_role != "super_admin":
        raise HTTPException(403, "Admin access required")
    
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            await conn.execute("""
                INSERT INTO auth_service.team_members (team_id, user_id)
                VALUES ($1, $2)
                ON CONFLICT (team_id, user_id) DO NOTHING
            """, team_id, user_id)
            
            logger.info(f"Added user {user_id} to team {team_id}")
            return {"message": "User added to team"}
    
    except Exception as e:
        logger.error(f"Error adding team member: {e}")
        raise HTTPException(500, f"Failed to add team member: {str(e)}")
