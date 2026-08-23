# Current Session State — the-M
# Last updated: 2026-08-23
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `dd8d546` — feat(a2a): multi-artifact + streaming agent support

Recent commits (newest first):
```
dd8d546 feat(a2a): multi-artifact + streaming agent support
f11c2f5 feat(a2a): full multi-artifact support — A2A spec compliant
671f371 fix(builder): force node re-render after registry fetch + fix controlled input
9da0f2b fix(builder): re-render nodes after node-type registry fetch resolves
8b00ab2 chore(traefik): remove stale Python bridge routing labels
2fe7858 docs(a2a): multi-artifact analysis + streaming gap investigation
5eb0e30 fix(canvas): validate live definition, fix emoji 404, fix debounce data flow
7155933 docs(current): update CURRENT.md — BuildValidator UI complete, new next task
a58287f feat(canvas): connect Canvas UI to BuildValidator — live validation, node/field highlighting
6ff1981 fix(docu-writer): fix format routing and markdown fence stripping
```

---

## Deployment state

**Active deployment: local Linux server**

Stack startup command:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
```

UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is the sole auth service (HS256 JWT + bcrypt)
- **`them-bridge` (Python) is permanently retired** — behind `profiles: [legacy]`; does NOT start in default or `--profile temporal` mode
- **`them-worker` (Python) is permanently retired** — behind `profiles: [legacy]`
- `them-go-bridge` is the active API gateway on port 8002
- `them-go-worker` is the active Temporal worker — **no explicit profile in `docker-compose.dev.yml`**, starts by default
- `them-agent-runtime` runs 2 replicas (port 9300 internal), profile `[agents]`
- Frontend `THE_M_API_URL` points to `http://them-traefik:8088`
- Named Docker volumes: `them-postgres-data`, `them-redis-data`, `them-logs` — `external: true`
- **Project name: `them_gateway`** — required for all compose commands

Currently running containers (verified):
```
them-go-bridge        ✅ healthy
them-go-worker        ✅ running (no profile — default service)
them-auth-go          ✅ healthy
them-agent-runtime-1  ✅ healthy (port 9300)
them-agent-runtime-2  ✅ healthy (port 9300)
them-frontend         ✅ running
them-postgres         ✅ healthy
them-redis            ✅ healthy
them-traefik          ✅ healthy
temporal-frontend     ✅ (with --profile temporal)
them-bridge (Python)  ❌ NOT running — profiles: [legacy]
them-worker (Python)  ❌ NOT running — profiles: [legacy]
```

---

## Go route ownership (all confirmed via Traefik labels)

All routes below are owned by `them-go-bridge` (`them-go-bridge-svc`, port 8002):

### Admin — read
- `GET /api/v1/admin/agents` (list)
- `GET /api/v1/admin/orchestrators` (list)
- `GET /api/v1/admin/applications` (list)

### Admin — write
- `POST /api/v1/admin/agents` — create
- `PUT|PATCH|DELETE /api/v1/admin/agents/{id}` — update/delete
- `POST /api/v1/admin/agents/discover`, `/agents/{id}/test`, `/agents/{id}/security-scan`
- `POST|PUT|PATCH|DELETE /api/v1/admin/orchestrators/{name}`
- `POST /api/v1/admin/applications`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}`
- `POST /api/v1/admin/applications/{id}/entry-points`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}`
- `PathRegexp /api/v1/admin/applications/{id}/.+` — all methods (covers provider-keys, runtime, agent-bindings subroutes)

### Admin — full ownership
- `PathPrefix /api/v1/admin/system-agents` — all methods
- `PathPrefix /api/v1/admin/tokens` — all methods
- `PathPrefix /api/v1/admin/sessions` — all methods
- `PathPrefix /api/v1/admin/component-definitions` — all methods
- `PathPrefix /api/v1/admin/agent-definitions` — all methods
- `GET|PUT /api/v1/admin/llm-providers/routing/config`
- `PathPrefix /api/v1/admin/llm-providers` — all methods
- `GET|POST|PATCH|DELETE /api/v1/admin/monitoring-config`

### Runs
- `GET /api/v1/runs` (list)
- `GET /api/v1/runs/stats`
- `GET /api/v1/runs/{id}` (detail)
- `GET /api/v1/runs/{id}/tasks`
- `GET /api/v1/runs/{id}/artifacts`
- `PATCH /api/v1/runs/{id}/cancel`
- `DELETE /api/v1/runs/{id}`
- `POST /api/v1/runs/bulk-delete`
- `POST /api/v1/runs/{id}/signal`

### App entry points (WS/SSE)
- `GET /apps/{slug}/ws`
- `GET|POST /apps/{slug}/sse`
- `GET /ws/orchestrate/{orch}/{ep}` (two-segment legacy path)
- `GET|POST /sse/orchestrate/{orch}/{ep}` (two-segment legacy path)

### Dashboard
- `GET /ws/dashboard`

### Health
- `GET|HEAD /health/live`, `/health/ready`

### Not yet migrated to Go Traefik (handler exists but route not wired)
- **`/a2a/*`** — Go handler implemented at `go/internal/a2a/` and mounted in `main.go`, but Traefik router `them-a2a` still points to `them-bridge-svc` (port 8001, Python — dead). **Active bug: `/a2a/` is currently broken.** Fix: redirect `them-a2a` router to `them-go-bridge-svc` in compose labels.

### Not in Go (no handler or Traefik route)
- `GET /api/v1/admin/users`, `/roles`, `/teams` — auth admin CRUD (served by `them-auth-service` on port 8701 directly from frontend; no Go handler needed unless we want to proxy it)
- `GET /runs/context/{ctx}/artifacts` — not used by admin UI
- Applications export/import/restore — Python-only, not migrated

---

## DB schema state (live)

All migrations applied through `db/037_agents_transport_canvas.sql`:

| Migration | Status |
|---|---|
| `db/001_schema.sql` through `db/027_*` | ✅ applied |
| `db/028_entry_points_tenant_scoped_slug.sql` | ✅ applied |
| `db/029_component_registry_foundation.sql` | ✅ applied |
| `db/030_component_subtype_adoption.sql` | ✅ applied |
| `db/031_phase_c_compiler_pins.sql` | ✅ applied |
| `db/032_ep_memory_config.sql` | ✅ applied — `entry_points` has 6 memory columns; `tasks.tenant_id` exists |
| `db/033_*` through `db/034_*` | ✅ applied |
| `db/035_agent_definitions.sql` | ✅ applied — `agent_definitions` table exists |
| `db/036_canvas_a2a_runtime.sql` | ✅ applied — `agent_runtime_specs` + `app_agent_bindings` exist |
| `db/037_agents_transport_canvas.sql` | ✅ applied — `agents_transport_check` includes `'canvas_a2a'` |

---

## Test state

```
go test ./...  — all packages, 0 failures (verified in Docker build dd8d546)
S1-11 agentregistry: 17 tests (was 10; +4 multi-artifact, +2 streaming)
```

---

## A2A feature state

### Playground + Artifacts tab
- Artifacts tab renders all file types: `image/*` → `<img>`, `application/pdf` → iframe,
  `text/html` → srcDoc iframe, `text/markdown`/text → `<pre>`, unknown → download
- `ArtifactPart.data` (base64) added to Go DAL + frontend API types
- Binary artifacts base64-encoded in `GetRunArtifacts` for transport to browser

### Multi-artifact (A2A spec compliant)
- `extractA2AResult` loops ALL artifact objects and ALL parts within each
- Single file → backward-compat `{"artifact":{}}` shape
- Multiple files → `{"artifacts":[...]}` plural shape
- Orchestrator fans out each artifact to `emitArtifactEvent` independently
- Strips both keys before LLM sees the result

### Streaming (SendStreamingMessage / SSE)
- `AgentConfig.SupportsStreaming` DB column + pgx scan
- `invokeA2AStreaming`: `bufio.Scanner` SSE reader, `onArtifact` callback per artifact
- `InvokeForRunStreaming` on `Registry` — signature matches `orchestrator.AgentInvoker`
- Non-streaming agents fall through to `InvokeForRun` transparently
- Orchestrator uses callback for progressive artifact recording/emission

### docu-writer agent
- Model: `claude-haiku-4-5-20251001` (async) — ~15-25s vs ~84s with sync Sonnet
- Formats: `html`, `markdown`, `pdf` (fpdf2)
- PDF: Claude → Markdown → fpdf2 → `bytes(pdf.output())` → `part.raw` (NOT `part.data`)
- Markdown fence stripping applied before rendering

---

## Canvas A2A Agent Builder — all phases complete

| Phase | What | State |
|---|---|---|
| A | Step config panels (LLM/HTTP/Transform/Response/Input forms) | ✅ |
| B | Skill editor, node library, data-flow subtitles, round-trip serialization | ✅ |
| C | `kind:"data"` part input mode; variadic `extraVars` in interpreter | ✅ |
| D | `a2a-go/v2` SDK replaces hand-rolled JSON-RPC dispatch; 12 tests (S1-53) | ✅ |
| Compiler | `go/internal/agentgen/compiler.go` — Compile + topoSort + DFS cycle detection | ✅ |
| Publish | `go/internal/admin/service/agent_definitions_publish.go` — compile + 3-table atomic CTE | ✅ |
| Binding UI | `AgentCredentialPanel` in applications page — per-slot credential entry | ✅ |
| Runtime wiring | `InvokeForRun` in `agentregistry` + `GetBindingID` | ✅ |
| Debug mode | Browser-side pipeline step-through with Anthropic API key | ✅ |
| Bug fixes | polJSON unmarshal, AllowedSkillIDs enforcement, skill selection by ID, slug cache | ✅ |
| BuildValidator UI | Debounced backend validation, node/field highlighting, issues panel, Publish gate | ✅ |

### Key security constraints (always in force)
- Credentials decrypted per-request, held only in `InvocationContext.Credentials`, never logged/persisted
- Port 9300 NOT in Traefik — agent-runtime is internal Docker network only
- `TaskState` has no credentials — re-decrypted from binding on each resume
- Binding invariant: `DefinitionID` pinned at publish, mismatch → 409

---

## LLM key architecture (current)

- Per-app keys stored in `applications.provider_keys` JSONB (AES-GCM encrypted)
- Format: `{"anthropic": {"ct": "enc:...", "hint": "XXXX"}}` (new) or `{"anthropic": "sk-ant-..."}` (legacy flat)
- **No global key fallback** — apps with no key get an explicit error (non-retryable Temporal failure)
- Worker: `resolveProvider` returns error when `cfg.LLMAPIKey == ""`
- Agent-runtime: `anthropicLLMFactory.NewProvider` returns error when `apiKey == ""`
- UI: Runtime tab in Applications view → provider + model + API key per app

---

## Next recommended task

### Step 1 — Test multi-artifact + streaming end-to-end (new, highest priority)

The code is committed but the streaming path has not been exercised against a live agent yet.
The docu-writer agent does NOT set `supports_streaming=true` — it is a sync A2A agent.
To test streaming you need an agent with `supports_streaming=true` in the DB that actually uses SSE.

**Testing checklist:**

**A. Multi-artifact (docu-writer, no streaming):**
1. Open playground → `doc-artifact-test` app
2. Send: `"Generate both an HTML page and a PDF of a project summary"`
3. Confirm Artifacts tab shows **two** entries — one `text/html`, one `application/pdf`
4. Confirm both download correctly and render in their respective viewers
5. Check run in admin → `/api/v1/runs/{id}/artifacts` returns 2 artifact objects

**B. Streaming (requires a streaming-capable agent):**
1. In DB: `UPDATE them.agents SET supports_streaming = true WHERE slug = '<your-a2a-agent>'`
2. That agent must respond to `POST /` with `Content-Type: text/event-stream`
3. Send a request through playground and observe artifacts arriving progressively
4. Confirm `onArtifact` callback fires — check `them-go-bridge` logs for artifact events
5. Verify each artifact is recorded in `them.artifacts` independently

**B-alt: use the a2a-stream test agent (`--profile test-agents`):**
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d a2a-stream
# Then register it in the agent store with supports_streaming=true
# and point an EP at it
```

### Step 2 — E2E canvas agent run (after A2A testing)
1. Publish a canvas agent (Input→LLM→Response) — validation UI should show green
2. Bind to an app with a valid API key
3. Include it as a tool in an app definition, publish
4. Run through playground — verify `run_steps` shows agent-runtime step

### Step 3 — After canvas run verified
- Auth admin CRUD (users/roles/teams) — Go implementation to retire Python `them-auth-service`

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

1. **Multi-artifact streaming not live-tested** — code committed and unit-tested, but no real streaming agent hit yet. See testing checklist above.

2. **E2E canvas agent run not verified** — all infrastructure exists (agent-runtime, InvokeForRun, A2A envelope, credential decryption) but no end-to-end run confirmed on live stack.

3. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. Frontend hits it directly. No Go proxy until we decide to retire the Python binary.

4. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims. Not started.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `go/internal/authserver/`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`
- **Python is permanently retired.** `them-bridge` and `them-worker` MUST remain behind `profiles: [legacy]`.
- **No global LLM key fallback.** Apps with no key get an explicit error.
- **No secrets in Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history.**
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** Global key must not be written or read.
- **EP cache key is `"{tenantID}:{slug}"`.** Invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{slug}"`.
- **`entry_points.tenant_id` is NOT NULL.** `UNIQUE(tenant_id, slug)` enforced at DB level.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** — never globally by name.
- **Project name: `them_gateway`** — required for all compose commands.

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Update this file at session end — do NOT create new NEXT_SESSION_*.md files.
3. ADRs are permanent — never archive them.
4. Trust code over docs; update docs when they diverge.
5. Documentation changes ship in same commit as the code they describe.
