#!/usr/bin/env bash
# scripts/deploy.sh — path-independent deployment helper for the-M (Hetzner)
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
F_HETZNER="$ROOT_DIR/docker-compose.hetzner.yml"

PROJECT_NAME="them_gateway"

# ── Preflight checks ─────────────────────────────────────────────────────────
_preflight() {
    local missing=0
    for f in "$F_BASE" "$F_HETZNER"; do
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
        -f "$F_HETZNER" \
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

# Warn (or, with THE_M_REQUIRE_MIGRATIONS=true, stop) when db/*.sql migrations are not applied to the database:
# code that needs a missing column fails at runtime, often silently (backlog F-135, db/125).
_check_migrations() {
    local py
    py="$(command -v python3.12 || command -v python3 || true)"
    if [ -z "$py" ] || ! docker ps --format '{{.Names}}' | grep -qx them-postgres; then
        echo "migration check skipped (needs python3 and a running them-postgres)" >&2
        return 0
    fi
    if ! "$py" "$ROOT_DIR/scripts/check_migrations.py"; then
        echo "WARNING: database is behind the code (see MISSING above) — apply the migrations (docs/guides/database-migrations.md)" >&2
        [ "${THE_M_REQUIRE_MIGRATIONS:-false}" = "true" ] && exit 1
    fi
    return 0
}

cmd_up() {
    _preflight
    _check_migrations
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
