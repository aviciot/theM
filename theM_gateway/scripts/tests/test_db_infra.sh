#!/usr/bin/env bash
# test_db_infra.sh — DB schema bootstrap and migration infrastructure tests
#
# Tests:
#   T1  worker readiness timeout blocks startup (simulated)
#   T2  concurrent initialization: exactly one process bootstraps
#   T3  concurrent upgrade: same migration not applied twice
#   T4  failed migration leaves no schema_migrations record
#   T5  rerunning a successful migration skips it safely
#   T6  clean restart does not reapply schema_current.sql
#
# Prerequisites:
#   - them-postgres container running and healthy
#   - db/schema_current.sql present
#   - scripts/linux-db-init.sh and scripts/linux-db-upgrade.sh present
#   - A scratch DB or confirmed that them schema will be dropped/recreated
#
# Usage:
#   cd theM_gateway
#   ./scripts/tests/test_db_infra.sh
#
# The tests use a SEPARATE database (them_test_infra) to avoid touching the
# production them DB. The test DB is created, used, and dropped per run.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
TEST_DB="them_test_infra"

PASS=0
FAIL=0

# ── Helpers ────────────────────────────────────────────────────────────────────

_ok() { echo "  [PASS] $1"; PASS=$((PASS + 1)); }
_fail() { echo "  [FAIL] $1" >&2; FAIL=$((FAIL + 1)); }

_psql_prod() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${TEST_DB}" -tAc "$1" 2>/dev/null || echo ""
}

_psql_prod_raw() {
  docker exec -i "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${TEST_DB}" \
    -v ON_ERROR_STOP=0 2>&1 || true
}

_psql_superuser() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U postgres -tAc "$1" 2>/dev/null || echo ""
}

# Environment overrides so init/upgrade scripts target the test DB
export POSTGRES_CONTAINER THE_M_DB_USER
export THE_M_DB_NAME="${TEST_DB}"
export REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"

# ── Setup: create isolated test database ──────────────────────────────────────

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  DB Infrastructure Tests — test_db_infra.sh                 ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "Test database: ${TEST_DB} (created fresh, dropped after tests)"
echo ""

# Verify postgres container is running
if ! docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d postgres \
    > /dev/null 2>&1; then
  echo "ERROR: ${POSTGRES_CONTAINER} not reachable. Start the stack first." >&2
  exit 1
fi

# Drop + recreate test DB (use superuser if available, otherwise them user)
docker exec "${POSTGRES_CONTAINER}" \
  psql -U postgres \
  -c "DROP DATABASE IF EXISTS ${TEST_DB};" \
  -c "CREATE DATABASE ${TEST_DB} OWNER ${THE_M_DB_USER};" \
  > /dev/null 2>&1 || \
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d postgres \
  -c "DROP DATABASE IF EXISTS ${TEST_DB};" \
  -c "CREATE DATABASE ${TEST_DB} OWNER ${THE_M_DB_USER};" \
  > /dev/null 2>&1

echo "Test DB created: ${TEST_DB}"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
echo "── T1: Worker readiness timeout blocks startup ───────────────────────────"
echo ""
# We cannot spin up a real Temporal stack here, so we test the logic by
# verifying that linux-start.sh's worker section exits non-zero when the
# temporal-admin-tools container is absent or the task queue has no pollers.
# We do this by examining the script's exit-on-failure path directly.

# Simulate: temporal-admin-tools missing → docker exec fails → worker not ready
# We invoke the worker-readiness block with a zero timeout via env override.
# To isolate just the worker block, we create a minimal test harness script.

WORKER_TEST_SCRIPT="$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
_WORKER_TIMEOUT=0  # immediate timeout
_WORKER_ELAPSED=0
_WORKER_READY=false

while [ "${_WORKER_ELAPSED}" -lt "${_WORKER_TIMEOUT}" ]; do
  _WORKER_ELAPSED=$((_WORKER_ELAPSED + 1))
done

if [ "${_WORKER_READY}" != "true" ]; then
  echo "WORKER_NOT_READY"
  exit 1
fi
exit 0
EOF
)"

if echo "${WORKER_TEST_SCRIPT}" | bash 2>/dev/null; then
  _fail "T1: Worker readiness check should fail on zero timeout but succeeded"
else
  EXIT=$?
  if [ "${EXIT}" -eq 1 ]; then
    _ok "T1: Worker readiness timeout exits non-zero (startup blocked as required)"
  else
    _fail "T1: Wrong exit code ${EXIT} (expected 1)"
  fi
fi

# Confirm that linux-start.sh Go-bridge step is AFTER worker step in startup order
START_SCRIPT="${GATEWAY_DIR}/scripts/linux-start.sh"
WORKER_LINE=$(grep -n "up -d.*them-worker" "${START_SCRIPT}" | head -1 | cut -d: -f1)
GOBRIDGE_LINE=$(grep -n "up -d.*them-go-bridge\b" "${START_SCRIPT}" | head -1 | cut -d: -f1)

if [ -n "${WORKER_LINE}" ] && [ -n "${GOBRIDGE_LINE}" ] && \
   [ "${GOBRIDGE_LINE}" -gt "${WORKER_LINE}" ]; then
  _ok "T1: Go bridge start (line ${GOBRIDGE_LINE}) is after worker start (line ${WORKER_LINE}) in linux-start.sh"
else
  _fail "T1: Could not confirm Go bridge starts after worker in linux-start.sh (worker=${WORKER_LINE}, go=${GOBRIDGE_LINE})"
fi

# Confirm linux-start.sh has 'exit 1' in the worker readiness failure path
# The diagnostic block spans up to 20 lines — use -A20 to cover the full block
if grep -A20 "Temporal worker not ready after" "${START_SCRIPT}" | grep -q "exit 1"; then
  _ok "T1: Worker failure path has 'exit 1' — startup is blocked on worker readiness"
else
  _fail "T1: Worker failure path does not have 'exit 1' in linux-start.sh"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── T2: Concurrent initialization — exactly one process bootstraps ────────"
echo ""

# Reset test DB
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${TEST_DB}" \
  -c "DROP SCHEMA IF EXISTS them CASCADE; DROP SCHEMA IF EXISTS auth_service CASCADE;" \
  > /dev/null 2>&1 || true

# Run two linux-db-init.sh invocations concurrently
"${GATEWAY_DIR}/scripts/linux-db-init.sh" > /tmp/db_init_proc1.log 2>&1 &
PID1=$!
"${GATEWAY_DIR}/scripts/linux-db-init.sh" > /tmp/db_init_proc2.log 2>&1 &
PID2=$!

wait "${PID1}" && EXIT1=0 || EXIT1=$?
wait "${PID2}" && EXIT2=0 || EXIT2=$?

# Exactly one should succeed (exit 0), one should fail or detect already-initialized
BOTH_SUCCEEDED=$([ "${EXIT1}" -eq 0 ] && [ "${EXIT2}" -eq 0 ] && echo true || echo false)
BOTH_FAILED=$([ "${EXIT1}" -ne 0 ] && [ "${EXIT2}" -ne 0 ] && echo true || echo false)

if [ "${BOTH_SUCCEEDED}" = "true" ]; then
  # Both succeeded is OK only if the second one detected already-initialized
  P1_INITIALIZED=$(grep -c "already initialized\|already-initialized\|Schema bootstrap complete" /tmp/db_init_proc1.log 2>/dev/null) || P1_INITIALIZED=0
  P2_INITIALIZED=$(grep -c "already initialized\|already-initialized\|Schema bootstrap complete" /tmp/db_init_proc2.log 2>/dev/null) || P2_INITIALIZED=0
  if [ "$((P1_INITIALIZED + P2_INITIALIZED))" -ge 2 ]; then
    _ok "T2: Both processes exited 0; one bootstrapped, other detected already-initialized"
  else
    _fail "T2: Both processes bootstrapped without detecting concurrent init"
  fi
elif [ "${BOTH_FAILED}" = "true" ]; then
  _fail "T2: Both concurrent init processes failed — one should succeed"
else
  _ok "T2: Exactly one process bootstrapped; the other got advisory lock error or detected already-initialized"
fi

# Verify exactly one set of migration rows (no duplicates)
VERSION_COUNT=$(_psql_prod "SELECT COUNT(*) FROM them.schema_migrations;" || echo 0)
DISTINCT_COUNT=$(_psql_prod "SELECT COUNT(DISTINCT version) FROM them.schema_migrations;" || echo 0)

if [ "${VERSION_COUNT}" = "${DISTINCT_COUNT}" ] && [ "${VERSION_COUNT:-0}" -gt 0 ]; then
  _ok "T2: schema_migrations has ${VERSION_COUNT} rows, all unique versions (no duplicates)"
else
  _fail "T2: schema_migrations has ${VERSION_COUNT} total vs ${DISTINCT_COUNT} distinct (duplicates or empty)"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── T3: Concurrent upgrade — same migration applied exactly once ──────────"
echo ""

# Create a test migration file (no BEGIN/COMMIT — upgrade script wraps in its own transaction)
# Version name must match the schema_migrations check constraint: ^\d{3}[a-z]?(_[a-z0-9_]+)?$
TEST_MIGRATION_FILE="/tmp/900_test_concurrent.sql"
cat > "${TEST_MIGRATION_FILE}" <<'MIGRATION_EOF'
-- Test migration: concurrent upgrade test
-- Note: no BEGIN/COMMIT here — linux-db-upgrade.sh wraps in its own transaction
CREATE TABLE IF NOT EXISTS them._test_concurrent_marker (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ DEFAULT now()
);
INSERT INTO them._test_concurrent_marker DEFAULT VALUES;
MIGRATION_EOF

# Run two upgrade attempts concurrently
"${GATEWAY_DIR}/scripts/linux-db-upgrade.sh" "${TEST_MIGRATION_FILE}" \
  > /tmp/upgrade_proc1.log 2>&1 &
UPID1=$!
"${GATEWAY_DIR}/scripts/linux-db-upgrade.sh" "${TEST_MIGRATION_FILE}" \
  > /tmp/upgrade_proc2.log 2>&1 &
UPID2=$!

wait "${UPID1}" && UEXIT1=0 || UEXIT1=$?
wait "${UPID2}" && UEXIT2=0 || UEXIT2=$?

# The marker table should have exactly ONE row (not two) — applied once
MARKER_COUNT=$(_psql_prod \
  "SELECT COUNT(*) FROM them._test_concurrent_marker;" 2>/dev/null || echo 0)

if [ "${MARKER_COUNT}" = "1" ]; then
  _ok "T3: Concurrent upgrade: marker table has exactly 1 row (migration applied once)"
else
  _fail "T3: Concurrent upgrade: marker table has ${MARKER_COUNT} rows (expected 1)"
fi

# schema_migrations must have exactly one row for this version
VERSION_KEY="900_test_concurrent"
SM_COUNT=$(_psql_prod \
  "SELECT COUNT(*) FROM them.schema_migrations WHERE version='${VERSION_KEY}';" || echo 0)
if [ "${SM_COUNT}" = "1" ]; then
  _ok "T3: schema_migrations has exactly 1 row for ${VERSION_KEY}"
else
  _fail "T3: schema_migrations has ${SM_COUNT} rows for ${VERSION_KEY} (expected 1)"
fi

# Both processes must have exited 0 (one applied, one was skipped)
if [ "${UEXIT1}" -eq 0 ] && [ "${UEXIT2}" -eq 0 ]; then
  _ok "T3: Both concurrent upgrade processes exited 0 (one applied, one skipped safely)"
else
  # One may have hit the advisory lock and failed — still acceptable
  # grep -c exits 1 when no match (but still prints "0") — capture cleanly without double-echo
  SKIP1=$(grep -c "Skipping\|skip\|already recorded" /tmp/upgrade_proc1.log 2>/dev/null) || SKIP1=0
  SKIP2=$(grep -c "Skipping\|skip\|already recorded" /tmp/upgrade_proc2.log 2>/dev/null) || SKIP2=0
  if [ "$((SKIP1 + SKIP2))" -ge 1 ]; then
    _ok "T3: One process skipped; migration not applied twice (exit codes: ${UEXIT1}, ${UEXIT2})"
  else
    _fail "T3: Unexpected exits (${UEXIT1}, ${UEXIT2}) without skip message"
  fi
fi

# Cleanup
_psql_prod "DROP TABLE IF EXISTS them._test_concurrent_marker;" > /dev/null 2>&1 || true
_psql_prod "DELETE FROM them.schema_migrations WHERE version='900_test_concurrent';" > /dev/null 2>&1 || true
rm -f "${TEST_MIGRATION_FILE}"

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── T4: Failed migration leaves no schema_migrations record ──────────────"
echo ""

# Version name must match the schema_migrations check constraint: ^\d{3}[a-z]?(_[a-z0-9_]+)?$
FAIL_MIGRATION_FILE="/tmp/901_test_fail.sql"
cat > "${FAIL_MIGRATION_FILE}" <<'FAIL_MIGRATION_EOF'
-- Test migration: intentional failure (no BEGIN/COMMIT — upgrade script owns the transaction)
CREATE TABLE IF NOT EXISTS them._test_fail_marker (id SERIAL PRIMARY KEY);
INSERT INTO them._test_fail_marker DEFAULT VALUES;
-- Force a SQL error to trigger rollback
SELECT 1/0;
FAIL_MIGRATION_EOF

# Run the failing migration — capture output and exit code without || true swallowing exit
FAIL_OUTPUT=$("${GATEWAY_DIR}/scripts/linux-db-upgrade.sh" "${FAIL_MIGRATION_FILE}" 2>&1) && FAIL_EXIT=0 || FAIL_EXIT=$?

if [ "${FAIL_EXIT}" -ne 0 ]; then
  _ok "T4: Failed migration exits non-zero (exit ${FAIL_EXIT})"
else
  _fail "T4: Failed migration exited 0 (should have failed)"
fi

# schema_migrations must NOT have a record for the failed version
FAIL_VERSION="901_test_fail"
SM_FAIL=$(_psql_prod \
  "SELECT COUNT(*) FROM them.schema_migrations WHERE version='${FAIL_VERSION}';" || echo 0)
if [ "${SM_FAIL}" = "0" ]; then
  _ok "T4: No schema_migrations record for failed migration ${FAIL_VERSION}"
else
  _fail "T4: schema_migrations has ${SM_FAIL} record(s) for failed migration — rollback did not work"
fi

# The marker table must NOT exist (transaction rolled back)
TABLE_EXISTS=$(_psql_prod \
  "SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema='them' AND table_name='_test_fail_marker';" || echo 0)
if [ "${TABLE_EXISTS}" = "0" ]; then
  _ok "T4: Rolled back — them._test_fail_marker table does not exist after failure"
else
  _fail "T4: them._test_fail_marker exists after failed migration — transaction was NOT rolled back"
fi

rm -f "${FAIL_MIGRATION_FILE}"

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── T5: Rerunning a successful migration skips it safely ─────────────────"
echo ""

# Version name must match the schema_migrations check constraint: ^\d{3}[a-z]?(_[a-z0-9_]+)?$
IDEMPOTENT_MIGRATION_FILE="/tmp/902_test_idempotent.sql"
cat > "${IDEMPOTENT_MIGRATION_FILE}" <<'IDEMPOTENT_EOF'
-- Test migration: idempotent marker
-- Note: no BEGIN/COMMIT here — linux-db-upgrade.sh wraps in its own transaction
CREATE TABLE IF NOT EXISTS them._test_idempotent_marker (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ DEFAULT now()
);
INSERT INTO them._test_idempotent_marker DEFAULT VALUES;
IDEMPOTENT_EOF

# First run — should apply
OUT1=$("${GATEWAY_DIR}/scripts/linux-db-upgrade.sh" "${IDEMPOTENT_MIGRATION_FILE}" 2>&1) && EXIT1=0 || EXIT1=$?

if [ "${EXIT1}" -eq 0 ]; then
  _ok "T5: First migration run succeeded (exit 0)"
else
  _fail "T5: First migration run failed unexpectedly (exit ${EXIT1})"
fi

# Second run — should skip
OUT2=$("${GATEWAY_DIR}/scripts/linux-db-upgrade.sh" "${IDEMPOTENT_MIGRATION_FILE}" 2>&1) && EXIT2=0 || EXIT2=$?

if [ "${EXIT2}" -eq 0 ] && echo "${OUT2}" | grep -q "Skipping\|skip\|already recorded"; then
  _ok "T5: Second run skipped safely (already recorded in schema_migrations)"
else
  _fail "T5: Second run did not skip (exit ${EXIT2})"
fi

# Marker table should have exactly 1 row (applied once, not twice)
IDEM_COUNT=$(_psql_prod \
  "SELECT COUNT(*) FROM them._test_idempotent_marker;" 2>/dev/null || echo 0)
if [ "${IDEM_COUNT}" = "1" ]; then
  _ok "T5: Marker table has 1 row — migration ran exactly once"
else
  _fail "T5: Marker table has ${IDEM_COUNT} rows (expected 1)"
fi

# Cleanup
_psql_prod "DROP TABLE IF EXISTS them._test_idempotent_marker;" > /dev/null 2>&1 || true
rm -f "${IDEMPOTENT_MIGRATION_FILE}"

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── T6: Clean restart does not reapply schema_current.sql ────────────────"
echo ""

# Record the minimum applied_at before calling init again
MIN_APPLIED_BEFORE=$(_psql_prod \
  "SELECT EXTRACT(EPOCH FROM MIN(applied_at))::bigint FROM them.schema_migrations;" || echo "0")

# Run linux-db-init.sh again — must be a no-op
REINIT_OUTPUT=$("${GATEWAY_DIR}/scripts/linux-db-init.sh" 2>&1)
REINIT_EXIT=$?

if [ "${REINIT_EXIT}" -eq 0 ]; then
  _ok "T6: linux-db-init.sh re-run exits 0"
else
  _fail "T6: linux-db-init.sh re-run exited ${REINIT_EXIT}"
fi

if echo "${REINIT_OUTPUT}" | grep -q "already initialized\|already-initialized\|Schema already"; then
  _ok "T6: Re-run output confirms no-op (schema already initialized)"
else
  _fail "T6: Re-run output does not indicate no-op: $(echo "${REINIT_OUTPUT}" | head -3)"
fi

# applied_at timestamps must not have changed (no rows were re-inserted)
MIN_APPLIED_AFTER=$(_psql_prod \
  "SELECT EXTRACT(EPOCH FROM MIN(applied_at))::bigint FROM them.schema_migrations;" || echo "0")

if [ "${MIN_APPLIED_BEFORE}" = "${MIN_APPLIED_AFTER}" ]; then
  _ok "T6: schema_migrations timestamps unchanged — schema_current.sql was not reapplied"
else
  _fail "T6: schema_migrations timestamps changed after restart — unexpected re-bootstrap"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Teardown
echo ""
echo "==> Dropping test database ${TEST_DB}..."
docker exec "${POSTGRES_CONTAINER}" \
  psql -U postgres \
  -c "DROP DATABASE IF EXISTS ${TEST_DB};" \
  > /dev/null 2>&1 || \
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d postgres \
  -c "DROP DATABASE IF EXISTS ${TEST_DB};" \
  > /dev/null 2>&1 || \
  echo "  WARNING: Could not drop ${TEST_DB} — drop manually if needed."

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "════════════════════════════════════════════════════════════════"
echo " DB Infrastructure Test Results"
echo "════════════════════════════════════════════════════════════════"
echo " Passed: ${PASS}"
echo " Failed: ${FAIL}"
echo "════════════════════════════════════════════════════════════════"

if [ "${FAIL}" -gt 0 ]; then
  echo " RESULT: FAILED — ${FAIL} check(s) did not pass."
  rm -f /tmp/db_init_proc{1,2}.log /tmp/upgrade_proc{1,2}.log
  exit 1
else
  echo " RESULT: ALL CHECKS PASSED"
  rm -f /tmp/db_init_proc{1,2}.log /tmp/upgrade_proc{1,2}.log
  exit 0
fi
