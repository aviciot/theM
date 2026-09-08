-- Phase 2: runtime-only role with no dashboard access.
-- Users assigned this role can obtain a JWT via /auth/runtime-login but cannot
-- log in via /auth/login (dashboard_access='none' triggers ErrDashboardAccessDenied).
INSERT INTO auth_service.roles (name, description, dashboard_access, rate_limit, cost_limit_daily, token_expiry)
VALUES ('end_user', 'Runtime-only access, no dashboard', 'none', 1000, 10.00, 3600)
ON CONFLICT (name) DO NOTHING;
