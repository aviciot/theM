#!/usr/bin/env bash
# linux-validate-clean-install.sh — Clean-install validation for the-M on Linux.
#
# Tests the complete fresh-install flow against a real Docker stack:
#   Phase 1: Start infrastructure only (Postgres + Redis)
#   Phase 2: Apply schema_current.sql and verify migration tracking
#   Phase 3: Verify partial-init detection works (schema_migrations present but empty)
#   Phase 4: Start Go-first full stack and verify health endpoints
#   Phase 5: Verify Traefik route ownership (/ws → Go, /sse → Go, /api/v1 → Python)
#   Phase 6: Restart the entire stack and confirm no schema re-bootstrap
#   Phase 7: Verify existing data remains intact after restart
#
# Prerequisites:
#   - Docker Engine running
#   - .env file present with required secrets
#   - Stack NOT already running (script uses its own start/stop)
#   - Run from theM_gateway/ directory
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-validate-clean-install.sh
#
# Exit code: 0 = all phases passed, 1 = one or more phases failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${GATEWAY_DIR}"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"
HOST_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || echo localhost)"

PASS=0
FAIL=0

COMPOSE=(
  docker compose
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
  --profile temporal
)

# ── Helpers ────────────────────────────────────────────────────────────────────

_ok() {
  local msg="$1"
  echo "  [PASS] ${msg}"
  PASS=$((PASS + 1))
}

_fail() {
  local msg="$1"
  echo "  [FAIL] ${msg}" >&2
  FAIL=$((FAIL + 1))
}

_psql_query() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -tAc "$1" 2>/dev/null || echo ""
}

_wait_healthy() {
  local container="$1" timeout="${2:-60}" elapsed=0
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null)" = "healthy" ]; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo "  ERROR: ${container} not healthy after ${timeout}s" >&2
      return 1
    fi
    sleep 5; elapsed=$((elapsed + 5))
  done
}

# ── Pre-flight ─────────────────────────────────────────────────────────────────
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║  the-M: Clean-Install Validation                                ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "Host IP: ${HOST_IP}"
echo ""

if [ ! -f .env ]; then
  echo "ERROR: .env not found. Run ./generate-env.sh first." >&2; exit 1
fi
if [ ! -f db/schema_current.sql ]; then
  echo "ERROR: db/schema_current.sql not found." >&2; exit 1
fi

# Stop any existing stack
echo "==> Stopping any existing stack..."
"${COMPOSE[@]}" down --remove-orphans --volumes 2>/dev/null || true
sleep 5

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 1: Fresh infrastructure start ──────────────────────────────────"
echo ""

"${COMPOSE[@]}" up -d them-postgres them-redis
_wait_healthy "${POSTGRES_CONTAINER}" 90
_wait_healthy "${REDIS_CONTAINER}"    30

# Verify truly empty DB
TABLE_COUNT=$(_psql_query \
  "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='them';")
if [ "${TABLE_COUNT}" = "0" ]; then
  _ok "Empty database — no them schema tables (expected on fresh Postgres volume)"
else
  # Some tables exist — verify schema_migrations is absent
  MIGRATIONS_EXISTS=$(_psql_query \
    "SELECT COUNT(*) FROM information_schema.tables
     WHERE table_schema='them' AND table_name='schema_migrations';")
  if [ "${MIGRATIONS_EXISTS}" = "0" ]; then
    _ok "No schema_migrations table (expected on truly fresh install)"
  else
    _fail "schema_migrations already exists on a supposedly fresh DB — volumes may not be clean"
  fi
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 2: Apply schema_current.sql — verify migration tracking ────────"
echo ""

echo "  Applying db/schema_current.sql..."
docker exec -i "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
  < db/schema_current.sql

# Verify migration table exists and has rows
VERSION_COUNT=$(_psql_query "SELECT COUNT(*) FROM them.schema_migrations;" || echo "0")
if [ "${VERSION_COUNT:-0}" -ge 25 ]; then
  _ok "them.schema_migrations populated: ${VERSION_COUNT} versions recorded"
else
  _fail "them.schema_migrations has only ${VERSION_COUNT:-0} versions (expected >= 25)"
fi

# Verify schema_migrations has version 025
HAS_025=$(_psql_query \
  "SELECT COUNT(*) FROM them.schema_migrations WHERE version='025_events_transport';")
if [ "${HAS_025}" = "1" ]; then
  _ok "Latest version 025_events_transport recorded in schema_migrations"
else
  _fail "Version 025_events_transport not found in schema_migrations"
fi

# Verify key tables exist at current shape
for tbl in agents orchestrators runs entry_points app_orchestrators middleware_defs schema_migrations; do
  EXISTS=$(_psql_query \
    "SELECT COUNT(*) FROM information_schema.tables
     WHERE table_schema='them' AND table_name='${tbl}';")
  if [ "${EXISTS}" = "1" ]; then
    _ok "Table them.${tbl} exists"
  else
    _fail "Table them.${tbl} NOT found"
  fi
done

# Verify events_transport column on them.runs (latest migration)
COL_EXISTS=$(_psql_query \
  "SELECT COUNT(*) FROM information_schema.columns
   WHERE table_schema='them' AND table_name='runs' AND column_name='events_transport';")
if [ "${COL_EXISTS}" = "1" ]; then
  _ok "Column them.runs.events_transport exists (Phase 11c column present)"
else
  _fail "Column them.runs.events_transport NOT found"
fi

# Verify NO demo/test data was inserted automatically
AGENT_COUNT=$(_psql_query "SELECT COUNT(*) FROM them.agents;")
if [ "${AGENT_COUNT}" = "0" ]; then
  _ok "No agents seeded automatically (demo data correctly omitted)"
else
  _fail "Unexpected agents in DB (${AGENT_COUNT} rows) — seed data should be opt-in"
fi

USER_COUNT=$(_psql_query "SELECT COUNT(*) FROM auth_service.users;")
if [ "${USER_COUNT}" = "0" ]; then
  _ok "No user accounts created automatically (user seeding correctly omitted)"
else
  _fail "Unexpected user accounts in DB (${USER_COUNT} rows) — user seeding should be opt-in"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 3: Partial-init detection ─────────────────────────────────────"
echo ""

# Simulate a partial init by emptying schema_migrations
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -c \
  "TRUNCATE them.schema_migrations;" > /dev/null

# Running linux-db-init.sh without --force-fresh should error on empty table
PARTIAL_OUTPUT=$("${SCRIPT_DIR}/linux-db-init.sh" 2>&1 || true)
PARTIAL_EXIT=$?
if [ "${PARTIAL_EXIT}" -ne 0 ] && echo "${PARTIAL_OUTPUT}" | grep -q "partial\|empty\|force-fresh"; then
  _ok "Partial-init detection: exits non-zero with clear diagnostic on empty schema_migrations"
else
  _fail "Partial-init detection: expected non-zero exit + diagnostic (got exit=${PARTIAL_EXIT})"
fi

# Restore migrations table
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -c \
  "INSERT INTO them.schema_migrations (version, description)
   VALUES ('025_events_transport', 'restored for test') ON CONFLICT DO NOTHING;" > /dev/null

# Now running linux-db-init.sh should be a no-op (exit 0, "already initialized" message)
INIT_OUTPUT="$("${SCRIPT_DIR}/linux-db-init.sh" 2>&1)"
INIT_RERUN_EXIT=$?
if [ "${INIT_RERUN_EXIT}" -eq 0 ] && \
   echo "${INIT_OUTPUT}" | grep -q "already initialized\|already-initialized"; then
  _ok "Re-run of linux-db-init.sh with populated schema_migrations is a no-op (exit 0)"
else
  _fail "Re-run of linux-db-init.sh did not short-circuit correctly (exit=${INIT_RERUN_EXIT})"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 4: Full Go-first stack startup ─────────────────────────────────"
echo ""

echo "  Running linux-start.sh..."
"${SCRIPT_DIR}/linux-start.sh" 2>&1 | tail -30

# Verify Go bridge health (direct)
for port in 8002 8003; do
  if curl -sf "http://localhost:${port}/health/live" 2>/dev/null | grep -q '"status"'; then
    _ok "Go bridge direct health: localhost:${port}/health/live OK"
  else
    _fail "Go bridge direct health: localhost:${port}/health/live unreachable"
  fi
done

# Verify Go health via Traefik path-rewrite
if curl -sf "http://${HOST_IP}:8088/go-health/live" 2>/dev/null | grep -q '"status"'; then
  _ok "Go health via Traefik: /go-health/live OK (path-rewrite working)"
else
  _fail "Go health via Traefik: /go-health/live failed"
fi

if curl -sf "http://${HOST_IP}:8088/go-health/ready" 2>/dev/null | grep -q '"redis"'; then
  _ok "Go ready via Traefik: /go-health/ready OK (Redis connection confirmed)"
else
  _fail "Go ready via Traefik: /go-health/ready failed"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 5: Traefik route ownership verification ────────────────────────"
echo ""

# /ws must reach Go (expects 401 from Go JWT gate, not 404/426 from Python)
WS_CODE="$(curl -sf -o /dev/null -w "%{http_code}" \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  "http://${HOST_IP}:8088/ws/orchestrate/app/ep" 2>/dev/null || echo "000")"
if [ "${WS_CODE}" = "401" ] || [ "${WS_CODE}" = "400" ]; then
  _ok "/ws route → Go bridge (HTTP ${WS_CODE}, Go JWT gate responding)"
else
  _fail "/ws route returned ${WS_CODE} (expected 401 from Go, not 404/426 from Python)"
fi

# /sse must reach Go (expects 401)
SSE_CODE="$(curl -sf -o /dev/null -w "%{http_code}" \
  "http://${HOST_IP}:8088/sse/orchestrate/app/ep" 2>/dev/null || echo "000")"
if [ "${SSE_CODE}" = "401" ] || [ "${SSE_CODE}" = "403" ]; then
  _ok "/sse route → Go bridge (HTTP ${SSE_CODE}, Go JWT gate responding)"
else
  _fail "/sse route returned ${SSE_CODE} (expected 401/403 from Go)"
fi

# /api/v1 must reach Python bridge (expects 401 from FastAPI, not Go)
API_CODE="$(curl -sf -o /dev/null -w "%{http_code}" \
  "http://${HOST_IP}:8088/api/v1/admin/agents" 2>/dev/null || echo "000")"
if [ "${API_CODE}" = "401" ] || [ "${API_CODE}" = "403" ]; then
  _ok "/api/v1 route → Python bridge (HTTP ${API_CODE})"
else
  _fail "/api/v1 route returned ${API_CODE} (expected 401/403)"
fi

# Verify Prometheus metrics confirm dual-mode
for port in 8002 8003; do
  if curl -sf "http://localhost:${port}/metrics" 2>/dev/null | grep -q "them_runstream_mode"; then
    _ok "Prometheus metrics: them_runstream_mode present on port ${port}"
  else
    _fail "Prometheus metrics: them_runstream_mode missing on port ${port}"
  fi
done

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 6: Full stack restart — no re-bootstrap ────────────────────────"
echo ""

# Insert a sentinel row before restart
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -c \
  "INSERT INTO them.config (config_key, config_value)
   VALUES ('_test_sentinel', '\"clean_install_test\"')
   ON CONFLICT (config_key) DO NOTHING;" > /dev/null

# Capture the applied_at timestamp of version 025 before restart
APPLIED_AT_BEFORE=$(_psql_query \
  "SELECT EXTRACT(EPOCH FROM applied_at)::bigint
   FROM them.schema_migrations ORDER BY applied_at LIMIT 1;")

# Stop and restart
echo "  Stopping stack..."
"${COMPOSE[@]}" down --remove-orphans 2>/dev/null || true
sleep 5
echo "  Restarting stack via linux-start.sh..."
"${SCRIPT_DIR}/linux-start.sh" 2>&1 | grep -E "==>|ERROR|WARN" | head -30

# Verify schema_migrations timestamps unchanged (no re-bootstrap)
APPLIED_AT_AFTER=$(_psql_query \
  "SELECT EXTRACT(EPOCH FROM applied_at)::bigint
   FROM them.schema_migrations ORDER BY applied_at LIMIT 1;")

if [ "${APPLIED_AT_BEFORE}" = "${APPLIED_AT_AFTER}" ]; then
  _ok "Schema not re-bootstrapped after restart (timestamps unchanged)"
else
  _fail "Schema_migrations timestamps changed after restart (unexpected re-bootstrap)"
fi

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Phase 7: Data integrity after restart ────────────────────────────────"
echo ""

# Verify sentinel row survives restart
SENTINEL=$(_psql_query \
  "SELECT config_value FROM them.config WHERE config_key='_test_sentinel';")
if [ "${SENTINEL}" = '"clean_install_test"' ]; then
  _ok "Sentinel config row survives stack restart"
else
  _fail "Sentinel config row not found after restart (data integrity issue)"
fi

# Verify version count unchanged
VERSION_COUNT_AFTER=$(_psql_query "SELECT COUNT(*) FROM them.schema_migrations;")
if [ "${VERSION_COUNT_AFTER}" = "${VERSION_COUNT}" ]; then
  _ok "schema_migrations row count unchanged after restart (${VERSION_COUNT_AFTER} rows)"
else
  _fail "schema_migrations row count changed after restart (was ${VERSION_COUNT}, now ${VERSION_COUNT_AFTER})"
fi

# Clean up sentinel
docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -c \
  "DELETE FROM them.config WHERE config_key='_test_sentinel';" > /dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "════════════════════════════════════════════════════════════════════"
echo " Clean-Install Validation Results"
echo "════════════════════════════════════════════════════════════════════"
echo " Passed: ${PASS}"
echo " Failed: ${FAIL}"
echo "════════════════════════════════════════════════════════════════════"

if [ "${FAIL}" -gt 0 ]; then
  echo " RESULT: FAILED — ${FAIL} check(s) did not pass."
  exit 1
else
  echo " RESULT: ALL CHECKS PASSED"
  exit 0
fi
