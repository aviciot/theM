# Gateway Policy Override Test Plan

**Date**: 2026-10-18  
**Feature**: Mode A - Policy Always Wins (Phase 2)

---

## What We Implemented

**Phase 1: Client Validation** ✅
- Added `config_status` validation to `gateway_clients`
- Clients without policy are rejected with HTTP 403
- Trigger auto-validates on INSERT/UPDATE

**Phase 2: Policy Override for Profile Apps** ✅
- Added `PolicyOverrideLLM` to `AppFlowWorkflowInput`
- Gateway passes policy model/provider to workflow
- Workflow overrides ALL LLM nodes with policy values
- MaxTokens also enforced from policy

---

## Test Scenario

### Setup

**Gateway Client:**
- Has profile app assigned (App Canvas workflow)
- Has policy profile assigned (required by Phase 1)

**Profile App "Smart Translator":**
- Entry point: `gateway`
- Has 1 LLM node configured with:
  - Provider: `anthropic`
  - Model: `claude-sonnet-4.5`
  - Max tokens: 2000

**Policy Profile "Mock Policy":**
- Model: `mock-model-v1` (NOT claude-sonnet!)
- Provider: `mock`
- Max tokens per request: 500

### Expected Behavior (Mode A)

**When client sends request:**
```bash
POST /{tenant}/llm/v1/chat/completions
Authorization: Bearer {client_token}
{
  "model": "fast",  // alias in policy → mock-model-v1
  "messages": [{"role": "user", "content": "Hello"}]
}
```

**Gateway should:**
1. ✅ Load policy profile for client
2. ✅ Resolve alias: `fast` → `mock-model-v1`
3. ✅ Start AppFlow workflow with `PolicyOverrideLLM`:
   - Provider: `mock`
   - Model: `mock-model-v1`
   - MaxTokens: 500
4. ✅ Workflow ignores app's LLM node config (`claude-sonnet-4.5`)
5. ✅ Workflow uses policy config (`mock-model-v1`)
6. ✅ Response comes from mock provider

**Verification:**
- Check logs: should show `provider=mock model=mock-model-v1`
- Response should be instant (mock provider, not real Claude API)
- `gateway_requests` should show `model_served=mock-model-v1`

---

## Test Steps

### 1. Verify Database State

```sql
-- Check client has both policy AND app
SELECT 
  id::text as client_id,
  policy_profile_id,
  app_id IS NOT NULL as has_app,
  config_status
FROM them.gateway_clients
WHERE policy_profile_id IS NOT NULL 
  AND app_id IS NOT NULL
LIMIT 1;
```

Expected: At least 1 row with `config_status='valid'`

### 2. Check Policy Profile

```sql
-- Check policy has mock model
SELECT 
  id,
  allowed_models,
  model_aliases,
  max_tokens_per_request
FROM them.gateway_policy_profiles
WHERE id = (SELECT policy_profile_id FROM them.gateway_clients WHERE config_status='valid' LIMIT 1);
```

Expected: `allowed_models` includes `mock-model-v1`, `max_tokens_per_request=500`

### 3. Send Test Request

```bash
# Get valid client token hash
CLIENT_TOKEN_HASH=$(docker exec them-postgres psql -U them -d them -t -c "
  SELECT token_hash 
  FROM them.gateway_clients 
  WHERE config_status='valid' 
    AND app_id IS NOT NULL 
    AND policy_profile_id IS NOT NULL 
  LIMIT 1
" | xargs)

# Note: We don't have the original token, only the hash
# For testing, we need to either:
# 1. Create a new test client with known token
# 2. Use test-runner with gateway client support
```

### 4. Check Logs

```bash
docker logs them-go-bridge --tail 50 | grep -E "policy|mock|override"
```

Expected logs:
- `"policy_override_llm":{"provider":"mock","model":"mock-model-v1","max_tokens":500}`
- `provider=mock model=mock-model-v1`

### 5. Verify in Database

```sql
-- Check gateway_requests for profile app call
SELECT 
  created_at,
  model_requested,
  model_served,
  provider,
  tokens_in,
  tokens_out,
  status
FROM them.gateway_requests
WHERE client_id = (
  SELECT id FROM them.gateway_clients 
  WHERE config_status='valid' 
    AND app_id IS NOT NULL 
  LIMIT 1
)
ORDER BY created_at DESC
LIMIT 1;
```

Expected:
- `model_requested`: whatever client sent (e.g., `"fast"`)
- `model_served`: `mock-model-v1` (policy override worked!)
- `provider`: `mock`
- `status`: `ok`

---

## Success Criteria

✅ **Phase 1 (Validation):**
- [ ] Clients without policy get HTTP 403
- [ ] Error message is clear: "No policy profile assigned..."

✅ **Phase 2 (Override):**
- [ ] Gateway passes `PolicyOverrideLLM` to workflow
- [ ] Workflow uses policy model (not app's LLM node model)
- [ ] Workflow uses policy max_tokens (not app's LLM node max_tokens)
- [ ] Response comes from policy's provider
- [ ] Logs show policy override in effect

---

## Next: Phase 3 - Token Tracking

After Phase 2 passes, implement token return from workflow:

1. Update `AppFlowWorkflowOutput` to include tokens
2. Update `InlineLLMActivityOutput` to return tokens
3. Update `InlineLLMCaller` interface to return tokens
4. Thread tokens through 5 layers
5. Gateway saves tokens to `gateway_requests`

**Estimated time:** 2-3 hours

---

## Files Changed (Phase 2)

**Schema:**
- `db/010_gateway_client_validation.sql` (Phase 1)

**Go Code:**
- `go/internal/llmgateway/dal.go` - Added `ErrInvalidClientConfig`, updated `LookupClientTenant`
- `go/internal/llmgateway/middleware.go` - Catch invalid client config, return 403
- `go/internal/llmgateway/handler.go` - Pass policy to `admitAndStartAppFlow`, create `PolicyOverrideLLM`
- `go/internal/appflow/workflow.go` - Added `PolicyLLMOverride` struct, apply override in LLM node execution

**Total:** 1 migration + 4 Go files

---

## Rollback Plan

If Phase 2 causes issues:

1. **Rollback Go code:** `git revert {commit_hash}`
2. **Rebuild:** `docker compose build them-go-bridge`
3. **Restart:** `docker compose restart them-go-bridge`
4. **Rollback DB:** `ALTER TABLE them.gateway_clients DROP COLUMN config_status, DROP COLUMN config_status_reason;`

Profile apps will still work - they just won't have policy enforcement (old behavior).
