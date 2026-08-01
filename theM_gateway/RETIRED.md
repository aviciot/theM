# RETIRED — theM_gateway Deployment Layout

**Date retired:** 2026-08-01
**Retired by:** Claude Code (Compose consolidation, Stages C–H)
**Reason:** Production Compose deployment migrated to canonical repository root `/home/avi/them`

---

## This directory is no longer the active deployment directory.

The running stack is now managed from:
```
/home/avi/them
```

Production launch command (run from root):
```bash
cd /home/avi/them
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal up -d
```

---

## What is still here (do not delete)

- `secrets.local` — backup copy of master passphrase. Identical to `/home/avi/them/secrets.local`.
- `.env` — backup copy of derived environment. Identical to `/home/avi/them/.env`.
- `docker-compose.hetzner-build.yml` — the only file NOT copied to root (hetzner build context overrides are not needed at root since contexts are already `.`)
- Symlinked/copied source dirs (`app/`, `agents/`, `db/`, etc.) — these point into or are copies of root. Root is authoritative.

## What was migrated to root

- `docker-compose.linux.yml`
- `docker-compose.integration.yml`
- `docker-compose.soak.yml`
- `docker-compose.traefik.yml`
- `docker-compose.cloudflare.yml`
- `.env` and `secrets.local`

## Do not run docker compose from this directory

Running `docker compose up` from this directory would conflict with the `--project-name them_gateway` stack now managed from root.

## Reference

See: `docs/architecture-v2/COMPOSE_CONSOLIDATION_EXECUTION_REPORT.md`
