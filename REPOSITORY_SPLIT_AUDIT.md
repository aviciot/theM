# Repository Split Audit
**Date:** 2026-08-01
**Auditor:** Claude Code (inspection only — no files modified, no containers restarted)

---

## 1. What Each Directory Is

### `/home/avi/them` (Directory A)
- The Git working tree root (`git rev-parse --show-toplevel` → `/home/avi/them`)
- Branch: `main`, HEAD: `c909c55` (feat(go-worker): add Go Temporal Worker deployment)
- Remote: `origin` → `github.com/aviciot/theM`
- **No `.env` file, no `secrets.local`**
- 2 Compose files: `docker-compose.yml`, `docker-compose.local.yml`
- This is where all recent Claude Code sessions have been writing code

### `/home/avi/them/theM_gateway` (Directory B)
- **Not a separate Git repository** — it has no `.git` directory or gitfile
- `git rev-parse --show-toplevel` from inside it also returns `/home/avi/them`
- It is a **subdirectory tracked by Directory A** (appears in `git ls-files`)
- Branch: `main`, HEAD: `c909c55` — **identical HEAD** to Directory A
- It IS a checked-out copy of a subset of the same repository
- Has its own `.env` (with secrets) and `secrets.local` — both gitignored
- Has 7 Compose files: `docker-compose.yml`, `docker-compose.linux.yml`, `docker-compose.integration.yml`, `docker-compose.soak.yml`, `docker-compose.traefik.yml`, `docker-compose.hetzner-build.yml`, `docker-compose.cloudflare.yml`
- Has its own `CLAUDE.md` (different hash from root `CLAUDE.md`)
- **This is where the running stack was launched from**

---

## 2. Commits Present in Each Directory

Both directories share the same Git object store (same `.git` at `/home/avi/them/.git`).  
**All commits exist in both** — there is no commit divergence.

| Commit | Message | In A | In B |
|---|---|---|---|
| `c909c55` | feat(go-worker): add Go Temporal Worker deployment | YES | YES |
| `a1251e8` | docs(r4c1): update handover with final HEAD 09c5665 | YES | YES |
| `0b60de6` | docs(r4b): update handover and status with final HEAD a95e859 | YES | YES |
| `0f30aa5` | docs(r4a): update handover and status with final HEAD 0056d95 | YES | YES |

There are **no commits unique to either directory** — they are the same repository.

---

## 3. Which Directory Contains the Latest Code

**Both directories share the same HEAD (`c909c55`) and the same Git history.**

However, the file contents differ between the two paths for shared filenames (e.g. `CLAUDE.md`, `docker-compose.yml`) because `theM_gateway/` contains its own versions of those files tracked under the `theM_gateway/` path in the repository — they are not the same file as the root-level equivalents.

| File | Root (`/home/avi/them/`) | `theM_gateway/` subdir | Match? |
|---|---|---|---|
| `CLAUDE.md` | `f492984b7116` | `2e146e0ae291` | DIFFERENT |
| `docker-compose.yml` | `279a663e2a41` | `afe544b24e01` | DIFFERENT |
| `go/CLAUDE.md` | `645d0d76b72c` | absent | B-missing |
| `go/cmd/worker/main.go` | `e7b2d2f2d7e8` | absent | B-missing |
| `Dockerfile.go` | `cc74774292db` | absent | B-missing |
| `Dockerfile.go.worker` | `4a768b28d3b6` | absent | B-missing |
| `docker-compose.local.yml` | `47a2764a5cfe` | absent | B-missing |
| `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` | `c9e117eb86d3` | absent | B-missing |
| `docs/architecture-v2/R4A_IMPLEMENTATION_REPORT.md` | `045937c9c201` | absent | B-missing |
| `docs/architecture-v2/R4B_IMPLEMENTATION_REPORT.md` | `e032afafd726` | absent | B-missing |
| `docs/architecture-v2/R4C1_IMPLEMENTATION_REPORT.md` | `01719251aded` | absent | B-missing |
| `db/026_tenant_foundation.sql` | `b40624830cc3` | absent | B-missing |

**Conclusion:** The root of Directory A (`/home/avi/them`) contains all the latest application code — Go source, migrations, docs, Dockerfiles. The `theM_gateway/` subdirectory is a deployment-oriented subtree with its own `CLAUDE.md` and Compose files but does not contain `go/`, `db/` migrations beyond what was there when it was set up, or the recent R-4 series work.

---

## 4. Which Directory Currently Runs the Stack

**The running stack was launched from `/home/avi/them/theM_gateway`** (Compose project: `them_gateway`).

All production containers report:
```
com.docker.compose.project           = them_gateway
com.docker.compose.project.working_dir = /home/avi/them/theM_gateway
```

Compose files used by the running stack:
1. `docker-compose.yml`
2. `docker-compose.linux.yml`
3. `docker-compose.integration.yml`
4. `docker-compose.soak.yml`
5. `docker-compose.traefik.yml`
6. `docker-compose.hetzner-build.yml`
7. `docker-compose.cloudflare.yml`

**Exception — Go Workers:** `them-go-worker` and `them-go-worker-2` report `project=them` with **no working_dir label**. These were started via `docker run` (not Compose) in the previous session, bypassing the Compose project entirely.

---

## 5. Changes Unique to One Directory

There is no commit divergence — same Git history in both. The differences are file-level:

**Files that exist only under the root (`/home/avi/them/`) and are absent from `theM_gateway/`:**
- `go/` — entire Go source tree (bridge + worker)
- `Dockerfile.go`, `Dockerfile.go.worker`
- `docker-compose.local.yml`
- `db/` — migration files (or at least the newer R-4 series)
- `docs/architecture-v2/` — R-4 implementation reports, handover docs
- `app/`, `auth_service/`, `agents/`, `frontend/` — may or may not exist in both

**Files that exist only under `theM_gateway/`:**
- `docker-compose.linux.yml`
- `docker-compose.integration.yml`
- `docker-compose.soak.yml`
- `docker-compose.traefik.yml`
- `docker-compose.hetzner-build.yml`
- `docker-compose.cloudflare.yml`
- `.env` (secrets — gitignored)
- `secrets.local` (gitignored)
- `theM_gateway/CLAUDE.md` (different content from root `CLAUDE.md`)

---

## 6. Whether Any Work Is at Risk of Being Lost

**No work is at risk of being lost from Git history.** Both directories share the same `.git` object store and the same HEAD (`c909c55`), which has been pushed to `origin/main`. All commits are safe.

**What IS at risk operationally:**

1. **The running stack does not use the latest Compose files from the root.** The `theM_gateway/docker-compose.yml` was built from a different (earlier) snapshot. If `docker-compose.local.yml` (root) contains worker definitions not present in `theM_gateway/`, those workers cannot be brought up by the Compose project managing the rest of the stack — they must be started separately.

2. **The Go Workers are orphaned from Compose.** `them-go-worker` and `them-go-worker-2` have no `working_dir` label and belong to project `them` — they were started ad-hoc, not through the `them_gateway` Compose project. If the stack is recreated from `theM_gateway/`, these workers will not be included.

3. **`theM_gateway/CLAUDE.md` may be diverged** from the root `CLAUDE.md` — different hash. If `theM_gateway/` is the directory used by operators, they may be reading outdated session guidance.

---

## 7. Recommended Canonical Directory

**`/home/avi/them` (the repository root) should be the canonical directory.**

Reasons:
- It is the actual Git working tree root
- It contains all Go source, migrations, Dockerfiles, and docs
- It is where all Claude Code sessions have been writing code
- `theM_gateway/` is a tracked subdirectory within it — not a peer

The `theM_gateway/` subdirectory appears to be a historical deployment artifact, possibly created when the server-side deployment path was first set up and the Compose files were placed in a subdirectory. Over time, work continued at the root while the deployment continued to launch from the subdirectory.

---

## 8. Safe Consolidation Plan (not executed)

The goal is to make the running stack use `docker-compose.yml` and `docker-compose.local.yml` from the repository root, and to retire `theM_gateway/` as the Compose working directory.

**Pre-conditions (verify before doing anything):**
1. Confirm `theM_gateway/CLAUDE.md` differences — determine which is authoritative
2. Confirm `theM_gateway/docker-compose.yml` differences vs root `docker-compose.yml` — check for any Hetzner/Linux-specific overrides that must be preserved
3. Confirm the `theM_gateway/` Compose files (`docker-compose.linux.yml`, `docker-compose.traefik.yml`, etc.) have been superseded by root files or still contain unique config

**Steps (do not execute — plan only):**

1. **Compare `theM_gateway/docker-compose.yml` vs root `docker-compose.yml`** — identify any overrides in the `theM_gateway/` version that must be merged into root before switching
2. **Compare `theM_gateway/CLAUDE.md` vs root `CLAUDE.md`** — merge any unique deployment guidance into root `CLAUDE.md`
3. **Copy `.env` and `secrets.local`** from `theM_gateway/` to the repository root (they are gitignored and safe to copy)
4. **Stop the running stack gracefully** via `docker compose -f theM_gateway/docker-compose.yml ... down` (with correct profile flags)
5. **Start the stack from the root** using `docker compose -f docker-compose.yml -f docker-compose.local.yml up -d` with the secrets now at root
6. **Verify all containers healthy** and confirm Compose labels point to `/home/avi/them`
7. **Remove `theM_gateway/` from tracked files** (or leave in place but document it as deprecated)
8. **Start Go Workers via Compose profile** (`--profile go-worker`) rather than ad-hoc `docker run`

**Risk:** The `theM_gateway/` Compose files may contain production-specific overrides (Traefik rules, Cloudflare, Linux bind mounts) that are not present in the root `docker-compose.yml`. Steps 1–2 must be done carefully before any stack restart.
