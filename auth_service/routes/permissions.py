"""
Permissions Routes
==================
Calculate effective permissions for users
"""

import logging
from fastapi import APIRouter, HTTPException
from typing import List, Dict
from decimal import Decimal

from config.database import get_db_pool

router = APIRouter()
logger = logging.getLogger(__name__)


@router.get("/permissions/{user_id}")
async def get_user_permissions(user_id: int):
    """
    Calculate effective permissions for user.
    
    Combines:
    - Role permissions
    - Team permissions (intersection)
    - User overrides (restrictions)
    
    This is called by omni2 before allowing MCP access.
    """
    pool = await get_db_pool()
    
    try:
        async with pool.acquire() as conn:
            # Get user with role
            user = await conn.fetchrow("""
                SELECT u.id, u.username, u.email, u.active, u.rate_limit_override,
                       r.id as role_id, r.name as role_name, r.mcp_access,
                       r.tool_restrictions, r.dashboard_access, r.rate_limit, r.cost_limit_daily
                FROM auth_service.users u
                JOIN auth_service.roles r ON u.role_id = r.id
                WHERE u.id = $1
            """, user_id)
            
            if not user:
                raise HTTPException(404, "User not found")
            
            if not user['active']:
                raise HTTPException(403, "User is inactive")
            
            # Get user's teams
            teams = await conn.fetch("""
                SELECT t.id, t.name, t.mcp_access, t.resource_access, 
                       t.team_rate_limit, t.team_cost_limit
                FROM auth_service.teams t
                JOIN auth_service.team_members tm ON t.id = tm.team_id
                WHERE tm.user_id = $1
            """, user_id)
            
            # Get user overrides
            overrides = await conn.fetchrow("""
                SELECT mcp_restrictions, tool_restrictions, custom_rate_limit, custom_cost_limit
                FROM auth_service.user_overrides
                WHERE user_id = $1
            """, user_id)
            
            # Calculate effective permissions
            tool_restrictions = user['tool_restrictions'] or {}
            if isinstance(tool_restrictions, str):
                import json
                tool_restrictions = json.loads(tool_restrictions)
            
            result = {
                "user_id": user['id'],
                "username": user['username'],
                "email": user['email'],
                "role": user['role_name'],
                "teams": [t['name'] for t in teams],
                
                # From role
                "mcp_access": list(user['mcp_access'] or []),
                "tool_access": tool_restrictions,
                "dashboard_access": user['dashboard_access'],
                "rate_limit": user['rate_limit'],
                "cost_limit_daily": float(user['cost_limit_daily']),
            }
            
            # Apply team restrictions (intersection)
            if teams:
                team_mcp_access = set()
                for team in teams:
                    if team['mcp_access']:
                        team_mcp_access.update(team['mcp_access'])
                
                # If teams restrict MCPs, intersect with role MCPs
                if team_mcp_access:
                    role_mcps = set(result['mcp_access']) if result['mcp_access'] != ['*'] else team_mcp_access
                    result['mcp_access'] = list(role_mcps & team_mcp_access)
                
                # Use team rate limit if set
                team_rate_limits = [t['team_rate_limit'] for t in teams if t['team_rate_limit']]
                if team_rate_limits:
                    result['rate_limit'] = min(result['rate_limit'], min(team_rate_limits))
                
                # Use team cost limit if set
                team_cost_limits = [t['team_cost_limit'] for t in teams if t['team_cost_limit']]
                if team_cost_limits:
                    result['cost_limit_daily'] = min(result['cost_limit_daily'], float(min(team_cost_limits)))
            
            # Apply user overrides (restrictions only)
            if overrides:
                # Remove restricted MCPs
                if overrides['mcp_restrictions']:
                    result['mcp_access'] = [
                        mcp for mcp in result['mcp_access'] 
                        if mcp not in overrides['mcp_restrictions']
                    ]
                
                # Apply tool restrictions
                if overrides['tool_restrictions']:
                    for mcp, tools in overrides['tool_restrictions'].items():
                        if mcp in result['tool_access']:
                            # Restrict tools
                            result['tool_access'][mcp] = [
                                t for t in result['tool_access'][mcp]
                                if t not in tools
                            ]
                
                # Apply custom rate limit (lower only)
                if overrides['custom_rate_limit']:
                    result['rate_limit'] = min(result['rate_limit'], overrides['custom_rate_limit'])
                
                # Apply custom cost limit (lower only)
                if overrides['custom_cost_limit']:
                    result['cost_limit_daily'] = min(result['cost_limit_daily'], float(overrides['custom_cost_limit']))
            
            # Apply user-specific rate limit override
            if user['rate_limit_override']:
                result['rate_limit'] = user['rate_limit_override']
            
            return result
    
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error calculating permissions: {e}")
        raise HTTPException(500, f"Failed to calculate permissions: {str(e)}")


@router.get("/permissions/check/{user_id}/{mcp_name}/{tool_name}")
async def check_permission(user_id: int, mcp_name: str, tool_name: str):
    """
    Quick check if user can access specific MCP tool.
    
    Returns: {"allowed": true/false, "reason": "..."}
    """
    try:
        permissions = await get_user_permissions(user_id)
        
        # Check MCP access
        if permissions['mcp_access'] != ['*'] and mcp_name not in permissions['mcp_access']:
            return {
                "allowed": False,
                "reason": f"User does not have access to {mcp_name}"
            }
        
        # Check tool access
        if mcp_name in permissions['tool_access']:
            allowed_tools = permissions['tool_access'][mcp_name]
            if allowed_tools != ['*'] and tool_name not in allowed_tools:
                return {
                    "allowed": False,
                    "reason": f"User does not have access to tool {tool_name}"
                }
        
        return {
            "allowed": True,
            "reason": "Access granted"
        }
    
    except HTTPException as e:
        return {
            "allowed": False,
            "reason": str(e.detail)
        }
    except Exception as e:
        logger.error(f"Error checking permission: {e}")
        return {
            "allowed": False,
            "reason": "Permission check failed"
        }
