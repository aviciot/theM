#!/usr/bin/env bash
# soak_setup_db.sh — Apply all DB migrations and seed data required for the soak.
#
# Run once after first docker compose up, or after wiping data/.
# Safe to re-run — all scripts use CREATE IF NOT EXISTS / ALTER ... IF NOT EXISTS.
#
# Usage:
#   cd theM_gateway
#   bash ../go/scripts/soak_setup_db.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/../../theM_gateway" && pwd)"

echo "==> [soak_setup_db] Applying DB schema and migrations..."
cd "${GATEWAY_DIR}"

# run_sql pipes a file directly into psql via stdin (docker exec -i).
# This avoids the docker cp + psql -f approach which mangles /tmp paths on
# Windows/Git Bash. docker exec -i <container> psql ... < file works correctly
# on Windows because Git Bash handles the stdin redirect natively.
run_sql() {
  local file="$1"
  local label="${2:-${file}}"
  echo "  Applying: ${label}"
  docker exec -i them-postgres psql -U them -d them -q 2>&1 < "${file}" \
    | grep -v "^$" | grep -v "^NOTICE" | grep -v "^CREATE" | grep -v "^ALTER" \
    | grep -v "^INSERT" | grep -v "^SET" || true
}

# Create auth_service schema (idempotent)
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;" -q

# Skip all migration files if them.runs already exists — the DB is already
# migrated. Always run the seed section below regardless.
RUNS_EXISTS=$(docker exec them-postgres psql -U them -d them -tAc \
  "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='them' AND table_name='runs');" 2>/dev/null || echo "f")

if [ "${RUNS_EXISTS}" = "t" ]; then
  echo "  [skip] them.runs exists — skipping migration files (DB already migrated)"
else
  echo "  [apply] them.runs not found — applying all migration files"
  run_sql "db/001_schema.sql" "001 core schema"
  run_sql "auth_service/SCHEMA.sql" "auth service schema"
  run_sql "db/002_seed.sql" "002 base seed data"
  run_sql "db/003_phase8.sql" "003 phase8"
  run_sql "db/004_phase9.sql" "004 phase9"
  run_sql "db/005_phase10.sql" "005 phase10"
  run_sql "db/006_phase11.sql" "006 phase11"
  run_sql "db/009_security_scan.sql" "009 security scan"
  run_sql "db/014_app_orchestrators.sql" "014 app orchestrators"
  run_sql "db/015_phase12_drop_deprecated.sql" "015 phase12 deprecations"
  run_sql "db/018_graph_compiler.sql" "018 graph compiler"
  run_sql "db/022_runtime_limits.sql" "022 runtime limits"
  run_sql "db/023_app_runtime.sql" "023 app runtime"
  run_sql "db/024_ep_queue.sql" "024 ep queue"
fi

echo ""
echo "==> [soak_setup_db] Seeding soak test orchestrator (max_iterations=0, no LLM needed)..."
docker exec -i them-postgres psql -U them -d them -q << 'EOSQL'
-- Soak test orchestrator: max_iterations=0 so runs complete immediately
-- without needing an Anthropic API key.
INSERT INTO them.orchestrators (
  name, description, model, max_iterations, temperature,
  system_prompt, enabled
) VALUES (
  'soak_test',
  'Soak test orchestrator — completes immediately at max_iterations=0',
  'claude-haiku-4-5-20251001',
  0,
  0.7,
  'You are a test assistant.',
  true
) ON CONFLICT (name) DO UPDATE SET
  max_iterations = 0,
  enabled = true;

-- Soak test application + entry points
INSERT INTO them.applications (name, slug, description, enabled)
VALUES ('Soak Test App', 'soak_app', 'Phase 11b soak validation app', true)
ON CONFLICT (slug) DO NOTHING;

-- Create a WS entry point for the soak app
INSERT INTO them.entry_points (
  application_id, name, slug, ep_type,
  orchestrator_name, enabled, max_concurrent_sessions
)
SELECT
  a.id, 'Soak WS EP', 'soak_ws', 'websocket',
  'soak_test', true, 10
FROM them.applications a
WHERE a.slug = 'soak_app'
ON CONFLICT (slug) DO UPDATE SET
  orchestrator_name = 'soak_test',
  enabled = true;

-- Create an SSE entry point for the soak app
INSERT INTO them.entry_points (
  application_id, name, slug, ep_type,
  orchestrator_name, enabled, max_concurrent_sessions
)
SELECT
  a.id, 'Soak SSE EP', 'soak_sse', 'sse',
  'soak_test', true, 10
FROM them.applications a
WHERE a.slug = 'soak_app'
ON CONFLICT (slug) DO UPDATE SET
  orchestrator_name = 'soak_test',
  enabled = true;

-- Seed a bearer token for soak test calls
-- Token value: "soak-test-token-phase11b" (hash stored; value used in scripts)
INSERT INTO them.access_tokens (
  token_hash, description, user_id, enabled
) VALUES (
  encode(sha256('soak-test-token-phase11b'::bytea), 'hex'),
  'Phase 11b soak test token',
  1,
  true
) ON CONFLICT (token_hash) DO NOTHING;
EOSQL

echo ""
echo "==> [soak_setup_db] Bust Redis cache for new orchestrator/EP..."
docker exec them-redis redis-cli DEL \
  "them:orchestrators:soak_test" \
  "them:agents:registry" \
  > /dev/null

echo ""
echo "==> [soak_setup_db] Done. DB is ready for soak."
echo "    Soak app:   soak_app"
echo "    WS EP:      soak_ws"
echo "    SSE EP:     soak_sse"
echo "    Test token: soak-test-token-phase11b"
