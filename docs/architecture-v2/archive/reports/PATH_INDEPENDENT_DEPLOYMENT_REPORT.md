# Path-Independent Deployment Report
**Date:** 2026-08-01
**Scope:** `scripts/deploy.sh` creation; stale comment removal; runbook path-independence

---

## Summary

Production deployment tooling is now path-independent. `scripts/deploy.sh` derives the repository root from its own file location and works from any caller directory. All bind mounts, build contexts, and env-file references resolve via `--project-directory` rather than the caller's CWD.

---

## Hardcoded Paths Found and Removed

### Compose file comments (functional: none; cosmetic: 3 files)

| File | Line | Was | Now |
|---|---|---|---|
| `docker-compose.integration.yml` | header comment | `cd theM_gateway` + legacy command | `./scripts/deploy.sh up` |
| `docker-compose.soak.yml` | header comment | `cd theM_gateway` + legacy command | `./scripts/deploy.sh up` |
| `docker-compose.traefik.yml` | header comment | `cd theM_gateway` + legacy command | `./scripts/deploy.sh up` |

No functional Compose directives (volumes, build contexts, env_file paths) contained hardcoded absolute paths. All bind mounts use `./` relative paths; all build contexts use `.`. These resolve correctly when `--project-directory` is set.

### Runbook (`LOCAL_TEST_ENVIRONMENT_RUNBOOK.md`)

All occurrences of `cd /home/avi/them` in commands replaced with:
- `scripts/deploy.sh <command>` for operational commands
- `"$(git rev-parse --show-toplevel)"` for path-independent shell variables
- `<repo-root>` for documentation references in prose/tables

The one remaining mention of `/home/avi/them` is factual context ("typically cloned to `/home/avi/them` on the production VPS") — not a command or a path assumption.

### Dockerfiles and traefik.yml

No hardcoded absolute paths found. Nothing to change.

### `generate-env.sh`

Already path-independent via `SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"`. No changes needed.

---

## `scripts/deploy.sh`

**Path:** `scripts/deploy.sh` (executable, `chmod +x`)

### How path-independence works

```bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
```

All Compose files are passed as absolute paths derived from `ROOT_DIR`. The `--project-directory "$ROOT_DIR"` flag anchors Docker's resolution of all relative paths in the Compose files (bind mounts, build contexts) to the repository root, regardless of caller CWD.

### Supported commands

| Command | Description |
|---|---|
| `config` | Render full Compose config (dry run, no containers touched) |
| `build` | Build all images without starting containers |
| `up` | Start or adopt the production stack (`--no-recreate`) |
| `status` | Show all `them_gateway` container states |
| `logs [service]` | Tail logs for all services, or a single named service |
| `restart <service>` | Restart a single running service (no rebuild) |

### Preflight checks

Before any command that touches Compose state (`config`, `build`, `up`, `restart`), the script verifies:
- All 6 Compose overlay files exist
- `.env` exists at `ROOT_DIR`
- `secrets.local` exists at `ROOT_DIR`

Missing files → clear error message with remediation hint → exit 1.

### Safety constraints encoded in the script

- Never runs `down --volumes`
- `up` always uses `--no-recreate` (never force-destroys running containers)
- `logs <service>` uses `docker logs -f <service>` directly (no Compose dependency)
- No secret values appear anywhere in the script

---

## Validation Results

`scripts/deploy.sh config` was invoked from three directories. All rendered identical output.

| Invocation directory | Exit code | `name:` | Service count | Warnings |
|---|---|---|---|---|
| `/home/avi` | 0 | `them_gateway` | 15 services + volumes + networks | version obsolete (cosmetic) |
| `/tmp` | 0 | `them_gateway` | 15 services + volumes + networks | version obsolete (cosmetic) |
| `/home/avi/them` | 0 | `them_gateway` | 15 services + volumes + networks | version obsolete (cosmetic) |

`diff` of all three rendered outputs: **identical** (0 differences).

Services rendered (all three directories):
```
temporal-admin-tools  temporal-frontend  temporal-ui
them-auth-service  them-bridge  them-frontend
them-go-bridge  them-go-bridge-2  them-go-worker  them-go-worker-2
them-postgres  them-redis  them-traefik  them-worker  vision-agent
```

---

## Remaining Path Dependencies

| Dependency | Location | Type | Removable? |
|---|---|---|---|
| `/home/avi/them` (informational only) | `LOCAL_TEST_ENVIRONMENT_RUNBOOK.md:13` | Documentation prose | No — factual context for current VPS |
| `com.docker.compose.project.working_dir` labels on 3 containers | `them-postgres`, `them-redis`, `temporal-frontend` | Docker runtime label (stale from pre-consolidation) | Yes — will update naturally at next planned maintenance recreate |
| `CLAUDE.md` general description | Various references to repo layout | Documentation | Not a deployment path dependency |

No Compose YAML, Dockerfile, or shell script contains a hardcoded absolute path that would prevent deployment from a different clone location.

---

## Files Changed

| File | Change |
|---|---|
| `scripts/deploy.sh` | **New** — path-independent deploy helper |
| `docker-compose.integration.yml` | Header comment: `cd theM_gateway` → `./scripts/deploy.sh up` |
| `docker-compose.soak.yml` | Header comment: `cd theM_gateway` → `./scripts/deploy.sh up` |
| `docker-compose.traefik.yml` | Header comment: `cd theM_gateway` → `./scripts/deploy.sh up` |
| `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` | All `cd /home/avi/them` commands replaced with path-independent equivalents |
| `docs/architecture-v2/PATH_INDEPENDENT_DEPLOYMENT_REPORT.md` | **New** — this report |
