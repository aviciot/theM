#!/usr/bin/env python3.12
"""Prepare payops_ai `smart-refund-handler` for the EP required-role scenarios (51/52).

Sets the websocket EP to access_mode=external_jwt (allowed_principals=both), publishes,
then sets required_role=jwt-premium. Idempotent. `--inspect` only prints the current state.
`--reset` puts the EP back to token mode and clears the role.

Usage: python3.12 test-runner/scripts/setup_ep_role_scenario.py [--inspect|--reset] [--base-url URL]
"""
import argparse, copy, json, sys, urllib.request, urllib.error

APP_SLUG, EP_SLUG, ROLE = "smart-refund-handler-97f244", "smart-refund-ws", "jwt-premium"
USER, PASSWORD = "payops-test", "admin123"


def call(base, method, path, token=None, body=None):
    req = urllib.request.Request(base + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", **({"Authorization": f"Bearer {token}"} if token else {})})
    try:
        with urllib.request.urlopen(req) as r:
            raw = r.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path} -> HTTP {e.code}: {e.read().decode()[:300]}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://localhost:8088")
    ap.add_argument("--inspect", action="store_true")
    ap.add_argument("--reset", action="store_true")
    a = ap.parse_args()
    b = a.base_url

    tok = call(b, "POST", "/auth/api/v1/auth/login", body={"username": USER, "password": PASSWORD})["access_token"]
    apps = call(b, "GET", "/api/v1/admin/applications", tok)
    apps = apps if isinstance(apps, list) else apps.get("applications", apps.get("items", []))
    app = next(x for x in apps if x["slug"] == APP_SLUG)
    app_id = app["id"]
    full = call(b, "GET", f"/api/v1/admin/applications/{app_id}", tok)
    ep = next(e for e in full["entry_points"] if e["slug"] == EP_SLUG)
    print(f"app_id={app_id} tenant_id={full.get('tenant_id')}\nep_id={ep['id']} access_policy={ep['access_policy']} required_role={ep.get('required_role')}")

    defs = call(b, "GET", f"/api/v1/admin/applications/{app_id}/definitions", tok)
    defs = defs if isinstance(defs, list) else defs.get("definitions", defs.get("items", []))
    latest = defs[0]
    d = call(b, "GET", f"/api/v1/admin/applications/{app_id}/definitions/{latest['id']}", tok)
    doc = d.get("definition", d)
    ep_def = next(e for e in doc["entry_points"] if e["slug"] == EP_SLUG)
    print("definition EP config:", json.dumps(ep_def.get("config")))
    if a.inspect:
        return

    mode, principals, role = ("token", "internal", None) if a.reset else ("external_jwt", "both", ROLE)
    new = copy.deepcopy(doc)
    cfg = next(e for e in new["entry_points"] if e["slug"] == EP_SLUG).setdefault("config", {})
    cfg["access_mode"], cfg["allowed_principals"] = mode, principals
    created = call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions", tok, {"definition": new})
    did = created["id"]
    v = call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions/{did}/validate", tok)
    print("validate:", json.dumps(v)[:200])
    call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions/{did}/publish", tok)
    call(b, "PATCH", f"/api/v1/admin/applications/{app_id}/entry-points/{ep['id']}/required-role", tok, {"required_role": role})
    print(f"done: mode={mode} principals={principals} required_role={role}")


main()
