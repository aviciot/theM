#!/bin/bash
set -e

echo "=== Odin DB Init ==="

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-omni-postgres}"
ODIN_DB_USER="${ODIN_DB_USER:-odin}"
ODIN_DB_PASSWORD="${ODIN_DB_PASSWORD:-change_me}"
ODIN_DB_NAME="${ODIN_DB_NAME:-odin}"

# Create role if not exists
docker exec "$POSTGRES_CONTAINER" psql -U omni -c \
  "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$ODIN_DB_USER') THEN CREATE ROLE $ODIN_DB_USER LOGIN PASSWORD '$ODIN_DB_PASSWORD'; END IF; END \$\$;"

# Create database if not exists
docker exec "$POSTGRES_CONTAINER" psql -U omni \
  -c "SELECT 'CREATE DATABASE $ODIN_DB_NAME OWNER $ODIN_DB_USER' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$ODIN_DB_NAME')" \
  | grep -q "CREATE DATABASE" && docker exec "$POSTGRES_CONTAINER" psql -U omni -c "CREATE DATABASE $ODIN_DB_NAME OWNER $ODIN_DB_USER;" || echo "Database $ODIN_DB_NAME already exists"

# Copy schema files
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

docker cp "$PROJECT_DIR/db/001_schema.sql" "$POSTGRES_CONTAINER:/tmp/odin_001_schema.sql"
docker cp "$PROJECT_DIR/auth_service/SCHEMA.sql" "$POSTGRES_CONTAINER:/tmp/odin_auth_schema.sql"
docker cp "$PROJECT_DIR/db/002_seed.sql" "$POSTGRES_CONTAINER:/tmp/odin_002_seed.sql"

# Apply schemas
echo "Applying odin schema..."
docker exec "$POSTGRES_CONTAINER" psql -U "$ODIN_DB_USER" -d "$ODIN_DB_NAME" -f /tmp/odin_001_schema.sql

echo "Applying auth_service schema..."
docker exec "$POSTGRES_CONTAINER" psql -U "$ODIN_DB_USER" -d "$ODIN_DB_NAME" -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec "$POSTGRES_CONTAINER" psql -U "$ODIN_DB_USER" -d "$ODIN_DB_NAME" -f /tmp/odin_auth_schema.sql

echo "Applying seed data..."
docker exec "$POSTGRES_CONTAINER" psql -U "$ODIN_DB_USER" -d "$ODIN_DB_NAME" -f /tmp/odin_002_seed.sql

echo "=== Odin DB Init complete ==="
