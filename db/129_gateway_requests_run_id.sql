-- db/129_gateway_requests_run_id.sql
-- Link a gateway call to the App Canvas run it started (backlog F-127, plan P4: the run id is the trace key).
-- Calls through a client with a profile app run an AppFlow workflow; recording its run id lets you ask
-- "which gateway calls, and what cost, belong to this run". NULL for direct (no profile app) calls.
-- No foreign key: runs are deleted by retention long before gateway history is.
ALTER TABLE them.gateway_requests ADD COLUMN IF NOT EXISTS run_id UUID;
CREATE INDEX IF NOT EXISTS idx_gateway_requests_run_id ON them.gateway_requests (run_id) WHERE run_id IS NOT NULL;
COMMENT ON COLUMN them.gateway_requests.run_id IS 'AppFlow run started for this call (profile app); NULL on the direct path';

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('129_gateway_requests_run_id', 'gateway_requests.run_id (link gateway calls to App Canvas runs)', NOW())
ON CONFLICT DO NOTHING;
