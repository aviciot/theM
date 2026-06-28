-- AUTH SERVICE SCHEMA - SINGLE SOURCE OF TRUTH
-- This file defines the complete database schema
-- ALL code must align with this schema
-- Version: 2.0 (IAM with role_id FK)

-- ============================================================
-- ROLES TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.roles (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) UNIQUE NOT NULL,
    description TEXT,
    
    -- IAM Controls (NO generic permissions column)
    mcp_access TEXT[] DEFAULT '{}',                    -- Which MCPs: ['*'] or ['database_mcp', 'macgyver_mcp']
    tool_restrictions JSONB DEFAULT '{}',              -- Per-MCP tool restrictions: {"database_mcp": ["analyze_query"]}
    dashboard_access VARCHAR(20) DEFAULT 'none',       -- 'admin', 'view', 'none'
    
    -- Rate Limiting
    rate_limit INTEGER DEFAULT 1000,                   -- Requests per hour
    cost_limit_daily DECIMAL(10,2) DEFAULT 100.00,     -- $ per day
    token_expiry INTEGER DEFAULT 3600,                 -- Seconds
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- USERS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE,
    
    -- Authentication
    role_id INTEGER REFERENCES auth_service.roles(id),  -- FK to roles (NOT VARCHAR!)
    password_hash VARCHAR(255),
    api_key_hash VARCHAR(255) UNIQUE,
    
    -- Status
    active BOOLEAN DEFAULT true NOT NULL,
    rate_limit_override INTEGER,                        -- Override role rate limit
    
    last_login_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_role_id ON auth_service.users(role_id);
CREATE INDEX IF NOT EXISTS idx_users_email ON auth_service.users(email);

-- ============================================================
-- TEAMS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.teams (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    
    -- Team-level restrictions
    mcp_access TEXT[] DEFAULT '{}',
    resource_access JSONB DEFAULT '{}',
    team_rate_limit INTEGER,
    team_cost_limit DECIMAL(10,2),
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- TEAM MEMBERS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.team_members (
    team_id INTEGER REFERENCES auth_service.teams(id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES auth_service.users(id) ON DELETE CASCADE,
    role VARCHAR(50) DEFAULT 'member',
    joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (team_id, user_id)
);

-- ============================================================
-- USER OVERRIDES TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.user_overrides (
    user_id INTEGER PRIMARY KEY REFERENCES auth_service.users(id) ON DELETE CASCADE,
    mcp_restrictions TEXT[] DEFAULT '{}',              -- Remove specific MCPs
    tool_restrictions JSONB DEFAULT '{}',              -- Remove specific tools
    custom_rate_limit INTEGER,
    custom_cost_limit DECIMAL(10,2),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- AUDIT LOG TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.auth_audit (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES auth_service.users(id) ON DELETE SET NULL,
    username VARCHAR(255),
    action VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL,
    ip_address VARCHAR(45),
    user_agent TEXT,
    details TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_auth_audit_user_id ON auth_service.auth_audit(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_audit_created_at ON auth_service.auth_audit(created_at);

-- ============================================================
-- USER SESSIONS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.user_sessions (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES auth_service.users(id) ON DELETE CASCADE,
    access_token_hash VARCHAR(255) UNIQUE NOT NULL,
    refresh_token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON auth_service.user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON auth_service.user_sessions(expires_at);

-- ============================================================
-- BLACKLISTED TOKENS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_service.blacklisted_tokens (
    token_hash VARCHAR(255) PRIMARY KEY,
    blacklisted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_blacklisted_tokens_expires_at ON auth_service.blacklisted_tokens(expires_at);

-- ============================================================
-- DEFAULT DATA
-- ============================================================

-- Default Roles
INSERT INTO auth_service.roles (name, description, mcp_access, tool_restrictions, dashboard_access, rate_limit, cost_limit_daily, token_expiry) VALUES
('super_admin', 'Full system access', ARRAY['*'], '{}', 'admin', 10000, 1000.00, 7200),
('developer', 'Developer access', ARRAY['database_mcp', 'macgyver_mcp', 'informatica_mcp'], '{"database_mcp": ["analyze_full_sql_context", "compare_query_plans"], "macgyver_mcp": ["*"], "informatica_mcp": ["*"]}', 'view', 5000, 100.00, 7200),
('analyst', 'Data analyst access', ARRAY['database_mcp'], '{"database_mcp": ["analyze_full_sql_context", "get_top_queries"]}', 'view', 1000, 50.00, 3600),
('viewer', 'Read-only access', ARRAY[], '{}', 'view', 100, 10.00, 3600)
ON CONFLICT (name) DO NOTHING;

-- ============================================================
-- CRITICAL RULES
-- ============================================================
-- 1. NO 'permissions' column anywhere
-- 2. users.role_id is INTEGER FK, NOT VARCHAR
-- 3. All IAM queries MUST JOIN roles table: SELECT r.name as role FROM users u JOIN roles r ON u.role_id = r.id
-- 4. tool_restrictions is JSONB, may need JSON parsing in code
-- 5. Any schema change MUST update: SCHEMA.sql → init_complete.py → test_full.py
