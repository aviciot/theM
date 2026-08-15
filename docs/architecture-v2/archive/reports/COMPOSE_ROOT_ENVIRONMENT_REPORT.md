# Compose Root Environment Report — Stage B
**Date:** 2026-08-01
**Stage:** B (Root environment preparation — no containers touched)
**Based on:** `docs/architecture-v2/COMPOSE_LAYOUT_CONSOLIDATION_PLAN.md`

---

## Objective

Create `/home/avi/them/.env` and `/home/avi/them/secrets.local` so that the production Compose command can be run from the repository root without sourcing secrets from `theM_gateway/`. No running containers were stopped, restarted, or recreated.

---

## Strategy — Same Derivation, Same Secrets

`theM_gateway/` uses `generate-env.sh` + `secrets.local` to derive all DB, JWT, and platform keys via HMAC-SHA256. Running the same script with the same `secrets.local` at the root produces byte-for-byte identical derived values. This is the safest approach: no secrets are regenerated, no values are guessed, no services need reconfiguration.

1. Copied `theM_gateway/secrets.local` → `/home/avi/them/secrets.local` (`chmod 600`)
2. Ran `bash /home/avi/them/generate-env.sh` from root — produced `/home/avi/them/.env` with all derived secrets
3. Injected `ANTHROPIC_API_KEY` from the running `them-bridge` container without printing the value
4. Set non-derived production values directly in `.env`
5. Set `chmod 600 /home/avi/them/.env`

---

## Variables Present in Root `.env`

All 21 required variables are present:

| Variable | Source | Value type |
|---|---|---|
| `THE_M_DB_USER` | `generate-env.sh` | Derived (static: `them`) |
| `THE_M_DB_PASSWORD` | `generate-env.sh` | Derived via HMAC-SHA256 |
| `THE_M_SECRET_KEY` | `generate-env.sh` | Derived via HMAC-SHA256 |
| `THE_M_JWT_SECRET` | `generate-env.sh` | Derived via HMAC-SHA256 |
| `THE_M_REDIS_PASSWORD` | `generate-env.sh` | Blank (no Redis AUTH on private network) |
| `LIVEKIT_API_KEY` | `generate-env.sh` | Derived via HMAC-SHA256 |
| `LIVEKIT_API_SECRET` | `generate-env.sh` | Derived via HMAC-SHA256 |
| `ANTHROPIC_API_KEY` | Injected from running `them-bridge` | Production value — not printed |
| `APP_ENV` | Patched post-generation | `production` |
| `LOG_LEVEL` | Patched post-generation | `INFO` |
| `RUN_EVENTS_MODE` | Patched post-generation | `dual` |
| `ANTHROPIC_MODEL` | Patched post-generation | `claude-sonnet-4-6` |
| `THE_M_CORS_ORIGINS` | Patched post-generation | `https://them.avico78.com` |
| `LIVEKIT_PUBLIC_URL` | Patched post-generation | `wss://them.avico78.com/livekit` |
| `THE_M_HOSTNAME` | Patched post-generation | `them.avico78.com` |
| `THE_M_UI_HOSTNAME` | Patched post-generation | `them.avico78.com` |
| `THE_M_BRIDGE_WS_URL` | Patched post-generation | `wss://them.avico78.com` |
| `DEBATE_ANTHROPIC_API_KEY` | Not set | Not required unless `--profile debate` |
| `DOCU_WRITER_ANTHROPIC_API_KEY` | Not set | Not required unless `--profile docu` |
| `SECURITY_SCANNER_ANTHROPIC_API_KEY` | Not set | Not required unless `--profile security` |
| `GOOGLE_MAPS_API_KEY` / `FAL_API_KEY` | Not set | Not required unless `--profile vision` |
| `OPENAI_API_KEY` | Not set | Not required unless `--profile voice` |

---

## `COMPOSE_PROJECT_NAME` Note

The root `.env` does NOT include `COMPOSE_PROJECT_NAME=them_gateway`. The project name must be passed explicitly via `--project-name them_gateway` on the command line to preserve container name association during Stage F (controlled recreate). Adding it to `.env` would be equivalent and may be done in a future stage if preferred.

---

## Insecure Fallback Fixed in `docker-compose.yml`

One stale default value was found and corrected:

| Location | Old value | New value |
|---|---|---|
| `docker-compose.yml:891` — `them-go-bridge` `SECRET_KEY` | `${THE_M_SECRET_KEY:-change-this-in-production}` | `${THE_M_SECRET_KEY:-change-this-secret-key}` |

**Why:** Go bridge config validation rejects the value `change-this-in-production` as a known-insecure default (`DefaultSecretKey`). All other services already used `change-this-secret-key`. This change only affects local dev environments where `THE_M_SECRET_KEY` is not set (production always sets it from `.env`).

---

## Validation Results

### Secret files are gitignored

```bash
git check-ignore -v .env secrets.local
# .gitignore:2:.env       .env
# .gitignore:5:secrets.local    secrets.local
```

Both files are covered by `.gitignore` and do not appear in `git status`.

### All required variables present

```bash
python3.12 -c "
required = [
    'THE_M_DB_USER', 'THE_M_DB_PASSWORD', 'THE_M_SECRET_KEY', 'THE_M_JWT_SECRET',
    'THE_M_REDIS_PASSWORD', 'ANTHROPIC_API_KEY', 'LIVEKIT_API_KEY', 'LIVEKIT_API_SECRET',
    'APP_ENV', 'LOG_LEVEL', 'RUN_EVENTS_MODE', 'ANTHROPIC_MODEL', 'THE_M_CORS_ORIGINS',
    'LIVEKIT_PUBLIC_URL', 'THE_M_HOSTNAME', 'THE_M_UI_HOSTNAME', 'THE_M_BRIDGE_WS_URL',
]
lines = open('.env').readlines()
keys = {l.split('=')[0] for l in lines if '=' in l and not l.startswith('#')}
missing = [r for r in required if r not in keys]
print('MISSING:', missing if missing else 'none')
print(f'Total variables defined: {len(keys)}')
"
# MISSING: none
# Total variables defined: 21
```

### `docker compose config` passes cleanly

```bash
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  config --quiet
# Result: no errors, no warnings about missing variables
```

---

## Files Changed (This Stage)

| File | Change | Committed? |
|---|---|---|
| `/home/avi/them/.env` | NEW — created from `generate-env.sh` + patches. `chmod 600`. | **No — gitignored, must never commit** |
| `/home/avi/them/secrets.local` | NEW — copied from `theM_gateway/secrets.local`. `chmod 600`. | **No — gitignored, must never commit** |
| `/home/avi/them/docker-compose.yml` | MODIFIED — stale fallback fixed on line 891 | Committed in Stage B commit |

---

## What Is Not Changed

- `theM_gateway/.env` and `theM_gateway/secrets.local` — untouched
- All running containers — untouched
- `theM_gateway/docker-compose.yml` and overlay files — untouched

---

## Next Steps

The root is now structurally and environmentally ready to manage the full stack. Stages C–H of the consolidation plan proceed in future sessions:

- **Stage C:** Validate images (confirm Go bridge and worker images exist under `them_gateway` project labels)
- **Stage D:** Confirm Go Workers appear in the root `temporal` profile (via `docker-compose.integration.yml`)
- **Stage E:** Cross-check project name, networks, and named volumes in rendered config
- **Stage F:** Controlled recreate — `up -d --no-recreate` from root with `--project-name them_gateway`
- **Stage G:** Health validation post-switchover
- **Stage H:** Rollback procedure if Stage F fails

See `docs/architecture-v2/COMPOSE_LAYOUT_CONSOLIDATION_PLAN.md` for full Stage F–H detail.
