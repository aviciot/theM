#!/usr/bin/env bash
# linux-start.sh — Start the full the-M stack on a Linux host.
#
# Brings up: Postgres, Redis, auth-service, Python bridge, Temporal frontend+UI+worker,
# both Go bridge replicas, and Traefik (with /ws + /sse + /go-health routing).
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-start.sh [--build] [--no-go-bridge]
#
# Options:
#   --build         Force rebuild of all images before starting
#   --no-go-bridge  Start without Go bridge replicas (Python-only mode)
#
# Prerequisites:
#   - Docker Engine >= 24 and docker compose v2 installed (docker compose, not docker-compose)
#   - .env file exists (copy from .env.linux.example and fill in secrets, or run ./generate-env.sh)
#   - DB migrations applied at least once (run scripts/linux-migrate.sh on first deploy)
#
# On first run: scripts/linux-migrate.sh must be run after postgres is healthy.
# On subsequent runs: migrations are idempotent and can be re-run safely.
#
# File permissions (set once after clone):
#   chmod +x scripts/linux-start.sh scripts/linux-stop.sh scripts/linux-migrate.sh
#   chmod +x scripts/linux-health.sh scripts/linux-logs.sh scripts/linux-rollback.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

BUILD_FLAG=""
INCLUDE_GO_BRIDGE=true

for arg in "$@"; do
  case "$arg" in
    --build) BUILD_FLAG="--build" ;;
    --no-go-bridge) INCLUDE_GO_BRIDGE=false ;;
  esac
done

# Compose file stack — Linux overlay replaces local.yml
COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
)

cd "${GATEWAY_DIR}"

# Verify .env exists
if [ ! -f .env ]; then
  echo "ERROR: .env not found in ${GATEWAY_DIR}"
  echo "  Copy .env.linux.example to .env and fill in required values."
  echo "  Or run: ./generate-env.sh"
  exit 1
fi

echo "==> [linux-start] Validating compose config..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal config --quiet

echo "==> [linux-start] Starting infrastructure (Postgres, Redis)..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  up -d ${BUILD_FLAG} them-postgres them-redis them-auth-service

echo "==> [linux-start] Waiting for Postgres to be healthy (up to 60s)..."
_wait_healthy() {
  local container="$1" timeout="${2:-60}" elapsed=0
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null)" = "healthy" ]; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo "  WARN: ${container} not healthy after ${timeout}s — check logs with: docker logs ${container}"
      return 1
    fi
    sleep 5; elapsed=$((elapsed + 5))
    echo "  ... ${container} (${elapsed}s elapsed)"
  done
  echo "  ${container}: healthy"
}

_wait_healthy "them-postgres" 60
_wait_healthy "them-redis"    30
_wait_healthy "them-auth-service" 60

echo "==> [linux-start] Starting Temporal + Python services..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  up -d ${BUILD_FLAG} temporal-frontend temporal-admin-tools temporal-ui them-worker them-bridge them-frontend

echo "==> [linux-start] Waiting for Temporal frontend (up to 90s)..."
_wait_healthy "them-bridge" 90

echo "==> [linux-start] Starting Traefik..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  up -d them-traefik

if [ "${INCLUDE_GO_BRIDGE}" = "true" ]; then
  echo "==> [linux-start] Starting Go bridge replicas..."
  docker compose "${COMPOSE_FILES[@]}" --profile temporal \
    up -d ${BUILD_FLAG} them-go-bridge them-go-bridge-2
  _wait_healthy "them-go-bridge"   60
  _wait_healthy "them-go-bridge-2" 60
fi

echo ""
echo "==> [linux-start] Stack status:"
docker compose "${COMPOSE_FILES[@]}" --profile temporal ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"

echo ""
echo "==> [linux-start] Service endpoints:"
echo "    Frontend / API:      http://$(hostname -I | awk '{print $1}'):8088"
echo "    Traefik dashboard:   http://$(hostname -I | awk '{print $1}'):8089"
echo "    Go bridge 1 (direct): http://localhost:8002/health/ready"
echo "    Go bridge 2 (direct): http://localhost:8003/health/ready"
echo "    Temporal UI:          http://$(hostname -I | awk '{print $1}'):8088/temporal/"
echo ""
echo "==> [linux-start] Done. Run health check:"
echo "    ./scripts/linux-health.sh"
