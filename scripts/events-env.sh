#!/bin/bash
# events-env.sh — append the observability-events secrets to .env WITHOUT touching
# any existing line. Derivation is identical to generate-env.sh (HMAC-SHA256 of
# secrets.local), so a later full ./generate-env.sh run produces the same values.
#
# Usage: scripts/events-env.sh
# Reads:  secrets.local  (never committed)
# Writes: .env           (append-only; never committed)
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRETS_FILE="$REPO/secrets.local"
ENV_FILE="$REPO/.env"

[ -f "$SECRETS_FILE" ] || { echo "ERROR: secrets.local not found (run ./generate-env.sh first)"; exit 1; }
[ -f "$ENV_FILE" ]     || { echo "ERROR: .env not found (run ./generate-env.sh first)"; exit 1; }

MASTER=$(grep "^THE_M_MASTER_SECRET=" "$SECRETS_FILE" | cut -d= -f2 | tr -d '[:space:]')
[ -n "$MASTER" ] || { echo "ERROR: THE_M_MASTER_SECRET not found in secrets.local"; exit 1; }

derive() { echo -n "the-m:$1" | openssl dgst -sha256 -hmac "$MASTER" | awk '{print $2}'; }

add_var() {  # add_var NAME VALUE — skip if NAME already present
  if grep -q "^$1=" "$ENV_FILE"; then
    echo "  $1 already set — left unchanged"
  else
    echo "$1=$2" >> "$ENV_FILE"
    echo "  $1 added"
  fi
}

if ! grep -q "^# ─── Observability events" "$ENV_FILE"; then
  printf '\n# ─── Observability events (docker-compose.events.yml) ───────────\n' >> "$ENV_FILE"
fi

add_var THE_M_EVENTS_CH_ADMIN_PASSWORD  "$(derive "events-ch-admin-password")"
add_var THE_M_EVENTS_CH_WRITER_PASSWORD "$(derive "events-ch-writer-password")"
add_var THE_M_EVENTS_CH_READER_PASSWORD "$(derive "events-ch-reader-password")"
add_var THE_M_EVENTS_REDIS_PASSWORD     "$(derive "events-redis-password")"

echo "Done. Values are not printed. Never commit .env."
