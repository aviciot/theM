#!/usr/bin/env bash
# linux-db-legacy-replay.sh — Full sequential replay of all DB migrations.
#
# ┌─────────────────────────────────────────────────────────────────────────────┐
# │  NOT PART OF NORMAL DEPLOYMENT                                              │
# │                                                                             │
# │  This script replays every migration from 001 → latest in order.           │
# │  It was the original bootstrap path before linux-db-init.sh existed.       │
# │                                                                             │
# │  For normal deployments:                                                    │
# │    Fresh install:  ./scripts/linux-start.sh  (calls linux-db-init.sh)      │
# │    Add migrations: ./scripts/linux-db-upgrade.sh db/NNN_name.sql           │
# │                                                                             │
# │  Use this script ONLY for:                                                  │
# │    - Reproducing a DB from scratch for debugging                            │
# │    - Verifying each migration is individually idempotent                    │
# │    - Historical CI replay of the full migration sequence                    │
# └─────────────────────────────────────────────────────────────────────────────┘
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-legacy-replay.sh
#
# Environment overrides:
#   POSTGRES_CONTAINER  (default: them-postgres)
#   REDIS_CONTAINER     (default: them-redis)
#   THE_M_DB_USER       (default: them)
#   THE_M_DB_NAME       (default: them)

set -euo pipefail

echo "╔══════════════════════════════════════════════════════════════════════╗"
echo "║  WARNING: linux-db-legacy-replay.sh is NOT the normal deploy path.  ║"
echo "║  For fresh installs, use: ./scripts/linux-start.sh                  ║"
echo "║  For upgrades, use:       ./scripts/linux-db-upgrade.sh <file.sql>  ║"
echo "╚══════════════════════════════════════════════════════════════════════╝"
echo ""
echo "Proceeding with full migration replay in 5 seconds... (Ctrl-C to abort)"
sleep 5

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"

# Helper: stream a file into psql via docker exec (no docker cp needed)
_psql_file() {
  local src="$1"
  echo "  Applying: $(basename "${src}")"
  docker exec -i "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
    < "${src}"
}

echo "==> [legacy-replay] Waiting for Postgres to be ready..."
for i in $(seq 1 24); do
  if docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
      > /dev/null 2>&1; then
    echo "  Postgres ready."
    break
  fi
  echo "  ... not ready (${i}/24)"
  sleep 5
done

docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  || { echo "ERROR: Postgres not ready after 2 minutes."; exit 1; }

echo "==> [legacy-replay] Applying base schema..."
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "CREATE SCHEMA IF NOT EXISTS auth_service;" -q
_psql_file "${PROJECT_DIR}/db/001_schema.sql"
_psql_file "${PROJECT_DIR}/auth_service/SCHEMA.sql"
_psql_file "${PROJECT_DIR}/db/002_seed.sql"

echo "==> [legacy-replay] Applying numbered migrations..."
for f in $(ls "${PROJECT_DIR}/db/"[0-9][0-9][0-9]_*.sql 2>/dev/null | sort); do
  basename_f="$(basename "${f}")"
  num="${basename_f%%_*}"
  [ "${num}" -le 2 ] && continue   # skip 001 + 002 already applied
  _psql_file "${f}"
done

echo "==> [legacy-replay] Flushing Redis cache..."
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
else
  echo "  Redis container '${REDIS_CONTAINER}' not running — skipping cache flush."
fi

echo "==> [legacy-replay] Complete."
echo ""
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "SELECT slug, enabled FROM them.agents ORDER BY slug;" 2>/dev/null || true
