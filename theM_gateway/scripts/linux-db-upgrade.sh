#!/usr/bin/env bash
# linux-db-upgrade.sh — Apply new schema migrations to an existing the-M deployment.
#
# This script is for UPGRADES only — when new migration files have been added to db/
# after the initial deployment. It applies only the SQL files you specify, in order.
#
# For a FRESH deployment, linux-db-init.sh (called by linux-start.sh) handles
# everything. You do not need this script on first install.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-upgrade.sh db/026_new_feature.sql db/027_another.sql
#
#   Or apply all migrations newer than a known baseline:
#   ./scripts/linux-db-upgrade.sh $(ls db/[0-9][0-9][0-9]_*.sql | sort | awk -F/ '$NF > "025"')
#
# Each migration file must be idempotent (IF NOT EXISTS, CREATE OR REPLACE, etc.).
# Test on a copy of the database before running against production.
#
# Environment overrides:
#   POSTGRES_CONTAINER  (default: them-postgres)
#   THE_M_DB_USER       (default: them)
#   THE_M_DB_NAME       (default: them)
#   REDIS_CONTAINER     (default: them-redis)

set -euo pipefail

if [ $# -eq 0 ]; then
  echo "Usage: $0 <migration.sql> [migration.sql ...]"
  echo "  Apply specific migration files to an existing deployment."
  echo "  For fresh installs, use linux-start.sh (calls linux-db-init.sh automatically)."
  exit 1
fi

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"

echo "==> [db-upgrade] Verifying Postgres is reachable..."
docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  || { echo "ERROR: Postgres not reachable."; exit 1; }

echo "==> [db-upgrade] Applying ${#} migration(s)..."
for f in "$@"; do
  [ -f "${f}" ] || { echo "ERROR: file not found: ${f}"; exit 1; }
  echo "  Applying: $(basename "${f}")"
  docker exec -i "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
    < "${f}"
done

# Flush Redis caches so any schema-linked cache (agents, orchestrators) is invalidated
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
fi

echo "==> [db-upgrade] Done."
