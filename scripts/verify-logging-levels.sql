-- Verify Logging Level Body Capture Enforcement
-- Run this after executing scenarios 40-43
-- Usage: docker exec -it them-postgres psql -U them -d them -f scripts/verify-logging-levels.sql

\echo ''
\echo '════════════════════════════════════════════════════════════'
\echo '  Logging Level Body Capture Verification'
\echo '════════════════════════════════════════════════════════════'
\echo ''

-- 1. Check governance profiles exist
\echo '1. Governance Profiles (should show 4 rows):'
\echo '───────────────────────────────────────────────────────────'
SELECT name, logging_level
FROM them.governance_policies
WHERE name LIKE 'perf-test-%'
ORDER BY name;

\echo ''

-- 2. Check gateway clients are linked
\echo '2. Gateway Clients → Profiles (should show 4 rows):'
\echo '───────────────────────────────────────────────────────────'
SELECT gc.label, gp.name as profile, gp.logging_level
FROM them.gateway_clients gc
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
ORDER BY gc.label;

\echo ''

-- 3. Count requests by logging level (all levels should have ~300 requests)
\echo '3. Request Count by Logging Level (all should have ~300):'
\echo '───────────────────────────────────────────────────────────'
SELECT
  gp.logging_level,
  COUNT(*) as request_count,
  COUNT(CASE WHEN gr.status = 'ok' THEN 1 END) as success_count,
  COUNT(CASE WHEN gr.status != 'ok' THEN 1 END) as error_count
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '2 hours'
GROUP BY gp.logging_level
ORDER BY gp.logging_level;

\echo ''

-- 4. Count bodies by logging level (ONLY debug/audit should have entries)
\echo '4. Body Capture by Logging Level (ONLY debug/audit should show):'
\echo '───────────────────────────────────────────────────────────'
SELECT
  gp.logging_level,
  COUNT(*) as body_count,
  COUNT(CASE WHEN grb.request_text IS NOT NULL THEN 1 END) as has_request,
  COUNT(CASE WHEN grb.response_text IS NOT NULL THEN 1 END) as has_response
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND grb.created_at > now() - interval '2 hours'
GROUP BY gp.logging_level
ORDER BY gp.logging_level;

\echo ''

-- 5. Verify MINIMAL/METRICS have NO bodies (should return 0)
\echo '5. Verify MINIMAL/METRICS Have NO Bodies (should be 0):'
\echo '───────────────────────────────────────────────────────────'
SELECT COUNT(*) as should_be_zero
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
WHERE gc.label IN ('perf-test-minimal-client', 'perf-test-metrics-client')
  AND grb.created_at > now() - interval '2 hours';

\echo ''

-- 6. Performance comparison
\echo '6. Latency Comparison (DEBUG/AUDIT should be slower):'
\echo '───────────────────────────────────────────────────────────'
SELECT
  gp.logging_level,
  COUNT(*) as requests,
  ROUND(AVG(gr.latency_ms)) as avg_ms,
  ROUND(MIN(gr.latency_ms)) as min_ms,
  ROUND(MAX(gr.latency_ms)) as max_ms,
  ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY gr.latency_ms)) as p50_ms,
  ROUND(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY gr.latency_ms)) as p95_ms
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '2 hours'
  AND gr.status = 'ok'
GROUP BY gp.logging_level
ORDER BY avg_ms;

\echo ''

-- 7. Calculate overhead percentage vs MINIMAL
\echo '7. Overhead vs MINIMAL Baseline:'
\echo '───────────────────────────────────────────────────────────'
WITH baseline AS (
  SELECT AVG(latency_ms) as baseline_ms
  FROM them.gateway_requests gr
  JOIN them.gateway_clients gc ON gr.client_id = gc.id
  WHERE gc.label = 'perf-test-minimal-client'
    AND gr.created_at > now() - interval '2 hours'
    AND gr.status = 'ok'
)
SELECT
  gp.logging_level,
  ROUND(AVG(gr.latency_ms)) as avg_ms,
  ROUND(baseline.baseline_ms) as baseline_ms,
  ROUND(((AVG(gr.latency_ms) - baseline.baseline_ms) / baseline.baseline_ms * 100)::numeric, 1) as overhead_pct
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
CROSS JOIN baseline
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '2 hours'
  AND gr.status = 'ok'
GROUP BY gp.logging_level, baseline.baseline_ms
ORDER BY overhead_pct;

\echo ''

-- 8. Sample body content (first DEBUG request)
\echo '8. Sample Body Content (DEBUG level, first request):'
\echo '───────────────────────────────────────────────────────────'
SELECT
  gr.id,
  gr.model_requested,
  gr.tokens_in,
  gr.tokens_out,
  gr.latency_ms,
  LENGTH(grb.request_text) as req_bytes,
  LENGTH(grb.response_text) as resp_bytes,
  LEFT(grb.request_text, 80) as req_sample
FROM them.gateway_requests gr
JOIN them.gateway_request_bodies grb ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
WHERE gc.label = 'perf-test-debug-client'
  AND gr.created_at > now() - interval '2 hours'
ORDER BY gr.created_at
LIMIT 1;

\echo ''
\echo '════════════════════════════════════════════════════════════'
\echo '  Verification Complete'
\echo '════════════════════════════════════════════════════════════'
\echo ''
\echo 'Expected Results:'
\echo '  • Sections 1-3: All 4 levels should show data'
\echo '  • Section 4: ONLY debug/audit should show (~300 rows each)'
\echo '  • Section 5: Should be 0 (no bodies for minimal/metrics)'
\echo '  • Section 6-7: DEBUG/AUDIT should be 30-60% slower than MINIMAL'
\echo '  • Section 8: Should show JSON text sample'
\echo ''
