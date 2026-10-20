#!/bin/bash
# Run all test scenarios and collect results

BACKEND="http://localhost:8093"

run_test() {
  local scenario_id=$1
  local scenario_name=$2

  echo "=========================================="
  echo "Running: $scenario_name"
  echo "=========================================="

  # Start the test
  local run_id=$(docker exec them-test-runner-backend wget -qO- \
    --post-data="{\"scenario_id\":\"$scenario_id\",\"n_users\":0}" \
    --header="Content-Type: application/json" \
    http://localhost:8090/run | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])")

  echo "Run ID: $run_id"

  # Wait for completion (max 60s)
  for i in {1..60}; do
    sleep 1
    local status=$(docker exec them-test-runner-backend wget -qO- \
      http://localhost:8090/run/$run_id | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('ended_at','running'))")

    if [ "$status" != "running" ]; then
      echo "✓ Test completed"
      break
    fi

    if [ $i -eq 60 ]; then
      echo "✗ Test timeout"
      return 1
    fi
  done

  # Get results
  docker exec them-test-runner-backend wget -qO- \
    http://localhost:8090/run/$run_id | python3 -c "
import sys, json
r = json.load(sys.stdin)
passed = r.get('passed', 0)
failed = r.get('failed', 0)
total = r.get('n_users', 0)
success_rate = (passed / total * 100) if total > 0 else 0

# Calculate avg latency
if r.get('results'):
    latencies = []
    for ur in r['results']:
        if ur.get('steps'):
            for step in ur['steps']:
                if step.get('latency_ms'):
                    latencies.append(step['latency_ms'])
    avg_latency = sum(latencies) / len(latencies) if latencies else 0
else:
    avg_latency = 0

print(f'Passed: {passed}/{total} ({success_rate:.1f}%)')
print(f'Failed: {failed}')
print(f'Avg Latency: {avg_latency:.0f}ms')
"

  echo ""
}

# Run tests
run_test "01-gateway-basic-mock" "01 — Gateway Basic (Mock Provider)"
run_test "02-gateway-streaming" "02 — Gateway Streaming"
run_test "10-perf-50-users-direct" "10 — Performance: 50 Users (Direct Path)"
run_test "20-perf-50-users-temporal" "20 — Performance: 50 Users (WITH Temporal)"

echo "=========================================="
echo "All tests complete!"
echo "=========================================="
