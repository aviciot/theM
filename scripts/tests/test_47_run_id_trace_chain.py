#!/usr/bin/env python3.12
"""
test_47_run_id_trace_chain.py — the App Canvas run id reaches the tool-call event (run id = trace key)

Chain under test (backlog F-127):
  AppFlow agent node -> A2A call with X-Them-Run-Id -> agent-runtime -> ActivityIC.RunID -> mcp_call step
  (agentgen.WithRunID) -> run_id to them-mcp-service -> ClickHouse events_tool_call (run_id, trace_id)

Builds a throwaway app on the default tenant: EP -> one agent node (the fixture canvas agent
`mcp3_e2e_test_1787838063`, whose only step calls smoke-mcp/get_latest_session), starts it through the
admin debug API, then expects an events_tool_call row whose run_id AND trace_id equal the run's id.
Skips (exit 0) when the fixture agent or MCP server is not in this database.

Prerequisites: stack with --profile temporal, --profile events and the agents profile; admin/admin123.
Usage: python3.12 scripts/tests/test_47_run_id_trace_chain.py     Dev stack only; deletes what it creates.
"""
import json, os, subprocess, sys, time, urllib.request, urllib.error

BASE = "http://localhost:8088"
TENANT = "00000000-0000-0000-0000-000000000001"
AGENT = "mcp3_e2e_test_1787838063"
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
        with urllib.request.urlopen(req, timeout=60) as r:
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
    r = subprocess.run(["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-At", "-c", sql], capture_output=True, text=True, timeout=20)
    if r.returncode != 0:
        raise RuntimeError(r.stderr.strip())
    return r.stdout.strip()


def clickhouse(sql):
    r = subprocess.run(["docker", "exec", "them-clickhouse", "clickhouse-client", "-q", sql], capture_output=True, text=True, timeout=20)
    if r.returncode != 0:
        raise RuntimeError(r.stderr.strip())
    return r.stdout.strip()


def wait_for(cond, secs):
    end = time.time() + secs
    while time.time() < end:
        v = cond()
        if v:
            return v
        time.sleep(2)
    return None


def main():
    if not psql(f"select id from them.component_definitions where kind='agent' and name='{AGENT}' and status='published'") \
            or psql("select id from them.mcp_servers where slug='smoke-mcp'") == "":
        print("SKIP: fixture agent or smoke-mcp server not present in this database")
        return 0
    token = login()
    slug = f"runid-trace-{int(time.time())}"
    app_id = run_id = None
    try:
        s, b = api("POST", "/admin/applications", token, {"name": slug, "slug": slug, "enabled": True})
        if not ok("create app", s == 201, f"{s} {b}"):
            return 1
        app_id = b["id"]
        s, _ = api("POST", f"/admin/applications/{app_id}/entry-points", token, {"slug": slug, "entry_point_type": "websocket", "enabled": True})
        ok("create entry point", s == 201, str(s))
        agent_id = psql(f"select id from them.agents where slug='{AGENT}' and tenant_id='{TENANT}'")
        s, b = api("POST", f"/admin/applications/{app_id}/agent-bindings/{agent_id}", token, {"credentials": {}})
        ok("bind the agent to the app", s in (200, 201, 204), f"{s} {b}")
        doc = {"schema_version": 2, "name": "run id trace", "execution_backend": "temporal",
               "components": [{"instance_id": "agent_1", "name": "MCP agent",
                               "definition_ref": {"kind": "agent", "namespace": TENANT, "name": AGENT, "version": 1}, "config": {}}],
               "connections": [],
               "entry_points": [{"instance_id": "ep_1", "slug": slug, "protocol": "websocket", "root": "agent_1", "config": {}}]}
        s, b = api("POST", f"/admin/applications/{app_id}/definitions", token, {"definition": doc})
        if not ok("create definition", s == 201, f"{s} {b}"):
            return 1
        s, b = api("POST", f"/admin/applications/{app_id}/debug/start", token, {"entry_point_slug": slug, "user_message": "go", "llm_overrides": {}})
        if not ok("debug start", s in (200, 201, 202), f"{s} {b}"):
            return 1
        run_id = b.get("run_id")
        status = wait_for(lambda: psql(f"select status from them.runs where id='{run_id}'") in ("completed", "failed") and psql(f"select status from them.runs where id='{run_id}'"), 90)
        ok("run finished", status is not None, psql(f"select status||' '||coalesce(error,'') from them.runs where id='{run_id}'"))
        ok("run completed (agent + MCP tool call worked)", status == "completed", psql(f"select coalesce(error,'') from them.runs where id='{run_id}'"))

        got = wait_for(lambda: clickhouse(f"select count() from them_events.events_tool_call where run_id = '{run_id}'") not in ("", "0"), 40)
        ok("tool-call event carries the run id (run_id)", got is not None, "no events_tool_call row with this run_id after 40 s")
        if got:
            row = clickhouse(f"select toString(trace_id), tool_name from them_events.events_tool_call where run_id = '{run_id}' order by ts desc limit 1").split("\t")
            ok("tool-call event trace_id equals the run id", row[0] == run_id, str(row))
            ok("tool name recorded", row[1] == "get_latest_session", str(row))
    finally:
        if run_id:
            for t in ("hil_approvals", "tasks"):
                psql(f"delete from them.{t} where run_id='{run_id}'")
            psql(f"delete from them.runs where id='{run_id}'")
        if app_id:
            api("DELETE", f"/admin/applications/{app_id}", token)
    print(f"\n{'ALL PASSED' if not FAILS else 'FAILED: ' + ', '.join(FAILS)}")
    return 1 if FAILS else 0


sys.exit(main())
