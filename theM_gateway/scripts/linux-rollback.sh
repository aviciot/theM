#!/usr/bin/env bash
# linux-rollback.sh — Roll back the Go bridge to a previous image tag.
#
# The Python bridge, Temporal, Postgres, and Redis are NOT rolled back —
# they are stateful services whose rollback requires a DB backup restore (out of scope).
#
# This script supports rolling back only the Go binary (stateless, single binary image)
# by re-tagging a previous image and restarting the Go bridge containers.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-rollback.sh --tag <image-tag>
#   ./scripts/linux-rollback.sh --list    # list available Go bridge image tags
#
# Examples:
#   ./scripts/linux-rollback.sh --list
#   ./scripts/linux-rollback.sh --tag them_gateway-them-go-bridge:20260721-abc1234
#
# After rollback, verify with:
#   ./scripts/linux-health.sh

set -euo pipefail

ACTION=""
TAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --list) ACTION="list" ;;
    --tag)  shift; TAG="$1"; ACTION="rollback" ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
  shift
done

COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$(cd "${SCRIPT_DIR}/.." && pwd)"

if [ "${ACTION}" = "list" ]; then
  echo "==> [linux-rollback] Available Go bridge image tags:"
  docker images --format "{{.Repository}}:{{.Tag}}\t{{.CreatedAt}}\t{{.Size}}" \
    | grep "them-go-bridge" | sort -r
  exit 0
fi

if [ "${ACTION}" != "rollback" ] || [ -z "${TAG}" ]; then
  echo "Usage: $0 --list | --tag <image-tag>"
  exit 1
fi

echo "==> [linux-rollback] Rolling back Go bridges to image: ${TAG}"

# Tag as latest so compose picks it up without rebuilding
docker tag "${TAG}" them_gateway-them-go-bridge:latest

# Stop Go bridges (graceful, 30s timeout)
echo "==> [linux-rollback] Stopping Go bridges..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  stop --timeout 30 them-go-bridge them-go-bridge-2

# Start with the re-tagged image (no --build, use local image)
echo "==> [linux-rollback] Starting Go bridges with rolled-back image..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  up -d --no-build them-go-bridge them-go-bridge-2

echo "==> [linux-rollback] Waiting for bridges to become healthy..."
_wait() {
  local c="$1" t="${2:-60}" e=0
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${c}" 2>/dev/null)" = "healthy" ]; do
    [ "${e}" -ge "${t}" ] && { echo "  WARN: ${c} not healthy after ${t}s"; return 1; }
    sleep 5; e=$((e + 5)); echo "  ... ${c} (${e}s)"
  done
  echo "  ${c}: healthy"
}

_wait "them-go-bridge"   60
_wait "them-go-bridge-2" 60

echo "==> [linux-rollback] Rollback complete. Run health check:"
echo "    ./scripts/linux-health.sh"
