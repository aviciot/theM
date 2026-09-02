-- 056_tenant_quotas.sql
-- Creates them.tenant_quotas: per-tenant resource caps and rate limits.
-- NULL = no limit enforced (unlimited). platform defaults: trial plan.

CREATE TABLE them.tenant_quotas (
    tenant_id               UUID PRIMARY KEY REFERENCES them.tenants(id) ON DELETE CASCADE,
    plan                    TEXT NOT NULL DEFAULT 'trial'
                              CHECK (plan IN ('trial', 'starter', 'pro', 'enterprise')),

    -- Hard resource limits (NULL = unlimited)
    max_agents              INTEGER,
    max_apps                INTEGER,
    max_mcp_servers         INTEGER,
    max_concurrent_runs     INTEGER,
    max_users               INTEGER,

    -- Usage limits (per calendar month, rolling; NULL = unlimited)
    monthly_llm_tokens      BIGINT,
    monthly_runs            INTEGER,

    -- Rate limits (per minute; NULL = unlimited)
    api_requests_per_minute INTEGER,
    runs_per_minute         INTEGER,

    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Default quotas for the bootstrap tenant (plan = trial)
INSERT INTO them.tenant_quotas (tenant_id, plan)
VALUES ('00000000-0000-0000-0000-000000000001', 'enterprise')
ON CONFLICT (tenant_id) DO NOTHING;
