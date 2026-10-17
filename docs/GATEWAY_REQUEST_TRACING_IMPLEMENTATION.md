# LLM Gateway Request Tracing - Implementation Steps

**Status**: IN PROGRESS (Phase 1 started)  
**Estimated Remaining**: 2.5 hours  
**Date**: 2026-10-16

---

## ✅ Completed (Phases 1-2 - 1.5 hours)

### Files Modified
1. **`go/internal/llmgateway/handler.go`**:
   - ✅ Phase 1: Added `middleware` import
   - ✅ Phase 1: Added request ID extraction/generation at handler entry
   - ✅ Phase 1: Created `writeGatewayErrorWithID()` function
   - ✅ Phase 1: Added `RequestID` field to `gatewayErrorBody` struct
   - ✅ Phase 1: Started using `logger` with request_id + tenant_id + client_id
   - ✅ Phase 2: Updated `dispatchAppFlowSync` signature to accept logger
   - ✅ Phase 2: Updated `dispatchAppFlowStream` signature to accept logger
   - ✅ Phase 2: Updated `handleSync` signature to accept logger
   - ✅ Phase 2: Updated `handleStream` signature to accept logger
   - ✅ Phase 2: All `writeGatewayError` calls replaced with `writeGatewayErrorWithID`
   - ✅ Phase 2: All `h.log` calls replaced with `logger`
   - ✅ Phase 2: Added request complete logs in defer blocks

---

## 🚧 TODO (Remaining Work)

### Phase 3: Update Service Layer (1 hour)

**File**: `go/internal/llmgateway/handler.go`

Replace ALL `writeGatewayError()` calls with `writeGatewayErrorWithID()`:
- Line 118: tenant not found
- Line 122: tenant mismatch
- Line 130: cannot decode body
- Line 134: model required
- Line 183: profile workflow start failed
- Line 192: profile workflow error
- Line 198: profile workflow bad status
- Line 238: profile workflow start failed (stream)
- Line 247: profile workflow error (stream)
- Line 253: profile workflow bad status (stream)
- Line 390: provider error (non-stream)
- Line 437: provider error (stream)

Replace ALL `h.log` calls with `logger`:
- Line 175: write_request (appflow sync) failed
- Line 230: write_request (appflow stream) failed
- Line 381: write_request failed
- Line 428: write_request (stream) failed

**Add request start/complete logs**:
```go
// At handler entry (after logger creation)
logger.Info("llmgateway: request started", "model", req.Model, "stream", req.Stream)

// At handler exit (before return, both success and error paths)
logger.Info("llmgateway: request complete", 
    "status", rec.HTTPStatus, 
    "latency_ms", time.Since(start).Milliseconds(),
    "tokens_in", rec.TokensIn,
    "tokens_out", rec.TokensOut)
```

---

### Phase 3: Update Service Layer (1 hour)

**File**: `go/internal/llmgateway/service.go`

#### 3.1 Update `checkPolicy` function (~30 min)

```go
func (s *Service) checkPolicy(ctx context.Context, tenantID, clientID, requestedModel string) (resolvedModel string, tokenCap int, err error) {
    // Extract request_id from context
    requestID, _ := ctx.Value(middleware.RequestIDKey).(string)
    
    // Create logger with request_id + client_id
    logger := s.log.With("request_id", requestID, "client_id", clientID)
    
    if s.policy == nil {
        return requestedModel, 0, nil
    }
    
    pol, lerr := s.policy.LoadPolicy(ctx, tenantID, clientID)
    if lerr != nil {
        if logger != nil {
            logger.Warn("llmgateway: policy load error — fail-open", "err", lerr)
        }
        return requestedModel, 0, nil
    }
    
    if pol == nil {
        if logger != nil {
            logger.Info("llmgateway: no policy profile assigned — allow all", "model", requestedModel)
        }
        return requestedModel, 0, nil
    }
    
    // ... rest of function, replace all s.log with logger
}
```

#### 3.2 Update `Call` function (~30 min)

```go
func (s *Service) Call(ctx context.Context, tenantID, clientID string, req ChatRequest) (string, CallResult, error) {
    requestID, _ := ctx.Value(middleware.RequestIDKey).(string)
    logger := s.log.With("request_id", requestID, "client_id", clientID)
    
    // Add logs at key points:
    logger.Info("llmgateway: provider call started", "provider", provName, "model", resolvedModel)
    
    // ... LLM call
    
    logger.Info("llmgateway: provider call complete", 
        "provider", provName, 
        "tokens_in", usage.InputTokens, 
        "tokens_out", usage.OutputTokens,
        "cost_usd", cr.CostUSD)
    
    return sb.String(), cr, nil
}
```

#### 3.3 Update `Stream` function

Similar changes to `Call` - add request_id to logger and key lifecycle logs.

---

### Phase 4: Register Middleware (15 minutes)

**File**: `go/cmd/them/main.go` (or wherever gateway routes are registered)

Find where gateway handler is mounted and add middleware:

```go
import "github.com/go-chi/chi/v5/middleware"

// In route setup:
r.Use(middleware.RequestID) // Add this BEFORE gateway routes

// Mount gateway handler
r.Mount("/{tenant_slug}/llm/v1", gwHandler.Routes())
```

**Alternative** (if middleware can't be added globally):

In `handler.go` `Routes()` function:
```go
func (h *Handler) Routes() http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.RequestID) // Add middleware to gateway sub-router
    r.Post("/{tenant_slug}/llm/v1/chat/completions", h.chatCompletions)
    return r
}
```

---

### Phase 5: Testing (15 minutes)

1. **Build and restart**:
```bash
cd /opt/docker/them
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge
```

2. **Run single request test**:
```bash
# Send request with custom X-Request-ID header
curl -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
  -H "Authorization: Bearer 172d93d74fa98d757593321dc45973f92f691e6d5a68a9f785cf1070eae2ea2b" \
  -H "X-Request-ID: test-trace-123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "fast",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

3. **Verify logs**:
```bash
docker logs them-go-bridge | grep "test-trace-123"
```

**Expected log sequence**:
```
INFO llmgateway: request started request_id=test-trace-123 tenant_id=... client_id=... model=fast
INFO llmgateway: model alias resolved request_id=test-trace-123 client_id=... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete request_id=test-trace-123 client_id=... resolved_model=mock-model-v1 token_cap=500
INFO llmgateway: provider call started request_id=test-trace-123 provider=mock model=mock-model-v1
INFO llmgateway: provider call complete request_id=test-trace-123 provider=mock tokens_in=5 tokens_out=20
INFO llmgateway: request complete request_id=test-trace-123 status=200 latency_ms=15 tokens_in=5 tokens_out=20
```

4. **Test error path** (blocked model):
```bash
curl -X POST http://localhost:8088/payops_ai/llm/v1/chat/completions \
  -H "Authorization: Bearer 172d93d74fa98d757593321dc45973f92f691e6d5a68a9f785cf1070eae2ea2b" \
  -H "X-Request-ID: test-blocked-456" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

**Expected**:
- HTTP 403 response with `"request_id": "test-blocked-456"` in error payload
- `X-Request-ID: test-blocked-456` header in response
- Logs show: `WARN llmgateway: model blocked by policy request_id=test-blocked-456`

5. **Run Test #1 again** (50 users):
- All logs should have unique request_ids
- Can trace individual requests through lifecycle
- Request IDs in error responses

---

## 🎯 Success Criteria

✅ **Functional**:
- [ ] Every request has unique request_id
- [ ] Request ID in all gateway logs (handler + service)
- [ ] Request ID in error response payload
- [ ] X-Request-ID header in responses
- [ ] Can trace single request end-to-end via `grep request_id=XXX`

✅ **Multi-Tenant**:
- [ ] Logs include tenant_id + client_id + request_id
- [ ] Different tenants' requests have different request_ids
- [ ] Can filter by tenant: `grep 'tenant_id=XXX request_id=YYY'`

✅ **Performance**:
- [ ] No measurable latency increase (< 0.1ms overhead)
- [ ] Request ID generation is fast (uses chi's built-in)

---

## 📝 Notes

### Multi-Tenant Considerations
- Every log includes `tenant_id` for isolation
- Can filter tenant-specific logs: `grep 'tenant_id=ad7d567d'`
- Request IDs are globally unique (not tenant-scoped)
- Tenant admins can only see their tenant's request_ids via filtered logs

### Request ID Format
Using `chi/middleware.RequestID`:
- Format: `{hostname}-{pid}/{random-string}` (e.g., `go-bridge-1/abc123`)
- Globally unique across replicas (includes hostname)
- Accepts `X-Request-ID` header from client for distributed tracing
- Generates new ID if header missing

### Why Chi Middleware
- Battle-tested, standard implementation
- Handles edge cases (duplicate headers, etc.)
- Compatible with distributed tracing tools
- Zero dependencies beyond chi (already in use)

---

## 🔗 Related Files

- **Handler**: `go/internal/llmgateway/handler.go`
- **Service**: `go/internal/llmgateway/service.go`
- **Main**: `go/cmd/them/main.go` (middleware registration)
- **Plan Doc**: `docs/GATEWAY_REQUEST_TRACING_PLAN.md` (original design)
- **Testing Doc**: `docs/GATEWAY_POLICY_TESTING.md` (test results)

---

## ⏭️ Next After This

After request tracing is complete and tested:
1. **Cache Implementation** (2 hours) - see `GATEWAY_POLICY_CACHE_PLAN.md`
2. **Test #3** (10 min) - 200-user stress test
3. **Tests #4-5** (20 min) - Budget + isolation

**Total Remaining to Production**: ~2.5 hours (tracing) + 2 hours (cache) + 30 min (testing) = **5 hours**

