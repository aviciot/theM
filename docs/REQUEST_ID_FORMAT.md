# Request ID Format

## Overview

Every request through the LLM Gateway gets a unique **request ID** for tracing.

---

## Format

### Auto-Generated (No X-Request-ID Header)

**Format**: `hostname/random-counter`

**Example**: `05b0c2bced18/FbQtga9N0t-000018`

**Breakdown**:
- `05b0c2bced18` = Container hostname (first 12 chars of full hostname)
- `FbQtga9N0t` = Base62-encoded random string (unique per process)
- `000018` = Atomic request counter (increments per request)

**Properties**:
- ✅ **Globally unique** across all replicas (includes hostname)
- ✅ **Sortable** by counter within same process/replica
- ✅ **Compact** (27 characters vs UUID's 36)
- ✅ **Standard** (chi middleware v5 default)

---

### Client-Provided (X-Request-ID Header)

**Format**: Whatever the client sends

**Example**: `mobile-app-user123-req456`

The gateway accepts any string format via the `X-Request-ID` header.

**Best Practice**:
```bash
# Good - unique per request
X-Request-ID: mobile-app-${userId}-${timestamp}-${random}

# Bad - reused across requests
X-Request-ID: my-app-request
```

---

## Implementation

Powered by chi middleware `RequestID`:
- Registered globally in `internal/server/server.go:80`
- Accepts `X-Request-ID` header from clients
- Auto-generates if header missing
- Stored in context under `middleware.RequestIDKey`

**Code Location**: `go/internal/llmgateway/handler.go:87-97`

---

## Usage Examples

### 1. Trace Single Request

```bash
# User sends request with custom ID
curl -H "X-Request-ID: debug-request-001" http://...

# Filter logs
docker logs them-go-bridge | grep "debug-request-001"
```

**Output**:
```json
{"msg":"llmgateway: request started","request_id":"debug-request-001"}
{"msg":"llmgateway: policy check complete","request_id":"debug-request-001"}
{"msg":"llmgateway: provider call started","request_id":"debug-request-001"}
{"msg":"llmgateway: request complete","request_id":"debug-request-001","status":200}
```

---

### 2. Find Auto-Generated Requests

```bash
# Auto-generated format has "/" in it
docker logs them-go-bridge | grep 'request_id="[^"]*/'
```

---

### 3. Error Response Includes Request ID

When a request fails, the error response includes the ID:

```json
{
  "error": {
    "type": "model_not_permitted",
    "message": "llmgateway: model not permitted by policy",
    "request_id": "05b0c2bced18/FbQtga9N0t-000018"
  }
}
```

Users can send this ID to support for instant diagnosis.

---

## Comparison: Auto-Generated vs UUID

| Feature | Chi Middleware Format | UUID v4 |
|---|---|---|
| **Example** | `05b0c2bced18/FbQtga9N0t-000018` | `550e8400-e29b-41d4-a716-446655440000` |
| **Length** | 27 characters | 36 characters |
| **Globally Unique** | ✅ (includes hostname) | ✅ |
| **Sortable** | ✅ (by counter) | ❌ (random) |
| **Predictable** | ⚠️ Partially (counter increments) | ❌ Fully random |
| **Collision Risk** | Near-zero (hostname + random + counter) | Near-zero (128-bit random) |

**Decision**: Use chi middleware format (shorter, sortable, standard).

---

## Security Note

**Counter Predictability**: The request counter increments sequentially (`000018`, `000019`, etc).

**Is this a problem?**
- ❌ **Not a security risk** for our use case
- The counter is per-process, not global
- Combined with hostname + random string = still globally unique
- Request IDs are logged, not used for authentication/authorization

**If concerned about predictability**, clients can provide their own random IDs via `X-Request-ID`.

---

## Best Practices

### For API Users

✅ **Do**: Generate unique ID per request
```javascript
const requestId = `${appName}-${userId}-${Date.now()}-${Math.random().toString(36)}`;
```

❌ **Don't**: Reuse same ID across multiple requests
```javascript
const requestId = "my-app-request"; // All requests have same ID!
```

### For Debugging

1. **User reports error** → ask for `request_id` from error response
2. **Grep logs**: `docker logs | grep request_id=XXX`
3. **See full trace** in chronological order

### For Monitoring

Filter logs by pattern:
```bash
# All auto-generated IDs
grep 'request_id="[^"]*/'

# All custom IDs (no slash)
grep 'request_id="[^"]*"' | grep -v '/'

# Specific tenant's requests
grep 'tenant_id=ad7d567d'

# Failed requests
grep 'status=403\|status=429\|status=500'
```

---

## Related Docs

- **Implementation**: `docs/GATEWAY_REQUEST_TRACING_IMPLEMENTATION.md`
- **Testing**: `docs/GATEWAY_POLICY_TESTING.md`
- **Status**: `docs/GATEWAY_STATUS.md`
