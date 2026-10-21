#!/bin/bash
# Generate test gateway requests for event delivery testing

set -e

TOKEN=$(grep THE_M_DEMO_APP_TOKEN /opt/docker/them/.env | cut -d= -f2)

echo "Sending 100 test requests..."

for i in $(seq 1 100); do
  curl -s -X POST http://localhost:8088/apps/demo/llm/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{\"model\":\"claude-sonnet-4-5\",\"messages\":[{\"role\":\"user\",\"content\":\"test $i\"}],\"max_tokens\":5}" \
    -o /dev/null &

  if [ $((i % 20)) -eq 0 ]; then
    wait
    echo "  Sent $i requests..."
  fi
done

wait
echo "All 100 requests sent"
