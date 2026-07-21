#!/usr/bin/env bash
set -euo pipefail

echo "=== the-M DB Init ==="

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-them-redis}"
THE_M_DB_USER="${THE_M_DB_USER:-them}"
THE_M_DB_NAME="${THE_M_DB_NAME:-them}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Wait for Postgres to be ready
echo "Waiting for Postgres..."
for i in $(seq 1 20); do
  docker exec "$POSTGRES_CONTAINER" pg_isready -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" > /dev/null 2>&1 && break
  echo "  ... not ready yet ($i/20)"
  sleep 2
done

# Copy schema + seed files into container
docker cp "$PROJECT_DIR/db/001_schema.sql"       "$POSTGRES_CONTAINER:/tmp/them_001_schema.sql"
docker cp "$PROJECT_DIR/auth_service/SCHEMA.sql" "$POSTGRES_CONTAINER:/tmp/them_auth_schema.sql"
docker cp "$PROJECT_DIR/db/002_seed.sql"         "$POSTGRES_CONTAINER:/tmp/them_002_seed.sql"

echo "Creating auth_service schema..."
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -c "CREATE SCHEMA IF NOT EXISTS auth_service;"

echo "Applying them schema..."
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -f /tmp/them_001_schema.sql

echo "Applying auth_service schema..."
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -f /tmp/them_auth_schema.sql

echo "Applying seed data..."
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -f /tmp/them_002_seed.sql

# Flush Redis orchestrator + agent cache so the bridge picks up fresh DB IDs.
echo "Flushing Redis orchestrator/agent cache..."
if docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
  docker exec "$REDIS_CONTAINER" redis-cli -n 0 \
    DEL them:agents:registry \
    "them:orchestrators:default" \
    > /dev/null
  echo "  Redis cache flushed."
else
  echo "  Redis container '$REDIS_CONTAINER' not running — skipping cache flush."
fi

echo ""
echo "=== the-M DB Init complete ==="
echo ""
echo "Agents seeded:"
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -c "SELECT slug, display_name, enabled FROM them.agents ORDER BY slug;"
echo ""
echo "Orchestrators seeded:"
docker exec "$POSTGRES_CONTAINER" psql -U "$THE_M_DB_USER" -d "$THE_M_DB_NAME" \
  -c "SELECT name, display_name, llm_model, enabled FROM them.orchestrators ORDER BY name;"
