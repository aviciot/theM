#!/usr/bin/env bash
# soak_stop.sh — Tear down the Phase 11b soak stack.
#
# Stops all containers, optionally removes soak-seeded rows from DB.
# Data volumes are preserved by default — add --volumes to wipe everything.
#
# Usage:
#   cd theM_gateway
#   bash ../go/scripts/soak_stop.sh [--volumes] [--clean-db]
#
#   --volumes   Also remove Docker named volumes (wipes all DB data)
#   --clean-db  Remove soak-seeded rows from DB without wiping volumes

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/../../theM_gateway" && pwd)"

REMOVE_VOLUMES=0
CLEAN_DB=0
for arg in "$@"; do
  case "$arg" in
    --volumes)  REMOVE_VOLUMES=1 ;;
    --clean-db) CLEAN_DB=1 ;;
  esac
done

echo "==> [soak_stop] Stopping soak stack..."
cd "${GATEWAY_DIR}"

COMPOSE_FLAGS=(
  -f docker-compose.yml
  -f docker-compose.local.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  --profile temporal
)

if [ "${REMOVE_VOLUMES}" -eq 1 ]; then
  echo "    WARNING: --volumes specified — all DB data will be removed."
  docker compose "${COMPOSE_FLAGS[@]}" down --volumes
else
  docker compose "${COMPOSE_FLAGS[@]}" down
fi

echo ""
echo "==> [soak_stop] Containers stopped."

if [ "${CLEAN_DB}" -eq 1 ] && [ "${REMOVE_VOLUMES}" -eq 0 ]; then
  echo ""
  echo "==> [soak_stop] Cleaning soak-seeded rows from DB..."

  # Wait briefly for postgres to still be accessible (it may still be up from partial shutdown)
  if docker ps --format '{{.Names}}' | grep -q "^them-postgres$"; then
    docker exec them-postgres psql -U them -d them -q << 'EOSQL'
-- Remove soak-seeded synthetic runs (not Go-path workflow runs)
DELETE FROM them.runs
WHERE context_id IN (
  SELECT id::text FROM them.runs
  WHERE status = 'running'
    AND started_at < now() - interval '3 minutes'
);

-- Remove the soak test bearer token
DELETE FROM them.access_tokens
WHERE description = 'Phase 11b soak test token';

-- Remove soak entry points + app (cascades to EPs)
DELETE FROM them.entry_points WHERE slug IN ('soak_ws', 'soak_sse');
DELETE FROM them.applications WHERE slug = 'soak_app';
DELETE FROM them.orchestrators WHERE name = 'soak_test';
EOSQL
    echo "    Soak seed data removed."
  else
    echo "    WARN: them-postgres not running; skipping DB clean."
  fi
fi

echo ""
echo "==> [soak_stop] Done."
echo ""
echo "    To restart: bash go/scripts/soak_start.sh"
echo "    To wipe all data: bash go/scripts/soak_stop.sh --volumes"
