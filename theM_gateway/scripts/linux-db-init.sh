#!/usr/bin/env bash
# linux-db-init.sh — Initialize or verify the the-M database schema.
#
# Behavior:
#   - Fresh DB (them.runs table absent): applies the complete final schema in one pass.
#   - Existing DB (them.runs table present): no-op — schema is already initialized.
#     Use linux-db-upgrade.sh to apply new migrations on an existing deployment.
#
# This script is called automatically by linux-start.sh on every startup.
# It is safe to run multiple times.
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-init.sh
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

# ── Wait for Postgres ──────────────────────────────────────────────────────────
echo "==> [db-init] Waiting for Postgres..."
for i in $(seq 1 24); do
  if docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
      > /dev/null 2>&1; then
    echo "  Postgres ready."
    break
  fi
  [ "${i}" -eq 24 ] && { echo "ERROR: Postgres not ready after 2 minutes."; exit 1; }
  echo "  ... not ready (${i}/24)"; sleep 5
done

# ── Detect whether schema already exists ──────────────────────────────────────
SCHEMA_EXISTS=$(docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -tAc "SELECT COUNT(*) FROM information_schema.tables
         WHERE table_schema='them' AND table_name='runs';" 2>/dev/null || echo "0")

if [ "${SCHEMA_EXISTS}" = "1" ]; then
  echo "==> [db-init] Schema already initialized — skipping (use linux-db-upgrade.sh for new migrations)."
  exit 0
fi

echo "==> [db-init] Fresh database — applying complete schema..."

# Helper: stream a file into psql via docker exec (no docker cp needed)
_psql_file() {
  local src="$1"
  echo "  Applying: $(basename "${src}")"
  docker exec -i "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
    < "${src}"
}

# Step 1: Create schemas
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "CREATE SCHEMA IF NOT EXISTS auth_service;" -q

# Step 2: Base schema + auth service schema + seed
_psql_file "${PROJECT_DIR}/db/001_schema.sql"
_psql_file "${PROJECT_DIR}/auth_service/SCHEMA.sql"
_psql_file "${PROJECT_DIR}/db/002_seed.sql"

# Step 3: All numbered migrations in order (003 → latest)
# Each file is idempotent — uses IF NOT EXISTS, CREATE OR REPLACE, etc.
for f in $(ls "${PROJECT_DIR}/db/"[0-9][0-9][0-9]_*.sql 2>/dev/null | sort); do
  num="$(basename "${f}" | cut -d_ -f1)"
  [ "${num}" -le 2 ] && continue   # skip 001 + 002 already applied
  _psql_file "${f}"
done

# Step 4: Flush Redis cache so the bridge picks up freshly seeded IDs
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
fi

echo "==> [db-init] Schema initialization complete."
echo ""
docker exec "${POSTGRES_CONTAINER}" psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  -c "SELECT slug, enabled FROM them.agents ORDER BY slug;" 2>/dev/null || true
