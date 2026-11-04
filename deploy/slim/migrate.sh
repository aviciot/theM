#!/bin/sh
# the-M Slim: set up / upgrade the database. Idempotent — safe on every start.
# Runs inside a postgres:16-alpine one-shot container (see docker-compose.yml, service `migrate`).
#   mounts:  /baseline  = deploy/slim/db   (100_schema.sql, 200_seed.sql, BASELINE_VERSION)
#            /migrations = db/             (db/NNN_*.sql newer than the baseline)
#   env:     PGHOST PGUSER PGPASSWORD PGDATABASE   superuser connection
#            APP_DB_PASSWORD ADMIN_DB_PASSWORD     passwords for roles them_app / them_admin
#            ADMIN_PASSWORD [ADMIN_USERNAME] [ADMIN_EMAIL]   first admin (only used when none exists)
set -eu

PSQL="psql -v ON_ERROR_STOP=1 -q -At"
: "${PGHOST:?}" "${PGUSER:?}" "${PGPASSWORD:?}" "${PGDATABASE:?}"
: "${APP_DB_PASSWORD:?}" "${ADMIN_DB_PASSWORD:?}"

echo "migrate: waiting for postgres"
i=0; until pg_isready -q; do i=$((i+1)); [ $i -gt 60 ] && { echo "postgres not ready"; exit 1; }; sleep 1; done

echo "migrate: roles"
$PSQL -v app_pw="$APP_DB_PASSWORD" -v admin_pw="$ADMIN_DB_PASSWORD" <<'SQL'
SELECT 'CREATE ROLE them_owner NOLOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='them_owner') \gexec
SELECT 'CREATE ROLE them_app LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='them_app') \gexec
SELECT 'CREATE ROLE them_admin LOGIN BYPASSRLS' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='them_admin') \gexec
SELECT format('ALTER ROLE them_app PASSWORD %L', :'app_pw') \gexec
SELECT format('ALTER ROLE them_admin PASSWORD %L', :'admin_pw') \gexec
SQL

if [ "$($PSQL -c "SELECT to_regclass('them.schema_migrations') IS NOT NULL")" = "f" ]; then
  echo "migrate: empty database -> applying baseline $(cat /baseline/BASELINE_VERSION)"
  $PSQL -1 -f /baseline/100_schema.sql >/dev/null
  $PSQL -1 -f /baseline/200_seed.sql >/dev/null
else
  echo "migrate: baseline already applied"
fi

BASE_NUM=$(sed 's/_.*//' /baseline/BASELINE_VERSION | sed 's/^0*//')
for f in /migrations/[0-9][0-9][0-9]_*.sql; do
  [ -e "$f" ] || continue
  v=$(basename "$f" .sql)
  n=$(echo "$v" | sed 's/_.*//' | sed 's/^0*//')
  [ "$n" -le "$BASE_NUM" ] && continue
  if [ "$($PSQL -c "SELECT count(*) FROM them.schema_migrations WHERE version='$v'")" = "0" ]; then
    echo "migrate: applying $v"
    $PSQL -1 -f "$f" >/dev/null
    # every migration from 127 records itself; record older-style ones so they are not re-run
    $PSQL -c "INSERT INTO them.schema_migrations(version, description) VALUES ('$v','applied by migrate.sh') ON CONFLICT DO NOTHING"
  fi
done

if [ -n "${ADMIN_PASSWORD:-}" ]; then
  if [ "$($PSQL -c "SELECT count(*) FROM auth_service.users u JOIN auth_service.roles r ON r.id=u.role_id WHERE r.name='super_admin'")" = "0" ]; then
    echo "migrate: creating first admin '${ADMIN_USERNAME:-admin}'"
    $PSQL -c "CREATE EXTENSION IF NOT EXISTS pgcrypto"
    $PSQL -v u="${ADMIN_USERNAME:-admin}" -v e="${ADMIN_EMAIL:-admin@localhost}" -v p="$ADMIN_PASSWORD" <<'SQL'
WITH u AS (
  INSERT INTO auth_service.users (username, name, email, role_id, password_hash, active)
  SELECT :'u', 'Administrator', :'e', r.id, crypt(:'p', gen_salt('bf', 12)), true
  FROM auth_service.roles r WHERE r.name = 'super_admin'
  RETURNING id)
INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
SELECT u.id, t.id, 'admin' FROM u, them.tenants t WHERE t.is_bootstrap;
SQL
  else
    echo "migrate: an admin already exists, not creating one"
  fi
fi
echo "migrate: done (newest recorded: $($PSQL -c 'SELECT max(version) FROM them.schema_migrations'))"
