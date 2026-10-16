# LLM Gateway Policy Profiles - Current Status

**Last Updated**: 2026-10-16  
**Phase**: Testing & Infrastructure Planning

---

## ✅ Completed

### Implementation
- [x] Policy profiles CRUD API (create, read, update, delete)
- [x] Client-to-profile assignment
- [x] Alias resolution (model name mapping)
- [x] Allowlist enforcement (allowed_models check)
- [x] Token cap per request (max_tokens_per_request)
- [x] Monthly budget tracking (monthly_budget_usd)
- [x] Mock provider integration (free testing)
- [x] Test runner dev mode (hot reload)

### Testing
- [x] **Test #1**: 50 concurrent users - PASSED ✅
  - Alias resolution: fast → mock-model-v1 ✅
  - Policy enforcement: token cap 500 ✅
  - Performance: 825ms avg, 1.39% CPU, 27MB RAM ✅
  - Zero errors, zero policy violations ✅

### Documentation
- [x] Testing report (`docs/GATEWAY_POLICY_TESTING.md`)
- [x] Cache implementation plan (`docs/GATEWAY_POLICY_CACHE_PLAN.md`)
- [x] Request tracing plan (`docs/GATEWAY_REQUEST_TRACING_PLAN.md`)
- [x] Technical debt tracking (`docs/TECHNICAL_DEBT.md`)
- [x] Architecture analysis (scaling strategy, bottleneck identification)

---

## 🔄 In Progress

### Current Task
**Test #2: Policy Rejection (Blocked Model)**
- Status: Ready to execute (2 minutes)
- Purpose: Validate allowlist enforcement blocks unauthorized models
- Setup: Change model to "gpt-4" (not in allowed_models)
- Expected: 403 error, no LLM provider called

---

## 🚧 Blocked / Critical Findings

### Production Blockers (Must Fix Before Production)

#### 1. No Policy Caching ⚠️ HIGH PRIORITY
**Problem**: Every request hits PostgreSQL for policy lookup (no caching)
- Test #1 measured: 150 requests = 150 DB queries (1:1 ratio)
- Bottleneck: DB saturates at ~300 req/sec
- CPU/Memory have plenty of headroom (not the bottleneck)

**Impact**: 
- Current capacity: ~300 req/sec
- With cache: ~5,000 req/sec (single replica)
- **99% DB load reduction** with cache

**Solution**: Two-tier cache (in-memory + Redis)
- Phase 1 (Redis): 2 hours → eliminates DB bottleneck
- Phases 2-4 (in-memory + invalidation): 4 hours → optimal performance
- **Status**: Plan complete, ready to implement after Test #2

**File**: `go/internal/llmgateway/dal.go` (LoadPolicy method)

---

#### 2. No Request Tracing ⚠️ HIGH PRIORITY
**Problem**: Can't trace individual requests through the system
- All logs share same client_id, no unique request identifier
- Multi-tenant environment creates "log soup"
- Production debugging nearly impossible with concurrent requests

**Impact**: Hours of debugging for simple issues

**Solution**: Request ID middleware + context threading
- Add `chi.middleware.RequestID`
- Thread request_id through context
- Include in all log statements
- **Status**: Plan complete, ~3 hours to implement

**File**: `go/internal/llmgateway/handler.go`, `service.go`

---

## 📋 Planned (Next Steps)

### Immediate (Next 3 Hours)
1. **Test #2**: Policy rejection (2 min) - validate unhappy path
2. **Implement Request Tracing**: (3 hours) - enables debugging Test #3

### Short Term (Next 2 Hours)
3. **Implement Cache Phase 1**: Redis cache (2 hours) - eliminate DB bottleneck

### Before Test #3 (200 Users)
4. **Test #3 Preparation**: Ensure tracing + cache working together

### Medium Term (Next 4 Hours)
5. **Cache Phases 2-4**: In-memory + invalidation + monitoring (4 hours)
6. **Test #3**: 200-user stress test with full infrastructure
7. **Test #4**: Budget enforcement validation
8. **Test #5**: Multiple policy profiles (isolation test)

**Total Time to Production-Ready**: ~10 hours from now

---

## 📊 Architecture

### Current Setup (Local Dev)
```
Client → them-traefik:8088 → them-go-bridge:8002 → [them-postgres, them-redis]
         (load balancer)       (single replica)
```

### Production Setup (Hetzner)
```
Client → them-traefik:8088 → [them-go-bridge:8002, them-go-bridge-2:8003] → [them-postgres, them-redis]
         (load balancer)       (2 replicas, ready to scale)
```

### Scaling Strategy
- **Phase 1**: Implement cache (eliminates DB bottleneck)
- **Phase 2**: Test on single replica (sufficient for 5000 req/sec)
- **Phase 3**: Add 2nd replica only if CPU > 70% (unlikely with cache)

**Key Insight**: Cache > Replication for this workload (DB-bound, not CPU-bound)

---

## 🎯 Success Criteria (Production Ready)

### Functional Requirements
- [x] Policy profiles CRUD working
- [x] Alias resolution working
- [x] Allowlist enforcement working
- [x] Token cap working
- [ ] Budget enforcement working (Test #4)
- [ ] Multi-profile isolation working (Test #5)
- [ ] Policy rejection working (Test #2 - in progress)

### Performance Requirements
- [x] 50 concurrent users: PASSED
- [ ] 200 concurrent users: Pending Test #3
- [ ] 1000 req/sec sustained: Pending cache implementation
- [ ] < 1s p95 latency: Current 825ms avg, need to measure p95

### Infrastructure Requirements
- [ ] Policy caching implemented (2-tier: in-memory + Redis)
- [ ] Request tracing implemented (request_id in all logs)
- [ ] Cache invalidation working (policy updates propagate)
- [ ] Multi-replica cache sync working (pub/sub)
- [ ] Monitoring in place (cache hit rate, error rate)

### Operational Requirements
- [ ] Production deployment tested (Hetzner stack)
- [ ] Rollback procedure documented
- [ ] Monitoring alerts configured
- [ ] Load testing complete (Tests #1-5 all passing)

---

## 📈 Metrics (From Test #1)

| Metric | Value | Target | Status |
|---|---|---|---|
| Concurrent users | 50 | 50 | ✅ Met |
| Response time (avg) | 825ms | < 1000ms | ✅ Met |
| CPU usage | 1.39% | < 70% | ✅ Met |
| Memory usage | 27MB | < 1GB | ✅ Met |
| Error rate | 0% | < 0.1% | ✅ Met |
| Policy violations | 0 | 0 | ✅ Met |
| DB queries/request | 1 | < 0.01 (with cache) | ❌ **Needs cache** |
| Cache hit rate | N/A | > 99% | ⏳ Not implemented yet |

---

## 🔗 Related Documents

- **Testing Report**: `docs/GATEWAY_POLICY_TESTING.md`
- **Cache Plan**: `docs/GATEWAY_POLICY_CACHE_PLAN.md`
- **Tracing Plan**: `docs/GATEWAY_REQUEST_TRACING_PLAN.md`
- **Technical Debt**: `docs/TECHNICAL_DEBT.md`
- **Main CLAUDE.md**: Project-wide rules and commands

---

## 🚀 Next Action

**Execute Test #2** (2 minutes)
- Change model to "gpt-4" in test scenario
- Run 5 users
- Confirm 403 blocked response
- Verify no LLM provider called

**Command**:
```bash
# Update scenario model
# Run test
# Check logs for "model blocked by policy"
```

**After Test #2**: Implement request tracing (3 hours), then cache Phase 1 (2 hours)

