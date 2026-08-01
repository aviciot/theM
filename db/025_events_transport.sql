-- Phase 11c-A: events_transport column on them.runs
-- Tracks which event delivery transport was used for each run.
-- Apply: docker exec them-postgres psql -U them -d them -f /tmp/025_events_transport.sql

ALTER TABLE them.runs
  ADD COLUMN IF NOT EXISTS events_transport TEXT NOT NULL DEFAULT 'pubsub';

ALTER TABLE them.runs
  ADD CONSTRAINT runs_events_transport_check
  CHECK (events_transport IN ('pubsub', 'streams'));

COMMENT ON COLUMN them.runs.events_transport IS
  'Event delivery transport for this run. pubsub = legacy Redis Pub/Sub (at-most-once). streams = Redis Streams with replay (Phase 11c+).';
