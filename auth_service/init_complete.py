#!/usr/bin/env python3
"""
Complete Auth Service Database Initialization
==============================================
Single script that:
1. Drops schema (if --drop flag)
2. Creates all tables from code expectations
3. Creates default roles
4. Creates admin users with passwords
5. Validates everything worked
"""

import asyncio
import asyncpg
import bcrypt
import sys
import os

DROP_SCHEMA = "--drop" in sys.argv

async def main():
    print("=" * 60)
    print("AUTH SERVICE DATABASE INITIALIZATION")
    print("=" * 60)
    
    if DROP_SCHEMA:
        print("\n⚠️  DROP MODE: Will delete existing schema!")
    
    # Connect as omni user
    conn = None
    try:
        print("\n[1/6] Connecting to database...")
        conn = await asyncpg.connect("postgresql://omni:omni@omni_pg_db:5432/omni")
        print("✓ Connected")
        
        # Drop schema if requested
        if DROP_SCHEMA:
            print("\n[2/6] Dropping auth_service schema...")
            await conn.execute("DROP SCHEMA IF EXISTS auth_service CASCADE")
            print("✓ Schema dropped")
        else:
            print("\n[2/6] Skipping drop (use --drop to drop schema)")
        
        # Create schema
        print("\n[3/6] Creating auth_service schema...")
        await conn.execute("CREATE SCHEMA IF NOT EXISTS auth_service")
        print("✓ Schema created")
        
        # Create tables
        print("\n[4/6] Creating tables...")
        
        # Roles table (IAM enhanced)
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.roles (
                id SERIAL PRIMARY KEY,
                name VARCHAR(50) UNIQUE NOT NULL,
                description TEXT,
                mcp_access TEXT[] DEFAULT '{}',
                tool_restrictions JSONB DEFAULT '{}',
                dashboard_access VARCHAR(20) DEFAULT 'none',
                rate_limit INTEGER DEFAULT 100,
                cost_limit_daily DECIMAL(10,2) DEFAULT 10.00,
                token_expiry INTEGER DEFAULT 3600,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        
        # Users table (IAM enhanced)
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.users (
                id SERIAL PRIMARY KEY,
                username VARCHAR(100) UNIQUE NOT NULL,
                name VARCHAR(255) NOT NULL,
                email VARCHAR(255) UNIQUE,
                role_id INTEGER REFERENCES auth_service.roles(id),
                password_hash VARCHAR(255),
                api_key_hash VARCHAR(255) UNIQUE,
                active BOOLEAN NOT NULL DEFAULT true,
                rate_limit_override INTEGER,
                last_login_at TIMESTAMP,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        
        # Teams table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.teams (
                id SERIAL PRIMARY KEY,
                name VARCHAR(100) UNIQUE NOT NULL,
                description TEXT,
                mcp_access TEXT[] DEFAULT '{}',
                resource_access JSONB DEFAULT '{}',
                team_rate_limit INTEGER,
                team_cost_limit DECIMAL(10,2),
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        
        # Team members table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.team_members (
                id SERIAL PRIMARY KEY,
                team_id INTEGER NOT NULL REFERENCES auth_service.teams(id) ON DELETE CASCADE,
                user_id INTEGER NOT NULL REFERENCES auth_service.users(id) ON DELETE CASCADE,
                joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                UNIQUE(team_id, user_id)
            )
        """)
        
        # User overrides table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.user_overrides (
                id SERIAL PRIMARY KEY,
                user_id INTEGER UNIQUE NOT NULL REFERENCES auth_service.users(id) ON DELETE CASCADE,
                mcp_restrictions TEXT[] DEFAULT '{}',
                tool_restrictions JSONB DEFAULT '{}',
                custom_rate_limit INTEGER,
                custom_cost_limit DECIMAL(10,2),
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        
        # Auth audit table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.auth_audit (
                id SERIAL PRIMARY KEY,
                user_id INTEGER,
                username VARCHAR(100),
                action VARCHAR(50) NOT NULL,
                resource VARCHAR(200),
                result VARCHAR(20) NOT NULL,
                ip_address INET,
                user_agent TEXT,
                details TEXT,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        
        # User sessions table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.user_sessions (
                id SERIAL PRIMARY KEY,
                user_id INTEGER NOT NULL,
                token_hash VARCHAR(64) NOT NULL,
                refresh_token_hash VARCHAR(64),
                expires_at TIMESTAMP NOT NULL,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON auth_service.user_sessions(user_id)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_user_sessions_token_hash ON auth_service.user_sessions(token_hash)")
        
        # Blacklisted tokens table
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS auth_service.blacklisted_tokens (
                id SERIAL PRIMARY KEY,
                token_hash VARCHAR(64) UNIQUE NOT NULL,
                expires_at TIMESTAMP NOT NULL,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """)
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_blacklisted_tokens_hash ON auth_service.blacklisted_tokens(token_hash)")
        
        # IAM indexes
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_users_role_id ON auth_service.users(role_id)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_users_email ON auth_service.users(email)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_team_members_team_id ON auth_service.team_members(team_id)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_team_members_user_id ON auth_service.team_members(user_id)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_auth_audit_user_id ON auth_service.auth_audit(user_id)")
        await conn.execute("CREATE INDEX IF NOT EXISTS idx_auth_audit_created_at ON auth_service.auth_audit(created_at)")
        
        print("✓ Tables created: roles, users, teams, team_members, user_overrides, auth_audit, user_sessions, blacklisted_tokens")
        
        # Insert default roles
        print("\n[5/6] Creating default roles...")
        
        # Super Admin
        await conn.execute("""
            INSERT INTO auth_service.roles (name, description, mcp_access, dashboard_access, rate_limit, cost_limit_daily, token_expiry) VALUES
            ('super_admin', 'Super Administrator - Full Access', 
             ARRAY['*'], 
             'admin', 
             10000, 
             1000.00, 
             7200)
            ON CONFLICT (name) DO UPDATE SET
                description = EXCLUDED.description,
                mcp_access = EXCLUDED.mcp_access,
                dashboard_access = EXCLUDED.dashboard_access
        """)
        
        # Developer
        await conn.execute("""
            INSERT INTO auth_service.roles (name, description, mcp_access, tool_restrictions, dashboard_access, rate_limit, cost_limit_daily, token_expiry) VALUES
            ('developer', 'Developer - MCP Access', 
             ARRAY['database_mcp', 'macgyver_mcp', 'informatica_mcp'], 
             '{"database_mcp": ["analyze_full_sql_context", "compare_query_plans"], "macgyver_mcp": ["*"], "informatica_mcp": ["*"]}'::jsonb,
             'view', 
             5000, 
             100.00, 
             3600)
            ON CONFLICT (name) DO UPDATE SET
                description = EXCLUDED.description,
                mcp_access = EXCLUDED.mcp_access,
                tool_restrictions = EXCLUDED.tool_restrictions,
                dashboard_access = EXCLUDED.dashboard_access
        """)
        
        # Analyst
        await conn.execute("""
            INSERT INTO auth_service.roles (name, description, mcp_access, tool_restrictions, dashboard_access, rate_limit, cost_limit_daily, token_expiry) VALUES
            ('analyst', 'Analyst - Read-Only MCP Access', 
             ARRAY['database_mcp'], 
             '{"database_mcp": ["analyze_full_sql_context", "check_oracle_access", "check_mysql_access"]}'::jsonb,
             'view', 
             1000, 
             50.00, 
             3600)
            ON CONFLICT (name) DO UPDATE SET
                description = EXCLUDED.description,
                mcp_access = EXCLUDED.mcp_access,
                tool_restrictions = EXCLUDED.tool_restrictions,
                dashboard_access = EXCLUDED.dashboard_access
        """)
        
        # Viewer
        await conn.execute("""
            INSERT INTO auth_service.roles (name, description, mcp_access, dashboard_access, rate_limit, cost_limit_daily, token_expiry) VALUES
            ('viewer', 'Viewer - Dashboard Only', 
             ARRAY[]::text[], 
             'view', 
             100, 
             10.00, 
             1800)
            ON CONFLICT (name) DO UPDATE SET
                description = EXCLUDED.description,
                mcp_access = EXCLUDED.mcp_access,
                dashboard_access = EXCLUDED.dashboard_access
        """)
        
        print("✓ Roles created: super_admin, developer, analyst, viewer")
        
        # Create admin users with passwords
        print("\n[6/6] Creating admin users...")
        
        # Get role IDs
        super_admin_role = await conn.fetchval("SELECT id FROM auth_service.roles WHERE name = 'super_admin'")
        viewer_role = await conn.fetchval("SELECT id FROM auth_service.roles WHERE name = 'viewer'")
        
        # Generate password hashes
        avi_hash = bcrypt.hashpw(b'avi123', bcrypt.gensalt()).decode()
        admin_hash = bcrypt.hashpw(b'admin123', bcrypt.gensalt()).decode()
        user_hash = bcrypt.hashpw(b'user123', bcrypt.gensalt()).decode()
        
        await conn.execute("""
            INSERT INTO auth_service.users (username, name, email, role_id, active, password_hash) VALUES
            ('avi', 'Avi Cohen', 'avicoiot@gmail.com', $1, true, $2),
            ('admin', 'Admin User', 'admin@company.com', $1, true, $3),
            ('user', 'Regular User', 'user@company.com', $4, true, $5)
            ON CONFLICT (username) DO UPDATE SET
                role_id = EXCLUDED.role_id,
                password_hash = EXCLUDED.password_hash
        """, super_admin_role, avi_hash, admin_hash, viewer_role, user_hash)
        
        # Verify users created
        users = await conn.fetch("""
            SELECT u.username, u.name, u.email, r.name as role 
            FROM auth_service.users u
            JOIN auth_service.roles r ON u.role_id = r.id
            ORDER BY u.username
        """)
        print(f"✓ Users created: {len(users)}")
        for user in users:
            print(f"  - {user['username']} ({user['email']}) - {user['role']}")
        
        await conn.close()
        
        print("\n" + "=" * 60)
        print("✓ IAM INITIALIZATION COMPLETE")
        print("=" * 60)
        print("\nCredentials:")
        print("  avicoiot@gmail.com / avi123 (super_admin)")
        print("  admin@company.com / admin123 (super_admin)")
        print("  user@company.com / user123 (viewer)")
        print("\nIAM Features:")
        print("  - Roles with MCP access control")
        print("  - Teams support")
        print("  - User overrides")
        print("  - Tool-level restrictions")
        print()
        
        return 0
        
    except Exception as e:
        print(f"\n✗ ERROR: {e}")
        if conn:
            await conn.close()
        return 1

if __name__ == "__main__":
    exit(asyncio.run(main()))
