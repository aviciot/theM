#!/bin/bash
# Test request tracing with blocked model

curl -s -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
  -H "Authorization: Bearer 172d93d74fa98d757593321dc45973f92f691e6d5a68a9f785cf1070eae2ea2b" \
  -H "X-Request-ID: test-blocked-002" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "This should be blocked"}]
  }'
