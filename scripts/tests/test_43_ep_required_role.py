#!/usr/bin/env python3.12
"""
test_43_ep_required_role.py — per-entry-point required tenant role (external_jwt) E2E

Drives the test-runner (creates and deletes its own temporary scenarios through the runner API)
against payops_ai `smart-refund-handler-97f244` / EP `smart-refund-ws`, changing the EP's
required role through the admin API between runs.

Users (Keycloak realm payops_ai): alice tier=premium -> role jwt-premium, bob tier=standard and
qa (no tier claim) -> no mapped role. Roles/mappings come from payops_ai's tenant_roles.

Cases
  A  required=jwt-premium : alice admitted, bob denied (403), qa denied (403)
  B  required cleared     : bob admitted                      (change takes effect without republish)
  C  required=m2m-partner : alice denied                      (alice's role is jwt-premium)
  D  required=jwt-premium : alice admitted again              (switching back works)
The EP is left with required_role=jwt-premium.

Prerequisites
  - stack up with --profile temporal and --profile sso; test-runner running (`cd test-runner && docker compose up -d`)
  - the EP set to external_jwt (this script runs test-runner/scripts/setup_ep_role_scenario.py if it is not)
  - KEYCLOAK_URL must use the issuer host configured in the tenant runtime IdP
    (default http://10.55.125.43:8088/auth/keycloak; see docs/architecture/test-runner.md)

Usage: python3.12 scripts/tests/test_43_ep_required_role.py [--base-url URL] [--runner-url URL]
Exit code 0 = all cases passed. Dev stack only: creates runs and uses real logins.
"""
import argparse, json, os, subprocess, sys, time, urllib.request, urllib.error

APP_SLUG, EP_SLUG, TENANT_SLUG = "smart-refund-handler-97f244", "smart-refund-ws", "payops_ai"
TENANT_ID = "ad7d567d-b222-40be-acb6-7929cf6ae168"
KEYCLOAK_URL = os.environ.get("KEYCLOAK_URL", "http://10.55.125.43:8088/auth/keycloak")
USERS = {"alice": "alice123", "bob": "bob123", "qa": "admin123"}
SETUP = os.path.join(os.path.dirname(__file__), "..", "..", "test-runner", "scripts", "setup_ep_role_scenario.py")


def http(method, url, token=None, body=None, timeout=60):
    req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", **({"Authorization": f"Bearer {token}"} if token else {})})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {url} -> HTTP {e.code}: {e.read().decode()[:300]}")


class Env:
    def __init__(self, base, runner):
        self.base, self.runner = base, runner
        self.token = http("POST", f"{base}/auth/api/v1/auth/login", body={"username": "payops-test", "password": "admin123"})["access_token"]
        apps = http("GET", f"{base}/api/v1/admin/applications", self.token)
        apps = apps if isinstance(apps, list) else apps.get("applications", apps.get("items", []))
        self.app_id = next(a["id"] for a in apps if a["slug"] == APP_SLUG)
        self.ep = self._ep()

    def _ep(self):
        app = http("GET", f"{self.base}/api/v1/admin/applications/{self.app_id}", self.token)
        return next(e for e in app["entry_points"] if e["slug"] == EP_SLUG)

    def ensure_external_jwt(self):
        if (self.ep.get("access_policy") or {}).get("mode") != "external_jwt":
            print("EP is not external_jwt — running setup script")
            subprocess.run([sys.executable, SETUP, "--base-url", self.base], check=True)
            self.ep = self._ep()

    def set_role(self, role):
        http("PATCH", f"{self.base}/api/v1/admin/applications/{self.app_id}/entry-points/{self.ep['id']}/required-role",
             self.token, {"required_role": role})
        time.sleep(2)  # cache invalidation

    def run_as(self, user):
        """Runs one virtual user through the test-runner; returns the raw per-user result."""
        sc = http("POST", f"{self.runner}/scenarios", body={
            "name": f"tmp test_43 {user}", "tenant_slug": TENANT_SLUG, "tenant_id": TENANT_ID,
            "app_id": self.app_id, "app_slug": APP_SLUG, "ep_slug": EP_SLUG, "ep_type": "websocket",
            "auth_mode": "external_jwt", "keycloak_url": KEYCLOAK_URL, "keycloak_realm": "payops_ai",
            "keycloak_client_id": "them-m", "keycloak_client_secret": "them-m-secret",
            "keycloak_users": [{"username": user, "password": USERS[user]}],
            "n_users": 1, "messages": ["I want a refund for order 1234", "50"]})
        try:
            run_id = http("POST", f"{self.runner}/run", body={"scenario_id": sc["id"]})["run_id"]
            for _ in range(40):
                time.sleep(3)
                try:
                    return http("GET", f"{self.runner}/history/{run_id}")["results"][0]
                except SystemExit:
                    continue
            raise SystemExit(f"run for {user} did not finish")
        finally:
            urllib.request.urlopen(urllib.request.Request(f"{self.runner}/scenarios/{sc['id']}", method="DELETE"))


def admitted(r):
    return bool(r.get("connected")) and not r.get("http_status")


def denied(r):
    return not r.get("connected") and r.get("http_status") == 403


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://localhost:8088")
    ap.add_argument("--runner-url", default="http://localhost:8093")
    a = ap.parse_args()
    env = Env(a.base_url, a.runner_url)
    env.ensure_external_jwt()

    results = []

    def check(case, role, user, want):
        env.set_role(role)
        r = env.run_as(user)
        ok = admitted(r) if want == "admitted" else denied(r)
        results.append(ok)
        print(f"{'PASS' if ok else 'FAIL'}  {case}: required={role or '-'} user={user} expected={want} "
              f"(connected={r.get('connected')} http={r.get('http_status')} err={str(r.get('error'))[:60]})")

    try:
        check("A1", "jwt-premium", "alice", "admitted")
        check("A2", "jwt-premium", "bob", "denied")
        check("A3", "jwt-premium", "qa", "denied")
        check("B", None, "bob", "admitted")
        check("C", "m2m-partner", "alice", "denied")
        check("D", "jwt-premium", "alice", "admitted")
    finally:
        env.set_role("jwt-premium")

    passed = sum(results)
    print(f"\n{passed}/{len(results)} passed")
    sys.exit(0 if passed == len(results) else 1)


main()
