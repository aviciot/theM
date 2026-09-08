-- Migration 084: end-user history ownership + backend token flag
-- Phase 1 of END_USER_AUTH_PLAN.md
--
-- 1. them.tasks.external_user_id  — server-set from validated JWT sub or trusted
--    backend-asserted X-External-User header. Used to scope history reads so
--    user A cannot access user B's conversation history.
--
-- 2. them.access_tokens.is_backend — distinguishes tokens created for server-side
--    backend callers (trusted to assert X-External-User) from tokens issued to
--    mobile/browser clients (never trusted to assert identity).

ALTER TABLE them.tasks
    ADD COLUMN IF NOT EXISTS external_user_id TEXT;

CREATE INDEX IF NOT EXISTS idx_tasks_external_user
    ON them.tasks(external_user_id)
    WHERE external_user_id IS NOT NULL;

ALTER TABLE them.access_tokens
    ADD COLUMN IF NOT EXISTS is_backend BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN them.tasks.external_user_id IS
    'End-user identity asserted by the tenant backend. Set server-side from JWKS JWT sub '
    'or from X-External-User header (only when token.is_backend=true). Never caller-supplied '
    'from non-backend tokens.';

COMMENT ON COLUMN them.access_tokens.is_backend IS
    'When true, the bearer of this token is a trusted server-side backend and may assert '
    'end-user identity via the X-External-User header. Must not be set on tokens issued '
    'to mobile or browser clients.';
