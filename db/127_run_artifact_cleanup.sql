-- db/127_run_artifact_cleanup.sql
-- Deleting a run must not leave its artifacts, approvals or stored files behind (backlog F-125, plan P2).
--
-- Before: run_artifacts and hil_approvals had NO foreign key to runs, so deleting a run (admin delete,
-- bulk delete, tenant delete) orphaned them, and the MinIO objects named by run_artifacts.storage_key
-- were never removed (nothing knew about them once the row was gone).
--
-- Deliberately NOT changed: tasks.run_id stays ON DELETE SET NULL. tasks + task_messages ARE the
-- conversation history (history.LoadHistory reads them by context_id), so deleting a run record must
-- not erase the user's chat memory.
--
-- 1. storage_deletions (outbox) + trigger: every deleted run_artifacts row that points at a stored file
--    records the key; the middleware worker's janitor deletes the object and then the outbox row.
--    A trigger catches every delete path (cascade, direct, tenant delete, this migration's own cleanup).
-- 2. remove existing orphans (after the trigger exists, so their files are queued too)
-- 3. foreign keys with ON DELETE CASCADE (NOT VALID, then VALIDATE; run_id indexes already exist)

-- ── 1. outbox ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS them.storage_deletions (
    id          BIGSERIAL   PRIMARY KEY,
    tenant_id   UUID,
    bucket      TEXT        NOT NULL CHECK (bucket IN ('artifacts')),
    storage_key TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts    INTEGER     NOT NULL DEFAULT 0,
    last_error  TEXT
);

CREATE INDEX IF NOT EXISTS idx_storage_deletions_pending ON them.storage_deletions (attempts, id);

ALTER TABLE them.storage_deletions OWNER TO them_owner;

-- Read/written only by the janitor through the admin pool (them_admin, BYPASSRLS). them_app has no
-- access at all: rows are inserted by the SECURITY DEFINER trigger function below (owned by the
-- superuser, which bypasses RLS), so RLS+FORCE with no them_app policy is defense-in-depth.
GRANT SELECT, UPDATE, DELETE ON them.storage_deletions TO them_admin;
GRANT USAGE, SELECT ON SEQUENCE them.storage_deletions_id_seq TO them_admin;
ALTER TABLE them.storage_deletions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.storage_deletions FORCE ROW LEVEL SECURITY;

CREATE OR REPLACE FUNCTION them.enqueue_artifact_object_deletion() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = them, pg_temp AS $$
BEGIN
    IF OLD.storage_key IS NOT NULL AND OLD.storage_key <> '' THEN
        INSERT INTO them.storage_deletions (tenant_id, bucket, storage_key)
        VALUES (OLD.tenant_id, 'artifacts', OLD.storage_key);
    END IF;
    RETURN OLD;
END
$$;

DROP TRIGGER IF EXISTS run_artifacts_enqueue_object_deletion ON them.run_artifacts;
CREATE TRIGGER run_artifacts_enqueue_object_deletion
    BEFORE DELETE ON them.run_artifacts
    FOR EACH ROW EXECUTE FUNCTION them.enqueue_artifact_object_deletion();

-- ── 2. existing orphans ────────────────────────────────────────────────────────
DELETE FROM them.run_artifacts a
 WHERE a.run_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM them.runs r WHERE r.id = a.run_id);

DELETE FROM them.hil_approvals h
 WHERE NOT EXISTS (SELECT 1 FROM them.runs r WHERE r.id = h.run_id);

-- ── 3. foreign keys ────────────────────────────────────────────────────────────
ALTER TABLE them.run_artifacts
    ADD CONSTRAINT run_artifacts_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES them.runs (id) ON DELETE CASCADE NOT VALID;
ALTER TABLE them.run_artifacts VALIDATE CONSTRAINT run_artifacts_run_id_fkey;

ALTER TABLE them.hil_approvals
    ADD CONSTRAINT hil_approvals_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES them.runs (id) ON DELETE CASCADE NOT VALID;
ALTER TABLE them.hil_approvals VALIDATE CONSTRAINT hil_approvals_run_id_fkey;

-- ── Record migration ───────────────────────────────────────────────────────────
INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('127_run_artifact_cleanup', 'run_artifacts/hil_approvals cascade with runs; storage_deletions outbox for stored files', NOW())
ON CONFLICT DO NOTHING;
