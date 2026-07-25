# Session Handover
# Generated: 2026-07-25
# Scope: Wave 6 complete — monitoring-config + llm-providers/routing/config

---

## Git State

**Branch:** `main`
**HEAD:** `01860f4 fix(admin/service): use unprocessable (422) for monitoring threshold ordering violations`
**origin/main:** push required after this commit (previous push landed at a1cc4f8; 01860f4 is one commit ahead)
**Working tree:** clean (only `go/them` compiled binary is untracked — do not commit)

### Wave 6 commits (newest first)

```
01860f4  fix(admin/service): use unprocessable (422) for monitoring threshold ordering violations
a1cc4f8  docs(wave6): TEST_INDEX, implementation-status, lessons-learned, handover
55eb923  cutover(wave6): enable Traefik routing for monitoring-config + llm-providers/routing/config
b0d1a31  feat(admin): add MonitoringConfig + LLMRouting handlers (Wave 6 Phase 3)
e78a6bd  feat(admin/service): add ConfigService for monitoring + llm_routing (Wave 6 Phase 2)
69e2dca  feat(admin/dal): add config table GetConfig + UpsertConfig (Wave 6 Phase 1)
```

---

## Wave 6 Selected Routes

Four Python admin operations migrated to Go:

| Route | Methods | Config key | Python file |
|---|---|---|---|
| `/api/v1/admin/monitoring-config` | GET, PUT | `them.config['monitoring']` | `app/routers/admin_monitoring_config.py` |
| `/api/v1/admin/llm-providers/routing/config` | GET, PUT | `them.config['llm_routing']` | `app/routers/admin_llm_providers.py` (lines 186–204) |

No provider CRUD. No Fernet. No DB schema changes. No Redis keys. No Wave 7 started.

---

## Live Traefik Route Ownership

All entries confirmed from `docker inspect them-go-bridge` labels.

### Go-owned routes (through Traefik)

| Router name | Rule | Priority | Owner |
|---|---|---|---|
| `them-go-health-sub` | `PathPrefix(/health/live) \|\| PathPrefix(/health/ready)` | 130 | Go |
| `them-go-admin-reads` | `(PathPrefix(/api/v1/admin/agents\|orchestrators\|applications) \|\| PathPrefix(/api/v1/runs)) && Method(GET)` | 110 | Go |
| `them-go-tokens` | `PathPrefix(/api/v1/admin/tokens)` | 120 | Go (Wave 5) |
| `them-go-sessions` | `PathPrefix(/api/v1/admin/sessions)` | 120 | Go (Wave 5) |
| `them-go-monitoring-config` | `Path(/api/v1/admin/monitoring-config)` | 120 | Go **(Wave 6)** |
| `them-go-llm-routing-config` | `Path(/api/v1/admin/llm-providers/routing/config)` | 120 | Go **(Wave 6)** |

Note: Wave 5 tokens/sessions labels live in `theM_gateway/docker-compose.traefik.yml` (separate gateway deployment). Wave 6 labels are in the local `docker-compose.yml`.

### Python still owns (not yet migrated)

| Route | Python file |
|---|---|
| `POST/PUT/DELETE /api/v1/admin/agents*` | `admin_agents.py` (action endpoints) |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | `admin_orchestrators.py` |
| `POST/PUT/DELETE /api/v1/admin/applications*` | `admin_applications.py` |
| `/api/v1/admin/applications/{id}/import\|export\|restore\|bulk-delete\|runtime` | `admin_applications.py` |
| `/api/v1/admin/agents/{id}/test\|discover\|security-scan` | `admin_agents.py` |
| `/api/v1/admin/orchestrators/{name}/test-llm` | `admin_orchestrators.py` |
| `/api/v1/admin/llm-providers` (CRUD) | `admin_llm_providers.py` — Wave 7 |
| `/api/v1/auth/*` | auth service proxy |
| `/ws/dashboard` | `ws_dashboard.py` |
| `/ws/orchestrate/{name}` (one-segment) | `ws_orchestrator.py` |

---

## Go Replicas Verification

```
them-go-bridge   Up 5 hours (healthy)  — port 8002 (internal)
them-go-bridge-2 not running            — was removed during JWT debug; restart with same env vars if needed
```

The second replica (`them-go-bridge-2`) was removed during debugging the JWT env var bug. The compose file is correct; it just needs to be restarted with `THE_M_JWT_SECRET` set.

---

## Python Rollback Method

Wave 6 Traefik labels are in `docker-compose.yml`. Rollback is:

1. Remove the two Wave 6 router blocks from `docker-compose.yml`:
   - `them-go-monitoring-config` (4 lines)
   - `them-go-llm-routing-config` (4 lines)
2. Restart Go bridge: `docker compose --profile go up --no-deps -d them-go-bridge`

Python bridge (`them-bridge`) is always running and never disabled. Once the labels are removed, Traefik routes both endpoints to Python at priority 100 automatically. No code change or Python restart required.

---

## JWT Environment-Variable Bug Fixed

**What broke:** The Go bridge's `docker-compose.yml` environment block declared `SECRET_KEY=${SECRET_KEY:-change-this-in-production}` — using the wrong variable name. The auth service signs tokens with `JWT_SECRET=${THE_M_JWT_SECRET}`. When `JWT_SECRET` is absent from the container, the Go bridge falls back to `SECRET_KEY` for HS256 validation, which is a different key. All JWT-authenticated admin requests to the Go bridge silently failed with 401.

**File changed:** `docker-compose.yml` — `them-go-bridge` service environment block (commit `55eb923`).

**Exact changes:**
```yaml
# Before (wrong):
- SECRET_KEY=${SECRET_KEY:-change-this-in-production}

# After (correct):
- SECRET_KEY=${THE_M_SECRET_KEY:-change-this-in-production}
- JWT_SECRET=${THE_M_JWT_SECRET:-}
```

**Runtime requirement:** The Go bridge must be started with `THE_M_JWT_SECRET` set in the shell environment (the value from `them-auth-service` env: `3e40024cceb6348491f01a8145813b0400e5cc88661e2c525d8647d3a112bddc`). Without it the container falls back to `SECRET_KEY` and JWT auth fails.

**Documented in:** `docs/architecture-v2/lessons-learned.md` as L-10.

---

## Test Totals

### Go unit tests (Docker build validates via `go test ./...` in builder stage)
All pass. No new failures. 17 tests added:
- `service/config_test.go`: 10 tests (defaults, merge, stored round-trip, validation, upsert)
- `config_handler_test.go`: 7 tests (defaults, valid PUT, 422 on bad thresholds, 400 on bad JSON)

### Python suite
```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 20
87 passed, 1 skipped (bridge-2 replica not running — expected)
0 failed
```

---

## Contract Test Results (live — Python vs Go, direct port comparison)

| Operation | Python (8001) | Go (8002) | Match |
|---|---|---|---|
| GET /admin/monitoring-config (no stored row) | 200, 8 default fields | 200, 8 default fields | ✅ identical JSON |
| GET /admin/llm-providers/routing/config (no stored row) | 200, 4 default fields | 200, 4 default fields | ✅ identical JSON |
| PUT /admin/monitoring-config (valid) | 200, stored values returned | 200, stored values returned | ✅ identical JSON |
| PUT /admin/monitoring-config (heatmap out of order) | 422 | 422 | ✅ status match |
| PUT /admin/llm-providers/routing/config (with fallbacks) | 200, fallback fields returned | 200, fallback fields returned | ✅ identical JSON |
| PUT via Traefik → GET via Python (roundtrip persistence) | — | write persisted | ✅ verified |

---

## Confirmation: No DB Schema, Redis, or Fernet Changes

- **DB schema:** No new tables, columns, or migrations. Wave 6 only reads/writes the pre-existing `them.config` table (rows `monitoring` and `llm_routing`). No `db/*.sql` files changed.
- **Redis:** No new key prefixes. No TTLs added. `docs/REDIS.md` not modified.
- **Fernet:** No encryption or decryption. `app/utils/crypto.py` not touched. `api_key_encrypted` column in `them.llm_providers` not accessed.

---

## Wave 7 Proposed Scope

**LLM provider CRUD + Fernet compatibility.**

Routes to migrate:
- `GET /api/v1/admin/llm-providers` — list all providers
- `POST /api/v1/admin/llm-providers` — create provider (Fernet-encrypt `api_key`)
- `GET /api/v1/admin/llm-providers/{id}` — get provider (mask api_key, never return plaintext)
- `PATCH /api/v1/admin/llm-providers/{id}` — update provider (optionally rotate api_key)
- `DELETE /api/v1/admin/llm-providers/{id}` — delete provider

Fernet specifics (from `app/utils/crypto.py`):
- Key: HMAC-SHA256 of `SECRET_KEY` + salt `"fernet"`, truncated to 32 bytes, base64url-encoded → Fernet key
- Python uses `cryptography.fernet.Fernet` — AES-128-CBC + HMAC-SHA256
- Go must decrypt Python-encrypted values for `api_key_masked` (show last 4 chars)
- Go must encrypt new values in compatible Fernet format so Python can decrypt them

**Use Opus to plan Wave 7. Do not implement.**

---

## Wave 7 Implementation Status

**Wave 7 has NOT started.** No Go code for LLM provider CRUD or Fernet has been written.

---

## Exact Next Task

**Plan Wave 7 only. Do not write any Go code.**

---

## First Prompt for Next Session

```
Read first (in this order):
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/implementation-status.md

Verify:
- branch is main
- HEAD is 01860f4 or newer
- origin/main is synchronized
- working tree is clean (go/them binary is OK to ignore)

Run sanity checks:
  python3.12 scripts/tests/run_tests.py 01 02 03 04 15
  docker ps --filter name=them-go-bridge --format "{{.Names}} {{.Status}}"

Use Opus for this task.

The task is to plan Wave 7 only. Do not implement any code.

Read these files:
  app/routers/admin_llm_providers.py     (full file — CRUD + Fernet usage pattern)
  app/utils/crypto.py                    (encrypt_value / decrypt_value implementation)
  db/001_schema.sql                      (them.llm_providers table definition)

Determine:
1. Can Fernet decryption be implemented in Go using stdlib only (crypto/aes, crypto/hmac,
   encoding/base64)? If yes, describe the key derivation and format exactly.
2. What is the minimal Go struct for LLMProvider that matches Python's LLMProviderOut
   (id, name, display_name, api_key_set, api_key_masked, base_url, default_model,
   model_pricing, enabled)?
3. Is api_key_masked safe to produce without full decryption (e.g. last-N-chars of
   ciphertext), or must it decrypt the stored value?
4. Identify any blockers that would prevent Wave 7 from fitting in a single session.

Write the plan to: docs/architecture-v2/WAVE7_PLAN.md

Return only the plan file path and a one-paragraph summary.
Do not write any Go code.
```
