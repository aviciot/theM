#!/bin/bash
# Check rate limit Redis keys and quota status

TENANT_ID="ad7d567d-b222-40be-acb6-7929cf6ae168"  # payops_ai
MINUTE_BUCKET=$(date +%s | awk '{print int($1/60)}')

echo "=========================================="
echo "Rate Limit Status Check"
echo "=========================================="
echo ""

echo "1. Tenant Quota Configuration:"
echo "---"
docker exec them-postgres psql -U them -d them -c "
SELECT
    t.slug as tenant,
    q.plan,
    q.api_requests_per_minute,
    q.monthly_llm_tokens,
    q.runs_per_minute,
    q.max_concurrent_runs
FROM them.tenant_quotas q
JOIN tenants t ON t.id = q.tenant_id
WHERE t.slug = 'payops_ai';
" 2>/dev/null

echo ""
echo "2. Current API Rate Limit Counter:"
echo "---"
API_KEY="rl:them:${TENANT_ID}:api:${MINUTE_BUCKET}"
echo "Key: $API_KEY"
docker exec them-redis redis-cli GET "$API_KEY" 2>/dev/null | \
  awk '{if($1=="") print "Count: 0 (no requests yet)"; else print "Count: " $1}'

docker exec them-redis redis-cli TTL "$API_KEY" 2>/dev/null | \
  awk '{if($1=="-2") print "TTL: Key does not exist"; else print "TTL: " $1 " seconds"}'

echo ""
echo "3. Current Month Token Usage:"
echo "---"
docker exec them-postgres psql -U them -d them -c "
SELECT
    COALESCE(SUM(total_tokens_in + total_tokens_out), 0) as tokens_used,
    50000 as token_limit,
    CASE
        WHEN COALESCE(SUM(total_tokens_in + total_tokens_out), 0) >= 50000
        THEN 'EXCEEDED'
        ELSE 'OK'
    END as status
FROM them.runs
WHERE completed_at >= date_trunc('month', NOW())
  AND completed_at IS NOT NULL;
" 2>/dev/null

echo ""
echo "4. Recent Gateway Requests (last 10):"
echo "---"
docker logs them-go-bridge 2>&1 | grep -i "gateway\|quota\|429" | tail -10

echo ""
echo "=========================================="
echo "To test rate limiting:"
echo "  1. Go to http://localhost:3210/scenarios"
echo "  2. Run scenario: '05 — Gateway Rate Limit'"
echo "  3. Watch for 429 errors after 10 requests"
echo "=========================================="
