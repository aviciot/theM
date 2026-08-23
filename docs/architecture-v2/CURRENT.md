# Current Session State — the-M
# Last updated: 2026-08-23
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `a58287f` — feat(canvas): connect Canvas UI to BuildValidator — live validation, node/field highlighting

Recent commits (newest first):
```
a58287f feat(canvas): connect Canvas UI to BuildValidator — live validation, node/field highlighting
6ff1981 fix(docu-writer): fix format routing and markdown fence stripping
bf5b536 docs(test-index) + fix(a2a): update TEST_INDEX for BuildValidator + fix /a2a Traefik routing
b9a84d4 feat(agentgen): BuildValidator — Issue type, Validate/CompileForPublish, stub severity
00ee278 docs(agentgen): update BuildValidator design — real-time UX, explicit Validate/CompileForPublish, stub severity
74812b2 feat(agents): canvas-built badge + Edit in Builder for published agents
01465eb fix(agent-runtime): fix 4 deferred code-review bugs in executeSkill / loadBinding
02648fb docs: remove Python-specific docs, clean up INDEX.md
e615767 fix(agent-runtime): remove global key fallback in anthropicLLMFactory
938e9b4 fix(worker): remove global key fallback — missing app key fails run explicitly
84a1855 fix(agentregistry): wrap canvas_a2a InvokeWithMeta in A2A JSON-RPC envelope
ca607e4 fix(llm-config): fix 5 code review bugs in LLM config and provider key layer
bba9590 feat(llm-config): move LLM provider/model config from canvas to App Runtime
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
go test ./...  — 38 packages, 0 failures
S1 total:         706
S2 total:          42
go test ./... :   666
```

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

**End-to-end canvas agent verification:**
1. Publish a canvas agent (Input→LLM→Response pipeline) — use the new validation UI to confirm it shows green before publish
2. Bind it to an application with a valid API key
3. Include it as a tool in an application definition, publish
4. Trigger a real run through playground
5. Verify `run_steps` shows the agent-runtime step recorded

**After that**: `/a2a/` routing fix — redirect `them-a2a` Traefik router from `them-bridge-svc` to `them-go-bridge-svc` (one-line compose label change).

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

1. **`/a2a/*` routing broken** — `them-a2a` Traefik router points to `them-bridge-svc` (dead Python). Go handler exists. One-line fix in compose labels. **Priority: fix first before any A2A testing.**

2. **E2E canvas agent run not verified** — all infrastructure exists (agent-runtime running, InvokeForRun wired, A2A envelope fixed, credential decryption working) but no end-to-end run through a canvas agent has been confirmed on the live stack.

3. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. The frontend hits it directly. No Go proxy needed unless we want to retire the Python auth service binary entirely.

4. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims, live two-tenant verification. Not started.

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
