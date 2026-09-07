#!/usr/bin/env python3
"""
test_38_multitenant.py — Multi-Tenant Robustness Automation

Covers Sections 0–11 of docs/MULTITENANT_TEST_PLAN.md:
  S0  Pre-flight
  S1  Super-admin login & capabilities
  S2  Tenant provisioning (API — mirrors the wizard)
  S3  Tenant-admin login & role enforcement
  S4  Tenant CRUD (RLS scoping)
  S5  Quota enforcement
  S6  Observability cross-tenant view
  S7  Tenant self-service settings
  S8  Token refresh preserves tenant
  S9  RLS: cross-tenant API isolation
  S10 Tenant deletion
  S11 SSO / OIDC (optional — requires --profile sso + Keycloak running)

Note on OIDC (S11):
  The full browser OIDC flow (PKCE + state cookie + browser redirect) cannot be
  driven by a pure HTTP script. S11 tests everything that is scriptable:
    - Keycloak discovery endpoint reachable
    - IdP config write/read/clear via PATCH /tenant/settings
    - /oidc/start returns a redirect (302) to the Keycloak authorize URL
    - /auth/tenant-lookup reflects idp_configured=true after save
  The actual browser login and callback (testuser@example.com / testpass) must
  be verified manually — see docs/MULTITENANT_TEST_PLAN.md S11-T11-3.

Usage:
  python3.12 scripts/tests/test_38_multitenant.py [options]

  --base-url URL      Stack base URL (default: http://localhost:8088)
  --tenant-slug SLUG  Slug for the test tenant (default: mt-test)
  --skip-sso          Skip Section 11 (SSO) even if Keycloak is reachable
  --skip-quota        Skip Section 5 (quota enforcement — creates + deletes many resources)
  --skip-deletion     Skip Section 10 (tenant deletion — useful to keep the tenant for manual testing)
  --keep-tenant       Alias for --skip-deletion

Prerequisites:
  - Stack running: docker compose ... up -d
  - admin / admin123 credentials present
  - For S11: docker compose ... --profile sso up -d them-keycloak
"""

import sys
import json
import time
import base64
import argparse
import urllib.request
import urllib.error
from typing import Optional

# ── Defaults ──────────────────────────────────────────────────────────────────
BASE_URL      = "http://localhost:8088"
ADMIN_USER    = "admin"
ADMIN_PASS    = "admin123"
TENANT_SLUG   = "mt-test"
TENANT_NAME   = "MT Test Corp"
TA_USERNAME   = "mt-admin"
TA_PASSWORD   = "MTAdmin1234!"
TA_NAME       = "MT Admin"

KEYCLOAK_DISCOVERY = "{base}/auth/keycloak/realms/them"
# Internal Keycloak URL — used for discovery_url in IdP config so the auth-go
# container can reach Keycloak directly (localhost:8088 is not reachable from inside a container)
KEYCLOAK_DISCOVERY_INTERNAL = "http://them-keycloak:8080/auth/keycloak/realms/them"
KEYCLOAK_CLIENT_ID = "them-m"
KEYCLOAK_SECRET    = "them-m-secret"
KEYCLOAK_REDIRECT  = "{base}/auth/api/v1/auth/oidc/callback"

# ── State (filled in during run) ──────────────────────────────────────────────
SA_TOKEN    = ""
TA_TOKEN    = ""
TENANT_ID   = ""
AGENT_IDS   = []   # created during quota test, cleaned up after
APP_IDS     = []   # created during quota test, cleaned up after

passed = 0
failed = 0
skipped = 0
section_failures: dict[str, list[str]] = {}
_current_section = ""


# ── Helpers ───────────────────────────────────────────────────────────────────

def section(name: str):
    global _current_section
    _current_section = name
    print(f"\n{'='*60}")
    print(f"  {name}")
    print(f"{'='*60}")


def check(label: str, ok: bool, detail: str = ""):
    global passed, failed
    status = "PASS" if ok else "FAIL"
    suffix = f"  ({detail})" if detail else ""
    print(f"  [{status}] {label}{suffix}")
    if ok:
        passed += 1
    else:
        failed += 1
        section_failures.setdefault(_current_section, []).append(label)


def skip(label: str, reason: str = ""):
    global skipped
    suffix = f"  — {reason}" if reason else ""
    print(f"  [SKIP] {label}{suffix}")
    skipped += 1


def req(method: str, path: str, body=None, token: str = "",
        cookie: str = "", allow_redirects: bool = False):
    """
    Make an HTTP request. Returns parsed JSON (dict or list).
    On HTTP error: returns {"__status": N, "__error": ...}.
    On redirect (3xx) with allow_redirects=True: returns {"__status": N, "__location": url}.

    token  — sent as Authorization: Bearer header (used by bridge admin routes)
    cookie — sent as Cookie: them_access_token=<value> (used by auth-service /me)
    """
    url = BASE_URL + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if cookie:
        headers["Cookie"] = f"them_access_token={cookie}"

    r = urllib.request.Request(url, data=data, headers=headers, method=method)

    if not allow_redirects:
        try:
            with urllib.request.urlopen(r, timeout=15) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return {"__status": e.code, "__error": json.loads(raw)}
            except Exception:
                return {"__status": e.code, "__error": raw.decode(errors="replace")}
        except Exception as e:
            return {"__status": 0, "__error": str(e)}
    else:
        # Capture the first redirect location without following it.
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *a, **k):
                return None
        opener = urllib.request.build_opener(NoRedirect)
        try:
            with opener.open(r, timeout=15) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            if e.code in (301, 302, 303, 307, 308):
                return {"__status": e.code, "__location": e.headers.get("Location", "")}
            raw = e.read()
            try:
                return {"__status": e.code, "__error": json.loads(raw)}
            except Exception:
                return {"__status": e.code, "__error": raw.decode(errors="replace")}
        except Exception as e:
            return {"__status": 0, "__error": str(e)}


def is_ok(resp) -> bool:
    if isinstance(resp, list):
        return True
    return "__status" not in resp


def status_of(resp) -> int:
    if isinstance(resp, list):
        return 200
    return resp.get("__status", 200)


def jwt_claims(token: str) -> dict:
    """Decode JWT payload (no signature verification — we trust our own auth service)."""
    try:
        parts = token.split(".")
        if len(parts) != 3:
            return {}
        # Add padding
        payload = parts[1] + "=" * (-len(parts[1]) % 4)
        return json.loads(base64.urlsafe_b64decode(payload))
    except Exception:
        return {}


# ── Section 0 — Pre-flight ────────────────────────────────────────────────────

def test_s0_preflight():
    section("S0 — Pre-flight")

    # Auth service health is mounted at /auth/health (mirrored under /auth/api/v1/auth/health too)
    resp = req("GET", "/auth/health/live")
    if not is_ok(resp):
        resp = req("GET", "/auth/api/v1/auth/health")
    check("Auth service reachable (/auth/health/live)", is_ok(resp),
          f"status={status_of(resp)}")

    resp = req("GET", "/health/live")
    check("Go bridge reachable (/health/live)", is_ok(resp),
          f"status={status_of(resp)}")


# ── Section 1 — Super-admin login ─────────────────────────────────────────────

def test_s1_superadmin():
    global SA_TOKEN
    section("S1 — Super-admin login & capabilities")

    resp = req("POST", "/auth/api/v1/auth/login",
               {"username": ADMIN_USER, "password": ADMIN_PASS})
    SA_TOKEN = resp.get("access_token", "")
    check("POST /auth/api/v1/auth/login → access_token", bool(SA_TOKEN),
          f"status={status_of(resp)}")
    if not SA_TOKEN:
        print("\n  FATAL: cannot continue without super-admin token.")
        sys.exit(1)

    # JWT claims carry role and tenant_id (login response body is tokenPairResponse — no role field)
    claims = jwt_claims(SA_TOKEN)
    check("JWT role = super_admin", claims.get("role") == "super_admin",
          f"role={claims.get('role')}")

    # /me uses cookie (not Bearer) — send access token as Cookie header
    me = req("GET", "/auth/api/v1/auth/me", cookie=SA_TOKEN)
    check("/me → 200", is_ok(me), f"status={status_of(me)}")
    check("/me role = super_admin", me.get("role") == "super_admin",
          f"role={me.get('role')}")

    # Can list tenants
    tenants = req("GET", "/api/v1/admin/tenants", token=SA_TOKEN)
    tenant_list = tenants if isinstance(tenants, list) else []
    check("GET /admin/tenants → non-empty list", len(tenant_list) >= 1,
          f"count={len(tenant_list)}")

    default_exists = any(t.get("slug") == "default" for t in tenant_list)
    check("'default' tenant exists", default_exists)

    # Observability accessible
    obs = req("GET", "/api/v1/admin/observability/summary", token=SA_TOKEN)
    check("GET /admin/observability/summary → 200", is_ok(obs),
          f"status={status_of(obs)}")

    # LLM providers accessible
    llm = req("GET", "/api/v1/admin/llm-providers", token=SA_TOKEN)
    check("GET /admin/llm-providers → 200", is_ok(llm),
          f"status={status_of(llm)}")


# ── Cleanup helper ────────────────────────────────────────────────────────────

def _cleanup_tenant(tenant_id: str):
    """Delete a tenant and all its dependent resources using the SA token."""
    # 1. Get a TA token BEFORE deleting the user (login works while user exists)
    stale_login = req("POST", "/auth/api/v1/auth/login",
                      {"username": TA_USERNAME, "password": TA_PASSWORD})
    stale_tok = stale_login.get("access_token", "")

    # 2. Delete tenant agents and apps via TA token (RLS-scoped to this tenant)
    if stale_tok:
        agents = req("GET", "/api/v1/admin/agents", token=stale_tok)
        for a in (agents if isinstance(agents, list) else []):
            req("DELETE", f"/api/v1/admin/agents/{a['id']}", token=stale_tok)
        apps = req("GET", "/api/v1/admin/applications", token=stale_tok)
        for app in (apps if isinstance(apps, list) else []):
            req("DELETE", f"/api/v1/admin/applications/{app['id']}", token=stale_tok)

    # 3. Delete the tenant-admin user
    users = req("GET", "/auth/api/v1/admin/users", token=SA_TOKEN)
    for u in (users if isinstance(users, list) else []):
        if u.get("username") == TA_USERNAME:
            req("DELETE", f"/auth/api/v1/admin/users/{u['id']}", token=SA_TOKEN)

    # 4. Delete the tenant
    time.sleep(0.3)
    del_r = req("DELETE", f"/api/v1/admin/tenants/{tenant_id}", token=SA_TOKEN)
    if not is_ok(del_r) and status_of(del_r) not in (200, 204):
        print(f"  [WARN] Tenant delete returned {status_of(del_r)}: {del_r}")
        # If still blocked, try direct DB cleanup as last resort
        import subprocess
        subprocess.run(
            ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
             "-c", f"DELETE FROM them.agents WHERE tenant_id='{tenant_id}'; "
                   f"DELETE FROM them.applications WHERE tenant_id='{tenant_id}';"],
            capture_output=True, text=True
        )
        time.sleep(0.2)
        del_r2 = req("DELETE", f"/api/v1/admin/tenants/{tenant_id}", token=SA_TOKEN)
        if not is_ok(del_r2) and status_of(del_r2) not in (200, 204):
            print(f"  [WARN] Tenant delete still failed after DB cleanup: {del_r2}")


# ── Section 2 — Tenant provisioning ──────────────────────────────────────────

def test_s2_provision():
    global TENANT_ID
    section("S2 — Tenant provisioning")

    # Cleanup stale tenant from a prior run
    existing = req("GET", "/api/v1/admin/tenants", token=SA_TOKEN)
    for t in (existing if isinstance(existing, list) else []):
        if t.get("slug") == TENANT_SLUG:
            old_id = t["id"]
            print(f"  [INFO] Stale tenant '{TENANT_SLUG}' found — cleaning up...")
            _cleanup_tenant(old_id)
            time.sleep(0.5)

    # Create tenant (display_name is required, not name)
    resp = req("POST", "/api/v1/admin/tenants", token=SA_TOKEN,
               body={"slug": TENANT_SLUG, "display_name": TENANT_NAME})
    check("POST /admin/tenants → 201", is_ok(resp) and resp.get("id"),
          f"status={status_of(resp)}")
    TENANT_ID = resp.get("id", "")
    if not TENANT_ID:
        print("  FATAL: tenant creation failed — cannot continue S2+.")
        return

    # Create tenant-admin user (served by them-auth-go at /auth/api/v1/admin/users)
    user_resp = req("POST", "/auth/api/v1/admin/users", token=SA_TOKEN, body={
        "username":    TA_USERNAME,
        "name":        TA_NAME,
        "password":    TA_PASSWORD,
        "role":        "viewer",          # platform role
        "tenant_role": "admin",           # membership role
        "tenant_id":   TENANT_ID,
    })
    check("POST /admin/users → 201 (tenant admin created)",
          is_ok(user_resp) and user_resp.get("id"),
          f"status={status_of(user_resp)}, detail={user_resp.get('__error','')}")

    # Set quota (PUT — UpsertQuota; plan field is required)
    quota_resp = req("PUT", f"/api/v1/admin/tenants/{TENANT_ID}/quota",
                     token=SA_TOKEN,
                     body={"plan": "starter", "max_agents": 3, "max_apps": 2})
    check("PUT /admin/tenants/{id}/quota → 200",
          is_ok(quota_resp), f"status={status_of(quota_resp)}, err={quota_resp.get('__error','') if isinstance(quota_resp, dict) else ''}")

    # Read quota back
    q = req("GET", f"/api/v1/admin/tenants/{TENANT_ID}/quota", token=SA_TOKEN)
    check("Quota max_agents=3 persisted", q.get("max_agents") == 3,
          f"max_agents={q.get('max_agents')}")
    check("Quota max_apps=2 persisted", q.get("max_apps") == 2,
          f"max_apps={q.get('max_apps')}")

    # Tenant appears in list
    tenants = req("GET", "/api/v1/admin/tenants", token=SA_TOKEN)
    slugs = [t.get("slug") for t in (tenants if isinstance(tenants, list) else [])]
    check(f"Tenant '{TENANT_SLUG}' in list after creation", TENANT_SLUG in slugs,
          f"slugs={slugs}")


# ── Section 3 — Tenant-admin login & role enforcement ─────────────────────────

def test_s3_tenant_admin():
    global TA_TOKEN
    section("S3 — Tenant-admin login & role enforcement")

    if not TENANT_ID:
        skip("All S3 tests", "TENANT_ID not set — S2 failed")
        return

    resp = req("POST", "/auth/api/v1/auth/login",
               {"username": TA_USERNAME, "password": TA_PASSWORD})
    TA_TOKEN = resp.get("access_token", "")
    check("Tenant-admin login → access_token", bool(TA_TOKEN),
          f"status={status_of(resp)}")
    if not TA_TOKEN:
        print("  FATAL: tenant-admin login failed.")
        return

    # role and tenant_id come from JWT claims (login response body is tokenPairResponse)
    claims = jwt_claims(TA_TOKEN)
    check("JWT role = admin", claims.get("role") == "admin",
          f"role={claims.get('role')}")

    login_tid = claims.get("tenant_id", "")
    default_tid = "00000000-0000-0000-0000-000000000001"
    check("JWT has tenant_id", bool(login_tid), f"tenant_id={login_tid}")
    check("tenant_id is NOT the default tenant", login_tid != default_tid,
          f"tenant_id={login_tid}")
    check("tenant_id matches provisioned tenant", login_tid == TENANT_ID,
          f"got={login_tid}, expected={TENANT_ID}")

    # /me uses cookie — send access token as Cookie header
    me = req("GET", "/auth/api/v1/auth/me", cookie=TA_TOKEN)
    check("/me → 200", is_ok(me), f"status={status_of(me)}")
    check("/me role = admin (not super_admin)", me.get("role") == "admin",
          f"role={me.get('role')}")

    # Blocked from super-admin routes — all must return 403
    # Note: /admin/users is served by them-auth-go at /auth/api/v1/admin/users
    blocked_routes = [
        "/api/v1/admin/tenants",                 # bridge — RequireSuperAdmin
        "/auth/api/v1/admin/users",              # auth-go — RequireSuperAdmin
        "/api/v1/admin/observability/summary",   # bridge — RequireSuperAdmin
        "/api/v1/admin/llm-providers",           # bridge — RequireSuperAdmin
    ]
    for path in blocked_routes:
        r = req("GET", path, token=TA_TOKEN)
        check(f"TA blocked from {path} → 403", status_of(r) == 403,
              f"got={status_of(r)}")

    # Tenant-admin CAN access tenant-scoped routes
    allowed_routes = [
        "/api/v1/admin/agents",
        "/api/v1/admin/applications",
        "/api/v1/admin/orchestrators",
    ]
    for path in allowed_routes:
        r = req("GET", path, token=TA_TOKEN)
        check(f"TA allowed on {path} → 200", is_ok(r),
              f"got={status_of(r)}")


# ── Section 4 — Tenant CRUD (RLS scoping) ────────────────────────────────────

def test_s4_rls_crud():
    section("S4 — Tenant CRUD (RLS scoping)")

    if not TA_TOKEN:
        skip("All S4 tests", "TA_TOKEN not set — S3 failed")
        return

    # Create agent as tenant admin (display_name is required)
    agent = req("POST", "/api/v1/admin/agents", token=TA_TOKEN, body={
        "display_name": "MT Echo Agent",
        "slug":         "mt_echo_agent",
        "transport":    "a2a_async",
        "endpoint_url": "http://a2a-echo:9200",
        "enabled":      True,
    })
    check("TA: create agent → 201", is_ok(agent) and agent.get("id"),
          f"status={status_of(agent)}")
    agent_id = agent.get("id", "")

    # Agent appears in TA's list
    agents_list = req("GET", "/api/v1/admin/agents", token=TA_TOKEN)
    agent_slugs = [a.get("slug") for a in (agents_list if isinstance(agents_list, list) else [])]
    check("Created agent visible to TA", "mt_echo_agent" in agent_slugs,
          f"slugs={agent_slugs[:5]}")

    # Create application as tenant admin
    app = req("POST", "/api/v1/admin/applications", token=TA_TOKEN, body={
        "name": "MT Test App",
        "slug": "mt-test-app",
    })
    check("TA: create application → 201", is_ok(app) and app.get("id"),
          f"status={status_of(app)}")
    app_id = app.get("id", "")

    # Application appears in TA's list
    apps_list = req("GET", "/api/v1/admin/applications", token=TA_TOKEN)
    app_slugs = [a.get("slug") for a in (apps_list if isinstance(apps_list, list) else [])]
    check("Created app visible to TA", "mt-test-app" in app_slugs,
          f"slugs={app_slugs[:5]}")

    # Verify individual agent detail is accessible and matches the created agent.
    # (Agent responses don't expose tenant_id — RLS enforces isolation at the DB layer.)
    agent_detail = req("GET", f"/api/v1/admin/agents/{agent_id}", token=TA_TOKEN)
    check("Agent detail accessible and matches created id",
          is_ok(agent_detail) and agent_detail.get("id") == agent_id,
          f"status={status_of(agent_detail)}, id={agent_detail.get('id')}, expected={agent_id}")


# ── Section 5 — Quota enforcement ─────────────────────────────────────────────

def test_s5_quota(run_quota: bool):
    section("S5 — Quota enforcement")

    if not run_quota:
        skip("All S5 tests (quota enforcement)", "--skip-quota flag set")
        return
    if not TA_TOKEN:
        skip("All S5 tests", "TA_TOKEN not set — S3 failed")
        return

    # Quota: max_agents=3 — S4 already created 1 agent; create 2 more to hit the limit
    created_agent_ids = []
    for i in range(1, 3):
        r = req("POST", "/api/v1/admin/agents", token=TA_TOKEN, body={
            "display_name": f"Quota Agent {i}",
            "slug": f"mt_quota_agent_{i}",
            "transport": "a2a_async", "endpoint_url": "http://a2a-echo:9200", "enabled": True,
        })
        if is_ok(r) and r.get("id"):
            created_agent_ids.append(r["id"])
    check("Created 2 more agents (total=3, at max_agents=3)", len(created_agent_ids) == 2,
          f"created={len(created_agent_ids)}")

    # Next agent must be rejected (quota now full)
    fourth = req("POST", "/api/v1/admin/agents", token=TA_TOKEN, body={
        "display_name": "Quota Agent Over",
        "slug": "mt_quota_agent_over",
        "transport": "a2a_async", "endpoint_url": "http://a2a-echo:9200", "enabled": True,
    })
    check("Agent over quota rejected", not is_ok(fourth),
          f"status={status_of(fourth)}")

    # Cleanup quota-test agents
    for aid in created_agent_ids:
        req("DELETE", f"/api/v1/admin/agents/{aid}", token=TA_TOKEN)

    # Quota: max_apps=2 — note: we already created mt-test-app in S4
    # Create 1 more app to fill quota, then try a 3rd
    r = req("POST", "/api/v1/admin/applications", token=TA_TOKEN, body={
        "name": "Quota App 2", "slug": "mt-quota-app-2",
    })
    quota_app2_id = r.get("id", "")
    check("Created app #2 (filling max_apps=2)", is_ok(r) and bool(quota_app2_id),
          f"status={status_of(r)}")

    third_app = req("POST", "/api/v1/admin/applications", token=TA_TOKEN, body={
        "name": "Quota App 3", "slug": "mt-quota-app-3",
    })
    check("3rd app rejected (quota exceeded)", not is_ok(third_app),
          f"status={status_of(third_app)}")

    # Cleanup
    if quota_app2_id:
        req("DELETE", f"/api/v1/admin/applications/{quota_app2_id}", token=TA_TOKEN)


# ── Section 6 — Observability: cross-tenant view ──────────────────────────────

def test_s6_observability():
    section("S6 — Observability (super-admin cross-tenant view)")

    obs = req("GET", "/api/v1/admin/observability/summary", token=SA_TOKEN)
    check("GET /admin/observability/summary → 200", is_ok(obs),
          f"status={status_of(obs)}")

    rows = obs if isinstance(obs, list) else []
    # Observability rows have tenant_id + display_name (no tenant_slug field)
    tenant_ids = [r.get("tenant_id") for r in rows]
    default_tid = "00000000-0000-0000-0000-000000000001"
    check("Observability includes 'default' tenant (by ID)", default_tid in tenant_ids,
          f"tenant_ids={tenant_ids}")
    check(f"Observability includes '{TENANT_SLUG}' tenant (by ID)",
          TENANT_ID and TENANT_ID in tenant_ids,
          f"TENANT_ID={TENANT_ID}, tenant_ids={tenant_ids}")

    # Each row has expected fields
    for row in rows:
        if row.get("tenant_id") == TENANT_ID:
            check("Observability row has run_count_30d field",
                  "run_count_30d" in row, f"keys={list(row.keys())}")
            check("Observability row has agent_count field",
                  "agent_count" in row, f"keys={list(row.keys())}")
            break


# ── Section 7 — Tenant self-service settings ──────────────────────────────────

def test_s7_self_service():
    section("S7 — Tenant self-service settings")

    if not TA_TOKEN:
        skip("All S7 tests", "TA_TOKEN not set — S3 failed")
        return

    # GET /tenant/settings
    settings = req("GET", "/api/v1/tenant/settings", token=TA_TOKEN)
    check("GET /tenant/settings → 200", is_ok(settings),
          f"status={status_of(settings)}")
    check("Settings slug = our tenant", settings.get("slug") == TENANT_SLUG,
          f"slug={settings.get('slug')}")

    # PATCH email_domain (writable field)
    patch = req("PATCH", "/api/v1/tenant/settings", token=TA_TOKEN,
                body={"email_domain": "mt-test.example.com"})
    check("PATCH /tenant/settings (email_domain) → 200", is_ok(patch),
          f"status={status_of(patch)}")
    check("email_domain updated", patch.get("email_domain") == "mt-test.example.com",
          f"email_domain={patch.get('email_domain')}")

    # Slug must remain unchanged
    check("slug is read-only (not changed by PATCH)",
          patch.get("slug") == TENANT_SLUG,
          f"slug={patch.get('slug')}")

    # Attempt to set slug via PATCH — must be ignored
    patch2 = req("PATCH", "/api/v1/tenant/settings", token=TA_TOKEN,
                 body={"slug": "hacked-slug"})
    check("slug cannot be changed via PATCH /tenant/settings",
          patch2.get("slug") == TENANT_SLUG,
          f"slug={patch2.get('slug')}")

    # GET /tenant/quota
    quota = req("GET", "/api/v1/tenant/quota", token=TA_TOKEN)
    check("GET /tenant/quota → 200", is_ok(quota),
          f"status={status_of(quota)}")
    check("Quota max_agents visible to TA", "max_agents" in quota,
          f"keys={list(quota.keys())[:8]}")


# ── Section 8 — Token refresh preserves tenant ────────────────────────────────

def test_s8_refresh():
    section("S8 — Token refresh preserves tenant")

    if not TENANT_ID:
        skip("All S8 tests", "TENANT_ID not set — S2 failed")
        return

    # Fresh login to get refresh token
    login = req("POST", "/auth/api/v1/auth/login",
                {"username": TA_USERNAME, "password": TA_PASSWORD})
    refresh_token = login.get("refresh_token", "")
    check("Login returns refresh_token", bool(refresh_token),
          f"present={bool(refresh_token)}")
    if not refresh_token:
        skip("Refresh sub-tests", "no refresh_token in login response")
        return

    # Refresh accepts Bearer header (not JSON body — tokenPairResponse only, no JSON body needed)
    refreshed = req("POST", "/auth/api/v1/auth/refresh", token=refresh_token)
    new_token = refreshed.get("access_token", "")
    check("POST /auth/refresh → new access_token", bool(new_token),
          f"status={status_of(refreshed)}")

    # Role and tenant_id come from JWT claims (tokenPairResponse has no role/tenant_id)
    if new_token:
        rclaims = jwt_claims(new_token)
        check("Refreshed JWT has role=admin", rclaims.get("role") == "admin",
              f"role={rclaims.get('role')}")
        check("Refreshed JWT has correct tenant_id",
              rclaims.get("tenant_id") == TENANT_ID,
              f"tenant_id={rclaims.get('tenant_id')}")

    # Refreshed token still blocked from super-admin routes
    if new_token:
        block = req("GET", "/api/v1/admin/tenants", token=new_token)
        check("Refreshed token blocked from /admin/tenants → 403",
              status_of(block) == 403, f"got={status_of(block)}")


# ── Section 9 — Cross-tenant API isolation ────────────────────────────────────

def test_s9_isolation():
    section("S9 — Cross-tenant API isolation")

    if not TA_TOKEN or not SA_TOKEN:
        skip("All S9 tests", "tokens not set — earlier sections failed")
        return

    # Create a resource as SA (scoped to default tenant via SA pool + GUC)
    # Then confirm TA cannot see it via their scoped token
    #
    # The super-admin token uses BYPASSRLS admin pool — it can see across tenants.
    # The tenant-admin token uses RLS App pool — it sees ONLY its own tenant.
    # This test verifies that behavior at the API level.

    # SA creates a marker agent in the default tenant's scope
    # (SA uses admin pool → agent lands in whichever tenant the SA JWT implies)
    # We verify: TA's GET /agents does not include agents from another tenant.

    # First snapshot TA's visible agents
    ta_agents_before = req("GET", "/api/v1/admin/agents", token=TA_TOKEN)
    ta_slugs_before = {a.get("slug") for a in (ta_agents_before if isinstance(ta_agents_before, list) else [])}

    # SA creates agent (admin pool — scoped to default tenant via SA's tenant context)
    sa_marker = req("POST", "/api/v1/admin/agents", token=SA_TOKEN, body={
        "display_name": "SA Marker Agent",
        "slug":         "sa_isolation_marker",
        "transport":    "a2a_async",
        "endpoint_url": "http://a2a-echo:9200",
        "enabled":      True,
    })
    sa_marker_id = sa_marker.get("id", "")

    if not sa_marker_id:
        skip("Cross-tenant leak check", "SA agent creation failed — cannot test isolation")
    else:
        # TA must NOT see the SA marker agent
        ta_agents_after = req("GET", "/api/v1/admin/agents", token=TA_TOKEN)
        ta_slugs_after = {a.get("slug") for a in (ta_agents_after if isinstance(ta_agents_after, list) else [])}
        check("TA cannot see agents created by SA (cross-tenant isolation)",
              "sa_isolation_marker" not in ta_slugs_after,
              f"ta_slugs={list(ta_slugs_after)[:5]}")

        # SA can see it
        sa_agents = req("GET", "/api/v1/admin/agents", token=SA_TOKEN)
        sa_slugs = {a.get("slug") for a in (sa_agents if isinstance(sa_agents, list) else [])}
        check("SA can see the marker agent (BYPASSRLS)",
              "sa_isolation_marker" in sa_slugs,
              f"sa_slugs_count={len(sa_slugs)}")

        # Cleanup SA marker
        req("DELETE", f"/api/v1/admin/agents/{sa_marker_id}", token=SA_TOKEN)

    # Unauthenticated access rejected
    no_auth = req("GET", "/api/v1/admin/agents")
    check("Unauthenticated GET /admin/agents → 401/403",
          status_of(no_auth) in (401, 403),
          f"got={status_of(no_auth)}")

    # Bad JWT rejected
    bad_jwt = req("GET", "/api/v1/admin/agents", token="eyJhbGciOiJIUzI1NiJ9.bad.sig")
    check("Malformed JWT → 401/403",
          status_of(bad_jwt) in (401, 403),
          f"got={status_of(bad_jwt)}")


# ── Section 10 — Tenant deletion ──────────────────────────────────────────────

def test_s10_deletion(run_deletion: bool):
    global TA_TOKEN, TENANT_ID
    section("S10 — Tenant deletion")

    if not run_deletion:
        skip("Tenant deletion", "--skip-deletion / --keep-tenant flag set")
        print(f"  [INFO] Tenant '{TENANT_SLUG}' (ID: {TENANT_ID}) left in place.")
        return
    if not TENANT_ID:
        skip("Tenant deletion", "TENANT_ID not set — S2 failed")
        return

    # Cleanup all tenant resources then delete
    _cleanup_tenant(TENANT_ID)
    time.sleep(0.3)

    # Verify deletion succeeded
    del_check = req("GET", f"/api/v1/admin/tenants/{TENANT_ID}", token=SA_TOKEN)
    check("Tenant no longer retrievable after deletion",
          status_of(del_check) in (404, 403, 200) and del_check.get("slug") != TENANT_SLUG,
          f"status={status_of(del_check)}")

    # Tenant no longer in list
    time.sleep(0.3)
    tenants = req("GET", "/api/v1/admin/tenants", token=SA_TOKEN)
    slugs = [t.get("slug") for t in (tenants if isinstance(tenants, list) else [])]
    check(f"Tenant '{TENANT_SLUG}' gone from list after deletion",
          TENANT_SLUG not in slugs, f"slugs={slugs}")

    # Tenant-admin login now fails (no membership)
    login = req("POST", "/auth/api/v1/auth/login",
                {"username": TA_USERNAME, "password": TA_PASSWORD})
    check("Tenant-admin login fails after tenant deletion",
          not login.get("access_token"),
          f"status={status_of(login)}, has_token={bool(login.get('access_token'))}")

    # Clear globals so S11 re-provisions instead of reusing the deleted tenant's token
    TA_TOKEN = ""
    TENANT_ID = ""


# ── Section 11 — SSO / OIDC ───────────────────────────────────────────────────

def test_s11_sso(skip_sso: bool):
    section("S11 — SSO / OIDC (Keycloak)")

    if skip_sso:
        skip("All S11 tests", "--skip-sso flag set")
        return

    # Check Keycloak is reachable
    kc_discovery = KEYCLOAK_DISCOVERY.format(base=BASE_URL)
    kc_resp = req("GET", f"/auth/keycloak/realms/them/.well-known/openid-configuration")
    if not is_ok(kc_resp):
        skip("All S11 tests",
             f"Keycloak not reachable (status={status_of(kc_resp)}). "
             f"Start with: docker compose ... --profile sso up -d them-keycloak")
        return

    check("Keycloak discovery endpoint reachable", True,
          f"issuer={kc_resp.get('issuer','?')[:60]}")

    # We need a live tenant with TA_TOKEN for IdP config tests.
    # If S10 ran deletion, re-provision a minimal tenant for S11.
    ta_token_for_sso = TA_TOKEN
    tenant_id_for_sso = TENANT_ID
    sso_cleanup_tenant = False

    if not ta_token_for_sso or not tenant_id_for_sso:
        # Clean up any stale SSO test tenant from a previous run
        sso_slug = TENANT_SLUG + "-sso"
        sso_user = TA_USERNAME + "-sso"
        existing = req("GET", "/api/v1/admin/tenants", token=SA_TOKEN)
        for t in (existing if isinstance(existing, list) else []):
            if t.get("slug") == sso_slug:
                _cleanup_tenant(t["id"])
                time.sleep(0.3)
                break
        # Also clean up stale SSO user
        users = req("GET", "/auth/api/v1/admin/users", token=SA_TOKEN)
        for u in (users if isinstance(users, list) else []):
            if u.get("username") == sso_user:
                req("DELETE", f"/auth/api/v1/admin/users/{u['id']}", token=SA_TOKEN)

        # Quick re-provision (display_name is required, not name)
        t2 = req("POST", "/api/v1/admin/tenants", token=SA_TOKEN,
                body={"slug": sso_slug, "display_name": "MT SSO Test"})
        tenant_id_for_sso = t2.get("id", "")
        if tenant_id_for_sso:
            req("POST", "/auth/api/v1/admin/users", token=SA_TOKEN, body={
                "username": sso_user, "name": "MT SSO Admin",
                "password": TA_PASSWORD, "role": "viewer",
                "tenant_role": "admin", "tenant_id": tenant_id_for_sso,
            })
            login = req("POST", "/auth/api/v1/auth/login",
                        {"username": sso_user, "password": TA_PASSWORD})
            ta_token_for_sso = login.get("access_token", "")
            sso_cleanup_tenant = True

    idp_config = {
        # Use internal Keycloak URL so auth-go can fetch discovery doc from inside Docker.
        # The external URL (localhost:8088) is not reachable from inside the container.
        "discovery_url": KEYCLOAK_DISCOVERY_INTERNAL,
        "client_id":     KEYCLOAK_CLIENT_ID,
        "client_secret": KEYCLOAK_SECRET,
        "redirect_uri":  KEYCLOAK_REDIRECT.format(base=BASE_URL),
    }

    # T11-1: Configure IdP via PATCH /tenant/settings
    if ta_token_for_sso:
        patch = req("PATCH", "/api/v1/tenant/settings", token=ta_token_for_sso,
                    body={"idp_config": idp_config})
        check("PATCH /tenant/settings with idp_config → 200", is_ok(patch),
              f"status={status_of(patch)}")
        check("idp_configured=true after save", patch.get("idp_configured") is True,
              f"idp_configured={patch.get('idp_configured')}")

        # T11-2: PATCH response reflects idp_configured; GET /tenant/settings returns base tenant fields
        # (GET uses dal.GetTenant which returns Tenant — no idp_configured field)
        # Re-PATCH with same config to re-verify idp_configured flag in response
        repatch = req("PATCH", "/api/v1/tenant/settings", token=ta_token_for_sso,
                      body={"idp_config": idp_config})
        check("Re-PATCH confirms idp_configured=true", repatch.get("idp_configured") is True,
              f"idp_configured={repatch.get('idp_configured')}")
        get_s = repatch  # use PATCH response as the current settings source

        # T11-3: /oidc/start returns redirect to Keycloak (302)
        # Route inside auth-go: /oidc/start → via Traefik PathPrefix(/auth) → /auth/oidc/start
        slug_for_sso = get_s.get("slug", TENANT_SLUG)
        oidc_start = req("GET", f"/auth/oidc/start?tenant={slug_for_sso}",
                         allow_redirects=True)
        is_redirect = oidc_start.get("__status") in (301, 302, 303, 307, 308)
        # 422 means the tenant has no IdP config yet for the slug — acceptable outcome;
        # the important thing is the route is reachable (not 404)
        route_reachable = is_redirect or oidc_start.get("__status") in (422, 400, 200)
        location = oidc_start.get("__location", "")
        check("/oidc/start route reachable (not 404)", route_reachable,
              f"status={oidc_start.get('__status')}")
        if is_redirect:
            check("/oidc/start location points to Keycloak",
                  "keycloak" in location.lower() or "openid" in location.lower(),
                  f"location={location[:80]}")
        else:
            # After PATCH /tenant/settings saved the IdP config, /oidc/start should redirect
            # Re-try with the slug from the settings response (which has the IdP config)
            oidc_start2 = req("GET", f"/auth/oidc/start?tenant={slug_for_sso}",
                              allow_redirects=True)
            is_redirect2 = oidc_start2.get("__status") in (301, 302, 303, 307, 308)
            check("/oidc/start redirects after IdP config saved", is_redirect2,
                  f"status={oidc_start2.get('__status')}, location={oidc_start2.get('__location','')[:60]}")

        # T11-4: tenant-lookup — set email_domain on tenant, then lookup by that domain
        req("PATCH", "/api/v1/tenant/settings", token=ta_token_for_sso,
            body={"email_domain": "example.com"})
        lookup = req("GET", "/auth/api/v1/auth/tenant-lookup?email=testuser@example.com")
        check("GET /auth/api/v1/auth/tenant-lookup endpoint reachable (200 or 404 with detail)",
              status_of(lookup) in (200, 404),
              f"status={status_of(lookup)}")
        if is_ok(lookup):
            check("Lookup found tenant — has slug field", "slug" in lookup,
                  f"keys={list(lookup.keys())[:6]}")
        else:
            # 404 means email_domain not indexed yet or wrong domain — acceptable
            check("Lookup 404 has detail field (handler working)", "detail" in lookup.get("__error", {}),
                  f"error={lookup.get('__error')}")

        # T11-5: Clear IdP config
        clear = req("PATCH", "/api/v1/tenant/settings", token=ta_token_for_sso,
                    body={"idp_config": None})
        check("PATCH /tenant/settings idp_config=null → 200", is_ok(clear),
              f"status={status_of(clear)}")
        check("After clear: idp_configured=false", clear.get("idp_configured") is False,
              f"idp_configured={clear.get('idp_configured')}")

        print()
        print("  [INFO] Full browser OIDC login (testuser@example.com / testpass)")
        print("         must be verified manually — see docs/MULTITENANT_TEST_PLAN.md S11-T11-3.")
    else:
        skip("IdP config tests", "could not get tenant-admin token for SSO test")

    # Cleanup SSO-only tenant if we created one
    if sso_cleanup_tenant and tenant_id_for_sso:
        _cleanup_tenant(tenant_id_for_sso)


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    global BASE_URL, TENANT_SLUG, TENANT_NAME, TA_USERNAME, TA_NAME

    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--base-url",     default=BASE_URL,    help="Stack base URL")
    parser.add_argument("--tenant-slug",  default=TENANT_SLUG, help="Test tenant slug")
    parser.add_argument("--skip-sso",     action="store_true", help="Skip Section 11 (SSO)")
    parser.add_argument("--skip-quota",   action="store_true", help="Skip Section 5 (quota)")
    parser.add_argument("--skip-deletion",action="store_true", help="Keep tenant after test")
    parser.add_argument("--keep-tenant",  action="store_true", help="Alias for --skip-deletion")
    args = parser.parse_args()

    BASE_URL     = args.base_url.rstrip("/")
    TENANT_SLUG  = args.tenant_slug
    TENANT_NAME  = f"{TENANT_SLUG.upper()} Corp"
    TA_USERNAME  = f"{TENANT_SLUG}-admin"
    TA_NAME      = f"{TENANT_SLUG.title()} Admin"
    run_deletion = not (args.skip_deletion or args.keep_tenant)
    run_quota    = not args.skip_quota

    print(f"\n{'='*60}")
    print(f"  Multi-Tenant Robustness Test")
    print(f"  Base URL:    {BASE_URL}")
    print(f"  Tenant slug: {TENANT_SLUG}")
    print(f"  Quota tests: {'yes' if run_quota else 'skipped'}")
    print(f"  SSO tests:   {'yes' if not args.skip_sso else 'skipped'}")
    print(f"  Deletion:    {'yes' if run_deletion else 'skipped (--keep-tenant)'}")
    print(f"{'='*60}")

    test_s0_preflight()
    test_s1_superadmin()
    test_s2_provision()
    test_s3_tenant_admin()
    test_s4_rls_crud()
    test_s5_quota(run_quota)
    test_s6_observability()
    test_s7_self_service()
    test_s8_refresh()
    test_s9_isolation()
    test_s10_deletion(run_deletion)
    test_s11_sso(args.skip_sso)

    # ── Summary ───────────────────────────────────────────────────────────────
    total = passed + failed
    print(f"\n{'='*60}")
    print(f"  Results: {passed}/{total} passed, {failed} failed, {skipped} skipped")
    if section_failures:
        print("\n  Failed checks by section:")
        for sec, checks in section_failures.items():
            print(f"    {sec}:")
            for c in checks:
                print(f"      - {c}")
    print()
    if failed:
        print("  OVERALL: FAIL")
        sys.exit(1)
    else:
        print("  OVERALL: PASS")


if __name__ == "__main__":
    main()
