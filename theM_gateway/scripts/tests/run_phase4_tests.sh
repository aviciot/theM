#!/usr/bin/env bash
# run_phase4_tests.sh — run all Phase 4 tests (includes Phase 3 as baseline)
# Usage: bash scripts/tests/run_phase4_tests.sh

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Resolve Python interpreter: prefer "${_PYTHON}", fall back to python3.11 or python3.
# On Windows dev machines system python3 may be 3.6 — always prefer "${_PYTHON}" there.
# On Linux CI/servers "${_PYTHON}" may not be installed; python3 is >= 3.10.
_PYTHON=""
for _candidate in "${_PYTHON}" python3.11 python3.10 python3; do
  if command -v "${_candidate}" > /dev/null 2>&1; then
    _ver="$(${_candidate} -c 'import sys; print(sys.version_info.minor)' 2>/dev/null || echo 0)"
    if [ "${_ver}" -ge 10 ] 2>/dev/null; then
      _PYTHON="${_candidate}"
      break
    fi
  fi
done
if [ -z "${_PYTHON}" ]; then
  echo "ERROR: Python 3.10+ not found in PATH." >&2
  exit 1
fi

PASS=0
FAIL=0

run_test() {
    local label="$1" cmd="$2"
    echo ""
    echo "────────────────────────────────────────"
    if eval "$cmd"; then
        echo "  >> $label: PASSED"; ((PASS++)) || true
    else
        echo "  >> $label: FAILED"; ((FAIL++)) || true
    fi
}

echo "========================================"
echo "  Odin Phase 4 Test Suite"
echo "========================================"

# Phase 3 baseline
run_test "01 DB schema"           "bash '$SCRIPT_DIR/test_01_db.sh'"
run_test "02 Redis"               "bash '$SCRIPT_DIR/test_02_redis.sh'"
run_test "03 Auth service health" "bash '$SCRIPT_DIR/test_03_auth_service.sh'"
run_test "04 Bridge health"       "bash '$SCRIPT_DIR/test_04_bridge_health.sh'"
run_test "07 Adapter factory"     ""${_PYTHON}" '$SCRIPT_DIR/test_07_adapter_factory.py'"
run_test "05 Agents API"          "bash '$SCRIPT_DIR/test_05_agents_api.sh'"
run_test "06 Orchestrators API"   "bash '$SCRIPT_DIR/test_06_orchestrators_api.sh'"

# Phase 4
run_test "08 Tokens API"          "bash '$SCRIPT_DIR/test_08_tokens_api.sh'"
run_test "09 Rate limiter"        ""${_PYTHON}" '$SCRIPT_DIR/test_09_rate_limiter.py'"

echo ""
echo "========================================"
echo "  Total: $PASS passed, $FAIL failed"
echo "========================================"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
