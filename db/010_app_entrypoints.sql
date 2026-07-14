-- Phase 2: split applications into parent (applications) + child (entry_points)
-- Idempotent: safe to re-run.
-- Apply:
--   docker cp db/010_app_entrypoints.sql them-postgres:/tmp/them_010_app_entrypoints.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_010_app_entrypoints.sql

BEGIN;

-- 1. Child table: entry_points. Slug UNIQUE lives here now.
CREATE TABLE IF NOT EXISTS them.entry_points (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id           UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    slug                     TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_-]{1,64}$'),
    entry_point_type         TEXT NOT NULL CHECK (entry_point_type IN ('websocket','sse','webrtc')),
    access_policy            JSONB NOT NULL DEFAULT '{"mode":"token"}',
    conversation_token_limit INTEGER,
    enabled                  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_entry_points_application_id ON them.entry_points(application_id);
CREATE INDEX IF NOT EXISTS idx_entry_points_slug           ON them.entry_points(slug);

-- 2. Backfill: one child row per existing application row (1:1).
--    Guarded so re-running does not duplicate.
INSERT INTO them.entry_points
    (application_id, slug, entry_point_type, access_policy,
     conversation_token_limit, enabled, created_at, updated_at)
SELECT
    a.id,
    a.slug,
    COALESCE(a.entry_point_type, 'websocket'),
    COALESCE(a.access_policy, '{"mode":"token"}'),
    a.conversation_token_limit,
    a.enabled,
    a.created_at,
    a.updated_at
FROM them.applications a
WHERE a.slug IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM them.entry_points e WHERE e.application_id = a.id
  );

-- 3. Drop child-shaped columns from applications (now in entry_points).
ALTER TABLE them.applications DROP CONSTRAINT IF EXISTS applications_slug_key;
ALTER TABLE them.applications DROP CONSTRAINT IF EXISTS applications_slug_check;
ALTER TABLE them.applications DROP CONSTRAINT IF EXISTS applications_entry_point_type_check;
-- Also handle the unnamed CHECK constraints Postgres generates
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'them.applications'::regclass
          AND contype = 'c'
          AND conname LIKE '%entry_point%'
    LOOP
        EXECUTE 'ALTER TABLE them.applications DROP CONSTRAINT IF EXISTS ' || quote_ident(r.conname);
    END LOOP;
END;
$$;

ALTER TABLE them.applications DROP COLUMN IF EXISTS slug;
ALTER TABLE them.applications DROP COLUMN IF EXISTS entry_point_type;
ALTER TABLE them.applications DROP COLUMN IF EXISTS access_policy;
ALTER TABLE them.applications DROP COLUMN IF EXISTS conversation_token_limit;

-- 4. Add entry_point_slug to runs for log/history traceability.
ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS entry_point_slug TEXT;
CREATE INDEX IF NOT EXISTS idx_runs_entry_point_slug ON them.runs(entry_point_slug);

COMMIT;
