#!/usr/bin/env bash
# linux-start-hetzner.sh — Start the the-M stack on the Hetzner VPC server.
#
# Hetzner-specific additions on top of linux-start.sh:
#   - docker-compose.cloudflare.yml: joins them-traefik to the shared proxy-network
#     (infra-traefik's network) so the external Traefik can route them.avico78.com
#     to them-traefik:8088.
#
# Prerequisites (Hetzner-specific, already in place on this server):
#   - infra-traefik running on proxy-network
#   - infra-cloudflared running (Cloudflare Tunnel)
#   - /home/avi/infrastructure/traefik/dynamic/them-routes.yml present
#     (router commented out until UI is ready to expose — see INSTALL.md)
#   - .env present with them.avico78.com URLs and ANTHROPIC_API_KEY set
#
# Usage:
#   cd theM_gateway
#   ./scripts/linux-start-hetzner.sh [--build]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${GATEWAY_DIR}"

# Delegate to the generic linux-start.sh but inject the Hetzner compose overlay.
# We re-export COMPOSE_FILE so the generic script's COMPOSE array picks it up via
# the extra -f flag we pass through. Instead, we simply call docker compose directly
# with the full overlay list, mirroring linux-start.sh's startup sequence.

# Pass all arguments through to the generic script — it handles --build and validation.
# Then bring up them-traefik with the cloudflare overlay applied on top.

# Step 1: Run the generic start script (uses its own COMPOSE without cloudflare overlay)
"${SCRIPT_DIR}/linux-start.sh" "$@"

# Step 2: Re-apply them-traefik with the Hetzner cloudflare overlay so it joins proxy-network.
# The generic script already started them-traefik; this recreates it with the extra network.
echo "==> [hetzner] Applying Cloudflare overlay to them-traefik..."
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  up -d --no-build them-traefik

echo "==> [hetzner] them-traefik joined proxy-network."
echo "    External route:  them.avico78.com → them-traefik:8088 (via infra-traefik)"
echo "    UI exposed:      NO (them-routes.yml router is commented out)"
echo "    To expose UI:    see INSTALL.md — 'Expose to Cloudflare when ready'"
