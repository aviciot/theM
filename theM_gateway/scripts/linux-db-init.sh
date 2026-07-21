#!/usr/bin/env bash
# linux-db-init.sh — Bootstrap the the-M database schema on a fresh installation.
#
# Detection strategy:
#   Checks them.schema_migrations for the presence of ANY applied version.
#   A partially-initialized DB (no schema_migrations table, or empty) is treated
#   as an error unless --force-fresh is passed.
#   A fully-initialized DB (any row in schema_migrations) is a no-op.
#
# Fresh install path:
#   Applies db/schema_current.sql — a single-file snapshot of the complete
#   current schema. Does NOT replay migration history (001..025). Idempotent.
#
# Seed data:
#   Does NOT insert demo agents, orchestrators, or user accounts automatically.
#   Use --seed-users to also apply db/seed_users.sql (dev/staging only).
#   Use --seed-demo to also apply db/seed_demo.sql (optional demo data).
#
# Locking:
#   Uses pg_try_advisory_lock(987654321) to prevent concurrent bootstrap if
#   two processes call this script simultaneously (e.g. blue/green deploy).
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-init.sh [--seed-users] [--seed-demo] [--force-fresh]
#
# Options:
#   --seed-users    Also apply db/seed_users.sql (admin+avi dev accounts).
#                   DO NOT use on production.
#   --seed-demo     Also apply db/seed_demo.sql (demo agents + orchestrators).
#                   DO NOT use on production.
#   --force-fresh   Allow bootstrap even if schema_migrations exists but is empty
#                   (partial/failed previous init). Normally exits with error.
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

OPT_SEED_USERS=false
OPT_SEED_DEMO=false
OPT_FORCE_FRESH=false

for arg in "$@"; do
  case "${arg}" in
    --seed-users)   OPT_SEED_USERS=true ;;
    --seed-demo)    OPT_SEED_DEMO=true ;;
    --force-fresh)  OPT_FORCE_FRESH=true ;;
    --*)            echo "Unknown option: ${arg}" >&2; exit 1 ;;
  esac
done

# ── Helper: run a SQL query via docker exec, return trimmed output ─────────────
_psql_query() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -tAc "$1" 2>/dev/null || echo ""
}

# ── Helper: stream a SQL file into psql via docker exec (no docker cp) ─────────
_psql_file() {
  local src="$1"
  echo "  Applying: $(basename "${src}")"
  docker exec -i "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
    < "${src}"
}

# ── Wait for Postgres ──────────────────────────────────────────────────────────
echo "==> [db-init] Waiting for Postgres..."
for i in $(seq 1 24); do
  if docker exec "${POSTGRES_CONTAINER}" \
      pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" > /dev/null 2>&1; then
    echo "  Postgres ready."
    break
  fi
  [ "${i}" -eq 24 ] && { echo "ERROR: Postgres not ready after 2 minutes." >&2; exit 1; }
  echo "  ... not ready (${i}/24)"; sleep 5
done

# ── Detect initialization state ───────────────────────────────────────────────
# Check 1: Does them.schema_migrations table exist?
MIGRATIONS_TABLE_EXISTS=$(_psql_query \
  "SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema='them' AND table_name='schema_migrations';")

# Check 2: Does them.schema_migrations have any rows?
MIGRATIONS_COUNT=""
if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ]; then
  MIGRATIONS_COUNT=$(_psql_query "SELECT COUNT(*) FROM them.schema_migrations;")
fi

# Decision tree:
#   MIGRATIONS_TABLE_EXISTS=0 → definitely fresh → bootstrap
#   MIGRATIONS_TABLE_EXISTS=1 and MIGRATIONS_COUNT>0 → initialized → no-op
#   MIGRATIONS_TABLE_EXISTS=1 and MIGRATIONS_COUNT=0 → partial/failed → error or force
if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ] && [ "${MIGRATIONS_COUNT:-0}" -gt 0 ]; then
  echo "==> [db-init] Schema already initialized (${MIGRATIONS_COUNT} migrations recorded)."
  echo "    To apply new migrations, use: ./scripts/linux-db-upgrade.sh db/026_name.sql"
  exit 0
fi

if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ] && [ "${MIGRATIONS_COUNT:-0}" -eq 0 ]; then
  if [ "${OPT_FORCE_FRESH}" = "false" ]; then
    echo "ERROR: them.schema_migrations exists but is empty." >&2
    echo "  This indicates a partial or failed previous initialization." >&2
    echo "  Investigate the database state before continuing." >&2
    echo "  If you are certain this is a fresh start, re-run with --force-fresh." >&2
    exit 1
  fi
  echo "  WARNING: --force-fresh: proceeding despite empty schema_migrations."
fi

echo "==> [db-init] Fresh database — bootstrapping schema..."

# ── Acquire advisory lock (prevent concurrent bootstrap) ──────────────────────
LOCK_ACQUIRED=$(_psql_query "SELECT pg_try_advisory_lock(987654321);")
if [ "${LOCK_ACQUIRED}" != "t" ]; then
  echo "ERROR: Could not acquire advisory lock (another process is bootstrapping)." >&2
  echo "  If no other process is running, connect and run: SELECT pg_advisory_unlock(987654321);" >&2
  exit 1
fi
echo "  Advisory lock acquired (987654321)."

# Ensure lock is released even on failure
_release_lock() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -tAc \
    "SELECT pg_advisory_unlock(987654321);" > /dev/null 2>&1 || true
}
trap _release_lock EXIT

# ── Check schema_current.sql exists ───────────────────────────────────────────
SCHEMA_SNAPSHOT="${PROJECT_DIR}/db/schema_current.sql"
if [ ! -f "${SCHEMA_SNAPSHOT}" ]; then
  echo "ERROR: ${SCHEMA_SNAPSHOT} not found." >&2
  echo "  This file is the canonical schema snapshot for fresh installations." >&2
  exit 1
fi

# ── Apply canonical schema snapshot ───────────────────────────────────────────
_psql_file "${SCHEMA_SNAPSHOT}"

# ── Verify migration tracking was applied ─────────────────────────────────────
VERSION_COUNT=$(_psql_query "SELECT COUNT(*) FROM them.schema_migrations;" || echo "0")
if [ "${VERSION_COUNT:-0}" -eq 0 ]; then
  echo "ERROR: schema_current.sql applied but them.schema_migrations is still empty." >&2
  echo "  The snapshot may be incomplete. Check db/schema_current.sql." >&2
  exit 1
fi
echo "  Schema initialized. Migration tracking: ${VERSION_COUNT} versions recorded."

# ── Optional: seed user accounts (dev/staging only) ───────────────────────────
if [ "${OPT_SEED_USERS}" = "true" ]; then
  SEED_USERS="${PROJECT_DIR}/db/seed_users.sql"
  if [ ! -f "${SEED_USERS}" ]; then
    echo "WARNING: --seed-users requested but ${SEED_USERS} not found. Skipping." >&2
  else
    echo "==> [db-init] Seeding dev user accounts..."
    _psql_file "${SEED_USERS}"
  fi
fi

# ── Optional: seed demo data ──────────────────────────────────────────────────
if [ "${OPT_SEED_DEMO}" = "true" ]; then
  SEED_DEMO="${PROJECT_DIR}/db/seed_demo.sql"
  if [ ! -f "${SEED_DEMO}" ]; then
    echo "WARNING: --seed-demo requested but ${SEED_DEMO} not found. Skipping." >&2
  else
    echo "==> [db-init] Seeding demo agents and orchestrators..."
    _psql_file "${SEED_DEMO}"
  fi
fi

# ── Flush Redis caches ────────────────────────────────────────────────────────
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
fi

echo "==> [db-init] Schema bootstrap complete."
