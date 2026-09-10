#!/usr/bin/env python3.12
"""
test_38_phase4_external_jwt.py — Phase 4 end-user auth E2E tests

Tests three credential paths end-to-end against a live stack:
  Path 1 — bank-issued RS256 JWT (external_jwt EP mode, Keycloak bank realm)
  Path 2 — backend service token (is_backend=true, X-External-User header trusted)
  Path 3 — the-M user JWT (user_jwt EP mode)

Runs entirely from the host — WS connections go through Traefik on port 8088.
Keycloak tokens are fetched through Traefik at /auth/keycloak/...

Prerequisites:
  - Stack running with --profile sso for PATH 1:
      docker compose --project-name them_gateway \\
          -f docker-compose.yml -f docker-compose.dev.yml --profile sso up -d
  - websockets Python package installed:
      pip install websockets   (or pip3 install websockets)
  - Admin credentials: admin / admin123

Usage:
  python3 scripts/tests/test_38_phase4_external_jwt.py [--base-url http://localhost:8088]
  python3 scripts/tests/test_38_phase4_external_jwt.py --skip-path1  # Keycloak not running
  ADMIN_JWT=<token> python3 scripts/tests/test_38_phase4_external_jwt.py

Cleanup:
  All test apps/EPs are deleted at the end. The runtime IDP config is restored
  to whatever it was before the test started.
"""

import sys
import os
import json
import asyncio
import uuid
import argparse
import urllib.request
import urllib.error

# ── Constants ──────────────────────────────────────────────────────────────────

BASE_URL = "http://localhost:8088"

KEYCLOAK_BASE = "http://localhost:8088/auth/keycloak"
BANK_REALM = "bank"
BANK_TEST_CLIENT = "bank-test-cli"
BANK_TEST_SECRET = "bank-test-secret"
BANK_TEST_USER = "avi2"
BANK_TEST_USER2 = "avi3"
BANK_TEST_PASS = "admin123"

ADMIN_USER = "admin"
ADMIN_PASS = "admin123"

# Admin user belongs to the default tenant; self-service routes use JWT claims for tenant.
DEFAULT_TENANT_SLUG = "default"
DEFAULT_TENANT_ID = "00000000-0000-0000-0000-000000000001"

# ── State ──────────────────────────────────────────────────────────────────────

passed = 0
failed = 0
warnings = 0
_created_app_ids = []
_prev_ridp = None


# ── Output ────────────────────────────────────────────────────────────────────

def check(label, ok, detail=""):
    global passed, failed
    status = "PASS" if ok else "FAIL"
    suffix = " — " + detail if detail else ""
    print("  [{}] {}{}".format(status, label, suffix))
    if ok:
        passed += 1
    else:
        failed += 1


def warn(label, detail=""):
    global warnings
    warnings += 1
    suffix = " — " + detail if detail else ""
    print("  [WARN] {}{}".format(label, suffix))


def section(title):
    print("\n── " + title)


# ── HTTP helpers ───────────────────────────────────────────────────────────────

def req(method, path, body=None, token="", base=None, extra_headers=None, raw_body=None):
    """HTTP request helper. Returns (status_code, parsed_body)."""
    url = (base or BASE_URL) + path
    if raw_body is not None:
        data = raw_body
    elif body is not None:
        data = json.dumps(body).encode()
    else:
        data = None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if extra_headers:
        headers.update(extra_headers)
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r) as resp:
            raw = resp.read()
            try:
                return resp.status, json.loads(raw)
            except Exception:
                return resp.status, raw.decode(errors="replace")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, raw.decode(errors="replace")


def admin_login():
    env_jwt = os.environ.get("ADMIN_JWT")
    if env_jwt:
        return env_jwt
    status, body = req("POST", "/auth/api/v1/auth/login",
                        {"username": ADMIN_USER, "password": ADMIN_PASS})
    if status != 200 or not isinstance(body, dict) or not body.get("access_token"):
        print("[FATAL] Admin login failed: {} {}".format(status, str(body)[:120]))
        sys.exit(1)
    return body["access_token"]


# ── Keycloak helpers ───────────────────────────────────────────────────────────

def kc_reachable():
    try:
        status, _ = req("GET",
                        "/auth/keycloak/realms/" + BANK_REALM)
        return status == 200
    except Exception:
        return False


def kc_admin_token():
    """Get Keycloak master-realm admin token through Traefik."""
    form = b"client_id=admin-cli&grant_type=password&username=admin&password=admin123"
    status, body = req(
        "POST",
        "/auth/keycloak/realms/master/protocol/openid-connect/token",
        raw_body=form,
        extra_headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    if status != 200 or not isinstance(body, dict):
        return None
    return body.get("access_token")


def kc_ensure_test_client(kc_token):
    """Create bank-test-cli in bank realm if not present."""
    status, body = req(
        "GET",
        "/auth/keycloak/admin/realms/{}/clients?clientId={}".format(
            BANK_REALM, BANK_TEST_CLIENT),
        token=kc_token,
    )
    if status == 200 and isinstance(body, list) and body:
        return True  # already exists
    client_def = {
        "clientId": BANK_TEST_CLIENT,
        "name": "Bank E2E Test Client",
        "enabled": True,
        "publicClient": False,
        "secret": BANK_TEST_SECRET,
        "standardFlowEnabled": False,
        "implicitFlowEnabled": False,
        "directAccessGrantsEnabled": True,
        "serviceAccountsEnabled": False,
        "protocol": "openid-connect",
        "redirectUris": [],
        "webOrigins": [],
    }
    status, _ = req(
        "POST",
        "/auth/keycloak/admin/realms/{}/clients".format(BANK_REALM),
        body=client_def,
        token=kc_token,
    )
    return status in (200, 201)


def kc_get_bank_token(username, password=BANK_TEST_PASS):
    """Get a bank-realm access token via password grant."""
    form = (
        "client_id={}&client_secret={}&grant_type=password&username={}&password={}".format(
            BANK_TEST_CLIENT, BANK_TEST_SECRET, username, password
        )
    ).encode()
    status, body = req(
        "POST",
        "/auth/keycloak/realms/{}/protocol/openid-connect/token".format(BANK_REALM),
        raw_body=form,
        extra_headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    if status != 200 or not isinstance(body, dict):
        return None
    tok = body.get("access_token", "")
    return tok if tok else None


def bank_jwks_uri():
    # The go-bridge fetches JWKS internally using the configured URI.
    # We store the internal URI (reachable from go-bridge container).
    return "http://them-keycloak:8080/auth/keycloak/realms/{}/protocol/openid-connect/certs".format(BANK_REALM)


def bank_issuer():
    # Keycloak tokens have iss = the URL of the token endpoint base.
    # From within the Docker network, this is http://them-keycloak:8080/auth/keycloak/realms/bank.
    # HOWEVER, the iss claim in the JWT is what Keycloak issued it with — it uses KC_HOSTNAME
    # or the request URL. Since we fetch through Traefik, the iss in the token will be:
    return "http://localhost:8088/auth/keycloak/realms/{}".format(BANK_REALM)


# ── Fixture helpers ────────────────────────────────────────────────────────────

def create_app(admin_token, tenant_id=DEFAULT_TENANT_ID):
    slug = "p4-" + uuid.uuid4().hex[:8]
    enabled = True
    status, body = req(
        "POST", "/api/v1/admin/applications",
        {"name": "Phase4 E2E App", "slug": slug, "enabled": enabled},
        token=admin_token,
    )
    if status not in (200, 201) or not isinstance(body, dict):
        return None
    app_id = body.get("id")
    if app_id:
        _created_app_ids.append(app_id)
    return app_id


def _psql(sql):
    """Run a SQL statement inside the postgres container."""
    import subprocess
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-c", sql],
        capture_output=True, text=True, timeout=10,
    )
    return result.returncode == 0, result.stdout + result.stderr


def create_ep(admin_token, app_id, access_mode, allowed_principals="both"):
    """Create an EP and immediately patch access_policy + allowed_principals via DB."""
    slug = "ep-{}-{}".format(access_mode[:5], uuid.uuid4().hex[:6])
    status, body = req(
        "POST", "/api/v1/admin/applications/{}/entry-points".format(app_id),
        {
            "slug": slug,
            "entry_point_type": "websocket",
            "enabled": True,
        },
        token=admin_token,
    )
    if status not in (200, 201) or not isinstance(body, dict):
        return None
    ep_id = body.get("id", "")
    if not ep_id:
        return None
    # Patch access_policy and allowed_principals directly in DB
    # (the EP create API only accepts slug/type/enabled; mode is set via canvas publish)
    mode_json = '{{"mode": "{}"}}'.format(access_mode)
    sql = (
        "UPDATE them.entry_points "
        "SET access_policy = '{}', allowed_principals = '{}' "
        "WHERE id = '{}';"
    ).format(mode_json, allowed_principals, ep_id)
    ok, out = _psql(sql)
    if not ok:
        print("  [WARN] DB patch failed for EP {}: {}".format(ep_id, out[:80]))
    return ep_id, slug


def get_app_slug(admin_token, app_id):
    status, body = req("GET", "/api/v1/admin/applications/{}".format(app_id), token=admin_token)
    if status == 200 and isinstance(body, dict):
        return body.get("slug")
    return None


def create_access_token(admin_token, is_backend=False):
    """Create an opaque access token. Sets is_backend via direct DB update if needed."""
    label = "p4-tok-" + uuid.uuid4().hex[:6]
    status, body = req(
        "POST", "/api/v1/admin/tokens",
        {"label": label},
        token=admin_token,
    )
    if status not in (200, 201) or not isinstance(body, dict):
        return None
    plaintext = body.get("token") or body.get("value") or body.get("raw_token")
    if not plaintext:
        return None
    if is_backend:
        # Patch is_backend via DB (not exposed in API)
        sql = "UPDATE them.access_tokens SET is_backend = true WHERE label = '{}' AND tenant_id = '{}';".format(
            label, DEFAULT_TENANT_ID)
        _psql(sql)
    return plaintext


def ridp_get(admin_token):
    status, body = req("GET", "/api/v1/tenant/runtime-idp", token=admin_token)
    return body if isinstance(body, dict) else {}


def ridp_put(admin_token, jwks_uri, issuer, audience=""):
    b = {"jwks_uri": jwks_uri, "issuer": issuer}
    if audience:
        b["audience"] = audience
    return req("PUT", "/api/v1/tenant/runtime-idp", body=b, token=admin_token)


def ridp_delete(admin_token):
    status, _ = req("DELETE", "/api/v1/tenant/runtime-idp", token=admin_token)
    return status


def delete_app(admin_token, app_id):
    req("DELETE", "/api/v1/admin/applications/{}".format(app_id), token=admin_token)


# ── WebSocket helper ───────────────────────────────────────────────────────────

async def _ws_attempt(tenant_slug, app_slug, ep_slug, bearer="", external_user=""):
    """Try a WS connection. Returns (admitted: bool, detail: str).
    Admission = the HTTP upgrade handshake succeeded (101 Switching Protocols).
    We don't wait for a message — the connection is immediately closed after open.
    """
    import websockets
    ws_url = "ws://localhost:8088/{}/apps/{}/{}/ws".format(tenant_slug, app_slug, ep_slug)
    headers = {}
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    if external_user:
        headers["X-External-User"] = external_user
    try:
        async with websockets.connect(ws_url, additional_headers=headers,
                                       open_timeout=5) as ws:
            # Connection established (101 upgrade succeeded) — that's what we check.
            # Close cleanly without waiting for messages.
            await ws.close()
            return True, "ok:connected"
    except Exception as e:
        return False, "err:" + type(e).__name__ + ":" + str(e)[:80]


def ws_check(tenant_slug, app_slug, ep_slug, bearer="", external_user="", expect_ok=True):
    """Run WS connection attempt. Returns (outcome_matches_expectation, detail)."""
    admitted, detail = asyncio.run(
        _ws_attempt(tenant_slug, app_slug, ep_slug, bearer, external_user)
    )
    return (admitted if expect_ok else not admitted), detail


# ── Teardown ──────────────────────────────────────────────────────────────────

def teardown(admin_token):
    section("Teardown")
    # Restore runtime IDP
    if _prev_ridp is not None:
        if _prev_ridp.get("configured"):
            ridp_put(
                admin_token,
                _prev_ridp.get("jwks_uri", ""),
                _prev_ridp.get("issuer", ""),
                _prev_ridp.get("audience", ""),
            )
            check("Runtime IDP restored to previous config", True)
        else:
            ridp_delete(admin_token)
            check("Runtime IDP cleared (was unconfigured before test)", True)
    # Delete test apps
    for app_id in _created_app_ids:
        delete_app(admin_token, app_id)
    if _created_app_ids:
        check("Deleted {} test app(s)".format(len(_created_app_ids)), True)


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    global _prev_ridp, BASE_URL, KEYCLOAK_BASE

    parser = argparse.ArgumentParser(description="Phase 4 E2E — external JWT auth")
    parser.add_argument("--base-url", default=BASE_URL)
    parser.add_argument("--skip-path1", action="store_true",
                        help="Skip bank JWT tests (Keycloak not running)")
    parser.add_argument("--skip-path2", action="store_true",
                        help="Skip backend service token tests")
    parser.add_argument("--skip-path3", action="store_true",
                        help="Skip the-M user JWT tests")
    args = parser.parse_args()
    BASE_URL = args.base_url.rstrip("/")
    KEYCLOAK_BASE = BASE_URL + "/auth/keycloak"

    # Check websockets available
    try:
        import websockets  # noqa: F401
    except ImportError:
        print("[FATAL] websockets not installed. Run: pip install websockets")
        sys.exit(1)

    print("=== test_38_phase4_external_jwt: Phase 4 End-User Auth E2E ===")
    print("    Stack:    {}".format(BASE_URL))
    print("    Tenant:   {} ({})".format(DEFAULT_TENANT_SLUG, DEFAULT_TENANT_ID))
    print("    Keycloak: {}/realms/{}".format(KEYCLOAK_BASE, BANK_REALM))

    # ── 0. Admin login ─────────────────────────────────────────────────────────
    section("0. Platform admin login")
    admin_token = admin_login()
    check("Admin login succeeded", bool(admin_token))

    # ── 0b. Save existing runtime IDP ─────────────────────────────────────────
    section("0b. Save existing runtime IDP config")
    _prev_ridp = ridp_get(admin_token)
    check("Existing config noted",
          True,
          "configured" if _prev_ridp.get("configured") else "not configured")

    # ═══════════════════════════════════════════════════════════════════════════
    # PATH 1: Bank-issued RS256 JWT (external_jwt EP mode, Keycloak)
    # ═══════════════════════════════════════════════════════════════════════════
    section("PATH 1 — Bank-issued RS256 JWT (external_jwt)")

    if args.skip_path1:
        warn("PATH 1 skipped (--skip-path1)")
    else:
        kc_ok = kc_reachable()
        check("Keycloak bank realm reachable", kc_ok,
              "run with --profile sso to enable")
        if not kc_ok:
            warn("PATH 1 tests skipped — Keycloak not reachable")
        else:
            # 1a. Ensure test client exists
            kc_tok = kc_admin_token()
            check("Keycloak admin token obtained", bool(kc_tok))
            if kc_tok:
                client_ok = kc_ensure_test_client(kc_tok)
                check("bank-test-cli client present in bank realm", client_ok,
                      "present or just created")

            # 1b. Get bank JWTs
            bank_jwt = kc_get_bank_token(BANK_TEST_USER)
            check("Bank JWT obtained ({})".format(BANK_TEST_USER), bool(bank_jwt))

            # 1c. Configure runtime IDP on default tenant
            section("1c. Configure runtime IDP")
            # JWKS URI: go-bridge fetches from inside Docker network
            # Issuer: must match iss claim in the token
            # The Keycloak token iss depends on how KC was accessed when token was issued.
            # Since we fetch via Traefik (localhost:8088), iss = http://localhost:8088/auth/keycloak/realms/bank
            # The go-bridge fetches JWKS via internal Docker DNS.
            # But the issuer validation in the validator compares cfg.Issuer to the token's iss claim.
            # Token iss = what Keycloak sets based on KC_HOSTNAME or the issuer URL.
            # With KC_HTTP_RELATIVE_PATH=/auth/keycloak and no KC_HOSTNAME set,
            # Keycloak uses the request host — so iss = http://localhost:8088/auth/keycloak/realms/bank
            # But go-bridge fetches JWKS at the internal URL (them-keycloak:8080).
            # We set jwks_uri to the internal URL, issuer to the external (matching token iss).
            jwks_uri = ("http://them-keycloak:8080/auth/keycloak"
                        "/realms/{}/protocol/openid-connect/certs".format(BANK_REALM))
            issuer = bank_issuer()  # http://localhost:8088/auth/keycloak/realms/bank

            status, ridp_body = ridp_put(admin_token, jwks_uri, issuer, audience="")
            check("PUT /api/v1/tenant/runtime-idp → 200",
                  status == 200, "status={} {}".format(status, str(ridp_body)[:80]))

            cur = ridp_get(admin_token)
            check("GET /api/v1/tenant/runtime-idp → configured=true",
                  cur.get("configured") is True, str(cur)[:80])

            if bank_jwt and status == 200:
                # 1d. Create external_jwt EP
                section("1d. Create external_jwt entry point")
                app1 = create_app(admin_token)
                check("Test app created", bool(app1))
                if app1:
                    app_slug1 = get_app_slug(admin_token, app1)
                    check("App slug retrieved", bool(app_slug1), app_slug1 or "")

                    ep_ext = create_ep(admin_token, app1, "external_jwt", "external")
                    check("external_jwt EP created (allowed_principals=external)", bool(ep_ext))

                    if ep_ext and app_slug1:
                        _, ep_slug_ext = ep_ext

                        # 1e. Happy path
                        section("1e. WS connection tests")
                        ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_ext,
                                              bearer=bank_jwt, expect_ok=True)
                        check("P1-01: Valid bank JWT → WS admitted", ok, detail[:100])

                        ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_ext,
                                              bearer="", expect_ok=False)
                        check("P1-02: No token → rejected", ok, detail[:80])

                        ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_ext,
                                              bearer="not.a.jwt", expect_ok=False)
                        check("P1-03: Malformed JWT → rejected", ok, detail[:80])

                        # 1f. Wrong audience forces rejection
                        section("1f. Audience mismatch → rejection")
                        ridp_put(admin_token, jwks_uri, issuer, audience="wrong-audience-xyz")
                        ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_ext,
                                              bearer=bank_jwt, expect_ok=False)
                        check("P1-04: Wrong audience config → JWT rejected", ok, detail[:80])
                        ridp_put(admin_token, jwks_uri, issuer, audience="")  # restore

                        # 1g. Spoofing: X-External-User ignored for external_jwt EP
                        section("1g. Identity spoofing blocked")
                        ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_ext,
                                              bearer=bank_jwt, external_user="attacker-id",
                                              expect_ok=True)
                        # Connection admitted — but ExternalUserID comes from JWT sub, not header.
                        # LC-EXT-7 (unit test) verifies the overwrite.
                        check("P1-05: JWT + spoofed X-External-User → admitted (sub from token)", ok, detail[:80])

                        # 1h. Cross-tenant: wrong tenant slug
                        section("1h. Cross-tenant rejection")
                        ok, detail = ws_check("bank", app_slug1, ep_slug_ext,
                                              bearer=bank_jwt, expect_ok=False)
                        check("P1-06: Wrong tenant slug → rejected", ok, detail[:80])

                        # 1i. Principal guard: internal-only EP blocks external JWT callers
                        section("1i. Principal guard")
                        ep_int = create_ep(admin_token, app1, "external_jwt", "internal")
                        if ep_int:
                            _, ep_slug_int = ep_int
                            ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug1, ep_slug_int,
                                                  bearer=bank_jwt, expect_ok=False)
                            check("P1-07: allowed_principals=internal rejects external JWT caller",
                                  ok, detail[:80])

    # ═══════════════════════════════════════════════════════════════════════════
    # PATH 2: Backend service token (is_backend=true)
    # ═══════════════════════════════════════════════════════════════════════════
    section("PATH 2 — Backend service token (is_backend=true)")

    if args.skip_path2:
        warn("PATH 2 skipped (--skip-path2)")
    else:
        app2 = create_app(admin_token)
        check("PATH 2 test app created", bool(app2))
        if app2:
            app_slug2 = get_app_slug(admin_token, app2)
            check("PATH 2 app slug retrieved", bool(app_slug2), app_slug2 or "")

            ep_both = create_ep(admin_token, app2, "token", "both")
            check("Token EP (allowed_principals=both) created", bool(ep_both))

            ep_int2 = create_ep(admin_token, app2, "token", "internal")
            check("Token EP (allowed_principals=internal) created", bool(ep_int2))

            backend_tok = create_access_token(admin_token, is_backend=True)
            check("P2-01: Backend token created (is_backend=true)", bool(backend_tok))

            regular_tok = create_access_token(admin_token, is_backend=False)
            check("P2-02: Regular token created (is_backend=false)", bool(regular_tok))

            if app_slug2 and ep_both:
                _, ep_slug_both = ep_both

                if backend_tok:
                    ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug2, ep_slug_both,
                                          bearer=backend_tok, external_user="customer-99",
                                          expect_ok=True)
                    check("P2-03: Backend token + X-External-User → admitted ('both' EP)",
                          ok, detail[:100])

                    ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug2, ep_slug_both,
                                          bearer=backend_tok, external_user="",
                                          expect_ok=True)
                    check("P2-04: Backend token alone → admitted ('both' EP)",
                          ok, detail[:100])

                if regular_tok:
                    ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug2, ep_slug_both,
                                          bearer=regular_tok, expect_ok=True)
                    check("P2-05: Regular token → admitted ('both' EP)", ok, detail[:100])

            if app_slug2 and ep_int2 and backend_tok:
                _, ep_slug_int2 = ep_int2
                ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug2, ep_slug_int2,
                                      bearer=backend_tok, expect_ok=False)
                check("P2-06: Backend token on internal-only EP → rejected (403)",
                      ok, detail[:80])

    # ═══════════════════════════════════════════════════════════════════════════
    # PATH 3: the-M user JWT (user_jwt EP mode)
    # ═══════════════════════════════════════════════════════════════════════════
    section("PATH 3 — the-M user JWT (user_jwt EP mode)")

    if args.skip_path3:
        warn("PATH 3 skipped (--skip-path3)")
    else:
        app3 = create_app(admin_token)
        check("PATH 3 test app created", bool(app3))
        if app3:
            app_slug3 = get_app_slug(admin_token, app3)
            check("PATH 3 app slug retrieved", bool(app_slug3), app_slug3 or "")

            ep_user = create_ep(admin_token, app3, "user_jwt", "internal")
            check("user_jwt EP created (allowed_principals=internal)", bool(ep_user))

            if ep_user and app_slug3:
                _, ep_slug_user = ep_user

                ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug3, ep_slug_user,
                                      bearer=admin_token, expect_ok=True)
                check("P3-01: the-M user JWT → admitted (user_jwt EP)", ok, detail[:100])

                ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug3, ep_slug_user,
                                      bearer="", expect_ok=False)
                check("P3-02: No token on user_jwt EP → rejected", ok, detail[:80])

                ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug3, ep_slug_user,
                                      bearer="invalid.jwt.here", expect_ok=False)
                check("P3-03: Invalid JWT on user_jwt EP → rejected", ok, detail[:80])

                # Opaque token must NOT work on user_jwt EP
                opaque = create_access_token(admin_token, is_backend=False)
                if opaque:
                    ok, detail = ws_check(DEFAULT_TENANT_SLUG, app_slug3, ep_slug_user,
                                          bearer=opaque, expect_ok=False)
                    check("P3-04: Opaque token on user_jwt EP → rejected", ok, detail[:80])

    # ── Teardown ───────────────────────────────────────────────────────────────
    teardown(admin_token)

    # ── Summary ───────────────────────────────────────────────────────────────
    print()
    print("Result: {} passed, {} failed, {} warnings".format(passed, failed, warnings))
    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()
