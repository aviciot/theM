# Admin Service Layer — Implementation Report
# Completed: 2026-07-24

---

## Summary

Extracted all business logic from `internal/admin/` HTTP handlers into a new
`internal/admin/service/` package. Six commits, zero regressions.

---

## Commits

| Hash | Description |
|---|---|
| `92a5cee` | Scaffolding — Dal/Cache/Temporal interfaces, typed errors, dal.IsNoRows |
| `51891fe` | RunService + rewire runs handler |
| `67a9e4f` | AgentService + rewire agents handler |
| `0a604cc` | OrchService + rewire orchestrators handler |
| `0236717` | AppService + rewire applications handler |
| `6c43492` | TokenService/TokenGenerator seam (Wave 5 boundary) |

---

## Package structure

```
go/internal/admin/service/
├── service.go       — Dal, Cache, Temporal interfaces; enabledOrDefault helper
├── errors.go        — ErrValidation, ErrUnprocessable, ErrNotFound, ErrTemporalUnavailable;
│                       FieldError; private validation/unprocessable constructors
├── runs.go          — RunService: List, Get, Signal
├── agents.go        — AgentService: List, Get, Create, Update, Delete + invalidate
├── orchestrators.go — OrchService: List, Get, Create, Update, Delete + invalidate
├── applications.go  — AppService: app CRUD + EP CRUD + invalidation orchestration;
│                       epConfigChannel, validEPTypes, IsValidEPType
└── tokens.go        — TokenGenerator interface + empty TokenService (Wave 5 boundary)
```

---

## Layer responsibilities (as implemented)

| Layer | Owns |
|---|---|
| Handler (`internal/admin/*.go`) | HTTP parsing, JSON decode, auth, status codes, chi URL params |
| Service (`internal/admin/service/`) | Validation, defaults, soft-delete, EP type validation, cache invalidation, Temporal ID construction, typed error sentinels |
| DAL (`internal/admin/dal/`) | SQL queries, row scanning, pgx pool |

---

## Key design decisions

**Consumer-defined interfaces.** `service.Dal`, `service.Cache`, `service.Temporal` are declared
in the service package. `*dal.DB` satisfies `service.Dal` structurally. No circular imports.

**dal.IsNoRows.** Added to `internal/admin/dal/dal.go` so the service layer can distinguish
`pgx.ErrNoRows` from other DB errors without importing pgx directly.

**Type aliases for backward compat.** `middleware.go` uses `type CacheInvalidator = service.Cache`
and `type TemporalSignaler = service.Temporal`. All existing handler constructor call sites in
`admin_test.go` compile unchanged.

**Typed error sentinels.** `ErrValidation` → 400, `ErrUnprocessable` → 422, `ErrNotFound` → 404,
`ErrTemporalUnavailable` → 503. `writeServiceError` in `middleware.go` maps them centrally.

**EP invalidation ordering.** `AppService.UpdateEntryPoint` publishes old slug before new slug.
This critical ordering contract is enforced in the service layer and verified by test
`TestAppService_UpdateEntryPoint_OldSlugBeforeNew`.

**enabledOrDefault.** Shared helper in `service.go` converts `*bool` to `bool` (nil → true).
Used by all four mutating services.

---

## Test results

```
go test ./internal/admin/...          → ok (all 3 packages)
go test ./...                         → ok (all 24 packages, 0 failures)
go test -race ./internal/admin/...    → ok (0 races in changed packages)
go test -race ./...                   → pre-existing race in internal/ws/TestDisconnectEndsSession
                                         NOT introduced by this work
```

**S1-25 (admin/service) test count: 23**

| Test | Covers |
|---|---|
| TestTokenService_Smoke | TokenService/TokenGenerator compile |
| TestAgentService_Create_Defaults | transport, MaxConcurrency, MaxRetries, TimeoutSeconds defaults |
| TestAgentService_Create_MissingSlug_Validation | ErrValidation |
| TestAgentService_Create_MissingDisplayName_Validation | ErrValidation |
| TestAgentService_Create_EnabledFalse_Respected | enabled=false passthrough |
| TestAgentService_Update_ReappliesMaxConcurrencyDefault | MaxConcurrency default on update |
| TestAgentService_Create_InvalidatesRegistry | them:agents:registry deleted |
| TestAgentService_NilCache_NoPanic | nil cache safety |
| TestOrchService_Create_Defaults | MaxIterations=10, HistoryWindow=20 defaults |
| TestOrchService_Create_MissingName_Validation | ErrValidation |
| TestOrchService_Create_InvalidatesCache | them:orchestrators:{name} deleted |
| TestOrchService_Delete_InvalidatesCache | them:orchestrators:{name} deleted |
| TestAppService_Create_MissingName_Validation | ErrValidation |
| TestAppService_CreateEntryPoint_InvalidType_Unprocessable | ErrUnprocessable for unknown types |
| TestAppService_CreateEntryPoint_ValidTypes | all 5 valid types accepted |
| TestAppService_UpdateEntryPoint_OldSlugBeforeNew | rename ordering invariant |
| TestAppService_UpdateEntryPoint_InvalidType_Unprocessable | ErrUnprocessable on update |
| TestAppService_DeleteEntryPoint_PublishesSlug | delete publishes slug |
| TestAppService_Update_InvalidatesAppEPs | app update flushes all EP slugs |
| TestRunService_Signal_BuildsWorkflowID | "ctx-{contextID}" convention |
| TestRunService_Signal_TemporalNil_Unavailable | ErrTemporalUnavailable |
| TestRunService_Signal_DBError_NotNotFound | non-pgx error not mapped to ErrNotFound |
| TestRunService_List_ForwardsParams | contextID and limit forwarded to DAL |

---

## API contracts unchanged

All handler constructor signatures are identical to pre-refactor:
- `NewAgentsHandler(db DBQuerier, cache CacheInvalidator) *AgentsHandler`
- `NewOrchestratorsHandler(db DBQuerier, cache CacheInvalidator) *OrchestratorsHandler`
- `NewApplicationsHandler(db DBQuerier, cache CacheInvalidator) *ApplicationsHandler`
- `NewRunsHandler(db DBQuerier, temporal TemporalSignaler) *RunsHandler`

All routes, HTTP status codes, JSON response shapes, and Traefik path rules are unchanged.
No DB schema changes. No migration needed.

---

## What is NOT done (Wave 5 boundary)

- `internal/admin/tokens.go` handler still calls DAL directly — Wave 5 wires it to `TokenService`
- `TokenGenerator.Generate` has no implementation — Wave 5 provides `auth.TokenIssuer`
- No new admin routes added

---

## TEST_INDEX.md

S1-25 (admin/service) added with 23 tests. Grand total: 283 unit tests.
Trigger map updated: changes to `internal/admin/service/` → run S1-25.
