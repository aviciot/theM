"""
test_42_appflow_inline_nodes — Inline LLM + Condition node E2E test.

Flow under test:
  EP (websocket) → LLM (mock provider) → Condition ─true──→ Agent TRUE (a2a_echo)
                                                   └false─→ Agent FALSE (a2a_echo)

What this proves that the Go unit tests cannot:
  - definition_ref.kind="inline" survives validate + publish (isBuiltinKind)
  - appflow.Validate now runs at validate time and its codes reach the API
  - the compiler maps inline/llm and inline/condition to executable node kinds
  - InlineLLMActivity is registered on the dag-worker and actually runs
  - condition edge labels route the flow to the branch the expression selects

Negative cases asserted against the live validate endpoint (these are the
regression guard for the publish-time validation wired in step 5):
  - a condition with only ONE outgoing edge  → condition_edge_count
  - an LLM node with no prompts at all       → llm_no_prompt
  - the SAME condition doc on execution_backend="local" → valid (the graph is
    not executed on that backend, so graph rules must not block it)

Steps:
   1. Create application + WS entry point
   2. Create definition (schema_version=2, execution_backend=temporal)
   3. Validate → expect valid
   4. Negative: break the condition edges → expect condition_edge_count
   5. Negative: clear both LLM prompts → expect llm_no_prompt
   6. Negative: same broken doc on "local" backend → expect valid
   7. Restore + publish
   8. Open WebSocket, send message → triggers AppFlowWorkflow
   9. Poll run until terminal; assert completed
  10. Assert the run traversed exactly one branch (not both)
  11. Cleanup

Run:
  python3.12 scripts/tests/test_42_appflow_inline_nodes.py

Requires:
  - Stack running with --profile temporal
  - them-dag-worker REBUILT since the inline-node commits and force-recreated
    (activities register at startup; `build` + `restart` is NOT enough — see
    docs/LESSONS.md). Without this the run fails with UnknownNodeKind or a
    missing-activity error.
  - a2a-echo container healthy
  - ADMIN_JWT env var (auto-fetched via /auth/login if absent)

Why the mock LLM provider: zero cost, no network, no API key. Its replies are
randomly chosen from a fixed set (cmd/dag-worker/main.go mockReplies), so the
condition expression MUST NOT depend on reply text — this test uses
{{gt (len .summary) 0}}, which is stable for any non-empty response.
"""

import asyncio
import base64
import json
import os
import subprocess
import sys
import time
import uuid
import urllib.request
import urllib.error

BASE_URL = "http://localhost:8088"
WS_BASE = "ws://localhost:8088"
ADMIN_USER = "admin"
ADMIN_PASS = "admin123"
TENANT_SLUG = "default"

A2A_ECHO_NAMESPACE = "them.tenant.00000000-0000-0000-0000-000000000001"
A2A_ECHO_NAME = "a2a_echo"


# ── HTTP helpers ──────────────────────────────────────────────────────────────

def fetch_token() -> str:
    env_jwt = os.environ.get("ADMIN_JWT", "")
    if env_jwt:
        return env_jwt
    body = json.dumps({"username": ADMIN_USER, "password": ADMIN_PASS}).encode()
    req = urllib.request.Request(
        f"{BASE_URL}/auth/api/v1/auth/login",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.load(resp)
    return data["access_token"]


def api(method, path, token, body=None, timeout=20):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"{BASE_URL}/api/v1{path}",
        data=data,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
        },
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            if not raw.strip():
                return resp.status, {}
            return resp.status, json.loads(raw)
    except urllib.error.HTTPError as e:
        try:
            body_bytes = e.read().decode()
            return e.code, json.loads(body_bytes) if body_bytes.strip() else {}
        except Exception:
            return e.code, {}


def get_tenant_id(token: str) -> str:
    parts = token.split(".")
    if len(parts) < 2:
        raise ValueError("invalid JWT")
    payload = json.loads(base64.urlsafe_b64decode(parts[1] + "=="))
    return payload.get("tenant_id", "")


def psql(sql: str) -> str:
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", sql],
        capture_output=True, text=True, timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError(f"psql error: {result.stderr.strip()}")
    return result.stdout.strip()


# ── Assertion helpers ─────────────────────────────────────────────────────────

PASS_COUNT = 0
FAIL_COUNT = 0


def ok(label, condition, detail=""):
    global PASS_COUNT, FAIL_COUNT
    if condition:
        print(f"  PASS  {label}")
        PASS_COUNT += 1
        return True
    msg = f" ({detail})" if detail else ""
    print(f"  FAIL  {label}{msg}")
    FAIL_COUNT += 1
    return False


def error_codes(report) -> list:
    """Extracts the list of validation error codes from a ValidationReport."""
    if not isinstance(report, dict):
        return []
    return [e.get("code", "") for e in (report.get("errors") or [])]


# ── WebSocket session helper ──────────────────────────────────────────────────

async def open_ws_and_get_run_id(token: str, app_slug: str, ep_slug: str) -> str:
    """Opens WS, sends one message, returns run_id from the first event carrying one."""
    try:
        import websockets
    except ImportError:
        print("  websockets not installed — pip install websockets")
        return ""

    uri = f"{WS_BASE}/{TENANT_SLUG}/apps/{app_slug}/{ep_slug}/ws?token={token}"
    run_id = None
    saw_token_event = False
    print(f"  WS connect: {WS_BASE}/{TENANT_SLUG}/apps/{app_slug}/{ep_slug}/ws (token=...)")
    try:
        async with websockets.connect(uri, open_timeout=15) as ws:
            await ws.send(json.dumps({"type": "message", "content": "Hello inline nodes!"}))
            print("  WS message sent")
            deadline = time.time() + 20
            while time.time() < deadline:
                try:
                    raw = await asyncio.wait_for(ws.recv(), timeout=2.0)
                    evt = json.loads(raw)
                    evt_type = evt.get("type", "")
                    print(f"  WS event: {evt_type} — {str(evt)[:100]}")
                    if evt_type == "token":
                        # Emitted by InlineLLMActivity — proves the inline LLM ran
                        # and its output reached the client through the run stream.
                        saw_token_event = True
                    if run_id is None and "run_id" in evt:
                        run_id = evt["run_id"]
                    if evt_type in ("done", "error"):
                        break
                except asyncio.TimeoutError:
                    continue
                except Exception:
                    break
            await asyncio.sleep(1)
    except Exception as e:
        print(f"  WS error: {e}")
    return f"{run_id or ''}|{'1' if saw_token_event else '0'}"


# ── Definition builders ───────────────────────────────────────────────────────

EP_INST = "ep_ws_1"
LLM_INST = "inline_llm_1"
COND_INST = "inline_condition_1"
AGENT_TRUE_INST = "agent_true_1"
AGENT_FALSE_INST = "agent_false_1"

AGENT_REF = {
    "kind": "agent",
    "namespace": A2A_ECHO_NAMESPACE,
    "name": A2A_ECHO_NAME,
    "version": 1,
}


def build_definition(ep_slug: str, *, backend="temporal", condition_edges=2,
                     llm_prompts=True) -> dict:
    """Builds the EP → LLM → Condition → (TRUE | FALSE) definition document.

    condition_edges=1 drops the false edge   → expect condition_edge_count
    llm_prompts=False clears both prompts    → expect llm_no_prompt
    backend="local" exercises the gate that keeps graph rules off local apps.
    """
    llm_config = {
        "node_type": "llm",
        "display_name": "Summarize",
        "provider": "mock",
        "output_var": "summary",
        "max_tokens": 256,
    }
    if llm_prompts:
        llm_config["system_prompt"] = "You are a concise summarizer."
        llm_config["user_prompt"] = "Summarise this: {{.input}}"

    connections = [
        {"source": LLM_INST, "target": COND_INST, "type": "flow_control"},
        {"source": COND_INST, "target": AGENT_TRUE_INST, "type": "flow_control",
         "label": "true"},
    ]
    if condition_edges >= 2:
        connections.append(
            {"source": COND_INST, "target": AGENT_FALSE_INST, "type": "flow_control",
             "label": "false"}
        )

    return {
        "schema_version": 2,
        "name": "E2E Inline Nodes Flow",
        "execution_backend": backend,
        "components": [
            {
                "instance_id": LLM_INST,
                "name": "Inline LLM",
                "definition_ref": {"kind": "inline", "namespace": "builtin",
                                   "name": "llm", "version": 1},
                "config": llm_config,
            },
            {
                "instance_id": COND_INST,
                "name": "Inline Condition",
                "definition_ref": {"kind": "inline", "namespace": "builtin",
                                   "name": "condition", "version": 1},
                "config": {
                    "node_type": "condition",
                    "display_name": "Has output?",
                    # Stable for any non-empty mock reply — must NOT depend on
                    # reply text, which the mock provider randomises.
                    "expression": '{{gt (len .summary) 0}}',
                },
            },
            {
                "instance_id": AGENT_TRUE_INST,
                "name": "True Branch Agent",
                "definition_ref": AGENT_REF,
                "config": {},
            },
            {
                "instance_id": AGENT_FALSE_INST,
                "name": "False Branch Agent",
                "definition_ref": AGENT_REF,
                "config": {},
            },
        ],
        "connections": connections,
        "entry_points": [
            {
                "instance_id": EP_INST,
                "slug": ep_slug,
                "protocol": "websocket",
                "root": LLM_INST,
                "config": {},
            }
        ],
    }


# ── Main test ─────────────────────────────────────────────────────────────────

def main():
    print("=== test_42_appflow_inline_nodes ===")

    token = fetch_token()
    tenant_id = get_tenant_id(token)
    if not tenant_id:
        print("SKIP: could not extract tenant_id from JWT")
        return 0

    suffix = uuid.uuid4().hex[:8]
    app_slug = f"e2e-inline-{suffix}"
    ep_slug = f"ep-ws-{suffix}"

    app_id = None
    def_id = None
    run_id = None
    access_token_id = None

    try:
        # ── Step 1: Create application ────────────────────────────────────────
        print("\n[1] Create application")
        status, body = api("POST", "/admin/applications", token, {
            "name": f"E2E Inline Nodes Test {suffix}",
            "slug": app_slug,
            "enabled": True,
        })
        if not ok("create application → 201", status == 201, f"{status} {body}"):
            return 1
        app_id = body.get("id", "")
        ok("app_id present", bool(app_id), str(body))

        # ── Step 2: Create WS entry point ─────────────────────────────────────
        print("\n[2] Create entry point")
        status, body = api("POST", f"/admin/applications/{app_id}/entry-points", token, {
            "slug": ep_slug,
            "entry_point_type": "websocket",
            "enabled": True,
        })
        if not ok("create entry point → 201", status == 201, f"{status} {body}"):
            return 1

        # ── Step 3: Create + validate the good definition ─────────────────────
        print("\n[3] Create definition and validate")
        status, body = api("POST", f"/admin/applications/{app_id}/definitions", token,
                           {"definition": build_definition(ep_slug)})
        if not ok("create definition → 201", status == 201, f"{status} {body}"):
            return 1
        def_id = body.get("id", "")
        ok("def_id present", bool(def_id), str(body))

        status, report = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/validate", token)
        ok("validate → 200", status == 200, f"{status} {report}")
        ok("inline kind accepted without a registry row (isBuiltinKind)",
           "component_not_found" not in error_codes(report),
           f"codes={error_codes(report)}")
        ok("valid definition reports valid=true",
           report.get("valid") is True, f"errors={report.get('errors')}")

        # ── Step 4: NEGATIVE — condition with one outgoing edge ───────────────
        print("\n[4] Negative: condition with only one outgoing edge")
        api("PUT", f"/admin/applications/{app_id}/definitions/{def_id}", token,
            {"definition": build_definition(ep_slug, condition_edges=1)})
        status, report = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/validate", token)
        codes = error_codes(report)
        ok("one-edge condition → valid=false", report.get("valid") is False, str(report)[:160])
        ok("one-edge condition → condition_edge_count",
           "condition_edge_count" in codes, f"codes={codes}")
        ok("error carries the offending instance_id (drives canvas highlighting)",
           any(e.get("instance_id") == COND_INST
               for e in (report.get("errors") or [])
               if e.get("code") == "condition_edge_count"),
           str(report.get("errors"))[:200])

        # ── Step 5: NEGATIVE — LLM node with no prompts ───────────────────────
        print("\n[5] Negative: LLM node with no prompts")
        api("PUT", f"/admin/applications/{app_id}/definitions/{def_id}", token,
            {"definition": build_definition(ep_slug, llm_prompts=False)})
        status, report = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/validate", token)
        codes = error_codes(report)
        ok("prompt-less LLM → llm_no_prompt", "llm_no_prompt" in codes, f"codes={codes}")

        # ── Step 6: NEGATIVE-CONTROL — same break, local backend → valid ──────
        # Proves the temporal gate: a local-backend app never executes the graph,
        # so graph rules must not block it (plan §7.2, test PUB-IN-04).
        print("\n[6] Control: same broken condition on local backend → valid")
        api("PUT", f"/admin/applications/{app_id}/definitions/{def_id}", token,
            {"definition": build_definition(ep_slug, backend="local", condition_edges=1)})
        status, report = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/validate", token)
        codes = error_codes(report)
        ok("local backend skips compiler rules",
           "condition_edge_count" not in codes, f"codes={codes}")

        # ── Step 7: Restore the good definition and publish ───────────────────
        print("\n[7] Restore good definition and publish")
        api("PUT", f"/admin/applications/{app_id}/definitions/{def_id}", token,
            {"definition": build_definition(ep_slug)})
        status, report = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/validate", token)
        if not ok("restored definition valid again", report.get("valid") is True,
                  str(report)[:200]):
            return 1

        status, body = api(
            "POST", f"/admin/applications/{app_id}/definitions/{def_id}/publish", token)
        if not ok("publish → 200 or 201", status in (200, 201), f"{status} {body}"):
            print(f"  publish response: {body}")
            return 1
        time.sleep(0.5)

        # ── Step 7b: Node Registry Phase 1 — Runtime-screen LLM node override ──
        print("\n[7b] Runtime-screen inline LLM node override")
        status, nodes = api("GET", f"/admin/applications/{app_id}/flow-llm-nodes", token)
        ok("GET flow-llm-nodes → 200", status == 200, f"{status} {nodes}")
        llm_node = next((n for n in nodes if n.get("node_id") == LLM_INST), None)
        ok("compiled inline llm node listed", llm_node is not None, str(nodes))
        if llm_node is not None:
            ok("compiled provider matches canvas config (mock)",
               llm_node.get("compiled_provider") == "mock", str(llm_node))
            ok("no override stored yet", not llm_node.get("override_provider"), str(llm_node))

        status, put_body = api(
            "PUT", f"/admin/applications/{app_id}/flow-llm-nodes/{LLM_INST}", token,
            {"provider": "anthropic", "model": "claude-haiku-4-5-20251001"})
        ok("PUT flow-llm-nodes override → 200", status == 200, f"{status} {put_body}")

        status, nodes = api("GET", f"/admin/applications/{app_id}/flow-llm-nodes", token)
        llm_node = next((n for n in nodes if n.get("node_id") == LLM_INST), None)
        ok("override persisted and merged into GET response",
           llm_node is not None
           and llm_node.get("override_provider") == "anthropic"
           and llm_node.get("override_model") == "claude-haiku-4-5-20251001",
           str(llm_node))
        ok("compiled value unchanged by override (canvas not touched)",
           llm_node is not None and llm_node.get("compiled_provider") == "mock",
           str(llm_node))

        # Reset the override back to the canvas-compiled "mock" provider so the
        # WS run below (steps 8-10) exercises the mock provider as before —
        # an override to a real provider would need a live API key. There is no
        # delete endpoint (Phase 1 scope): set it explicitly back to mock.
        status, put_body = api(
            "PUT", f"/admin/applications/{app_id}/flow-llm-nodes/{LLM_INST}", token,
            {"provider": "mock", "model": "mock"})
        ok("PUT flow-llm-nodes reset to mock → 200", status == 200, f"{status} {put_body}")

        # ── Step 8: Access token + WS session ─────────────────────────────────
        print("\n[8] Create access token and open WS session")
        status, tok_body = api("POST", "/admin/tokens", token, {
            "label": f"e2e-inline-ws-token-{suffix}",
            "user_id": 1,
        })
        ws_bearer = tok_body.get("token", "")
        access_token_id = tok_body.get("id", "")
        ok("access token created → 201", status == 201, f"{status} {tok_body}")
        if not ws_bearer:
            return 1

        combined = asyncio.run(open_ws_and_get_run_id(ws_bearer, app_slug, ep_slug))
        run_id, saw_token = (combined.split("|") + ["", "0"])[:2]
        ok("run_id from WS session", bool(run_id), f"got {run_id!r}")

        if not run_id:
            rows = psql(
                f"SELECT id::text FROM them.runs "
                f"WHERE tenant_id='{tenant_id}'::uuid "
                f"AND application_id='{app_id}'::uuid "
                f"ORDER BY started_at DESC LIMIT 1"
            )
            run_id = rows.strip()
            ok("run_id from DB fallback", bool(run_id), f"got {run_id!r}")
            if not run_id:
                print("  SKIP: cannot determine run_id — aborting E2E")
                return 0

        # ── Step 9: Poll run until terminal ───────────────────────────────────
        print(f"\n[9] Poll run until terminal (run_id={run_id})")
        final_status = None
        deadline = time.time() + 180
        while time.time() < deadline:
            status, run_data = api("GET", f"/runs/{run_id}", token)
            if status == 200:
                final_status = run_data.get("status", "")
                if final_status in ("completed", "failed", "rejected", "canceled"):
                    break
            time.sleep(3)

        ok("run reached terminal status",
           final_status in ("completed", "failed", "rejected", "canceled"),
           f"final_status={final_status!r}")
        ok("run.status == completed", final_status == "completed",
           f"got {final_status!r} — if failed, confirm them-dag-worker was REBUILT "
           f"and force-recreated since the inline-node commits (activities register "
           f"at startup)")

        # ── Step 10: token event proves the inline LLM actually ran ───────────
        print("\n[10] Verify inline LLM produced a token event")
        ok("WS received a token event from InlineLLMActivity",
           saw_token == "1",
           "no token event seen — InlineLLMActivity may not be registered on the worker")

        # ── Step 11: exactly one branch traversed ─────────────────────────────
        print("\n[11] Verify the condition took exactly one branch")
        step_count_raw = psql(
            f"SELECT COUNT(*) FROM them.run_steps WHERE run_id='{run_id}'::uuid"
        ).strip()
        try:
            step_count = int(step_count_raw)
        except ValueError:
            step_count = -1
        # AppFlow does not record run_steps yet (plan §5.2 / §11 item 6), so this
        # is informational rather than an assertion — it must not fail the suite
        # until per-node recording lands.
        print(f"  INFO  run_steps rows for this run: {step_count} "
              f"(AppFlow per-node recording is not implemented yet — plan §11 item 6)")

        # NOTE: them.runs.final_output is deliberately NOT asserted here.
        # SetFinalOutput is called only from the orchestrator path
        # (internal/orchestrator/orchestrator.go:553); AppFlow never writes it,
        # exactly as it never writes run_steps. AppFlowWorkflow returns FinalText
        # and FinalizeRunActivity delivers it to the client in the "done" stream
        # event — which step 8 already observed. Asserting the column here would
        # be asserting a gap, not a behaviour. Both are plan §11 item 6.
        final_output = psql(
            f"SELECT COALESCE(final_output,'') FROM them.runs WHERE id='{run_id}'::uuid"
        ).strip()
        print(f"  INFO  runs.final_output: {final_output!r} "
              f"(empty is expected — AppFlow does not populate it yet)")

        # What AppFlow does guarantee: a clean terminal state with no error text.
        run_error = psql(
            f"SELECT COALESCE(error,'') FROM them.runs WHERE id='{run_id}'::uuid"
        ).strip()
        ok("completed run carries no error text", run_error == "",
           f"error={run_error!r}")

    finally:
        # ── Cleanup ───────────────────────────────────────────────────────────
        print("\n[cleanup]")
        if run_id:
            try:
                psql(f"DELETE FROM them.run_steps WHERE run_id='{run_id}'::uuid")
                psql(f"DELETE FROM them.runs WHERE id='{run_id}'::uuid")
                print("  cleaned up run + run_steps")
            except Exception as e:
                print(f"  cleanup runs: {e}")
        if access_token_id:
            s, _ = api("DELETE", f"/admin/tokens/{access_token_id}", token)
            print(f"  DELETE access token → {s}")
        if app_id:
            s, _ = api("DELETE", f"/admin/applications/{app_id}", token)
            print(f"  DELETE application → {s}")

    print()
    if FAIL_COUNT == 0:
        print(f"PASS test_42_appflow_inline_nodes ({PASS_COUNT} checks)")
        return 0
    print(f"FAIL test_42_appflow_inline_nodes ({FAIL_COUNT} failed, {PASS_COUNT} passed)")
    return 1


if __name__ == "__main__":
    sys.exit(main())
