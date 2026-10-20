#!/bin/bash
# Run all 4 logging performance tests

cd /opt/docker/them

echo "Running logging performance tests..."
echo "======================================"

# Admin JWT
TOKEN=$(curl -s -X POST http://localhost:8701/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])" 2>/dev/null)

# Test 40 (minimal)
echo ""
echo "TEST 40 — MINIMAL logging..."
RUN_ID_40=$(curl -s -X POST "http://localhost:8093/run" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"scenario_id":"40-logging-perf-minimal"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))" 2>/dev/null)
echo "Run ID: $RUN_ID_40"
sleep 15
RESULT_40=$(curl -s "http://localhost:8093/history/$RUN_ID_40" -H "Authorization: Bearer $TOKEN")
echo "$RESULT_40" | python3 -c "
import sys, json
data = json.load(sys.stdin)
total = data.get('total_messages', 0)
passed = data.get('passed', 0)
avg_latency = data.get('avg_latency_ms', 0)
print(f'Result: {passed}/{total} passed, Avg latency: {avg_latency}ms')
"

# Test 41 (metrics)
echo ""
echo "TEST 41 — METRICS logging..."
RUN_ID_41=$(curl -s -X POST "http://localhost:8093/run" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"scenario_id":"41-logging-perf-metrics"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))" 2>/dev/null)
echo "Run ID: $RUN_ID_41"
sleep 15
RESULT_41=$(curl -s "http://localhost:8093/history/$RUN_ID_41" -H "Authorization: Bearer $TOKEN")
echo "$RESULT_41" | python3 -c "
import sys, json
data = json.load(sys.stdin)
total = data.get('total_messages', 0)
passed = data.get('passed', 0)
avg_latency = data.get('avg_latency_ms', 0)
print(f'Result: {passed}/{total} passed, Avg latency: {avg_latency}ms')
"

# Test 42 (debug)
echo ""
echo "TEST 42 — DEBUG logging..."
RUN_ID_42=$(curl -s -X POST "http://localhost:8093/run" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"scenario_id":"42-logging-perf-debug"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))" 2>/dev/null)
echo "Run ID: $RUN_ID_42"
sleep 15
RESULT_42=$(curl -s "http://localhost:8093/history/$RUN_ID_42" -H "Authorization: Bearer $TOKEN")
echo "$RESULT_42" | python3 -c "
import sys, json
data = json.load(sys.stdin)
total = data.get('total_messages', 0)
passed = data.get('passed', 0)
avg_latency = data.get('avg_latency_ms', 0)
print(f'Result: {passed}/{total} passed, Avg latency: {avg_latency}ms')
"

# Test 43 (audit)
echo ""
echo "TEST 43 — AUDIT logging..."
RUN_ID_43=$(curl -s -X POST "http://localhost:8093/run" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"scenario_id":"43-logging-perf-audit"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))" 2>/dev/null)
echo "Run ID: $RUN_ID_43"
sleep 15
RESULT_43=$(curl -s "http://localhost:8093/history/$RUN_ID_43" -H "Authorization: Bearer $TOKEN")
echo "$RESULT_43" | python3 -c "
import sys, json
data = json.load(sys.stdin)
total = data.get('total_messages', 0)
passed = data.get('passed', 0)
avg_latency = data.get('avg_latency_ms', 0)
print(f'Result: {passed}/{total} passed, Avg latency: {avg_latency}ms')
"

echo ""
echo "======================================"
echo "All 4 tests complete!"
echo "Run IDs:"
echo "  40 (minimal): $RUN_ID_40"
echo "  41 (metrics): $RUN_ID_41"
echo "  42 (debug):   $RUN_ID_42"
echo "  43 (audit):   $RUN_ID_43"
