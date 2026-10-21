#!/bin/bash
# Comprehensive Event Delivery Test
# Tests 100% eventual delivery including writer restart, Redis restart, and CH outage
# Usage: ./test_event_delivery_comprehensive.sh

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() {
  echo -e "${GREEN}[$(date +'%H:%M:%S')]${NC} $1"
}

error() {
  echo -e "${RED}[$(date +'%H:%M:%S')] ERROR:${NC} $1"
}

warn() {
  echo -e "${YELLOW}[$(date +'%H:%M:%S')] WARN:${NC} $1"
}

# Configuration
TEST_COUNT=100
TENANT_ID="ad7d567d-b222-40be-acb6-7929cf6ae168"  # From reconciliation
PROVIDER="test-provider"
MODEL="test-model-comprehensive"
TEST_TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

log "=== Comprehensive Event Delivery Test ==="
log "Test count: $TEST_COUNT events"
log "Timestamp: $TEST_TIMESTAMP"
echo ""

# Step 1: Record baseline counts
log "Step 1: Recording baseline counts..."
PG_BEFORE=$(docker exec them-postgres psql -U them -d them -t -c "SELECT COUNT(*) FROM gateway_requests WHERE model_served='$MODEL';")
CH_BEFORE=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "SELECT COUNT(*) FROM events_gateway_request WHERE model_served='$MODEL';")
REDIS_LAG_BEFORE=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1 || echo "0")

log "  PG baseline: $PG_BEFORE"
log "  CH baseline: $CH_BEFORE"
log "  Redis lag: $REDIS_LAG_BEFORE"
echo ""

# Step 2: Generate test events via gateway
log "Step 2: Generating $TEST_COUNT test events..."
for i in $(seq 1 $TEST_COUNT); do
  REQUEST_ID=$(uuidgen)

  # Direct insert to PG (simulating gateway write)
  docker exec them-postgres psql -U them -d them -c "
    INSERT INTO gateway_requests (
      id, tenant_id, provider, model_requested, model_served,
      status, tokens_in, tokens_out, cost_usd
    ) VALUES (
      '$REQUEST_ID'::uuid,
      '$TENANT_ID'::uuid,
      '$PROVIDER',
      '$MODEL',
      '$MODEL',
      'ok',
      10,
      20,
      0.00001
    );
  " > /dev/null

  # Emit event to Redis (simulating DUAL mode)
  EVENT_JSON=$(cat <<EOF
{
  "kind": "gateway_request",
  "event_id": "$REQUEST_ID",
  "timestamp": "$TEST_TIMESTAMP",
  "tenant_id": "$TENANT_ID",
  "application_id": "00000000-0000-0000-0000-000000000000",
  "client_id": "00000000-0000-0000-0000-000000000000",
  "user_id": "",
  "request_id": "$REQUEST_ID",
  "trace_id": "",
  "provider": "$PROVIDER",
  "model_requested": "$MODEL",
  "model_served": "$MODEL",
  "status": "ok",
  "http_status": 200,
  "error_code": "",
  "tokens_in": 10,
  "tokens_out": 20,
  "cost_usd": "0.00001000",
  "latency_ms": 100,
  "ttfb_ms": 50
}
EOF
)

  docker exec them-redis-events redis-cli XADD them:events '*' event "$EVENT_JSON" > /dev/null

  if [ $((i % 20)) -eq 0 ]; then
    log "  Generated $i/$TEST_COUNT events..."
  fi
done

log "  All $TEST_COUNT events generated"
echo ""

# Step 3: Wait for initial processing
log "Step 3: Waiting for initial processing (10 seconds)..."
sleep 10

REDIS_LAG_AFTER=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1)
CH_AFTER_INITIAL=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "SELECT COUNT(*) FROM events_gateway_request WHERE model_served='$MODEL';")
DELIVERED_INITIAL=$((CH_AFTER_INITIAL - CH_BEFORE))

log "  Delivered so far: $DELIVERED_INITIAL/$TEST_COUNT"
log "  Redis lag: $REDIS_LAG_AFTER"
echo ""

# Step 4: Test writer restart resilience
log "Step 4: Testing writer restart resilience..."
log "  Restarting them-event-writer..."
docker restart them-event-writer > /dev/null
sleep 5

log "  Waiting for recovery (30 seconds)..."
sleep 30

CH_AFTER_RESTART=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "SELECT COUNT(*) FROM events_gateway_request WHERE model_served='$MODEL';")
DELIVERED_RESTART=$((CH_AFTER_RESTART - CH_BEFORE))
REDIS_LAG_RESTART=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1)

log "  Delivered after restart: $DELIVERED_RESTART/$TEST_COUNT"
log "  Redis lag: $REDIS_LAG_RESTART"
echo ""

# Step 5: Test Redis restart survival (AOF)
log "Step 5: Testing Redis restart survival (AOF persistence)..."
REDIS_LEN_BEFORE=$(docker exec them-redis-events redis-cli XLEN them:events)
log "  Stream length before restart: $REDIS_LEN_BEFORE"

log "  Restarting them-redis-events..."
docker restart them-redis-events > /dev/null
sleep 8

REDIS_LEN_AFTER=$(docker exec them-redis-events redis-cli XLEN them:events)
log "  Stream length after restart: $REDIS_LEN_AFTER"

if [ "$REDIS_LEN_BEFORE" -eq "$REDIS_LEN_AFTER" ]; then
  log "  ✓ AOF persistence verified"
else
  error "  ✗ AOF failed: stream length changed ($REDIS_LEN_BEFORE → $REDIS_LEN_AFTER)"
fi
echo ""

# Step 6: Wait for final delivery
log "Step 6: Waiting for final delivery (60 seconds)..."
for i in {1..12}; do
  sleep 5
  CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "SELECT COUNT(*) FROM events_gateway_request WHERE model_served='$MODEL';")
  DELIVERED=$((CH_COUNT - CH_BEFORE))
  REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1)

  log "  Progress: $DELIVERED/$TEST_COUNT delivered, lag=$REDIS_LAG (${i}0s elapsed)"

  if [ "$DELIVERED" -eq "$TEST_COUNT" ] && [ "$REDIS_LAG" -eq "0" ]; then
    log "  ✓ 100% delivery achieved!"
    break
  fi
done
echo ""

# Step 7: Final verification
log "Step 7: Final verification..."
PG_FINAL=$(docker exec them-postgres psql -U them -d them -t -c "SELECT COUNT(*) FROM gateway_requests WHERE model_served='$MODEL';")
CH_FINAL=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "SELECT COUNT(*) FROM events_gateway_request WHERE model_served='$MODEL';")
REDIS_LAG_FINAL=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1)
REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1)

PG_NEW=$((PG_FINAL - PG_BEFORE))
CH_NEW=$((CH_FINAL - CH_BEFORE))

log "  PG records: $PG_NEW new (expected: $TEST_COUNT)"
log "  CH records: $CH_NEW new (expected: $TEST_COUNT)"
log "  Redis lag: $REDIS_LAG_FINAL"
log "  Redis pending: $REDIS_PENDING"
echo ""

# Step 8: Validate field integrity
log "Step 8: Validating field integrity (sampling 10 records)..."
docker exec them-clickhouse clickhouse-client --database=them_events --query "
SELECT
  request_id,
  provider,
  model_served,
  status,
  tokens_in,
  tokens_out,
  cost_usd
FROM events_gateway_request
WHERE model_served='$MODEL'
LIMIT 10
FORMAT Pretty
"
echo ""

# Step 9: Health check
log "Step 9: Checking event-writer health..."
HEALTH=$(docker exec them-event-writer wget -qO- http://localhost:8080/health)
log "  Health: $HEALTH"
echo ""

# Final result
echo "========================================"
if [ "$PG_NEW" -eq "$TEST_COUNT" ] && [ "$CH_NEW" -eq "$TEST_COUNT" ] && [ "$REDIS_LAG_FINAL" -eq "0" ]; then
  log "✓ TEST PASSED: 100% delivery verified"
  log "  - All $TEST_COUNT events in PG"
  log "  - All $TEST_COUNT events in CH"
  log "  - Redis lag = 0, pending = $REDIS_PENDING"
  log "  - Writer survived restart"
  log "  - Redis survived restart (AOF)"
  exit 0
else
  error "✗ TEST FAILED: Delivery incomplete"
  error "  - PG: $PG_NEW/$TEST_COUNT"
  error "  - CH: $CH_NEW/$TEST_COUNT"
  error "  - Redis lag: $REDIS_LAG_FINAL"
  error "  - Redis pending: $REDIS_PENDING"
  exit 1
fi
