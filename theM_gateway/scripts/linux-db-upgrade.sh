#!/usr/bin/env bash
# linux-db-upgrade.sh — Apply new schema migrations to an existing the-M deployment.
#
# For UPGRADES ONLY — when new migration files have been added to db/ after the
# initial deployment. Does NOT replay all migrations, only what you specify.
#
# Advisory lock and atomicity:
#   Each migration runs inside a SINGLE psql session that:
#     1. Acquires pg_try_advisory_lock(987654321) — fails if another deploy is migrating
#     2. Checks them.schema_migrations to skip already-applied versions
#     3. Applies the migration SQL (must be transactional — wrap in BEGIN/COMMIT)
#     4. Records the version in them.schema_migrations (inside the same session)
#     5. Session ends — lock automatically released
#
#   The lock is SESSION-SCOPED in PostgreSQL. All steps for one migration share
#   one psql connection so the lock is never silently released mid-operation.
#
#   If the migration SQL fails, PostgreSQL rolls back the transaction and the
#   schema_migrations INSERT never occurs. The lock is released when the
#   session exits with an error.
#
# Version convention:
#   File: db/026_feature_name.sql → version = "026_feature_name" (basename minus .sql)
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-db-upgrade.sh db/026_new_feature.sql
#   ./scripts/linux-db-upgrade.sh db/026_one.sql db/027_two.sql
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

# ── Pre-flight: Postgres reachable + schema_migrations exists ──────────────────
echo "==> [db-upgrade] Verifying database state..."

docker exec "${POSTGRES_CONTAINER}" \
  pg_isready -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
  || { echo "ERROR: Postgres not reachable." >&2; exit 1; }

MIGRATIONS_TABLE=$(docker exec "${POSTGRES_CONTAINER}" \
  psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" -tAc \
  "SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema='them' AND table_name='schema_migrations';" 2>/dev/null || echo "0")

if [ "${MIGRATIONS_TABLE}" != "1" ]; then
  echo "ERROR: them.schema_migrations does not exist." >&2
  echo "  Run ./scripts/linux-start.sh first to initialize the schema." >&2
  exit 1
fi

echo "  Database state: OK (schema_migrations present)"

# ── Apply each migration file in a single session per file ───────────────────
APPLIED=0
SKIPPED=0
FAILED=0

for f in "$@"; do
  if [ ! -f "${f}" ]; then
    echo "ERROR: file not found: ${f}" >&2
    FAILED=$((FAILED + 1))
    continue
  fi

  VERSION="$(basename "${f}" .sql)"
  DESCRIPTION="$(head -5 "${f}" | grep -m1 '^\s*--' | sed 's/^--[[:space:]]*//' || echo "${VERSION}")"

  echo "==> [db-upgrade] Processing: ${VERSION}"

  # ── Single-session per migration: lock + check + apply + record ─────────────
  # The preamble acquires the advisory lock and checks if already applied.
  # Then we stream the migration file body.
  # Then the postamble records success.
  # All three parts run in the same psql session — lock held throughout.
  #
  # If the migration SQL has no BEGIN/COMMIT, the preamble wraps the whole
  # thing in a transaction via \set ON_ERROR_ROLLBACK on and explicit BEGIN.

  PREAMBLE="$(cat <<PREAMBLE_EOF
-- Acquire advisory lock (session-scoped — released when this session ends)
DO \$\$
BEGIN
  IF NOT pg_try_advisory_lock(987654321) THEN
    RAISE EXCEPTION 'upgrade-lock-held: advisory lock 987654321 already taken — another migration is in progress';
  END IF;
END;
\$\$;

-- Skip if already applied (check inside the locked session)
-- RAISE EXCEPTION aborts the session immediately so the migration SQL never runs.
-- The caller detects 'upgrade-skip:' in the error output and treats it as a successful skip.
DO \$\$
DECLARE v INTEGER;
BEGIN
  SELECT COUNT(*) INTO v FROM them.schema_migrations WHERE version = '${VERSION}';
  IF v > 0 THEN
    RAISE EXCEPTION 'upgrade-skip:${VERSION}';
  END IF;
END;
\$\$;

BEGIN;
PREAMBLE_EOF
)"

  POSTAMBLE="$(cat <<POSTAMBLE_EOF
INSERT INTO them.schema_migrations (version, description)
VALUES ('${VERSION}', '${DESCRIPTION//\'/\'\'}')
ON CONFLICT (version) DO NOTHING;

COMMIT;

DO \$\$ BEGIN RAISE NOTICE 'upgrade-success:${VERSION}'; END; \$\$;
POSTAMBLE_EOF
)"

  # Stream preamble + migration file + postamble into one psql session.
  # Use && / || to capture exit code without triggering set -e on non-zero exit.
  # RAISE EXCEPTION (used for skip detection) exits psql with a non-zero code;
  # we need to inspect the output before deciding whether it's a skip or an error.
  SESSION_OUTPUT=$(
    { printf '%s\n' "${PREAMBLE}"; cat "${f}"; printf '\n%s\n' "${POSTAMBLE}"; } \
    | docker exec -i "${POSTGRES_CONTAINER}" \
        psql -U "${THE_M_DB_USER}" -d "${THE_M_DB_NAME}" \
        -v ON_ERROR_STOP=1 2>&1
  ) && SESSION_EXIT=0 || SESSION_EXIT=$?

  # Check for skip signal (already applied)
  if echo "${SESSION_OUTPUT}" | grep -q "upgrade-skip:${VERSION}"; then
    echo "  Skipping: ${VERSION} (already recorded in schema_migrations)"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  if [ "${SESSION_EXIT}" -ne 0 ]; then
    echo "ERROR: Migration ${VERSION} failed." >&2
    echo "${SESSION_OUTPUT}" | tail -20 | sed 's/^/  /' >&2
    echo ""
    echo "  The transaction was rolled back. schema_migrations has NOT been updated." >&2
    echo "  Fix the issue in ${f} and re-run this script." >&2
    FAILED=$((FAILED + 1))
    exit 1
  fi

  if ! echo "${SESSION_OUTPUT}" | grep -q "upgrade-success:${VERSION}"; then
    echo "ERROR: Migration ${VERSION} ran but success marker missing." >&2
    echo "${SESSION_OUTPUT}" | tail -10 | sed 's/^/  /' >&2
    FAILED=$((FAILED + 1))
    exit 1
  fi

  echo "  Applied: ${VERSION} — recorded in schema_migrations."
  APPLIED=$((APPLIED + 1))
done

# ── Flush Redis caches so schema-linked caches are invalidated ─────────────────
if [ "${APPLIED}" -gt 0 ]; then
  if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
    docker exec "${REDIS_CONTAINER}" redis-cli -n 0 \
      DEL them:agents:registry them:orchestrators:default > /dev/null 2>&1 || true
    echo "  Redis cache flushed."
  fi
fi

echo "==> [db-upgrade] Done. Applied: ${APPLIED}, Skipped: ${SKIPPED}, Failed: ${FAILED}"
[ "${FAILED}" -gt 0 ] && exit 1 || exit 0
