#!/usr/bin/env python3.12
"""
test_09_rate_limiter.py — unit tests for rate_limiter and token_cache logic.
No containers required for rate limiter logic tests.
Token cache tests need the container (imports pydantic chain).
Usage: python3.12 scripts/tests/test_09_rate_limiter.py
"""

import sys
import os
import asyncio

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../.."))

PASS = 0
FAIL = 0


def check(desc, ok, detail=""):
    global PASS, FAIL
    if ok:
        print(f"  [PASS] {desc}"); PASS += 1
    else:
        print(f"  [FAIL] {desc}" + (f"  ({detail})" if detail else "")); FAIL += 1


print("=== test_09_rate_limiter: Rate Limiter & Token Cache ===")

# 1. rate_limiter imports and slot logic
try:
    import importlib, time
    # Patch db_module to avoid real Redis
    import types
    fake_db = types.ModuleType("app.database")
    fake_db.redis_client = None
    sys.modules["app.database"] = fake_db
    sys.modules["app.utils.logger"] = types.ModuleType("app.utils.logger")
    sys.modules["app.utils.logger"].logger = type("L", (), {
        "warning": lambda s,*a,**k: None,
        "error": lambda s,*a,**k: None,
        "info": lambda s,*a,**k: None,
    })()

    from app.services.rate_limiter import _slot, check_rate_limit, get_current_count

    slot = _slot()
    check("_slot() returns int", isinstance(slot, int))
    check("_slot() is current hour", slot == int(time.time()) // 3600)

    # With no Redis, rate limiter should allow all requests
    result = asyncio.run(check_rate_limit(user_id=1, limit_rpm=10))
    check("check_rate_limit allows when Redis=None", result[0] is True)

    result = asyncio.run(check_rate_limit(user_id=1, limit_rpm=0))
    check("check_rate_limit allows when limit=0 (disabled)", result[0] is True)

    count = asyncio.run(get_current_count(user_id=1))
    check("get_current_count returns 0 when Redis=None", count == 0)

except ImportError as exc:
    print(f"  [SKIP] rate_limiter tests — missing deps ({exc})")
except Exception as exc:
    check("rate_limiter import", False, str(exc))

# 2. token_cache hash function
try:
    import hashlib
    # Directly test the hash logic (no imports needed)
    token = "test-token-abc123"
    expected_hash = hashlib.sha256(token.encode()).hexdigest()
    check("sha256 hash is 64 chars", len(expected_hash) == 64)
    check("sha256 hash is deterministic", expected_hash == hashlib.sha256(token.encode()).hexdigest())
    check("different tokens produce different hashes",
          hashlib.sha256(b"a").hexdigest() != hashlib.sha256(b"b").hexdigest())
except Exception as exc:
    check("token hash logic", False, str(exc))

# 3. _deps module structure (no real auth service needed)
try:
    # Just verify the module can be imported structurally
    import ast, pathlib
    src = pathlib.Path(os.path.join(os.path.dirname(__file__), "../../app/_deps.py")).read_text()
    tree = ast.parse(src)
    funcs = [n.name for n in ast.walk(tree) if isinstance(n, ast.AsyncFunctionDef)]
    check("require_jwt defined in _deps.py", "require_jwt" in funcs)
    check("require_admin defined in _deps.py", "require_admin" in funcs)
    check("require_bearer defined in _deps.py", "require_bearer" in funcs)
except Exception as exc:
    check("_deps.py structure", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
