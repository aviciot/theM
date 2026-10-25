---
title: Lessons — Docker, Compose & Traefik
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-10-05 — `docker restart` does NOT switch to a freshly built image

**Symptom:** rebuilt all 3 dag-worker images (`docker compose build them-dag-worker them-dag-worker-2 them-dag-worker-debug`), then ran `docker compose restart` — containers still ran the old image. New code was not active, same errors persisted.
**Root cause:** `docker compose restart` (and `docker restart`) restart the container with its **existing image layer** — it does not re-pull or re-apply the new build. Only `docker compose up -d` recreates containers from the freshly built image.
**Fix:** always use `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d <service>` after building. Verify with `docker inspect <container> --format '{{.Image}}'` and compare against `docker images | grep <service>` — the digests must match.
**Watch for:** after any `docker compose build`, confirm `docker inspect` digest matches image list digest before trusting the new binary is running. `docker restart` is only for restarting with the SAME image (e.g. picking up new env vars from a restarted compose service).

---

---

## 2026-07-03 — NEXT_PUBLIC_ env vars are baked at build time, not runtime

**Symptom:** Playground WS connected to `ws://localhost:8001` in the browser — connection refused because the bridge is not on localhost from the user's machine.
**Root cause:** `NEXT_PUBLIC_*` variables in Next.js are inlined at build time by webpack. Setting them in docker-compose `environment:` only affects the Next.js server process, not the browser bundle. The browser always got the hardcoded fallback `ws://localhost:8001`.
**Fix:** Derive the WS URL at runtime using `window.location.hostname` so the browser connects to the correct host. Expose bridge port 8001 on the host so the browser can reach it directly.
**Watch for:** Never rely on `NEXT_PUBLIC_*` for values that differ between dev/prod unless the image is rebuilt for each environment. Runtime config must come from `window.location` or an API call.

---

---

## 2026-07-03 — Redis cache survives DB wipe; stale FK causes silent run failure

**Symptom:** Playground ran successfully (LLM responded, trace showed events) but `odin.runs` table was empty. No errors in the UI.
**Root cause:** Postgres was recreated (DB wiped) but Redis survived. The orchestrator cache (`odin:orchestrators:default`) still held the old `orchestrator_id` UUID. `run_recorder.start_run()` inserted a Run with that UUID as FK — but the orchestrators table was empty, causing a FK violation. `start_run()` had no try/except, so it raised, was swallowed somewhere upstream, and the run proceeded without a DB record.
**Fix:** (1) Added try/except to `start_run()` so it logs the error clearly and returns a dummy UUID rather than crashing silently. (2) After any DB reset, recreate orchestrators via the UI — this writes a fresh DB row and updates Redis with the correct ID.
**Watch for:** After `docker compose down -v` or any DB volume wipe, always recreate all orchestrators and agents via the UI before testing. Redis TTLs mean stale cache can persist for up to 600s even after a DB reset.

---

---

## 2026-07-06 — Traefik v3.x fails on Docker 29 with "client version 1.24 is too old"

**Symptom:** Traefik v3.1/v3.3 Docker provider logged `Error 400: client version 1.24 is too old. Minimum supported API version is 1.44` and failed to discover any containers.
**Root cause:** Traefik's Go Docker client hard-codes `/v1.24/_ping` for initial API version negotiation. Docker 29 raised its minimum supported API version to 1.44 and rejects the v1.24 request. Setting `DOCKER_API_VERSION` env var on Traefik has no effect on this negotiation path.
**Fix:** Use `traefik:v3.6` (v3.6.1+). PR #12256 added `WithAPIVersionNegotiation()` to Traefik's Docker client — it now performs a proper version handshake instead of hardcoding v1.24.
**Watch for:** If Traefik is ever downgraded below v3.6 on Docker 29+, the Docker provider will silently fail to discover containers. `tecnativa/docker-socket-proxy` does NOT fix this — it doesn't rewrite the URL version path.

---

---

## 2026-07-06 — Traefik Docker provider silently skips unhealthy containers

**Symptom:** Traefik DEBUG logs showed `"Filtering unhealthy or starting container: them-frontend-..."`. The `them-ui` router never appeared in the Traefik API even though the labels were correct.
**Root cause:** Traefik's Docker provider skips containers that are not in `healthy` state (i.e., `starting` or `unhealthy`). The `them-frontend` healthcheck used `curl -f -L` but the Node.js Alpine container doesn't have `curl` — every check returned exit code -1 (executable not found), keeping the container perpetually `unhealthy`.
**Fix:** Changed healthcheck to `wget -q -O/dev/null http://localhost:3200/login || exit 1`. `wget` is available in Alpine busybox. Also: `docker compose restart` does NOT pick up a changed healthcheck — must use `docker compose up -d --force-recreate`.
**Watch for:** Never use `curl` in healthchecks for Node.js Alpine containers. Always check that the healthcheck binary exists in the target image before writing the check. A container that is `unhealthy` is invisible to Traefik.

---

---

## 2026-07-07 — Traefik silently stops routing when a container is unhealthy after restart

**Symptom:** `http://<host>:8088` appeared unreachable after a `docker compose up -d`. No Traefik errors, no obvious reason.

**Root cause:** Traefik's Docker provider drops any container from the routing table the moment it transitions to `unhealthy` or `starting`. A `docker compose up -d` that recreates containers puts them back in the `health: starting` window — during which Traefik refuses to route to them. If the healthcheck binary doesn't exist (e.g., `curl` missing on Alpine), the container stays `unhealthy` indefinitely.

**Fix:** `docker compose ps` immediately — look for any container showing `(unhealthy)` or `(health: starting)`. That is the thing Traefik is not routing to. Fix the healthcheck binary, recreate the container, wait for `(healthy)`.

**Watch for:** After any `docker compose up -d` that recreates `them-bridge` or `them-frontend`, give Traefik 10–20s to mark them healthy before assuming the URL is broken. Do not chase Traefik config or network issues until you've confirmed `docker compose ps` shows all containers `(healthy)`.

---

---

## 2026-07-07 — Multiple Traefik instances on one host — logs are not the same Traefik

**Symptom:** Investigating `them-traefik` unreachability, found Traefik container logs showing health check failures for `omni-bridge-svc` and other services we didn't recognize. Concluded Traefik was misconfigured.

**Root cause:** Multiple Traefik instances were running on the host — `them-traefik` (port 8088), `traefik-external` (8090/8091), `omni-traefik` (3000). All three share the Docker socket and pick up labels from all containers. The container whose logs we were reading was a *different* Traefik, not ours. Its errors were for Omni's containers, not the-M's.

**Fix:** Always confirm which Traefik instance you are looking at: `docker logs them-traefik` (not `traefik` or another alias). `docker compose ps` in `/opt/docker/odin` shows only our containers; the other Traefik instances belong to other stacks.

**Watch for:** On shared Docker hosts with multiple stacks, `docker ps` shows all containers from all projects. Always target by container name (`them-traefik`), never by image name alone.

---

---

## 2026-07-16 — omni-traefik ingested them-bridge labels → 503 on all A2A calls

**Symptom:** Test and Discover on any external A2A agent (e.g. `http://10.55.125.43:3000/a2a/codeagent/`) returned `503 - no available server`. The bridge could reach the host fine; the proxy on the other machine had the route but no healthy backend.

**Root cause:** `omni-traefik` (the reverse proxy on `10.55.125.43:3000`) had no `--providers.docker.constraints` set, so it ingested Traefik labels from **all** containers visible on the Docker socket — including `them-bridge` from this stack. `them-bridge` registers `/a2a/` at priority 100; OMNI's own A2A router was only priority 25. them-bridge won every route match and forwarded to its own (dead) backends → 503.

**Fix applied on OMNI side:**
- *Immediate:* raised OMNI's A2A router priority to 110 so it beats them-bridge even when both labels are visible.
- *Structural:* added `--providers.docker.constraints=!Label('traefik-instance','them')` to `omni-traefik` — it now ignores all `them-*` containers entirely.

**Watch for:** Any time two independent stacks share a Docker socket (same host), their Traefik instances will cross-contaminate unless both have `--providers.docker.constraints` set to filter out the other stack's labels. `them-traefik` already does this correctly — verify `omni-traefik` does too after any OMNI upgrade or redeploy.

**Corollary for this stack:** `them-traefik` is correctly isolated — verify by checking `traefik/traefik.yml` has a constraints filter that excludes OMNI labels. If `them-traefik` ever gets a socket mount without constraints, it will ingest OMNI's labels the same way.

---

---

## 2026-07-21 — Docker BuildKit cannot follow symlinks outside the build context

**Symptom:** `them-bridge` and `them-worker` Docker builds failed with `too many links` / `failed to read dockerfile`. theM_gateway/ contains symlinks to ../traefik, ../auth_service, etc.

**Root cause:** Docker BuildKit resolves the build context at the directory level and does NOT follow symlinks that point outside the build context root. A symlink `theM_gateway/traefik -> ../traefik` resolves to `/opt/docker/them/traefik` which is outside the `/opt/docker/them/theM_gateway/` build context.

**Fix:** Replace symlinks with actual copies: `cp -r ../traefik ./traefik`, `cp -r ../auth_service ./auth_service`. Use rsync for directories that need partial overrides: `rsync -av --ignore-existing ../app/ ./app/`.

**Watch for:** Never use symlinks inside the Docker build context that point outside it. This is a hard limitation of BuildKit — no flag overrides it.

---

---

## Docker Compose Multi-Service Image Naming (2026-09-09)

When two services in `docker-compose.yml` have separate `build:` blocks using the **same Dockerfile**, Docker Compose assigns **different image names** to each service (e.g., `them_gateway-them-go-worker` and `them_gateway-them-go-worker-2`). Rebuilding service 1 does NOT update service 2's image.

**Symptoms:** After `docker compose build --no-cache svc1`, `svc2` container still runs the old binary. `strings /app/binary | grep sql` shows old SQL.

**Diagnosis:** `docker inspect container-name --format '{{.Image}}'` vs `docker images image-name --format '{{.ID}}'` — image SHA mismatch = stale container.

**Fix:** Always name both services explicitly in `docker compose build`:
```bash
docker compose ... build --no-cache them-go-worker them-go-worker-2
docker compose ... up -d --force-recreate them-go-worker them-go-worker-2
```

**Watch for:** Any pair of containers that share a Dockerfile but differ only by env vars. Always rebuild and recreate both together after a code change.

---

## A rebuilt Docker image can still run a stale binary if the running container was never restarted (found 2026-09-24)

**Symptom:** `PUT /admin/my/llm-providers/{name}` (Settings → LLM Providers → Save allowed models)
returned a plain 404 `{"error":"not found"}` for the bootstrap tenant's own Anthropic row —
verified the row existed, the JWT's `tenant_id` claim was correct, the SQL query worked fine via
direct `psql`, and the route/handler/service code on disk was already correct (matched Phase 2's
`GetProviderByNameForTenant(ctx, name, tenantctx.BootstrapTenantID)` fix from earlier the same
session). Every static check passed; the request still 404'd.

**Root cause:** `them-go-bridge`'s running container had not been rebuilt/restarted since an
earlier commit landed this session (most likely Phase 2's `dal/llm_providers.go` fix). `docker
inspect`'s image `Created` timestamp looked recent (a few hours old) — this was misleading: the
image being "recent" only means *some* build happened recently, not that it contains the *latest*
committed source. Confirmed by adding a temporary debug log inside the failing code path,
rebuilding + restarting, and observing the exact same request succeed — with the debug log itself
never firing, and zero net diff on the file once removed. The old binary was simply running old
logic that no longer exists in git history in any form still checked in.

**Fix:** `docker compose ... build them-go-bridge && docker compose ... up -d them-go-bridge`.

**Watch for:** after any Go source change lands (commit, or even before commit in the same
session), if `them-go-bridge` isn't rebuilt+restarted as part of that same change, later manual
`curl`/browser verification against the live container is silently testing old code — a passing
`go test ./...` run proves the source is correct, never that the *running service* reflects it.
This project's own `CLAUDE.md` trigger map already says to rebuild+restart specific workers after
touching their packages (e.g. `them-dag-worker` after `internal/temporal/`) — this incident is the
same principle applied to `them-go-bridge` itself, which has no dedicated trigger-map row today
because nearly every `internal/admin/` change implies it. Before trusting *any* live/manual
verification result (not just automated tests) as evidence a fix works, rebuild + restart the
specific container being tested first, even if it "was already rebuilt earlier this session."

---

---

## `docker compose restart` does NOT pick up a freshly built image — only `up -d` does (found 2026-09-28)

**Symptom:** File Guard's raw-bytes synchronous-wait fix (Phase 0 of
`docs/_archive/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md`) kept showing the pre-fix `"pending"` trace on live runs,
across two separate rebuild+restart cycles, even though `docker inspect <container> --format
'{{.Image}}'` and `docker images ... --format '{{.ID}}'` appeared consistent enough at a glance to
look like the fix was deployed. Only surfaced clearly once a temporary debug log line was added,
rebuilt, and STILL never fired on a live run — proof the running binary genuinely predated the
edit, not a logic bug in the fix itself.

**Root cause:** the deploy command used was `docker compose ... --profile temporal restart
them-dag-worker them-dag-worker-2 them-dag-worker-debug` after `... build ...`. `restart` only
stops and starts the EXISTING container — it does not check whether the image tag it was created
from has since been rebuilt, and does not swap in the new image. The container kept running the
old binary indefinitely across any number of `build` + `restart` cycles. `docker inspect
<container> --format '{{.Image}}'` will keep reporting the OLD image's digest in this case, which
looks like a legitimate confirmation but is actually the tell — comparing it against `docker images
<repo> --format '{{.ID}}'` (the freshest build) would have shown the mismatch immediately, but this
comparison was done loosely/dismissed as a manifest-list-digest formatting difference instead of a
real mismatch the first time around.

**Fix:** `docker compose ... build <services> && docker compose ... up -d --force-recreate
<services>` — `up -d` (with or without `--force-recreate`) detects that the image changed and
recreates the container from the new image; `restart` never does this recreation step at all.

**Watch for:** any deploy step in this project that uses `restart` after a `build` — grep the
command being run, not just its stated intent. When verifying a live fix, always diff the exact
image ID string (`docker inspect <container> --format '{{.Image}}'` vs `docker images <repo>
--format '{{.ID}}'`) character-for-character — a "looks close enough" comparison across a
manifest-list digest and a per-platform ID is not the same check and will hide this exact bug.
Found while live-verifying `go/TEST_INDEX.md`'s S1-177 (File Guard Phase 0).

---
