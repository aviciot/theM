#!/bin/bash
# Test auto-generated UUID request ID

echo "=== Test 1: No X-Request-ID header (should auto-generate UUID) ==="
curl -s -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
  -H "Authorization: Bearer 172d93d74fa98d757593321dc45973f92f691e6d5a68a9f785cf1070eae2ea2b" \
  -H "Content-Type: application/json" \
  -d '{"model":"fast","messages":[{"role":"user","content":"test auto UUID"}]}' | jq -r '.id'

echo ""
echo "=== Checking logs for auto-generated UUID format ==="
sleep 1
docker logs them-go-bridge --since 5s | grep "llmgateway: request started" | tail -1
