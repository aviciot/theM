#!/usr/bin/env bash
# run_all_tests.sh — Odin full test suite (all phases)
# Usage: bash scripts/tests/run_all_tests.sh
#        ADMIN_JWT=<token> bash scripts/tests/run_all_tests.sh   # enables E2E test

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0; FAIL=0

run_test() {
    local label="$1" cmd="$2"
    echo ""; echo "────────────────────────────────────────"
    if eval "$cmd"; then
        echo "  >> $label: PASSED"; ((PASS++)) || true
    else
        echo "  >> $label: FAILED"; ((FAIL++)) || true
    fi
}

echo "========================================"
echo "  Odin Full Test Suite (Phases 0-7)"
echo "========================================"

# ── Phase 0-1: Infrastructure ─────────────────────────────────────────────────
run_test "01 DB schema"             "bash '$SCRIPT_DIR/test_01_db.sh'"
run_test "02 Redis"                 "bash '$SCRIPT_DIR/test_02_redis.sh'"
run_test "03 Auth service health"   "bash '$SCRIPT_DIR/test_03_auth_service.sh'"
run_test "04 Bridge health"         "bash '$SCRIPT_DIR/test_04_bridge_health.sh'"

# ── Phase 3: Registry + Adapters ─────────────────────────────────────────────
run_test "07 Adapter factory"       "python3.12 '$SCRIPT_DIR/test_07_adapter_factory.py'"
run_test "05 Agents API"            "bash '$SCRIPT_DIR/test_05_agents_api.sh'"
run_test "06 Orchestrators API"     "bash '$SCRIPT_DIR/test_06_orchestrators_api.sh'"

# ── Phase 4: Token cache + Rate limiter ──────────────────────────────────────
run_test "08 Tokens API"            "bash '$SCRIPT_DIR/test_08_tokens_api.sh'"
run_test "09 Rate limiter"          "python3.12 '$SCRIPT_DIR/test_09_rate_limiter.py'"

# ── Phase 5: Orchestrator loop ────────────────────────────────────────────────
run_test "10 Run recorder"          "python3.12 '$SCRIPT_DIR/test_10_run_recorder.py'"
run_test "11 WS orchestrate"        "bash '$SCRIPT_DIR/test_11_ws_orchestrate.sh'"

# ── Phase 6: Dashboard + Runs ────────────────────────────────────────────────
run_test "12 Runs API"              "bash '$SCRIPT_DIR/test_12_runs_api.sh'"
run_test "13 Dashboard WS"          "python3.12 '$SCRIPT_DIR/test_13_dashboard_ws.py'"

# ── Phase 7: Compose health + E2E ────────────────────────────────────────────
run_test "15 Compose health"        "bash '$SCRIPT_DIR/test_15_compose_health.sh'"
run_test "14 E2E orchestrate"       "bash '$SCRIPT_DIR/test_14_e2e_orchestrate.sh'"

echo ""
echo "========================================"
echo "  Total: $PASS passed, $FAIL failed"
echo "========================================"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
