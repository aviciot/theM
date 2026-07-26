# Session Handover
# Generated: 2026-07-26
# Scope: Go-Native Engineering Gate complete — Wave 7 Phase 3 has not started

---

## Git State

**Branch:** `main`
**HEAD:** `233e8bc docs(gate): Go-Native Engineering Gate — architecture review and mandatory rules`
**origin/main:** synchronized — gate commit pushed successfully.
**Working tree:** clean (only `go/them` compiled binary is untracked — do not commit)

### Commits since Wave 6 base (newest first)

```
233e8bc  docs(gate): Go-Native Engineering Gate — architecture review and mandatory rules
b80ff78  docs(handover): update push status — all commits pushed to origin/main
cc03e2f  docs(handover): correct push status — HTTPS credentials unavailable, 15 commits pending
1d83c61  docs(handover): Tenant Foundation Gate — ownership decisions and Wave 7 impact
6637887  docs(wave7): Phase 2 implementation report, lessons-learned, TEST_INDEX, handover
dc391b7  feat(admin/service): Wave 7 Phase 2 — LLMProviderService + unit tests
9df65cd  feat(admin/dal): Wave 7 Phase 2 — LLM provider DAL and interface
44208ee  feat(crypto): Wave 7 Phase 1 — Go Fernet compatibility package
```

Wave 6 base: `f4faa06`

---

## Push Status

**Pushed successfully.** Gate commit `233e8bc` delivered to `origin/main`.
Branch is up to date with `origin/main`.

---

## Go-Native Engineering Gate Result

**Full report:** `docs/architecture-v2/GO_NATIVE_ENGINEERING_GATE.md`

**Overall verdict: APPROVED WITH CONDITIONS**

The Go gateway is **architecturally Go-native**. No structural copies of Python implementation
were found in critical paths. Where Python patterns appear they preserve the wire contract, not
Python internals.

### Confirmed Go-native improvements over Python

| Area | Python | Go |
|---|---|---|
| History loading | Full-scan then slice | DB-level `LIMIT` in query |
| Ghost session pruning | TTL bug — no pruning | Atomic Lua SREM + shadow TTL |
| Session counter | Hardcoded 0 in heartbeat | `atomic.LoadInt32` counter |
| HMAC check order | Delegates to library | Explicit constant-time before decrypt |
| Token cache | Auth service on every request | sync.Map L1 + Redis L2 + pub/sub eviction |
| Admission gate | No gate | Atomic Lua admission script |

---

## Must-Fix Before Wave 7 Phase 3 (2 findings)

### MF-1 — ErrConflict missing from writeServiceError

**File:** `go/internal/admin/middleware.go` line 116
**Impact:** `LLMProviderService.Create` returns `ErrConflict` on duplicate name.
`writeServiceError` has no case for `ErrConflict`. The handler falls through and returns 500.
The correct response is 409 Conflict.
**Fix:** Add one case to `writeServiceError`:
```go
case errors.Is(err, service.ErrConflict):
    writeError(w, http.StatusConflict, err.Error())
```
**Effort:** 2 lines. Must be in the same commit as the Phase 3 handler.

### MF-2 — Phase 3 handler 500 responses must use static strings

**File:** Phase 3 handlers only (not yet written)
**Impact:** Service-layer errors for LLM provider operations could include DB error messages
with internal table/column names if `err.Error()` is forwarded to clients.
**Fix:** Phase 3 handler 500 responses must use `"internal server error"` not
`"create provider: " + err.Error()`. The service layer provides typed errors for all expected
failures; only unexpected infra errors reach the 500 path.
**Effort:** Review constraint enforced during Phase 3 implementation.

---

## 11 Deferred Findings

All documented in `docs/architecture-v2/GO_NATIVE_ENGINEERING_GATE.md §11`.

| ID | Description | Classification |
|----|-------------|----------------|
| A-1 | Triple `newID()` definition in ws/sse/a2a | low-risk cleanup |
| A-2 | WS/SSE ServeHTTP code duplication (~450 lines each) | architecture debt |
| L-1 | orchDone drain race — event loss under high throughput | low-risk |
| L-2 | Heartbeat goroutine has no shutdown path | architecture debt |
| L-3 | Subscribe goroutines vs Redis close ordering | architecture debt |
| R-1 | Fetch-then-modify PATCH = 2 DB round-trips | performance risk (acceptable) |
| T-1 | SessionInfo missing TenantID field | architecture debt (tenant wave) |
| T-2 | AppID not set in session Hash at registration | architecture debt (tenant wave) |
| P-1 | Numbered sequential comments mirror Python flow | low-risk cleanup |
| P-2 | maskKey uses slog.Warn directly, not injected logger | low-risk cleanup |
| P-3 | epconfig.CheckAccess is a free function (style) | low-risk cleanup |

None of these affect correctness of the Wave 7 Phase 3 routes.

---

## Wave 7 Status

| Phase | Description | Status | Commit |
|---|---|---|---|
| Phase 1 | Fernet crypto package (`go/internal/crypto/`) | **COMPLETE** | `44208ee` |
| Phase 2 | LLM provider DAL + service layer | **COMPLETE** | `9df65cd` + `dc391b7` |
| Tenant Gate | Tenant foundation decisions | **COMPLETE** | `1d83c61` |
| Go-Native Gate | Architecture review + mandatory rules | **COMPLETE** | `233e8bc` |
| Phase 3 | HTTP handlers + route wiring + contract tests + Traefik cutover | **NOT STARTED** |

---

## Phase 3 Has Not Started

No handlers have been added. No routes have been registered. No Traefik labels have been
changed. The Python bridge continues to serve all 5 LLM provider CRUD routes on port 8001.

---

## Exact Next Task — Wave 7 Phase 3

Implement HTTP handlers and Traefik cutover for LLM provider CRUD (5 routes).

### Pre-condition (do first)

Fix MF-1 in `go/internal/admin/middleware.go` before writing any handler:

```go
// in writeServiceError, add before `default:`
case errors.Is(err, service.ErrConflict):
    writeError(w, http.StatusConflict, err.Error())
```

Run `go test ./internal/admin/...` to confirm no regression. This fix may be committed alone
or together with the Phase 3 handler commit — but must not be omitted.

### Phase 3 implementation steps (from WAVE7_PLAN.md)

**New file:** `go/internal/admin/llm_providers.go`

5 handler methods on `LLMProvidersHandler`:
- `GET /admin/llm-providers` → `service.List` → 200 `[]LLMProviderOut`
- `POST /admin/llm-providers` → `service.Create` → 201 + `Location` header; 400/409 on error
- `GET /admin/llm-providers/{id}` → `service.Get` → 200; 404 on not found
- `PATCH /admin/llm-providers/{id}` → `service.Update` → 200; 404 on not found
  - PATCH body: use `json.RawMessage` intermediate to detect `api_key` field presence
  - Set `LLMProviderPatch.APIKeyPresent = true` when `"api_key"` key appears in JSON
- `DELETE /admin/llm-providers/{id}` → `service.Delete` → 204; 404 on not found

Handler must NOT:
- Log the request body (contains plaintext `api_key`)
- Forward `err.Error()` from service to HTTP responses on 500 paths
- Import `internal/crypto` (crypto stays in service layer)

**Modified files:**
- `go/internal/admin/router.go` — register `LLMProvidersHandler` in `BuildRouter`
- `go/internal/admin/admin_test.go` — add handler unit tests (fake-DB pattern)
- `go/TEST_INDEX.md` — update test count and trigger map

**Traefik cutover (separate commit after all tests pass):**
- Add `them-go-llm-providers` router block in `docker-compose.yml`
- Rule: `PathPrefix(/api/v1/admin/llm-providers) && !Path(/api/v1/admin/llm-providers/routing/config)`
- Priority: 120 (same as tokens, sessions, monitoring-config)
- Confirm live request hits Go bridge via logs before declaring cutover complete

**Contract tests:**
- `scripts/test_wave7_fernet_compat.py` already exists — run it after cutover

**Commit plan (from WAVE7_PLAN.md):**

| Commit | Files |
|--------|-------|
| Phase 3a (MF-1 + handler) | `go/internal/admin/middleware.go`, `go/internal/admin/llm_providers.go`, `go/internal/admin/router.go`, `go/internal/admin/admin_test.go`, `go/TEST_INDEX.md` |
| Phase 3b (Traefik cutover) | `docker-compose.yml` |
| Phase 3c (docs) | `docs/architecture-v2/WAVE7_IMPLEMENTATION_REPORT.md`, `docs/architecture-v2/implementation-status.md`, `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` |

---

## Live Traefik Route Ownership (unchanged)

### Go-owned routes

| Router name | Rule | Priority | Owner |
|---|---|---|---|
| `them-go-health-sub` | `PathPrefix(/health/live) \|\| PathPrefix(/health/ready)` | 130 | Go |
| `them-go-admin-reads` | `PathPrefix(/api/v1/admin/agents\|orchestrators\|applications) \|\| PathPrefix(/api/v1/runs) && Method(GET)` | 110 | Go |
| `them-go-tokens` | `PathPrefix(/api/v1/admin/tokens)` | 120 | Go (Wave 5) |
| `them-go-sessions` | `PathPrefix(/api/v1/admin/sessions)` | 120 | Go (Wave 5) |
| `them-go-monitoring-config` | `Path(/api/v1/admin/monitoring-config)` | 120 | Go (Wave 6) |
| `them-go-llm-routing-config` | `Path(/api/v1/admin/llm-providers/routing/config)` | 120 | Go (Wave 6) |

### Python still owns

| Route | Python file |
|---|---|
| `/api/v1/admin/llm-providers` (5 CRUD routes) | `admin_llm_providers.py` — **Wave 7 Phase 3 target** |
| `POST/PUT/DELETE /api/v1/admin/agents*` | `admin_agents.py` |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | `admin_orchestrators.py` |
| `POST/PUT/DELETE /api/v1/admin/applications*` | `admin_applications.py` |
| `/api/v1/auth/*` | auth service proxy |
| `/ws/dashboard` | `ws_dashboard.py` |
| `/ws/orchestrate/{name}` | `ws_orchestrator.py` |

---

## Test State (end of Go-Native Gate)

```
go test ./...                                    23 packages PASS, 0 failures
go test -race ./internal/admin/... ./internal/crypto/...    PASS, 0 data races
go test -tags=integration ./internal/admin/dal/...          11/11 PASS (live Postgres)
go test ./internal/crypto/...                               32/32 PASS
python3.12 scripts/tests/run_tests.py 01 02 03 04 15        55/55 PASS
```

No changes to test files were made during the gate session.

---

## Hard Constraints That Must Remain in Force

1. No plaintext API key may be returned, logged, embedded in errors, or persisted unencrypted.
2. WARN logs may contain provider ID and error category only — never plaintext, ciphertext, or key material.
3. `api_key_encrypted` DB column must never appear in any JSON response field.
4. `THE_M_SECRET_KEY` / `SECRET_KEY` is validated at startup (non-empty, non-default) — do not remove.
5. Plaintext bytes must be zeroed immediately after masking.
6. `APIKeyPresent` flag must be set correctly by the handler — absence and null are distinct.
7. No SQL outside DAL. No crypto inside handlers.
8. All list endpoints return `[]` not `null`.
9. `go test ./...` must pass before every commit.
10. `TEST_INDEX.md` must be updated in the same commit as any test change.
11. LLM providers are platform-global — no `tenant_id` parameter in any Wave 7 handler.
12. Wave 7 Phase 3 handlers must be classified as Platform control-plane API in any catalogue.
13. `writeServiceError` must handle every typed sentinel returned by any service it covers (MF-1).
14. Handler 500 responses must use static strings — never `err.Error()` from service/DAL (MF-2).

---

## Files Most Relevant to Phase 3

| File | Purpose |
|---|---|
| `go/internal/admin/middleware.go` | Add ErrConflict case (MF-1 fix) |
| `go/internal/admin/service/llm_providers.go` | Service layer — already complete |
| `go/internal/admin/dal/llm_providers.go` | DAL layer — already complete |
| `go/internal/crypto/fernet.go` | Crypto package — already complete |
| `go/internal/admin/router.go` | Register new handler here |
| `go/internal/admin/admin_test.go` | Add handler tests here |
| `go/TEST_INDEX.md` | Update in same commit as tests |
| `docker-compose.yml` | Add Traefik router block for cutover |
| `docs/architecture-v2/WAVE7_PLAN.md` | Full Phase 3 spec |
| `docs/architecture-v2/GO_NATIVE_ENGINEERING_GATE.md` | Gate findings — read before Phase 3 |

---

## First Prompt for Next Session (Wave 7 Phase 3)

```
Read first, in this exact order:
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/GO_NATIVE_ENGINEERING_GATE.md
5. /opt/docker/them/docs/architecture-v2/WAVE7_PLAN.md

Verify:
- branch is main
- HEAD is 233e8bc or newer
- origin/main is synchronized
- working tree is clean

Use Sonnet.

This is Wave 7 Phase 3: HTTP handlers + route wiring + Traefik cutover for LLM provider CRUD.

Before writing any handler, apply MF-1:
In go/internal/admin/middleware.go, add to writeServiceError:
    case errors.Is(err, service.ErrConflict):
        writeError(w, http.StatusConflict, err.Error())
Run go test ./internal/admin/... to confirm no regression.

Then implement:
1. go/internal/admin/llm_providers.go — LLMProvidersHandler with 5 methods
   (List/Create/Get/Update/Delete). PATCH must detect api_key field presence via
   json.RawMessage and set APIKeyPresent=true when present.
2. Register LLMProvidersHandler in go/internal/admin/router.go BuildRouter.
3. Add handler unit tests to go/internal/admin/admin_test.go (fake-DB pattern).
4. Update go/TEST_INDEX.md in the same commit.
5. Separate commit: add them-go-llm-providers Traefik router block in docker-compose.yml.
6. Confirm live request hits Go bridge via logs before declaring cutover complete.
7. Run Python suite: python3.12 scripts/tests/run_tests.py 01 02 03 04 15

Security constraints:
- Do not log the request body (contains plaintext api_key)
- Handler 500 responses use static strings only — never err.Error()
- No crypto imports in handlers — crypto stays in service layer

Stop at handover after cutover is confirmed and all tests pass.
```
