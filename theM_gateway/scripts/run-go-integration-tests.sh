#!/usr/bin/env bash
# run-go-integration-tests.sh — starts the integration compose overlay,
# waits for services to be healthy, then runs the Go integration tests.
#
# Prerequisites:
#   The base stack and all migrations must already have been applied once.
#   The temporal profile (--profile temporal) must be available in docker-compose.yml.
#
# Usage:
#   cd theM_gateway
#   ./scripts/run-go-integration-tests.sh
#
# Environment overrides (optional):
#   TEMPORAL_HOST_PORT   default: localhost:17233
#   TEST_POSTGRES_DSN    default: host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable
#   TEST_REDIS_ADDR      default: localhost:16379
#   GO_TEST_TIMEOUT      default: 120s

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
GO_DIR="$(cd "${GATEWAY_DIR}/../go" && pwd)"

TEMPORAL_HOST_PORT="${TEMPORAL_HOST_PORT:-localhost:17233}"
TEST_POSTGRES_DSN="${TEST_POSTGRES_DSN:-host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable}"
TEST_REDIS_ADDR="${TEST_REDIS_ADDR:-localhost:16379}"
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-120s}"

echo "==> Bringing up integration compose overlay..."
cd "${GATEWAY_DIR}"
docker compose \
  -f docker-compose.yml \
  -f docker-compose.integration.yml \
  --profile temporal \
  up -d

echo "==> Waiting 15s for services to become healthy..."
sleep 15

echo "==> Running Go integration tests in ${GO_DIR}..."
cd "${GO_DIR}"

TEMPORAL_HOST_PORT="${TEMPORAL_HOST_PORT}" \
TEST_POSTGRES_DSN="${TEST_POSTGRES_DSN}" \
TEST_REDIS_ADDR="${TEST_REDIS_ADDR}" \
  go test \
    -tags=integration \
    -v \
    -timeout "${GO_TEST_TIMEOUT}" \
    ./internal/temporal/...

echo "==> Integration tests complete."
