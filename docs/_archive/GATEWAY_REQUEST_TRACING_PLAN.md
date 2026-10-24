# LLM Gateway Request Tracing - Implementation Plan

## Problem Statement

**Current State**: All logs for the same client share the same `client_id`, but there's no unique identifier per request.

**Impact**: 
- Can't trace a single request through the system (policy check → provider call → response)
- Production debugging is nearly impossible with concurrent requests
- Multi-tenant environment makes "log soup" - hundreds of requests with same client_id

**Example Current Logs**:
```
INFO llmgateway: model alias resolved client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete client_id=1023ce69... resolved_model=mock-model-v1
INFO llmgateway: model alias resolved client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete client_id=1023ce69... resolved_model=mock-model-v1
```
**Problem**: Which log pair belongs to the same request? Impossible to tell!

**Discovered**: 2026-10-16 during 50-user test analysis

---

## Solution: Request ID Middleware + Context Threading

### Architecture

```
HTTP Request
  ↓
Middleware: Generate/Extract Request ID
  ↓
Context: Store request_id
  ↓
Handler → Service → DAL
  ↓   ↓   ↓
All logs include request_id
```

### Request ID Format

**Standard**: Use `chi` middleware's built-in `RequestID` middleware (compatible with X-Request-ID header)

**Format**: `{instance_id}-{counter}` (e.g., `go-bridge-1-000567`)

**Why**: 
- Already have instance_id in logs
- Counter ensures uniqueness within instance
- External clients can pass X-Request-ID for distributed tracing

---

## Implementation

### Step 1: Add Request ID Middleware

**File**: `go/internal/llmgateway/handler.go`

```go
import (
    "github.com/go-chi/chi/v5/middleware"
)

func (h *Handler) RegisterRoutes(r chi.Router) {
    // Add request ID middleware FIRST (before any other middleware)
    r.Use(middleware.RequestID)
    
    // Existing routes
    r.Post("/{tenant_slug}/llm/v1/chat/completions", h.ChatCompletions)
}
```

### Step 2: Thread Request ID Through Context

**File**: `go/internal/llmgateway/handler.go`

```go
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
    // Extract request ID from middleware
    requestID := middleware.GetReqID(r.Context())
    
    // Add to context for downstream layers
    ctx := context.WithValue(r.Context(), "request_id", requestID)
    
    // Include in all logs from this handler
    logger := h.log.With("request_id", requestID)
    
    // ... rest of handler logic, use logger instead of h.log
    logger.Info("llmgateway: request started", "tenant", tenantSlug, "model", req.Model)
    
    // Pass ctx to service layer
    result, err := h.service.Call(ctx, tenantID, clientID, req)
}
```

### Step 3: Update Service Layer Logging

**File**: `go/internal/llmgateway/service.go`

```go
func (s *Service) checkPolicy(ctx context.Context, tenantID, clientID, requestedModel string) (resolvedModel string, tokenCap int, error) {
    // Extract request ID from context
    requestID, _ := ctx.Value("request_id").(string)
    
    // Create logger with request_id
    logger := s.log.With("request_id", requestID, "client_id", clientID)
    
    // ... policy loading
    
    // All logs include request_id
    logger.Info("llmgateway: model alias resolved", "requested", requestedModel, "resolved", resolved)
    logger.Info("llmgateway: policy check complete", "resolved_model", resolved, "token_cap", tokenCap)
    
    // ... rest of policy logic
}

func (s *Service) Call(ctx context.Context, tenantID, clientID string, req ChatRequest) (string, CallResult, error) {
    requestID, _ := ctx.Value("request_id").(string)
    logger := s.log.With("request_id", requestID, "client_id", clientID)
    
    // ... provider call
    
    logger.Info("llmgateway: provider call started", "provider", provName, "model", resolvedModel)
    // ... LLM call
    logger.Info("llmgateway: provider call complete", "provider", provName, "tokens_in", usage.InputTokens, "tokens_out", usage.OutputTokens)
    
    return sb.String(), cr, nil
}
```

### Step 4: Update DAL Layer (Optional)

**File**: `go/internal/llmgateway/dal.go`

```go
// Only add request_id to DAL logs if debugging DB queries
// Not required for normal operation

func (d *DAL) LoadPolicy(ctx context.Context, tenantID, clientID string) (*Policy, error) {
    requestID, _ := ctx.Value("request_id").(string)
    
    // Optional: log DB query with request_id
    // d.log.Debug("llmgateway: loading policy", "request_id", requestID, "client_id", clientID)
    
    // ... existing query logic
}
```

### Step 5: Include Request ID in Error Responses

**File**: `go/internal/llmgateway/handler.go`

```go
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, errType, message string) {
    requestID := middleware.GetReqID(r.Context())
    
    w.Header().Set("X-Request-ID", requestID) // Return to client for support tickets
    w.WriteHeader(status)
    
    payload := map[string]interface{}{
        "error": map[string]string{
            "type":       errType,
            "message":    message,
            "request_id": requestID, // Include in error payload
        },
    }
    json.NewEncoder(w).Encode(payload)
}
```

---

## Example: Before vs After

### Before (Current)
```
INFO llmgateway: model alias resolved client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete client_id=1023ce69... resolved_model=mock-model-v1
INFO llmgateway: model alias resolved client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete client_id=1023ce69... resolved_model=mock-model-v1
```
**Problem**: Can't tell which logs belong to the same request!

### After (With Request ID)
```
INFO llmgateway: request started request_id=go-bridge-1-000567 tenant=payops_ai model=fast
INFO llmgateway: model alias resolved request_id=go-bridge-1-000567 client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete request_id=go-bridge-1-000567 client_id=1023ce69... resolved_model=mock-model-v1 token_cap=500
INFO llmgateway: provider call started request_id=go-bridge-1-000567 provider=mock model=mock-model-v1
INFO llmgateway: provider call complete request_id=go-bridge-1-000567 provider=mock tokens_in=12 tokens_out=45
INFO llmgateway: request complete request_id=go-bridge-1-000567 status=200 latency_ms=3

INFO llmgateway: request started request_id=go-bridge-1-000568 tenant=payops_ai model=fast
INFO llmgateway: model alias resolved request_id=go-bridge-1-000568 client_id=1023ce69... requested=fast resolved=mock-model-v1
INFO llmgateway: policy check complete request_id=go-bridge-1-000568 client_id=1023ce69... resolved_model=mock-model-v1 token_cap=500
```
**Solution**: Can trace each request end-to-end! Grep for `request_id=go-bridge-1-000567` gets full request lifecycle.

---

## Log Queries (After Implementation)

### Trace a Specific Request
```bash
docker logs them-go-bridge | grep "request_id=go-bridge-1-000567"
```

### Find All Failed Requests
```bash
docker logs them-go-bridge | grep "request_id=" | grep "error\|blocked\|exceeded"
```

### Track a Client's Requests
```bash
docker logs them-go-bridge | grep "client_id=1023ce69" | grep "request_id="
```

### Find Slow Requests (>1s)
```bash
docker logs them-go-bridge | grep "request_id=" | grep "latency_ms" | awk '$NF > 1000'
```

---

## Multi-Tenant Considerations

**the-M is Multi-Tenant**: Each tenant has multiple clients, each client makes multiple requests.

**Request ID Scope**: 
- Request ID is unique across ALL tenants (generated by middleware)
- Logs include both `request_id` and `tenant_id` for full context
- Cross-tenant correlation is intentionally difficult (isolation/security)

**Example Multi-Tenant Logs**:
```
INFO llmgateway: request started request_id=go-bridge-1-000567 tenant=payops_ai client_id=abc...
INFO llmgateway: request started request_id=go-bridge-1-000568 tenant=acme_corp client_id=xyz...
INFO llmgateway: request started request_id=go-bridge-1-000569 tenant=payops_ai client_id=def...
```

**Tenant Admin View** (future feature):
- Tenant admin can filter logs by their `tenant_id`
- Can see all `request_id`s for their tenant only
- Cannot see other tenants' request IDs

---

## Distributed Tracing (Future Enhancement)

### Current Plan: Request ID Only
Simple, effective, low overhead.

### Future: OpenTelemetry Integration
When load grows to 10,000+ req/sec:
1. Replace custom request_id with OpenTelemetry trace_id
2. Add span_id for internal operations
3. Export to Jaeger/Grafana Tempo for visual traces
4. Correlate with external services (Anthropic API, etc.)

**Why Not Now**: 
- Adds complexity and overhead
- Current request_id approach is sufficient for 1000 req/sec
- Can migrate later without breaking changes (trace_id replaces request_id)

---

## Testing Plan

### Test 1: Request ID Uniqueness
**Setup**: Run 100 concurrent requests  
**Expected**: All 100 requests have unique request_ids

### Test 2: End-to-End Tracing
**Setup**: 
1. Send 1 request with `X-Request-ID: test-12345`
2. Check logs for all lifecycle events

**Expected**: 
```
request started request_id=test-12345
model alias resolved request_id=test-12345
policy check complete request_id=test-12345
provider call started request_id=test-12345
provider call complete request_id=test-12345
request complete request_id=test-12345
```

### Test 3: Error Tracing
**Setup**: Send request with blocked model (gpt-4)  
**Expected**: 
- Error response includes `request_id` in payload
- Error response has `X-Request-ID` header
- All error logs include same `request_id`

### Test 4: Performance Impact
**Setup**: Run 50-user test with/without request ID logging  
**Expected**: Latency increase < 0.5ms (negligible)

---

## Implementation Plan

### Phase 1: Basic Request ID (1 hour)
- Add `middleware.RequestID` to routes
- Update handler logs to include request_id
- Test with single request

### Phase 2: Service Layer Integration (1 hour)
- Thread request_id through context
- Update `checkPolicy` and `Call` logs
- Test with 10 concurrent requests

### Phase 3: Error Response Integration (30 min)
- Add request_id to error payloads
- Add X-Request-ID header
- Test error cases

### Phase 4: Documentation (30 min)
- Update log query examples
- Add troubleshooting guide
- Update CLAUDE.md with new log format

**Total Effort**: ~3 hours

---

## Dependencies

### Go Packages (already in use)
- `github.com/go-chi/chi/v5/middleware` ✅ (RequestID middleware)

### No Database Changes
Uses existing context/logging infrastructure.

### No Configuration Changes
Works automatically - no env vars needed.

---

## Performance Impact

**Overhead per request**:
- Generate request ID: ~0.001ms
- Context storage: ~0.001ms
- Log field addition: ~0.01ms per log line

**Total**: < 0.1ms per request (negligible)

**Benefit**: Debugging time reduction from hours → minutes

---

## Decision

**Status**: APPROVED  
**Priority**: HIGH (production blocker)  
**Implementation**: Start immediately after Test #2  
**Timeline**: Complete before Test #3 (200-user stress test)

**Why Critical**: Without request tracing, production issues are nearly impossible to debug in a multi-tenant concurrent environment. This is a hard requirement before any production deployment.

---

## Future Work

### Log Aggregation (Post-Production)
When logs grow too large for `docker logs`:
1. Forward logs to Loki/ELK/CloudWatch
2. Build dashboard with filters: tenant_id, client_id, request_id, error type
3. Alert on error rate spikes

### Request ID Propagation (Post-Production)
Pass request_id to:
- Anthropic API (via X-Request-ID header)
- Internal A2A agent calls
- Temporal workflows
→ Full distributed trace across all services

