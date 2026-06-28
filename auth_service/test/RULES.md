# Auth Service Test Rules

## Mandatory Testing Protocol

### After Database Initialization
**ALWAYS run `test_full.py` after running `init_complete.py --drop`**

```bash
# Step 1: Initialize database
docker exec mcp-auth-service python init_complete.py --drop

# Step 2: Run regression tests (MANDATORY)
python auth_service/test/test_full.py
```

Expected result: **12/12 tests PASS**

---

## Test Maintenance Rules

### When to Update Tests

1. **New Endpoint Added** → Add test case to `test_full.py`
2. **Endpoint Modified** → Update corresponding test
3. **Database Schema Changed** → Verify all tests still pass
4. **New IAM Feature** → Add integration test

### Test Coverage Requirements

**Current Coverage (12 tests):**
- ✅ Health check
- ✅ Login (email/password)
- ✅ Token validation
- ✅ Token refresh
- ✅ Logout
- ✅ Validate after logout
- ✅ Invalid credentials
- ✅ Traefik routing
- ✅ List roles
- ✅ List teams
- ✅ Get user permissions
- ✅ Check specific permission

**Must maintain 100% endpoint coverage.**

---

## Regression Testing

### Before Committing Code
1. Run `init_complete.py --drop`
2. Run `test_full.py`
3. All tests must pass (12/12)

### After Schema Changes
1. Update `init_complete.py` if needed
2. Update `test_full.py` if endpoints changed
3. Run full test suite
4. Document changes in commit message

---

## Test Failure Protocol

**If tests fail:**
1. Check auth_service logs: `docker logs mcp-auth-service`
2. Check database state: `docker exec omni_pg_db psql -U omni -d omni -c "SELECT * FROM auth_service.users"`
3. Verify services running: `docker ps | grep auth`
4. Re-run init if needed
5. Fix code/tests
6. Re-run until 12/12 pass

**Never commit with failing tests.**
