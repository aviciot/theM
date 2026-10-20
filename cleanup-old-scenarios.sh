#!/bin/bash
cd /opt/docker/them/test-runner/data/scenarios

echo "=== Deleting old/duplicate scenarios ==="

OLD_FILES=(
  "7835db70-3b23-4be9-a089-fe7b4beff6cf.json"
  "gw-profile-basic-multiturn.json"
  "gw-profile-basic-stream.json"
  "gw-profile-pii-guardrail.json"
  "gw-profile-pii-guardrail-stream.json"
  "gw-profile-response-sanitizer.json"
  "payops-debator-basic.json"
  "perf-test-1b-25users.json"
  "perf-test-1c-50users.json"
  "perf-test-1d-100users.json"
  "perf-test-1e-200users.json"
  "perf-test-2a-stress-test.json"
  "perf-test-2b-single-message.json"
  "perf-test-2c-long-conversation.json"
  "perf-test-3a-temporal-50users.json"
  "perf-test-3b-temporal-100users.json"
  "test-policy-mock.json"
)

for file in "${OLD_FILES[@]}"; do
  if [ -f "$file" ]; then
    rm "$file" && echo "✓ Deleted: $file"
  else
    echo "✗ Not found: $file"
  fi
done

echo ""
echo "=== Remaining scenarios (should be 01-30 only) ==="
ls -1 *.json | sort

echo ""
echo "=== Restarting backend to reload ==="
docker restart them-test-runner-backend

echo "Done!"
