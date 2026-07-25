# Session Handover
# Generated: 2026-07-25
# Scope: Wave 7 Phase 2 complete — LLM provider DAL + service layer

---

## Git State

**Branch:** `main`
**HEAD:** `dc391b7 feat(admin/service): Wave 7 Phase 2 — LLMProviderService + unit tests`
**origin/main:** NOT yet synchronized — local main is 12 commits ahead of origin/main (push required when credentials available)
**Working tree:** clean after documentation commit (only `go/them` compiled binary is untracked — do not commit)

### Wave 7 commits (newest first)

```
<doc commit>  docs(wave7): Phase 2 implementation report, lessons-learned, TEST_INDEX, handover
dc391b7       feat(admin/service): Wave 7 Phase 2 — LLMProviderService + unit tests
9df65cd       feat(admin/dal): Wave 7 Phase 2 — LLM provider DAL and interface
44208ee       feat(crypto): Wave 7 Phase 1 — Go Fernet compatibility package
```

Wave 6 base: `f4faa06`
Wave 5 base: `bc8c461`

---

## Current Objective

**Wave 7: LLM provider CRUD migrated from Python to Go.**

- Phase 1 (Fernet crypto package): **COMPLETE** — `44208ee`
- Phase 2 (DAL + service layer): **COMPLETE** — `9df65cd` + `dc391b7`
- Phase 3 (HTTP handlers + route wiring): **NOT STARTED**

---

## What Phase 3 Must Implement

**File:** `go/internal/admin/llm_providers.go` (or split into `llm_providers_handlers.go`)

### Routes

| Method | Path | Handler calls | Status code |
|--------|------|--------------|-------------|
| GET | `/api/v1/admin/llm-providers` | `service.List` | 200 |
| POST | `/api/v1/admin/llm-providers` | `service.Create` | 201 |
| GET | `/api/v1/admin/llm-providers/{id}` | `service.Get` | 200 |
| PATCH | `/api/v1/admin/llm-providers/{id}` | `service.Update` | 200 |
| DELETE | `/api/v1/admin/llm-providers/{id}` | `service.Delete` | 204 |

### Error mapping (handler → HTTP status)

| Service error | HTTP status |
|---|---|
| `ErrNotFound` | 404 |
| `ErrConflict` | 409 |
| `ErrValidation` | 422 |
| any other error | 500 |

### PATCH body — APIKeyPresent detection (CRITICAL)

The `LLMProviderPatch.APIKeyPresent` bool must be set by the handler when `api_key` appears in
the JSON body — regardless of whether its value is null or non-null.

**Required approach:** Decode the PATCH body in two passes:
1. First unmarshal into `map[string]json.RawMessage` (or equivalent) to detect presence of `api_key` key.
2. Then unmarshal into `LLMProviderPatch` normally.
3. Set `patch.APIKeyPresent = true` if `api_key` key was found in step 1.

Without this, `{"api_key": null}` and `{}` are indistinguishable at the Go struct level (both give `nil *string`).

### Route registration

Register in `cmd/them/main.go` under the admin sub-router with `RequireSuperAdmin` middleware.
After handler + test are working, add Traefik labels in `docker-compose.yml` for all 5 routes.

---

## Live Traefik Route Ownership (unchanged from Wave 6)

### Go-owned routes

| Router name | Rule | Priority | Owner |
|---|---|---|---|
| `them-go-health-sub` | `PathPrefix(/health/live) \|\| PathPrefix(/health/ready)` | 130 | Go |
| `them-go-admin-reads` | `PathPrefix(/api/v1/admin/agents\|orchestrators\|applications) \|\| PathPrefix(/api/v1/runs) && Method(GET)` | 110 | Go |
| `them-go-tokens` | `PathPrefix(/api/v1/admin/tokens)` | 120 | Go (Wave 5) |
| `them-go-sessions` | `PathPrefix(/api/v1/admin/sessions)` | 120 | Go (Wave 5) |
| `them-go-monitoring-config` | `Path(/api/v1/admin/monitoring-config)` | 120 | Go (Wave 6) |
| `them-go-llm-routing-config` | `Path(/api/v1/admin/llm-providers/routing/config)` | 120 | Go (Wave 6) |

### Python still owns (not yet migrated)

| Route | Python file |
|---|---|
| `/api/v1/admin/llm-providers` (CRUD — 5 routes) | `admin_llm_providers.py` — **Wave 7 Phase 3** |
| `POST/PUT/DELETE /api/v1/admin/agents*` | `admin_agents.py` |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | `admin_orchestrators.py` |
| `POST/PUT/DELETE /api/v1/admin/applications*` | `admin_applications.py` |
| `/api/v1/auth/*` | auth service proxy |
| `/ws/dashboard` | `ws_dashboard.py` |
| `/ws/orchestrate/{name}` | `ws_orchestrator.py` |

---

## Files Most Relevant to Phase 3

| File | Why |
|---|---|
| `go/internal/admin/service/llm_providers.go` | Service layer — Phase 3 calls these methods |
| `go/internal/admin/service/errors.go` | `ErrConflict`, `ErrNotFound`, `ErrValidation` — map to HTTP codes |
| `go/internal/admin/dal/llm_providers.go` | DAL types returned through service |
| `go/internal/admin/agents.go` | Handler pattern to follow (CRUD style) |
| `go/internal/admin/admin_test.go` | Pattern for handler tests — `S1-15` |
| `go/internal/admin/service/service_test.go` | `fakeDal` with provider stubs already wired |
| `cmd/them/main.go` | Where to register routes |
| `docker-compose.yml` | Where to add Traefik labels for cutover |
| `app/routers/admin_llm_providers.py` | Python contract — response shape, status codes |

---

## Services Built — Ready to Wire

```go
// In cmd/them/main.go wiring:
llmSvc := service.NewLLMProviderService(dal, cfg.SecretKey)
```

`service.NewLLMProviderService(d Dal, secretKey string) *LLMProviderService` — already exported,
takes the DAL interface and derives the Fernet key internally. No additional crypto setup needed.

---

## Test Results at End of Phase 2

### Go unit suite
```
go test ./...
23 packages PASS, 0 failures
```

S1-25 (admin/service): 60 tests (was 34; +26 for LLM provider service)

### Race detector
```
go test -race ./internal/admin/... ./internal/crypto/...
PASS, 0 data races
```

### DAL integration (live Postgres)
```
go test -tags=integration ./internal/admin/dal/...
11/11 PASS
```

### Fernet regression
```
go test ./internal/crypto/...
32/32 PASS
```

### Python suite
```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
55/55 PASS
```

---

## Hard Constraints That Must Remain in Force

1. **No plaintext API key may be returned, logged, embedded in errors, or persisted unencrypted.**
2. **WARN logs may contain provider ID and error category only — never plaintext, ciphertext, or key material.**
3. **`api_key_encrypted` DB column must never appear in any JSON response field.**
4. **`THE_M_SECRET_KEY` is validated at startup (non-empty, non-default) — do not remove this check.**
5. **Plaintext bytes must be zeroed immediately after masking** (`for i := range plainBytes { plainBytes[i] = 0 }`).
6. **`APIKeyPresent` flag must be set correctly by the handler** — absence and null are distinct states.
7. **No SQL outside DAL. No crypto inside handlers.**
8. **All list endpoints return `[]` not `null`.**
9. **`go test ./...` must pass before every commit.**
10. **`TEST_INDEX.md` must be updated in the same commit as any test change.**

---

## Exact Next Task

**Phase 3:** Implement the 5 LLM provider HTTP handlers and wire routes.

Steps:
1. Create handler file `go/internal/admin/llm_providers.go` (or add to `go/internal/admin/admin.go`)
2. Parse PATCH body with two-pass JSON decode to detect `api_key` presence
3. Map service errors to HTTP status codes per the table above
4. Register 5 routes in `cmd/them/main.go` under admin sub-router with `RequireSuperAdmin`
5. Write handler tests following `internal/admin/admin_test.go` pattern, using existing `fakeDal`
   (already has provider stubs in `service_test.go`)
6. Add handler test entries to `go/TEST_INDEX.md` (S1-15 or new section)
7. Run `go test ./...` and `go test -race ./internal/admin/...`
8. Add Traefik labels in `docker-compose.yml` for all 5 routes (priority 120)
9. Restart Go bridge and verify live requests hit Go from logs
10. Run `python3.12 scripts/tests/run_tests.py 01 02 03 04 15` — zero failures
11. Commit: `feat(admin): Wave 7 Phase 3 — LLM provider HTTP handlers + route wiring`
12. Commit: `docs(wave7): Phase 3 implementation report + handover`
13. Push if credentials available

---

## First Prompt for Next Session

```
Read first, in this exact order:
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/WAVE7_PLAN.md
5. /opt/docker/them/docs/architecture-v2/WAVE7_IMPLEMENTATION_REPORT.md
6. /opt/docker/them/go/internal/admin/service/llm_providers.go
7. /opt/docker/them/go/internal/admin/service/errors.go
8. /opt/docker/them/go/internal/admin/agents.go   (handler pattern to follow)
9. /opt/docker/them/app/routers/admin_llm_providers.py   (Python contract)

Verify:
- branch is main
- HEAD is dc391b7 or newer (documentation commit may be on top)
- working tree is clean
- go/them may remain as the only untracked compiled binary

Use Sonnet for implementation and testing.

Implement only Wave 7 Phase 3:
HTTP handlers and route wiring for LLM provider CRUD.

Do not add Fernet logic to handlers — call the service layer only.
Do not change the service or DAL — they are complete.
Do not change Traefik until handlers are tested and passing.

Scope:
- Handler file: go/internal/admin/llm_providers.go
- 5 operations: List, Get, Create, Update (PATCH with APIKeyPresent detection), Delete
- Route registration in cmd/them/main.go
- Handler tests following admin_test.go pattern
- Traefik labels in docker-compose.yml
- Live cutover verification from Go bridge logs

PATCH body MUST use two-pass JSON decode to detect api_key presence:
  raw := map[string]json.RawMessage{}
  json.Unmarshal(body, &raw)
  _, apiKeyPresent := raw["api_key"]
  // then decode into LLMProviderPatch normally
  patch.APIKeyPresent = apiKeyPresent

Security constraints remain in full force:
- No plaintext API key in any log, error, or response
- WARN logs: provider ID and error_category only
- api_key_encrypted never returned in JSON
- Plaintext bytes zeroed after masking (already done in service layer)

Tests required:
- Handler unit tests (no real DB) using fakeDal / fakeService pattern
- go test ./... (full suite, 0 failures)
- go test -race ./internal/admin/...
- python3.12 scripts/tests/run_tests.py 01 02 03 04 15

Commits required:
1. Handlers + handler tests + route registration + TEST_INDEX.md update
2. Traefik labels + live cutover verification
3. Documentation updates (WAVE7_IMPLEMENTATION_REPORT Phase 3, handover)

Stop after Phase 3. Do not begin Wave 8.
```
