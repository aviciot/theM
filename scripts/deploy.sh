#!/usr/bin/env bash
# scripts/deploy.sh — path-independent deployment helper for the-M
#
# Works from any directory. Derives ROOT_DIR from this script's location.
# Never contains secret values. Fails clearly if required files are missing.
#
# Usage:
#   ./scripts/deploy.sh config               # render full Compose config (no containers touched)
#   ./scripts/deploy.sh build                # build all images without starting containers
#   ./scripts/deploy.sh up                   # start or adopt the production stack
#   ./scripts/deploy.sh status               # show all them_gateway container states
#   ./scripts/deploy.sh logs [service]       # tail logs (all services, or one)
#   ./scripts/deploy.sh restart <service>    # restart a single service (no rebuild)
#
# Never runs: docker compose down --volumes

set -euo pipefail

# ── Derive root from script location ────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── Required files ───────────────────────────────────────────────────────────
ENV_FILE="$ROOT_DIR/.env"
SECRETS_FILE="$ROOT_DIR/secrets.local"

# ── Compose file chain ───────────────────────────────────────────────────────
F_BASE="$ROOT_DIR/docker-compose.yml"
F_LINUX="$ROOT_DIR/docker-compose.linux.yml"
F_INTEGRATION="$ROOT_DIR/docker-compose.integration.yml"
F_SOAK="$ROOT_DIR/docker-compose.soak.yml"
F_TRAEFIK="$ROOT_DIR/docker-compose.traefik.yml"
F_CLOUDFLARE="$ROOT_DIR/docker-compose.cloudflare.yml"

PROJECT_NAME="them_gateway"

# ── Preflight checks ─────────────────────────────────────────────────────────
_preflight() {
    local missing=0
    for f in "$F_BASE" "$F_LINUX" "$F_INTEGRATION" "$F_SOAK" "$F_TRAEFIK" "$F_CLOUDFLARE"; do
        if [ ! -f "$f" ]; then
            echo "ERROR: required Compose file missing: $f" >&2
            missing=1
        fi
    done
    if [ ! -f "$ENV_FILE" ]; then
        echo "ERROR: .env not found at $ENV_FILE" >&2
        echo "       Run: $ROOT_DIR/generate-env.sh" >&2
        missing=1
    fi
    if [ ! -f "$SECRETS_FILE" ]; then
        echo "ERROR: secrets.local not found at $SECRETS_FILE" >&2
        echo "       Copy from secrets.local.example and run generate-env.sh" >&2
        missing=1
    fi
    if [ "$missing" -ne 0 ]; then
        exit 1
    fi
}

# ── Base compose invocation ───────────────────────────────────────────────────
# --project-directory sets the base for all relative bind-mount paths and
# build contexts in the Compose files, making invocation path-independent.
_compose() {
    docker compose \
        --project-name "$PROJECT_NAME" \
        --project-directory "$ROOT_DIR" \
        --env-file "$ENV_FILE" \
        -f "$F_BASE" \
        -f "$F_LINUX" \
        -f "$F_INTEGRATION" \
        -f "$F_SOAK" \
        -f "$F_TRAEFIK" \
        -f "$F_CLOUDFLARE" \
        "$@"
}

# ── Commands ──────────────────────────────────────────────────────────────────
cmd_config() {
    _preflight
    _compose --profile temporal config
}

cmd_build() {
    _preflight
    _compose --profile temporal build
}

cmd_up() {
    _preflight
    _compose --profile temporal up -d --no-recreate
}

cmd_status() {
    docker compose \
        --project-name "$PROJECT_NAME" \
        --project-directory "$ROOT_DIR" \
        --env-file "$ENV_FILE" \
        -f "$F_BASE" \
        ps --all --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null \
    || docker ps --filter "label=com.docker.compose.project=$PROJECT_NAME" \
        --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
}

cmd_logs() {
    local service="${1:-}"
    if [ -n "$service" ]; then
        docker logs -f "$service"
    else
        _compose --profile temporal logs -f --tail 50
    fi
}

cmd_restart() {
    local service="${1:-}"
    if [ -z "$service" ]; then
        echo "Usage: $0 restart <service>" >&2
        echo "Example: $0 restart them-go-bridge" >&2
        exit 1
    fi
    _preflight
    _compose --profile temporal restart "$service"
}

# ── Dispatch ──────────────────────────────────────────────────────────────────
COMMAND="${1:-help}"
shift || true

case "$COMMAND" in
    config)   cmd_config ;;
    build)    cmd_build ;;
    up)       cmd_up ;;
    status)   cmd_status ;;
    logs)     cmd_logs "${1:-}" ;;
    restart)  cmd_restart "${1:-}" ;;
    help|--help|-h)
        cat <<'USAGE'
Usage: deploy.sh <command> [args]

Commands:
  config              Render full Compose config (dry run, no containers touched)
  build               Build all images without starting containers
  up                  Start or adopt the production stack (--no-recreate)
  status              Show all them_gateway container states
  logs [service]      Tail logs for all services or a single named service
  restart <service>   Restart a single running service (no rebuild)

This script never runs:  docker compose down --volumes
USAGE
        ;;
    *)
        echo "Unknown command: $COMMAND" >&2
        echo "Run '$0 help' for usage." >&2
        exit 1
        ;;
esac
