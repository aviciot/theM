#!/usr/bin/env bash
# linux-start.sh — Start the the-M stack on a Linux host (Go-first deployment).
#
# The Go gateway (them-go-bridge × 2) is the primary runtime:
#   /ws  /sse  /go-health  routing  sessions  gate  run-streaming
#
# Python components that still run alongside Go:
#   them-worker      — Temporal activity worker (orchestration logic, LLM calls)
#   them-auth-service — Authentication / JWT issuing (not yet rewritten in Go)
#   them-bridge      — Admin API (/api/v1/*) only; WS/SSE are Go-owned
#   them-frontend    — Next.js dashboard (not yet rewritten)
#
# Startup order:
#   1. Validate environment (.env, compose config)
#   2. Start Postgres + Redis (infrastructure)
#   3. Start Temporal (workflow runtime — Go worker needs it)
#   4. Initialize or verify the final DB schema (fresh: full bootstrap; existing: no-op)
#   5. Start auth-service (Go reads JWT keys issued here)
#   6. Start Python worker (Temporal activities)
#   7. Start both Go bridge replicas (primary gateway — WS, SSE, gate, sessions)
#   8. Start Traefik (routes /ws + /sse to Go; /api/v1 + /health to Python bridge)
#   9. Start Python bridge + frontend (admin API, UI)
#  10. Run health checks
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-start.sh [--build]
#
# Options:
#   --build   Force rebuild of all images before starting
#
# Prerequisites:
#   - Docker Engine >= 24, docker compose v2
#   - .env file present (copy .env.linux.example, fill in secrets, or run ./generate-env.sh)
#   - chmod +x scripts/linux-*.sh  (set once after clone)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_FLAG=""

for arg in "$@"; do
  case "$arg" in
    --build) BUILD_FLAG="--build" ;;
    --*)     echo "Unknown option: ${arg}"; exit 1 ;;
  esac
done

COMPOSE=(
  docker compose
  -f docker-compose.yml
  -f docker-compose.linux.yml
  -f docker-compose.integration.yml
  -f docker-compose.soak.yml
  -f docker-compose.traefik.yml
  --profile temporal
)

cd "${GATEWAY_DIR}"

# ── Step 1: Validate environment ───────────────────────────────────────────────
echo "==> [start] Validating environment..."

if [ ! -f .env ]; then
  echo "ERROR: .env not found."
  echo "  Copy .env.linux.example to .env, fill in required values, and re-run."
  echo "  Or generate secrets: ./generate-env.sh"
  exit 1
fi

# Source .env to check required vars (don't export — just validate)
_missing=()
while IFS='=' read -r key _val; do
  [[ "${key}" =~ ^[[:space:]]*# ]] && continue
  [[ -z "${key}" ]] && continue
  : # env vars are present in file — docker compose will read them
done < .env

for _var in THE_M_DB_PASSWORD THE_M_SECRET_KEY THE_M_JWT_SECRET ANTHROPIC_API_KEY; do
  _v="$(grep "^${_var}=" .env | cut -d= -f2- | tr -d '[:space:]')"
  if [ -z "${_v}" ] || [[ "${_v}" == CHANGE_ME* ]]; then
    _missing+=("${_var}")
  fi
done

if [ ${#_missing[@]} -gt 0 ]; then
  echo "ERROR: Required .env variables not set or still at placeholder:"
  for v in "${_missing[@]}"; do echo "  ${v}"; done
  exit 1
fi

echo "  Validating compose config..."
"${COMPOSE[@]}" config --quiet
echo "  Environment OK."

# ── Step 2: Infrastructure — Postgres + Redis ──────────────────────────────────
echo "==> [start] Starting Postgres and Redis..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-postgres them-redis

_wait_healthy() {
  local container="$1" timeout="${2:-60}" elapsed=0
  echo -n "  Waiting for ${container}..."
  until [ "$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null)" = "healthy" ]; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo ""
      echo "  ERROR: ${container} not healthy after ${timeout}s"
      echo "         Check logs: docker logs ${container} --tail 50"
      return 1
    fi
    sleep 5; elapsed=$((elapsed + 5)); echo -n "."
  done
  echo " healthy (${elapsed}s)"
}

_wait_healthy "them-postgres" 90
_wait_healthy "them-redis"    30

# ── Step 3: Temporal ──────────────────────────────────────────────────────────
echo "==> [start] Starting Temporal..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} temporal-frontend temporal-admin-tools temporal-ui

# Temporal healthcheck uses nc -z — wait for it to be healthy or just started
echo -n "  Waiting for temporal-frontend..."
for i in $(seq 1 18); do
  _s="$(docker inspect --format='{{.State.Health.Status}}' temporal-frontend 2>/dev/null || echo starting)"
  if [ "${_s}" = "healthy" ] || [ "${_s}" = "unhealthy" ]; then
    echo " ${_s}"
    break
  fi
  sleep 5; echo -n "."; [ "${i}" -eq 18 ] && echo " (timeout — continuing)"
done

# ── Step 4: Initialize or verify DB schema ────────────────────────────────────
echo "==> [start] Initializing DB schema (no-op if already present)..."
"${SCRIPT_DIR}/linux-db-init.sh"

# ── Step 5: Auth service (Go reads JWT keys from this) ────────────────────────
echo "==> [start] Starting auth service..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-auth-service
_wait_healthy "them-auth-service" 60

# ── Step 6: Python Temporal worker ────────────────────────────────────────────
# Runs Temporal activity implementations: LLM calls, tool routing, run recording.
# Not yet rewritten in Go. Required for any orchestration workflow.
echo "==> [start] Starting Python Temporal worker..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-worker

# Wait for the Temporal worker to be fully operational before starting Go bridges.
# Readiness criteria (all three must pass):
#   1. Container process is alive (State.Status == running)
#   2. Worker connected to Temporal — pollers present on the task queue
#      (verified via `temporal task-queue describe` in temporal-admin-tools)
#   3. Both workflow and activity pollers reported for them-orchestration
#
# Using the Temporal CLI via temporal-admin-tools is the authoritative check —
# it confirms the server-side view, not just that the container is running.
# Timeout: 120 seconds. Failure is fatal — Go bridges must not start without
# the worker, as orchestration calls would immediately stall.
echo "  Waiting for Temporal worker to register on task queue 'them-orchestration' (up to 120s)..."
_WORKER_TIMEOUT=120
_WORKER_ELAPSED=0
_WORKER_READY=false

while [ "${_WORKER_ELAPSED}" -lt "${_WORKER_TIMEOUT}" ]; do
  # Check 1: container is running
  _STATE="$(docker inspect --format='{{.State.Status}}' them-worker 2>/dev/null || echo absent)"
  if [ "${_STATE}" != "running" ]; then
    # Container may have exited — surface the error immediately
    _EXIT="$(docker inspect --format='{{.State.ExitCode}}' them-worker 2>/dev/null || echo unknown)"
    if [ "${_EXIT}" != "0" ] && [ "${_EXIT}" != "unknown" ]; then
      echo "" >&2
      echo "ERROR: them-worker exited with code ${_EXIT}." >&2
      echo "  Logs:" >&2
      docker logs them-worker --tail 30 2>&1 | sed 's/^/  /' >&2
      exit 1
    fi
    sleep 3; _WORKER_ELAPSED=$((_WORKER_ELAPSED + 3)); echo -n "."; continue
  fi

  # Check 2: Temporal task-queue has at least one workflow and one activity poller
  # `temporal task-queue describe` exits 0 and prints poller info when connected.
  if docker exec temporal-admin-tools \
       temporal task-queue describe \
         --task-queue them-orchestration \
         --namespace default \
       2>/dev/null | grep -q "Poller\|poller\|WorkflowTaskPoller\|ActivityTaskPoller\|worker"; then
    _WORKER_READY=true
    break
  fi

  sleep 3; _WORKER_ELAPSED=$((_WORKER_ELAPSED + 3))
  echo -n "."
done
echo ""

if [ "${_WORKER_READY}" != "true" ]; then
  echo "ERROR: Temporal worker not ready after ${_WORKER_TIMEOUT}s." >&2
  echo "  The worker must be polling the task queue before Go bridges can start." >&2
  echo ""
  echo "  Worker container status:"
  docker inspect --format='  Status: {{.State.Status}}  ExitCode: {{.State.ExitCode}}' \
    them-worker 2>/dev/null || echo "  (container not found)"
  echo ""
  echo "  Last 20 log lines from them-worker:"
  docker logs them-worker --tail 20 2>&1 | sed 's/^/  /'
  echo ""
  echo "  Task queue state (may be empty if worker never connected):"
  docker exec temporal-admin-tools \
    temporal task-queue describe --task-queue them-orchestration --namespace default \
    2>&1 | sed 's/^/  /' || true
  exit 1
fi

echo "  Temporal worker ready — task queue polling confirmed (${_WORKER_ELAPSED}s)."

# ── Step 7: Go bridge replicas (primary gateway) ──────────────────────────────
echo "==> [start] Starting Go bridge replicas (primary WS/SSE gateway)..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-go-bridge them-go-bridge-2
_wait_healthy "them-go-bridge"   90
_wait_healthy "them-go-bridge-2" 90

# ── Step 8: Traefik ───────────────────────────────────────────────────────────
# Routes: /ws → Go, /sse → Go, /go-health → Go, /api/v1 → Python bridge (started next)
echo "==> [start] Starting Traefik..."
# Retry once after 35s if port binding fails (TIME_WAIT from a recent stop can hold the port)
if ! "${COMPOSE[@]}" up -d them-traefik 2>&1; then
  echo "  Traefik port binding failed (likely OS TIME_WAIT from recent stop). Waiting 35s..."
  docker rm -f them-traefik 2>/dev/null || true
  sleep 35
  echo "  Retrying Traefik startup..."
  "${COMPOSE[@]}" up -d them-traefik
fi

# ── Step 9: Python bridge (admin API only) + frontend ─────────────────────────
# them-bridge serves /api/v1/admin/* and /health/* only on Linux.
# WS and SSE routes are owned by the Go bridges via Traefik priority labels.
echo "==> [start] Starting Python bridge (admin API) and frontend..."
"${COMPOSE[@]}" up -d ${BUILD_FLAG} them-bridge them-frontend
_wait_healthy "them-bridge" 60

# ── Step 10: Health verification ──────────────────────────────────────────────
echo ""
echo "==> [start] Stack status:"
"${COMPOSE[@]}" ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"

echo ""
HOST_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || echo localhost)"

echo "==> [start] Go gateway endpoints (primary):"
echo "    WS   → ws://${HOST_IP}:8088/ws/orchestrate/{app}/{ep}"
echo "    SSE  → http://${HOST_IP}:8088/sse/orchestrate/{app}/{ep}"
echo "    Health (via Traefik): http://${HOST_IP}:8088/go-health/live"
echo "    Go bridge 1 (direct): http://localhost:8002/health/ready"
echo "    Go bridge 2 (direct): http://localhost:8003/health/ready"
echo ""
echo "==> [start] Other endpoints:"
echo "    Admin API:      http://${HOST_IP}:8088/api/v1/"
echo "    Frontend:       http://${HOST_IP}:8088/"
echo "    Temporal UI:    http://${HOST_IP}:8088/temporal/"
echo "    Traefik dash:   http://${HOST_IP}:8089/"
echo ""
echo "==> [start] Done. Run full health check:"
echo "    ./scripts/linux-health.sh"
