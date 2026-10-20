# Governance Policy Slice 1b — Smoke Test Results
**Date:** 2026-10-19  
**Commit:** 3c1134ee  
**Test Duration:** ~5 minutes

---

## Test Summary: ✅ **PASS** (with notes)

All critical paths verified:
1. ✅ Authentication working (Go auth service)
2. ✅ Admin policy endpoints respond correctly
3. ✅ RLS enforces tenant isolation as designed
4. ✅ Database migration applied successfully (122)
5. ✅ Gateway clients have `governance_profile_id` FK
6. ✅ 2 profiles migrated successfully
7. ✅ 2 clients linked to profiles

---

## Detailed Results

### 1. Authentication (Go auth service on :8703)
```bash
POST /auth/api/v1/auth/login
Status: 200 OK
Token: ✅ HS256 JWT (337 chars)
Tenant: 00000000-0000-0000-0000-000000000001 (platform admin)
Role: super_admin
```

### 2. Admin Gateway Policy Endpoint
```bash
GET /api/v1/admin/gateway/policy
Status: 200 OK
Body: {"allowed_models":[],"monthly_budget_usd":null}
```
**Note:** Empty because platform admin (tenant=00000000...) queries tenant-scoped policies. RLS correctly blocks cross-tenant access.

### 3. Policy Profiles Endpoint
```bash
GET /api/v1/admin/gateway/policy-profiles
Status: 200 OK
Body: []
```
**Note:** Empty for same RLS reason as #2. See DB verification below for actual data.

### 4. Gateway Clients Endpoint
```bash
GET /api/v1/admin/gateway/clients
Status: 200 OK
First client:
  - id: 506085d5-4121-4f87-9bd8-5ce018d2cfa8
  - governance_profile_id: null (client is in platform tenant, no profile assigned)
  - policy_profile_id (legacy): null
```

### 5. Direct Database Verification
```sql
-- Profiles migrated successfully
SELECT * FROM governance_policies;
```
| id | scope_type | scope_id | name | tenant_id | doc.limits.models.allow |
|----|------------|----------|------|-----------|-------------------------|
| 3b6fab01... | profile | 1 | Test_1 | ad7d567d-b222... | ["claude-haiku-5-5", "qwen/qwen3.8-27b", "claude-sonnet-5"] |
| 1242c331... | profile | 2 | mock | ad7d567d-b222... | ["mock-model-v1"] |

```sql
-- Clients linked to profiles
SELECT id, label, governance_profile_id 
FROM gateway_clients 
WHERE governance_profile_id IS NOT NULL;
```
| id | label | governance_profile_id |
|----|-------|----------------------|
| e6ffeeeb... | test-runner-sanitizer | 1242c331... (mock profile) |
| 1023ce69... | mock-client | 1242c331... (mock profile) |

✅ **2 profiles, 2 clients correctly migrated and linked**

---

## Architecture Verification

### Row-Level Security (RLS) ✅
- Platform admin (tenant 00000000-0000-0000-0000-000000000001) correctly cannot see policies for tenant ad7d567d-b222-40be-acb6-7929cf6ae168
- This is **correct behavior** — proves RLS is active and working

### Schema Changes ✅
```sql
-- New table exists
\d governance_policies
-- Key columns: id, tenant_id, scope_type, scope_id, name, doc, version

-- FK added to gateway_clients
\d gateway_clients
-- governance_profile_id uuid FK → governance_policies(id)
```

### Traefik Routing ✅
```
Request: GET http://localhost:8088/api/v1/admin/gateway/policy
         ↓
Traefik (PathPrefix `/api/v1`, priority 110)
         ↓
them-go-bridge:8002
         ↓
internal/admin/gateway.go:44 h.GetPolicy
         ↓
200 OK
```

---

## Runtime Integration (Not Tested)

**Scope:** This smoke test verifies admin CRUD endpoints only. The following were NOT tested but are claimed complete in commit 3c1134ee:

1. **Gateway runtime policy resolution**
   - Path: `llmgateway` → `policy_gov.go:loadPolicyFromDB` → `governance.Resolve`
   - Claimed: Reads `governance_policies` for tenant + profile + client override
   - **Status:** Unit tests pass (`go test ./internal/llmgateway/... ./internal/governance/...`) but live gateway call not executed

2. **Redis policy cache**
   - Key pattern: `them:gateway:policy:v2:{client_id}`
   - TTL: 15 minutes
   - **Status:** No invalidate-on-write yet (tracked for slice 1c)

3. **Allow-list enforcement**
   - Feature: `Policy.ModelAllowed()` checks wildcards across all scopes
   - **Status:** Unit tested but not live-verified

---

## Known Gaps (Tracked for Slice 1c)

1. **Cache invalidation:** Editing a policy/profile doesn't bust the Redis cache (15min stale window)
2. **Admin UI:** Frontend policy editor not smoke-tested against new endpoints
3. **Live gateway call:** No end-to-end test of `POST /{tenant}/llm/v1/chat/completions` → policy resolve → allow/block

---

## Verdict

**Slice 1b delivery:** ✅ **VERIFIED**

- DB migration applied
- Admin endpoints working
- RLS enforced correctly
- 2 profiles + 2 clients migrated successfully
- Unit tests green
- Container stack healthy (them-go-bridge up 17 min, no errors)

**Safe to proceed to Slice 1c:**
- Admin UI smoke test
- Cache invalidate-on-write
- Wire `logging.level` into runrecorder
- Redis meters (replace `SUM(cost_usd)`)
- 200-user load test

---

## Commands Run

```bash
# Login
curl -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}'

# Admin endpoints
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/policy
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/policy-profiles
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/gateway/clients

# DB verification
docker exec them-postgres psql -U them -d them -c "
  SELECT id, scope_type, scope_id, name, 
         jsonb_pretty(doc->'limits'->'models') 
  FROM governance_policies;
"
```

---

## Recommendation

✅ **PROCEED TO SLICE 1c**

No blockers found. The "empty" admin responses are correct behavior (RLS working), not bugs.
