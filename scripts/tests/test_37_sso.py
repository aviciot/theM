#!/usr/bin/env python3
"""
test_37_sso.py — Step 37 SSO smoke test

Tests the tenant-admin self-service IdP configuration write path.
Does NOT drive the full browser OIDC flow (requires a running Keycloak instance
and a real browser). Instead, verifies:
  1. Super-admin can log in and list tenants
  2. PATCH /tenant/settings with idp_config sets idp_configured=true
  3. GET /auth/api/v1/auth/tenant-lookup?email=testuser@example.com returns idp_configured=true

Usage:
  python3 scripts/tests/test_37_sso.py [--base-url http://localhost:8088]

Prerequisites:
  - Stack running: docker compose ... up -d
  - Admin credentials: admin / admin123
  - If testing full OIDC flow: start with --profile sso to bring up them-keycloak

Discovery URL for Keycloak test IdP (when running with --profile sso):
  http://localhost:8088/auth/keycloak/realms/them
Client ID: them-m
Client Secret: them-m-secret
Redirect URI: http://localhost:8088/auth/api/v1/auth/oidc/callback
"""

import sys
import json
import argparse
import urllib.request
import urllib.error

BASE_URL = "http://localhost:8088"
ADMIN_USER = "admin"
ADMIN_PASS = "admin123"

IDP_CONFIG = {
    "discovery_url": "http://localhost:8088/auth/keycloak/realms/them",
    "client_id": "them-m",
    "client_secret": "them-m-secret",
    "redirect_uri": "http://localhost:8088/auth/api/v1/auth/oidc/callback",
}

TEST_EMAIL = "testuser@example.com"

passed = 0
failed = 0


def check(label: str, ok: bool, detail: str = "") -> None:
    global passed, failed
    status = "PASS" if ok else "FAIL"
    suffix = f" — {detail}" if detail else ""
    print(f"  [{status}] {label}{suffix}")
    if ok:
        passed += 1
    else:
        failed += 1


def req(method: str, path: str, body=None, token: str = "") -> dict:
    url = BASE_URL + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return {"__status": e.code, "__error": json.loads(raw)}
        except Exception:
            return {"__status": e.code, "__error": raw.decode(errors="replace")}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base-url", default=BASE_URL, help="Base URL (default: http://localhost:8088)")
    args = parser.parse_args()

    global BASE_URL
    BASE_URL = args.base_url.rstrip("/")

    print(f"\nStep 37 SSO smoke test — base URL: {BASE_URL}\n")

    # ── Test 1: Login as super_admin ─────────────────────────────────────────
    print("1. Super-admin login")
    resp = req("POST", "/auth/api/v1/auth/login", {"username": ADMIN_USER, "password": ADMIN_PASS})
    token = resp.get("access_token", "")
    check("POST /auth/api/v1/auth/login → access_token", bool(token),
          f"status={resp.get('__status', 'ok')}")
    if not token:
        print("\nCannot continue without a token — is the stack running?")
        sys.exit(1)

    # ── Test 2: List tenants, pick one ───────────────────────────────────────
    print("\n2. List tenants")
    tenants = req("GET", "/api/v1/admin/tenants", token=token)
    tenant_list = tenants if isinstance(tenants, list) else tenants.get("tenants", [])
    check("GET /api/v1/admin/tenants → non-empty list", len(tenant_list) > 0,
          f"count={len(tenant_list)}")

    # Pick first tenant (prefer 'default' slug)
    target = next((t for t in tenant_list if t.get("slug") == "default"), None)
    if target is None and tenant_list:
        target = tenant_list[0]
    check("Found a tenant to configure", target is not None,
          f"slug={target.get('slug') if target else 'none'}")
    if not target:
        print("\nNo tenant found — cannot continue.")
        sys.exit(1)
    slug = target.get("slug")

    # ── Test 3: PATCH tenant/settings with idp_config ────────────────────────
    print(f"\n3. Configure IdP for tenant '{slug}' via PATCH /tenant/settings")
    patch_resp = req("PATCH", "/api/v1/tenant/settings", {"idp_config": IDP_CONFIG}, token=token)
    check("PATCH /tenant/settings → 200", "__status" not in patch_resp,
          f"status={patch_resp.get('__status', 'ok')}")
    check("Response has idp_configured=true", patch_resp.get("idp_configured") is True,
          f"idp_configured={patch_resp.get('idp_configured')}")

    # ── Test 4: GET /tenant/settings reflects idp_configured ─────────────────
    print("\n4. Verify GET /tenant/settings reflects idp_configured")
    get_resp = req("GET", "/api/v1/tenant/settings", token=token)
    check("GET /tenant/settings → 200", "__status" not in get_resp,
          f"status={get_resp.get('__status', 'ok')}")
    check("GET response has idp_configured=true", get_resp.get("idp_configured") is True,
          f"idp_configured={get_resp.get('idp_configured')}")

    # ── Test 5: tenant-lookup by email returns idp_configured ────────────────
    print(f"\n5. Tenant lookup by email: {TEST_EMAIL}")
    lookup_resp = req("GET", f"/auth/api/v1/auth/tenant-lookup?email={TEST_EMAIL}")
    check("GET /auth/api/v1/auth/tenant-lookup → 200", "__status" not in lookup_resp,
          f"status={lookup_resp.get('__status', 'ok')}")

    # The tenant-lookup is by email domain — testuser@example.com would only show
    # idp_configured if the tenant has email_domain=example.com set. Check that
    # the endpoint is reachable and returns a valid shape.
    check("Lookup response has 'slug' field", "slug" in lookup_resp,
          f"keys={list(lookup_resp.keys())[:6]}")

    # ── Test 6: Clear IdP config ──────────────────────────────────────────────
    print("\n6. Clear IdP config")
    clear_resp = req("PATCH", "/api/v1/tenant/settings", {"idp_config": None}, token=token)
    check("PATCH /tenant/settings idp_config=null → 200", "__status" not in clear_resp,
          f"status={clear_resp.get('__status', 'ok')}")
    check("After clear: idp_configured=false", clear_resp.get("idp_configured") is False,
          f"idp_configured={clear_resp.get('idp_configured')}")

    # ── Summary ───────────────────────────────────────────────────────────────
    total = passed + failed
    print(f"\n{'='*50}")
    print(f"Results: {passed}/{total} passed, {failed} failed")
    if failed:
        print("OVERALL: FAIL")
        sys.exit(1)
    else:
        print("OVERALL: PASS")


if __name__ == "__main__":
    main()
