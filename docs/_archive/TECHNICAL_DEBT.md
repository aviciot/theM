# Technical Debt & Future Improvements

This document tracks code that works correctly but could be improved for better scalability, maintainability, robustness, or performance. Items here are NOT bugs—they are opportunities for refactoring when time allows.

**Rule**: Any time you encounter code that could be done better (especially for robustness/performance), add it here with context and proposed solution.

---

## High Priority (Scalability / Robustness)

### LLM Gateway: Hardcoded Provider Registry
**File**: `go/internal/llmgateway/service.go` (lines 496-519, 551-560)  
**Added**: 2026-10-16  
**Impact**: Medium - Adding new LLM providers requires code changes in 3 locations

**Current State**:
- `modelToProvider()` uses hardcoded switch statement to map model prefixes to provider names
- Provider factory uses hardcoded switch to instantiate providers
- API key requirements hardcoded in ResolverFactory.Build

**Problem**:
- Not scalable - each new provider needs changes in 3 places
- Error-prone - easy to forget one location
- Can't add providers dynamically (e.g., from DB config)

**Proposed Solution**:
```go
// Centralized provider registry
type ProviderConfig struct {
    Name          string
    ModelPrefixes []string
    NeedsAPIKey   bool
    Factory       func(resolved Resolved, model string, maxTokens int) llm.Provider
}

var providerRegistry = []ProviderConfig{
    {
        Name: "mock",
        ModelPrefixes: []string{"mock-"},
        NeedsAPIKey: false,
        Factory: func(r Resolved, model string, max int) llm.Provider {
            return llm.NewMockProvider(nil)
        },
    },
    {
        Name: "anthropic",
        ModelPrefixes: []string{"claude-"},
        NeedsAPIKey: true,
        Factory: func(r Resolved, model string, max int) llm.Provider {
            return llm.NewAnthropicProvider(r.Key, model, max)
        },
    },
    // ... other providers
}

func modelToProvider(model string) string {
    for _, p := range providerRegistry {
        for _, prefix := range p.ModelPrefixes {
            if strings.HasPrefix(model, prefix) {
                return p.Name
            }
        }
    }
    return ""
}
```

**Effort**: ~2 hours  
**Benefit**: Future provider additions become single-entry changes

---

## Medium Priority (Performance)

### Test Runner Frontend: API Route Proxy Inefficiency
**File**: `test-runner/frontend/app/api/backend/[...path]/route.ts`  
**Added**: 2026-10-16  
**Impact**: Low - Minor latency on every API call

**Current State**:
- Next.js API route proxies all requests to backend
- Reads full request body, re-serializes, forwards
- Adds ~5-15ms latency per request

**Problem**:
- Unnecessary middleware hop for same-network containers
- Could directly call backend from frontend (both in Docker network)

**Proposed Solution**:
1. Environment-based: Dev mode uses direct backend URL, prod uses proxy
2. Or: Use Next.js `rewrites` in `next.config.ts` (already configured but not working in dev mode)
3. Or: Accept the tradeoff - proxy is simple and works

**Effort**: ~1 hour (if using rewrites), ~3 hours (if env-based switching)  
**Benefit**: Faster API responses, simpler architecture

---

## Low Priority (Code Quality)

### Next.js Config: `allowedDevOrigins` Hardcoded IP
**File**: `test-runner/frontend/next.config.ts`  
**Added**: 2026-10-16  
**Impact**: Very Low - Works but not portable

**Current State**:
```ts
allowedDevOrigins: ['10.55.125.43'],
```

**Problem**:
- Hardcoded developer's external IP
- Won't work for other developers without editing config
- HMR (hot module reload) fails from different IPs

**Proposed Solution**:
```ts
allowedDevOrigins: process.env.ALLOWED_DEV_IPS?.split(',') || [],
```

Then set in `.env.local`:
```
ALLOWED_DEV_IPS=10.55.125.43,192.168.1.100
```

**Effort**: 15 minutes  
**Benefit**: Multi-developer friendly

---

## Monitoring / Observability

### LLM Gateway: No Request Tracing
**File**: `go/internal/llmgateway/handler.go`, `service.go`  
**Added**: 2026-10-16  
**Impact**: Medium - Hard to debug production issues

**Current State**:
- Policy enforcement logged
- Provider calls logged
- But no request ID linking logs together

**Problem**:
- Can't trace a single request through policy → provider → response
- Hard to correlate errors with specific requests in production
- Multiple concurrent requests create log soup

**Proposed Solution**:
1. Add request ID middleware (chi middleware)
2. Thread context with request ID through all layers
3. Include `request_id` in every log statement

**Effort**: ~2 hours  
**Benefit**: Production debugging becomes 10x easier

---

## Template for New Entries

```markdown
### [Component]: [Brief Description]
**File**: `path/to/file.ext` (line numbers if applicable)  
**Added**: YYYY-MM-DD  
**Impact**: High/Medium/Low - [Why this matters]

**Current State**:
[What the code does now]

**Problem**:
[Why it could be better - focus on robustness/performance/scalability]

**Proposed Solution**:
[Concrete approach with code example if helpful]

**Effort**: [Time estimate]  
**Benefit**: [What improves]
```

---

## Resolved Items

### ~~Test Runner: Scenarios Page Stuck Loading~~
**Resolved**: 2026-10-16  
**Fix**: Removed `/api` prefix from proxy rewrite path  
**Files**: `test-runner/frontend/app/api/backend/[...path]/route.ts`

