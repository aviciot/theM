#!/bin/bash
# Test poison event isolation — prove malformed events go to DLQ without blocking valid events
# Sends: valid → malformed → valid
# Expected: 2 delivered to CH, 1 in DLQ

set -e

echo "=== Poison Event Isolation Test ==="
echo ""

# Get Redis password
REDIS_PASS=$(grep THE_M_EVENTS_REDIS_PASSWORD /opt/docker/them/.env | cut -d= -f2)

# Helper to send event to Redis Streams
send_event() {
    local event_json="$1"
    local description="$2"

    echo "Sending: $description"
    docker exec them-redis-events redis-cli XADD them:events "*" event "$event_json" 2>&1 | head -1
}

# Test start timestamp
TEST_START=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "Test start: $TEST_START"
echo ""

# Event 1: Valid gateway request
EVENT1_ID=$(uuidgen)
EVENT1_REQ_ID=$(uuidgen)
VALID_EVENT_1=$(cat <<EOF
{
  "event_id": "$EVENT1_ID",
  "kind": "gateway_request",
  "tenant_id": "11111111-1111-1111-1111-111111111111",
  "timestamp": "$TEST_START",
  "gateway_request": {
    "request_id": "$EVENT1_REQ_ID",
    "application_id": "22222222-2222-2222-2222-222222222222",
    "client_id": "test-client",
    "user_id": "test-user",
    "provider": "mock",
    "model_requested": "test-model",
    "model_served": "poison-isolation-test-v1",
    "status": "ok",
    "latency_ms": 10,
    "tokens_in": 5,
    "tokens_out": 10,
    "cost_usd": 0.0001
  }
}
EOF
)

send_event "$VALID_EVENT_1" "Event 1 (valid)"

# Event 2: Malformed (missing GatewayRequest payload)
EVENT2_ID=$(uuidgen)
MALFORMED_EVENT=$(cat <<EOF
{
  "event_id": "$EVENT2_ID",
  "kind": "gateway_request",
  "tenant_id": "11111111-1111-1111-1111-111111111111",
  "timestamp": "$TEST_START"
}
EOF
)

send_event "$MALFORMED_EVENT" "Event 2 (malformed — missing gateway_request payload)"

# Event 3: Valid gateway request
EVENT3_ID=$(uuidgen)
EVENT3_REQ_ID=$(uuidgen)
VALID_EVENT_3=$(cat <<EOF
{
  "event_id": "$EVENT3_ID",
  "kind": "gateway_request",
  "tenant_id": "11111111-1111-1111-1111-111111111111",
  "timestamp": "$TEST_START",
  "gateway_request": {
    "request_id": "$EVENT3_REQ_ID",
    "application_id": "22222222-2222-2222-2222-222222222222",
    "client_id": "test-client",
    "user_id": "test-user",
    "provider": "mock",
    "model_requested": "test-model",
    "model_served": "poison-isolation-test-v1",
    "status": "ok",
    "latency_ms": 12,
    "tokens_in": 6,
    "tokens_out": 11,
    "cost_usd": 0.00012
  }
}
EOF
)

send_event "$VALID_EVENT_3" "Event 3 (valid)"

echo ""
echo "⏳ Waiting 10 seconds for processing..."
sleep 10

# Verify results
echo ""
echo "📊 Verification:"
echo ""

# Count in ClickHouse
CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
  SELECT COUNT(*)
  FROM events_gateway_request
  WHERE model_served = 'poison-isolation-test-v1';
")

echo "  ClickHouse events: $CH_COUNT (expected: 2)"

# Check DLQ
DLQ_COUNT=$(docker exec them-redis-events redis-cli XLEN them:events:dlq)
echo "  DLQ count: $DLQ_COUNT (expected: ≥1)"

# Check Redis lag
REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1 || echo "0")
REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1 || echo "0")

echo "  Redis lag: $REDIS_LAG (expected: 0)"
echo "  Redis pending: $REDIS_PENDING (expected: 0)"

# Verify specific request IDs in ClickHouse
echo ""
echo "🔬 Field verification:"
CH_EVENT1=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
  SELECT request_id
  FROM events_gateway_request
  WHERE request_id = '$EVENT1_REQ_ID';
" 2>&1 | xargs)

CH_EVENT3=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
  SELECT request_id
  FROM events_gateway_request
  WHERE request_id = '$EVENT3_REQ_ID';
" 2>&1 | xargs)

if [ "$CH_EVENT1" == "$EVENT1_REQ_ID" ]; then
    echo "  ✅ Event 1 delivered (request_id: $EVENT1_REQ_ID)"
else
    echo "  ❌ Event 1 NOT found (request_id: $EVENT1_REQ_ID)"
fi

if [ "$CH_EVENT3" == "$EVENT3_REQ_ID" ]; then
    echo "  ✅ Event 3 delivered (request_id: $EVENT3_REQ_ID)"
else
    echo "  ❌ Event 3 NOT found (request_id: $EVENT3_REQ_ID)"
fi

# Check event-writer logs for poison handling
echo ""
echo "🔍 Event-writer logs (last 20 lines):"
docker logs them-event-writer --since 15s | tail -20

echo ""
echo "=========================================="

# Final verdict
if [ "$CH_COUNT" -eq 2 ] && [ "$DLQ_COUNT" -ge 1 ] && [ "$REDIS_LAG" -eq 0 ]; then
    echo "✅ TEST PASSED"
    echo "  • 2 valid events delivered to ClickHouse"
    echo "  • Malformed event moved to DLQ"
    echo "  • No blocking occurred (lag=0)"
    exit 0
else
    echo "❌ TEST FAILED"
    echo "  • CH count: $CH_COUNT (expected: 2)"
    echo "  • DLQ count: $DLQ_COUNT (expected: ≥1)"
    echo "  • Redis lag: $REDIS_LAG (expected: 0)"
    exit 1
fi
