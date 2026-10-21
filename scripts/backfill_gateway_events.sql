-- Backfill missing gateway_request events from Postgres to ClickHouse
-- Run: psql -U them -d them -f backfill_gateway_events.sql > backfill_data.csv
-- Then: clickhouse-client --query "INSERT INTO them_events.events_gateway_request FORMAT CSV" < backfill_data.csv

-- Generate CSV rows matching ClickHouse schema
COPY (
  SELECT
    id::text as event_id,
    1 as schema_version,
    created_at as ts,
    tenant_id::text,
    '00000000-0000-0000-0000-000000000000' as application_id,  -- Default/unknown
    '00000000-0000-0000-0000-000000000000' as client_id,       -- Default/unknown
    '' as user_id,                                             -- Empty string for missing
    id::text as request_id,                                    -- Use PG ID as request_id
    '' as trace_id,
    provider,
    model_served as model_requested,
    model_served,
    status,
    CASE
      WHEN status = 'ok' THEN 200
      WHEN status = 'error' THEN 500
      ELSE 0
    END as http_status,
    '' as error_code,
    tokens_in,
    tokens_out,
    cost_usd,
    0 as latency_ms,  -- Not tracked in PG schema
    0 as ttfb_ms      -- Not tracked in PG schema
  FROM gateway_requests
  WHERE created_at < '2026-10-20 18:00:00'  -- Only backfill old records, not the recent working set
  ORDER BY created_at ASC
) TO STDOUT WITH CSV;
