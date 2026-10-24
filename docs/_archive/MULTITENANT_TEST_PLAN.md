# Multi-Tenant Robustness Test Plan — the-M
# Last updated: 2026-09-07

This plan proves the multi-tenant solution is robust across all layers:
DB isolation (RLS), auth (JWT + role enforcement), API (route guards), and UX (nav visibility, playground, runs).

Each test has a **type** tag:
- **UI** — manual browser steps
- **API** — curl / script (no browser)
- **DB** — psql query inside the postgres container
- **Script** — runnable Python check

Run order matters: complete each section before moving to the next.

---

## Prerequisites

Stack running (with Temporal for WS sessions):
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml ps
```
All containers: `healthy` or `running`.

Shorthand used throughout:
- **BASE** = `http://localhost:8088`
- **SA_TOKEN** = super-admin JWT (fetched in Section 1)
- **TA_TOKEN** = tenant-admin JWT (fetched in Section 3)

---

## Section 0 — Pre-flight

### T0-1 — All containers healthy (Script)
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml ps --format "table {{.Name}}\t{{.Status}}"
```
**Pass:** `them-go-bridge`, `them-auth-go`, `them-postgres`, `them-redis`, `them-frontend` all show `healthy` or `running`.

### T0-2 — Auth service reachable (API)
```bash
curl -s BASE/auth/api/v1/auth/health | jq .
```
**Pass:** `{"status":"ok"}` or HTTP 200.

### T0-3 — Bridge reachable (API)
```bash
curl -s BASE/health/live | jq .
```
**Pass:** HTTP 200.

---

## Section 1 — Super-Admin Login & Capabilities

### T1-1 — Super-admin login returns JWT with correct claims (API)
```bash
curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | jq '{token: .access_token, role: .role}'
```
**Pass:** `access_token` non-empty, `role` = `"super_admin"` (or decode JWT and check `role` claim).

Save as SA_TOKEN:
```bash
SA_TOKEN=$(curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
```

### T1-2 — /me returns super_admin role (API)
```bash
curl -s BASE/auth/api/v1/auth/me -H "Authorization: Bearer $SA_TOKEN" | jq .role
```
**Pass:** `"super_admin"`.

### T1-3 — Super-admin sees Tenants/Users/Observability nav (UI)

1. Open `BASE` in browser, log in as `admin` / `admin123`.
2. Check left nav.

**Pass:** Nav shows **Tenants**, **Users**, **Observability** under Admin section.

### T1-4 — Super-admin can list tenants (API)
```bash
curl -s BASE/api/v1/admin/tenants -H "Authorization: Bearer $SA_TOKEN" | jq 'length'
```
**Pass:** ≥ 1 (at minimum the `default` tenant exists).

### T1-5 — Super-admin can access observability (API)
```bash
curl -s -o /dev/null -w "%{http_code}" \
  BASE/api/v1/admin/observability/summary \
  -H "Authorization: Bearer $SA_TOKEN"
```
**Pass:** `200`.

---

## Section 2 — Tenant Provisioning (UI + API)

### T2-1 — Create tenant via wizard (UI)

1. Admin → Tenants → **New Tenant**.
2. Step 1: slug `acme`, name `Acme Corp` → **Create Tenant →**.
3. Step 2: username `acme-admin`, name `Acme Admin`, password `Acme1234!` → **Create User →**.
4. Step 3: set `max_agents=5`, `max_apps=3` → **Save Quota →**.
5. Step 4: **Done**.

**Pass:** `acme` tenant appears in the list. Panel opens with General / Identity Provider / Quotas tabs.

### T2-2 — Tenant exists in DB (DB)
```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT slug, name, enabled FROM them.tenants ORDER BY created_at;"
```
**Pass:** Two rows — `default` and `acme`.

### T2-3 — Quota saved correctly (API)
```bash
ACME_ID=$(curl -s BASE/api/v1/admin/tenants -H "Authorization: Bearer $SA_TOKEN" \
  | python3 -c "import sys,json; t=[x for x in json.load(sys.stdin) if x['slug']=='acme']; print(t[0]['id'])")

curl -s BASE/api/v1/admin/tenants/$ACME_ID/quota \
  -H "Authorization: Bearer $SA_TOKEN" | jq '{max_agents, max_apps}'
```
**Pass:** `max_agents=5`, `max_apps=3`.

### T2-4 — Tenant admin user exists in auth service (DB)
```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT u.username, m.role FROM auth_service.users u
      JOIN them.tenant_memberships m ON m.user_id = u.id
      JOIN them.tenants t ON t.id = m.tenant_id
      WHERE t.slug='acme';"
```
**Pass:** Row with `username=acme-admin`, `role=admin`.

---

## Section 3 — Tenant-Admin Login & Role Enforcement

### T3-1 — Tenant-admin login returns JWT with tenant_id and admin role (API)
```bash
curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"acme-admin","password":"Acme1234!"}' \
  | jq '{token: .access_token, role: .role, tenant_id: .tenant_id}'
```
**Pass:** `access_token` non-empty, `role = "admin"`, `tenant_id` = acme tenant UUID (not null, not default tenant ID).

Save as TA_TOKEN:
```bash
TA_TOKEN=$(curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"acme-admin","password":"Acme1234!"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
```

### T3-2 — /me returns admin role for tenant admin (API)
```bash
curl -s BASE/auth/api/v1/auth/me -H "Authorization: Bearer $TA_TOKEN" | jq .role
```
**Pass:** `"admin"` (membership role, not `"super_admin"`).

### T3-3 — Tenant admin cannot access super-admin routes (API)

These must all return 403:
```bash
for path in \
  /api/v1/admin/tenants \
  /api/v1/admin/users \
  /api/v1/admin/observability/summary \
  /api/v1/admin/llm-providers; do
  code=$(curl -s -o /dev/null -w "%{http_code}" $BASE$path \
    -H "Authorization: Bearer $TA_TOKEN")
  echo "$path → $code"
done
```
**Pass:** All four paths return `403`.

### T3-4 — Tenant admin nav is scoped (UI)

1. Log out. Log in as `acme-admin` / `Acme1234!`.
2. Check left nav.

**Pass:**
- Nav shows: Applications, Agents, Orchestrators, MCP Servers, Runs, Audit Logs.
- Nav does NOT show: Tenants, Users, Observability, LLM Providers.

### T3-5 — Direct navigation to super-admin pages redirects (UI)

1. While logged in as `acme-admin`, type in URL bar: `BASE/admin/tenants`.
2. **Pass:** Redirected away (to `/admin/applications` or login). Never see the tenants list.

---

## Section 4 — Tenant-Admin CRUD (RLS Enforcement)

### T4-1 — Tenant admin can create an agent (API)
```bash
curl -s -X POST BASE/api/v1/admin/agents \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme Echo","slug":"acme-echo","transport":"a2a",
       "url":"http://a2a-echo:9200","enabled":true}' | jq '{id,name,slug}'
```
**Pass:** 201 response with agent `id`.

### T4-2 — Tenant admin can create an application (API)
```bash
curl -s -X POST BASE/api/v1/admin/applications \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme App","slug":"acme-app"}' | jq '{id,name,slug}'
```
**Pass:** 201 with app `id`.

Save app ID:
```bash
ACME_APP_ID=$(curl -s -X POST BASE/api/v1/admin/applications \
  ... | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
```

### T4-3 — Agent created by acme-admin is NOT visible to default tenant (DB)

Get the agent ID from T4-1, then:
```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT a.name, t.slug AS tenant
      FROM them.agents a
      JOIN them.tenants t ON t.id = a.tenant_id
      WHERE a.slug='acme-echo';"
```
**Pass:** Row shows `tenant=acme`. Only one row.

### T4-4 — Default tenant SA token cannot see acme agent (API)
```bash
curl -s BASE/api/v1/admin/agents -H "Authorization: Bearer $SA_TOKEN" \
  | python3 -c "import sys,json; agents=json.load(sys.stdin); \
    print([a['slug'] for a in agents if a['slug']=='acme-echo'])"
```

> Note: Super-admin uses BYPASSRLS admin pool and sees across all tenants. This test verifies
> a *tenant-scoped* token for the default tenant cannot see acme agents.

To test true cross-tenant isolation, get a default-tenant user token and check:
```bash
# Assuming default tenant has a user 'default-user':
DEFAULT_TOKEN=$(curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

# Super-admin token uses admin pool (BYPASSRLS) — use for DB verification only.
# RLS isolation is verified by the Go integration test TestRLS_TwoTenantFullIsolation.
```

**Pass:** RLS integration test `TestRLS_TwoTenantFullIsolation` covers cross-tenant isolation at DB layer.
```bash
cd go && go test -tags=integration -run TestRLS_TwoTenantFullIsolation ./internal/db/... -v
```

---

## Section 5 — Quota Enforcement

### T5-1 — Quota limits are respected: max_agents (API)

With `acme` quota set to `max_agents=5`, create 5 agents (acme-1 through acme-5), then try a 6th:
```bash
for i in 1 2 3 4 5; do
  curl -s -X POST BASE/api/v1/admin/agents \
    -H "Authorization: Bearer $TA_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"Agent $i\",\"slug\":\"acme-quota-$i\",\"transport\":\"a2a\",
         \"url\":\"http://a2a-echo:9200\",\"enabled\":true}" \
    | jq -r '.slug // .error'
done

# 6th agent — should be rejected
curl -s -w "\n%{http_code}" -X POST BASE/api/v1/admin/agents \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Too Many","slug":"acme-quota-6","transport":"a2a",
       "url":"http://a2a-echo:9200","enabled":true}'
```
**Pass:** First 5 succeed (201). 6th returns `429` or `409` with quota error.

> Cleanup: delete acme-quota-1 through acme-quota-5 before continuing.

### T5-2 — max_apps enforced (API)

With `max_apps=3`, create 3 apps and try a 4th:
```bash
for i in 1 2 3; do
  curl -s -X POST BASE/api/v1/admin/applications \
    -H "Authorization: Bearer $TA_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"App $i\",\"slug\":\"acme-app-quota-$i\"}" \
    | jq -r '.slug // .error'
done

curl -s -w "\n%{http_code}" -X POST BASE/api/v1/admin/applications \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Over Quota","slug":"acme-app-quota-4"}'
```
**Pass:** First 3 succeed. 4th returns `429` or `409`.

> Cleanup: delete the 3 quota-test apps before continuing.

---

## Section 6 — Playground & Run Isolation (UI + DB)

### T6-1 — Full setup in UI as tenant admin

1. Log in as `acme-admin`.
2. Navigate to **Applications → Acme App** (created in T4-2, or create it now via UI).
3. Open the entry point card.
4. Assign an orchestrator with the `a2a-echo` agent.
5. Set the LLM provider key (if not already set on the app).
6. Click **Playground**.

### T6-2 — Send a message in Playground (UI)

1. Type `Hello from acme!` and send.
2. **Pass:** Response appears. Run shows in **Runs** with status `completed`.

### T6-3 — Run is scoped to acme tenant (DB)
```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT r.id, r.status, t.slug AS tenant
      FROM them.runs r
      JOIN them.applications a ON a.id = r.application_id
      JOIN them.tenants t ON t.id = a.tenant_id
      ORDER BY r.created_at DESC LIMIT 3;"
```
**Pass:** Most recent run shows `tenant=acme`.

### T6-4 — acme-admin Runs page shows only acme runs (UI)

While logged in as `acme-admin`, go to **Runs**.
**Pass:** Only runs from `Acme App` appear. No runs from `default` tenant.

### T6-5 — Super-admin Observability shows both tenants (UI + API)
```bash
curl -s BASE/api/v1/admin/observability/summary \
  -H "Authorization: Bearer $SA_TOKEN" | jq '[.[] | {tenant: .tenant_slug, runs: .run_count_30d}]'
```
**Pass:** Both `default` and `acme` appear in the summary, each with their own run counts.

---

## Section 7 — Tenant Self-Service Settings

### T7-1 — Tenant admin can view own settings (API)
```bash
curl -s BASE/api/v1/tenant/settings \
  -H "Authorization: Bearer $TA_TOKEN" | jq '{slug, name, email_domain}'
```
**Pass:** Returns the `acme` tenant's own fields. Does NOT expose another tenant's data.

### T7-2 — Tenant admin can update own settings (API)
```bash
curl -s -X PATCH BASE/api/v1/tenant/settings \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"email_domain":"acme.example.com"}' | jq '.email_domain'
```
**Pass:** Returns `"acme.example.com"`.

### T7-3 — Tenant admin cannot change slug or enabled via self-service (API)
```bash
curl -s -X PATCH BASE/api/v1/tenant/settings \
  -H "Authorization: Bearer $TA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"hacked","enabled":false}' | jq '{slug, enabled}'
```
**Pass:** `slug` stays `"acme"`, `enabled` stays `true` (read-only fields ignored).

### T7-4 — Tenant admin can view own quota (API)
```bash
curl -s BASE/api/v1/tenant/quota \
  -H "Authorization: Bearer $TA_TOKEN" | jq '{max_agents, max_apps}'
```
**Pass:** `max_agents=5`, `max_apps=3` (as set in T2-1).

---

## Section 8 — Token Refresh Preserves Tenant

### T8-1 — Refresh token carries tenant (API)
```bash
# Get login response with refresh token
LOGIN=$(curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"acme-admin","password":"Acme1234!"}')

REFRESH=$(echo $LOGIN | python3 -c "import sys,json; print(json.load(sys.stdin)['refresh_token'])")

# Refresh
REFRESHED=$(curl -s -X POST BASE/auth/api/v1/auth/refresh \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$REFRESH\"}")

echo $REFRESHED | python3 -c "import sys,json; d=json.load(sys.stdin); \
  print('role:', d.get('role'), 'tenant_id:', d.get('tenant_id'))"
```
**Pass:** `role=admin`, `tenant_id` = acme tenant UUID (same as original login).

### T8-2 — Refreshed token still blocked from super-admin routes (API)
```bash
REFRESHED_TOKEN=$(echo $REFRESHED | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

curl -s -o /dev/null -w "%{http_code}" \
  BASE/api/v1/admin/tenants \
  -H "Authorization: Bearer $REFRESHED_TOKEN"
```
**Pass:** `403`.

---

## Section 9 — Cross-Tenant Data Isolation (RLS)

### T9-1 — RLS integration test (Script)
```bash
cd go && go test -tags=integration -run TestRLS_TwoTenantFullIsolation ./internal/db/... -v
```
**Pass:** All 24 table checks pass. Cross-tenant INSERT rejected. Test output shows `PASS`.

### T9-2 — Catalog verification (Script)
```bash
cd go && go test -tags=integration -run TestRLS_CatalogVerification ./internal/db/... -v
```
**Pass:** CV-01..CV-05 all pass (28 tables, FORCE RLS, correct policy names).

### T9-3 — Verify both tenant tables have RLS enabled (DB)
```bash
docker exec -it them-postgres psql -U them -d them -c "
  SELECT tablename, rowsecurity, forceroulsecurity
  FROM pg_tables
  WHERE schemaname='them'
  ORDER BY tablename;" 2>/dev/null || \
docker exec -it them-postgres psql -U them -d them -c "
  SELECT relname AS table, relrowsecurity AS rls_enabled, relforcerowsecurity AS rls_forced
  FROM pg_class
  WHERE relnamespace = 'them'::regnamespace AND relkind='r'
  ORDER BY relname;"
```
**Pass:** All `them.*` tables show `rls_enabled=t` and `rls_forced=t`.

---

## Section 10 — Tenant Deletion (Cleanup)

### T10-1 — Delete tenant via UI (UI)

> Do this AFTER all isolation tests are complete.

1. Log in as super-admin.
2. Admin → Tenants → click `acme` card.
3. General tab → Danger Zone → **Delete Tenant** → confirm.

**Pass:** Tenant disappears from the list. Panel closes.

### T10-2 — Delete fails when tenant still has resources (UI)

> Test this before deleting data:

1. While `acme` still has agents/apps, try Delete Tenant.

**Pass:** Error shown — delete rejected. Message indicates resources must be removed first.

### T10-3 — Tenant gone from DB after deletion (DB)
```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT slug FROM them.tenants ORDER BY created_at;"
```
**Pass:** Only `default` remains. No `acme` row.

### T10-4 — acme-admin login fails after deletion (API)
```bash
curl -s -X POST BASE/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"acme-admin","password":"Acme1234!"}' | jq .
```
**Pass:** Login fails — 401 or error indicating no tenant membership.

---

## Section 11 — SSO / OIDC (Optional — requires `--profile sso`)

Only run if Keycloak is started:
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml --profile sso up -d them-keycloak
# Wait ~20s, then verify:
curl -s BASE/auth/keycloak/realms/them/.well-known/openid-configuration | jq .issuer
```

### T11-1 — IdP config write/read (Script)
```bash
python3 scripts/tests/test_37_sso.py
```
**Pass:** All 6 checks pass.

### T11-2 — Tenant-admin can configure own IdP (UI)

1. Log in as `acme-admin`.
2. Go to **Settings → SSO** tab (under tenant settings).
3. Fill in Keycloak discovery URL, client ID, secret.
4. Save.

**Pass:** `idp_configured=true` badge shown.

### T11-3 — SSO login flow (UI)

1. Log out.
2. On login page, enter `testuser@example.com` — email-first flow detects SSO.
3. Click **Sign in with SSO**.
4. Keycloak login: `testuser@example.com` / `testpass`.
5. Redirect back.

**Pass:** Logged in as SSO user, scoped to `acme` tenant.

---

## Known Gaps (not testable yet)

| Gap | Reason |
|---|---|
| WebRTC entry points | Not yet wired in UI |
| Quota enforcement UI warnings | Quota is stored + enforced at API; no in-UI over-quota banner |
| SSO group→role mapping UI | OIDC group claims handled in Go; no UI for configuring mappings per-tenant |
| Automated live two-tenant API E2E script | The RLS Go integration tests cover DB isolation; no Python script covers full flow end-to-end |

---

## Quick Reference — Sanity Commands

```bash
# All containers healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Tenant list (DB)
docker exec -it them-postgres psql -U them -d them -c "SELECT slug, name, enabled FROM them.tenants;"

# Run count per tenant
docker exec -it them-postgres psql -U them -d them -c "
  SELECT t.slug, COUNT(r.id) AS runs
  FROM them.tenants t
  LEFT JOIN them.applications a ON a.tenant_id=t.id
  LEFT JOIN them.runs r ON r.application_id=a.id
  GROUP BY t.slug ORDER BY t.slug;"

# RLS status
docker exec -it them-postgres psql -U them -d them -c "
  SELECT relname, relrowsecurity, relforcerowsecurity
  FROM pg_class WHERE relnamespace='them'::regnamespace AND relkind='r'
  ORDER BY relname;"

# Auth logs
docker logs them-auth-go --tail 30

# Bridge logs
docker logs them-go-bridge --tail 30

# Run Go RLS integration tests
cd go && go test -tags=integration -v ./internal/db/...
```
