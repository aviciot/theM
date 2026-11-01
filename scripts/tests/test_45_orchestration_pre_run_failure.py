#!/usr/bin/env python3.12
"""
test_45_orchestration_pre_run_failure.py — orchestration runs that fail before the loop are reported

Orchestration path (WS entry point -> OrchestrationWorkflow -> RunOrchestratorActivity), driven through
the test-runner against payops_ai `debator-9f9480` / public websocket EP `my-ws` (mock LLM).

  1. baseline: a normal run connects, gets a reply, and the run row is `completed`
  2. the EP's LLM is switched to a provider with no key (openai) so the config load fails BEFORE
     orchestrator.Run starts: the client must receive an error ("run could not start ...") and the run
     row must be `failed` with ended_at set and the reason in `error` (it used to hang and stay `running`)
  3. the EP's LLM is restored to mock/mock (always, even on failure)

Prerequisites: stack up with --profile temporal; test-runner running (`cd test-runner && docker compose up -d`);
payops-test / admin123. Usage: python3.12 scripts/tests/test_45_orchestration_pre_run_failure.py
Exit code 0 = pass. Dev stack only.
"""
import json, subprocess, sys, time, urllib.request

A, R = "http://localhost:8088", "http://localhost:8093"
APP, EP, TENANT_ID = "debator-9f9480", "my-ws", "ad7d567d-b222-40be-acb6-7929cf6ae168"
FAILS = []


def ok(name, cond, detail=""):
    print(f"  {'PASS' if cond else 'FAIL'}  {name}" + ("" if cond else f"  [{detail}]"))
    if not cond:
        FAILS.append(name)


def call(method, url, token=None, body=None):
    req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", **({"Authorization": f"Bearer {token}"} if token else {})})
    raw = urllib.request.urlopen(req, timeout=90).read()
    return json.loads(raw) if raw.strip() else {}


def sql(q):
    return subprocess.run(["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-At", "-c", q], capture_output=True, text=True).stdout.strip()


def run_once(app_id):
    sc = call("POST", f"{R}/scenarios", body={"name": "tmp test_45", "tenant_slug": "payops_ai", "tenant_id": TENANT_ID, "app_id": app_id,
                                               "app_slug": APP, "ep_slug": EP, "ep_type": "websocket", "auth_mode": "public",
                                               "n_users": 1, "messages": ["hello"]})
    try:
        rid = call("POST", f"{R}/run", body={"scenario_id": sc["id"]})["run_id"]
        for _ in range(30):
            time.sleep(2)
            try:
                return call("GET", f"{R}/history/{rid}")["results"][0]
            except Exception:
                pass
    finally:
        urllib.request.urlopen(urllib.request.Request(f"{R}/scenarios/{sc['id']}", method="DELETE"))
    return {}


def latest_run():
    return sql("select status||'|'||coalesce(error,'')||'|'||(ended_at is not null) from them.runs where entry_point_slug='%s' "
               "and tenant_id='%s' order by started_at desc limit 1" % (EP, TENANT_ID)).split("|")


def main():
    tok = call("POST", f"{A}/auth/api/v1/auth/login", body={"username": "payops-test", "password": "admin123"})["access_token"]
    app_id = sql(f"select id::text from them.applications where slug='{APP}'")
    ep_id = sql(f"select id::text from them.entry_points where application_id='{app_id}' and slug='{EP}'")
    llm_url = f"{A}/api/v1/admin/applications/{app_id}/entry-points/{ep_id}/llm"
    orig = sql(f"select coalesce(llm_provider,'')||'/'||coalesce(llm_model,'') from them.entry_points where id='{ep_id}'").split("/")

    print("\n[1] baseline")
    r = run_once(app_id)
    ok("baseline: client connected and got a reply", bool(r.get("connected")) and any(s.get("ok") for s in (r.get("steps") or [])), str(r)[:200])
    time.sleep(2)
    st = latest_run()
    ok("baseline: run completed", st[0] == "completed", str(st))

    try:
        print("\n[2] config load fails before the loop")
        call("PATCH", llm_url, tok, {"llm_provider": "openai", "llm_model": "gpt-4o"})
        time.sleep(2)
        r = run_once(app_id)
        errs = " ".join((s.get("error") or "") for s in (r.get("steps") or [])) + " " + (r.get("error") or "")
        ok("client receives an error event", "could not start" in errs, errs[:200])
        time.sleep(3)
        st = latest_run()
        ok("run row is failed", st[0] == "failed", str(st))
        ok("failure reason stored in runs.error", "could not start" in (st[1] if len(st) > 1 else ""), str(st))
        ok("ended_at is set", len(st) > 2 and st[2] in ("t", "true"), str(st))
    finally:
        call("PATCH", llm_url, tok, {"llm_provider": orig[0] or None, "llm_model": orig[1] or None})
        print(f"\n[3] restored EP LLM to {orig[0]}/{orig[1]}")

    print(f"\n{'ALL PASSED' if not FAILS else 'FAILED: ' + ', '.join(FAILS)}")
    sys.exit(1 if FAILS else 0)


main()
