#!/usr/bin/env python3.12
"""
test_44_appflow_hil_timeout.py — HIL timer firing marks the approval timed_out (F-124)

No agent needed. Builds an app on the default tenant: EP -> HIL gate (timeout 5 s, fallback approve)
-> transform node, starts it through the admin debug API (no LLM nodes, so no overrides), and does
NOT approve. Expected:
  - the hil_approvals row ends `timed_out` (it used to stay `pending` forever)
  - the run ends `completed` (the approve fallback lets the flow continue)
  - no run_steps / tasks / approvals are left open
Also runs a second app where a human approves before the timer: the row must end `approved`.

Prerequisites: stack up with --profile temporal; admin / admin123 (or ADMIN_JWT).
Usage: python3.12 scripts/tests/test_44_appflow_hil_timeout.py
Exit code 0 = pass. Dev stack only; it deletes the apps and run rows it creates.
"""
import json, os, subprocess, sys, time, urllib.request, urllib.error

BASE = "http://localhost:8088"
FAILS = []


def ok(name, cond, detail=""):
    print(f"  {'PASS' if cond else 'FAIL'}  {name}" + ("" if cond else f"  [{detail}]"))
    if not cond:
        FAILS.append(name)
    return cond


def api(method, path, token, body=None):
    req = urllib.request.Request(f"{BASE}/api/v1{path}", method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return r.status, json.loads(raw) if raw.strip() else {}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        return e.code, json.loads(raw) if raw.strip().startswith("{") else {}


def login():
    if os.environ.get("ADMIN_JWT"):
        return os.environ["ADMIN_JWT"]
    req = urllib.request.Request(f"{BASE}/auth/api/v1/auth/login", method="POST", data=json.dumps({"username": "admin", "password": "admin123"}).encode(),
                                 headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=15))["access_token"]


def psql(sql):
    r = subprocess.run(["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-At", "-c", sql], capture_output=True, text=True, timeout=15)
    if r.returncode != 0:
        raise RuntimeError(r.stderr.strip())
    return r.stdout.strip()


def definition(ep_slug, hil_timeout):
    return {
        "schema_version": 2, "name": "HIL timeout E2E", "execution_backend": "temporal",
        "components": [
            {"instance_id": "hil_1", "name": "HIL", "definition_ref": {"kind": "flow_control", "namespace": "them.builtin", "name": "hil", "version": 1},
             "config": {"approver_role": "admin", "timeout_seconds": hil_timeout, "fallback_action": "approve", "prompt": "approve?"}},
            {"instance_id": "tf_1", "name": "Shout", "definition_ref": {"kind": "inline", "namespace": "builtin", "name": "transform", "version": 1},
             "config": {"functions": [{"fn": "upper", "input_var": "input", "output_var": "shout"}]}},
        ],
        "connections": [{"source": "hil_1", "target": "tf_1", "type": "flow_control"}],
        "entry_points": [{"instance_id": "ep_1", "slug": ep_slug, "protocol": "websocket", "root": "hil_1", "config": {}}],
    }


def wait_for(cond, secs):
    end = time.time() + secs
    while time.time() < end:
        v = cond()
        if v:
            return v
        time.sleep(2)
    return None


def build_and_start(token, tag, hil_timeout):
    slug = f"hil-timeout-{tag}-{int(time.time())}"
    s, b = api("POST", "/admin/applications", token, {"name": slug, "slug": slug, "enabled": True})
    if not ok(f"[{tag}] create app", s == 201, f"{s} {b}"):
        return None, None
    app_id = b["id"]
    s, _ = api("POST", f"/admin/applications/{app_id}/entry-points", token, {"slug": slug, "entry_point_type": "websocket", "enabled": True})
    ok(f"[{tag}] create entry point", s == 201, str(s))
    s, b = api("POST", f"/admin/applications/{app_id}/definitions", token, {"definition": definition(slug, hil_timeout)})
    if not ok(f"[{tag}] create definition", s == 201, f"{s} {b}"):
        return app_id, None
    s, b = api("POST", f"/admin/applications/{app_id}/debug/start", token, {"entry_point_slug": slug, "user_message": "hello", "llm_overrides": {}})
    if not ok(f"[{tag}] debug start", s in (200, 201, 202), f"{s} {b}"):
        return app_id, None
    return app_id, b.get("run_id")


def children_open(run_id):
    return psql(f"""select (select count(*) from them.run_steps where run_id='{run_id}' and status in ('running','pending'))
      + (select count(*) from them.tasks where run_id='{run_id}' and state in ('submitted','working','input-required'))
      + (select count(*) from them.hil_approvals where run_id='{run_id}' and status='pending')""")


def main():
    token = login()
    apps, runs = [], []
    try:
        print("\n[A] timer fires, nobody decides")
        app_id, run_id = build_and_start(token, "a", 5)
        if app_id:
            apps.append(app_id)
        if run_id:
            runs.append(run_id)
            ok("[A] approval row appears pending", wait_for(lambda: psql(f"select status from them.hil_approvals where run_id='{run_id}'") == "pending", 20) is not None)
            ok("[A] approval becomes timed_out", wait_for(lambda: psql(f"select status from them.hil_approvals where run_id='{run_id}'") == "timed_out", 40) is not None,
               psql(f"select status from them.hil_approvals where run_id='{run_id}'"))
            ok("[A] run completes (approve fallback)", wait_for(lambda: psql(f"select status from them.runs where id='{run_id}'") == "completed", 40) is not None,
               psql(f"select status from them.runs where id='{run_id}'"))
            ok("[A] nothing left open", children_open(run_id) == "0", children_open(run_id))

        print("\n[B] human approves before the timer")
        app_id, run_id = build_and_start(token, "b", 300)
        if app_id:
            apps.append(app_id)
        if run_id:
            runs.append(run_id)
            ok("[B] approval row appears pending", wait_for(lambda: psql(f"select status from them.hil_approvals where run_id='{run_id}'") == "pending", 20) is not None)
            s, b = api("POST", f"/runs/{run_id}/hil/hil_1/approve", token, {"comment": "ok"})
            ok("[B] approve API", s in (200, 202, 204), f"{s} {b}")
            ok("[B] run completes", wait_for(lambda: psql(f"select status from them.runs where id='{run_id}'") == "completed", 40) is not None,
               psql(f"select status from them.runs where id='{run_id}'"))
            ok("[B] approval stays approved (not timed_out)", psql(f"select status from them.hil_approvals where run_id='{run_id}'") == "approved",
               psql(f"select status from them.hil_approvals where run_id='{run_id}'"))
    finally:
        for r in runs:
            psql(f"delete from them.hil_approvals where run_id='{r}'")
            psql(f"delete from them.tasks where run_id='{r}'")
            psql(f"delete from them.runs where id='{r}'")
        for a in apps:
            api("DELETE", f"/admin/applications/{a}", token)
    print(f"\n{'ALL PASSED' if not FAILS else 'FAILED: ' + ', '.join(FAILS)}")
    sys.exit(1 if FAILS else 0)


main()
