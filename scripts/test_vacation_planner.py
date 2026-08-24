#!/usr/bin/env python3
"""
Test script for the Kosher Vacation Planner agent.

Usage:
    python3 scripts/test_vacation_planner.py --token <JWT>
    python3 scripts/test_vacation_planner.py --token <JWT> --query "Compare Paris and Amsterdam for a kosher trip in October with a budget in USD"
    python3 scripts/test_vacation_planner.py --token <JWT> --import-only   # just import/update the definition
"""

import argparse
import json
import sys
import time
import urllib.request
import urllib.error

BASE_URL = "http://localhost:8088"
DEFINITION_FILE = "docs/architecture-v2/kosher-vacation-planner-agent.json"
AGENT_SLUG = "kosher-vacation-planner"
DEFAULT_QUERY = "Compare Rome and Barcelona for a kosher trip in September with a budget in ILS"


def request(method, path, token, body=None):
    url = BASE_URL + path
    data = json.dumps(body).encode() if body else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Accept", "application/json")
    if data:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def step(label):
    print(f"\n{'─'*60}")
    print(f"  {label}")
    print(f"{'─'*60}")


def ok(msg):
    print(f"  ✓  {msg}")


def fail(msg):
    print(f"  ✗  {msg}")
    sys.exit(1)


def import_definition(token):
    step("1. Import / update agent definition")
    with open(DEFINITION_FILE) as f:
        definition = json.load(f)

    # Check if it already exists
    status, existing = request("GET", "/api/v1/admin/agent-definitions", token)
    if status != 200:
        fail(f"Could not list definitions: {status} {existing}")

    existing_ids = {d["agent_slug"]: d["id"] for d in existing if isinstance(existing, list)}

    if AGENT_SLUG in existing_ids:
        def_id = existing_ids[AGENT_SLUG]
        ok(f"Definition already exists (id={def_id}), updating...")
        status, result = request("PUT", f"/api/v1/admin/agent-definitions/{def_id}", token, {"definition": definition})
        if status not in (200, 201):
            fail(f"PUT failed: {status} {result}")
        ok(f"Updated: revision={result.get('revision', '?')}")
    else:
        ok("Definition not found, creating...")
        status, result = request("POST", "/api/v1/admin/agent-definitions", token, definition)
        if status not in (200, 201):
            fail(f"POST failed: {status} {result}")
        def_id = result.get("id")
        ok(f"Created: id={def_id}")

    return result


def validate_definition(token, def_id):
    step("2. Validate definition")
    status, result = request("POST", f"/api/v1/admin/agent-definitions/{def_id}/validate", token)
    if status != 200:
        fail(f"Validate failed: {status} {result}")

    issues = result.get("issues", [])
    errors = [i for i in issues if i.get("severity") == "error"]
    warnings = [i for i in issues if i.get("severity") == "warning"]

    if errors:
        for e in errors:
            print(f"  ✗  ERROR: {e.get('message')} (node={e.get('node_id', '?')})")
        fail(f"{len(errors)} validation error(s)")

    if warnings:
        for w in warnings:
            print(f"  ⚠  WARN:  {w.get('message')} (node={w.get('node_id', '?')})")
    ok(f"Validation passed — {len(warnings)} warning(s), 0 errors")


def publish_definition(token, def_id):
    step("3. Publish definition")
    status, result = request("POST", f"/api/v1/admin/agent-definitions/{def_id}/publish", token)
    if status not in (200, 201):
        fail(f"Publish failed: {status} {result}")
    ok(f"Published: revision={result.get('revision', '?')}, agent_id={result.get('agent_id', '?')}")
    return result.get("agent_id")


def find_or_create_application(token, agent_id):
    step("4. Find or create test application")
    status, apps = request("GET", "/api/v1/admin/applications", token)
    if status != 200:
        fail(f"Could not list applications: {status} {apps}")

    slug = "vacation-planner-test"
    existing = next((a for a in apps if a.get("slug") == slug), None)

    if existing:
        app_id = existing["id"]
        ok(f"Using existing application: {slug} (id={app_id})")
        # Find entry point
        eps = existing.get("entry_points", [])
        if not eps:
            fail("Existing application has no entry points")
        ep_slug = eps[0]["slug"]
        ok(f"Entry point: {ep_slug}")
        return app_id, ep_slug

    # Create new application with this agent
    body = {
        "name": "Vacation Planner Test",
        "slug": slug,
        "orchestrator_id": None,
        "entry_points": [
            {
                "slug": "main",
                "name": "Main",
                "transport": "ws",
                "agent_id": agent_id,
            }
        ]
    }
    status, result = request("POST", "/api/v1/admin/applications", token, body)
    if status not in (200, 201):
        # Try minimal body — different API versions
        body2 = {"name": "Vacation Planner Test", "slug": slug}
        status2, result2 = request("POST", "/api/v1/admin/applications", token, body2)
        if status2 not in (200, 201):
            fail(f"Could not create application: {status} {result}")
        result = result2

    app_id = result["id"]
    ok(f"Created application: {slug} (id={app_id})")

    eps = result.get("entry_points", [])
    ep_slug = eps[0]["slug"] if eps else "main"
    ok(f"Entry point: {ep_slug}")
    return app_id, ep_slug


def run_agent_test(token, app_slug, query):
    step(f"5. Run agent via agent-runtime")
    print(f"  Query: {query}")
    print()

    # Use the agent-runtime direct invoke if available, else show instructions
    body = {
        "agent_slug": AGENT_SLUG,
        "skill_id": "plan-vacation",
        "input": query,
    }
    status, result = request("POST", "/api/v1/agent-runtime/invoke", token, body)

    if status == 404:
        # agent-runtime invoke endpoint may not exist — show manual test instructions
        print("  ℹ  Direct invoke endpoint not available.")
        print("  ℹ  To test manually, connect via WebSocket:")
        print(f"     App slug: {app_slug}")
        print(f"     URL: ws://localhost:8088/apps/{app_slug}/ws")
        print(f"     Send: {json.dumps({'type': 'message', 'content': query})}")
        return

    if status != 200:
        fail(f"Invoke failed: {status} {result}")

    output = result.get("output") or result.get("response") or result.get("result") or result
    print(f"  Output:\n")
    if isinstance(output, str):
        for line in output.splitlines():
            print(f"    {line}")
    else:
        print(f"    {json.dumps(output, indent=2)}")
    ok("Agent run complete")


def main():
    parser = argparse.ArgumentParser(description="Test Kosher Vacation Planner agent")
    parser.add_argument("--token", required=True, help="Admin JWT token")
    parser.add_argument("--query", default=DEFAULT_QUERY, help="Test query to send")
    parser.add_argument("--import-only", action="store_true", help="Only import and validate, skip running")
    parser.add_argument("--skip-import", action="store_true", help="Skip import, go straight to run")
    args = parser.parse_args()

    token = args.token.strip()

    print(f"\n{'='*60}")
    print(f"  Kosher Vacation Planner — Deploy & Test")
    print(f"{'='*60}")

    if not args.skip_import:
        result = import_definition(token)
        def_id = result.get("id")

        validate_definition(token, def_id)

        agent_id = publish_definition(token, def_id)
    else:
        # Look up existing
        step("Skipping import — looking up existing definition")
        status, defs = request("GET", "/api/v1/admin/agent-definitions", token)
        if status != 200:
            fail(f"Could not list definitions: {status}")
        existing = next((d for d in defs if d.get("agent_slug") == AGENT_SLUG), None)
        if not existing:
            fail(f"Definition '{AGENT_SLUG}' not found. Run without --skip-import first.")
        def_id = existing["id"]
        agent_id = existing.get("compiled_agent_id")
        ok(f"Found definition id={def_id}")

    if args.import_only:
        print(f"\n{'='*60}")
        print("  Import-only mode — done.")
        print(f"{'='*60}\n")
        return

    app_id, ep_slug = find_or_create_application(token, agent_id)
    run_agent_test(token, "vacation-planner-test", args.query)

    print(f"\n{'='*60}")
    print("  All steps complete.")
    print(f"{'='*60}\n")


if __name__ == "__main__":
    main()
