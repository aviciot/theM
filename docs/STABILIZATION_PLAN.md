# the-M Stabilization Plan

**Date**: 2026-10-18  
**Goal**: Stabilize each area of the-M platform for production readiness  
**Status**: LLM Gateway complete, moving to other subsystems

---

## Completed Areas ✅

### 1. LLM Gateway (Complete - 2026-10-18)
**Status**: Production Ready 🎉

**What's Done**:
- ✅ Request tracing (unique IDs, full lifecycle logging)
- ✅ Redis cache (99.7% DB query reduction)
- ✅ Policy enforcement (alias, allowlist, token cap)
- ✅ Performance tested (200 users, 212ms avg, 0.13% CPU)
- ✅ Error handling (403 rejections, request IDs in errors)
- ✅ Observability (grep-able request traces)

**Tests Passed**:
- Test #1: 50 users (alias resolution) ✅
- Test #2: 5 users (policy rejection) ✅
- Test #3: 200 users (cache + tracing) ✅

**Documentation**:
- `docs/GATEWAY_STATUS.md`
- `docs/TEST3_RESULTS.md`
- `docs/REQUEST_ID_FORMAT.md`
- `docs/GATEWAY_REQUEST_TRACING_IMPLEMENTATION.md`
- `docs/GATEWAY_POLICY_CACHE_PLAN.md`

---

## Remaining Areas to Stabilize

### 2. App Canvas / AppFlow (Priority: HIGH)

**Current State**: See `docs/CURRENT.md` and `docs/STATUS.md`

**Known Issues** (from STATUS.md):
- Check for any "BROKEN" or "PENDING" items in `docs/STATUS.md`
- Check for open issues in `docs/APP_EXPORT_IMPORT_INVESTIGATION.md`

**Stabilization Needs**:
1. **Testing**: Run app canvas scenarios
2. **Export/Import**: Verify completeness (see `docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md`)
3. **Error handling**: Ensure graceful failures
4. **Observability**: Request tracing for app flows

**Action**: Read `docs/STATUS.md` and identify next critical issue

---

### 3. Authentication / Authorization (Priority: HIGH)

**Components**:
- `them-auth-go` (Go auth service - login/me/refresh/logout)
- `them-auth-service` (Python - admin CRUD only)
- JWT validation (RS256)
- Bearer token cache (Redis L1 + L2)

**Stabilization Needs**:
1. **Token revocation**: Pub/sub working correctly
2. **Session cleanup**: Orphaned sessions handled
3. **Error messages**: Clear error responses
4. **Testing**: Auth flow end-to-end

**Action**: Check auth error rates in logs

---

### 4. WebSocket / SSE Runtime (Priority: HIGH)

**Components**:
- `internal/ws/` (WebSocket handler)
- `internal/sse/` (SSE handler)
- `internal/execution/` (lifecycle orchestration)
- `internal/gate/` (admission control)
- `internal/session/` (session management)

**Stabilization Needs**:
1. **Connection stability**: No unexpected disconnects
2. **Memory leaks**: Sessions cleaned up properly
3. **Gate logic**: No deadlocks in admission queue
4. **Error recovery**: Graceful handling of Temporal failures

**Action**: Monitor active sessions and gate metrics

---

### 5. Temporal Workflows (Priority: MEDIUM)

**Components**:
- `internal/temporal/` (workflows + activities)
- `internal/appflow/` (app canvas workflows)
- `them-dag-worker` (2 replicas)

**Stabilization Needs**:
1. **Worker health**: Both replicas polling correctly
2. **Activity registration**: All activities registered
3. **Workflow errors**: Proper error propagation
4. **Replay safety**: Deterministic workflow code

**Action**: Check Temporal UI for failed workflows

---

### 6. Database / RLS (Priority: MEDIUM)

**Components**:
- PostgreSQL with Row-Level Security
- `internal/db/` (RLS pools)
- Tenant isolation enforcement

**Stabilization Needs**:
1. **RLS coverage**: All tenant-scoped tables protected
2. **Connection pooling**: No pool exhaustion
3. **Query performance**: Indexes on hot paths
4. **Migrations**: Clean migration history

**Action**: Run RLS audit query from `docs/` if available

---

### 7. Redis / Caching (Priority: MEDIUM)

**Components**:
- Redis (sessions, cache, rate limits, gate, pub/sub)
- `internal/cache/` (Redis wrappers)

**Stabilization Needs**:
1. **Memory usage**: Redis memory bounded
2. **Key expiration**: TTLs set correctly
3. **Pub/sub reliability**: Token revocation propagates
4. **Failover**: Graceful degradation if Redis down

**Action**: Check Redis memory usage and keyspace size

---

### 8. A2A (Agent-to-Agent) (Priority: LOW)

**Components**:
- `internal/a2a/` (JSON-RPC server)
- `internal/agentregistry/` (agent discovery)
- A2A test agents (echo, slow, stream)

**Stabilization Needs**:
1. **Agent discovery**: Registry cache working
2. **Wire format**: A2A v1.0 spec compliance
3. **Security**: Agent authentication
4. **Error handling**: Graceful failures

**Action**: Run A2A test suite if available

---

### 9. Monitoring / Metrics (Priority: LOW)

**Components**:
- Prometheus metrics (`/metrics`)
- Structured logging (slog)
- Health checks (`/health/live`, `/health/ready`)

**Stabilization Needs**:
1. **Metric coverage**: Key operations instrumented
2. **Log levels**: Appropriate INFO/WARN/ERROR usage
3. **Health checks**: Accurate readiness checks
4. **Alerting**: Critical metrics defined

**Action**: Review Prometheus dashboard

---

## Stabilization Workflow

### For Each Area:

1. **Assess Current State**
   - Read relevant `docs/*.md` files
   - Check `docs/STATUS.md` for known issues
   - Run health checks / smoke tests

2. **Identify Issues**
   - Review logs for errors
   - Check metrics for anomalies
   - Run test suite if available
   - Look for BROKEN/PENDING in docs

3. **Prioritize Fixes**
   - P0: Production blockers (data loss, security, crashes)
   - P1: User-facing errors (bad UX, confusing errors)
   - P2: Technical debt (performance, code quality)
   - P3: Nice-to-have (optimizations, refactors)

4. **Fix and Test**
   - Implement fix
   - Add/update tests
   - Update documentation
   - Verify in local environment

5. **Document**
   - Update `docs/STATUS.md`
   - Update area-specific docs
   - Add to `docs/LESSONS.md` if applicable
   - Commit with clear message

---

## Next Steps (Recommended Order)

### Immediate (Today)

**Option 1: Check Overall Health**
```bash
# Check all container health
docker compose ps

# Check for errors in last hour
docker logs them-go-bridge --since 1h | grep -E "ERROR|FATAL"
docker logs them-auth-go --since 1h | grep -E "ERROR|FATAL"

# Check Redis memory
docker exec them-redis redis-cli INFO memory

# Check Postgres connections
docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM pg_stat_activity;"
```

**Option 2: Read STATUS.md**
- Check `docs/STATUS.md` for critical issues
- Identify next blocker from status doc

**Option 3: Focus Area Deep Dive**
- Pick one area (App Canvas recommended)
- Read all docs for that area
- Run tests for that area
- Fix top 3 issues

---

### Short Term (This Week)

1. **App Canvas Stabilization**
   - Run canvas scenarios end-to-end
   - Fix export/import completeness gaps
   - Add error handling where missing

2. **Auth Stabilization**
   - Test full auth flow (login → refresh → logout)
   - Verify token revocation working
   - Check session cleanup

3. **WebSocket/SSE Stabilization**
   - Monitor for memory leaks
   - Check gate admission logic
   - Verify session cleanup

---

### Medium Term (This Month)

4. **Temporal Workflow Audit**
   - Check failed workflows in Temporal UI
   - Verify both dag-worker replicas healthy
   - Add retry logic where missing

5. **Database Health**
   - Run EXPLAIN ANALYZE on slow queries
   - Add indexes where needed
   - Verify RLS coverage

6. **Redis Optimization**
   - Set memory limits if needed
   - Add key expiration policies
   - Monitor pub/sub lag

---

## Success Criteria (Per Area)

For an area to be considered "stabilized":

✅ **Functional**:
- [ ] Core features working end-to-end
- [ ] Error handling graceful (no crashes)
- [ ] Edge cases handled

✅ **Tested**:
- [ ] Unit tests passing
- [ ] Integration tests passing
- [ ] Manual smoke test passed

✅ **Documented**:
- [ ] Architecture documented
- [ ] Known issues documented
- [ ] Troubleshooting guide exists

✅ **Observable**:
- [ ] Key metrics exposed
- [ ] Errors logged with context
- [ ] Request tracing (where applicable)

✅ **Performant**:
- [ ] No memory leaks
- [ ] Response time acceptable
- [ ] Resource usage reasonable

---

## Tracking Progress

Update this file as areas complete:
- Move area from "Remaining" → "Completed"
- Add completion date
- Link to relevant docs/test results
- Note any remaining technical debt

---

## Questions to Answer (Per Area)

1. **What is the current state?** (working / broken / untested)
2. **What are the known issues?** (check docs/STATUS.md)
3. **What tests exist?** (can we run them?)
4. **What's the biggest risk?** (data loss / security / crashes)
5. **What's the quickest win?** (low-hanging fruit)

---

## Reference Docs

**Project-wide**:
- `docs/CURRENT.md` - Current architecture state
- `docs/STATUS.md` - Known issues and blockers
- `docs/LESSONS.md` - What burned us before
- `docs/INDEX.md` - Doc navigation

**Area-specific**:
- Gateway: `docs/GATEWAY_*.md`
- App Canvas: `docs/APP_*.md`
- Auth: `auth_service/SCHEMA.sql`, `go/internal/authserver/`
- Temporal: `docs/TEMPORAL_*.md` (if exists)
- A2A: `docs/A2A_*.md`

---

**Current Focus**: Choose next area to stabilize

**Recommendation**: Start with `docs/STATUS.md` to identify critical blockers
