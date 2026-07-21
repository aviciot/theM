#!/usr/bin/env bash
# linux-db-upgrade.sh — Apply new schema migrations to an existing the-M deployment.
#
# For UPGRADES ONLY — when new migration files have been added to db/ after the
# initial deployment. Does NOT replay all migrations, only what you specify.
#
# Migration protocol:
#   1. Acquires pg_try_advisory_lock(987654321) — fails if another deploy is migrating
#   2. Checks them.schema_migrations to skip already-applied versions
#   3. Applies the migration SQL under a transaction
#   4. Inserts a row into them.schema_migrations on success
#   5. Releases the advisory lock
#
# Version convention:
#   File: db/026_feature_name.sql → version = "026_feature_name" (basename minus .sql)
#   Version must match the regex: ^\d{3}[a-z]?(_[a-z0-9_]+)?$
#
# For a FRESH installation, use linux-start.sh (calls linux-db-init.sh automatically).
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-upgrade.sh db/026_new_feature.sql
#   ./scripts/linux-db-upgrade.sh db/026_new_feature.sql db/027_another.sql
#
# Environment overrides:
#   POSTGRES_CONTAINER  (default: them-postgres)
#   REDIS_CONTAINER     (default: them-redis)
#   THE_M_DB_USER       (default: them)
#   THE_M_DB_NAME       (default: them)

set -euo pipefail

if [ $# -eq 0 ]; then
  echo "Usage: $0 <migration.sql> [migration.sql ...]" >&2
  echo "  Apply specific new migration files to an existing deployment." >&2
  echo "  For fresh installs, use: ./scripts/linux-start.sh" >&2
  exit 1
fi

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"

_psql() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" "$@"
}

_psql_query() {
  _psql -tAc "$1" 2>/dev/null || echo ""
}

echo "==> [db-upgrade] Verifying Postgres is reachable..."
docker exec "${POSTGRES_CONTAINER}" \
  pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  || { echo "ERROR: Postgres not reachable." >&2; exit 1; }

# Verify schema_migrations table exists (must be initialized first)
MIGRATIONS_TABLE=$(_psql_query \
  "SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema='them' AND table_name='schema_migrations';")

if [ "${MIGRATIONS_TABLE}" != "1" ]; then
  echo "ERROR: them.schema_migrations does not exist." >&2
  echo "  Run ./scripts/linux-start.sh first to initialize the schema." >&2
  exit 1
fi

# Acquire advisory lock
echo "==> [db-upgrade] Acquiring advisory lock..."
LOCK_ACQUIRED=$(_psql_query "SELECT pg_try_advisory_lock(987654321);")
if [ "${LOCK_ACQUIRED}" != "t" ]; then
  echo "ERROR: Could not acquire advisory lock (another migration is in progress)." >&2
  echo "  Wait for the other process to complete, or run:" >&2
  echo "    docker exec ${POSTGRES_CONTAINER} psql -U ${THE_M_DB_USER} -d ${THE_M_DB_NAME} -c \"SELECT pg_advisory_unlock(987654321);\"" >&2
  exit 1
fi
echo "  Advisory lock acquired (987654321)."

_release_lock() {
  _psql -tAc "SELECT pg_advisory_unlock(987654321);" > /dev/null 2>&1 || true
}
trap _release_lock EXIT

# Apply each migration file
APPLIED=0
SKIPPED=0
FAILED=0

for f in "$@"; do
  if [ ! -f "${f}" ]; then
    echo "ERROR: file not found: ${f}" >&2
    FAILED=$((FAILED + 1))
    continue
  fi

  # Derive version from filename: db/026_new_feature.sql → 026_new_feature
  VERSION="$(basename "${f}" .sql)"

  # Check if already applied
  ALREADY=$(_psql_query \
    "SELECT COUNT(*) FROM them.schema_migrations WHERE version = '${VERSION}';")

  if [ "${ALREADY}" = "1" ]; then
    echo "  Skipping: ${VERSION} (already applied)"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  echo "  Applying: ${VERSION}"

  # Apply in a transaction with ON_ERROR_STOP
  if docker exec -i "${POSTGRES_CONTAINER}" \
      psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
      -q -v ON_ERROR_STOP=1 \
      < "${f}"; then

    # Record successful application
    DESCRIPTION="$(head -5 "${f}" | grep -m1 '^\s*--' | sed 's/^--[[:space:]]*//' || echo "${VERSION}")"
    _psql -c "INSERT INTO them.schema_migrations (version, description)
              VALUES ('${VERSION}', '${DESCRIPTION//\'/\'\'}')
              ON CONFLICT (version) DO NOTHING;" > /dev/null

    echo "    Recorded in them.schema_migrations."
    APPLIED=$((APPLIED + 1))
  else
    echo "ERROR: Migration ${VERSION} failed." >&2
    FAILED=$((FAILED + 1))
    echo "  Rolling back is automatic (each migration should be transactional)." >&2
    echo "  Fix the issue and re-run this script." >&2
    exit 1
  fi
done

# Flush Redis caches so schema-linked caches are invalidated
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
fi

echo "==> [db-upgrade] Done. Applied: ${APPLIED}, Skipped: ${SKIPPED}, Failed: ${FAILED}"
[ "${FAILED}" -gt 0 ] && exit 1 || exit 0
