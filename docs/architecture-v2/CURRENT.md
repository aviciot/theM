# Current Session State — the-M
# Last updated: 2026-08-24
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `6be45b6` — feat(mcp): MCP-1 admin CRUD API + DB migrations

Recent commits (newest first):
```
6be45b6 feat(mcp): MCP-1 admin CRUD API + DB migrations
2f29cd3 feat(agentgen): Phase 1 app-level agent params — decrypt-at-runtime injection
4cb2dd9 feat(a2a-stream): emit two file artifacts (HTML + zip) for multi-file streaming test
78be532 feat(agentgen): add Description to NodeDef + palette tooltips
1f7e229 fix(agentgen): correct EdgeRules for Input + clean up is_source/is_sink convention
a1ca8a3 fix(a2a-stream): fix SSE field names + role enum for streaming
d09a93f feat(agentgen): data-driven edge rules (Option A)
ccb9d40 feat(a2a-stream): add zip file artifact to streaming response for testing
134dc48 fix(validation): detect disconnected nodes + reject cycle edges
51e78c0 fix(admin): make /admin/node-types public — no auth required
dd8d546 feat(a2a): multi-artifact + streaming agent support
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
| `db/038_app_agent_params.sql` | ✅ applied — `app_agent_bindings.agent_params` JSONB column |

---

## Test state

```
go test ./...  — all packages, 0 failures (verified 2026-08-23, commit 2f29cd3)
S1-11 agentregistry: 17 tests (was 10; +4 multi-artifact, +2 streaming)
S1-48 agentgen (interpreter): +5 inject-mode tests (header/query/basic/custom_header/no-inject)
S1-50 agentgen (compiler): +3 AppParam tests (populate/undeclared/empty)
Live e2e confirmed 2026-08-23:
  - run 23aeb8bf: streaming single zip artifact via a2a-stream ✅
  - run 5691b24a: streaming two files (HTML + zip) via a2a-stream ✅
```

---

## A2A feature state — COMPLETE AND LIVE VERIFIED (2026-08-23)

| Scenario | Agent | Status |
|---|---|---|
| Sync single file | `docu-writer` (HTML/PDF/MD) | ✅ live |
| Streaming single file | `a2a-stream` v1.1 | ✅ live (run 23aeb8bf) |
| Streaming multi-file | `a2a-stream` v1.2 | ✅ live (run 5691b24a) |

### Playground + Artifacts tab
- Artifacts tab renders all file types: `image/*` → `<img>`, `application/pdf` → iframe,
  `text/html` → srcDoc iframe, `text/markdown`/text → `<pre>`, unknown → download
- `ArtifactPart.data` (base64) in Go DAL + frontend API types for binary transport
- Binary artifacts base64-encoded in `GetRunArtifacts` for transport to browser

### Multi-artifact (A2A spec compliant)
- `extractA2AResult` loops ALL artifact objects and ALL parts within each
- Single file → backward-compat `{"artifact":{}}` shape
- Multiple files → `{"artifacts":[...]}` plural shape
- Orchestrator fans out each artifact to `emitArtifactEvent` + `run_artifacts` independently
- Strips both keys before LLM sees the result

### Streaming (SendStreamingMessage / SSE)
- `AgentConfig.SupportsStreaming` set from agent card `capabilities.streaming` on discover
- `invokeA2AStreaming`: `bufio.Scanner` SSE reader, `onArtifact` callback per `lastChunk:true` event
- Wire format: `"role":"ROLE_USER"` (string, not int), camelCase JSON tags (`artifactUpdate`, `lastChunk`)
- Non-streaming agents fall through to `InvokeForRun` transparently
- **All worker replicas must be rebuilt together** — Temporal load-balances across all workers on same task queue

### docu-writer agent
- Model: `claude-haiku-4-5-20251001` (async) — ~15-25s vs ~84s with sync Sonnet
- Formats: `html`, `markdown`, `pdf` (fpdf2)
- PDF: Claude → Markdown → fpdf2 → `bytes(pdf.output())` → `part.raw` (NOT `part.data`)
- `part.data` is `google.protobuf.Value` (JSON only) — cannot hold binary bytes
- Markdown fence stripping applied before rendering

### a2a-stream test agent (v1.2.0)
- Streams ~16 text words word-by-word (0.1s apart)
- Emits `stream_report.html` (`text/html`, text part with filename)
- Emits `stream_output.zip` (`application/zip`, raw bytes via `part.raw`)
- `capabilities.streaming: true` → `supports_streaming=true` set automatically on discover

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

## App-level agent params — Phase 1 complete (2026-08-23)

**Backend Go implementation fully done (commit 2f29cd3). DB migration applied.**

What was built:
- `AppParamDecl` on `NodeDef` — node types declare what runtime params they accept
- `AgentParamSpec` collected by compiler (stage 3.5), stored in `agent_runtime_specs.spec` as `required_params`
- `app_agent_bindings.agent_params` JSONB — encrypted storage (Fernet for secrets, plaintext for others)
- `GET/PUT /admin/applications/{app_id}/agents/{agent_id}/params` — admin API (hint-only, no plaintext)
- `InvocationContext.AgentParams` — decrypted per-request in agent-runtime, never logged
- HTTP node: `bearer_token`/`api_key` params; inject modes: header/query/basic/custom_header
- LLM node: `model_override` param — overrides compiled model at runtime
- **Frontend (Phase 1) NOT yet done** — see next task below

What is pending (Phase 1 frontend):
1. `frontend/src/lib/api.ts`: Add `AgentParamMeta`, `AgentParamsResponse` types + `getAgentParams`/`putAgentParams`
2. `frontend/src/app/admin/applications/page.tsx` `RuntimeView`: Add "Agent Parameters" section per bound agent
3. `frontend/src/app/admin/agents/builder/page.tsx`: Add `app_param_key`/`inject_mode`/`inject_header_name` fields to HTTP step panel

---

## MCP-1 Migration Status

**Completed in this session:**
- `them-mcp-service` binary — `go/cmd/mcp-service/main.go` + `go/internal/mcp/` (all files: config, dal, client, registry, leader, supervisor, health, executor, server, health_test)
- `Dockerfile.mcp-service` + `docker-compose.yml` service entry (port 8010, Traefik disabled)
- DB migrations applied: `db/041_mcp_servers.sql` (them.mcp_servers) + `db/042_mcp_app_credentials.sql` (them.app_mcp_credentials)
- Admin CRUD API: `go/internal/admin/dal/mcp_servers.go`, `go/internal/admin/service/mcp_servers.go`, `go/internal/admin/mcp_servers.go`
- Service added to router: `go/internal/admin/router.go` (tenantScoped group)
- Dal interface updated: `go/internal/admin/service/service.go`
- All fake Dal structs updated: `service_test.go`, `agent_definitions_test.go`, `definitions_publish_test.go`, `tenant_isolation_test.go`
- 11 new unit tests: `go/internal/admin/service/mcp_servers_test.go`
- Docs updated: REDIS.md, SCHEMA.md, CLAUDE.md (trigger map + container map), TEST_INDEX.md (S1-62), CURRENT.md

**What's NOT done yet (MCP-2 onward):**
- Traefik labels for `/api/v1/admin/mcp-servers` on `them-go-bridge` — routes exist in Go but not yet Traefik-exposed
- Frontend UI for MCP server management
- MCP-2: canvas node type for MCP tool calls
- MCP-3: runtime executor wired into agent-runtime tool calls
- `them-mcp-service` not yet started in production (needs `--profile mcp` in compose)

---

## Next recommended task

### Step 1 — Phase 1 frontend for agent params (immediate)

Implement the frontend side of the agent params system (see design doc `docs/architecture-v2/APP_AGENT_PARAMS_DESIGN.md`, section "Phase 1 Frontend"):
1. `api.ts`: `getAgentParams(appId, agentId)` → `GET /admin/applications/{appId}/agents/{agentId}/params`; `putAgentParams(appId, agentId, params)` → `PUT` same route
2. `applications/page.tsx` RuntimeView: for each bound canvas agent, show its `required_params` with fill status + input fields for secrets/strings (hint shown when set)
3. `agents/builder/page.tsx` HTTP step panel: add `App Param Key` dropdown (from node's `AppParams` declarations) + `Inject Mode` select + conditional `Header Name` field

### Step 3 — E2E canvas agent run (after frontend done)

Streaming and multi-artifact are confirmed working. Next unverified path:
1. Publish a canvas agent (Input→LLM→Response) — BuildValidator should show green
2. Bind to an app with a valid API key in Runtime tab; set any required agent params
3. Add the canvas agent as a tool in an app EP, publish
4. Run through playground — verify `run_steps` shows agent-runtime step
5. Confirm credentials + agent params flow: binding → `InvocationContext` → agent-runtime → Anthropic

### Step 4 — Auth admin CRUD Go proxy (lower priority)
- `them-auth-service` (Python, port 8701) still serves user/role/team management
- Frontend hits it directly via its own Traefik routes
- When ready: implement Go proxy at `go/internal/authadmin/` + Traefik redirect

### Step 5 — Wave 9 tenant items
- Session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims
- Not started — begin only after Steps 1–3 are complete

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

1. **E2E canvas agent run not verified** — all infrastructure exists (agent-runtime, InvokeForRun, A2A envelope, credential decryption) but no end-to-end run confirmed on live stack.

2. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. Frontend hits it directly. No Go proxy until we decide to retire the Python binary.

3. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims. Not started.

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
