#!/usr/bin/env python3.12
"""
test_17_memory.py — structural tests for Phase 8.4 context summarization memory.
No containers required.
Usage: python3.12 scripts/tests/test_17_memory.py
"""

import sys, os, ast, pathlib

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../.."))

PASS = 0
FAIL = 0
ROOT = pathlib.Path(os.path.join(os.path.dirname(__file__), "../.."))


def check(desc, ok, detail=""):
    global PASS, FAIL
    if ok:
        print(f"  [PASS] {desc}"); PASS += 1
    else:
        print(f"  [FAIL] {desc}" + (f"  ({detail})" if detail else "")); FAIL += 1


def src(path): return (ROOT / path).read_text()
def funcs(path):
    tree = ast.parse(src(path))
    return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]
def assignments(path):
    tree = ast.parse(src(path))
    return [n.targets[0].id for n in ast.walk(tree) if isinstance(n, ast.Assign) and isinstance(n.targets[0], ast.Name)]


print("=== test_17_memory: Phase 8.4 Context Summarization Memory ===")

# 1. memory_service.py — file exists and has required functions/constants
try:
    s = src("app/services/memory_service.py")
    fns = funcs("app/services/memory_service.py")
    check("memory_service.py exists", True)
    check("get_injected_context defined", "get_injected_context" in fns)
    check("summarize_context defined", "summarize_context" in fns)
    check("resolve_summarizer defined", "resolve_summarizer" in fns)
    check("Redis key prefix them:ctx:", "them:ctx:" in s)
    check("summary TTL defined", "_SUMMARY_TTL" in s)
    check("never raises (try/except in summarize_context)", "return None" in s)
    check("raw artifacts preserved (no DB delete call)", ".delete(" not in s and "DELETE FROM" not in s)
except Exception as exc:
    check("memory_service.py", False, str(exc))

# 2. Orchestrator model has memory columns
try:
    s = src("app/models.py")
    check("memory_enabled column", "memory_enabled" in s)
    check("summarize_every_n_calls column", "summarize_every_n_calls" in s)
    check("memory_raw_fallback_n column", "memory_raw_fallback_n" in s)
    check("summarizer_provider column", "summarizer_provider" in s)
    check("summarizer_model column", "summarizer_model" in s)
    check("summarizer_api_key_encrypted column", "summarizer_api_key_encrypted" in s)
except Exception as exc:
    check("models.py memory columns", False, str(exc))

# 3. admin_orchestrators.py — memory fields in API shapes
try:
    s = src("app/routers/admin_orchestrators.py")
    check("OrchestratorCreate has memory_enabled", "memory_enabled" in s)
    check("OrchestratorOut has memory_enabled", s.count("memory_enabled") >= 2)
    check("summarize_every_n_calls in router", "summarize_every_n_calls" in s)
    check("summarizer_provider in router", "summarizer_provider" in s)
except Exception as exc:
    check("admin_orchestrators.py memory fields", False, str(exc))

# 4. task_runner.py — memory integration
try:
    s = src("app/services/task_runner.py")
    check("memory_service imported", "memory_service" in s or "from app.services.memory_service" in s)
    check("get_injected_context called", "get_injected_context" in s)
    check("summarize_context called", "summarize_context" in s)
    check("agent_calls_since_summary tracked", "agent_calls_since_summary" in s)
    check("memory_enabled checked", "memory_enabled" in s)
    check("summarize_every_n_calls checked", "summarize_every_n_calls" in s)
    check("injected context prepended to agent message", "_injected_ctx" in s)
except Exception as exc:
    check("task_runner.py memory integration", False, str(exc))

# 5. Redis key documented
try:
    s = src("docs/reference/redis.md")
    check("them:ctx: key documented in REDIS.md", "them:ctx:" in s)
    check("memory_service.py listed as owner", "memory_service" in s)
except Exception as exc:
    check("REDIS.md memory key", False, str(exc))

# 6. DB migration exists
try:
    migration = src("db/003_phase8.sql")
    check("003_phase8.sql exists", True)
    check("memory_enabled column in migration", "memory_enabled" in migration)
    check("summarize_every_n_calls in migration", "summarize_every_n_calls" in migration)
    check("summarizer_provider in migration", "summarizer_provider" in migration)
except Exception as exc:
    check("db/003_phase8.sql", False, str(exc))

# 7. Frontend types include memory fields
try:
    s = src("frontend/src/lib/api.ts")
    check("OrchestratorFull has memory_enabled", "memory_enabled" in s)
    check("OrchestratorFull has summarize_every_n_calls", "summarize_every_n_calls" in s)
    check("OrchestratorFull has summarizer_provider", "summarizer_provider" in s)
except Exception as exc:
    check("frontend/src/lib/api.ts memory fields", False, str(exc))

print(f"\n{'='*50}")
print(f"Results: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
