# Test #3: 200-User Stress Test - Checklist

## Pre-Test Setup ✅
- [x] Request tracing implemented (all logs have request_id)
- [x] Redis cache implemented (15 min TTL)
- [x] Test scenario updated: `n_users: 200`
- [x] Services running: them-go-bridge, them-redis, them-postgres

## How to Run

1. **Open Test Runner**: Navigate to `http://localhost:8088` → Test Runner
2. **Select Scenario**: "Test-Policy-Mock"
3. **Verify Settings**:
   - Users: 200
   - Model: "fast"
   - Messages: 3 per user (600 total requests)
4. **Click "Run Test"**

## What to Monitor

### During Test
```bash
# Watch bridge logs
docker logs them-go-bridge -f | grep "llmgateway:"

# Watch container stats
docker stats them-go-bridge them-redis them-postgres
```

### Key Metrics to Observe
- **Response time**: Should be < 1s avg (vs 825ms in Test #1)
- **Error rate**: Should be 0%
- **CPU usage**: Bridge should stay < 50% (was 1.39% with 50 users)
- **Memory**: Bridge should stay < 200MB (was 27MB with 50 users)

## What to Check After Test

### 1. Cache Performance
```bash
# Check Redis cache hits
docker exec them-redis redis-cli INFO stats | grep keyspace_hits

# Check if policy is cached
docker exec them-redis redis-cli GET "them:gateway:policy:1023ce69-710d-41bb-bd12-baa2fcce4c4b"
```

**Expected**: 
- First request: DB query (cache miss)
- Next 599 requests: Redis hits (cache hit rate ~99.8%)

### 2. Request Tracing Legibility
```bash
# Pick a random request ID from logs
docker logs them-go-bridge --since 5m | grep "request_id=" | head -1

# Trace that specific request
docker logs them-go-bridge --since 5m | grep "request_id=XXX"
```

**Expected**: Clear trace from start → policy check → provider call → complete

### 3. Database Load
```bash
# Check active connections during test
docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM pg_stat_activity WHERE datname='them';"
```

**Expected**: Minimal increase (cache protects DB)

### 4. Error Analysis
```bash
# Check for any errors
docker logs them-go-bridge --since 5m | grep -E "ERROR|WARN" | grep -v "workflow not found"

# Check HTTP status distribution
docker logs them-go-bridge --since 5m | grep "request complete" | grep -o "status=[0-9]*" | sort | uniq -c
```

**Expected**: 
- Zero HTTP 500 errors
- Zero policy load failures
- All requests: status=200

## Success Criteria

✅ **Performance**:
- [ ] 200 users handled successfully
- [ ] Response time < 1s avg
- [ ] Zero errors
- [ ] CPU < 50%
- [ ] Memory < 200MB

✅ **Cache**:
- [ ] ~600 Redis hits (one cache miss, rest hits)
- [ ] Policy cached in Redis
- [ ] Zero cache-related errors
- [ ] DB queries minimal (< 10 total)

✅ **Tracing**:
- [ ] All logs have unique request_ids
- [ ] Can trace individual requests via grep
- [ ] Request IDs in error responses (if any)
- [ ] No "log soup" - clear request boundaries

## Test Results Log

### Run 1: [DATE/TIME]
- Users: 200
- Total Requests: 600
- Response Time Avg: ____ ms
- Response Time p95: ____ ms
- Error Rate: ____%
- CPU Peak: ____%
- Memory Peak: ____ MB
- Cache Hit Rate: ____%
- DB Queries: ____
- Status: ✅ PASS / ❌ FAIL

**Notes**:
- 
- 

### Observations
- 
- 

### Issues Found
- 
- 

### Next Steps
- 
- 

---

## Post-Test Cleanup

```bash
# Restore test scenario to original (50 users for future Test #1 reruns)
# Edit test-runner/data/scenarios/test-policy-mock.json: "n_users": 50

# Clear Redis cache (optional - for clean next test)
docker exec them-redis redis-cli FLUSHDB
```
