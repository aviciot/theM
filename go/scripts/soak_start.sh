#!/usr/bin/env bash
# soak_start.sh — Start the complete hybrid stack for Phase 11b soak validation.
#
# What it starts:
#   - PostgreSQL (them-postgres)
#   - Redis (them-redis)
#   - Temporal server (temporal-frontend + temporal-history + temporal-matching + temporal-worker)
#   - Python Temporal worker (them-worker)
#   - Python bridge (them-bridge)
#   - Go bridge replica 1 (them-go-bridge)    — port 8002
#   - Go bridge replica 2 (them-go-bridge-2)  — port 8003
#   - Traefik reverse proxy (them-traefik)
#
# Both Go bridges run the reconciler with DryRun=true. They will compete for
# the advisory lock — only one sweeps at a time, the other logs "advisory lock held".
#
# Usage:
#   cd theM_gateway
#   bash ../go/scripts/soak_start.sh
#
# Prerequisites:
#   - Docker + docker compose v2 installed
#   - .env file generated (run .\generate-env.ps1 or ./generate-env.sh first)
#   - DB schema already applied (run soak_setup_db.sh once if first time)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/../../theM_gateway" && pwd)"

echo "==> [soak_start] Starting hybrid stack with two Go bridge replicas..."
echo "    Gateway dir: ${GATEWAY_DIR}"
cd "${GATEWAY_DIR}"

docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  --profile temporal \
  up -d --build \
  them-postgres them-redis temporal-frontend them-worker them-bridge \
  them-go-bridge them-go-bridge-2

echo ""
echo "==> [soak_start] Waiting for services to become healthy (up to 90s)..."

wait_healthy() {
  local container="$1"
  local timeout="${2:-60}"
  local elapsed=0
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null)" = "healthy" ]; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo "  WARN: ${container} not healthy after ${timeout}s — continuing anyway"
      return
    fi
    sleep 5
    elapsed=$((elapsed + 5))
    echo "  Waiting for ${container} (${elapsed}s)..."
  done
  echo "  ${container}: healthy"
}

wait_healthy "them-postgres" 60
wait_healthy "them-redis" 30
wait_healthy "them-go-bridge" 60
wait_healthy "them-go-bridge-2" 60

echo ""
echo "==> [soak_start] Waiting 20s for Python worker to connect to Temporal..."
sleep 20

echo ""
echo "==> [soak_start] Stack status:"
docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  --profile temporal \
  ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"

echo ""
echo "==> [soak_start] Done. Services running:"
echo "    Go bridge 1:  http://localhost:8002"
echo "    Go bridge 2:  http://localhost:8003"
echo "    Temporal UI:  http://localhost:3111  (if exposed)"
echo "    Python bridge: http://localhost:8088 (via Traefik)"
echo ""
echo "==> Next: run the soak validation:"
echo "    python3 go/scripts/soak_runner.py"
