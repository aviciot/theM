#!/usr/bin/env bash
# linux-migrate.sh — Apply all DB migrations to the running Postgres container.
#
# Safe to run multiple times — each migration file is idempotent (uses IF NOT EXISTS,
# CREATE OR REPLACE, ALTER TABLE ... IF NOT EXISTS, etc.).
#
# Run order:
#   1. Base schema (001_schema.sql)
#   2. Auth service schema (auth_service/SCHEMA.sql)
#   3. Seed data (002_seed.sql)
#   4. All numbered migrations in db/ order (003_*, 004_*, ...)
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-migrate.sh
#
# Environment overrides:
#   POSTGRES_CONTAINER  (default: them-postgres)
#   REDIS_CONTAINER     (default: them-redis)
#   THE_M_DB_USER       (default: them)
#   THE_M_DB_NAME       (default: them)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"

# Helper: copy file into container and execute it
_run_sql() {
  local src="$1"
  local dest="/tmp/$(basename "${src}")"
  echo "  Applying: $(basename "${src}")"
  docker cp "${src}" "${POSTGRES_CONTAINER}:${dest}"
  docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -f "${dest}" -q
}

echo "==> [linux-migrate] Waiting for Postgres to be ready..."
for i in $(seq 1 24); do
  if docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
      > /dev/null 2>&1; then
    echo "  Postgres ready."
    break
  fi
  echo "  ... not ready ($i/24)"
  sleep 5
done

# Verify container is reachable
docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  || { echo "ERROR: Postgres not ready after 2 minutes."; exit 1; }

echo "==> [linux-migrate] Applying base schema..."
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "CREATE SCHEMA IF NOT EXISTS auth_service;" -q
_run_sql "${PROJECT_DIR}/db/001_schema.sql"
_run_sql "${PROJECT_DIR}/auth_service/SCHEMA.sql"
_run_sql "${PROJECT_DIR}/db/002_seed.sql"

echo "==> [linux-migrate] Applying numbered migrations..."
# Sort numerically so 003 < 004 < ... < 025
for f in $(ls "${PROJECT_DIR}/db/"[0-9][0-9][0-9]_*.sql 2>/dev/null | sort); do
  # Skip 001 and 002 (already applied above)
  basename_f="$(basename "${f}")"
  num="${basename_f%%_*}"
  [ "${num}" -le 2 ] && continue
  _run_sql "${f}"
done

echo "==> [linux-migrate] Flushing Redis agent/orchestrator cache..."
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null
  echo "  Redis cache flushed."
else
  echo "  Redis container '${REDIS_CONTAINER}' not running — skipping cache flush."
fi

echo "==> [linux-migrate] Migration complete."
echo ""
echo "Agents seeded:"
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "SELECT slug, display_name, enabled FROM them.agents ORDER BY slug;" 2>/dev/null || true
echo ""
echo "Orchestrators seeded:"
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "SELECT name, display_name, llm_model, enabled FROM them.orchestrators ORDER BY name;" 2>/dev/null || true
