#!/usr/bin/env bash
# linux-health.sh — Verify health of the running the-M stack on Linux.
#
# Checks each container's health status, verifies HTTP endpoints, and prints
# a summary. Exits 0 if all required services are healthy, 1 otherwise.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-health.sh

set -euo pipefail

PASS=0
FAIL=0

_check() {
  local name="$1" result="$2"
  if [ "${result}" = "ok" ]; then
    echo "  [OK]   ${name}"
    PASS=$((PASS + 1))
  else
    echo "  [FAIL] ${name}: ${result}"
    FAIL=$((FAIL + 1))
  fi
}

_container_health() {
  local container="$1"
  local status
  status="$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null || echo 'not_found')"
  echo "${status}"
}

_http_ok() {
  local url="$1"
  if curl -sf --max-time 5 "${url}" > /dev/null 2>&1; then
    echo "ok"
  else
    echo "HTTP check failed: ${url}"
  fi
}

echo "==> [linux-health] Container health checks..."

for container in them-postgres them-redis them-auth-service them-bridge them-worker \
                 them-go-bridge them-go-bridge-2 them-traefik; do
  status="$(_container_health "${container}")"
  case "${status}" in
    healthy)   _check "${container}" "ok" ;;
    not_found) _check "${container}" "container not found (may not be running)" ;;
    *)         _check "${container}" "status=${status}" ;;
  esac
done

echo ""
echo "==> [linux-health] HTTP endpoint checks..."

_check "Postgres readiness (pg_isready)" \
  "$(docker exec them-postgres pg_isready -U them -d them > /dev/null 2>&1 && echo ok || echo failed)"

_check "Redis ping" \
  "$(docker exec them-redis redis-cli ping 2>/dev/null | grep -q PONG && echo ok || echo failed)"

_check "Auth service /health" \
  "$(_http_ok http://localhost:8088/health || _http_ok http://localhost:8001/health/live 2>/dev/null && echo ok || echo failed)"

_check "Go bridge 1 /health/live" \
  "$(_http_ok http://localhost:8002/health/live)"

_check "Go bridge 1 /health/ready" \
  "$(_http_ok http://localhost:8002/health/ready)"

_check "Go bridge 2 /health/live" \
  "$(_http_ok http://localhost:8003/health/live)"

_check "Go bridge 2 /health/ready" \
  "$(_http_ok http://localhost:8003/health/ready)"

_check "Traefik /go-health/live (via proxy)" \
  "$(_http_ok http://localhost:8088/go-health/live)"

_check "Traefik /go-health/ready (via proxy)" \
  "$(_http_ok http://localhost:8088/go-health/ready)"

_check "Traefik dashboard" \
  "$(_http_ok http://localhost:8089/api/overview)"

echo ""
echo "==> [linux-health] Prometheus metrics sample (Go bridge 1)..."
curl -sf --max-time 5 http://localhost:8002/metrics 2>/dev/null \
  | grep -E "^them_runstream_mode|^them_reconciler_scanned" \
  | head -5 || echo "  (metrics endpoint not reachable)"

echo ""
echo "==> [linux-health] DB connectivity check..."
docker exec them-postgres psql -U them -d them \
  -c "SELECT COUNT(*) AS run_count FROM them.runs;" 2>/dev/null \
  | head -4 || echo "  (DB query failed)"

echo ""
echo "==> [linux-health] Summary: ${PASS} passed, ${FAIL} failed."
[ "${FAIL}" -eq 0 ] && exit 0 || exit 1
