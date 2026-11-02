#!/usr/bin/env python3.12
"""Prepare payops_ai `smart-refund-handler` for the EP required-role scenarios (51-56).

One app, four websocket entry points (all external_jwt, allowed_principals=both, same flow):
  smart-refund-ws   needs role jwt-premium   (the original EP)
  role-premium-ws   needs role jwt-premium   (a second EP with the same rule)
  role-partner-ws   needs role m2m-partner   (a role nobody in the test realm has)
  role-open-ws      needs no role            (any valid login is admitted)
Idempotent. `--inspect` only prints the current state.
`--reset` puts all of them back to token mode and clears the roles.

Usage: python3.12 test-runner/scripts/setup_ep_role_scenario.py [--inspect|--reset] [--base-url URL]
"""
import argparse, copy, json, sys, urllib.request, urllib.error

APP_SLUG, EP_SLUG, ROLE = "smart-refund-handler-97f244", "smart-refund-ws", "jwt-premium"
EXTRA_EPS = {"role-premium-ws": "jwt-premium", "role-partner-ws": "m2m-partner", "role-open-ws": None}
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

    mode, principals = ("token", "internal") if a.reset else ("external_jwt", "both")
    wanted = {EP_SLUG: None if a.reset else ROLE}
    wanted.update({k: None if a.reset else v for k, v in EXTRA_EPS.items()})
    new = copy.deepcopy(doc)
    for n, slug in enumerate(wanted):
        ep_new = next((e for e in new["entry_points"] if e["slug"] == slug), None)
        if ep_new is None:
            ep_new = {"instance_id": f"ep_role_{n}", "slug": slug, "protocol": "websocket", "root": ep_def["root"], "config": {}}
            new["entry_points"].append(ep_new)
        cfg = ep_new["config"] = ep_new.get("config") or {}
        cfg["access_mode"], cfg["allowed_principals"] = mode, principals
    have = {e["slug"] for e in full["entry_points"]}
    for slug in wanted:
        if slug not in have:
            call(b, "POST", f"/api/v1/admin/applications/{app_id}/entry-points", tok,
                 {"slug": slug, "entry_point_type": "websocket", "enabled": True})
    created = call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions", tok, {"definition": new})
    did = created["id"]
    v = call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions/{did}/validate", tok)
    print("validate:", json.dumps(v)[:200])
    call(b, "POST", f"/api/v1/admin/applications/{app_id}/definitions/{did}/publish", tok)
    full = call(b, "GET", f"/api/v1/admin/applications/{app_id}", tok)
    ids = {e["slug"]: e["id"] for e in full["entry_points"]}
    for slug, role in wanted.items():
        call(b, "PATCH", f"/api/v1/admin/applications/{app_id}/entry-points/{ids[slug]}/required-role", tok, {"required_role": role})
        print(f"done: {slug} mode={mode} principals={principals} required_role={role}")


main()
