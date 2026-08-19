# Go Gateway — Test Index

**Purpose of this file:**
Every test in the Go gateway is listed here with its ID, type, trigger, and coverage.
This index is the source of truth for CI/CD pipeline configuration, deploy checklists,
and the CLAUDE.md trigger map. When you add, change, or delete a test — update this
file in the same commit. When a CI stage is wired, cross-reference the suite ID here.

---

## Test suites at a glance

| Suite | Command | When to run | Duration |
|---|---|---|---|
| **Unit** | `go test ./...` | Every commit, every PR, pre-deploy | ~5s |
| **Unit + Race** | `go test -race ./...` | Every PR merge, nightly | ~15s |
| **Integration** | `go test -tags=integration ./...` | Post-deploy smoke, staging, release | ~30s |
| **Live deploy** | `DEPLOY_AND_TEST.md` T-01 → T-23 | After every production deploy | ~10 min |

---

## Suite 1 — Unit tests (`go test ./...`)

No external services needed. All dependencies are mocked or in-process.
Run on: every commit, every PR, every pre-deploy check.

### S1-01 · Config — `internal/config/config_test.go`

**Purpose:** Startup validation rejects bad/missing secrets before any network connection is made.

| Test | What it proves |
|---|---|
| `TestLoad_ValidConfig` | All required env vars present → config loads cleanly |
| `TestLoad_MissingDatabasePassword` | Missing `DATABASE_PASSWORD` → error at startup |
| `TestLoad_EmptySecretKey` | Empty `SECRET_KEY` → error at startup |
| `TestLoad_DefaultSecretKey` | `SECRET_KEY=change-this-in-production` → error at startup |
| `TestLoad_MissingDatabaseHost` | Missing `DATABASE_HOST` → error at startup |
| `TestLoad_CustomPort` | `APP_PORT` env override is respected |
| `TestConfig_DSN` | DSN string format is correct for pgx |
| `TestConfig_RedisAddr` | Redis address `host:port` format is correct |
| `TestConfig_SafeString_MasksSecrets` | Secrets never appear in log output |
| `TestReconcilerDryRun_DefaultsToTrue` | Missing `RECONCILER_DRY_RUN` → defaults to `true` (safe) |
| `TestReconcilerDryRun_ExplicitTrue` | `RECONCILER_DRY_RUN=true` → `true` |
| `TestReconcilerDryRun_ExplicitFalse` | `RECONCILER_DRY_RUN=false` → `false` (enables writes) |
| `TestReconcilerDryRun_InvalidValueFallsToTrue` | `RECONCILER_DRY_RUN=not-a-bool` → `true` (fail-safe) |
| `TestRunEventsMode` | `RUN_EVENTS_MODE` parsing (Phase 11c-B): missing/invalid→pubsub, dual, streams, case-insensitive |
| `TestShutdownDrain_Default` | Missing `SHUTDOWN_DRAIN_SECONDS` → 30 (default) |
| `TestShutdownDrain_Valid` | `SHUTDOWN_DRAIN_SECONDS=60` → 60 |
| `TestShutdownDrain_BelowMin_Clamped` | `SHUTDOWN_DRAIN_SECONDS=2` → 5 (clamped to minimum) |
| `TestShutdownDrain_Invalid_Clamped` | `SHUTDOWN_DRAIN_SECONDS=abc` → 5 (clamped to minimum) |
| `TestWorkerTaskQueue_Default` | Missing `WORKER_TASK_QUEUE` → `"them-orchestration-go"` (R-2C: Go-only queue default) |
| `TestWorkerTaskQueue_Override` | `WORKER_TASK_QUEUE=custom-queue` → `"custom-queue"` (env var override respected) |

**Trigger:** any change to `internal/config/config.go` or `.env.example`

---

### S1-02 · Health — `internal/health/health_test.go`

**Purpose:** Liveness and readiness endpoints behave correctly under all infrastructure states.

| Test | What it proves |
|---|---|
| `TestLive_AlwaysReturns200` | `/health/live` returns 200 even when DB/Redis are down |
| `TestReady_BothHealthy_Returns200` | Both probes pass → 200 `{"status":"ok"}` |
| `TestReady_DBUnreachable_Returns503` | DB probe fails → 503 with postgres error |
| `TestReady_RedisUnreachable_Returns503` | Redis probe fails → 503 with redis error |
| `TestReady_BothUnreachable_Returns503` | Both fail → 503 listing both failures |

**Trigger:** any change to `internal/health/health.go`

---

### S1-03 · Server — `internal/server/server_test.go`

**Purpose:** All required routes are registered and respond on the correct paths.

| Test | What it proves |
|---|---|
| `TestRoutes_LiveEndpointRegistered` | `/health/live` returns 200 |
| `TestRoutes_ReadyEndpointRegistered` | `/health/ready` returns 200 with mock pingers |
| `TestRoutes_MetricsEndpointRegistered` | `/metrics` returns 200 with Prometheus text |
| `TestRoutes_UnknownPath_Returns404` | Unknown path returns 404 (not 200 or panic) |

**Trigger:** any change to `internal/server/server.go`

---

### S1-04 · JWT — `internal/auth/jwt_test.go`

**Purpose:** Local RS256 JWT validation — no HTTP calls, all paths covered.

| Test | What it proves |
|---|---|
| `TestValidateJWT_Valid` | Valid RS256 token → correct Claims returned |
| `TestValidateJWT_Expired` | Expired token → `ErrTokenExpired` |
| `TestValidateJWT_TamperedSignature` | Modified signature → `ErrTokenSignature` |
| `TestValidateJWT_Malformed_MissingDot` | Token with no `.` separator → `ErrTokenMalformed` |
| `TestValidateJWT_Malformed_TwoSegments` | Token with only 2 segments → `ErrTokenMalformed` |
| `TestParseRSAPublicKey_Valid` | Valid PEM key parses successfully |
| `TestParseRSAPublicKey_Garbage` | Random bytes → parse error |
| `TestParseRSAPublicKey_EmptyPEM` | Empty input → parse error |
| `TestParseRSAPublicKey_WrongPEMType` | Wrong PEM block type → parse error |

**Trigger:** any change to `internal/auth/jwt.go`

---

### S1-05 · Token cache — `internal/auth/token_cache_test.go`

**Purpose:** Two-level bearer token cache with cross-pod Redis pub/sub invalidation.

| Test | What it proves |
|---|---|
| `TestTokenCache_Validate_Hit` | Valid token in DB → `TokenInfo` returned |
| `TestTokenCache_Validate_Miss` | Unknown token → error |
| `TestTokenCache_Validate_L1Cache` | Second call hits L1 — DB queried only once |
| `TestTokenCache_Revoke_EvictsL1` | Revoke → L1 evicted → next Validate goes to DB |
| `TestTokenCache_Subscribe_CrossPodInvalidation` | Pub/sub message → L1 evicted on receiving pod |

**Trigger:** any change to `internal/auth/token_cache.go`

---

### S1-31 · Tenant middleware — `internal/auth/tenant_middleware_test.go`

**Purpose:** R-4b — validated tenant identity flows from auth token/JWT into context. Confirms
no header can override TenantID; no secret appears in errors; two tenants resolve independently.

| Test | What it proves |
|---|---|
| `TestBearerTenant_ValidToken` | TM-01: valid token with TenantID → 200, TenantID in context |
| `TestBearerTenant_MissingToken` | TM-02: no Authorization header → 401 |
| `TestBearerTenant_InvalidToken` | TM-03: unknown token → 401 |
| `TestBearerTenant_TokenWithoutTenant` | TM-04: valid token, empty TenantID → 403 |
| `TestBearerTenant_EmptyTenantID` | TM-05: empty string stored in DB → 403 |
| `TestBearerTenant_HeaderCannotOverride` | TM-06: X-Tenant-ID header ignored; TenantID from token only |
| `TestBearerTenant_TwoTenantsIndependent` | TM-07: alpha and bravo tokens resolve independently |
| `TestHS256Tenant_ValidJWTWithTenant` | TM-08: HS256 JWT with tenant_id claim → 200, TenantID in context |
| `TestHS256Tenant_JWTWithoutTenant` | TM-09: HS256 JWT without tenant_id → 403 |
| `TestHS256Tenant_MissingToken` | TM-10: no token on HS256TenantMiddleware → 401 |
| `TestTenantMiddleware_NoSecretInErrors` | TM-11: signing secret never appears in error responses |
| `TestValidateJWT_TenantIDRoundTrip` | TM-12: RS256 JWT carries TenantID through sign/validate cycle |
| `TestValidateJWT_NoTenantID` | TM-13: JWT without tenant_id → empty TenantID, not an error |
| `TestValidateHS256JWT_TenantIDRoundTrip` | TM-14: HS256 JWT carries TenantID through validate cycle |
| `TestTokenCache_TenantIDFlows` | TM-15: TenantID flows L1 miss → DB → L1 cache |

**Trigger:** any change to `internal/auth/middleware.go`, `internal/auth/jwt.go`,
`internal/auth/token_cache.go`, `internal/auth/pgx_querier.go` — also run S1-34 (`internal/admin/tenant_http_test.go`) which tests the wiring of BearerTenantMiddleware at the router level

---

### S1-41 · Component registry resolver — `internal/registry/resolver_test.go`

**Purpose:** Phase A of Application v2 — design-time component definition resolver with tenant isolation.
Verifies the two-path resolution (UUID fast path → portable ref fallback), tenant access rules
(builtin accessible to all; tenant-scoped only to owning tenant), and publish-pipeline constraints
(deprecated blocked at ResolveForPublish, allowed at Resolve for palette queries).

| Test | What it proves |
|---|---|
| `TestResolver_TenantOwnedDefinitionResolvesForOwner` | Tenant-scoped definition resolves for its owning tenant |
| `TestResolver_BuiltinResolvesForAnyTenant` | Builtin definition (scope=builtin) is accessible to any tenant ID |
| `TestResolver_NoCrossTenantResolution` | Tenant A cannot resolve a definition owned by tenant B → ErrNotFound |
| `TestResolver_ExactVersionResolution` | Version is forwarded to DAL; correct versioned definition returned |
| `TestResolver_MissingDefinitionReturnsErrNotFound` | Unknown definition → ErrNotFound |
| `TestResolver_DisabledDefinitionReturnsErrDisabled` | enabled=false → ErrDisabled regardless of scope |
| `TestResolver_DeprecatedDefinition_ResolveSucceeds` | Deprecated definition: Resolve succeeds (palette queries allowed to see deprecated) |
| `TestResolver_DeprecatedDefinition_ResolveForPublishReturnsErrDeprecated` | Deprecated definition: ResolveForPublish → ErrDeprecated (blocks new pins) |
| `TestResolver_UUIDFastPathHitsBeforeRef` | UUID provided + found → UUID result returned; ref lookup not used |
| `TestResolver_UUIDMissFallsThroughToRef` | UUID provided but not found → falls through to portable ref lookup |
| `TestResolver_ResolveForPublish_PublishedDefinitionSucceeds` | ResolveForPublish happy path: published definition resolves cleanly |
| `TestResolver_TwoTenantsIndependent` | Alpha and bravo tenants with same-named definitions resolve independently |

**Trigger:** any change to `internal/registry/resolver.go`, `internal/registry/pgx.go`, or `internal/registry/types.go`

---

### S1-42 · Application Definition CRUD — `internal/admin/definitions_test.go`

**Purpose:** Phase B — Application Definition draft CRUD with tenant isolation. Verifies the full
lifecycle of draft definitions: create, read, list, update, delete. Proves tenant isolation
(cross-tenant UUID guessing returns 404), application ownership checks (wrong app ID returns 404),
immutability constraints (published definition cannot be updated or deleted → 409), and
structural validation (duplicate instance_id → 422, malformed JSON → 400, secret_value key → 400).

| Test | What it proves |
|---|---|
| `TestDefinitions_CreateDraft_HappyPath` | POST /definitions returns 201 with id+revision |
| `TestDefinitions_CreateDraft_AppNotFound` | Wrong tenant → application sub-SELECT returns 0 rows → 404 |
| `TestDefinitions_CreateDraft_DuplicateInstanceID` | Duplicate instance_id in components → 422 |
| `TestDefinitions_CreateDraft_MalformedJSON` | Definition is not a JSON object → 400 |
| `TestDefinitions_GetDefinition_HappyPath` | GET /definitions/{def_id} returns 200 with body |
| `TestDefinitions_GetDefinition_WrongTenant` | UUID guessing across tenants → 404 |
| `TestDefinitions_ListDefinitions_ReturnsOrdered` | GET /definitions returns [] slice ordered by revision desc |
| `TestDefinitions_UpdateDraft_HappyPath` | PUT /definitions/{def_id} returns 200 updated:true |
| `TestDefinitions_UpdateDraft_PublishedConflict` | PUT on published definition → 409 |
| `TestDefinitions_UpdateDraft_WrongApp` | PUT with wrong appID → 404 |
| `TestDefinitions_DeleteDraft_HappyPath` | DELETE /definitions/{def_id} returns 204 |
| `TestDefinitions_DeleteDraft_PublishedConflict` | DELETE on published definition → 409 |

**Trigger:** any change to `internal/admin/definitions.go`, `internal/admin/service/definitions.go`,
or `internal/admin/dal/definitions.go`

---

### S1-43 · Application Definition Validate — `internal/admin/service/definitions_publish_test.go`

**Purpose:** Phase C — ValidateDefinition exercises registry resolution errors, structural errors,
protocol validation, dangling connections, and duplicate instance_ids in a component definition
without touching the database.

| Test | What it proves |
|---|---|
| `TestValidateDefinition_ValidDef_ReturnsValidTrue` | All components resolve → valid=true, no errors |
| `TestValidateDefinition_UnknownComponent_ReturnsNotFound` | Unknown component ref → valid=false, code=component_not_found |
| `TestValidateDefinition_DisabledComponent_ReturnsDisabled` | Disabled component → valid=false, code=component_disabled |
| `TestValidateDefinition_DeprecatedComponent_ReturnsDeprecated` | Deprecated component → valid=false, code=component_deprecated |
| `TestValidateDefinition_DuplicateInstanceID_ReturnsError` | Duplicate instance_id in components → valid=false, error reported |
| `TestValidateDefinition_DanglingConnection_ReturnsError` | Connection target not in instance_ids → valid=false, code=dangling_connection |
| `TestValidateDefinition_InvalidProtocol_ReturnsError` | Entry point protocol not in allowlist → valid=false, code=invalid_protocol |
| `TestValidateDefinition_EmbeddedSecret_ReturnsError` | secret_value key → valid=false (structural_error) |
| `TestValidateDefinition_DefinitionNotFound_ReturnsErrNotFound` | Missing defID → ErrNotFound (not a report) |
| `TestValidateDefinition_WrongTenant_ReturnsErrNotFound` | DAL returns pgx.ErrNoRows → ErrNotFound |

**Trigger:** any change to `internal/admin/service/publish.go`, `internal/admin/service/definitions.go`,
`internal/admin/dal/publish.go`, or `internal/registry/`

---

### S1-44 · Application Definition Publish — `internal/admin/service/definitions_publish_test.go`

**Purpose:** Phase C — PublishDefinition covers the full compile-and-publish pipeline: validation,
orchestrator projection upsert, entry_point projection upsert, stale deactivation, and the final
atomic DAL publish call. Tests use a fake registry and fake DAL — no PostgreSQL required.

| Test | What it proves |
|---|---|
| `TestPublishDefinition_Success_ReturnsPublishResult` | Happy path: result has correct definition_id + revision |
| `TestPublishDefinition_PublishedDefinition_ReturnsConflict` | Already-published def → ErrConflict, DAL not called |
| `TestPublishDefinition_ValidationFails_ReturnsValidationError` | Invalid components → ErrValidation, DAL not called |
| `TestPublishDefinition_OrchProjectionCreated` | Orchestrator row upserted with correct name/instance_id/config/component pin |
| `TestPublishDefinition_EPProjectionCreated` | Entry point row upserted with correct slug/type/orchestrator ID |
| `TestPublishDefinition_StaleProjectionsDeactivated` | DeactivateStaleOrchestrators + DeactivateStaleEntryPoints both called |
| `TestPublishDefinition_ToolConnectionWiresAgentIDs` | Tool connections from orchestrator to agent populate AllowedAgentIDs |
| `TestPublishDefinition_DelegationConnectionSetsDelegatable` | Delegation target orchestrator gets delegatable=true |
| `TestPublishDefinition_UpsertOrchError_AbortsPipeline` | DAL upsert error → pipeline aborts, PublishDefinition not called |
| `TestPublishDefinition_DefinitionNotFound_ReturnsErrNotFound` | Missing definition → ErrNotFound |
| `TestPublishDefinition_WrongTenantBlocked` | DAL returns pgx.ErrNoRows → ErrNotFound |
| `TestPublishDefinition_NoRegistry_SkipsResolution` | No registry wired → resolution skipped, publish succeeds |

**Trigger:** any change to `internal/admin/service/publish.go`, `internal/admin/dal/publish.go`,
`internal/admin/definitions.go`, `internal/admin/router.go`, or `internal/registry/`

---

### S1-45 · Admin registry handler (Phase D) — `internal/admin/registry_test.go`

**Purpose:** Phase D — RegistryHandler covers the GET /admin/component-definitions endpoint.
Tests use a fakeDB with no rows (empty result) and verify the handler returns 200 OK with
a JSON array (not null) even when no component definitions exist.

| Test | What it proves |
|---|---|
| `TestRegistryHandler_ListComponentDefinitions_ReturnsArray` | Happy path: empty table → 200 OK with `[]` JSON array, not null |

**Trigger:** any change to `internal/admin/registry.go`, `internal/admin/dal/registry.go`, or `internal/admin/router.go`

---

### S1-40 · Auth server (Go) — `internal/authserver/*_test.go`

**Purpose:** the Go replacement for the Python `them-auth-service`. Proves HS256 JWT issuance
is byte-compatible with what the Go bridge validates, bcrypt passwords verify, the login/me/refresh/
logout contract behaves like the Python service, and secrets never leak into config logs.

| Test | What it proves |
|---|---|
| `TestIssueAndVerifyAccessToken` | Access token round-trips; claims (sub/username/role/type=access) correct |
| `TestRoleExpiryOverride` | `roles.token_expiry` override honoured in `expires_in` |
| `TestRefreshTokenType` | Refresh token carries `type=refresh` |
| `TestVerifyRejectsWrongSecret` | Wrong HMAC secret → `ErrTokenSignature` |
| `TestVerifyRejectsExpired` | Past `exp` → `ErrTokenExpired` |
| `TestVerifyRejectsMalformed` | Non-3-segment / garbage → error |
| `TestBridgeCompatibility` | **Auth-server token validates under bridge `auth.ValidateHS256JWT`** with same secret |
| `TestHashTokenIsHexSHA256` | Token hash = lowercase hex SHA-256 (matches Python `hash_token`) |
| `TestPasswordRoundTrip` / `TestVerifyPassword*` | bcrypt verify; empty/garbled hash → false, no panic |
| `TestConfigValidate*` / `TestSafeStringMasksSecrets` / `TestDSN` | Env validation; secrets masked in `SafeString`; DSN format |
| `TestLogin*` (password/email/apikey/wrong/unknown/missing/dashboard-denied) | Full login matrix incl. dashboard_access gate (403) |
| `TestMeAndRefreshFlow` / `TestRefreshRejectsAccessToken` / `TestMeRejectsEmptyToken` | /me + /refresh semantics; access token rejected on refresh |
| `TestLogoutRevokesToken` | Logout blacklists token; subsequent /me → `ErrTokenRevoked` |
| `TestHTTP*` (login/me/refresh/logout/verify/validate/mirror/health) | End-to-end chi router: cookies set/cleared, `{detail}` errors, `/auth/*` Traefik mirror, forwardAuth headers, health/ready |

**Trigger:** any change to `internal/authserver/` (config, jwt, password, store, pgx, service,
handlers, router) or `cmd/auth-server/main.go`. Run `go test ./internal/authserver/...`.

---

### S1-32 · Tenant context — `internal/tenantctx/tenantctx_test.go`

**Purpose:** Typed context package for tenant identity — no stringly-typed key, correct error
types, parent-child isolation.

| Test | What it proves |
|---|---|
| `TestTenantCtx_RoundTrip` | TC-01: WithTenantID + TenantIDFromCtx returns correct ID |
| `TestTenantCtx_MissingTenant` | TC-02: empty context → ErrNoTenant |
| `TestTenantCtx_EmptyStringIsInvalid` | TC-03: WithTenantID("") → ErrInvalidTenant on retrieval |
| `TestTenantCtx_TwoTenantsIndependent` | TC-04: alpha and bravo in separate contexts — no cross-contamination |
| `TestTenantCtx_ChildOverrideDoesNotAffectParent` | TC-05: child context override does not mutate parent |
| `TestTenantCtx_MustPanicsOnMissing` | TC-06: MustTenantIDFromCtx panics when tenant absent |
| `TestTenantCtx_MustReturnsValue` | TC-07: MustTenantIDFromCtx returns value when present |
| `TestTenantCtx_StringKeyCannotOverride` | TC-08: raw string context key cannot retrieve typed tenant value |

**Trigger:** any change to `internal/tenantctx/tenantctx.go`

---

### S1-06 · Session — `internal/session/session_test.go`

**Purpose:** Session lifecycle with atomic Lua scripts — fixes Critical finding #1 (ghost-set bug).

| Test | What it proves |
|---|---|
| `TestStore_Register_StoresHashAndSets` | Register creates Hash + shadow key + Set membership |
| `TestStore_End_Cleanup` | End removes Hash, shadow key, and Set membership |
| `TestStore_Get_NotFound` | Get on unknown session → not-found error |
| `TestStore_CountEPSessions_PrunesGhosts` | Shadow key expired → ghost pruned from Set on next count |
| `TestStore_WriteHeartbeat_ReportsRealCount` | Heartbeat uses `atomic.LoadInt32` — not hardcoded 0 |
| `TestStore_SignalDisconnect_PubSub` | Disconnect signal published to correct Redis channel |
| `TestStore_ActiveSessionsCounter_Atomic` | Concurrent register/end → counter is race-safe |
| `TestStore_ListEPSessions` | `ListEPSessions` calls luaPruneAndList and returns live session IDs |
| `TestStore_ListAppSessions` | `ListAppSessions` calls luaPruneAndList with app set key |
| `TestStore_SignalDisconnect_ReturnsDelivered` | `SignalDisconnect` returns `(true, nil)` on success |

**Trigger:** any change to `internal/session/session.go`

---

### S1-07 · Event bus — `internal/event/bus_test.go`

**Purpose:** In-process fan-out bus — never blocks on slow consumers; terminal event guarantee (R-0 L-1 / OD-1).

| Test | What it proves |
|---|---|
| `TestPublish_specificTopic` | Event delivered to matching subscriber |
| `TestPublish_wrongTopic` | Event NOT delivered to non-matching subscriber |
| `TestWildcard` | `"*"` subscriber receives all topics |
| `TestSlowConsumer` | Full channel → event dropped, bus does not block |
| `TestUnsubscribe` | Unsubscribe closes channel, no further events delivered |
| `TestConcurrentPublish` | Concurrent publishes → no data race (run with `-race`) |
| `TestBus_TerminalEventDeliveredOnFullBuffer` | "done" event delivered via termCh even when evCh is full (R-0 OD-1) |
| `TestBus_TerminalEventDroppedIfTermChFull` | Second terminal event does not block publisher when termCh (cap 1) is full |
| `TestBus_TerminalEventAlsoRoutedToEvCh` | Terminal event appears in both evCh and termCh when evCh has capacity |

**Trigger:** any change to `internal/event/bus.go`

---

### S1-08 · Domain types — `internal/domain/domain_test.go`

**Purpose:** Compile-time guard that typed constants are non-empty strings.

| Test | What it proves |
|---|---|
| `TestRoleConstants` | `RoleUser`, `RoleAssistant`, `RoleTool`, `RoleSystem` all non-empty |
| `TestTaskStatusConstants` | All `TaskStatus*` constants non-empty |
| `TestRunStatusConstants` | All `RunStatus*` constants non-empty |

**Trigger:** any change to `internal/domain/domain.go`

---

### S1-09 · Run recorder — `internal/runrecorder/recorder_test.go`

**Purpose:** Run persistence SQL is correct — uses mock DB, no live Postgres needed.
Extended in Phase R-3 to cover file artifact persistence, size limit enforcement, filename
sanitization, and cross-run access denial.

| Test | What it proves |
|---|---|
| `TestCreateRun_callsCorrectSQL` | `INSERT INTO them.runs` with 8-arg signature (id, tenant_id, entry_point_slug, status, started_at, events_transport, goal, orchestrator_name); tenant_id is a plain string (NOT NULL) |
| `TestCreateRun_eventsTransportByMode` | events_transport derived from RunEventsMode: pubsub→"pubsub", dual/streams→"streams" (Phase 11c-B) |
| `TestCreateRun_explicitTransportOverridesMode` | non-empty `run.EventsTransport` overrides the configured mode |
| `TestUpdateRunStatus_withErrorMessage` | `UPDATE` sets `status` and `error` (column is "error", not "error_message") |
| `TestUpdateRunStatus_completed` | Completed run → empty error string |
| `TestRecordUsage_insertsCorrectly` | `INSERT INTO them.run_usage` correct args |
| `TestRecordStep_insertsCorrectly` | `INSERT INTO them.run_steps` correct args |
| `TestDBError_propagates` | DB error is wrapped and returned, not swallowed |
| `TestRecordArtifact_Success` | Valid artifact → INSERT issued, non-empty UUID returned |
| `TestRecordArtifact_ExactlyOneMB` | Data at exactly 1 MiB → succeeds (boundary inclusive) |
| `TestRecordArtifact_OverLimit` | Data at 1 MiB+1 → `ErrArtifactTooLarge`, no DB call |
| `TestRecordArtifact_SanitizesFilename` | `../../etc/passwd` filename → only `passwd` stored |
| `TestGetArtifact_WrongRun` | artifact_id from run A via run B URL → error (cross-run denied by query) |
| `TestGetArtifact_WrongArtifact` | non-existent artifact_id → error |
| `TestSanitizeFilename_PathTraversal` | directory components stripped; `../../etc/passwd` → `passwd` |
| `TestSanitizeFilename_Safe` | normal filenames preserved unchanged |
| `TestSanitizeFilename_HiddenFile` | `.htaccess` → `file.htaccess` (hidden file protection) |
| `TestMetadataEvent_HasNoFilePayload` | returned artifact ID does not contain raw file data |
| `TestCreateRun_writesTenantID` | R-4d fixup: TenantID written as plain string arg[1]; no *string nullable |
| `TestCreateRun_emptyTenantIDReturnsError` | R-4d fixup: empty TenantID → `ErrMissingTenantID` before any DB call (NOT NULL constraint) |
| `TestCreateRun_twoTenantsProduceDistinctRows` | R-4d: two different tenant UUIDs → two distinct plain-string arg[1] values |
| `TestCreateTask_insertsChildTaskRow` | CreateTask issues INSERT INTO them.tasks with delegated kind; correct 5-arg order (tenantID, runID, contextID, runID, agentSlug) |
| `TestCompleteTask_updatesState` | CompleteTask issues UPDATE them.tasks with 'completed' on success, 'failed' on error |
| `TestCompleteRootTask_updatesRootRow` | CompleteRootTask issues UPDATE WHERE kind='root' for the run, sets completed/failed state |

**Trigger:** any change to `internal/runrecorder/recorder.go` or `internal/runrecorder/pgx.go`

---

### S1-10 · LLM provider — `internal/llm/provider_test.go`

**Purpose:** Typed tool definitions + provider interface + streaming cancellation (fixes findings #8, #9).

| Test | What it proves |
|---|---|
| `TestMockProvider_streamsAllEventsInOrder` | Events delivered in sequence |
| `TestMockProvider_respectsContextCancellation` | Cancel context → stream stops, no goroutine leak |
| `TestToolDef_emptyNameReturnsError` | `ToolDef.Validate()` rejects empty name |
| `TestToolDef_emptyDescriptionReturnsError` | `ToolDef.Validate()` rejects empty description |
| `TestToolDef_validDoesNotReturnError` | Valid ToolDef passes validation |
| `TestMockProvider_emptyResponsesClosesChannelImmediately` | Empty response set → channel closed cleanly |

**Trigger:** any change to `internal/llm/provider.go`, `internal/llm/mock.go`, `internal/llm/anthropic.go`

---

### S1-11 · Agent registry — `internal/agentregistry/registry_test.go`

**Purpose:** Agent invocation routing + two-level per-tenant Redis cache + pub/sub invalidation.
Fully rewritten for SEC-03: Redis key is now `them:agents:registry:{tenant_id}`, L1 key is
`"{tenantID}:{slug}"`. Invalidation is per-tenant only — global eviction is impossible by design.

| Test | What it proves |
|---|---|
| `TestInvokeMock` | Mock adapter returns immediately without HTTP |
| `TestInvokeA2A` | A2A adapter sends correct JSON-RPC 2.0 request, extracts result |
| `TestCacheMissThenPopulate` | Cache miss → DB load → Redis populated |
| `TestPubSubInvalidation` | Pub/sub message on `them:agents:changed` → in-process cache cleared |
| `TestUnknownSlug` | Unknown agent slug → `ErrUnknownAgent` (typed sentinel) |
| `TestCacheMissThenPopulate_TenantScopedKey` | SEC-03: Redis key is `them:agents:registry:{tenantA}` — global key `them:agents:registry` does NOT exist |
| `TestTenantIsolation_SameSlug` | SEC-03: tenantA and tenantB with same slug → separate cache entries, no sharing |
| `TestTenantInvalidation_DoesNotCrossContaminate` | SEC-03: invalidating tenantA does NOT evict tenantB's cache entries |
| `TestCrossTenatLookup_ReturnsMiss` | SEC-03: tenantA cannot retrieve an agent registered under tenantB (returns ErrUnknownAgent) |
| `TestPubSubEmptyPayload_Ignored` | SEC-03: empty pub/sub payload → no eviction (guards against accidental global eviction) |

**Trigger:** any change to `internal/agentregistry/registry.go` or `internal/agentregistry/pgx_querier.go`

---

### S1-12 · WebSocket handler — `internal/ws/handler_test.go`

**Purpose:** WS handler migrated to `execution.Lifecycle` (R-5 Phase 3). Tests verify the full
Admit→Upgrade→Subscribe→Start→stream pipeline via fakes injected into `execution.NewLifecycleWithRecorder`.
Lifecycle.Admit runs BEFORE `upgrader.Upgrade`, so all pre-Admit errors return clean HTTP responses.
On upgrade failure, lc.Release cleans up gate/session state with a bounded 5-second timeout.
Execution Core Hardening (R-5.1): runs created as "admitted", transition to "running" only on Start success;
Release marks run "failed" when Start never ran (orphan-run prevention).

| Test | What it proves |
|---|---|
| `TestWS_Unauthenticated` | No token + token-mode EP → 401 before upgrade (Lifecycle.Admit returns Unauthorized) |
| `TestWS_AuthenticatedUpgrade` | Valid token → 101 Switching Protocols |
| `TestWS_MessageAndDone` | User message → token events → `{"type":"done"}` received |
| `TestWS_DisconnectEndsSession` | Client close → `session.Store.End` called via lc.Release |
| `TestWS_GateCapExceeded` | Gate returns `ErrCapExceeded` → 429 before WS upgrade; session never registered |
| `TestWS_GateAdmittedAndReleased` | Gate admitted → Check→Confirm called; Release called on session end |
| `TestWS_GateRollbackOnRegisterFailure` | `session.Register` fails inside Lifecycle.Admit (pre-upgrade) → HTTP 500; Gate.Rollback called |
| `TestWS_PublicEPNoTokenAllowed` | No token + AccessMode=public → 101 upgrade succeeds |
| `TestWS_TokenEPNoTokenRejected` | No token + AccessMode=token → 401 |
| `TestWS_AnonymousSessionGateTokenHashEmpty` | Anonymous session passes `TokenHash=""` to gate — no shared per-token rate-limit bucket |
| `TestWS_AnonymousSessionUserIDIsZero` | Anonymous session stores `UserID=0` in SessionInfo |
| `TestWS_AuthenticatedRequestToPublicEP` | Authenticated request to public EP succeeds |
| `TestWS_VoiceEPReturns501` | Voice EP → 501 via AdmitErrNotImplemented (pre-upgrade); gate and session never called |
| `TestWS_VoiceEPPublicReturns501` | Public voice EP → 501 before gate or session |
| `TestWS_TemporalPathUsedWhenEnabled` | `ExecuteWorkflow` called via Lifecycle.Start; client receives done event from run stream |
| `TestWS_ReplayUnavailableForwardedToClient` | `replay_unavailable` event forwarded to WS client (Phase 11c-C fix) |
| `TestWS_NoTemporalReturnsErrorEvent` | Temporal nil → Lifecycle.Start returns StartError → WS error event sent after upgrade |
| `TestWS_RunStoresTenantID` | R-5: CreateRun receives TenantID/ApplicationID from EPConfig; WorkflowInput identity fields overwritten by Lifecycle.Start |
| `TestWS_ClientTenantHeaderIgnored` | R-5: X-Tenant-ID header cannot override server-resolved TenantID from EPConfig |
| `TestWS_IDsAreUUIDv4` | All run/session/context IDs are UUID v4 (Python worker requires uuid.UUID() parsing) |
| `TestWS_AdmitBeforeUpgrade_EPNotFound` | EP not found → 404 HTTP response (not WS error frame) — confirms Admit runs before upgrade |
| `TestWS_UpgradeFailure_RunMarkedFailed` | R-5.1: Upgrade fails after Admit → Release marks run as failed (orphan prevention) |
| `TestWS_FirstMessageError_RunMarkedFailed` | R-5.1: Client disconnects before first message → Release marks run as failed |
| `TestWS_StartFailure_RunMarkedFailed` | R-5.1: Temporal Start fails → Release marks run as failed |

**Trigger:** any change to `internal/ws/handler.go` or `internal/execution/lifecycle.go`

---

### S1-13 · SSE handler — `internal/sse/handler_test.go`

**Purpose:** Server-Sent Events endpoint migrated to `execution.Lifecycle` (R-5). Tests cover the
full Admit→SSE headers→Start→stream pipeline via fakes injected into `execution.NewLifecycleWithRecorder`.
SSE headers are written AFTER Lifecycle.Admit succeeds — pre-Admit errors return clean HTTP codes.

| Test | What it proves |
|---|---|
| `TestSSEUnauthenticated` | No token + token-mode EP → 401 before SSE headers |
| `TestSSETokenEvents` | Valid auth + message → token events in SSE format |
| `TestSSEDoneClosesStream` | Done event → stream closed with run_id |
| `TestSSEGateCapExceeded` | Gate returns `ErrCapExceeded` → 429 before SSE headers |
| `TestSSEGateAdmittedAndReleased` | Gate admitted → Check→Confirm called; Release called on stream end |
| `TestSSEGateRollbackOnRegisterFailure` | `session.Register` fails → Gate.Rollback called; HTTP 500 (no SSE headers yet) |
| `TestSSEPublicEPNoTokenAllowed` | No token + AccessMode=public → 200 + SSE stream opened |
| `TestSSETokenEPNoTokenRejected` | No token + AccessMode=token → 401 |
| `TestSSEAnonymousSessionGateTokenHashEmpty` | Anonymous session passes `TokenHash=""` to gate — no shared per-token rate-limit bucket |
| `TestSSEAnonymousSessionUserIDIsZero` | Anonymous session stores `UserID=0` in SessionInfo |
| `TestSSEAuthenticatedRequestToPublicEP` | Authenticated request to public EP succeeds |
| `TestSSEVoiceEPReturns501` | Voice EP → 501 via AdmitErrNotImplemented; gate and session never called |
| `TestSSEVoiceEPPublicReturns501` | Public voice EP → 501 before gate or session |
| `TestSSETemporalPathUsedWhenEnabled` | `ExecuteWorkflow` called via Lifecycle.Start; client receives done event from run stream |
| `TestSSEReplayUnavailableForwardedToClient` | `replay_unavailable` event forwarded as SSE (Phase 11c-C fix) |
| `TestSSENoTemporalReturns503` | Temporal nil → Lifecycle.Start returns error → SSE error event (headers already sent) |
| `TestSSE_RunStoresTenantID` | R-5: CreateRun receives TenantID/ApplicationID from EPConfig; WorkflowInput fields overwritten by Lifecycle.Start |
| `TestSSE_ClientTenantHeaderIgnored` | R-5: X-Tenant-ID header cannot override server-resolved TenantID |
| `TestSSE_EventsTransportDerivedFromMode` | EventsTransport set on run from RunEventsMode (default → "pubsub") |
| `TestSSE_LifecycleCallSequence` | gate.Check + CreateRun + ExecuteWorkflow all called; gate.Release called on cleanup |
| `TestSSE_MissingMessage` | Missing ?message= → 400 before any Lifecycle call |
| `TestSSE_RunStreamSubscribedBeforeStart` | R-5.2: runEvents subscribe called BEFORE ExecuteWorkflow (bootstrap ordering invariant) |
| `TestSSE_IDsAreUUIDv4` | All run/session/context IDs are UUID v4 (Python worker requires uuid.UUID() parsing) |

**Trigger:** any change to `internal/sse/handler.go` or `internal/execution/lifecycle.go`

---

### S1-14 · A2A server — `internal/a2a/server_test.go`

**Purpose:** Unification refactor: A2A now uses `internal/execution.Lifecycle` for the shared admission pipeline. Tests verify auth, EPConfig resolution, access control, gate, session, Temporal dispatch, result mapping, wire format compliance, and correct injection of `*execution.Lifecycle` via test fakes.

| Test | What it proves |
|---|---|
| `TestA2A_MissingSlug_404` | Unknown `app_slug` → Lifecycle AdmitErrNotFound → HTTP 404 |
| `TestA2A_DisabledEP_403` | Disabled EP → Lifecycle AdmitErrForbidden → HTTP 403 |
| `TestA2A_BlockedToken_403` | Blocked application → Lifecycle AdmitErrForbidden → HTTP 403 |
| `TestA2A_MissingTokenOnTokenEP_401` | Token-mode EP + no bearer → Lifecycle AdmitErrUnauthorized → HTTP 401 |
| `TestA2A_InvalidToken_401` | Invalid/expired token + token-mode EP → Lifecycle AdmitErrUnauthorized → HTTP 401 |
| `TestA2A_PublicEP_NoToken_OK` | Public EP + no bearer → succeeds (HTTP 200) |
| `TestA2A_CapExceeded_429` | Gate ErrCapExceeded → Lifecycle AdmitErrCapExceeded → HTTP 429 |
| `TestA2A_TenantIDFromEPConfig` | TenantID in WorkflowInput comes from EPConfig, not request |
| `TestA2A_ClientCannotOverrideTenantID` | Request body injection attempt does not alter TenantID/AppID |
| `TestA2A_WorkflowInputHasTenantID` | WorkflowInput has correct TenantID, ApplicationID, RunID, ContextID, EntryPointSlug |
| `TestA2A_SessionRegistered` | session.Register called before ExecuteWorkflow; TenantID + AppID stored |
| `TestA2A_SessionEndedOnCompletion` | session.End called after workflow completes |
| `TestA2A_GateReleasedOnCompletion` | gate.Check, gate.Confirm, gate.Release all called on success |
| `TestA2A_GateRollbackOnRegisterFail` | gate.Rollback called if session.Register fails |
| `TestA2A_RPCResult_CompletedState` | Successful workflow → `state: completed` + text artifact (no `"kind"` field) |
| `TestA2A_RPCResult_HasTaskID` | Result includes non-empty `taskId` |
| `TestA2A_RPCError_WorkflowFailed` | Temporal error → sanitized `"internal error"`, not raw err.Error() |
| `TestA2A_ContextIDFromParams` | Caller-provided `contextId` used as ContextID in WorkflowInput |
| `TestA2A_ContextIDGeneratedIfAbsent` | Missing `contextId` → unique UUID generated per request |
| `TestA2A_DirectOrchNotUsed_TemporalCalledInstead` | Temporal ExecuteWorkflow called (no direct orch.Run path) |
| `TestA2A_CleanupOnGateFailure` | Gate denial → session not registered, gate.Release not called |
| `TestA2AUnknownMethod` | Unknown method → JSON-RPC error code `-32601` |
| `TestA2AMalformedJSON` | Unparseable body → JSON-RPC error code `-32700` |
| `TestA2A_TemporalNotConfigured_503` | Nil Temporal client → HTTP 503 |
| `TestA2A_ContextID_IsUUIDv4` | ContextID generated by Lifecycle is UUID v4 (36 chars with dashes) |
| `TestA2A_LifecycleInterface_Satisfied` | Compile-time: all test fakes satisfy transport.* and execution.* interfaces |
| `TestA2A_TemporalInterface_Satisfied` | Compile-time: fakeTemporal satisfies TemporalClientExecutor |

**Trigger:** any change to `internal/a2a/server.go`, `internal/execution/lifecycle.go`, or `internal/epconfig/pgx.go`

---

### S1-35 · Execution Lifecycle — `internal/execution/lifecycle_test.go`

**Purpose:** Shared execution lifecycle pipeline — unit tests for `Lifecycle.Admit`, `Lifecycle.Start`, and `Lifecycle.Release`. Covers security (TenantID injection prevention), error kinds, gate/session cleanup ordering, ID format requirements, and Execution Core Hardening (R-5.1): orphan-run prevention, fatal gate.Confirm, fail-fast production dependency validation.

| Test | What it proves |
|---|---|
| `TestLifecycle_HappyPath` | Admit → Start → Release all succeed; run created as "admitted", updated to "running" on Start, gate/session/recorder called in order |
| `TestLifecycle_EPNotFound` | Unknown EPSlug → AdmitErrNotFound (404); gate never checked |
| `TestLifecycle_TokenRequired_Absent` | Token-mode EP + no token → AdmitErrUnauthorized (401) |
| `TestLifecycle_TokenRequired_Invalid` | Token presented but invalid → AdmitErrUnauthorized (401) |
| `TestLifecycle_GateCapExceeded` | Gate ErrCapExceeded → AdmitErrCapExceeded (429); gate.Rollback NOT called |
| `TestLifecycle_SessionRegisterFails_GateRolledBack` | session.Register error → AdmitErrInternal; gate.Rollback called |
| `TestLifecycle_RecorderCreateRunFails` | recorder.CreateRun error → AdmitErrInternal; session.End + gate.Release called |
| `TestLifecycle_StartTemporalFails` | Start with erroring temporal → error; Release still cleans up |
| `TestLifecycle_ReleaseNilHandle_NoOp` | Release(nil) is a safe no-op |
| `TestLifecycle_TenantIDFromEPConfig_NotFromRequest` | Caller cannot override TenantID/AppID — always from EPConfig |
| `TestLifecycle_ContextIDProvidedByCaller_Preserved` | Caller-supplied ContextID is preserved in handle |
| `TestLifecycle_ContextIDGeneratedWhenEmpty` | Empty ContextID → UUID v4 generated; different from RunID |
| `TestLifecycle_PublicEP_NoToken_Admitted` | Public EP + no token → admission succeeds |
| `TestLifecycle_AllIDsAreUUIDv4` | RunID, ContextID, SessionID are all UUID v4 format |
| `TestLifecycle_Release_MarksRunFailed_WhenStartNeverCalled` | R-5.1: Admit creates run as "admitted"; Release without Start → UpdateRunStatus(failed) |
| `TestLifecycle_Release_DoesNotMarkFailed_AfterSuccessfulStart` | R-5.1: Start sets startedOK=true → Release skips the failed update |
| `TestLifecycle_ConfirmFatal_SessionCleanedUp` | R-5.1: gate.Confirm failure → session.End + gate.Release called; CreateRun skipped |
| `TestNewLifecycle_PanicsOnNilDeps` | R-5.1: NewLifecycle panics when epLoader/gate/sessions/recorder/temporal are nil |
| `TestLifecycle_AdmitCleanup_BothFailPathsCleanUp` | R-5.2: admitCleanup covers both Confirm-fail and CreateRun-fail paths (session+gate released in both) |
| `TestLifecycle_Start_UpdateRunStatus_AllRetriesExhausted_StartedOKSet` | R-5.2: all 3 UpdateRunStatus retries fail → startedOK=true; Release skips failed update (workflow is executing) |

**Trigger:** any change to `internal/execution/lifecycle.go`, `internal/execution/errors.go`, or `internal/execution/request.go`

---

### S1-15 · Admin API — `internal/admin/admin_test.go`

**Purpose:** CRUD correctness, cache invalidation, EP config cross-pod invalidation, Temporal signal wiring, token CRUD handler contract, session admin handler contract (Wave 5), LLM provider CRUD handler contract + MF-1 writeServiceError fix (Wave 7), Wave 8 PutRuntime + BulkDelete handler contract.
SQL query strings and scan helpers now live in `internal/admin/dal/`; the handler layer is tested here via fakeDB satisfying `dal.Querier`.

| Test | What it proves |
|---|---|
| `TestListAgentsEmptyArray` | Empty DB → returns `[]` not `null` (JSON safety) |
| `TestCreateAgent` | POST → 201 with `Location` header |
| `TestGetNonexistentAgent` | Unknown ID → 404 |
| `TestListRunsContextIDFilter` | `?context_id=` → correct SQL WHERE clause |
| `TestSignalRun` | POST `.../signal` → Temporal `SignalWorkflow` called with correct args |
| `TestRunsStats_Empty` | RS-1: GET /runs/stats, no DB rows → `{total:0, by_status:{}, total_cost_usd:"0.000000"}` |
| `TestRunsStats_WithData` | RS-2: GET /runs/stats with rows → totals and by_status aggregated correctly |
| `TestRunsGet_ReturnsDetail` | RD-1: GET /runs/{run_id} → RunDetail shape with steps/usage/children arrays (not null) |
| `TestRunsTasks_Empty` | RT-1: GET /runs/{run_id}/tasks, no rows → `[]` not null |
| `TestRunsTasks_WithData` | RT-2: GET /runs/{run_id}/tasks with rows → Task array with correct fields |
| `TestRunsArtifacts_Empty` | RA-1: GET /runs/{run_id}/artifacts, no rows → `[]` not null |
| `TestRunsArtifacts_WithData` | RA-2: GET /runs/{run_id}/artifacts with rows → Artifact array with parts parsed |
| `TestRunsRoute_StatsNotParsedAsRunID` | RO-1: GET /runs/stats → stats JSON not run-not-found (static route wins over /{run_id} wildcard) |
| `TestRunsCancel_Success` | RW-1: PATCH /runs/{run_id}/cancel with matching run → 200 Run JSON |
| `TestRunsCancel_NotFound` | RW-2: PATCH /runs/{run_id}/cancel run not found (both QueryRows return ErrNoRows) → 404 |
| `TestRunsDelete_Success` | RW-3: DELETE /runs/{run_id} → 204 No Content |
| `TestRunsDelete_NotFound` | RW-4: DELETE /runs/{run_id} run not found → 404 |
| `TestRunsBulkDelete_WithIDs` | RW-5: POST /runs/bulk-delete with IDs → 200 `{"deleted":1}` |
| `TestRunsBulkDelete_EmptyIDs` | RW-6: POST /runs/bulk-delete empty run_ids → 200 `{"deleted":0}` without DB hit |
| `TestUpdateEntryPoint_NoSlugChange_PublishesSlug` | PUT entry-point (no rename) → publishes `"{tenantID}:{slug}"` to `them:ep:config:changed` |
| `TestUpdateEntryPoint_SlugRename_PublishesBothSlugs` | PUT entry-point (rename) → publishes both `"{tenantID}:{old}"` and `"{tenantID}:{new}"` |
| `TestUpdateEntryPoint_SlugRename_OldSlugPublishedFirst` | Old slug published before new slug in rename path |
| `TestUpdateEntryPoint_OldSlugLookupFails_OnlyNewSlugPublished` | Old slug lookup fails → only new slug published; no error |
| `TestDeleteEntryPoint_PublishesSlug` | DELETE entry-point → fetches slug then publishes it |
| `TestUpdateApplication_PublishesAllEPSlugs` | PUT application → publishes all EP slugs for that app |
| `TestDeleteApplication_PublishesAllEPSlugs` | DELETE application → publishes all EP slugs for that app |
| `TestUpdateEntryPoint_NilCache_NoPanic` | nil cache → no panic (cache is optional) |
| `TestCreateEntryPoint_DoesNotPublish` | POST entry-point → no invalidation for new EP (nothing to evict) |
| `TestAdminRequiresSuperAdmin_AnonymousRejected` | Anonymous request (no JWT claims) to admin endpoint → 401; RequireSuperAdmin middleware is fail-closed |
| `TestCreateEntryPoint_InvalidEPType_Returns422` | POST entry-point with `ep_type="grpc"` → 422; no cache invalidation published |
| `TestUpdateEntryPoint_InvalidEPType_Returns422` | PUT entry-point with `ep_type="tcp"` → 422; no cache invalidation published |
| `TestCreateEntryPoint_ValidEPTypes_Accepted` | POST entry-point with each of `websocket`, `sse`, `voice` → 201 (3 subtests) |
| `TestUpdateEntryPoint_EmptyEPType_Allowed` | PUT entry-point with empty `ep_type` → 200 (partial update — keeps existing DB value) |
| `TestPatchAgentAliasesUpdate` | PATCH /agents/{id} is routed (not 405) |
| `TestPatchOrchestratorAliasesUpdate` | PATCH /orchestrators/{name} is routed (not 405) |
| `TestPatchApplicationAliasesUpdate` | PATCH /applications/{id} is routed (not 405) |
| `TestPatchEntryPointAliasesUpdate` | PATCH /applications/{id}/entry-points/{ep_id} is routed (not 405) |
| `TestListTokens_EmptyArray` | GET /tokens → `[]` not null |
| `TestListTokens_InvalidUserID_400` | GET /tokens?user_id=bad → 400 |
| `TestGetToken_NotFound` | GET /tokens/{id} with DB error → 404 |
| `TestCreateToken_MissingLabel_400` | POST /tokens without label → 400 |
| `TestDeleteToken_NotFound` | DELETE /tokens/{id} with pgx.ErrNoRows → 404 |
| `TestListSessions_NeitherParam_400` | GET /sessions (no query params) → 400 |
| `TestListSessions_BothParams_400` | GET /sessions?app_id=a&ep_slug=b → 400 |
| `TestListSessions_ByAppID_ReturnsEmpty` | GET /sessions?app_id=x → `{"sessions":[],"count":0}` |
| `TestListSessions_ByEPSlug_ReturnsEmpty` | GET /sessions?ep_slug=x → `{"sessions":[],"count":0}` |
| `TestDisconnectSession_NotFound` | POST /sessions/{id}/disconnect, Get returns error → 404 |
| `TestDisconnectSession_Success` | POST /sessions/{id}/disconnect, live session → 200 `{"signal_delivered":true}` |
| `TestGetMonitoringConfig_NoRow_ReturnsDefaults` | GET /monitoring-config, no DB row → 200 with defaults (heatmap_low=1, stats_window_seconds=300) |
| `TestPutMonitoringConfig_Valid_Returns200` | PUT /monitoring-config with valid body → 200, returned values match input |
| `TestPutMonitoringConfig_BadThresholds_Returns422` | PUT /monitoring-config with heatmap_low>heatmap_high → 422 |
| `TestPutMonitoringConfig_BadJSON_Returns400` | PUT /monitoring-config with non-JSON body → 400 |
| `TestGetLLMRouting_NoRow_ReturnsDefaults` | GET /llm-providers/routing/config, no DB row → 200 with defaults (anthropic, claude-sonnet-4-6, null fallbacks) |
| `TestPutLLMRouting_Valid_Returns200` | PUT /llm-providers/routing/config with valid body → 200, returned values match input |
| `TestPutLLMRouting_BadJSON_Returns400` | PUT /llm-providers/routing/config with non-JSON body → 400 |
| `TestLLMProvidersHandler_List_200` | GET /llm-providers → 200, JSON array (empty = `[]` not null) |
| `TestLLMProvidersHandler_List_WithProviders` | GET /llm-providers → 200, provider array; no-key provider shows `api_key_set=false`, `api_key_masked=null` |
| `TestLLMProvidersHandler_Create_201` | POST /llm-providers valid body → 201 with Location header containing new id |
| `TestLLMProvidersHandler_Create_400_MissingName` | POST /llm-providers without name → 400 |
| `TestLLMProvidersHandler_Create_409_DuplicateName` | POST /llm-providers with DB error → non-200 response (409 path covered by service tests) |
| `TestLLMProvidersHandler_Get_200` | GET /llm-providers/{id} found → 200 with provider JSON |
| `TestLLMProvidersHandler_Get_404` | GET /llm-providers/{id} with pgx.ErrNoRows → 404 |
| `TestLLMProvidersHandler_Get_BadID` | GET /llm-providers/notanumber → 400 |
| `TestLLMProvidersHandler_Patch_200` | PATCH /llm-providers/{id} partial update → 200 with updated JSON |
| `TestLLMProvidersHandler_Patch_404` | PATCH /llm-providers/{id} with pgx.ErrNoRows → 404 |
| `TestLLMProvidersHandler_Patch_APIKeyAbsent` | PATCH without api_key field → APIKeyPresent=false; existing key unchanged |
| `TestLLMProvidersHandler_Patch_APIKeyExplicitNull` | PATCH with `"api_key":null` → APIKeyPresent=true; service clears the key |
| `TestLLMProvidersHandler_Delete_204` | DELETE /llm-providers/{id} found → 204 No Content |
| `TestLLMProvidersHandler_Delete_404` | DELETE /llm-providers/{id} with pgx.ErrNoRows → 404 |
| `TestLLMProvidersHandler_NoPlaintextAPIKeyInResponse` | GET response body never contains `api_key_encrypted` or plaintext key material |
| `TestWriteServiceError_ErrConflict_Returns409` | POST /llm-providers route is reachable (MF-1 fix: ErrConflict→409 wired in writeServiceError) |
| `TestLLMProvidersHandler_RequiresSuperAdmin` | Anonymous request to /llm-providers → 401 via RequireSuperAdmin middleware |
| `TestPutRuntime_Handler_200` | W8-H1: PUT /applications/{id}/runtime → 200 with config |
| `TestPutRuntime_Handler_404_NotFound` | W8-H2: ExecReturning pgx.ErrNoRows → 404 |
| `TestPutRuntime_Handler_400_BadJSON` | W8-H3: non-JSON body → 400 |
| `TestPutRuntime_Handler_NilSlicesAsEmptyArrays` | W8-H4: nil blocked_tokens/blocked_user_ids serialize as `[]` not `null` |
| `TestBulkDelete_Handler_200` | W8-H5: POST /applications/bulk-delete → 200 `{"deleted":N}` |
| `TestBulkDelete_Handler_400_BadJSON` | W8-H6: non-JSON body → 400 |
| `TestBulkDelete_Handler_400_TooManyIDs` | W8-H7: 201 IDs → 400 (service.ErrValidation) |
| `TestBulkDelete_RouteNotMaskedByIDParam` | W8-H8: bulk-delete route registered before /{id} — empty list → 200, not 404/405 |

**Trigger:** any change to `internal/admin/` (any file) OR `internal/admin/dal/` (any file)

---

### S1-36 · Agent action endpoints — `internal/admin/agents_actions_test.go`

**Purpose:** Wave 8 agent Store actions: discover (card fetch + slug generation), test (latency probe), security-scan (202 accepted + job_id). Uses httptest mock HTTP servers to exercise the full handler path without network calls to real agents.

| Test | What it proves |
|---|---|
| `TestDiscover_Success` | Mock server returns valid agent card → ok=true, display_name, suggested_slug, supports_streaming, skills array |
| `TestDiscover_ConnectionFailure` | Mock server not reachable → ok=false, detail non-empty |
| `TestDiscover_NonJSON` | Mock server returns non-JSON 200 → ok=false, detail contains "parse card JSON" |
| `TestTest_Success` | Mock server returns valid card with 2 skills → ok=true, latency_ms≥0, detail contains "2 skills" |
| `TestTest_Failure` | Mock server returns 503 → ok=false, detail contains "503" |
| `TestTest_NotFound` | DB returns pgx.ErrNoRows → 404 |
| `TestSecurityScan_NoScanner` | Scanner agent not in DB → 503 with "Security scanner agent not registered" |
| `TestSecurityScan_Accepted` | Target + scanner both in DB → 202 with non-empty job_id and correct agent_id |

**Trigger:** any change to `internal/admin/agents.go`, `internal/admin/classify.go`, `internal/admin/scanjob.go`, or `internal/admin/dal/agents.go`

---

### S1-16 · Rate limiter — `internal/ratelimit/limiter_test.go`

**Purpose:** Redis INCR rate limiting per token and per application.

| Test | What it proves |
|---|---|
| `TestCheckTokenAllowed` | First request under limit → allowed |
| `TestCheckTokenDenied` | Request over limit → denied |
| `TestCheckAppDifferentMinuteResets` | New minute bucket → counter resets |

**Trigger:** any change to `internal/ratelimit/limiter.go`

---

### S1-17 · Runtime admission gate — `internal/gate/gate_test.go`

**Purpose:** Reservation TTL pattern (Check → Register → Confirm contract), atomic Lua admission, queue protocol (BLPop signal channel, re-compete on wake), Rollback for immediate slot recovery on Register failure. Covers all admission/rejection/queue/cancellation paths and the ghost auto-cleanup guarantee.

| Test | What it proves |
|---|---|
| `TestAdmitNoLimits` | No limits → admitted; EP + app Set membership + shadow keys written |
| `TestEPCapExceeded` | EP cap=1, second session → `ErrCapExceeded` |
| `TestAppCapExceeded` | App cap=1, second session → `ErrCapExceeded` |
| `TestRateLimit` | RPM=1, second request in same minute → `ErrRateLimited` |
| `TestNoAppID` | Empty AppID → only EP Set written, no app Set writes |
| `TestGhostPruning` | Ghost member (no shadow key) pruned; cap check counts correctly |
| `TestQueueDisabledOnCapExceeded` | QueueTimeout=0 + cap full → `ErrCapExceeded` immediately |
| `TestQueueTimeout` | QueueTimeout>0, BLPOP times out → `ErrQueueFull` |
| `TestConfirmExtendsShadow` | Confirm refreshes shadow keys from ReservationTTL (10s) to full ShadowTTL (90s) |
| `TestRollbackRemovesAdmission` | Rollback SREMs Set entry + DELs shadow; slot freed immediately for next session |
| `TestReservationExpiryAutoCleanup` | Shadow expires (simulates crash between Check and Confirm) → ghost pruned on next admission |
| `TestQueueWakeUpIsACompete` | Queued session wakes but slot taken by concurrent session → `ErrCapExceeded`, not re-queued |
| `TestMultipleWaitersCompete` | Two waiters, two signals, one slot → exactly one admitted, one `ErrCapExceeded` |
| `TestCancellationWhileQueued` | Context cancelled while waiting in queue → error returned without deadlock |
| `TestReleaseNoWaiters` | Release with no waiters → no panic, no error (idempotent) |
| `TestRollbackWakesQueuedSession` | Rollback on Register failure → Release called → queued session wakes and wins the slot |

**Trigger:** any change to `internal/gate/gate.go`

---

### S1-19 · Auth Redis adapter — `internal/cache/auth_adapter_test.go`

**Purpose:** Compile-time interface satisfaction check — `*AuthRedisClient` implements `auth.RedisClient`.
The behavioural contract of `auth.RedisClient` is exercised in S1-05 via `mockRedis`.

| Test | What it proves |
|---|---|
| `TestAuthRedisClient_ImplementsInterface` | `*AuthRedisClient` satisfies `auth.RedisClient` at compile time |

**Trigger:** any change to `internal/cache/auth_adapter.go`

---

### S1-20 · RunStream Redis adapter — `internal/cache/runstream_adapter_test.go`

**Purpose:** Compile-time interface satisfaction check — `*RunStreamRedisClient` implements `runstream.Subscriber`.
The Streams reader adapter `*RunStreamerRedisClient` (`internal/cache/runstreamer_adapter.go`) satisfies
`runstream.RedisStreamer` via a compile-time assertion in that file; exercised end-to-end by S1-24 integration.
The Streams writer adapter `*RunStreamerWriterRedisClient` (`internal/cache/runstreamer_writer_adapter.go`)
satisfies `runstream.StreamPublisher` via a compile-time assertion in that file (R-2C Phase 3).

| Test | What it proves |
|---|---|
| `TestRunStreamRedisClient_ImplementsInterface` | `*RunStreamRedisClient` satisfies `runstream.Subscriber` at compile time |

**Trigger:** any change to `internal/cache/runstream_adapter.go`, `internal/cache/runstreamer_adapter.go`, or `internal/cache/runstreamer_writer_adapter.go`

---

### S1-21 · Run stream — `internal/runstream/stream_test.go`

**Purpose:** Redis pub/sub stream with reconnect. Go pre-generates runID and subscribes to
`them:dash:run:{runID}:tokens` before workflow start. Terminal events close the output channel
immediately. Transient Redis disconnects trigger bounded exponential backoff reconnect (up to
`ReconnectMaxAttempts=6`, `ReconnectBaseDelay=100ms` → `ReconnectMaxDelay=3200ms`).
At-most-once delivery: events missed during a reconnect gap are lost, not replayed.

| Test | What it proves |
|---|---|
| `TestStream_ForwardsMessages` | Messages forwarded with correct Type; channel closes on terminal event |
| `TestStream_TerminalDoneClosesImmediately` | `"done"` event closes output channel without waiting for source to close |
| `TestStream_TerminalErrorClosesImmediately` | `"error"` event (max_iterations=0 path) also closes immediately |
| `TestStream_ContextCancel` | Context cancelled → output closes promptly |
| `TestStream_ReconnectOnSourceClose` | Source closes without terminal → reconnect → resumes delivery from second subscription |
| `TestStream_ContextCancelDuringBackoff` | ctx cancel during backoff wait → clean exit, no further attempts |
| `TestStream_ReconnectExhaustionEmitsOneError` | All 6 attempts fail → exactly one synthetic `error` event emitted |
| `TestStream_NoDuplicateTerminalEvent` | Second terminal in source not delivered — Stream already closed after first |
| `TestStream_NoGoroutineLeak` | Goroutine exits cleanly after terminal event path |
| `TestStream_TerminalAfterReconnectNoFurtherAttempts` | Terminal event after reconnect stops further reconnect attempts |

**Prometheus counters exposed:**
- `them_runstream_disconnects_total`
- `them_runstream_reconnect_attempts_total`
- `them_runstream_reconnect_success_total`
- `them_runstream_reconnect_failure_total`

**Trigger:** any change to `internal/runstream/stream.go`

---

### S1-23 · Run stream Streamer (Redis Streams read/replay + publisher) — `internal/runstream/streamer_test.go`, `dispatcher_test.go`, `publisher_test.go`

**Purpose:** Phase 11c-B durable event delivery. `StreamFromRedis` replays history from a
client-supplied `last_event_id` via XRANGE, then transitions to live XREAD BLOCK using a
**continuous cursor** (resume from the last replayed entry ID, not `$`) so no entry is dropped
at the replay→live boundary. `Dispatcher` picks Pub/Sub vs Streams from `RUN_EVENTS_MODE` × the
run's `events_transport` value — never inferred from key existence or timing.
`PublishEvent` (R-2C Phase 3) writes events to Redis Streams cross-process; `publisher_test.go`
verifies key format, JSON structure, nil-safety, and round-trip compatibility with `decodeEntry`.

**Streamer tests (`streamer_test.go`):**

| Test | What it proves |
|---|---|
| `TestStreamFromRedis_ReplayOnly` | 5 XRANGE entries then empty → 5 events, channel closes |
| `TestStreamFromRedis_LiveOnly` | empty XRANGE → XREAD delivers 3 events |
| `TestStreamFromRedis_ReplayToLive` | 3 replay + 2 live = 5, in order, no duplicates |
| `TestStreamFromRedis_ContinuousCursor` | first live XREAD resumes from last replayed entry ID, not `$` |
| `TestStreamFromRedis_ReplayUnavailable` | trimmed `last_event_id` → synthetic `replay_unavailable` first, then resume from oldest |
| `TestStreamFromRedis_TerminalClosesChannel` | `"done"` closes channel; entry after it not delivered |
| `TestStreamFromRedis_AllTerminalTypes` | all 5 terminal types (done/error/canceled/terminated/timed_out) close the channel |
| `TestStreamFromRedis_ContextCancelStops` | ctx cancel during live block → goroutine exits, channel closes |
| `TestStreamFromRedis_MultiPodSafety` | two concurrent readers for the same run each keep their own cursor |

**Dispatcher tests (`dispatcher_test.go`):**

| Test | What it proves |
|---|---|
| `TestDispatcher_PubsubMode_AlwaysPubsub` | mode=pubsub → Pub/Sub regardless of events_transport |
| `TestDispatcher_DualMode_StreamsRun` | mode=dual + events_transport=streams → Streams |
| `TestDispatcher_DualMode_LegacyRun` | mode=dual + events_transport=pubsub → Pub/Sub (legacy run) |
| `TestDispatcher_StreamsMode_StreamsRun` | mode=streams + events_transport=streams → Streams |
| `TestDispatcher_StreamsMode_LegacyRow` | mode=streams + events_transport=pubsub → Pub/Sub (legacy row, not forced) |
| `TestDispatcher_PubsubMode_ModeTakesPrecedence` | mode=pubsub + events_transport=streams → Pub/Sub (mode wins) |

**Prometheus metrics exposed (`metrics.go`):**
- `them_runstream_xadd_total`
- `them_runstream_xadd_errors_total`
- `them_runstream_replay_sessions_total`
- `them_runstream_replay_events_total`
- `them_runstream_replay_unavailable_total`
- `them_runstream_mode` (gauge: 0=pubsub, 1=dual, 2=streams)

**Publisher tests (`publisher_test.go` — R-2C Phase 3):**

| Test | What it proves |
|---|---|
| `TestPublishEvent_WritesCorrectKey` | Key format is `them:dash:run:{runID}:stream` |
| `TestPublishEvent_WritesDataField` | `"data"` field is valid JSON with `type`, `run_id`, `context_id`; payload fields preserved |
| `TestPublishEvent_NilPublisher_NoPanic` | nil publisher → no call, no panic |
| `TestPublishEvent_CompatibleWithDecodeEntry` | publish token+done, read back via `StreamFromRedis` — round-trip types and payload fields match |
| `TestPublishEvent_EmptyRunID_NoWrite` | Empty RunID → XAdd still called; filtering is caller's responsibility (worker main skips) |
| `TestPublishEvent_XAddError_DoesNotPanic` | XAdd error is tolerated — no panic |

**Integration (`streamer_integration_test.go`, `//go:build integration`):** writes real events to a
Redis stream via XADD, verifies replay + live delivery + terminal close against a live Redis.

**Trigger:** any change to `internal/runstream/streamer.go`, `publisher.go`, `dispatcher.go`, `metrics.go`, `streamid.go`, `internal/cache/runstreamer_adapter.go`, or `internal/cache/runstreamer_writer_adapter.go`

---

### S1-22 · Run reconciler — `internal/reconciler/reconciler_test.go`

**Purpose:** Temporal-backed run reconciliation. Sweeps `them.runs` for rows stuck in
`status='running'` and reconciles against Temporal's authoritative `DescribeWorkflowExecution`
response. PostgreSQL advisory lock prevents duplicate concurrent sweeps. Dry-run mode emits
no DB writes. Safe NotFound policy: no DB update on 404 (protects Python-native runs and
history-expired rows). Status mapping per ADR-002.

| Test | What it proves |
|---|---|
| `TestReconciler_FreshRunSkipped` | Rows excluded by StaleAfter produce no Temporal call and no DB update |
| `TestReconciler_TemporalRunningLeaveUnchanged` | RUNNING workflow → no DB update |
| `TestReconciler_CompletedUpdatesStatus` | COMPLETED → `completed` |
| `TestReconciler_FailedUpdatesStatus` | FAILED → `failed` |
| `TestReconciler_CanceledUpdatesStatus` | CANCELED → `canceled` (single-L canonical spelling) |
| `TestReconciler_TerminatedMapsToStopped` | TERMINATED → `stopped` (ADR-002: operator stop, not failure) |
| `TestReconciler_TimedOutMapsToFailed` | TIMED_OUT → `failed` (ADR-002: no timed_out in schema) |
| `TestReconciler_NotFoundNoDestructiveUpdate` | NotFound from Temporal → no DB write |
| `TestReconciler_TemporalUnavailableNoDBUpdate` | Temporal transient error → no DB write |
| `TestReconciler_AdvisoryLockPreventsDoubleSweep` | Second reconciler instance skips sweep when lock is held |
| `TestReconciler_IdempotentUpdate` | Repeated reconciliation of same run writes same payload (DB idempotency via WHERE status='running') |
| `TestReconciler_DryRunNoWrites` | DryRun=true → no Exec calls, no DB updates |
| `TestReconciler_ContinuedAsNewNoUpdate` | CONTINUED_AS_NEW → no DB update (new execution is active) |
| `TestMapTemporalStatus` | All 8 Temporal status enum values map to expected DB statuses |
| `TestIsNotFound` | gRPC codes.NotFound detected; Unavailable and generic errors rejected |

**Prometheus counters exposed:**
- `them_reconciler_scanned_total`
- `them_reconciler_unchanged_total`
- `them_reconciler_updated_total`
- `them_reconciler_notfound_total`
- `them_reconciler_errors_total`
- `them_reconciler_dryrun_total`

**Trigger:** any change to `internal/reconciler/reconciler.go`

---

### S1-18 · EP config loader — `internal/epconfig/epconfig_test.go`

**Purpose:** Entry point and application runtime configuration resolution from DB — precedence rules, fail-closed policy, in-process TTL cache, cache invalidation, cross-pod pub/sub eviction. The single typed model shared by both WS and SSE handlers.

| Test | What it proves |
|---|---|
| `TestLoad_EPMaxConcurrentSessions` | `entry_points.max_concurrent_sessions` → `EPMaxConcurrent` |
| `TestLoad_AppMaxConcurrentSessions` | `runtime_config.max_concurrent_sessions` → `AppMaxConcurrent` |
| `TestLoad_BothLimitsSet` | EP and app limits are independent fields, both resolved correctly |
| `TestLoad_RateLimitRPM` | `runtime_config.rate_limit_rpm` → `RateLimitRPM` |
| `TestLoad_QueueTimeout` | `entry_points.queue_timeout_seconds` → `QueueTimeout` as duration |
| `TestLoad_NullQueueTimeout` | NULL `queue_timeout_seconds` → 0 (no queue) |
| `TestCheckAccess_DisabledEP` | `EPEnabled=false` → `ErrDisabled` (fail-closed 403) |
| `TestCheckAccess_DisabledApp` | `AppEnabled=false` → `ErrDisabled` (fail-closed 403) |
| `TestLoad_PublicEP` | `access_policy.mode=public` → `AccessModePublic` |
| `TestLoad_AuthenticatedEP` | `access_policy.mode=token` → `AccessModeToken` |
| `TestCheckAccess_BlockedToken` | Token hash on block-list → `ErrBlocked` (fail-closed 403) |
| `TestCheckAccess_BlockedUserID` | User ID on block-list → `ErrBlocked` (fail-closed 403) |
| `TestCheckAccess_NotBlocked` | Token and user NOT on block-lists → no error |
| `TestLoad_EPNotFound` | No EP row for slug → `ErrNotFound` |
| `TestLoad_DBUnavailable` | Any non-ErrNotFound DB error → `ErrDBUnavailable` (fail-closed 503) |
| `TestLoad_MalformedRuntimeConfig` | Invalid JSONB → treated as `{}` (unlimited), no error returned |
| `TestLoad_NullAndZeroLimits` | NULL/0 limits → 0 = unlimited for all fields |
| `TestLoad_NegativeLimitsTreatedAsUnlimited` | Negative limits clamped to 0 (unlimited) |
| `TestLoad_CacheHit` | Second `Load` for same slug → DB called only once |
| `TestLoad_DisabledEPNotCached` | Disabled EP never cached → DB queried every call |
| `TestInvalidate_EvictsEntry` | `Invalidate(tenantID, slug)` → next `Load(tenantID, slug)` re-queries DB (tenant-scoped) |
| `TestInvalidateApp_EvictsAppEntries` | `InvalidateApp(appID)` → only EPs for that app evicted |
| `TestLoad_MissingAccessPolicyDefaultsToToken` | NULL `access_policy` → defaults to `"token"` auth |
| `TestLoad_AppIDPropagated` | `AppID` from DB propagated correctly to `EPConfig.AppID` |
| `TestSubscribe_MessageEvictsCache` | Pub/sub message `"{tenantID}:{slug}"` → cache evicted; next Load re-queries DB (tenant-scoped payload) |
| `TestLoad_TTLFallback_NoSubscriber` | Without subscriber, fresh entry is cached (TTL not yet expired) |

**Trigger:** any change to `internal/epconfig/epconfig.go` or `internal/epconfig/pgx.go`

---

### S1-25 · Admin service layer — `internal/admin/service/service_test.go`

**Purpose:** Unit tests for the admin service layer. Covers business logic in isolation using fakes
for all dependencies (Dal, Cache, Temporal). Verifies default application, validation, cache
invalidation, and error mapping — without any real DB, Redis, or Temporal.

| Test | What it proves |
|---|---|
| `TestAgentService_Create_Defaults` | Missing transport/MaxConcurrency/MaxRetries/TimeoutSeconds → defaults applied (a2a_async, 5, 2, 30) |
| `TestAgentService_Create_MissingSlug_Validation` | Missing slug → `ErrValidation` |
| `TestAgentService_Create_MissingDisplayName_Validation` | Missing display_name → `ErrValidation` |
| `TestAgentService_Create_EnabledFalse_Respected` | `enabled=false` passed in → DAL called with `enabled=false` (not default-overridden) |
| `TestAgentService_Update_ReappliesMaxConcurrencyDefault` | `MaxConcurrency=0` on update → defaults to 5 |
| `TestAgentService_Create_InvalidatesRegistry` | Successful create → `them:agents:registry` deleted from cache |
| `TestTokenService_Create_GeneratesHashAndReturnsPlaintext` | Create generates crypto/rand token, stores hash, returns plaintext in Plaintext field |
| `TestTokenService_Create_OrchMissing_NotFound` | orchID set but not in DB → `ErrNotFound` |
| `TestTokenService_Create_NoOrch_SkipsExistsCheck` | nil orchID → OrchestratorExists never called |
| `TestTokenService_Update_InvalidatesByHash` | Update success → cache Del + Publish called with correct hash |
| `TestTokenService_Update_Missing_NotFound` | Update with pgx.ErrNoRows → `ErrNotFound` |
| `TestTokenService_Delete_InvalidatesByHash` | Delete success → cache Del + Publish called with correct hash |
| `TestTokenService_NilCache_NoPanic` | nil cache on Create/Update/Delete → no panic |
| `TestTokenService_List_ForwardsUserFilter` | List forwards optional userID to DAL |
| `TestSessionAdmin_ListByApp_SkipsNotFound` | Ghost session IDs (Get returns error) silently dropped |
| `TestSessionAdmin_List_ReturnsEmptySliceNotNil` | Empty session set → non-nil `[]` |
| `TestSessionAdmin_Disconnect_NotFound` | Get returns error → `ErrNotFound` |
| `TestSessionAdmin_Disconnect_Delivered` | Get succeeds + SignalDisconnect → `(true, nil)` |
| `TestAgentService_NilCache_NoPanic` | nil cache → no panic (cache is optional) |
| `TestOrchService_Create_Defaults` | Missing MaxIterations/HistoryWindow → defaults applied (10, 20); enabled defaults to true |
| `TestOrchService_Create_MissingName_Validation` | Missing name → `ErrValidation` |
| `TestOrchService_Create_InvalidatesCache` | Successful create → `them:orchestrators:{name}` deleted from cache |
| `TestOrchService_Delete_InvalidatesCache` | Delete → `them:orchestrators:{name}` deleted from cache |
| `TestAppService_Create_MissingName_Validation` | Missing name → `ErrValidation` |
| `TestAppService_CreateEntryPoint_InvalidType_Unprocessable` | `ep_type="grpc"` → `ErrUnprocessable` |
| `TestAppService_CreateEntryPoint_ValidTypes` | All 5 valid EP types (websocket, sse, voice, webrtc, a2a) → no error |
| `TestAppService_UpdateEntryPoint_OldSlugBeforeNew` | Rename: old `"{tenantID}:{slug}"` published before new — critical ordering; `UpdateEntryPoint` now takes `tenantID` for fallback |
| `TestAppService_UpdateEntryPoint_InvalidType_Unprocessable` | `ep_type="tcp"` on update → `ErrUnprocessable` |
| `TestAppService_DeleteEntryPoint_PublishesSlug` | Delete EP → `"{tenantID}:{slug}"` published to invalidation channel (tenant-scoped) |
| `TestAppService_Update_InvalidatesAppEPs` | Update app → all `"{tenantID}:{slug}"` pairs published (uses `ListEPTenantSlugsForApp`) |
| `TestRunService_Signal_BuildsWorkflowID` | `Signal` constructs `"ctx-{contextID}"` workflow ID |
| `TestRunService_Signal_TemporalNil_Unavailable` | nil Temporal → `ErrTemporalUnavailable` |
| `TestRunService_Signal_DBError_NotNotFound` | Non-pgx DB error → returned as-is, not mapped to ErrNotFound |
| `TestRunService_List_ForwardsParams` | `List` forwards contextID and limit to DAL |
| `TestGetMonitoring_NoRow_ReturnsDefaults` | No DB row → 8 fields returned at Python-identical defaults |
| `TestGetMonitoring_StoredRow_MergesOverDefaults` | Partial JSONB row → stored fields overwrite defaults; absent keys stay at default |
| `TestGetMonitoring_DALError_Propagates` | DAL error → wrapped and returned |
| `TestPutMonitoring_ValidInput_Upserts` | Valid MonitoringConfig → DAL UpsertConfig called with `config_key="monitoring"` and correct JSON |
| `TestPutMonitoring_InvalidHeatmapOrder_ReturnsValidationError` | heatmap low>medium → ErrUnprocessable |
| `TestPutMonitoring_InvalidEdgeOrder_ReturnsValidationError` | edge thin>medium → ErrUnprocessable |
| `TestGetLLMRouting_NoRow_ReturnsDefaults` | No DB row → defaults (anthropic, claude-sonnet-4-6, nil fallbacks) |
| `TestGetLLMRouting_StoredRow_Returned` | Stored row → all fields including fallback_provider/fallback_model returned |
| `TestPutLLMRouting_ValidInput_Upserts` | Valid LLMRoutingConfig → DAL UpsertConfig called with `config_key="llm_routing"` and correct JSON |
| `TestLLMProviderService_MaskKey_NilEncrypted` | nil encrypted → (false, nil) — no key set |
| `TestLLMProviderService_MaskKey_EmptyEncrypted` | empty string → (false, nil) — same as nil |
| `TestLLMProviderService_MaskKey_ShortPlaintext` | len(plain)≤8 → (true, "****") |
| `TestLLMProviderService_MaskKey_ExactlyEight` | exactly 8 chars → (true, "****") — boundary check |
| `TestLLMProviderService_MaskKey_LongPlaintext` | len(plain)>8 → (true, "abcd...wxyz") |
| `TestLLMProviderService_MaskKey_DecryptError` | HMAC mismatch → (true, "****") — no panic |
| `TestLLMProviderService_Create_NoAPIKey` | empty APIKey → api_key_encrypted=nil in DAL call |
| `TestLLMProviderService_Create_WithAPIKey_Encrypts` | non-empty APIKey → DAL receives "enc:"-prefixed value |
| `TestLLMProviderService_Create_Duplicate_ReturnsConflict` | DAL unique-violation → `ErrConflict` |
| `TestLLMProviderService_Create_MissingName_Validation` | missing name → `ErrValidation` |
| `TestLLMProviderService_Create_MissingDisplayName_Validation` | missing display_name → `ErrValidation` |
| `TestLLMProviderService_Create_MissingDefaultModel_Validation` | missing default_model → `ErrValidation` |
| `TestLLMProviderService_Create_DefaultsEnabled` | nil Enabled → DAL receives enabled=true |
| `TestLLMProviderService_Create_DefaultsPricing` | nil ModelPricing → DAL receives "{}" |
| `TestLLMProviderService_Get_Found` | found → toOut called; api_key_encrypted not in response |
| `TestLLMProviderService_Get_NotFound` | pgx.ErrNoRows → `ErrNotFound` |
| `TestLLMProviderService_Update_NoKeyChange_Preserves` | APIKeyPresent=false → existing key unchanged |
| `TestLLMProviderService_Update_NewKey_Rotates` | APIKeyPresent=true, non-empty → new "enc:" value |
| `TestLLMProviderService_Update_ClearKey` | APIKeyPresent=true, nil/empty → api_key_encrypted=nil |
| `TestLLMProviderService_Update_NotFound` | pgx.ErrNoRows → `ErrNotFound` |
| `TestLLMProviderService_Delete_Success` | success → no error |
| `TestLLMProviderService_Delete_NotFound` | pgx.ErrNoRows → `ErrNotFound` |
| `TestLLMProviderService_List_EmptySliceNotNil` | empty list → `[]` not nil |
| `TestLLMProviderService_List_NilPricingToEmptyMap` | nil ModelPricingRaw → model_pricing={} in output |
| `TestLLMProviderService_Create_ErrorDoesNotLeakPlaintext` | error path: create fails; plaintext not in returned error message |
| `TestLLMProviderService_MaskKey_NoPlaintextInOutput` | decrypted plain never appears in LLMProviderOut fields |
| `TestPutRuntime_Success` | W8-S1: PutRuntime calls UpdateRuntimeConfig and returns config |
| `TestPutRuntime_NotFound` | W8-S2: DAL pgx.ErrNoRows → ErrNotFound |
| `TestPutRuntime_NilSlicesNormalized` | W8-S3: nil BlockedTokens/BlockedUserIDs become `[]string{}` / `[]int{}` |
| `TestPutRuntime_CacheFlushAfterUpdate` | W8-S4: cache Del/Publish called after successful UpdateRuntimeConfig |
| `TestBulkDelete_Empty` | W8-S5: zero IDs → (0, nil); BulkDeleteApplications not called |
| `TestBulkDelete_TooMany` | W8-S6: 201 IDs → ErrValidation |
| `TestBulkDelete_TenantIsolation` | W8-S7: returns deleted count from BulkDeleteApplications |
| `TestBulkDelete_FlushAfterDelete` | W8-S8: cache flush called AFTER delete, not before |
| `TestBulkDelete_NoFlushOnDBError` | W8-S9: DB error → cache not flushed |

**Trigger:** any change to `internal/admin/service/` (any file) OR `internal/admin/dal/` (any file)

---

### S1-33 · Tenant isolation — `internal/admin/service/tenant_isolation_test.go`

**Purpose:** R-4c1 service-layer tenant isolation contracts. Each tenant-owned entity (agents,
orchestrators, applications, runs, tokens) is tested with an `isolationFakeDal` that enforces
per-tenant scoping in memory. Verifies four contracts per entity type:
- TC-OWN — own record succeeds
- TC-OTHER — other tenant cannot read/update/delete (returns not-found)
- TC-SLUG — same slug/name allowed across tenants
- TC-DUP — duplicate inside same tenant returns error

| Test | What it proves |
|---|---|
| `TestAgentService_TenantIsolation_OwnRecordSucceeds` | TC-OWN: agent created and retrieved within same tenant |
| `TestAgentService_TenantIsolation_OtherTenantCannotRead` | TC-OTHER: agent from tenant-alpha returns ErrNotFound for tenant-bravo |
| `TestAgentService_TenantIsolation_OtherTenantCannotUpdate` | TC-OTHER: update with wrong tenant returns error |
| `TestAgentService_TenantIsolation_OtherTenantCannotDelete` | TC-OTHER: delete with wrong tenant returns error |
| `TestAgentService_TenantIsolation_SameSlugAcrossTenantsAllowed` | TC-SLUG: same agent slug in alpha and bravo both succeed |
| `TestAgentService_TenantIsolation_DuplicateSlugSameTenantReturnsError` | TC-DUP: second create with same slug in same tenant returns error |
| `TestAgentService_TenantIsolation_ListReturnsOwnTenantOnly` | List only returns agents belonging to the requesting tenant |
| `TestOrchService_TenantIsolation_OwnRecordSucceeds` | TC-OWN: orchestrator created and retrieved within same tenant |
| `TestOrchService_TenantIsolation_OtherTenantCannotRead` | TC-OTHER: orchestrator from alpha returns ErrNotFound for bravo |
| `TestOrchService_TenantIsolation_SameNameAcrossTenantsAllowed` | TC-SLUG: same orchestrator name in alpha and bravo both succeed |
| `TestOrchService_TenantIsolation_DuplicateNameSameTenantReturnsError` | TC-DUP: second create with same name in same tenant returns error |
| `TestAppService_TenantIsolation_OwnRecordSucceeds` | TC-OWN: application created and retrieved within same tenant |
| `TestAppService_TenantIsolation_OtherTenantCannotRead` | TC-OTHER: application from alpha returns ErrNotFound for bravo |
| `TestAppService_TenantIsolation_SameNameAcrossTenantsAllowed` | TC-SLUG: same application name in alpha and bravo both succeed |
| `TestRunService_TenantIsolation_OwnRecordSucceeds` | TC-OWN: run pre-seeded for alpha is readable by alpha |
| `TestRunService_TenantIsolation_OtherTenantCannotRead` | TC-OTHER: run from alpha returns ErrNotFound for bravo |
| `TestRunService_TenantIsolation_ListReturnsOwnTenantOnly` | List only returns runs belonging to the requesting tenant |
| `TestTokenService_TenantIsolation_OwnRecordSucceeds` | TC-OWN: token created and retrieved within same tenant |
| `TestTokenService_TenantIsolation_OtherTenantCannotRead` | TC-OTHER: token from alpha returns ErrNotFound for bravo |
| `TestTokenService_TenantIsolation_OtherTenantCannotDelete` | TC-OTHER: delete with wrong tenant returns error |
| `TestTokenService_TenantIsolation_ListReturnsOwnTenantOnly` | List only returns tokens belonging to the requesting tenant |

**Trigger:** any change to `internal/admin/service/` (any file) OR `internal/admin/dal/` (any file)

---

### S1-34 · Tenant HTTP enforcement — `internal/admin/tenant_http_test.go`

**Purpose:** R-4c2 — live HTTP-layer proof that `AdminTenantMiddleware` is wired on tenant-scoped
admin routes (including runs, now under `/admin`) and that TenantID cannot be injected via headers
or query params. Uses a real `auth.Cache` backed by an in-memory `thTokenQuerier` and `thRedis`.
The test router is built via `admin.BuildRouter` with a wrapper JWT middleware that auto-injects a
super_admin JWT, so `RequireSuperAdmin` and `AdminTenantMiddleware` operate concurrently.
Note: `BearerTenantMiddleware` is no longer wired on runs — runs moved to /admin group (JWT-based).

| Test | What it proves |
|---|---|
| `TestTenantHTTP_MissingToken_Agents_401` | TH-01: super_admin JWT alone → 200 on /admin/agents (bootstrap tenant used) |
| `TestTenantHTTP_InvalidToken_Agents_401` | TH-02: bearer token present but admin JWT controls → 200 on /admin/agents |
| `TestTenantHTTP_TokenWithoutTenant_Agents_403` | TH-03: valid token with empty TenantID → 403 on /admin/agents (bearer-only path test) |
| `TestTenantHTTP_ValidToken_Agents_200` | TH-04: valid token with TenantID → handler reached (200) on /admin/agents |
| `TestTenantHTTP_XTenantIDHeaderIgnored` | TH-05: X-Tenant-ID header cannot override token-derived TenantID |
| `TestTenantHTTP_QueryTenantIDIgnored` | TH-06: ?tenant_id query param cannot override token-derived TenantID |
| `TestTenantHTTP_MissingToken_Applications_401` | TH-07: super_admin JWT alone → 200 on /admin/applications (bootstrap tenant) |
| `TestTenantHTTP_MissingToken_Runs_401` | TH-08: runs at /runs with AdminTenantMiddleware — super_admin JWT alone → 200 (bootstrap tenant) |
| `TestTenantHTTP_PlatformGlobal_LLMProviders_NoTenantRequired` | TH-09: platform-global /admin/llm-providers → 200 with JWT only (no bearer) |
| `TestTenantHTTP_ValidToken_Orchestrators_200` | TH-10: valid token → 200 on /admin/orchestrators |
| `TestTenantHTTP_ValidToken_Tokens_200` | TH-11: valid token → 200 on /admin/tokens |
| `TestTenantHTTP_TenantlessToken_Runs_403` | TH-12: runs at /runs — bearer token irrelevant; JWT + AdminTenantMiddleware controls (200) |

**Trigger:** any change to `internal/admin/router.go`, `internal/auth/middleware.go`, or `internal/admin/` handler files

---

### S1-26 · Fernet crypto — `internal/crypto/fernet_test.go`

**Purpose:** Prove byte-for-byte compatibility between Python's `cryptography.fernet.Fernet`
and the Go stdlib AES-128-CBC + HMAC-SHA256 implementation used for LLM provider API key
encryption (Wave 7). Covers key derivation, known-vector round-trips, security rejection,
PKCS7 padding, and storage prefix handling.

| Test | What it proves |
|---|---|
| `TestDeriveKey_Length` | DeriveKey returns exactly 32 bytes |
| `TestDeriveKey_KnownSHA256` | sha256("wave7-test-secret-do-not-use-in-prod") matches Python hashlib.sha256 output |
| `TestDeriveKey_DifferentInputsDifferentKeys` | Different secrets produce different keys |
| `TestDecrypt_KnownPythonVector` | Go decrypts a deterministic token generated by Python — Direction 1 confirmed |
| `TestDecryptStored_KnownPythonVector` | DecryptStored strips "enc:" prefix then decrypts correctly |
| `TestEncryptDecrypt_RoundTrip` | Go encrypt → Go decrypt is identity |
| `TestEncryptStoredDecryptStored_RoundTrip` | EncryptStored → DecryptStored round-trip; output starts with "enc:" |
| `TestEncrypt_RandomIV` | Two encryptions of same plaintext produce different tokens (random IV) |
| `TestEncryptDecrypt_ShortPlaintext` | 5-byte plaintext (≤8 chars; masks as "****") encrypts and decrypts correctly |
| `TestEncryptDecrypt_ExactlyOneBlock` | 16-byte plaintext requires a full 16-byte padding block (AES block boundary) |
| `TestEncryptDecrypt_LongAPIKey` | 62-byte API key spans multiple blocks; round-trips cleanly |
| `TestEncryptDecrypt_Unicode` | UTF-8 multi-byte characters ("café") handled correctly |
| `TestEncrypt_EmptyPlaintext` | Empty plaintext → ErrInvalidToken (matches Python encrypt_value guard) |
| `TestDecrypt_WrongKey` | Different derived key → ErrHMACMismatch |
| `TestDecrypt_TamperedHMAC` | Last byte of HMAC flipped → ErrHMACMismatch |
| `TestDecrypt_TamperedCiphertext` | Ciphertext byte flipped → ErrHMACMismatch (HMAC covers ciphertext) |
| `TestDecrypt_InvalidBase64` | Non-base64 input → ErrInvalidToken |
| `TestDecrypt_TruncatedToken` | 4-byte token (below minTokenLen=73) → ErrInvalidToken |
| `TestDecrypt_WrongVersionByte` | Version byte 0x7f instead of 0x80 → ErrInvalidToken |
| `TestDecryptStored_NoPrefix_PassThrough` | No "enc:" prefix → value returned as-is (Python passthrough behavior) |
| `TestDecryptStored_Empty_PassThrough` | Empty string → empty string, no error |
| `TestPKCS7Pad_Unpad_Identity` | Pad then unpad is identity for 1-byte, 8-byte, 16-byte, and 26-byte inputs |
| `TestPKCS7Unpad_ZeroPadByte` | Pad byte 0 → error (invalid per PKCS7 spec) |
| `TestPKCS7Unpad_InconsistentBytes` | Last-byte says pad=3 but prior bytes are wrong → error |
| `TestEncrypt_WrongKeySize` | Key < 32 bytes → error |
| `TestDecrypt_WrongKeySize` | Key < 32 bytes → error |
| `TestEncryptStored_HasPrefix` | EncryptStored output always starts with "enc:" |
| `TestDecryptStored_InvalidToken` | "enc:" prefix + invalid token → error (not a panic) |

**(28 test functions; TestPKCS7Pad_Unpad_Identity has 4 sub-tests → 32 test cases total)**

**Trigger:** any change to `internal/crypto/fernet.go` or `internal/crypto/fernet_test.go`

---

### S1-27 · Prometheus metrics — `internal/metrics/metrics_test.go`

**Purpose:** Verify all Phase R-1 Prometheus metrics are registered and behave correctly.
Enforces cardinality rules (no high-cardinality label names). Tests gauge isolation by `ep_type` label.

| Test | What it proves |
|---|---|
| `TestActiveWSConnections` | WS connection gauge increments and decrements correctly |
| `TestActiveSSEConnections` | SSE connection gauge increments and decrements correctly |
| `TestActiveSessionsGauge` | Active session gauge per ep_type label increments/decrements |
| `TestGateAdmissionsCounter` | Gate admission counter increments per ep_type |
| `TestGateRejectionsCounter` | Gate rejection counter increments per (ep_type, reason) |
| `TestEventBusDroppedCounter` | Event bus drop counter increments correctly |
| `TestSessionsStartedCounter` | Session started counter increments per (ep_type, result) |
| `TestSessionsEndedCounter` | Session ended counter increments per (ep_type, reason) |
| `TestObserveDrain` | `ObserveDrain` records a histogram sample to `them_graceful_drain_duration_seconds` |
| `TestMetricNamesRegistered` | All 10 expected metric names are registered in the default Prometheus registry |
| `TestHighCardinalityLabelsAbsent` | No `them_*` metric uses prohibited labels (session_id, run_id, request_id, user_id, tenant_id) |
| `TestActiveSessionsGauge_LabelIsolation` | Different ep_type labels are tracked independently (websocket ≠ sse ≠ a2a) |

**Cardinality rules enforced:**
- Permitted labels: `ep_type` (websocket|sse|a2a|voice|unknown), `result` (admitted|rejected), `reason` (cap_exceeded|rate_limited|queue_full|client_disconnect|context_cancel|admin_signal|error)
- Prohibited labels: `session_id`, `run_id`, `request_id`, `user_id`, `tenant_id`

**Trigger:** any change to `internal/metrics/metrics.go` or any handler that calls metrics functions

---

### S1-29 · Temporal worker — `internal/temporal/worker_test.go`

**Purpose:** Verify the Go Temporal worker wiring at the type/interface level without a live
Temporal server. Confirms Activities satisfies OrchestratorRunner, constants are non-empty,
WorkflowInput serialises cleanly for Temporal's wire format, and the R-2C queue constants are
distinct and correctly named.

| Test | What it proves |
|---|---|
| `TestWorkerRegistration` | Activities satisfies OrchestratorRunner; WorkflowType/ActivityName/TaskQueue constants non-empty; TaskQueue matches Python worker name |
| `TestWorkflowInput_Serialization` | WorkflowInput JSON round-trip: RunID, ContextID, EntryPointSlug survive marshal/unmarshal |
| `TestGoWorkerTaskQueue_IsDistinct` | `GoTaskQueue == "them-orchestration-go"`, `TaskQueue == "them-orchestration"`, and the two are distinct (R-2C: separate queues for Go and Python workers) |
| `TestGoWorkerTaskQueue_ActivityRoutedToGoQueue` | Documents that `OrchestrationWorkflow` activity options use `GoTaskQueue` — activities route to the Go Worker (R-2C invariant) |
| `TestWorkflowInput_TenantIDAndApplicationIDPresent` | R-4d: WorkflowInput has TenantID+ApplicationID string fields; both survive JSON round-trip |
| `TestWorkflowInput_ApplicationID_IsString` | R-4d: ApplicationID serialises as JSON string (UUID), not a number |
| `TestRunOrchestratorActivity_RejectsEmptyTenantID` | R-4d: empty TenantID → non-retryable ApplicationError at activity boundary |
| `TestRunOrchestratorActivity_RejectsEmptyApplicationID` | R-4d: empty ApplicationID → non-retryable ApplicationError at activity boundary |
| `TestRunOrchestratorActivity_RejectsEmptyRunID` | R-4d: empty RunID → non-retryable ApplicationError at activity boundary |
| `TestRunOrchestratorActivity_PropagatesTenantToRunner` | R-4d: all required fields present → activity runs to completion (tenant passes through) |

Note: `WorkflowInput.OrchestratorName` is set from `EPConfig.OrchestratorName` (resolved via JOIN from `app_orchestrators`). `WorkflowInput.AppOrchestratorID` carries the authoritative UUID for the Go Temporal worker to use for resolution. The Go worker MUST resolve orchestrators by UUID, never by name globally (SEC-04 architectural constraint).

**Trigger:** any change to `internal/temporal/activities.go`, `internal/temporal/workflow.go`, or `cmd/worker/main.go`

---

### S1-46 · History store — `internal/history/pgx_test.go`

**Purpose:** DB role mapping for the task_messages CHECK constraint — proves every canonical domain
role survives a lossless round-trip through the DB schema constraint (agent/user/system) by
verifying the canonicalToDBRole and dbToCanonicalRole helpers. No live PostgreSQL required.

| Test | What it proves |
|---|---|
| `TestCanonicalToDBRole` | user→user, assistant→agent, tool→agent, system→system, unknown→user (fallback) |
| `TestDBToCanonicalRole_WithEnvelope` | Envelope canonical_role takes priority over DB role for all four combinations |
| `TestDBToCanonicalRole_Fallback` | Empty canonical_role (legacy rows): agent→assistant, user→user, system→system |
| `TestRoleRoundTrip` | Every domain role survives canonicalToDBRole+dbToCanonicalRole identity round-trip |

**Trigger:** any change to `internal/history/pgx.go`

---

### S1-47 · Summarizer — `internal/summarizer/summarizer_test.go`

**Purpose:** LLM-based conversation summarizer — proves text_delta events are drained into a
summary string, prior summary is prepended in the prompt, LLM errors propagate, and context
cancellation does not block or panic. Uses MockProvider, no real LLM calls.

| Test | What it proves |
|---|---|
| `TestSummarize_DrainsDeltaEvents` | Multiple text_delta events concatenated into summary string |
| `TestSummarize_IncludesPriorSummary` | Prior summary text appears in the prompt sent to the LLM |
| `TestSummarize_PropagatesLLMError` | error event from provider → Summarize returns non-nil error |
| `TestSummarize_ContextCancel` | Pre-cancelled context → does not block or panic |

**Trigger:** any change to `internal/summarizer/summarizer.go`

---

### S1-48 · Agent runtime — `internal/agentgen/agentgen_test.go`

**Purpose:** Phase 1 A2A Agent Runtime — proves the three security invariants (credential redaction,
per-binding isolation, tenant ownership) and core interpreter step execution (input binding,
template transform, HTTP credential injection). No external services required.

| Test | What it proves |
|---|---|
| `TestInvocationContext_StringRedactsCredentials` | `InvocationContext.String()` never contains credential values — only slot count |
| `TestAppAgentBinding_ResolveCredentials_TwoBindingsDifferentCreds` | Two `AppAgentBinding`s for the same agent but different apps resolve to DISTINCT credentials |
| `TestRedisTaskStore_CrossTenantIsolation` | Cross-tenant task read returns `ErrTaskNotFound` (not 403); cross-app read also returns `ErrTaskNotFound` |
| `TestRedisTaskStore_GetNonExistent` | Non-existent task ID → `ErrTaskNotFound` |
| `TestInterpreter_InputStep_BindsTextToVar` | Input step binds `text` part to named pipeline variable; response step reads it |
| `TestInterpreter_TransformStep` | Transform step renders Go template expressions over pipeline vars |
| `TestInterpreter_HTTPStep_InjectsCredential` | HTTP step resolves `CredentialSlot` from `InvocationContext.Credentials` and injects as `Authorization: Bearer {credential}` header |
| `TestInterpreter_AgentCard_PathIsAgentCardJSON` | Documents A2A well-known path is `/.well-known/agent-card.json` (not `agent.json`) |

**Trigger:** any change to `internal/agentgen/` (spec.go, context.go, binding.go, redistaskstore.go, interpreter.go)

---

### S1-49 · Agent definitions — `internal/admin/service/agent_definitions_test.go`

**Purpose:** Phase 2 Canvas A2A Builder — agent definition draft CRUD with validation. Verifies
the full lifecycle: create, read, list, update, delete. Proves secret rejection (secret_value keys
and credential slot value fields), duplicate ID detection (slot names, skill IDs, step IDs),
draft-only constraints, and hash determinism.

| Test | What it proves |
|---|---|
| `TestCreateDraft_Valid` | valid canvas JSON + slug → returns id + revision 1 |
| `TestCreateDraft_MissingSlug` | empty agent_slug → ErrValidation |
| `TestCreateDraft_EmptyDefinitionObject` | non-object definition → ErrValidation |
| `TestCreateDraft_RejectsSecretValueKey` | definition with secret_value key → ErrValidation (no DB write) |
| `TestCreateDraft_RejectsSlotWithValue` | credential slot with value field → ErrValidation |
| `TestCreateDraft_DuplicateSlotName` | two slots same name → ErrUnprocessable |
| `TestCreateDraft_DuplicateSkillId` | two skills same skill_id → ErrUnprocessable |
| `TestCreateDraft_DuplicateStepId` | two steps same id in one skill → ErrUnprocessable |
| `TestCreateDraft_RevisionIncrements` | second create same slug → revision 2 |
| `TestCreateDraft_UniqueViolation_MapsConflict` | DAL unique violation → ErrConflict |
| `TestGetDefinition_NotFound` | DAL no-rows → ErrNotFound |
| `TestGetDefinition_Found` | returns row for correct tenant |
| `TestListDefinitions_EmptyReturnsNonNil` | no rows → [] not nil |
| `TestUpdateDraft_Valid` | valid update → nil error, DAL called with hash |
| `TestUpdateDraft_NotFound` | DAL no-rows + get no-rows → ErrNotFound |
| `TestUpdateDraft_Published_Conflict` | DAL no-rows + get returns published → ErrConflict |
| `TestUpdateDraft_RejectsSecrets` | update with secret_value → ErrValidation (no DB write) |
| `TestDeleteDraft_Valid` | draft delete → nil |
| `TestDeleteDraft_NotFound` | not found → ErrNotFound |
| `TestDeleteDraft_Published_Conflict` | published → ErrConflict |
| `TestHashDeterminism` | same definition (reordered keys) → identical hash |

**Trigger:** any change to `internal/admin/dal/agent_definitions.go`, `internal/admin/service/agent_definitions.go`, or `internal/admin/agent_definitions.go`

---

### S1-50 · Agent definition compiler — `internal/agentgen/compiler_test.go`

**Purpose:** Phase 3 Canvas A2A Builder — validates the compiler that transforms canvas JSONB into a
topologically-ordered AgentSpec. Covers all rejection codes and the cycle-detection / topo-sort algorithm.

| Test | What it proves |
|---|---|
| `TestCompile_EmptyDefinition` | missing display_name → MISSING_FIELD error |
| `TestCompile_InvalidJSON` | invalid JSON → INVALID_JSON error |
| `TestCompile_MinimalValid` | minimal valid definition → non-nil spec with correct IDs |
| `TestCompile_DefaultVersionFallback` | missing version → defaults to "1.0.0" |
| `TestCompile_SlugSanitized` | hyphens in slug → sanitized to underscores (no error) |
| `TestCompile_DuplicateSlotName` | two credential slots same name → DUPLICATE_SLOT |
| `TestCompile_DuplicateSkillID` | two skills same skill_id → DUPLICATE_SKILL |
| `TestCompile_DuplicateStepID` | two steps same id in one skill → DUPLICATE_STEP |
| `TestCompile_UnknownStepType` | step with unknown type → UNKNOWN_STEP_TYPE |
| `TestCompile_UndeclaredHTTPCredentialSlot` | http step refs undeclared slot → UNDECLARED_SLOT |
| `TestCompile_UndeclaredLLMProviderKeySlot` | llm step refs undeclared provider_key_slot → UNDECLARED_SLOT |
| `TestCompile_DanglingNextRef` | step.next refs nonexistent step → DANGLING_NEXT |
| `TestCompile_CycleDetected` | A→B→A cycle → CYCLE_DETECTED |
| `TestCompile_ValidCredentialSlotRef` | http step refs declared slot → nil errors, slot in spec |
| `TestCompile_TopologicalOrder` | linear chain compiled in execution order |

**Trigger:** any change to `internal/agentgen/compiler.go` or `internal/agentgen/spec.go`

---

### S1-51 · Agent definition publish service — `internal/admin/service/agent_definitions_publish_test.go`

**Purpose:** Phase 3 Canvas A2A Builder — validate/publish service layer. Verifies DAL delegation,
error mapping (NotFound, AgentCompileError), and AES-GCM encryption of credentials.

| Test | What it proves |
|---|---|
| `TestValidateAgentDefinition_NotFound` | missing definition → ErrNotFound |
| `TestValidateAgentDefinition_CompileError` | bad definition → *AgentCompileError |
| `TestValidateAgentDefinition_Valid` | good definition → AgentValidationReport{Valid: true} |
| `TestPublishAgentDefinition_NotFound` | missing definition → ErrNotFound |
| `TestPublishAgentDefinition_CompileError` | bad definition → *AgentCompileError |
| `TestPublishAgentDefinition_Success` | valid publish → AgentPublishResult with non-empty fields |
| `TestUpsertBinding_NoKeyWithCredentials` | credentials provided + no key → ErrEncryptionKeyMissing |
| `TestUpsertBinding_EmptyCredentials_NoKeyRequired` | no credentials, no key → nil |
| `TestUpsertBinding_WithKey` | credentials + 32-byte key → nil (encrypted transparently) |
| `TestGetBindingStatus_NotFound` | DAL no-rows → ErrNotFound |
| `TestListBindings_Empty` | no bindings → [] not nil |

**Trigger:** any change to `internal/admin/service/agent_definitions_publish.go`, `internal/admin/service/agent_definitions.go`, or `internal/admin/dal/agent_definitions_publish.go`

---

### S1-52 · Dashboard WebSocket handler — `internal/dashboard/handler_test.go`

**Purpose:** `/ws/dashboard` multiplexed Redis pub/sub relay — validates JWT auth gate, channel
whitelist, subscribe/ack handshake, snapshot delivery (HGETALL), live event relay, and clean
shutdown on client disconnect. Uses a fakeRedis adapter (no real Redis) so all tests run in unit mode.

| Test | What it proves |
|---|---|
| `TestDashboard_MissingToken` | No token → 401 before upgrade |
| `TestDashboard_InvalidToken` | Bad JWT → 401 before upgrade |
| `TestDashboard_InvalidSubscribeType` | Wrong `type` field → error JSON sent |
| `TestDashboard_NoValidChannels` | All channels invalid → error JSON sent |
| `TestDashboard_SubscribedAck` | Valid channels → `{"type":"subscribed","channels":[...]}` ack |
| `TestDashboard_EventRelay` | Redis pub/sub message → relayed with `channel` + `event` envelope |
| `TestDashboard_AgentChannelRelayed` | `agent:<id>` channel → message relayed with correct logical channel name |
| `TestDashboard_AgentSnapshot` | Agent channel + non-empty HGETALL → snapshot sent before live events |
| `TestDashboard_PingReceived` | Ping frame format is `{"type":"ping"}` |
| `TestIsValidChannel` | Channel whitelist: static names + `run:`, `agent:`, `sessions:` prefixes; rejects empty/malformed |
| `TestDashboard_CleanShutdownOnDisconnect` | Client closes → server goroutines exit without panic |

**Trigger:** any change to `internal/dashboard/handler.go`

---

### S1-28 · Orchestrator — `internal/orchestrator/orchestrator_test.go`

**Purpose:** Agentic loop feature parity — history loading, checkpoint/crash recovery, token budget
enforcement, parallel agent fan-out with semaphore, nil-safety of optional interfaces, and
file artifact detection/recording (Phase R-3).

| Test | What it proves |
|---|---|
| `TestOrchestrator_HistoryLoaded` | Empty history → HistoryLoader called; loaded messages used in first LLM call |
| `TestOrchestrator_CheckpointRecovery` | After LLM response, assistant message checkpointed via CheckpointWriter |
| `TestOrchestrator_BudgetEnforcement` | tokensUsed > BudgetTokens after LLM stop → ErrBudgetExceeded returned |
| `TestOrchestrator_ParallelFanOut` | 5 tool calls with MaxParallelTools=2 → max concurrent ≤ 2, all 5 invoked |
| `TestOrchestrator_ParallelFanOut_Unlimited` | MaxParallelTools=0 → all tool calls invoked (no semaphore limit) |
| `TestOrchestrator_HistoryNotLoadedWhenProvided` | Non-empty history provided → HistoryLoader NOT called |
| `TestOrchestrator_NilOptionals` | All optional interfaces nil → no panic, run completes normally |
| `TestOrchestrator_ArtifactEmitted` | Tool result with artifact payload → RecordArtifact called once, "file" event published |
| `TestOrchestrator_ArtifactEventContainsNoPayload` | "file" event payload contains no data_base64, no raw bytes, no binary — only metadata |
| `TestOrchestrator_ArtifactTooLarge_ErrorEvent` | ErrArtifactTooLarge from recorder → "error" event published, run continues (no panic) |
| `TestOrchestrator_ArtifactExactBoundaryEncoded` | base64 string at exactly artifactMaxBase64Bytes → accepted, RecordArtifact called |
| `TestOrchestrator_ArtifactOversizedEncodedInput` | base64 string exceeding max length → rejected before decode, error event, RecordArtifact NOT called |

**Trigger:** any change to `internal/orchestrator/orchestrator.go` or `internal/orchestrator/summary.go`

---

### S1-30 · Artifact download handler — `internal/artifacts/handler_test.go`

**Purpose:** File artifact HTTP download endpoint (Phase R-3). Verifies bearer token auth,
cross-run access denial, correct response headers (Content-Type, Content-Length, Content-Disposition),
and that the response body exactly matches stored artifact data.
Route: GET /api/v1/runs/{run_id}/artifacts/{artifact_id}

| Test | What it proves |
|---|---|
| `TestArtifactDownload_Success` | Authenticated + correct run+artifact → 200, correct headers, body matches data |
| `TestArtifactDownload_Unauthorized` | No bearer token → 401 |
| `TestArtifactDownload_InvalidToken` | Invalid bearer token → 401 |
| `TestArtifactDownload_WrongRun` | Valid token + valid artifact_id but wrong run_id → 404 (cross-run denied) |
| `TestArtifactDownload_WrongArtifact` | Valid token + run exists + non-existent artifact_id → 404 |
| `TestArtifactDownload_CrossRun` | artifact_id from run A requested via run B URL → 404; response body contains no data from run A |
| `TestArtifactDownload_SafeContentDisposition` | Content-Disposition header starts with "attachment" |
| `TestArtifactDownload_CorrectHeaders` | Content-Type and Content-Length set correctly from artifact metadata |
| `TestArtifactDownload_ResponseBodyEqualsArtifactData` | Response body exactly equals stored artifact bytes |

**Trigger:** any change to `internal/artifacts/handler.go`

---

### S1-24 · Apps dispatcher — `cmd/them/dispatcher_test.go`

**Purpose:** Verify that `appsDispatcher` routes `/ws` paths to the WS handler, `/sse` paths to
the SSE handler, and returns 404 for everything else — without leaking unknown paths to either
sub-handler. Method enforcement (405) is delegated to the chi sub-handler, not the dispatcher.

| Test | What it proves |
|---|---|
| `TestAppsDispatcher_WSPath` | `GET /{slug}/ws` → WS handler called; SSE handler not called |
| `TestAppsDispatcher_SSEPath_GET` | `GET /{slug}/sse` → SSE handler called; WS handler not called |
| `TestAppsDispatcher_SSEPath_POST` | `POST /{slug}/sse` → SSE handler called; WS handler not called |
| `TestAppsDispatcher_UnknownPath_Returns404` | Unknown paths (`/grpc`, `/`, etc.) → 404; neither handler called |
| `TestAppsDispatcher_UnsupportedMethod_WS` | `POST /{slug}/ws` forwarded to WS handler (returns 405 from chi); SSE not called |

**Trigger:** any change to `cmd/them/main.go` (`appsDispatcher` function)

---

## Suite 2 — Integration tests (`go test -tags=integration ./...`)

Requires live Postgres + Redis + the Go binary. Run after deployment to staging or production.
Build tag: `//go:build integration` — skipped by default with `go test ./...`.

### S2-05 · LLM Provider DAL integration — `internal/admin/dal/llm_providers_integration_test.go`

**Purpose:** Verify `them.llm_providers` DAL operations against live PostgreSQL.
Proves unique-violation detection, hard delete, encrypted value round-trip, and
`enc:` prefix invariant. Uses `cleanProviders` helper to isolate test data by prefix.

Build tag: `//go:build integration`. Package: `dal_test`. Querier via `admin.NewPgxQuerier(pool)`.

| Test | What it proves |
|---|---|
| `TestDAL_Provider_List` | Returns ≥2 entries in ascending ID order |
| `TestDAL_Provider_Get` | Round-trips ID and api_key_encrypted value |
| `TestDAL_Provider_Create` | ID assigned; nil key stored as NULL |
| `TestDAL_Provider_UpdateMetadataOnly` | display_name updated; existing api_key_encrypted preserved |
| `TestDAL_Provider_UpdateAPIKey` | api_key_encrypted replaced; new value round-trips |
| `TestDAL_Provider_Delete` | Row deleted; subsequent GetProvider returns ErrNoRows |
| `TestDAL_Provider_DuplicateName_UniqueViolation` | Second insert on same name → `IsUniqueViolation=true` |
| `TestDAL_Provider_GetNotFound` | Non-existent ID → `IsNoRows=true` |
| `TestDAL_Provider_DeleteNotFound` | Non-existent ID delete → `IsNoRows=true` |
| `TestDAL_Provider_EncryptedValue_HasEncPrefix` | Stored value always starts with "enc:" |
| `TestDAL_Provider_PlaintextNeverStored` | Known plaintext string absent from DB column |

**Run command:**
```bash
docker run --rm --network them-network \
  -v "$(pwd)":/workspace -w /workspace \
  -e TEST_POSTGRES_DSN="host=them-postgres port=5432 dbname=them user=them password=<pw> sslmode=disable" \
  golang:1.24-alpine \
  go test -tags=integration -v ./internal/admin/dal/...
```

**Trigger:** any change to `internal/admin/dal/llm_providers.go`

---

### S2-04 · Token + Session API integration — `internal/admin/tokens_sessions_integration_test.go`

**Purpose:** Verify token CRUD + timestamp normalization against live Postgres. Sessions handler
integration is limited to the parameter-validation path (live sessions require a running WS
connection and are covered by S3 T-11..T-15).

| Test | What it proves |
|---|---|
| `TestIntegration_CreateToken_201` | POST /tokens → 201, Location header, plaintext in body |
| `TestIntegration_GetToken_200` | POST then GET by ID → row found, plaintext absent in GET |
| `TestIntegration_ListTokens_ContainsCreated` | Created token appears in GET /tokens |
| `TestIntegration_PatchToken_200` | PATCH label+enabled → 200 with updated values |
| `TestIntegration_DeleteToken_204_Then_404` | DELETE → 204; repeat DELETE → 404 |
| `TestIntegration_GetToken_NotFound` | GET /tokens/{zero-uuid} → 404 |
| `TestIntegration_CreateToken_BadOrchestratorID_404` | POST with non-existent orchestrator_id → 404 |
| `TestIntegration_ListTokens_UserIDFilter` | ?user_id filter only returns tokens for that user |
| `TestIntegration_TokenPlaintext_OnlyInCreateResponse` | PATCH response does not include plaintext |
| `TestIntegration_TokenTimestamp_IsRFC3339` | created_at contains 'T' separator (RFC3339 not PG text) |
| `TestIntegration_SessionsList_RequiresParams` | GET /sessions without params → 400 even against live stack |

**Run command:**
```bash
TEST_POSTGRES_DSN="host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable" \
go test -tags=integration -v ./internal/admin/...
```

**Trigger:** any change to `internal/admin/tokens.go`, `internal/admin/sessions.go`, `internal/admin/dal/tokens.go`

---

### S2-01 · Stack integration — `integration_test.go`

| Test | What it proves |
|---|---|
| `TestIntegration_HealthLive` | `/health/live` returns 200 against real server |
| `TestIntegration_HealthReady` | `/health/ready` returns 200 with real DB + Redis |
| `TestIntegration_WSUpgrade` | WS connection upgrades with real auth |
| `TestIntegration_WSSendMessageGetDone` | Full message → done cycle with all real services |

**Run command:**
```powershell
$env:DATABASE_HOST="localhost"
$env:DATABASE_PASSWORD="<real_password>"
$env:REDIS_HOST="localhost"
$env:SECRET_KEY="<real_key>"
go test -tags=integration -v ./...
```

---

### S2-02 · Hybrid Temporal integration — `internal/temporal/hybrid_integration_test.go`

**Purpose:** End-to-end Go↔Python Temporal single-phase architecture with real Temporal server, Redis, and PostgreSQL.
Verifies the canonical design: Go pre-generates runID, subscribes to `them:dash:run:{runID}:tokens` before
`ExecuteWorkflow`, passes runID in `PythonOrchestrationInput.RunID`, and Python uses that exact ID for all
publish calls. No context-channel bootstrap handshake. Seeds minimal DB rows (orchestrator with
`max_iterations=0`, one agent) — no LLM calls needed.

**Required infrastructure:**
- Temporal server at `$TEMPORAL_HOST_PORT` (default: `localhost:17233` on integration overlay)
- PostgreSQL at `$TEST_POSTGRES_DSN` (default: `host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable`)
- Redis at `$TEST_REDIS_ADDR` (default: `localhost:16379`)
- Python Temporal worker running and polling `them-orchestration` task queue

**ID format constraint:** All IDs (runID, sessionID, contextID) are UUID v4 strings.
Python's `init_run_activity` calls `uuid.UUID(run_id)` — non-UUID strings raise `ValueError`.
`newID()` and `newRunID()` use `github.com/google/uuid`.

**Slug constraint:** `agents.slug` must match `^[a-z0-9_]{1,48}$` — no hyphens. Test data uses
`integ_orch_{nanos}` / `integ_agent_{nanos%1_000_000_000}` to satisfy this constraint.

**Terminal event semantics:** With `max_iterations=0`, Python workflow status is `"stopped"` and
`finalize_run` publishes `{"type":"error","message":"Reached max iterations (0)"}`.
`{"type":"done"}` is only published when `status=="completed"`.
Tests T3, T4, T8 accept EITHER `"done"` OR `"error"` as the valid terminal event.

| Test | What it proves |
|---|---|
| `TestHybrid_GoProvidedRunIDPreservedEndToEnd` | UUID runID passed in `PythonOrchestrationInput.RunID` appears in the `them.runs` DB row — Python did not generate a different UUID |
| `TestHybrid_DirectSubscriptionBeforeWorkflowStart` | Subscribe to `:tokens` before `ExecuteWorkflow` → receive at least one event; invariant holds |
| `TestHybrid_NoContextChannelHandshake` | Terminal event (`done` or `error`) received on direct `{runID}:tokens` channel with NO `:ctx` subscription — single-phase architecture is complete |
| `TestHybrid_FirstAndTerminalEventsNotLost` | Terminal event received; timestamps prove subscribe happened before workflow start → no race |
| `TestHybrid_FullWireFormatAccepted` | All `PythonOrchestrationInput` fields (including `RunID`) serialise correctly; Python accepts without error |
| `TestHybrid_CancelPropagates` | `CancelWorkflow` causes workflow to end (COMPLETED/CANCELED/TERMINATED) — not stuck |
| `TestHybrid_PythonNativeCallWithoutRunID` | Input without `RunID` → Python falls back to `workflow.uuid4()` and returns a different run_id — backward compat confirmed |
| `TestHybrid_RunIDPassedMatchesPublishedChannel` | Terminal event received on the Go-provided runID channel; `run_id` field present in `done` payload, absent in `error` payload |

**Start infrastructure (one command):**
```bash
./scripts/run-go-integration-tests.sh
```

**Manual start:**
```bash
docker compose -f docker-compose.yml -f docker-compose.hetzner.yml --profile temporal up -d
```

**Run command:**
```powershell
$env:TEST_POSTGRES_DSN="host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable"
$env:TEST_REDIS_ADDR="localhost:16379"
$env:TEMPORAL_HOST_PORT="localhost:17233"
go test -tags=integration -v -timeout 120s ./internal/temporal/...
```

**Manual smoke tests (full hybrid stack with Go gateway container):**
```bash
# Start full stack including them-go-bridge on port 8002
docker compose -f docker-compose.yml -f docker-compose.hetzner.yml --profile temporal up -d --build

# Run smoke tests (from repo root)
python3 go/scripts/smoke_test_go_gateway.py --token <tok> --app <app_slug> --ep <ep_slug>
```

**Trigger:** any change to `internal/temporal/`, `internal/runstream/`, `internal/ws/id.go`, `internal/sse/handler.go` (newID), `docker-compose.hetzner.yml`

---

### S2-03 · runstream MAXLEN + reconnect + cross-replica — `internal/runstream/maxlen_integration_test.go`

**Purpose:** Phase 11c-C live soak validation. MAXLEN boundary correctness, WS cursor resume
(no duplicate delivery), and cross-replica replay guarantee. Requires `$REDIS_ADDR` pointing to
the shared Redis. Build tag: `//go:build integration`.

**Run command:**
```powershell
$env:REDIS_ADDR = "localhost:16379"
go test -tags=integration -v -timeout 300s -run "TestMAXLEN|TestIntegration_WS_ReconnectResume|TestIntegration_CrossReplicaReplay" ./internal/runstream/...
```

| Test | What it proves |
|---|---|
| `TestMAXLEN_Scenario1_NormalRun` | 1,000 events replayed, done event closes channel |
| `TestMAXLEN_Scenario2_AtBoundary` | 5,000 events (at MAXLEN boundary) — ≥4,900 token events + 1 done received |
| `TestMAXLEN_Scenario3_OverMAXLEN` | 6,000 events with MAXLEN ~5,000 trim — ≥4,900 tokens received (oldest ~1,000 trimmed), done terminates |
| `TestMAXLEN_Scenario4_ToolHeavyMixed` | 200 tool_call + 200 tool_result + 400 token = 800 interleaved events, all counts exact |
| `TestMAXLEN_Scenario5_ReplayUnavailable` | 6,000 events + MAXLEN trim; cursor "1-0" (before oldest) → `replay_unavailable` emitted first, then tokens resume from oldest, done closes channel |
| `TestIntegration_WS_ReconnectResume` | LastEventID = event-3 cursor → only events 4 and 5 delivered (no duplicates of 1-3) |
| `TestIntegration_CrossReplicaReplay` | Events written via client-1 (replica-1 path) are fully replayed by client-2 (replica-2 path) from shared Redis |

**Trigger:** any change to `internal/runstream/streamer.go`, `streamid.go`, or `internal/cache/runstreamer_adapter.go`

---

## Suite 3 — Live deploy verification (`DEPLOY_AND_TEST.md`)

Manual checklist of 23 tests against a running Docker stack.
Run after every production deployment.
See `DEPLOY_AND_TEST.md` for full instructions.

| ID | Test | Purpose |
|---|---|---|
| T-01 | Liveness | Container is alive |
| T-02 | Readiness | DB + Redis both reachable |
| T-03 | Metrics | Prometheus scrape works |
| T-04 | Unauth admin | 401 enforced |
| T-05 | Bearer token valid | Token cache + DB query works |
| T-06–T-10 | Admin CRUD | Agents/orchestrators/apps CRUD via real DB |
| T-11–T-14 | WebSocket + orchestration | Full LLM round-trip, run persisted |
| T-15 | SSE | Streaming endpoint works |
| T-16–T-18 | A2A | JSON-RPC 2.0 protocol compliance |
| T-19 | Rate limit | Redis INCR keys created |
| T-20 | Token revocation | Redis pub/sub fires cross-pod |
| T-21 | Ghost session pruning | Shadow TTL expiry + atomic pruning |
| T-22 | Integration suite | `go test -tags=integration ./...` |
| T-23 | Go vs Python parity | Same agent count from both bridges |

---

## CI/CD pipeline mapping

| Stage | Suite | Trigger | Gate |
|---|---|---|---|
| **PR check** | S1 (unit) | Every push to any branch | Must pass — PR blocked if failing |
| **PR merge** | S1 + race | Merge to `main` | Must pass — merge blocked |
| **Staging deploy** | S1 + S2 (integration) | After merge to `main` | Must pass — prod deploy blocked |
| **Production deploy** | S1 + S2 + S3 (live) | Manual trigger after staging passes | Must pass — rollback if failing |
| **Nightly** | S1 + race + S2 | Scheduled 02:00 UTC | Alert on failure |

---

## Trigger map — what to run when you change what

| Changed file(s) | Run |
|---|---|
| `internal/config/config.go` | S1-01 |
| `internal/health/health.go` | S1-02 |
| `internal/server/server.go` | S1-03 |
| `internal/auth/jwt.go` | S1-04 + S1-31 |
| `internal/auth/token_cache.go` | S1-05 + S1-31 |
| `internal/auth/middleware.go` | S1-31 |
| `internal/auth/pgx_querier.go` | S1-31 |
| `internal/authserver/` (any file) or `cmd/auth-server/main.go` | S1-40 |
| `internal/tenantctx/tenantctx.go` | S1-32 |
| `internal/session/session.go` | S1-06 |
| `internal/event/bus.go` | S1-07 |
| `internal/domain/domain.go` | S1-08 |
| `internal/runrecorder/recorder.go` | S1-09 |
| `internal/orchestrator/orchestrator.go` | S1-28 |
| `internal/orchestrator/summary.go` | S1-28 |
| `internal/history/pgx.go` | S1-46 |
| `internal/summarizer/summarizer.go` | S1-47 |
| `internal/temporal/activities.go`, `internal/temporal/workflow.go` | S1-29 |
| `internal/llm/` (any file) | S1-10 |
| `internal/agentregistry/registry.go` | S1-11 |
| `internal/agentgen/` (any file) | S1-48 + S1-50 |
| `internal/agentgen/compiler.go` | S1-50 |
| `cmd/agent-runtime/main.go` | S1-48 + S1-50 + S1 (full suite) |
| `internal/admin/system_agents.go` | S1-15 + S1 (full suite) |
| `internal/dashboard/handler.go` | S1-52 |
| `internal/ws/handler.go` | S1-12 |
| `internal/sse/handler.go` | S1-13 |
| `internal/runstream/streamer.go`, `dispatcher.go`, `publisher.go`, `metrics.go`, `streamid.go` | S1-23 |
| `internal/cache/runstreamer_adapter.go` | S1-20 + S1-23 (integration) |
| `internal/cache/runstreamer_writer_adapter.go` | S1-20 + S1-23 |
| `cmd/worker/main.go` | S1-29 + S1 (full suite) |
| `internal/a2a/server.go` | S1-14 |
| `internal/execution/lifecycle.go` | S1-35 + S1-14 + S1-13 |
| `internal/execution/errors.go` | S1-35 + S1-13 |
| `internal/execution/request.go` | S1-35 + S1-13 |
| `internal/admin/` (any file) | S1-15 + S1-25 + S1-34 + S1-42 + S1-43 + S1-44 + S1-45 + S1-49 + S1-50 + S1-51 |
| `internal/admin/dal/` (any file) | S1-15 + S1-25 + S1-34 + S1-42 + S1-43 + S1-44 + S1-45 + S1-49 + S1-51 + S2-05 (integration) |
| `internal/admin/dal/agent_definitions_publish.go` | S1-51 |
| `internal/admin/dal/agent_bindings.go` | S1-51 |
| `internal/admin/dal/definitions.go` | S1-42 |
| `internal/admin/dal/registry.go` | S1-45 |
| `internal/admin/dal/llm_providers.go` | S1-25 + S2-05 (integration) |
| `internal/admin/dal/agent_definitions.go` | S1-49 |
| `internal/admin/service/` (any file) | S1-25 + S1-33 + S1-42 + S1-43 + S1-44 + S1-49 + S1-51 |
| `internal/admin/service/agent_definitions_publish.go` | S1-51 |
| `internal/admin/service/definitions.go` | S1-42 + S1-43 + S1-44 |
| `internal/admin/service/publish.go` | S1-43 + S1-44 |
| `internal/admin/dal/publish.go` | S1-43 + S1-44 |
| `internal/admin/service/agent_definitions.go` | S1-49 |
| `internal/admin/definitions.go` | S1-42 + S1-43 + S1-44 |
| `internal/admin/agent_definitions.go` | S1-49 |
| `internal/admin/registry.go` | S1-45 |
| `internal/admin/router.go` | S1-43 + S1-44 + S1-45 + S1-49 |
| `internal/crypto/fernet.go` | S1-26 |
| `internal/transport/transport.go` | S1-12 + S1-13 |
| `internal/metrics/metrics.go` | S1-27 |
| `internal/ratelimit/limiter.go` | S1-16 |
| `internal/gate/gate.go` | S1-17 |
| `internal/epconfig/epconfig.go` | S1-18 |
| `internal/epconfig/pgx.go` | S1-18 |
| `internal/cache/auth_adapter.go` | S1-19 |
| `internal/cache/runstream_adapter.go` | S1-20 |
| `internal/runstream/stream.go` | S1-21 |
| `internal/reconciler/reconciler.go` | S1-22 |
| `internal/registry/resolver.go`, `internal/registry/pgx.go`, `internal/registry/types.go` | S1-41 + S1-43 + S1-44 |
| `cmd/them/main.go` | S1-24 + S1 (full suite) |
| `go.mod` or `go.sum` | S1 (full suite) |
| `Dockerfile.go` | S1 + rebuild + S2 |
| `docker-compose.yml` | S2 + S3 T-01..T-05 |
| Any `internal/` file | S1 (full suite) |
| Before any production deploy | S1 + S2 + S3 |

---

## Rules — keeping this index current

These rules apply to every code change. They are non-negotiable.

1. **New test function added** → add a row to the relevant suite table above.
2. **Test renamed or deleted** → update or remove the row.
3. **New package with tests** → add a new `S1-XX` section.
4. **Coverage expands** → update the "What it proves" column.
5. **New CI stage wired** → update the CI/CD pipeline mapping table.
6. **This index is updated in the same commit as the code change** — never in a follow-up commit.

If a test is added without updating this index, the PR should not be merged.

---

## Total test count

| Suite | Package | Tests |
|---|---|---|
| S1-01 | config | 18 |
| S1-02 | health | 5 |
| S1-03 | server | 4 |
| S1-04 | auth/jwt | 9 |
| S1-05 | auth/token_cache | 5 |
| S1-06 | session | 10 |
| S1-07 | event | 9 |
| S1-08 | domain | 3 |
| S1-09 | runrecorder | 22 |
| S1-10 | llm | 6 |
| S1-11 | agentregistry | 10 |
| S1-12 | ws | 24 |
| S1-13 | sse | 23 |
| S1-14 | a2a | 27 |
| S1-15 | admin | 55 |
| S1-16 | ratelimit | 3 |
| S1-17 | gate | 16 |
| S1-18 | epconfig | 26 |
| S1-19 | cache | 1 |
| S1-20 | cache (runstream adapter) | 1 |
| S1-21 | runstream (pub/sub) | 10 |
| S1-22 | reconciler | 15 |
| S1-23 | runstream (streamer + dispatcher + publisher) | 21 |
| S1-24 | cmd/them (apps dispatcher) | 5 |
| S1-25 | admin/service | 69 |
| S1-26 | crypto (fernet) | 32 |
| S1-27 | metrics | 12 |
| S1-28 | orchestrator | 12 |
| S1-29 | temporal (worker + serialization + R-4d) | 10 |
| S1-30 | artifacts (download handler) | 9 |
| S1-31 | auth/tenant_middleware (R-4b) | 15 |
| S1-32 | tenantctx (R-4b) | 8 |
| S1-33 | admin/service tenant isolation (R-4c1) | 21 |
| S1-34 | admin tenant HTTP enforcement (R-4c2) | 12 |
| S1-35 | execution lifecycle (unification refactor) | 21 |
| S1-36 | admin agent action endpoints (Wave 8: discover/test/security-scan) | 8 |
| S1-40 | authserver (Go auth service) | 38 |
| S1-41 | registry (component definition resolver) | 12 |
| S1-42 | admin definitions (Phase B: application definition CRUD) | 12 |
| S1-43 | admin definitions validate (Phase C: ValidateDefinition) | 10 |
| S1-44 | admin definitions publish (Phase C: PublishDefinition) | 12 |
| S1-45 | admin registry handler (Phase D: ListComponentDefinitions) | 1 |
| S1-46 | history (DB role mapping + round-trip) | 4 |
| S1-47 | summarizer (LLM-based conversation summarizer) | 4 |
| S1-48 | agentgen (Phase 1 A2A Agent Runtime: invariants + interpreter) | 8 |
| S1-49 | agent definitions (Phase 2 Canvas A2A Builder CRUD) | 21 |
| S1-50 | agent definition compiler | 14 |
| S1-51 | agent definition publish service | 11 |
| S1-52 | dashboard WebSocket handler | 11 |
| **S1 total** | | **681** |
| S2-01 | integration | 4 |
| S2-02 | hybrid integration | 8 |
| S2-03 (streamer) | runstream streamer (Redis, in S1-23) | 1 |
| S2-03 (MAXLEN) | runstream MAXLEN + reconnect + cross-replica | 7 |
| S2-04 | admin tokens + sessions integration | 11 |
| S2-05 | admin/dal llm_providers integration | 11 |
| **S2 total** | | **42** |
| S3 live | manual | 23 |
| **`go test ./...` total** | | **652** |
