#!/usr/bin/env bash
# linux-logs.sh — Collect and optionally archive logs from the the-M stack.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-logs.sh [--tail N] [--since 1h] [--save /path/to/dir] [service...]
#
# Options:
#   --tail N        Show last N lines per service (default: 100)
#   --since PERIOD  Show logs since duration (e.g. 1h, 30m, 2024-01-15) (default: 1h)
#   --save DIR      Save logs to DIR as <service>.log files for archiving
#   service...      Specific service names to collect (default: all core services)
#
# Examples:
#   ./scripts/linux-logs.sh --tail 50 them-go-bridge
#   ./scripts/linux-logs.sh --since 30m --save /tmp/them-logs
#   ./scripts/linux-logs.sh --tail 200

set -euo pipefail

TAIL=100
SINCE="1h"
SAVE_DIR=""
SERVICES=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tail)  shift; TAIL="$1" ;;
    --since) shift; SINCE="$1" ;;
    --save)  shift; SAVE_DIR="$1" ;;
    --*) echo "Unknown option: $1"; exit 1 ;;
    *) SERVICES+=("$1") ;;
  esac
  shift
done

if [ ${#SERVICES[@]} -eq 0 ]; then
  SERVICES=(
    them-postgres them-redis them-auth-service
    them-bridge them-worker
    them-go-bridge them-go-bridge-2
    them-traefik temporal-frontend
  )
fi

COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$(cd "${SCRIPT_DIR}/.." && pwd)"

if [ -n "${SAVE_DIR}" ]; then
  mkdir -p "${SAVE_DIR}"
  echo "==> [linux-logs] Saving logs to ${SAVE_DIR}..."
  for svc in "${SERVICES[@]}"; do
    echo "  Collecting ${svc}..."
    docker compose "${COMPOSE_FILES[@]}" --profile temporal \
      logs --no-log-prefix --since "${SINCE}" --tail "${TAIL}" "${svc}" \
      > "${SAVE_DIR}/${svc}.log" 2>&1 || echo "  (${svc} not running)"
  done
  echo "==> [linux-logs] Logs saved to ${SAVE_DIR}/"
else
  echo "==> [linux-logs] Streaming logs (tail=${TAIL}, since=${SINCE}) for: ${SERVICES[*]}"
  docker compose "${COMPOSE_FILES[@]}" --profile temporal \
    logs --since "${SINCE}" --tail "${TAIL}" "${SERVICES[@]}" 2>&1 || true
fi
