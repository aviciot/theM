# Gateway Implementation - Handover for Next Session

**Date**: 2026-10-18  
**Status**: Implementation complete, debugging auth issue  
**Time Spent**: ~8 hours across 3 phases

---

## Quick Context

We successfully implemented all 3 phases of LLM Gateway policy enforcement and token tracking:

1. ✅ **Phase 1**: Client validation (clients must have policy)
2. ✅ **Phase 2**: Policy override (policy model overrides app LLM nodes)
3. ✅ **Phase 3**: Token tracking (tokens flow from provider → workflow → gateway → DB)

**All code builds and runs** but we hit an **auth issue during testing**.

---

## Current Problem

**Test requests are getting 401 Unauthorized** when they should succeed.

### What We Know

1. **Client setup is correct:**
   ```sql
   -- Client ID: e6ffeeeb-5d23-4724-a4a3-bb7716e3e68e
   -- Token: test-gateway-token-123
   -- Token hash: 799846fa95d95a259d33a56e0da4961bff927f48c8ed0c9737ed45e55e38286c
   -- Policy: ID 2 (mock policy with mock-model-v1)
   -- App: payops-response-sanitizer (has gateway entry point)
   -- config_status: valid
   ```

2. **Test request:**
   ```bash
   curl -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
     -H "Authorization: Bearer test-gateway-token-123" \
     -H "Content-Type: application/json" \
     -d '{"model":"fast","messages":[{"role":"user","content":"Test"}]}'
   
   # Returns: 401 {"error":{"message":"invalid or unknown gateway token"}}
   ```

3. **Logs show:**
   ```
   2026/10/18 11:56:57 [...] "POST .../chat/completions" - 401 78B in 4ms
   ```
   - No middleware logs from our `GatewayClientMiddleware`
   - JWT middleware is enabled (might be interfering?)

4. **Debug logging added but not showing:**
   - Added logs to `dispatchAppFlowSync()` to trace policy loading
   - Added logs to show tokens from workflow
   - **None of these logs appear** - suggests request never reaches handler

### Root Cause Hypothesis

**The middleware chain is wrong.** Gateway routes should use `GatewayClientMiddleware` but may be using JWT middleware instead.

---

## Files Changed (Not Yet Committed)

### Schema
- `db/010_gateway_client_validation.sql` - Client validation (Phase 1)

### Core Implementation
- `go/internal/llmgateway/dal.go` - `ErrInvalidClientConfig`, validation in `LookupClientTenant`
- `go/internal/llmgateway/middleware.go` - Catch validation errors, return 403
- `go/internal/llmgateway/handler.go` - Policy loading, model/provider recording, **DEBUG LOGGING**
- `go/internal/llmgateway/gateway_test.go` - Updated fake
- `go/internal/appflow/activities.go` - Updated `InlineLLMCaller` interface, activity output
- `go/internal/appflow/workflow.go` - `PolicyLLMOverride`, token accumulation
- `go/internal/agentgen/interpreter.go` - Updated `LLMProvider` interface

### Provider Adapters (Token Return)
- `go/cmd/dag-worker/main.go` - 3 adapters + `dbLLMCaller` + `completeWithHistory` + router call
- `go/cmd/agent-runtime/llm.go` - 3 adapters (duplicates)

### Tests (26 files total)
- 4 test mock files updated
- All builds pass ✅

### Documentation
- `docs/GATEWAY_POLICY_OVERRIDE_TEST.md` - Test plan
- `docs/GATEWAY_ALL_PHASES_COMPLETE.md` - Full implementation summary

---

## Immediate Next Steps

### 1. Fix Middleware Chain (15 minutes)

**Check route registration:**
```bash
grep -A 20 "llm/v1/chat/completions" go/cmd/them/main.go
```

**Expected:** Gateway routes should use `llmgateway.GatewayClientMiddleware(dalWriter)`

**NOT:** JWT middleware or bearer token middleware

**Files to check:**
- `go/cmd/them/main.go` - Route registration
- `go/internal/server/server.go` - Middleware setup

**Likely fix:** Ensure gateway routes are in a separate route group with only `GatewayClientMiddleware`, not JWT.

### 2. Test Again (5 minutes)

Once middleware is fixed:
```bash
# Test request (script already exists)
/tmp/test_gateway_request.sh

# Check logs (should now see DEBUG logs)
docker logs them-go-bridge --tail 50 | grep -E "DEBUG|policy|workflow output"
```

### 3. Verify End-to-End (10 minutes)

**Check database after successful request:**
```sql
SELECT 
  created_at::time,
  model_requested,
  model_served,      -- Should be: mock-model-v1
  provider,          -- Should be: mock
  tokens_in,         -- Should be: 10
  tokens_out,        -- Should be: 25
  status
FROM them.gateway_requests
WHERE client_id = 'e6ffeeeb-5d23-4724-a4a3-bb7716e3e68e'
ORDER BY created_at DESC
LIMIT 1;
```

**Expected values:**
- `model_requested`: `fast`
- `model_served`: `mock-model-v1` (policy override worked!)
- `provider`: `mock`
- `tokens_in`: `10` (from mock provider)
- `tokens_out`: `25` (from mock provider)
- `status`: `ok`

### 4. Remove Debug Logging (5 minutes)

Once verified working, remove DEBUG logs from `handler.go`:
- Line ~201: "DEBUG: dispatchAppFlowSync called"
- Line ~208: "llmgateway: policy loaded" / "policy is nil"
- Line ~247: "llmgateway: workflow output received"
- Line ~261: "llmgateway: recorded model/provider" / "policy is nil"

Keep the WARN logs (they're useful).

### 5. Commit Everything (10 minutes)

```bash
git add db/010_gateway_client_validation.sql
git add go/internal/llmgateway/
git add go/internal/appflow/
git add go/internal/agentgen/
git add go/cmd/dag-worker/
git add go/cmd/agent-runtime/
git add docs/GATEWAY_*.md

git commit -m "feat(gateway): implement policy override and token tracking

Phase 1: Client validation
- Clients without policy rejected with HTTP 403
- Auto-validation trigger on gateway_clients

Phase 2: Policy override (Mode A - policy always wins)
- Gateway policy overrides ALL LLM nodes in profile apps
- Model, provider, max_tokens enforced from policy
- PolicyLLMOverride threaded through workflow

Phase 3: Token tracking
- Tokens flow: provider → activity → workflow → gateway → DB
- Updated InlineLLMCaller interface to return (text, tokensIn, tokensOut, error)
- Updated all provider adapters (6 adapters across 2 files)
- Workflow accumulates tokens from all LLM nodes
- Gateway saves tokens to gateway_requests table

Files changed: 1 migration + 13 Go files + 4 test files + 2 docs

Ready for budget enforcement testing (Test #4).

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Test Scenario (Once Auth Fixed)

**Setup:**
- Client: `e6ffeeeb-5d23-4724-a4a3-bb7716e3e68e`
- Policy: Mock policy (model: `mock-model-v1`, alias: `fast` → `mock-model-v1`)
- App: `payops-response-sanitizer` with LLM node (originally configured for `claude-sonnet-4.5`)
- Token: `test-gateway-token-123`

**Expected behavior:**
1. ✅ Client validation passes (has policy)
2. ✅ Policy override applies: LLM node uses `mock-model-v1` instead of `claude-sonnet-4.5`
3. ✅ Tokens returned: `tokens_in=10, tokens_out=25`
4. ✅ Database records: `model_served=mock-model-v1, provider=mock`

---

## Key Implementation Details

### Policy Override Logic

**Location:** `go/internal/appflow/workflow.go` lines ~793-825

```go
// Mode A: Policy Override (LLM Gateway enforcement)
if input.PolicyOverrideLLM != nil {
    provider = input.PolicyOverrideLLM.Provider
    model = input.PolicyOverrideLLM.Model
    if input.PolicyOverrideLLM.MaxTokens > 0 {
        maxTokens = input.PolicyOverrideLLM.MaxTokens
    }
}
```

### Token Flow

1. **Provider** (`anthropicAdapter.Complete`) extracts from `StreamEvent.Usage` on "stop" event
2. **Activity** (`InlineLLMActivity`) returns `InlineLLMActivityOutput{TokensIn, TokensOut}`
3. **Workflow** accumulates: `totalTokensIn += llmOut.TokensIn`
4. **Workflow** returns: `AppFlowWorkflowOutput{TokensIn, TokensOut}`
5. **Gateway** extracts: `rec.TokensIn = output.TokensIn`

### Model/Provider Recording

**Location:** `go/internal/llmgateway/handler.go` lines ~246-267

```go
if policy != nil {
    resolved := req.Model
    if policy.ModelAliases != nil {
        if aliasModel, ok := policy.ModelAliases[req.Model]; ok {
            resolved = aliasModel
        }
    }
    rec.ModelServed = resolved
    rec.Provider = modelToProvider(resolved)
}
```

---

## Reference Commands

**Rebuild after changes:**
```bash
cd /opt/docker/them
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  build them-go-bridge them-dag-worker them-dag-worker-2

# IMPORTANT: Use stop + up, NOT restart (restart doesn't pick up new image)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  stop them-go-bridge them-dag-worker them-dag-worker-2
  
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  up -d them-go-bridge them-dag-worker them-dag-worker-2
```

**Check logs:**
```bash
# Gateway
docker logs them-go-bridge --tail 50 | grep llmgateway

# Workers
docker logs them-dag-worker --tail 30 | grep inline

# Database
docker exec them-postgres psql -U them -d them -c "
  SELECT * FROM them.gateway_requests 
  WHERE client_id = 'e6ffeeeb-5d23-4724-a4a3-bb7716e3e68e' 
  ORDER BY created_at DESC LIMIT 3;"
```

---

## Paste This Prompt to Continue

```
Continue debugging the LLM Gateway implementation from HANDOVER_GATEWAY_DEBUG.md.

Current issue: Test requests getting 401 Unauthorized, likely due to middleware chain 
using JWT middleware instead of GatewayClientMiddleware for gateway routes.

Fix the middleware chain in go/cmd/them/main.go, rebuild/restart services, then verify:
1. Auth works (request succeeds)
2. DEBUG logs appear (policy loaded, tokens received)
3. Database shows model_served=mock-model-v1, provider=mock, tokens_in=10, tokens_out=25

Once verified working, remove DEBUG logs and commit all Phase 1-3 changes.
```

---

## Documentation Already Written

- ✅ `docs/GATEWAY_STATUS.md` - Updated with all 3 phases complete
- ✅ `docs/GATEWAY_ALL_PHASES_COMPLETE.md` - Full summary
- ✅ `docs/GATEWAY_POLICY_OVERRIDE_TEST.md` - Test plan
- ✅ `docs/TEST3_RESULTS.md` - Cache/tracing test results (from before)

---

## Success Criteria (When Complete)

- [ ] Auth works (401 fixed)
- [ ] Request succeeds (200 OK)
- [ ] `model_served` = `mock-model-v1` (policy override worked)
- [ ] `provider` = `mock`
- [ ] `tokens_in` = `10`, `tokens_out` = `25` (token tracking worked)
- [ ] All 3 phases committed to git
- [ ] Ready for Test #4 (budget enforcement)

**Estimated time to complete:** 45 minutes
