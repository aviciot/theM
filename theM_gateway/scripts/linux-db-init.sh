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
# Advisory lock:
#   ALL operations (detect, lock, apply, verify) run within a single psql
#   session. PostgreSQL advisory locks are session-scoped; opening a new
#   connection for the schema apply would silently release the lock acquired
#   by a previous connection.
#
# Seed data:
#   Does NOT insert demo agents, orchestrators, or user accounts automatically.
#   Use --seed-users to also apply db/seed_users.sql (dev/staging only).
#   Use --seed-demo to also apply db/seed_demo.sql (optional demo data).
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

# ── Helper: run SQL query in a dedicated short-lived psql session ──────────────
# Only for pre-lock detection queries (separate sessions are fine before we hold the lock).
_psql_query() {
  docker exec "${POSTGRES_CONTAINER}" \
    psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -tAc "$1" 2>/dev/null || echo ""
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

# ── Detect initialization state (pre-lock, separate session is fine) ──────────
MIGRATIONS_TABLE_EXISTS=$(_psql_query \
  "SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema='them' AND table_name='schema_migrations';")

MIGRATIONS_COUNT="0"
if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ]; then
  MIGRATIONS_COUNT=$(_psql_query "SELECT COUNT(*) FROM them.schema_migrations;")
fi

# Decision tree:
#   MIGRATIONS_TABLE_EXISTS=0 → definitely fresh → bootstrap
#   MIGRATIONS_TABLE_EXISTS=1 and MIGRATIONS_COUNT>0 → initialized → no-op
#   MIGRATIONS_TABLE_EXISTS=1 and MIGRATIONS_COUNT=0 → partial/failed → error or force
if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ] && [ "${MIGRATIONS_COUNT}" -gt 0 ] 2>/dev/null; then
  echo "==> [db-init] Schema already initialized (${MIGRATIONS_COUNT} migrations recorded)."
  echo "    To apply new migrations, use: ./scripts/linux-db-upgrade.sh db/026_name.sql"
  exit 0
fi

if [ "${MIGRATIONS_TABLE_EXISTS}" = "1" ] && [ "${MIGRATIONS_COUNT}" -eq 0 ] 2>/dev/null; then
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

# ── Check schema_current.sql exists ───────────────────────────────────────────
SCHEMA_SNAPSHOT="${PROJECT_DIR}/db/schema_current.sql"
if [ ! -f "${SCHEMA_SNAPSHOT}" ]; then
  echo "ERROR: ${SCHEMA_SNAPSHOT} not found." >&2
  exit 1
fi

# ── Single-session bootstrap: lock + apply + verify within one psql session ───
#
# IMPORTANT: pg_try_advisory_lock() is SESSION-SCOPED. If we acquire it in one
# psql invocation and apply the schema in another, the lock is released the
# moment the first session ends. We therefore run the entire bootstrap — detect,
# lock, apply schema_current.sql, verify, (seed) — inside a single persistent
# psql session using \i to source the snapshot file.
#
# The session script:
#   1. Tries to acquire the advisory lock; RAISEs if it fails
#   2. Checks again inside the session (double-check after acquiring lock)
#   3. Sources schema_current.sql via \i (runs inside the same connection)
#   4. Verifies schema_migrations was populated
#   5. Emits a sentinel line so we can detect success from the shell
#   6. Lock is automatically released when the session ends (EXIT)

echo "  Applying schema_current.sql within a single psql session (advisory lock held throughout)..."

# Build the session script as a temp heredoc streamed via stdin.
# \i cannot take a host path — we stream schema_current.sql inline after the
# session preamble by concatenating the files for docker exec -i.

SESSION_SCRIPT="$(cat <<'SESSION_EOF'
-- Acquire advisory lock (session-scoped — held until this psql session exits)
DO $$
BEGIN
  IF NOT pg_try_advisory_lock(987654321) THEN
    RAISE EXCEPTION 'advisory lock 987654321 already held — another bootstrap is in progress';
  END IF;
END;
$$;

-- Double-check inside the locked session (handles the TOCTOU window)
DO $$
DECLARE
  v_count INTEGER;
BEGIN
  SELECT COUNT(*) INTO v_count
  FROM information_schema.tables
  WHERE table_schema = 'them' AND table_name = 'schema_migrations';
  IF v_count = 1 THEN
    SELECT COUNT(*) INTO v_count FROM them.schema_migrations;
    IF v_count > 0 THEN
      RAISE NOTICE 'db-init:already-initialized:%', v_count;
    END IF;
  END IF;
END;
$$;

SESSION_EOF
)"

# Concatenate: session preamble + schema_current.sql + verification tail
VERIFY_TAIL="$(cat <<'VERIFY_EOF'
-- Verify schema_migrations was populated
DO $$
DECLARE v INTEGER;
BEGIN
  SELECT COUNT(*) INTO v FROM them.schema_migrations;
  IF v = 0 THEN
    RAISE EXCEPTION 'schema_current.sql applied but schema_migrations is empty — snapshot may be incomplete';
  END IF;
  RAISE NOTICE 'db-init:success:%', v;
END;
$$;
VERIFY_EOF
)"

# Stream all three parts into a single psql session
INIT_OUTPUT=$(
  { printf '%s\n' "${SESSION_SCRIPT}"; cat "${SCHEMA_SNAPSHOT}"; printf '\n%s\n' "${VERIFY_TAIL}"; } \
  | docker exec -i "${POSTGRES_CONTAINER}" \
      psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
      -v ON_ERROR_STOP=1 2>&1
)
INIT_EXIT=$?

if echo "${INIT_OUTPUT}" | grep -q "db-init:already-initialized:"; then
  RECORDED=$(echo "${INIT_OUTPUT}" | grep -o "db-init:already-initialized:[0-9]*" | cut -d: -f3)
  echo "==> [db-init] Schema already initialized (${RECORDED} migrations recorded, detected inside lock)."
  exit 0
fi

if [ "${INIT_EXIT}" -ne 0 ]; then
  echo "ERROR: Schema bootstrap failed." >&2
  echo "${INIT_OUTPUT}" | tail -20 | sed 's/^/  /' >&2
  exit 1
fi

# Confirm success marker
if ! echo "${INIT_OUTPUT}" | grep -q "db-init:success:"; then
  echo "ERROR: Bootstrap ran but success marker missing in psql output." >&2
  echo "${INIT_OUTPUT}" | tail -10 | sed 's/^/  /' >&2
  exit 1
fi

VERSION_COUNT=$(echo "${INIT_OUTPUT}" | grep -o "db-init:success:[0-9]*" | cut -d: -f3)
echo "  Schema initialized. Migration tracking: ${VERSION_COUNT} versions recorded."

# ── Optional: seed user accounts (dev/staging only) ───────────────────────────
if [ "${OPT_SEED_USERS}" = "true" ]; then
  SEED_USERS="${PROJECT_DIR}/db/seed_users.sql"
  if [ ! -f "${SEED_USERS}" ]; then
    echo "WARNING: --seed-users requested but ${SEED_USERS} not found. Skipping." >&2
  else
    echo "==> [db-init] Seeding dev user accounts..."
    docker exec -i "${POSTGRES_CONTAINER}" \
      psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
      < "${SEED_USERS}"
  fi
fi

# ── Optional: seed demo data ──────────────────────────────────────────────────
if [ "${OPT_SEED_DEMO}" = "true" ]; then
  SEED_DEMO="${PROJECT_DIR}/db/seed_demo.sql"
  if [ ! -f "${SEED_DEMO}" ]; then
    echo "WARNING: --seed-demo requested but ${SEED_DEMO} not found. Skipping." >&2
  else
    echo "==> [db-init] Seeding demo agents and orchestrators..."
    docker exec -i "${POSTGRES_CONTAINER}" \
      psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -q -v ON_ERROR_STOP=1 \
      < "${SEED_DEMO}"
  fi
fi

# ── Flush Redis caches ────────────────────────────────────────────────────────
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
    DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
  echo "  Redis cache flushed."
fi

echo "==> [db-init] Schema bootstrap complete."
