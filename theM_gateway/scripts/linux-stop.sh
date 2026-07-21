#!/usr/bin/env bash
# linux-stop.sh — Gracefully stop the the-M stack on Linux.
#
# Sends SIGTERM to all containers and waits up to --timeout seconds before SIGKILL.
# Does NOT remove volumes — data persists after stop/start.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-stop.sh [--timeout 30] [--remove-orphans] [--volumes]
#
# Options:
#   --timeout N      Seconds to wait for graceful shutdown per container (default: 30)
#   --remove-orphans Remove containers not defined in compose files
#   --volumes        DANGER: also remove named volumes (data will be lost)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

STOP_TIMEOUT=30
EXTRA_FLAGS=""

for arg in "$@"; do
  case "$arg" in
    --timeout) shift; STOP_TIMEOUT="${1:-30}" ;;
    --remove-orphans) EXTRA_FLAGS="${EXTRA_FLAGS} --remove-orphans" ;;
    --volumes)
      echo "WARNING: --volumes will delete all persistent data (Postgres, Redis, logs)."
      read -r -p "Type 'yes' to confirm: " confirm
      [ "${confirm}" = "yes" ] || { echo "Aborted."; exit 1; }
      EXTRA_FLAGS="${EXTRA_FLAGS} --volumes"
      ;;
  esac
done

COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
)

cd "${GATEWAY_DIR}"

echo "==> [linux-stop] Stopping the-M stack (timeout=${STOP_TIMEOUT}s)..."
docker compose "${COMPOSE_FILES[@]}" --profile temporal \
  down --timeout "${STOP_TIMEOUT}" ${EXTRA_FLAGS}

echo "==> [linux-stop] Done. Volumes preserved (use --volumes to delete)."
