#!/usr/bin/env bash
# linux-start-hetzner.sh — Start the the-M stack on the Hetzner VPC server.
#
# Hetzner-specific differences from the generic linux-start.sh:
#   - docker-compose.hetzner-build.yml: overrides build contexts to parent (them/)
#     because Dockerfiles live there, not in theM_gateway/ on this server
#   - docker-compose.cloudflare.yml: joins them-traefik to the shared proxy-network
#     (infra-traefik's network) so infra-traefik can route them.avico78.com → them-traefik:8088
#
# Hetzner-specific files:
#   - docker-compose.hetzner-build.yml   build context overrides
#   - docker-compose.cloudflare.yml      proxy-network + Traefik label
#   - scripts/linux-start-hetzner.sh     this script
#
# Prerequisites (already in place on this server):
#   - infra-traefik running on proxy-network
#   - infra-cloudflared running (Cloudflare Tunnel)
#   - /home/avi/infrastructure/traefik/dynamic/them-routes.yml present
#     (router commented out until UI is ready to expose — see INSTALL.md)
#   - .env present with them.avico78.com URLs and ANTHROPIC_API_KEY set
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-start-hetzner.sh [--build]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_FLAG=""

for arg in "$@"; do
  case "$arg" in
    --build) BUILD_FLAG="--build" ;;
    --*)     echo "Unknown option: ${arg}"; exit 1 ;;
  esac
done

# Full Hetzner compose stack — includes build overrides + cloudflare network overlay
COMPOSE=(
  docker compose
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
  -f docker-compose.hetzner-build.yml
  -f docker-compose.cloudflare.yml
  --profile temporal
)

cd "${GATEWAY_DIR}"

# ── Step 1: Validate environment ───────────────────────────────────────────────
echo "==> [hetzner] Validating environment..."

if [ ! -f .env ]; then
  echo "ERROR: .env not found. Run ./generate-env.sh then add ANTHROPIC_API_KEY."
  exit 1
fi

for _var in THE_M_DB_PASSWORD THE_M_SECRET_KEY THE_M_JWT_SECRET ANTHROPIC_API_KEY; do
  _v="$(grep "^${_var}=" .env | cut -d= -f2- | tr -d '[:space:]')"
  if [ -z "${_v}" ] || [[ "${_v}" == CHANGE_ME* ]]; then
    echo "ERROR: ${_var} not set in .env"
    exit 1
  fi
done

echo "  Validating compose config..."
"${COMPOSE[@]}" config --quiet
echo "  Environment OK."

# ── Step 2: Infrastructure — Postgres + Redis ──────────────────────────────────
echo "==> [hetzner] Starting Postgres and Redis..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-postgres them-redis

_wait_healthy() {
  local container="$1" timeout="${2:-60}" elapsed=0
  echo -n "  Waiting for ${container}..."
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null)" = "healthy" ]; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo ""
      echo "  ERROR: ${container} not healthy after ${timeout}s"
      docker logs "${container}" --tail 20
      return 1
    fi
    sleep 5; elapsed=$((elapsed + 5)); echo -n "."
  done
  echo " healthy (${elapsed}s)"
}

_wait_healthy "them-postgres" 90
_wait_healthy "them-redis"    30

# ── Step 3: Temporal ──────────────────────────────────────────────────────────
echo "==> [hetzner] Starting Temporal..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} temporal-frontend temporal-admin-tools temporal-ui

echo -n "  Waiting for temporal-frontend..."
for i in $(seq 1 18); do
  _s="$(docker inspect --format='{{.State.Health.Status}}' temporal-frontend 2>/dev/null || echo starting)"
  if [ "${_s}" = "healthy" ] || [ "${_s}" = "unhealthy" ]; then
    echo " ${_s}"; break
  fi
  sleep 5; echo -n "."; [ "${i}" -eq 18 ] && echo " (timeout — continuing)"
done

# ── Step 4: Initialize or verify DB schema ────────────────────────────────────
echo "==> [hetzner] Initializing DB schema..."
"${SCRIPT_DIR}/linux-db-init.sh"

# ── Step 5: Auth service ──────────────────────────────────────────────────────
echo "==> [hetzner] Starting auth service..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-auth-service
_wait_healthy "them-auth-service" 60

# ── Step 6: Python Temporal worker ────────────────────────────────────────────
echo "==> [hetzner] Starting Python Temporal worker..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-worker

echo "  Waiting for Temporal worker to register (up to 120s)..."
_WORKER_TIMEOUT=120; _WORKER_ELAPSED=0; _WORKER_READY=false
while [ "${_WORKER_ELAPSED}" -lt "${_WORKER_TIMEOUT}" ]; do
  _STATE="$(docker inspect --format='{{.State.Status}}' them-worker 2>/dev/null || echo absent)"
  if [ "${_STATE}" != "running" ]; then
    _EXIT="$(docker inspect --format='{{.State.ExitCode}}' them-worker 2>/dev/null || echo unknown)"
    if [ "${_EXIT}" != "0" ] && [ "${_EXIT}" != "unknown" ]; then
      echo ""; echo "ERROR: them-worker exited (code ${_EXIT})"
      docker logs them-worker --tail 30; exit 1
    fi
    sleep 3; _WORKER_ELAPSED=$((_WORKER_ELAPSED + 3)); echo -n "."; continue
  fi
  if docker exec temporal-admin-tools \
       temporal task-queue describe --task-queue them-orchestration --namespace default \
       2>/dev/null | grep -q "Poller\|poller\|worker"; then
    _WORKER_READY=true; break
  fi
  sleep 3; _WORKER_ELAPSED=$((_WORKER_ELAPSED + 3)); echo -n "."
done
echo ""
[ "${_WORKER_READY}" != "true" ] && { echo "ERROR: Temporal worker not ready"; exit 1; }
echo "  Temporal worker ready (${_WORKER_ELAPSED}s)."

# ── Step 7: Go bridge replicas ────────────────────────────────────────────────
echo "==> [hetzner] Starting Go bridge replicas..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-go-bridge them-go-bridge-2
_wait_healthy "them-go-bridge"   90
_wait_healthy "them-go-bridge-2" 90

# ── Step 8: Traefik (with proxy-network via cloudflare overlay) ───────────────
echo "==> [hetzner] Starting Traefik..."
if ! "${COMPOSE[@]}" up -d them-traefik 2>&1; then
  echo "  Traefik port binding failed. Waiting 35s..."
  docker rm -f them-traefik 2>/dev/null || true
  sleep 35
  "${COMPOSE[@]}" up -d them-traefik
fi

# ── Step 9: Python bridge + frontend ──────────────────────────────────────────
echo "==> [hetzner] Starting Python bridge and frontend..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-bridge them-frontend
_wait_healthy "them-bridge" 60

# ── Step 10: Summary ──────────────────────────────────────────────────────────
echo ""
"${COMPOSE[@]}" ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"
echo ""
echo "==> [hetzner] Stack ready."
echo "    Internal:       http://localhost:8088"
echo "    Traefik dash:   http://localhost:8089"
echo "    Go health:      http://localhost:8088/go-health/live"
echo "    Go health:      http://localhost:8088/go-health/ready"
echo "    External:       NOT exposed (see INSTALL.md — 'Expose to Cloudflare when ready')"
echo ""
echo "==> [hetzner] Run health check:"
echo "    ./scripts/linux-health.sh"
