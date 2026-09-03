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
| `TestPublishDefinition_EPPublicAccessMode` | EP config.access_mode="public" → AccessPolicy {"mode":"public"} in upserted row |
| `TestPublishDefinition_EPDefaultAccessMode` | EP without config.access_mode → secure default {"mode":"token"} |

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
logout contract behaves like the Python service, OIDC authorization-code flow with PKCE works
end-to-end with a mock IdP, and secrets never leak into config logs.

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
| `TestHTTPMeReturnsTenantID` | GET /auth/me response includes `tenant_id` from the JWT claims (Step 16) |
| `TestHTTPLoginWithTenantSlug` | Login with matching `tenant_slug` succeeds; unknown slug → 403 (Step 16) |
| `TestOIDCStart_RedirectsToIdP` (OIDC-01) | start→discovery→302 to IdP with code_challenge S256 + signed state cookie set |
| `TestOIDCStart_MissingTenant` (OIDC-02) | missing `?tenant=` → 400 |
| `TestOIDCStart_UnknownTenant` (OIDC-03) | unknown slug → 404 |
| `TestOIDCStart_NoIDPConfig` (OIDC-04) | tenant exists but idp_config=null → 422 |
| `TestOIDCCallback_MissingParams` (OIDC-05) | callback with no state/code → 400 |
| `TestOIDCCallback_TamperedState` (OIDC-06) | HMAC mismatch on state → 400 |
| `TestOIDCCallback_MissingStateCookie` (OIDC-07) | valid state but no PKCE cookie → 400 |
| `TestOIDCCallback_HappyPath` (OIDC-08) | full flow: state OK → IdP exchange → UpsertOIDCUser → 302 with auth cookies + state cookie cleared + code_verifier forwarded |
| `TestStateSignVerify` (OIDC-09) | state sign/verify round-trip extracts correct slug |
| `TestStateVerifyRejectsWrongKey` (OIDC-10) | wrong key → error |
| `TestPKCECodeChallenge` (OIDC-11) | codeChallenge is deterministic, differs from verifier |
| `TestOIDCCallback_TokenCarriesTenantID` (OIDC-12) | issued access token carries correct `tenant_id` claim |
| `TestJWKS_ValidSignatureAccepted` (OIDC-13) | valid RS256 id_token + matching JWKS key → claims returned |
| `TestJWKS_TamperedSignatureRejected` (OIDC-14) | altered signature bytes → `verifyRS256IDToken` returns error |
| `TestJWKS_UnknownKidRejected` (OIDC-15) | token kid not in JWKS → error (no matching key) |
| `TestJWKS_WrongAlgRejected` (OIDC-16) | alg=HS256 in header → rejected before JWKS fetch |
| `TestJWKS_FetchErrorPropagated` (OIDC-17) | JWKS fetch failure → error propagated |
| `TestJWKSCache_HitAvoidsRefetch` (OIDC-18) | second verify call within TTL window uses cached doc, no second upstream fetch |
| `TestJWKSCache_ExpiredEntryRefetches` (OIDC-19) | TTL of 1ns → entry expires, next call fetches from upstream (2 total fetches) |
| `TestJWKSCache_UnknownKidTriggersRefetch` (OIDC-20) | cached doc has old-key; token carries new-key → re-fetch once, finds new key, succeeds |
| `TestHTTPTenantLookup_Found` (OIDC-21) | GET /tenant-lookup?email=user@acme.com → 200 with slug/display_name/idp_configured |
| `TestHTTPTenantLookup_NotFound` (OIDC-22) | email domain not registered → 404 |
| `TestHTTPTenantLookup_MissingEmail` (OIDC-23) | missing email param → 400 |
| `TestHTTPTenantLookup_InvalidEmail` (OIDC-24) | no @ in input → 400 |
| `TestOIDCCallback_GroupsMatchedRoleOverridden` (OIDC-25) | id_token includes groups claim matching a tenant group mapping → UpsertOIDCUser called with mapped role (not "viewer") |
| `TestOIDCCallback_GroupsUnmatchedDefaultRole` (OIDC-26) | groups present but none match any mapping → UpsertOIDCUser called with "viewer" |
| `TestOIDCCallback_NoGroupsDefaultRole` (OIDC-27) | id_token has no groups claim → UpsertOIDCUser called with "viewer" even when mappings are configured |

**Trigger:** any change to `internal/authserver/` (config, jwt, password, store, pgx, service,
handlers, router, oidc, oidc_store, oidc_jwks) or `cmd/auth-server/main.go`. Run `go test ./internal/authserver/...`.

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
| `TestCacheMissThenPopulate_TenantScopedKey` | SEC-03: Redis key is `them:agents:registry:{tenantA}` — global key `them:agents:registry` does NOT exist |
| `TestTenantIsolation_SameSlug` | SEC-03: tenantA and tenantB with same slug → separate cache entries, no sharing |
| `TestTenantInvalidation_DoesNotCrossContaminate` | SEC-03: invalidating tenantA does NOT evict tenantB's cache entries |
| `TestCrossTenatLookup_ReturnsMiss` | SEC-03: tenantA cannot retrieve an agent registered under tenantB (returns ErrUnknownAgent) |
| `TestPubSubChannelRegistered` | Pub/sub subscription established on `them:agents:changed` at startup |
| `TestUnknownSlug` | Unknown agent slug → `ErrUnknownAgent` (typed sentinel) |
| `TestInvokeForRun_CanvasA2A_UsesBindingID` | `canvas_a2a` transport → `GetBindingID` called; `X-Them-Binding-Id` header forwarded to agent-runtime |
| `TestInvokeForRun_NonCanvas_DelegatesToStandardRouting` | Non-canvas transport in `InvokeForRun` falls through to standard mock adapter dispatch |
| `TestExtractA2AResult_SingleArtifact_LegacyShape` | Single file part → legacy `{"artifact":{}}` shape preserved (backward compat) |
| `TestExtractA2AResult_MultiArtifact_TwoParts` | Two file parts in one artifact → `{"artifacts":[...]}` plural shape, both collected |
| `TestExtractA2AResult_MultiArtifact_TwoArtifacts` | Two separate Artifact objects → both collected in plural shape |
| `TestExtractA2AResult_MixedParts_OnlyFilePartsCollected` | Text-only parts skipped; only filename-bearing parts collected |
| `TestPubSubEmptyPayload_Ignored` | SEC-03: empty pub/sub payload → no eviction (guards against accidental global eviction) |
| `TestInvokeForRunStreaming_SingleArtifact_CallbackFired` | `SupportsStreaming=true` agent → SSE response parsed; `onArtifact` callback fired with correct filename/contentType/base64 |
| `TestInvokeForRunStreaming_NonStreamingFallback` | `SupportsStreaming=false` agent → `InvokeForRunStreaming` falls through to `InvokeForRun`; callback not used |

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
| `TestWS_VoiceEPReturns501` | Voice EP on WS path → 404 (voice served by HTTP handler, not WS/SSE lifecycle); gate and session never called |
| `TestWS_VoiceEPPublicReturns501` | Public voice EP on WS path → 404 before gate or session |
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
| `TestSSEVoiceEPReturns501` | Voice EP on SSE path → 404 (voice served by HTTP handler, not WS/SSE lifecycle); gate and session never called |
| `TestSSEVoiceEPPublicReturns501` | Public voice EP on SSE path → 404 before gate or session |
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
| `TestSSEFileEventForwardedAsArtifactUpdate` | `file` bus event forwarded as `artifact-update` SSE event with correct filename/content_type/url fields |

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
| `TestA2A_AgentCard_StreamingTrue` | agent card `/.well-known/agent.json` returns `streaming: true` in capabilities |
| `TestA2A_AgentCard_WithPublicURL` | `WithPublicURL("https://example.com")` → card URL uses public base, not request host |
| `TestA2A_AgentCard_DerivedURL` | No `WithPublicURL` set → card URL derived from request scheme + host |
| `TestA2A_AgentCard_SynthesizedCard` | CardLoader returns synthesized card → served with URL injected |
| `TestA2A_AgentCard_FallbackToOrchName` | CardLoader returns row with nil AgentCardJSON → fallback card uses OrchestratorDisplayName |
| `TestA2A_AgentCard_FallbackNoLoader` | No CardLoader configured → static "the-M Orchestrator" fallback served |
| `TestA2AStream_ContentType` | message/stream → 200 + text/event-stream content type |
| `TestA2AStream_EmitsCompletedStatus` | Successful stream → task-status-update completed event emitted |
| `TestA2AStream_MissingToken_401` | Missing token on token EP → 401 (clean HTTP, no SSE started) |
| `TestA2AStream_UnknownSlug_404` | Unknown slug → 404 |
| `TestA2AStream_CapExceeded_429` | Gate cap exceeded → 429 |
| `TestA2AStream_NoText_RPCError` | Empty message → JSON-RPC error |
| `TestA2AStream_FileEventForwardedAsArtifactUpdate` | `file` bus event forwarded as A2A `artifact-update` stream/event frame with correct URL/mediaType/name |
| `TestA2ASend_ResultIsSpecCompliant` | (A2A-WF01) `SendMessage` result object shape: `result.task.id`, `result.task.status.state == "TASK_STATE_COMPLETED"`, `result.task.artifacts[0].parts[0].text` — exact A2A v1.0 wire format |
| `TestA2AStream_TokenIsSpecCompliant` | (A2A-WF02) `SendStreamingMessage` token SSE frame shape: `data: {"result":{"artifactUpdate":{...}}}` — no `params`, no `kind`, exact v1.0 wire format |
| `TestA2AStream_ArtifactUpdateIsSpecCompliant` | (A2A-WF03) `SendStreamingMessage` completed-status SSE frame shape: `data: {"result":{"statusUpdate":{"status":{"state":"TASK_STATE_COMPLETED"}}}}` — no `isFinal` field in wire format |

**Trigger:** any change to `internal/a2a/server.go`, `internal/a2a/pgx.go`, `internal/execution/lifecycle.go`, or `internal/epconfig/pgx.go`

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
| `TestLifecycle_QuotaConcurrentRunsExceeded` | LC-QE-01: QuotaEnforcer returns ErrQuotaConcurrentRuns → AdmitErrQuotaConcurrentRuns (429); gate.Check never called |
| `TestLifecycle_QuotaRunsPerMinuteExceeded` | LC-QE-02: QuotaEnforcer returns ErrQuotaRunsPerMinute → AdmitErrQuotaRunsPerMinute (429); gate.Check never called |
| `TestLifecycle_QuotaMonthlyRunsExceeded` | LC-QE-04: QuotaEnforcer returns ErrQuotaMonthlyRuns → AdmitErrQuotaMonthlyRuns (429); gate.Check never called |
| `TestLifecycle_NilQuotaEnforcer` | LC-QE-03: no quota enforcer wired → quota check skipped; run admitted normally |

**Trigger:** any change to `internal/execution/lifecycle.go`, `internal/execution/errors.go`, `internal/execution/request.go`, or `internal/quota/enforcer.go`

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
| `TestTenantLLMProviders_List_200_Empty` | TLP-01: GET /admin/tenants/{id}/llm-providers → 200 with empty JSON array |
| `TestTenantLLMProviders_List_400_MissingID` | TLP-02: missing tenant id segment → non-200 |
| `TestTenantLLMProviders_Upsert_200` | TLP-03: PUT /admin/tenants/{id}/llm-providers/{name} success → 200; plaintext key not in response |
| `TestTenantLLMProviders_Upsert_404_PlatformNotFound` | TLP-04: provider name not in platform → 404 |
| `TestTenantLLMProviders_Upsert_400_BadJSON` | TLP-05: malformed request body → 400 |
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

### S1-60 · Provider key encryption — `internal/admin/service/provider_keys_test.go`

**Purpose:** Unit tests for per-app provider key encryption/decryption and `parseProviderKeys` format
detection. All paths exercised through the exported service API; the unexported `parseProviderKeys`
is reached indirectly. Includes regression tests for the empty-CT fall-through bug.

| Test | What it proves |
|---|---|
| `TestSetGetProviderKey_NewFormat_RoundTrip` | PK-1: `SetProviderKey` stores `{"ct":"…","hint":"1234"}` and `GetPlaintextProviderKey` recovers the plaintext after round-trip |
| `TestSetGetProviderKey_NoCryptoKey_RoundTrip` | PK-2: nil crypto key → `CT = "plain:…"` stored; `GetPlaintextProviderKey` strips prefix |
| `TestGetPlaintextProviderKey_LegacyFlatFormat` | PK-3: legacy `{"anthropic":"sk-ant-…"}` flat row → plaintext returned without error |
| `TestGetProviderKeys_ReturnsHint` | PK-4: `GetProviderKeys` returns `KeySet=true` + correct 4-char hint for non-empty CT |
| `TestSetProviderKey_UnsupportedProvider` | PK-5: unknown provider → `ErrUnprocessable` |
| `TestSetProviderKey_EmptyKey` | PK-6: empty key string → `ErrValidation` |
| `TestSetProviderKey_ShortKey_EmptyHint` | PK-7: key shorter than 4 chars → hint is `""` |
| `TestGetProviderKeys_EmptyCTStructured_ReturnsEmpty` | PK-8 (regression): `{"anthropic":{"ct":"","hint":""}}` → empty list, no error (was: parse error due to flat-unmarshal fall-through) |
| `TestGetPlaintextProviderKey_EmptyCTStructured_ReturnsEmpty` | PK-9 (regression): same all-empty CT row → empty string, no error |

**Trigger:** any change to `internal/admin/service/applications.go` (provider key methods), `go/cmd/agent-runtime/main.go` (`loadAppAPIKey`), OR `internal/admin/service/provider_keys_test.go`

---

### S1-62 · App global params — `internal/admin/service/app_global_params_test.go`

**Purpose:** Unit tests for app-level global named parameter CRUD: encryption of secrets, plaintext
storage of non-secrets, validation of name format and param type, masking on read, and plaintext
decryption roundtrip via `GetPlaintextAppParams`.

| Test | What it proves |
|---|---|
| `TestAppParam_SecretRoundtrip` | AGP-1: `SetAppParam` secret stores `{"ct":"...","hint":"XXXX"}`; `GetPlaintextAppParams` decrypts correctly |
| `TestAppParam_NonSecretStoredAsString` | AGP-2: non-secret stored as plain JSON string |
| `TestAppParam_BadName` | AGP-3: name with invalid chars → `ErrValidation` |
| `TestAppParam_BadType` | AGP-4: unsupported type → `ErrUnprocessable` |
| `TestAppParam_GetSecretMasked` | AGP-5: `GetAppParams` returns `IsSet=true`, `ValueHint` set, `Value` empty for secrets |
| `TestAppParam_GetNonSecretValue` | AGP-6: `GetAppParams` returns `Value` populated for non-secrets, no `ValueHint` |
| `TestAppParam_Delete` | AGP-7: `DeleteAppParam` calls DAL correctly |
| `TestAppParam_NilCryptoKey_Roundtrip` | AGP-8: nil crypto key → `plain:` prefix roundtrip works (test-mode) |

**Trigger:** any change to `internal/admin/service/applications.go` (app global param methods), `go/internal/admin/dal/applications.go` (GetAppParams/SetAppParam/DeleteAppParam), OR `db/045_app_global_params.sql`

---

### S1-63 · Compiler LLM nodes — `internal/agentgen/compiler_test.go`

**Purpose:** Verifies that `collectLLMNodes` correctly walks all skills and emits `AgentLLMNodeSpec` entries for LLM steps. Replaces old `collectAppParamRefs` / `AppParamRefs` tests (removed with the model_override_param_ref mechanism).

| Test | What it proves |
|---|---|
| `TestCompile_LLMNode_CollectedInSpec` | CMP-10: single LLM step → `LLMNodes` has one entry with correct provider/model |
| `TestCompile_MultipleLLMNodes` | CMP-11: two LLM steps across two skills → two entries in `LLMNodes` |
| `TestCompile_HTTPNode_BothKeyAndRef` | CMP-12: HTTP with both `app_param_key` and `app_param_ref` → `RequiredParams` still populated |
| `TestCompile_NoLLMNodes` | CMP-13: no LLM steps → `LLMNodes` is nil |
| `TestCompile_LLMNode_LabelFallback` | CMP-14: LLM step with no label field → `Label` falls back to node ID |

**Trigger:** any change to `internal/agentgen/compiler.go` (`collectLLMNodes`, `buildSpec`), `internal/agentgen/spec.go` (`AgentLLMNodeSpec`, `LLMNodes` field)

---

### S1-64 · Interpreter app_param_ref and NodeLLMOverride — `internal/agentgen/agentgen_test.go`

**Purpose:** Verifies runtime resolution of `app_param_ref` from `InvocationContext.AppGlobalParams` (HTTP path) and per-node LLM provider+model override from `InvocationContext.NodeLLMOverrides`.

| Test | What it proves |
|---|---|
| `TestInterpreter_HTTPStep_AppParamRef_Injected` | INT-10: `app_param_ref` + matching `AppGlobalParams` → `Authorization: Bearer` injected |
| `TestInterpreter_HTTPStep_AppParamRef_AbsentRequired` | INT-11: `app_param_ref` absent + non-empty `inject_mode` → error |
| `TestInterpreter_HTTPStep_AppParamRef_AbsentOptional` | INT-12: `app_param_ref` absent + empty `inject_mode` → silently skips |
| `TestInterpreter_HTTPStep_AppParamRef_TakesPrecedenceOverKey` | INT-13: `app_param_ref` takes precedence over `app_param_key` when both set |
| `TestInterpreter_LLMStep_NodeLLMOverride` | INT-14: `NodeLLMOverrides[nodeID]` → overrides compiled provider and model at execution |

**Trigger:** any change to `internal/agentgen/interpreter.go` (`execHTTP` AppParamRef block, `execLLM` NodeLLMOverrides block, `injectAuthParam`)

---

### S1-65 · Agent-runtime decodeAppGlobalParams — `cmd/agent-runtime/main_test.go`

**Purpose:** Unit tests for the pure `decodeAppGlobalParams` helper extracted from `loadAppGlobalParams`. Verifies decryption of secret entries (plain: test mode prefix), plain string pass-through, graceful handling of bad JSON and empty objects, and mixed blobs.

| Test | What it proves |
|---|---|
| `TestDecodeAppGlobalParams_SecretPlainPrefix` | RT-20: secret entry with `"ct":"plain:..."` → plaintext stripped and returned |
| `TestDecodeAppGlobalParams_PlainString` | RT-21: non-secret string entry → returned verbatim |
| `TestDecodeAppGlobalParams_BadJSON` | RT-22: malformed raw JSON → empty map, no panic |
| `TestDecodeAppGlobalParams_EmptyObject` | RT-23: empty `{}` → empty map |
| `TestDecodeAppGlobalParams_MixedEntries` | RT-24: secret + plain string in same blob → both decoded correctly |

**Trigger:** any change to `cmd/agent-runtime/main.go` (`decodeAppGlobalParams`, `loadAppGlobalParams`)

---

### S1-66 · Admin handler app params — `internal/admin/app_params_handler_test.go`

**Purpose:** HTTP handler-layer tests for `GET/PUT/DELETE /admin/applications/{id}/app-params[/{name}]`. Uses `bytesQueryFakeDB` and `fakeDB` to simulate DB responses without integration. Verifies status codes, response shapes, validation errors, and secret masking.

| Test | What it proves |
|---|---|
| `TestGetAppParams_Handler_200_Empty` | HTTP-20: GET returns 200 + empty array when no params stored |
| `TestGetAppParams_Handler_200_SecretParam` | HTTP-21a: secret param appears with is_set+value_hint; plaintext absent |
| `TestGetAppParams_Handler_200_StringParam` | HTTP-21b: non-secret param appears with value field |
| `TestSetAppParam_Handler_200_Secret` | HTTP-22a: PUT secret → 200 {name, updated: true} |
| `TestSetAppParam_Handler_200_String` | HTTP-22b: PUT string → 200 |
| `TestSetAppParam_Handler_400_BadName` | HTTP-23a: uppercase name → 400 validation |
| `TestSetAppParam_Handler_422_BadType` | HTTP-23b: unsupported type → 422 |
| `TestSetAppParam_Handler_400_BadJSON` | HTTP-23c: bad JSON body → 400 |
| `TestSetAppParam_Handler_400_EmptyValue` | HTTP-23d: empty value → 400 |
| `TestDeleteAppParam_Handler_200` | HTTP-24: DELETE → 200 {name, deleted: true} |
| `TestGetAppParams_Handler_404_AppMissing` | HTTP-25: pgx.ErrNoRows → 404 |

**Trigger:** any change to `internal/admin/applications.go` (GetAppParams, SetAppParam, DeleteAppParam handlers) or `internal/admin/service/applications.go` (app param service methods)

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

### S1-61 · Workerconfig provider key format — `internal/temporal/workerconfig/loader_test.go`

**Purpose:** Verifies that `PgxLoader` constructs without panicking and that `RunConfig` zero values
signal global-key fallback correctly. The `loadProviderKey` function is integration-tested via the
live DB path; these unit tests cover construction and type contracts only.

| Test | What it proves |
|---|---|
| `TestRunConfig_ZeroValue` | Zero-value `RunConfig` has empty LLMProvider+LLMAPIKey (global fallback signal) |
| `TestPgxLoader_NewPgxLoader` | `NewPgxLoader(nil, nil)` returns non-nil; `*PgxLoader` satisfies `Loader` interface |

**Trigger:** any change to `internal/temporal/workerconfig/loader.go`

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

**Purpose:** A2A Agent Runtime — proves security invariants (credential redaction, per-binding
isolation, tenant ownership), core interpreter step execution (input binding, template transform,
HTTP app-param injection in all 4 inject modes, data part variable injection), two-tier LLM key
resolution (platform → per-app), LLM model override via AgentParams, and config-driven routing
via Condition and Branch step types. No external services required.

| Test | What it proves |
|---|---|
| `TestInvocationContext_StringRedactsAppKey` | `InvocationContext.String()` never contains API key values |
| `TestAppAgentBinding_TwoBindingsDifferentPolicies` | Two `AppAgentBinding`s for the same agent but different apps have independent policies |
| `TestRedisTaskStore_CrossTenantIsolation` | Cross-tenant task read returns `ErrTaskNotFound`; cross-app read also returns `ErrTaskNotFound` |
| `TestRedisTaskStore_GetNonExistent` | Non-existent task ID → `ErrTaskNotFound` |
| `TestInterpreter_InputStep_BindsTextToVar` | Input step binds `text` part to named pipeline variable; response step reads it |
| `TestInterpreter_TransformStep` | Transform step renders Go template expressions over pipeline vars |
| `TestInterpreter_HTTPStep_StaticHeader` | HTTP step injects static header from `Headers` map |
| `TestInterpreter_HTTPStep_InjectMode_Header` | HTTP step injects `Authorization: Bearer <token>` from `ic.AgentParams` when inject_mode="header" |
| `TestInterpreter_HTTPStep_InjectMode_Query` | HTTP step appends query param from `ic.AgentParams` when inject_mode="query" |
| `TestInterpreter_HTTPStep_InjectMode_CustomHeader` | HTTP step sets custom header from `ic.AgentParams` when inject_mode="custom_header" |
| `TestInterpreter_HTTPStep_InjectMode_Basic` | HTTP step sets `Authorization: Basic ...` from `ic.AgentParams` when inject_mode="basic" |
| `TestInterpreter_HTTPStep_NoInject_WhenParamEmpty` | No injection (and no error) when param key not set and inject_mode="" |
| `TestInterpreter_AgentCard_PathIsAgentCardJSON` | Documents A2A well-known path is `/.well-known/agent-card.json` |
| `TestInterpreter_LLMStep_FallsBackToInput` | LLM step with no `user_prompt` config falls back to `vars["input"]` |
| `TestInterpreter_LLMStep_ExplicitUserPromptOverridesInput` | Explicit `user_prompt` template takes priority over `vars["input"]` |
| `TestInterpreter_DataPartVars_AvailableInTemplate` | Data part fields passed as `extraVars` are available as pipeline vars |
| `TestInterpreter_DataPartVars_DoNotOverwriteExplicitInput` | `vars["input"]` from text part set before data vars; extra keys don't clobber input |
| `TestInterpreter_LLMStep_TwoTier_PlatformKey` | No AppAPIKey → platform env key used |
| `TestInterpreter_LLMStep_TwoTier_AppKeyOverridesPlatform` | `AppAPIKey["anthropic"]` set → overrides platform key |
| `TestInterpreter_LLMStep_TwoTier_EmptyAppKeyFallsBack` | `AppAPIKey["anthropic"]=""` → falls back to platform key |
| `TestInterpreter_ConditionStep_PassPath` | Condition step: truthy expression ("hello") → routes to PassNext |
| `TestInterpreter_ConditionStep_FailPath` | Condition step: falsy expression ("") → routes to FailNext |
| `TestInterpreter_BranchStep_TruePath` | Branch step: `{{eq .x "yes"}}` with x=yes renders "true" → TrueNext |
| `TestInterpreter_BranchStep_FalsePath` | Branch step: `{{eq .x "yes"}}` with x=no renders "false" → FalseNext |
| `TestInterpreter_TransformStep_JSONExtractions` | Transform JSONPath extractions: parses a JSON string var (LLM output) and assigns fields to named pipeline vars |
| `TestInterpreter_TransformStep_JSONExtract_FromMap` | Transform JSONPath extractions: parses a map[string]any (http_response) and extracts a field to a named var |
| `TestInterpreter_BranchStep_EdgeFallback` | Branch step with empty TrueNext/FalseNext config routes via Next[0]=true, Next[1]=false edge order |
| `TestInterpreter_HTTPStep_FormKey_URLEncodes` | When `form_key` is set, body_template is percent-encoded and sent as `{key}={encoded}` (required for Overpass QL with colon tags like `diet:kosher`) |

**Trigger:** any change to `internal/agentgen/` (spec.go, context.go, binding.go, redistaskstore.go, interpreter.go, nodes.go) or `cmd/agent-runtime/main.go`

---

### S1-67 · Stage 6 runtime contract enforcement — `internal/agentgen/interpreter_contracts_test.go`

**Purpose:** Verifies Stage 6 scoped input resolution and output-only promotion in the interpreter.
Proves that nodes receive only their declared Inputs, undeclared global vars are invisible,
undeclared output writes are dropped, Required missing inputs return ErrContractViolation (unwrappable
via errors.As), optional missing inputs are silently tolerated, fan-out (multiple steps reading
the same upstream output) works correctly, legacy steps without compiled contracts fall through
to the global-vars path unchanged, and transform outputs are derived from functions[].output_var only.

| Test | What it proves |
|---|---|
| `TestInterpreter_Contract_EndToEnd` (CONT-1) | Fully compiled pipeline with scoped contracts produces correct result end-to-end |
| `TestInterpreter_Contract_ScopedInput_UndeclaredVarNotVisible` (CONT-2) | Globally present var absent from step.Inputs is not visible to the node |
| `TestInterpreter_Contract_OutputPromotion_UndeclaredWriteDropped` (CONT-3) | Var written by node but absent from step.Outputs is not promoted to global state |
| `TestInterpreter_Contract_MissingRequiredInput_Error` (CONT-4) | Required input absent at runtime → ErrContractViolation with correct StepID/VarName/Kind |
| `TestInterpreter_Contract_MissingOptionalInput_NoError` (CONT-5) | Optional input absent at runtime → no error; step executes normally |
| `TestInterpreter_Contract_FanOut_TwoStepsReadSameVar` (CONT-6) | Two sequential steps both reading same upstream-promoted var each receive the value correctly |
| `TestInterpreter_Contract_LegacyFallback_NoContractPassesGlobalVars` (CONT-7) | Steps with no Inputs/Outputs use full global vars (backward-compatible legacy path) |
| `TestInterpreter_Contract_ErrorIsUnwrappable` (CONT-8) | ErrContractViolation is unwrappable via errors.As even when wrapped by step error context |
| `TestInterpreter_Contract_Transform_OutputsFromFunctionOutputVar` (CONT-9) | Transform outputs come from functions[].output_var only; no exposed_vars or parallel mechanism |
| `TestInterpreter_Contract_BranchStep_ScopedInputRoutes` (CONT-10) | Branch step with scoped Inputs still routes correctly via nextStepOverride (true and false paths) |
| `TestInterpreter_Contract_ScopedRebuildPerStep` (CONT-11) | Scoped vars rebuilt from current global state per step; upstream promotions visible to next step |
| `TestErrContractViolation_ErrorString` (CONT-12) | ErrContractViolation.Error() contains StepID, VarName, and Kind |

**Trigger:** any change to `internal/agentgen/interpreter.go`, `internal/agentgen/spec.go`, or `internal/agentgen/nodes.go`

---

### S1-68 · Explicit canvas data bindings — `internal/agentgen/bindings_test.go`

**Purpose:** Verifies Stage A of explicit data bindings: per-step `inputs` binding map in canvas JSON,
compiler binding resolution (PortID/SourceStep/SourcePort on VarRef), BROKEN_BINDING validation at
both Validate (warning) and CompileForPublish (error) severity, static PortDef declarations on LLM
and Response node types, and backward compatibility for canvas JSON with no `inputs` field.

| Test | What it proves |
|---|---|
| `TestBindings_NoExplicitBindings_Clean` (BND-1) | Canvas with no `inputs` fields compiles without BROKEN_BINDING issues |
| `TestBindings_ExplicitBinding_PopulatesSourceFields` (BND-2) | Explicit binding from valid step/port annotates VarRef with SourceStep/SourcePort |
| `TestBindings_BrokenBinding_UnknownSourceStep_Validate` (BND-3) | Non-existent from_step → BROKEN_BINDING warning on Validate |
| `TestBindings_BrokenBinding_UnknownSourceStep_Publish` (BND-4) | Non-existent from_step → BROKEN_BINDING error on CompileForPublish |
| `TestBindings_BrokenBinding_UnknownSourcePort_Validate` (BND-5) | Valid from_step but non-existent from_port → BROKEN_BINDING warning on Validate |
| `TestBindings_BrokenBinding_UnknownSourcePort_Publish` (BND-6) | Valid from_step but non-existent from_port → BROKEN_BINDING error on CompileForPublish |
| `TestBindings_LLMNodeHasStaticPorts` (BND-7) | LLM node type has InputPorts + OutputPorts with stable ID "output" |
| `TestBindings_ResponseNodeHasInputPortNoOutputPort` (BND-8) | Response node (sink) has InputPorts, no OutputPorts |
| `TestBindings_VarRefJSONRoundTrip` (BND-9) | VarRef with PortID/SourceStep/SourcePort round-trips through JSON |
| `TestBindings_NoInputsField_BackwardCompat` (BND-10) | Classic canvas JSON (no `inputs` field) compiles identically to pre-binding behaviour |

**Trigger:** any change to `internal/agentgen/compiler.go`, `internal/agentgen/noderegistry.go`, or `internal/agentgen/nodes.go`

---

### S1-69 · MCP call node and executor — `internal/agentgen/mcp_test.go`

**Purpose:** Verifies the `mcp_call` canvas step: node registration, validation, DeriveInputs/DeriveOutputs, runtime execution via MCPCaller, error propagation, args template rendering, and full pipeline compilation.

| Test | What it proves |
|---|---|
| `TestMCP_NodeRegistered` (MCP-1) | StepMCPCall is registered with non-nil Execute |
| `TestMCP_Validate_MissingFields` (MCP-2) | Empty mcp_call config → INVALID_CONFIG errors for mcp_server_slug, tool_name, output_var |
| `TestMCP_Validate_ValidConfig` (MCP-3) | Complete config → no validation errors from compiler |
| `TestMCP_DeriveOutputs` (MCP-4) | DeriveOutputs returns VarRef for output_var |
| `TestMCP_DeriveInputs_Template` (MCP-5) | DeriveInputs extracts template var references from args_template |
| `TestMCP_Execute_NoCaller` (MCP-6) | Execute without MCPCaller → error (MCP_SERVICE_URL unset) |
| `TestMCP_Execute_CallsCallerAndSetsVar` (MCP-7) | Execute with stub MCPCaller → result stored in pipeline var; correct appID/slug/tool passed |
| `TestMCP_Execute_CallerError` (MCP-8) | MCPCaller error propagates through Execute |
| `TestMCP_Execute_ArgsTemplateRendered` (MCP-9) | args_template renders pipeline vars into JSON args |
| `TestMCP_Compiles_InPipeline` (MCP-10) | Full canvas pipeline with mcp_call compiles without errors |

**Trigger:** any change to `internal/agentgen/nodes.go`, `internal/agentgen/mcp_caller.go`, `internal/agentgen/interpreter.go`, or `internal/agentgen/spec.go`

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

**Purpose:** BuildValidator architecture — validates the compiler that transforms canvas JSONB into a
topologically-ordered AgentSpec. Covers all rejection codes, cycle-detection / topo-sort, severity
split (warning at validate / error at publish), and structured Issue fields (SkillID/NodeID/Field).

`CompileError` is a type alias for `Issue` — tests use both names interchangeably.

| Test | What it proves |
|---|---|
| `TestCompile_EmptyDefinition` | missing display_name → MISSING_FIELD error |
| `TestCompile_InvalidJSON` | invalid JSON → INVALID_JSON error |
| `TestCompile_MinimalValid` | minimal valid definition → non-nil spec with correct IDs |
| `TestCompile_DefaultVersionFallback` | missing version → defaults to "1.0.0" |
| `TestCompile_SlugSanitized` | hyphens in slug → sanitized to underscores (no error) |
| `TestCompile_EmptyAgentRoot_NoSkills` | agent with no skills compiles cleanly |
| `TestCompile_DuplicateSkillID` | two skills same skill_id → DUPLICATE_SKILL |
| `TestCompile_DuplicateStepID` | two steps same id in one skill → DUPLICATE_STEP |
| `TestCompile_UnknownStepType` | step with unknown type → UNKNOWN_STEP_TYPE |
| `TestCompile_HTTPNode_AcceptsConfig` | http step without app_param_key compiles cleanly |
| `TestCompile_LLMNode_AcceptsConfig` | llm step compiles cleanly and is collected into LLMNodes |
| `TestCompile_HTTPNode_AppParams` | http step with `app_param_key: "bearer_token"` → RequiredParams contains composite key `step1:bearer_token` with UsedByNodes |
| `TestCompile_HTTPNode_FreeFormAppParamKey` | http step with free-form `app_param_key: "geoapify_key"` → RequiredParams contains composite key `step1:geoapify_key` |
| `TestCompile_NoParams` | agent with no param-aware nodes → empty RequiredParams |
| `TestCompile_DanglingNextRef` | step.next refs nonexistent step → DANGLING_NEXT |
| `TestCompile_CycleDetected` | A→B→A cycle → CYCLE_DETECTED |
| `TestCompile_SpecHasNoCredentialSlots` | compiled spec has no CredentialSlots (removed) |
| `TestCompile_TopologicalOrder` | linear chain compiled in execution order |
| `TestValidate_StubNodeIsWarning` | Validate(): stub node (a2a_call) → NODE_NOT_EXECUTABLE severity=warning |
| `TestValidate_StubNodeDoesNotBlockSpec` | Validate(): stub nodes present → spec still non-nil (canvas can render) |
| `TestCompileForPublish_StubNodeIsError` | CompileForPublish(): stub node → NODE_NOT_EXECUTABLE severity=error |
| `TestCompileForPublish_ImplementedOnlySucceeds` | CompileForPublish(): implemented-only graph → no errors, non-nil spec |
| `TestIssue_StructuredFields` | UNKNOWN_STEP_TYPE issue carries skill_id and node_id populated correctly |

**Trigger:** any change to `internal/agentgen/compiler.go` or `internal/agentgen/spec.go`

---

### S1-65 · Data-flow derivation and Stage 5 validation — `internal/agentgen/dataflow_test.go`

**Purpose:** VarRef derivation (DeriveInputs/DeriveOutputs) for all 11 node types and Stage 5
`validateDataFlow` (UNRESOLVED_INPUT detection). Verifies that compiled StepSpec carries correct
Inputs/Outputs for each node type, that missing upstream writers emit the right severity
(warning at validate, error at publish for Required inputs), and that "input" is always pre-seeded.

| Test | What it proves |
|---|---|
| `TestDataFlow_Input_DefaultOutput` | input node outputs "input" by default |
| `TestDataFlow_Input_BindingOutput` | input node outputs binding name when bindings.text is set |
| `TestDataFlow_Input_ImplicitInput` | input node DeriveInputs includes "input" as non-required |
| `TestDataFlow_LLM_UserPromptTemplateVars` | LLM extracts template vars from user_prompt |
| `TestDataFlow_LLM_EmptyUserPromptIncludesInput` | LLM with no user_prompt includes "input" in Inputs |
| `TestDataFlow_LLM_SetUserPromptExcludesInput` | LLM with user_prompt set does NOT include "input" |
| `TestDataFlow_LLM_DefaultOutput` | LLM output defaults to "output" when output_var unset |
| `TestDataFlow_LLM_ConfiguredOutput` | LLM output uses configured output_var |
| `TestDataFlow_HTTP_TemplateVarInputs` | HTTP inputs = template vars from url_template + body_template (deduped) |
| `TestDataFlow_HTTP_AlwaysOutputsHTTPResponse` | HTTP always outputs "http_response" |
| `TestDataFlow_HTTP_ExtractionOutputs` | HTTP outputs extraction var names |
| `TestDataFlow_Transform_InputsAndOutputs` | Transform inputs = unique input_vars; outputs = unique output_vars |
| `TestDataFlow_Response_InputRequired` | Response inputs from_var with Required=true; empty outputs |
| `TestDataFlow_Response_DefaultFromVar` | Response defaults from_var to "output" |
| `TestDataFlow_Branch_ExpressionVars` | Branch inputs = template vars from expression (incl. nested .var in actions) |
| `TestDataFlow_Loop_AccumVar` | Loop outputs accum_var; inputs = condition template vars |
| `TestDataFlow_Parallel_MergeVar` | Parallel outputs merge_var when set |
| `TestDataFlow_A2ACall_InputsAndOutputs` | A2A Call input_var (Required=true), output_var |
| `TestDataFlow_HumanWait_ReplyVar` | HumanWait outputs reply_var |
| `TestDataFlow_Stage5_ResolvedFlow` | Fully resolved pipeline (LLM writes → response reads) → no UNRESOLVED_INPUT |
| `TestDataFlow_Stage5_MissingResponseVar_PublishFails` | Response reading unwritten var → UNRESOLVED_INPUT error at publish |
| `TestDataFlow_Stage5_MissingResponseVar_ValidateWarns` | Same → warning (not error) at validate |
| `TestDataFlow_Stage5_LLMTemplateVarUnresolved` | LLM template var with no upstream writer → UNRESOLVED_INPUT warning |
| `TestDataFlow_Stage5_InputVarAlwaysAvailable` | "input" is pre-seeded — never causes UNRESOLVED_INPUT |
| `TestDataFlow_Stage5_FullPipeline_NoIssues` | Correct full pipeline (input→LLM→response) has no data-flow issues |
| `TestDataFlow_Stage5_PreBranchVar_AvailableOnBothArms` | Var written before branch is guaranteed on both arms (path-sensitive) |
| `TestDataFlow_Stage5_Branch_UnwrittenVarOnArm_PublishFails` | Unwritten var read on branch arm → UNRESOLVED_INPUT error at publish |
| `TestDataFlow_Stage5_Branch_ArmInternalFlow_NoIssue` | Var written within arm, read by successor on same arm → no issue |

**Trigger:** any change to `internal/agentgen/compiler.go`, `internal/agentgen/nodes.go`,
`internal/agentgen/noderegistry.go`, or `internal/agentgen/spec.go`

---

### S1-72 · ExecutionPlan compiler — `internal/agentgen/plan_compiler_test.go`

**Purpose:** Phase 0 of DAG execution. Verifies that `CompileExecutionPlan` produces a correct
`ExecutionPlan` with join annotations from a `SkillSpec`. Also covers the ExecutionPolicy
resolution logic (`resolvePolicy`) — per-node defaults, HTTP method upgrade, MCP mutation
heuristic, user override clamping, and backward compatibility.

| Test | What it proves |
|---|---|
| `TestCompileExecutionPlan_Linear` | A→B→C linear chain: no join nodes, correct `Next` pointers, correct `StartID` |
| `TestCompileExecutionPlan_FanOutJoin` | Diamond DAG (s1→s2a,s2b→s3): s1 fans out; s3 gets `JoinWaitAll` + `JoinOf=[s2a,s2b]` |
| `TestCompileExecutionPlan_Branch` | Branch DAG (s1→br→s_true/s_false→s_end): s_end gets `JoinBranchMerge` (arms are mutually exclusive) |
| `TestCompileExecutionPlan_MixedFanOut` | LLM parallel fan-out (s2→s3a,s3b→s4): s4 gets `JoinWaitAll` (non-Branch fan-out source) |
| `TestCompileExecutionPlan_BranchMerge` | Branch convergence (br→s_true/s_false→s_end): s_end gets `JoinBranchMerge`; arm nodes get `JoinNone` |
| `TestCompileExecutionPlan_Nil` | nil input and empty-step skill both return non-nil plan with zero nodes; no panic |
| `TestResolvePolicy_HTTPGet` | HTTP GET → MaxAttempts=3, RequiresIdempotencyKey=false |
| `TestResolvePolicy_HTTPPost` | HTTP POST → MaxAttempts=1, RequiresIdempotencyKey=false (no retry → key not required) |
| `TestResolvePolicy_HTTPPostRetryRequiresKey` | HTTP POST + canvas override MaxAttempts=2 → RequiresIdempotencyKey=true |
| `TestResolvePolicy_HTTPEmptyMethod` | HTTP empty method treated as GET → MaxAttempts=3 |
| `TestResolvePolicy_LLM` | LLM default → MaxAttempts=2, InitialIntervalSeconds=2.0 |
| `TestResolvePolicy_UserOverrideClamped` | Canvas override MaxAttempts=10 clamped to MaxPolicy.MaxAttempts=3 |
| `TestResolvePolicy_NonRetryableNotOverridable` | Canvas override cannot clear NonRetryableErrors |
| `TestResolvePolicy_ZeroValBackwardCompat` | Zero-value policy (old compiled agents): executor guard converts MaxAttempts=0 → 1 |
| `TestCompileExecutionPlan_PolicyPopulated` | All nodes in compiled plan have non-zero MaxAttempts, TimeoutSeconds, NonRetryableErrors |
| `TestResolvePolicy_MCPMutatingVsRead` | MCP read → MaxAttempts=2, RequiresIdempotencyKey=false; mutating → MaxAttempts=1, RequiresIdempotencyKey=false (no retry) |
| `TestResolvePolicy_MCPMutatingOverrideHardClamped` | EP-10: canvas override MaxAttempts=3 on mutating MCP tool is hard-clamped to 1 (no canvas override can raise it) |
| `TestValidateLoopBodies_UnknownBodyStep` | PC-LOOP-1: body_steps references unknown step ID → error returned |
| `TestValidateLoopBodies_HistoryBudgetExceeded` | PC-LOOP-2: 51 body steps × 100 iterations = 5100 > MaxLoopHistoryBudget → error returned |
| `TestValidateLoopBodies_Valid` | PC-LOOP-3: valid loop (1 body step × 10 iterations) passes without error |
| `TestCompileLoopBodyPlan_BranchJoinInsideBody` | PC-LOOP-4: branch→arm_t/arm_f→join body plan: branch has 2 Next, join gets JoinBranchMerge + JoinOf[2], join terminal has no Next |
| `TestCompileLoopBodyPlan_TerminalExcludesPostLoop` | PC-LOOP-5: body step with Next pointing outside body: intra-body Next trimmed to [] (post-loop edge removed) |
| `TestResolveLoopOuterNext_UsesLoopStepNext` | PC-LOOP-6: loopStep.Next=[post_loop] → resolveLoopOuterNext returns [post_loop] (no legacy scan needed) |

**Trigger:** any change to `internal/agentgen/plan_compiler.go`, `internal/agentgen/spec.go`
(`ExecutionPlan`, `PlanNode`, `JoinMode`, `ExecutionPolicy`, `LoopConfig` types), `internal/agentgen/nodes.go`
(per-node DefaultPolicy/MaxPolicy, loop `BodySteps` handling), or loop SubPlan compilation logic

---

### S1-73 · LocalExecutor (DAG runtime Phase 1) — `internal/agentgen/local_executor_test.go`

**Purpose:** Phase 1 of DAG execution. Verifies `LocalExecutor` correctly executes compiled
`ExecutionPlan`s: linear chains, goroutine fan-out, wait_all join with var merging, error
propagation + context cancellation, deep-copy isolation, and nil/empty plan safety.
Also covers `execNode` ExecutionPolicy enforcement: per-attempt timeout (StartToCloseTimeout
semantics), exponential backoff retry, vars isolation across retry attempts, non-retryable
error detection via `NonRetryableError` interface, and idempotency guard.

| Test | What it proves |
|---|---|
| `TestLocalExecutor_Linear` | A→B→C chain executes in order; vars propagate correctly |
| `TestLocalExecutor_FanOut` | Fan-out (s1→s2a,s2b) both branches run (counter=2) |
| `TestLocalExecutor_Join_WaitAll` | Both branches arrive at join; merged vars contain keys from both branches |
| `TestLocalExecutor_JoinFailure_CancelsOtherBranch` | Error in one branch cancels ctx; slow sibling unblocks; error is returned |
| `TestDeepCopyVars_NestedMap` | `deepCopyVars` deep-copies nested maps and slices; mutation of copy doesn't affect original |
| `TestLocalExecutor_Nil` | nil plan and empty plan both return a non-nil error cleanly |
| `TestLocalExecutor_BranchTrue` | Branch routes to true arm; s_end runs exactly once via JoinBranchMerge |
| `TestLocalExecutor_BranchFalse` | Branch routes to false arm; s_end runs exactly once via JoinBranchMerge |
| `TestLocalExecutor_DeterministicMerge` | 50 iterations: when two branches write same key, JoinOf order determines winner deterministically |
| `TestLocalExecutor_CausalErrorPreserved` | 20 iterations: causal error survives context.Canceled from sibling cancellation |
| `TestExecNodeTimeout` | execNode with TimeoutSeconds=1 cancels slow step's context and returns error |
| `TestExecNodeNoTimeoutWhenZero` | execNode with TimeoutSeconds=0 adds no deadline to the node's context |
| `TestExecNodeRetry_SucceedsOnThirdAttempt` | MaxAttempts=3, step fails first 2 attempts, succeeds on 3rd — exactly 3 calls |
| `TestExecNodeRetry_ExhaustsAttempts` | MaxAttempts=3, all attempts fail — exactly 3 calls, last error returned |
| `TestExecNodeRetry_ContractViolationIsNonRetryable` | *ErrContractViolation stops after 1 attempt even with MaxAttempts=3 |
| `TestExecNodeRetry_CancelledStopsImmediately` | Pre-cancelled context stops execution with ≤1 attempt |
| `TestExecNodeRetry_IdempotencyKeyMissing` | RequiresIdempotencyKey=true + MaxAttempts=2 + no header → *ErrIdempotencyKeyMissing |
| `TestExecNodeRetry_IdempotencyKeyPresent_AllowsExecution` | RequiresIdempotencyKey=true + MaxAttempts=2 + Idempotency-Key header → guard does not fire |
| `TestExecNodeRetry_PerAttemptTimeout` | TimeoutSeconds applies per-attempt (StartToCloseTimeout): DeadlineExceeded from attempt 1 is non-retryable, stops after 1 call |
| `TestExecNodeRetry_VarsIsolation` | Failed attempt's var writes are not visible to the next attempt's input vars |
| `TestExecNodeRetry_NonRetryableInterface` | Error implementing NonRetryableError.IsNonRetryable()=true stops after 1 attempt (interface-based detection) |
| `TestExecuteNodeForActivity_IdempotencyGuard` | ExecuteNodeForActivity enforces idempotency guard: RequiresIdempotencyKey=true + MaxAttempts=2 + no header → *ErrIdempotencyKeyMissing |
| `TestExecuteNodeForActivity_IdempotencyGuard_MaxAttempts1_Skips` | RequiresIdempotencyKey=true but MaxAttempts=1 → guard does not fire (no retry, no protection needed) |
| `TestIsNonRetryable_NoStringMatch` | EP-L14: NonRetryableErrors string list NOT checked in LocalExecutor — plain error containing "InvalidConfig" is NOT non-retryable |
| `TestExecNodeRetry_FreshClonePerAttempt` | EP-L15: each retry attempt gets a fresh interp.clone(); nextStepOverride from attempt 1 is not visible in attempt 2 |
| `TestResolveMaxConcurrentTasks_Zero` | 0 and negative → DefaultMaxConcurrentTasks (10); values > SystemMaxConcurrentTasks → clamped to 100 |
| `TestLocalExecutor_ConcurrencyLimit` | Fan-out of 5 nodes with limit=2: high-water mark of simultaneous executions never exceeds 2 |
| `TestLocalExecutor_ConcurrencyLimit_Cancellation` | Context cancel while nodes wait at semaphore: Execute returns promptly (no deadlock, 5s timeout) |
| `TestLocalExecutor_Loop_BasicIteration` | EP-LOOP-1: 3 items → body runs 3 times (callCount verified) |
| `TestLocalExecutor_Loop_MaxIterations` | EP-LOOP-2: max_iterations=2 caps 5-item list to 2 body runs |
| `TestLocalExecutor_Loop_MissingItemsVar` | EP-LOOP-3: absent items_var → no-op, body never runs, no error |
| `TestLocalExecutor_Loop_NonListItemsVar` | EP-LOOP-4: items_var holds a string → execution error returned |
| `TestLocalExecutor_Loop_NilSubPlan` | EP-LOOP-5: nil SubPlan → no-op, no error |
| `TestLocalExecutor_Loop_BranchInsideBody` | EP-LOOP-6: branch inside body — items=["true","false","true"]: trueCount=2, falseCount=1 (Parallel+Branch via DAG machinery) |
| `TestLocalExecutor_Loop_IterationIsolation` | EP-LOOP-7: iter 0 writes "sentinel" to body_out; iter 1 starts fresh and must NOT see "sentinel" from iter 0 |
| `TestLocalExecutor_Loop_ScopedAccumulation` | EP-LOOP-8: accum_var entries contain only declared "body_out" — not item_var ("current_item") or outer "items" |

**Trigger:** any change to `internal/agentgen/local_executor.go`, `internal/agentgen/node_executor.go`,
`internal/agentgen/plan_compiler.go`, `internal/agentgen/spec.go` (`NonRetryableError` interface,
`ErrContractViolation`, `ErrIdempotencyKeyMissing`), `internal/agentgen/context.go`
(InvocationPolicies, ResolveMaxConcurrentTasks), `internal/agentgen/nodes.go`
(ExecutionPolicy fields used by execNode, loop Execute), or `StepLoop` config changes

---

### S1-74 · DAG E2E smoke tests — `internal/agentgen/agentgen_test.go` (Phase 2)

**Purpose:** Full end-to-end integration of `CompileExecutionPlan` + `LocalExecutor` +
real `Interpreter.executeStep` using production node types (`StepInput`, `StepBranch`,
`StepTransform`, `StepResponse`). These tests exercise the complete canvas execution stack
without mocking any internal layer.

| Test | What it proves |
|---|---|
| `TestDAG_BranchConvergence_TruePath` | `input="yes"` → Branch true arm → `upper("yes")` → Response `"YES"`; compiler emits `JoinBranchMerge` for the convergence node |
| `TestDAG_BranchConvergence_FalsePath` | `input="no"` → Branch false arm → `lower("no")` → Response `"no"`; same skill, other arm taken |
| `TestDAG_ParallelTransforms_BothBranchesRun` | Non-Branch fan-out (Input→arm_a,arm_b→join): compiler emits `JoinWaitAll`; both `upper` and `lower` transforms run; merged vars contain both outputs |

**Trigger:** any change to `internal/agentgen/agentgen_test.go` (E2E helpers),
`internal/agentgen/local_executor.go`, `internal/agentgen/plan_compiler.go`,
`internal/agentgen/nodes.go`, or `internal/agentgen/spec.go`

---

### S1-75 · Phase 4-A: ExecuteNodeForActivity, ActivityIC, ExecutionBackend — `internal/agentgen/node_executor_test.go`

**Purpose:** Verifies the narrow Temporal-independent adapter (`ExecuteNodeForActivity`),
the credential-safe `ActivityIC` type, and the `ExecutionBackend` field wired end-to-end
through compiler → `AgentSpec`. Includes a security test that confirms secrets
(`AppAPIKey`, `AgentParams`, `AppGlobalParams`) are never copied into `ActivityIC`.

| Test | What it proves |
|---|---|
| `TestNA01_InputNode_WritesInputVar` | Input node executes cleanly; NextOverride and ResultText are empty |
| `TestNA02_ResponseNode_CapturesResultText` | Response node populates ResultText and ResultMT |
| `TestNA03_BranchNode_SetsNextOverride` | Branch true/false paths each set the correct NextOverride |
| `TestNA04_NilInterp_ReturnsError` | nil interpreter returns an error, no panic |
| `TestNA05_UnknownNodeType_ReturnsError` | Unknown StepType returns an error |
| `TestNA06_ContextCancellation_Propagates` | Cancelled context does not cause a panic |
| `TestNA07_IsolatedState_ConcurrentCallsDoNotShare` | 100 concurrent Clone()→Execute calls never share nextStepOverride |
| `TestNA08_ScopedOutputProjection` | Undeclared output key "secret" absent from out.Vars; ResultText correct |
| `TestNA09_ErrContractViolation_MissingRequiredInput` | Missing required input → *ErrContractViolation |
| `TestNA10_ActivityIC_Validate` | Missing TenantID/ApplicationID/AgentID → error; BindingID optional |
| `TestNA11_ActivityICFromInvocationContext_NoSecrets` | ActivityIC JSON contains no secret values from InvocationContext |
| `TestNA12_AgentSpec_ExecutionBackend_RoundTrip` | "temporal" round-trips through JSON marshal/unmarshal |
| `TestNA13_AgentSpec_DefaultExecutionBackend_IsEmpty` | Empty ExecutionBackend is omitted from JSON (omitempty) |
| `TestNA14_Compiler_RejectsInvalidExecutionBackend` | "kubernetes" → INVALID_FIELD issue from validateStructural |
| `TestNA15_Compiler_CopiesExecutionBackend` | "temporal" in canvas JSON → AgentSpec.ExecutionBackend == "temporal" |
| `TestNA16_Compiler_DefaultExecutionBackend_IsEmpty` | Missing execution_backend → AgentSpec.ExecutionBackend == "" |

**Trigger:** any change to `internal/agentgen/node_executor.go`, `internal/agentgen/spec.go`
(`ExecutionBackend` field, `NonRetryableError` interface), `internal/agentgen/compiler.go`
(execution_backend validation + buildSpec), or `internal/agentgen/context.go`

---

### S1-76 · Phase 4-B: CanvasAgentWorkflow + CanvasAgentActivities — `internal/temporal/canvas_workflow_test.go`

**Purpose:** Conformance tests for the Temporal DAG execution layer. Verifies that
`CanvasAgentWorkflow` correctly orchestrates fan-out, join, branch convergence, error
propagation, HumanWait signal return, and result capture — all using the Temporal
`WorkflowTestSuite` (in-process, no live Temporal required). Also verifies
`CanvasAgentActivities.ExecuteStepActivity` activity-level contracts.

| Test | What it proves |
|---|---|
| `TestCT01_LinearChain` | A→B→C executes in order; result from C |
| `TestCT02_ParallelFanOut_JoinWaitAll` | A→{B,C}→D: both B and C execute; D gets merged vars (JoinWaitAll) |
| `TestCT03_BranchTruePath` | Branch true arm executes; false arm skipped; join continues (JoinBranchMerge) |
| `TestCT04_BranchFalsePath` | Branch false arm executes; true arm skipped |
| `TestCT05_NodeError_PropagatesAndCancelsSiblings` | Activity loader error propagates as workflow error |
| `TestCT06_EmptyPlan_ReturnsError` | Empty plan returns non-retryable ApplicationError |
| `TestCT07_JoinBranchMerge_SecondArmDropped` | Only one arm reaches the join; second arm is dropped |
| `TestCT08_ContractViolation_CausesWorkflowFailure` | Missing required input → ErrContractViolation → workflow failure |
| `TestCT09_InvalidIC_NonRetryable` | Invalid ActivityIC returns error before any activity is dispatched |
| `TestCT10_ResponseResult_Propagation` | StepResponse result propagates to CanvasAgentWorkflowOutput |
| `TestCanvasAgentWorkflowInput_Serialization` | CanvasAgentWorkflowInput round-trips through JSON (Temporal history) |
| `TestStepActivityOutput_NoSecrets` | StepActivityOutput JSON contains no secret values |
| `TestExecuteStepActivity_InvalidIC_ReturnsError` | Invalid ActivityIC rejected with TenantID error |
| `TestExecuteStepActivity_HumanWait_ReturnsImmediately` | human_wait node returns WaitingForHuman=true without blocking |
| `TestExecuteStepActivity_NilInterp_ReturnsError` | nil InterpTemplate returns non-retryable error |
| `TestExecuteStepActivity_LoaderError_Propagates` | ContextLoader.Load error propagates as activity error |
| `TestNoResultBugFixed` | ResultMT-only output (empty ResultText, non-empty ResultMT) triggers result capture; truly empty output does not |
| `TestActivityOptionsFromPolicy` | LLM PlanNode from CompileExecutionPlan carries MaxAttempts=2, positive TimeoutSeconds, non-empty NonRetryableErrors |
| `TestWorkflowConcurrencyLimit_ZeroResolvesToDefault` | CT-CONC1: MaxConcurrentTasks=0 in workflow input resolves to 10; linear plan completes normally |
| `TestExecuteStepActivity_Loop_BasicIteration` | CT-LOOP-1: loop node with 3-item list runs body 3×; accum_var entries contain only declared output key (not all pipeline vars) |
| `TestExecuteStepActivity_Loop_NilSubPlan` | CT-LOOP-2: loop node with nil SubPlan is a no-op (no error) |
| `TestExecuteStepActivity_Loop_NonListItemsVar` | CT-LOOP-3: loop node with non-list items_var returns an error |
| `TestCTLoopDurable1_BasicIteration` | CT-LOOP-DURABLE-1: CanvasAgentWorkflow runs loop body as 3 separate activities (one per item) |
| `TestCTLoopDurable2_EmptyList` | CT-LOOP-DURABLE-2: empty items list skips all body activities; post-loop step runs normally |
| `TestCTLoopDurable3_MaxIterationsCap` | CT-LOOP-DURABLE-3: max_iterations=3 caps 10-item list to 3 body activity invocations |
| `TestCTLoopDurable4_AccumVarScopedToOutputs` | CT-LOOP-DURABLE-4: accum_var entries contain only declared body Outputs keys, not undeclared keys |
| `TestCTLoopDurable5_BranchInsideBody` | CT-LOOP-DURABLE-5: branch node inside loop body routes to correct arm per item (2 true, 1 false) |
| `TestCTLoopDurable6_IterationIsolation` | CT-LOOP-DURABLE-6: iter 0 writes "sentinel" to body_out; iter 1 starts with fresh bodyIterState and must NOT see iter 0's value |
| `TestCTLoopDurable7_ScopedAccumVar` | CT-LOOP-DURABLE-7: accum_var contains "done:x"/"done:y" but NOT "should_not_appear" or "items" (scoped to declared Outputs only) |

**Trigger:** any change to `internal/temporal/canvas_workflow.go`, `internal/temporal/canvas_activities.go`,
`internal/agentgen/context.go` (ResolveMaxConcurrentTasks),
`internal/agentgen/plan_compiler.go` (policy resolution affects Temporal activity options), or
`internal/agentgen/nodes.go` (execLoop, ExecNodeWithPolicy, or StepLoop Validate)

---

### S1-77 · Phase 4-C: TemporalExecutor — `internal/temporal/temporal_executor_test.go`

**Purpose:** Unit tests for `TemporalExecutor`, which implements `agentgen.ExecutionBackend` by
submitting a `CanvasAgentWorkflow` to Temporal and blocking until completion. All tests use
`temporalmocks.NewClient` and `temporalmocks.NewWorkflowRun` (no live Temporal server required).

| Test | What it proves |
|---|---|
| `TestTemporalExecutor_Execute_Success` (TE-01) | `ExecuteWorkflow` called; `Get` populates output; `ExecutionResult{Text, MediaType}` returned correctly |
| `TestTemporalExecutor_Execute_WorkflowError` (TE-02) | Error from `run.Get` is wrapped and returned; no panic |
| `TestTemporalExecutor_Execute_EmptyPlan` (TE-03) | nil plan and empty plan both return error before calling `ExecuteWorkflow` |
| `TestTemporalExecutor_ImplementsExecutionBackend` (TE-04) | Compile-time guard: `TemporalExecutor` satisfies `agentgen.ExecutionBackend` |
| `TestTemporalExecutor_DefaultTimeout` (TE-05) | `NewTemporalExecutor` with zero timeout returns non-nil executor (default timeout applied) |
| `TestTemporalExecutor_StableWorkflowID` (TE-06) | Workflow ID incorporates `ic.InvocationID`; retries re-attach to existing workflow |
| `TestTemporalExecutor_PolicyMaxConcurrentTasks` (TE-07) | `ic.Policies.MaxConcurrentTasks` forwarded to `CanvasAgentWorkflowInput`, overriding struct default |
| `TestTemporalExecutor_HumanWait_UsesLongTimeout` (TE-08) | Plan with `human_wait` node → `StartWorkflowOptions.WorkflowExecutionTimeout >= 24h`; short workflowTimeout not used |
| `TestTemporalExecutor_NoHumanWait_UsesShortTimeout` (TE-09) | Plan without `human_wait` → configured short timeout (30s) used exactly; HITL override not applied |
| `TestTemporalExecutor_Submit_ReturnsHandleWithoutBlocking` (TE-10) | Submit() calls ExecuteWorkflow; returns WorkflowID+RunID; never calls run.Get() (non-blocking) |
| `TestTemporalExecutor_Submit_EmptyPlan` (TE-11) | Submit() with nil plan returns error before calling ExecuteWorkflow |
| `TestTemporalExecutor_SignalCanvasStep_Delegates` (TE-12) | SignalCanvasStep() calls client.SignalWorkflow with correct workflowID/runID/signalName/payload |
| `TestPlanHasHumanWait` (TE-13) | PlanHasHumanWait() returns true for human_wait plan, false for others, false for nil |

**Trigger:** any change to `internal/temporal/temporal_executor.go` or `cmd/dag-worker/main.go`

---

### S1-78 · dag-worker SQL tenant scope — `cmd/dag-worker/main_test.go`

**Purpose:** Asserts that every DB query in `dbContextLoader` carries a `tenant_id` predicate
to prevent cross-tenant data access. Tests verify query strings statically; integration tests
cover live round-trips.

| Test | What it proves |
|---|---|
| `TestDBContextLoader_SQLContainsTenantScope/loadSpec` | `agent_runtime_specs` query scoped by `tenant_id` |
| `TestDBContextLoader_SQLContainsTenantScope/loadAppAPIKey` | `applications` provider-key query scoped by `tenant_id` |
| `TestDBContextLoader_SQLContainsTenantScope/loadAppGlobalParams` | `applications` global-params query scoped by `tenant_id` |
| `TestDBContextLoader_SQLContainsTenantScope/loadBinding` | `app_agent_bindings` JOIN `applications` filters by `a.tenant_id` |

**Trigger:** any change to `cmd/dag-worker/main.go` (DB query methods)

---

### S1-79 · HITLStore — `internal/agentgen/hitl_store_test.go`

**Purpose:** Unit tests for `HITLStore`, which persists HITL task handles in Redis (Phase 5-B hardened
schema: `{workflow_id, run_id, tenant_id, step_id, wait_token, state}`) so the signal endpoint can
route human responses to the correct Temporal workflow. State machine: submitted → waiting → signalled
→ deleted. Atomic CAS via `TrySignal` prevents duplicate delivery.

| Test | What it proves |
|---|---|
| `TestHITLStore_StoreAndGet` (HS-1) | Store then Get returns exact WorkflowID/RunID/StepID |
| `TestHITLStore_GetMissing` (HS-2) | Get on missing key returns `ErrHITLNotFound` |
| `TestHITLStore_Delete` (HS-3) | Delete removes handle; subsequent Get returns `ErrHITLNotFound` |
| `TestHITLStore_StoreOverwrite` (HS-4) | Second Store for same taskID overwrites; Get returns new values |
| `TestHITLStore_KeyPrefix` (HS-5) | Redis key uses `them:hitl:` prefix |
| `TestHITLStore_UpdateWaitToken` (HS-6) | UpdateWaitToken transitions state to "waiting" and stores the token |
| `TestHITLStore_TrySignal_Success` (HS-7) | TrySignal with correct token → state "signalled", returns updated handle |
| `TestHITLStore_TrySignal_WrongToken` (HS-8) | TrySignal with wrong token → ErrHITLWrongToken, state unchanged |
| `TestHITLStore_TrySignal_NotWaiting` (HS-9) | TrySignal when state ≠ "waiting" → ErrHITLNotWaiting |
| `TestHITLStore_MarkDone` (HS-10) | MarkDone removes handle; subsequent Get returns ErrHITLNotFound |
| `TestHITLStore_RepeatedWait` (HS-11) | UpdateWaitToken when state=signalled resets to waiting with new token (loop body re-use) |

**Trigger:** any change to `internal/agentgen/hitl_store.go`

---

### S1-80 · agent-runtime HITL async path — `cmd/agent-runtime/main_test.go`

**Purpose:** Tests for Phase 5-B HITL async execution: executeSkill submits without blocking for HITL
plans, stores the handle, and returns `working`. HITLRequestHandler intercepts GetTask/SubscribeToTask/CancelTask
to sync HITL state via Temporal query polling.

| Test | What it proves |
|---|---|
| `TestExecuteSkill_HITL_ReturnsWorking` (RT-HITL-1) | executeSkill with HITL Temporal plan calls Submit, emits `working` (not `input-required`) immediately |
| `TestExecuteSkill_HITL_StoresHandle` (RT-HITL-2) | After Submit, workflow handle stored in hitlStore keyed by taskID with correct WorkflowID/StepID |
| `TestHITLRequestHandler_CancelTask_CancelsWorkflow` (RT-HITL-3) | CancelTask for HITL task calls CancelWorkflow on the Temporal executor |
| `TestHITLRequestHandler_CancelTask_NonHITL_Delegates` (RT-HITL-4) | CancelTask for non-HITL task delegates to the inner SDK handler |
| `TestHITLRequestHandler_SubscribeToTask_NonHITL_Delegates` (RT-HITL-5) | SubscribeToTask for non-HITL task delegates to the inner SDK handler |

**Trigger:** any change to `cmd/agent-runtime/main.go` (HITLRequestHandler, executeSkill HITL path),
`internal/agentgen/hitl_store.go`, `internal/agentgen/a2a_task_store.go`,
`internal/temporal/temporal_executor.go` (CanvasAwaiter/CanvasCanceler/CanvasHITLQuerier)

---

### S1-81 · Canvas HITL signal admin endpoint — `internal/admin/canvas_tasks_test.go`

**Purpose:** Tests for `CanvasTasksHandler`, which exposes `POST /admin/canvas-tasks/{task_id}/signal`
behind JWT + RequireSuperAdmin + AdminTenantMiddleware. Validates tenant ownership, atomic CAS via
`TrySignal`, and signal delivery to the correct Temporal workflow.

| Test | What it proves |
|---|---|
| `TestCanvasTasksHandler_Signal_Success` (CSIG-1) | Correct token + correct tenant → 200 OK, SignalCanvasStep called with right workflowID and payload |
| `TestCanvasTasksHandler_Signal_NotFound` (CSIG-2) | Missing handle → 404, no signal delivered |
| `TestCanvasTasksHandler_Signal_CrossTenant` (CSIG-3) | Tenant mismatch → 403 Forbidden, no signal delivered |
| `TestCanvasTasksHandler_Signal_WrongToken` (CSIG-4) | Wrong wait_token → 409 Conflict, no signal delivered |

**Trigger:** any change to `internal/admin/canvas_tasks.go`, `internal/agentgen/hitl_store.go`

---

### S1-82 · A2A Call node Phase 5-C + 5-C gaps — `internal/agentgen/a2a_test.go`

**Purpose:** Tests for the `a2a_call` node implementation (Phase 5-C) plus the gap fixes:
binding-required resolver, stable idempotency UUIDs, remote error sanitization, and E2E
coverage through both LocalExecutor and `ExecuteNodeForActivity` (Temporal path).

| Test | What it proves |
|---|---|
| `TestA2A_NodeRegistered` (A2A-1) | `StepA2ACall` is registered with non-nil Execute |
| `TestA2A_Validate_MissingFields` (A2A-2) | Missing `agent_slug` and `input_var` produce "error" issues |
| `TestA2A_Validate_ValidConfig` (A2A-3) | Complete config produces no required-field errors |
| `TestA2A_Execute_NoCaller` (A2A-4) | Execute with nil A2ACaller returns an error |
| `TestA2A_Execute_CallsCallerAndSetsVar` (A2A-5) | Correct tenant/app/slug/invocationID/stepID forwarded; result in output_var |
| `TestA2A_Execute_CallerError` (A2A-6) | A2ACaller errors propagate cleanly |
| `TestA2A_Execute_PropagatesDepth` (A2A-7) | IC.A2ACallDepth forwarded via A2ACallParams.Depth |
| `TestA2A_Execute_SelfCallRejected` (A2A-8) | agent_slug == caller slug → error before A2ACaller.Call |
| `TestA2A_MaxDepthEnforced` (A2A-9) | depth >= MaxA2ACallDepth stub error propagates |
| `TestA2A_HTTPA2ACaller_DepthCapRejected` (A2A-9b) | HTTPA2ACaller directly rejects depth >= MaxA2ACallDepth |
| `TestA2A_HumanWait_LocalBackend_Rejected` (A2A-10) | human_wait + local → HUMAN_WAIT_REQUIRES_TEMPORAL warning + publish error |
| `TestA2A_HumanWait_TemporalBackend_OK` (A2A-11) | human_wait + temporal → no HUMAN_WAIT_REQUIRES_TEMPORAL |
| `TestA2A_HTTPA2ACaller_Integration` (A2A-12) | All 4 headers verified: X-Them-Tenant-Id, X-Them-Application-Id, X-Them-Agent-Id, X-Them-Binding-Id |
| `TestA2A_DeriveOutputs_DefaultVar` (A2A-13) | DeriveOutputs returns "a2a_response" when output_var not set |
| `TestA2A_HTTPA2ACaller_NoBinding_FailClosed` (A2A-14) | Fail closed when resolver returns no-binding error |
| `TestA2A_HTTPA2ACaller_StableRequestIDs` (A2A-15) | Retry with same InvocationID+StepID+AgentSlug produces identical RPC and message UUIDs |
| `TestA2A_HTTPA2ACaller_RemoteErrorSanitized` (A2A-16) | Remote error with URL → [url-redacted], truncated at 300 chars |
| `TestA2A_E2E_LocalExecutor` (A2A-17) | Full pipeline via LocalExecutor: all 4 identity headers + depth + output_var + tenant isolation |
| `TestA2A_E2E_ExecuteNodeForActivity` (A2A-18) | Full pipeline via ExecuteNodeForActivity: ActivityIC.A2ACallDepth propagates through depth+1 to target |

**Trigger:** any change to `internal/agentgen/a2a_caller.go`, `internal/agentgen/interpreter.go` (execA2ACall),
`internal/agentgen/nodes.go` (StepA2ACall), `internal/agentgen/compiler.go` (validateHumanWaitBackend),
`internal/agentgen/context.go` (A2ACallDepth), `internal/agentgen/node_executor.go` (ActivityIC)

---

### S1-83 · StreamOut node Phase 5-D — `internal/agentgen/stream_out_test.go`

**Purpose:** Verifies the stream_out pipeline sink: reads `from_var`, defaults to `"output"` when absent,
sets `result.Text` and `result.MediaType`, validates `from_var` required at compile time, and integrates
correctly in a full LLM→stream_out pipeline.

| Test | What it proves |
|---|---|
| `TestStreamOut_ReadsFromVar` (SO-1) | result.Text is set from named from_var |
| `TestStreamOut_DefaultMediaType` (SO-2) | media_type defaults to text/plain |
| `TestStreamOut_ExplicitMediaType` (SO-3) | explicit media_type is honoured |
| `TestStreamOut_MissingVar_EmptyResult` (SO-4) | missing var → empty result, no error |
| `TestStreamOut_DefaultFromVar` (SO-5) | empty from_var config → falls back to "output" |
| `TestStreamOut_Validate_MissingFromVar` (SO-6) | Validate emits STREAM_OUT_MISSING_FROM_VAR when from_var absent |
| `TestStreamOut_Validate_Valid` (SO-7) | Validate accepts stream_out with from_var set |
| `TestStreamOut_DeriveInputs` (SO-8) | DeriveInputs declares from_var as required input |
| `TestStreamOut_DeriveInputs_DefaultVar` (SO-9) | DeriveInputs defaults to "output" when from_var empty |
| `TestStreamOut_FullPipeline` (SO-10) | LLM stub → stream_out: result.Text from LLM, media_type=text/plain |

**Trigger:** any change to `internal/agentgen/nodes.go` (StepStreamOut), `internal/agentgen/interpreter.go` (execStreamOut),
`internal/agentgen/spec.go` (StreamOutStepConfig)

---

### S1-51 · Agent definition publish service — `internal/admin/service/agent_definitions_publish_test.go`

**Purpose:** Canvas A2A Builder validate/publish service layer. Verifies DAL delegation,
error mapping (NotFound, AgentCompileError), AES-GCM encryption of credentials, and the
BuildValidator severity split: ValidateAgentDefinition uses `agentgen.Validate()` (stubs→warnings);
PublishAgentDefinition uses `agentgen.CompileForPublish()` (stubs→errors).

| Test | What it proves |
|---|---|
| `TestValidateAgentDefinition_NotFound` | missing definition → ErrNotFound |
| `TestValidateAgentDefinition_CompileError` | bad definition → *AgentCompileError |
| `TestValidateAgentDefinition_Valid` | good definition → AgentValidationReport{Valid: true, Issues: [...warnings]} |
| `TestValidateAgentDefinition_InlineDefinitionOverridesDB` | valid DB def + invalid inline → *AgentCompileError (inline wins) |
| `TestValidateAgentDefinition_InlineValidDefinition` | invalid DB def + valid inline → Valid=true (inline wins) |
| `TestValidateAgentDefinition_StepContractsPopulated` | valid 2-step pipeline → StepContracts maps each stepID to {Inputs,Outputs} |
| `TestPublishAgentDefinition_NotFound` | missing definition → ErrNotFound |
| `TestPublishAgentDefinition_CompileError` | bad definition → *AgentCompileError |
| `TestPublishAgentDefinition_Success` | valid publish → AgentPublishResult with non-empty fields |
| `TestUpsertBinding_NoKeyWithCredentials` | credentials provided + no key → ErrEncryptionKeyMissing |
| `TestUpsertBinding_EmptyCredentials_NoKeyRequired` | no credentials, no key → nil |
| `TestUpsertBinding_WithKey` | credentials + 32-byte key → nil (encrypted transparently) |
| `TestGetBindingStatus_NotFound` | DAL no-rows → ErrNotFound |
| `TestListBindings_Empty` | no bindings → [] not nil |
| `GetAgentLLMNodes` stub tests (in fake DAL implementations) | new DAL interface methods present in all 4 fake DAL structs |

**New methods:** `GetAgentLLMNodes` (merges spec `llm_nodes` with `config_overrides["llm_nodes"]` overrides) and `PutNodeLLMOverride` (validates non-empty + calls DAL) added to `agent_definitions_publish.go`. HTTP handlers added in `internal/admin/agent_bindings.go`: `GET /agents/{id}/llm-nodes` and `PUT /agents/{id}/llm-nodes/{node_id}`.

**Trigger:** any change to `internal/admin/service/agent_definitions_publish.go`, `internal/admin/service/agent_definitions.go`, `internal/admin/dal/agent_definitions_publish.go`, or `internal/admin/dal/agent_bindings.go`

---

### S1-54 · Node Definition Registry — `internal/agentgen/noderegistry_test.go`

**Purpose:** Validates the `NodeDef` registry is the single source of truth for all 12 canvas node
types. Covers registration completeness, metadata correctness (IsSource/IsSink/OutputArity/Version),
`ToInfo()` deriving `Executable` from `Execute != nil`, per-type `Validate` functions, compiler
integration via `LookupNode`, and the new multi-port schema fields
(`ControlOutputPorts`, `DynamicOutputSource`, `PortDef.Color`, `PortDef.MaxConnections`).

| Test | What it proves |
|---|---|
| `TestNodeRegistry_AllTypesRegistered` | all 12 StepType constants have a NodeDef in the registry |
| `TestNodeRegistry_KnownStepTypesCount` | KnownStepTypes() returns exactly 12 |
| `TestNodeRegistry_InputProperties` | input: IsSource=true, IsSink=false, OutputArity=single, Execute≠nil, Version≥1 |
| `TestNodeRegistry_ToInfo` | ToInfo(): Executable=true for input (Execute≠nil), Executable=true for branch (now implemented) |
| `TestNodeRegistry_AllNodesHaveLabelAndVersion` | every registered node has non-empty Label and Version≥1 and valid OutputArity |
| `TestNodeRegistry_ResponseProperties` | response: IsSource=false, IsSink=true, OutputArity=none, Execute≠nil |
| `TestNodeRegistry_BranchOutputArity` | branch: OutputArity=multi, Execute≠nil (implemented) |
| `TestNodeRegistry_StreamOutIsSink` | stream_out: IsSink=true, OutputArity=none, Execute≠nil (Phase 5-D) |
| `TestNodeRegistry_UnknownTypeReturnsFalse` | LookupNode("banana") → ok=false |
| `TestNodeRegistry_CompilerRejectsUnknownStepType` | compiler returns UNKNOWN_STEP_TYPE for unregistered type |
| `TestNodeRegistry_ImplementedTypesHaveNonNilExecute` | input/llm/http/transform/response/branch/loop/mcp_call/a2a_call/stream_out all have Execute≠nil |
| `TestNodeRegistry_StubTypesHaveNilExecute` | human_wait has Execute=nil (only remaining stub after Phase 5-D) |
| `TestNodeRegistry_ParallelOutputArity` | parallel: OutputArity=multi |
| `TestNodeRegistry_ParallelIsImplemented` | parallel: Execute≠nil (fan-out coordinator is now executable) |
| `TestNodeRegistry_Helpers_Smoke` | compileFail/hasCode helpers work; minimal spec compiles cleanly |
| `TestNodeRegistry_BranchControlOutputPorts` | branch has 2 ControlOutputPorts (true/false) with Color set; round-trips via ToInfo |
| `TestNodeRegistry_TransformDynamicOutputSource` | transform DynamicOutputs=true; DynamicOutputSource="functions[].output_var"; exposed via ToInfo |
| `TestNodeRegistry_PortDefColorAndMaxConnections` | PortDef.Color and MaxConnections round-trip through JSON marshal/unmarshal |

**Trigger:** any change to `internal/agentgen/noderegistry.go`, `internal/agentgen/nodes.go`, or `internal/agentgen/compiler.go`

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
| `TestIsValidChannel` | Channel whitelist: static names + `run:`, `agent:`, `sessions:`, `scan:` prefixes; rejects empty/malformed |
| `TestDashboard_CleanShutdownOnDisconnect` | Client closes → server goroutines exit without panic |
| `TestDashboard_ScanSnapshot` | `scan:<artifact_id>` channel + pre-populated Redis state key → snapshot delivered with `artifact_scan` event |
| `TestDashboard_AppsSnapshot` | `apps` channel + pre-populated Redis cache → app_status snapshot delivered |

**Trigger:** any change to `internal/dashboard/handler.go`

---

### S1-53 · Agent-runtime SDK adoption (Phase D) + Phase 4-C hardening — `cmd/agent-runtime/main_test.go`

**Purpose:** Phase D — validates the official `github.com/a2aproject/a2a-go/v2` SDK integration in
`cmd/agent-runtime/main.go`. Covers spec cache TTL (tenant-scoped keys), SDK agent card construction,
static card handler, the `executeSkill` event sequence, InvocationID stamping from A2A TaskID, and
tenant cache key isolation. No Postgres or Redis required.

| Test | What it proves |
|---|---|
| `TestSpecCache_MissAndHit` | Cold miss returns nil; set+get returns spec; expired entry returns nil |
| `TestSpecCache_IsolatedKeys` | Two distinct agentIDs cached independently with tenant-scoped keys |
| `TestSpecCacheKey_TenantIsolation` | Same agentID under different tenants produces distinct cache keys |
| `TestExecuteSkill_InvocationIDFromTaskID` | `executeSkill` stamps `ic.InvocationID` from `execCtx.TaskID` (stable A2A task ID) |
| `TestBuildSDKAgentCard_SupportedInterfacesAndModes` | `buildSDKAgentCard` emits `SupportedInterfaces` (not deprecated `URL`), JSONRPC binding, per-skill InputModes/OutputModes |
| `TestBuildSDKAgentCard_StaticHandler` | `NewStaticAgentCardHandler` wrapping the built card returns 200 with `application/json` content-type and agent name in body |
| `TestExecuteSkill_SDKEventSequence` | `executeSkill` emits Submitted → Working → ArtifactUpdateEvent → Completed on success |
| `TestExecuteSkill_NoSkills_EmitsFailed` | Agent with empty skill list emits Working → Failed without panic |
| `TestExecuteSkill_StoredTask_NoSubmitted` | When `ExecutorContext.StoredTask` is non-nil, Submitted event is skipped; first event is Working |
| `TestJSONRPCHandler_MethodNotFound` | `NewJSONRPCHandler` wrapping `NewHandler` returns JSON error (HTTP 200) for unknown method names |
| `TestExecuteSkill_SkillSelectionByID` | `Message.Metadata["skill_id"]` selects the matching skill (not always Skills[0]) |
| `TestExecuteSkill_SkillSelectionByID_NotFound` | Unknown `skill_id` in metadata → Failed event (not panic) |
| `TestExecuteSkill_PolicyAllowedSkillIDs_Denied` | `Policies.AllowedSkillIDs` excludes a skill → Failed event |
| `TestExecuteSkill_PolicyAllowedSkillIDs_Permitted` | Skill in `AllowedSkillIDs` → Completed event |
| `TestLoadBinding_SQLTenantScope` | `bindingID` path enforces all 4 IDs (`b.id + b.application_id + b.agent_id + a.tenant_id`); `no-bindingID` path enforces 3 predicates |
| `TestLoadBinding_CrossAgentRejection` | Live DB: correct 4-ID lookup succeeds; wrong `agentID` rejected; wrong `appID` rejected (gated by `THEM_AGENT_RUNTIME_E2E=true`) |
| `TestLoadAppAPIKey_SQLTenantScope` | `loadAppAPIKey` query contains `tenant_id = $2::uuid` predicate |
| `TestLoadAppGlobalParams_SQLTenantScope` | `loadAppGlobalParams` query contains `tenant_id = $2::uuid` predicate |

**Trigger:** any change to `cmd/agent-runtime/main.go` (specCache, specCacheKey, buildSDKAgentCard, executeSkill, handle, agentCard, loadBinding, loadSpecBySlug, loadAppAPIKey, loadAppGlobalParams)

---

### S1-28 · Orchestrator — `internal/orchestrator/orchestrator_test.go`

**Purpose:** Agentic loop feature parity — history loading, checkpoint/crash recovery, token budget
enforcement, parallel agent fan-out with semaphore, nil-safety of optional interfaces,
file artifact detection/recording (Phase R-3), and MCP tool dispatch.

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
| `TestOrchestrator_MCPTool_DispatchedToService` | `mcp__<server>__<tool>` call → POST to MCPServiceURL/internal/execute with correct body |
| `TestOrchestrator_MCPTool_NoServiceURL` | `mcp__*` call with empty MCPServiceURL → tool error, run completes without panic |
| `TestOrchestrator_MCPTools_InBuildTools` | MCPServerAttachment.ToolDefs appear in tools list passed to LLM |
| `TestOrchestrator_FileScanningEvent` | File gate returns gated ID + subscriber present → "file_scanning" event emitted synchronously |
| `TestOrchestrator_ScanResult_Clean` | Scan subscriber returns clean → "file" event emitted after scan |
| `TestOrchestrator_ScanResult_Infected` | Scan subscriber returns infected → "file_blocked" event with threat field |
| `TestOrchestrator_ScanResult_Timeout` | Scan subscriber times out (ok=false) → fallback "file" event emitted |

**Trigger:** any change to `internal/orchestrator/orchestrator.go`, `internal/orchestrator/scan_subscriber.go`, or `internal/orchestrator/summary.go`

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
| `TestArtifactDownload_ScanPending` | scan_status=pending → 202 Accepted (gate holds download until scan completes) |
| `TestArtifactDownload_ScanScanning` | scan_status=scanning → 202 Accepted |
| `TestArtifactDownload_ScanInfected` | scan_status=infected → 451 Unavailable For Legal Reasons |
| `TestArtifactDownload_ScanClean` | scan_status=clean → 200 with file content served normally |
| `TestArtifactDownload_MinIO` | storage_key set + ByteFetcher → bytes fetched from MinIO, 200 with correct body |
| `TestArtifactDownload_InfectedGone` | storage_key="" + data=nil (infected/scrubbed) → 410 Gone |
| `TestArtifactDownload_MinIOFetchError` | storage_key set but MinIO fetch fails → 500 |

**Trigger:** any change to `internal/artifacts/handler.go`

---

### S1-24 · Apps dispatcher — `cmd/them/dispatcher_test.go`

**Purpose:** Verify that `appsDispatcher` routes `/ws` paths to the WS handler, `/sse` paths to
the SSE handler, `/voice/*` paths to the voice handler, and returns 404 for everything else.
Method enforcement (405) is delegated to the chi sub-handler, not the dispatcher.

| Test | What it proves |
|---|---|
| `TestAppsDispatcher_WSPath` | `GET /{slug}/ws` → WS handler called; SSE handler not called |
| `TestAppsDispatcher_SSEPath_GET` | `GET /{slug}/sse` → SSE handler called; WS handler not called |
| `TestAppsDispatcher_SSEPath_POST` | `POST /{slug}/sse` → SSE handler called; WS handler not called |
| `TestAppsDispatcher_UnknownPath_Returns404` | Unknown paths (`/grpc`, `/`, etc.) → 404; neither handler called |
| `TestAppsDispatcher_UnsupportedMethod_WS` | `POST /{slug}/ws` forwarded to WS handler (returns 405 from chi); SSE not called |
| `TestAppsDispatcher_VoicePath` | `POST /{slug}/voice/transcribe` and `/voice/tts` → voice handler called; WS/SSE not called |
| `TestAppsDispatcher_VoiceNilHandler` | Voice path with nil voice handler → 404 |

**Trigger:** any change to `cmd/them/main.go` (`appsDispatcher` function)

---

### S1-62 · MCP server admin service — `internal/admin/service/mcp_servers_test.go`

**Purpose:** Unit tests for the MCP server admin service layer. Verifies validation, defaults,
not-found mapping, and credential encryption — all using the shared `fakeDal`, no real DB or crypto stack.

| Test | What it proves |
|---|---|
| `TestMCPServerService_Create_MissingName` | S1-MCP-01: missing name → `ErrValidation` |
| `TestMCPServerService_Create_MissingSlug` | S1-MCP-02: missing slug → `ErrValidation` |
| `TestMCPServerService_Create_InvalidTransport` | S1-MCP-03: transport="grpc" → `ErrUnprocessable` |
| `TestMCPServerService_Create_InvalidAuthType` | S1-MCP-04: auth_type="api_key" → `ErrUnprocessable` |
| `TestMCPServerService_Create_DefaultsApplied` | S1-MCP-05: omitted transport/auth_type → defaults http/none; enabled=true |
| `TestMCPServerService_Update_NotFound` | S1-MCP-06: GetMCPServer returns pgx.ErrNoRows → `ErrNotFound` |
| `TestMCPServerService_Update_AppliesPatch` | S1-MCP-07: patch name+transport; slug unchanged |
| `TestMCPServerService_Delete_NotFound` | S1-MCP-08: DeleteMCPServer returns pgx.ErrNoRows → `ErrNotFound` |
| `TestMCPServerService_SetCredential_Empty` | S1-MCP-09: empty credential → `ErrValidation` |
| `TestMCPServerService_SetCredential_EncryptsAndUpserts` | S1-MCP-10: valid credential → UpsertAppMCPCredential called |
| `TestMCPServerService_SetCredential_DefaultsHeaderName` | S1-MCP-11: empty auth_header_name → defaults to "Authorization" |

**Trigger:** any change to `internal/admin/service/mcp_servers.go` or `internal/admin/dal/mcp_servers.go` or `internal/admin/mcp_servers.go`

---

### S1-70 · Voice handler — `internal/voice/handler_test.go`

**Purpose:** Unit tests for the Go voice HTTP handler. Verifies auth gating, EP config resolution,
provider validation, and error responses for both STT (transcribe) and TTS endpoints — using fake
ConfigLoader, KeyResolver, and Authenticator to avoid real provider calls.

| Test | What it proves |
|---|---|
| `TestTranscribe_EPNotFound` | ConfigLoader returns error → 404 (slug not a voice EP) |
| `TestTranscribe_TokenEPNoAuth` | Token EP + no Bearer header → 401 |
| `TestTranscribe_EPDisabled` | EP disabled flag → 403 |
| `TestTranscribe_NoSTTProvider` | STT provider not configured → 400 |
| `TestTranscribe_NoAPIKey` | Provider key empty → 400 |
| `TestTTS_MissingText` | Empty `text` body → 400 |
| `TestTTS_TokenEPNoAuth` | Token EP + no Bearer on TTS → 401 |

**Trigger:** any change to `internal/voice/handler.go`, `internal/voice/service.go`, or `internal/voice/pgx.go`

---

### S1-71 · Agent Definition Schema + Generate — `internal/admin/agent_definition_schema_test.go`

**Purpose:** Unit tests for the AI Copilot Phase 0 endpoints: `GET /admin/agent-definitions/schema`
and `POST /admin/agent-definitions/generate`. Verifies static schema content, node LLM knowledge
fields, generate input validation, and response shape. Uses a mock `generateLLMCaller` to avoid
real Anthropic calls.

| Test | What it proves |
|---|---|
| `TestSchema_ReturnsWireFormatAndIssueCodes` | Schema response contains wire_format (with agent_root/skills), issue_codes (DUPLICATE_SKILL, CYCLE_DETECTED, etc.), and node_types |
| `TestSchema_NodeTypesHaveConfigFields` | Core implemented node types (llm, http, mcp_call, transform) expose config_fields for LLM prompt building |
| `TestGenerate_EmptyPromptReturns400` | Empty prompt string → 400 Bad Request |
| `TestGenerate_BadJSONBodyReturns400` | Non-JSON request body → 400 Bad Request |
| `TestGenerate_NoLLMReturns501` | Handler constructed with nil llm → 501 Not Implemented |
| `TestGenerate_ValidDefinitionReturnedWithIssues` | Mock LLM returns a valid definition → 200 with definition JSON, issues array, valid flag |
| `TestGenerate_LLMReturnsCodeFencedJSON` | Mock LLM wraps JSON in a code fence → extracted and validated correctly |

**Trigger:** any change to `internal/admin/agent_definition_schema.go` or `internal/agentgen/noderegistry.go` or `internal/agentgen/nodes.go`

---

### S1-87 · Middleware pipeline + AV scanner — `internal/middleware/middleware_test.go`, `internal/middleware/av/clamav_test.go`

**Purpose:** Unit tests for the file-scan middleware pipeline and ClamAV scanner.
Pipeline tests verify processor ordering, error propagation, and fail-open behaviour.
AV scanner tests use a mock clamd listener to verify the INSTREAM protocol, threat detection,
oversized-file blocking, and TCP null-byte response parsing. Live scan covered by `TestAVScanner_LiveClamd` (CLAMAV_SOCKET env).

| Test | What it proves |
|---|---|
| `TestRegistry_GetReturnsNilForUnknown` | Unregistered processor → nil |
| `TestRegistry_PanicOnDuplicate` | Registering same processor name twice → panic |
| `TestDefaultSecurityConfig_DisabledByDefault` | Default config has Enabled=false (zero overhead) |
| `TestDefaultSecurityConfig_HasAllProcessors` | Default config has all 5 processor keys |
| `TestMergeDefaults_FillsMissingKeys` | MergeDefaults fills missing processor configs from defaults |
| `TestValidate_DisabledIsAlwaysValid` | Disabled config passes validation |
| `TestValidate_RejectsInvalidMaxFileMB` | max_file_mb=0 → validation error |
| `TestValidate_RejectsInvalidSensitivity` | sensitivity="extreme" → validation error |
| `TestEnabledProcessors_EmptyWhenDisabled` | Disabled config → no processors returned |
| `TestEnabledProcessors_FiltersByPartKind` | file part → av_scan only; text part → pii_redact only |
| `TestPipeline_NamesEmpty_ReturnsDisabled` | Empty processor list → FinalStatus="disabled" |
| `TestPipeline_AllClean_ReturnsClean` | All processors return clean → FinalStatus="clean" |
| `TestPipeline_BlockStopsFurtherProcessors` | Block=true stops chain; threat propagated |
| `TestPipeline_ModifiedPartPassedToNext` | Modified part passed to next processor; flagged+non-blocking → clean |
| `TestPipeline_ProgressPublisherCalled` | Publisher called with "running" then result |
| `TestPipeline_ProcessorError_ReturnsError` | Processor error + Block=false → FinalStatus="error" (not "clean") |
| `TestAVScanner_CleanFile` | Mock clamd "OK" → outcome="clean", Block=false |
| `TestAVScanner_InfectedFile_BlockEnabled` | Mock clamd "FOUND" + block_on_infected=true → infected + Block=true |
| `TestAVScanner_InfectedFile_WarnOnly` | Mock clamd "FOUND" + block_on_infected=false → infected, Block=false |
| `TestAVScanner_OversizedFile_Blocks` | File > max_file_mb → error outcome + Block=true |
| `TestAVScanner_NonFilePart_Skips` | text part → outcome="skipped", no dial |
| `TestAVScanner_Disabled_Skips` | enabled=false → outcome="skipped", no dial |
| `TestAVScanner_SocketUnavailable_FailsOpen` | Nonexistent socket → error outcome, Block=false (fail-open) |
| `TestAVScanner_EmptyBytes_ReturnsClean` | Zero-byte file → "clean" immediately without scanning |
| `TestAVScanner_Name` | Scanner.Name() == "av_scan" |

**Trigger:** any change to `internal/middleware/pipeline.go`, `internal/middleware/config.go`, `internal/middleware/processor.go`, or `internal/middleware/av/clamav.go`

---

### S1-88 · Middleware job DAL quarantine path — `internal/middleware/job_test.go`

**Purpose:** Unit tests for the quarantine-first DAL: enqueue with quarantine_id, load bytes from
MinIO, promote clean bytes to artifacts bucket, insert infected metadata-only row, legacy path compat.

| Test | What it proves |
|---|---|
| `TestJobDAL_EnqueueWithQuarantine` | SQL INSERT includes quarantine_id column |
| `TestJobDAL_LoadFileBytes_QuarantinePath` | When QuarantineID set, metadata from DB + bytes from fake store |
| `TestJobDAL_Complete_CleanPath` | Clean scan: bytes promoted to artifacts bucket, quarantine deleted |
| `TestJobDAL_Complete_InfectedPath` | Infected: metadata-only run_artifacts row, no artifacts bucket write |
| `TestJobDAL_Complete_LegacyPath` | QuarantineID="" → legacy UPDATE path, no MinIO calls |

**Trigger:** any change to `internal/middleware/job.go`

---

### S1-89 · Object storage client — `internal/storage/storage_test.go`

**Purpose:** Unit tests for the MinIO/S3 storage.Client constructor. Verifies URL parsing,
scheme detection (HTTP vs HTTPS), and error on malformed endpoint.

| Test | What it proves |
|---|---|
| `TestNew_InvalidEndpoint` | Bad URL scheme → error |
| `TestNew_ValidEndpoint` | HTTP endpoint → non-nil client |
| `TestNew_HTTPSEndpoint` | HTTPS endpoint → no error (Secure=true) |

**Trigger:** any change to `internal/storage/storage.go`

---

### S1-91 · Quarantine reaper — `internal/middleware/reaper_test.go`

**Purpose:** Unit tests for the Reaper that deletes expired quarantine objects from MinIO and the DB.

| Test | What it proves |
|---|---|
| `TestReaper_DeletesExpiredRows` | Happy path: expired rows deleted from both MinIO and DB |
| `TestReaper_NoRows` | No expired rows → no-op, zero deletes |
| `TestReaper_MinIOErrorDoesNotBlockDBDelete` | MinIO failure → DB row still deleted (fail-forward) |
| `TestReaper_EmptyStorageKeySkipsMinIO` | Row with no storage_key (already scrubbed) → MinIO call skipped |
| `TestReaper_QueryErrorIsHandled` | DB query error → handled without panic |

**Trigger:** any change to `internal/middleware/reaper.go`

---

### S1-92 · Managed Apps catalog + tenant bindings — `internal/admin/managed_apps_test.go`

**Purpose:** Platform CRUD for the managed-app catalog and per-tenant binding activation.
Verifies empty-list `[]`, create/get, param-manifest replacement, and binding upsert.

| Test | What it proves |
|---|---|
| `TestManagedApps_List_Empty` (MA-01) | GET catalog returns `[]` when no apps |
| `TestManagedApps_List_Populated` (MA-02) | GET catalog returns populated list |
| `TestManagedApps_Create_Success` (MA-03) | POST creates managed app → 201 |
| `TestManagedApps_Create_MissingName` (MA-04) | POST missing name → 400 |
| `TestManagedApps_Get_Found` (MA-05) | GET /{id} returns app + empty params array |
| `TestManagedApps_Get_NotFound` (MA-06) | GET /{id} unknown id → 404 |
| `TestManagedApps_PutParams` (MA-07) | PUT /{id}/params replaces manifest → 200 with updated count |
| `TestManagedApps_Bindings_List` (MA-08) | GET tenant bindings returns list scoped to context tenant |
| `TestManagedApps_Binding_Upsert` (MA-09) | PUT binding upserts → 200 with binding row |
| `TestManagedApps_Binding_MissingConfig` (MA-10) | PUT binding with no config field → 400 |

**Trigger:** `internal/admin/managed_apps.go`, `internal/admin/dal/managed_apps.go`, `internal/admin/router.go`

---

### S1-93 · Workerconfig managed app parameter substitution — `internal/temporal/workerconfig/loader_test.go`

**Purpose:** Verify `ManagedAppParams` struct and `ApplyParamSubstitution` helper for runtime parameter injection (Step 7).
Non-nil params replace `{{PARAMS.KEY}}` placeholders; unmatched keys are left unchanged; nil params are safe.

| Test | What it proves |
|---|---|
| `TestManagedAppParams_ConfigSubstitution` (MAP-01) | `{{PARAMS.KEY}}` replaced; unmatched left as-is; non-string types formatted via `%v` |
| `TestManagedAppParams_NilSafe` (MAP-02) | `ApplyParamSubstitution` returns prompt unchanged when params is nil |
| `TestRunConfig_ManagedAppParams_ZeroNil` (MAP-03) | Zero-value `RunConfig.ManagedAppParams` is nil |

**Trigger:** `internal/temporal/workerconfig/loader.go`, `internal/temporal/workerconfig/loader_test.go`

---

### S1-94 · Tenant CRUD + PATCH + quota + members handler — `internal/admin/tenants_test.go`

**Purpose:** Verify the tenant list/get/create/patch/quota/members HTTP handlers (Steps 4, 10, 12, 16). Covers the PATCH endpoint which updates display_name, enabled, and idp_config; the GET/PUT quota endpoints; and the GET/POST member management endpoints.

| Test | What it proves |
|---|---|
| `TestTenants_List_Empty` (TN-01) | GET /tenants with no rows → 200 with `[]` |
| `TestTenants_List_Populated` (TN-02) | GET /tenants with 2 rows → both slugs present |
| `TestTenants_Get_Found` (TN-03) | GET /tenants/{id} → 200 with correct fields |
| `TestTenants_Get_NotFound` (TN-04) | GET /tenants/{missing} → 404 |
| `TestTenants_Create_Success` (TN-05) | POST /tenants → 201 with new tenant |
| `TestTenants_Create_MissingSlug` (TN-06) | POST without slug → 400 |
| `TestTenants_Create_MissingDisplayName` (TN-07) | POST without display_name → 400 |
| `TestTenants_Create_BadJSON` (TN-08) | POST invalid JSON → 400 |
| `TestTenants_Patch_Success` (TN-09) | PATCH display_name → 200 with updated TenantDetail |
| `TestTenants_Patch_NotFound` (TN-10) | PATCH missing tenant → 404 |
| `TestTenants_Patch_BadJSON` (TN-11) | PATCH invalid JSON → 400 |
| `TestTenants_Patch_IDPConfigured` (TN-12) | PATCH with idp_config → 200 with idp_configured=true |
| `TestTenants_GetQuota_NotFound` (TN-13) | GET /tenants/{id}/quota with no quota row → 404 |
| `TestTenants_GetQuota_Found` (TN-14) | GET /tenants/{id}/quota → 200 with plan field |
| `TestTenants_UpsertQuota_Success` (TN-15) | PUT /tenants/{id}/quota → 200 with saved plan |
| `TestTenants_UpsertQuota_BadPlan` (TN-16) | PUT with invalid plan value → 400 |
| `TestTenants_UpsertQuota_BadJSON` (TN-17) | PUT with invalid JSON → 400 |
| `TestTenants_ListMembers_Empty` (TN-18) | GET /tenants/{id}/members with no rows → 200 with `[]` |
| `TestTenants_ListMembers_Populated` (TN-19) | GET /tenants/{id}/members with 1 row → username + role present |
| `TestTenants_AddMember_Success` (TN-20) | POST /tenants/{id}/members → 201 with role |
| `TestTenants_AddMember_MissingUserID` (TN-21) | POST without user_id → 400 |
| `TestTenants_AddMember_MissingRole` (TN-22) | POST without role → 400 |
| `TestTenants_ListGroupMappings_Empty` (GM-01) | GET /tenants/{id}/group-mappings with no rows → 200 with `[]` |
| `TestTenants_ListGroupMappings_Populated` (GM-02) | GET /tenants/{id}/group-mappings with 1 row → group_claim + role present |
| `TestTenants_UpsertGroupMapping_Success` (GM-03) | PUT /tenants/{id}/group-mappings → 200 with mapping fields |
| `TestTenants_UpsertGroupMapping_MissingGroupClaim` (GM-04) | PUT without group_claim → 400 |
| `TestTenants_UpsertGroupMapping_InvalidRole` (GM-05) | PUT with role not in {viewer,member,admin,super_admin} → 400 |
| `TestTenants_DeleteGroupMapping_Success` (GM-06) | DELETE /tenants/{id}/group-mappings/{mapping_id} → 204 |
| `TestTenants_DeleteGroupMapping_NotFound` (GM-07) | DELETE missing mapping → 404 |

**Trigger:** `internal/admin/tenants.go`, `internal/admin/dal/tenants.go`

---

### S1-95 · Quota enforcer — `internal/quota/enforcer_test.go`

**Purpose:** Unit tests for `quota.Enforcer.Check` — the per-tenant run limit checker. Covers all three enforcement paths (concurrent runs via DB COUNT, runs/min via Redis INCR, monthly runs via Redis INCR) and the nil-limit / DB-error cases.

| Test | What it proves |
|---|---|
| `TestEnforcer_NilLimits` (QE-01) | All limits nil → always passes; no DB or Redis call needed |
| `TestEnforcer_ConcurrentBelowLimit` (QE-02) | Active run count (3) < limit (5) → passes |
| `TestEnforcer_ConcurrentAtLimit` (QE-03) | Active run count (5) == limit (5) → ErrConcurrentRunsExceeded |
| `TestEnforcer_RPMBelowLimit` (QE-04) | Redis INCR returns 5 < limit (10) → passes |
| `TestEnforcer_RPMExceeded` (QE-05) | Redis INCR returns 11 > limit (10) → ErrRunsRateLimited |
| `TestEnforcer_DBError` (QE-06) | DB error counting active runs → wrapped error surfaced (not ErrConcurrentRunsExceeded) |
| `TestEnforcer_MonthlyNilLimit` (QE-07) | MonthlyRuns nil → no enforcement; always passes |
| `TestEnforcer_MonthlyBelowLimit` (QE-08) | Monthly INCR returns 500 < limit (1000) → passes |
| `TestEnforcer_MonthlyExceeded` (QE-09) | Monthly INCR returns 1001 > limit (1000) → ErrMonthlyRunsExceeded |

**Trigger:** any change to `internal/quota/enforcer.go` or `internal/admin/dal/runs.go` (CountActiveRuns)

---

### S1-98 · DB Pools — `internal/db/pools_test.go`

**Purpose:** Unit tests for the RLS pool infrastructure: error paths on bad DSNs, compile-time interface assertions for TenantTx/AdminTx/adminQuerier, and UUID string format verification for the set_config call.

| Test | What it proves |
|---|---|
| `TestNewPools_BadAppDSN` | Invalid app DSN returns error (not nil) |
| `TestTenantTx_InterfaceAssertions` | Compile-time: TenantTx/AdminTx/adminQuerier satisfy dbtype interfaces |
| `TestPools_Close_NilSafe` | Close path covered by integration tests (documents gap) |
| `TestBeginTenantTx_TenantIDFormat` | uuid.UUID.String() produces canonical UUID format for set_config |

**Trigger:** any change to `internal/db/db.go`

---

### S1-99 · dbtype Querier interfaces — `internal/dbtype/querier_test.go`

**Purpose:** Compile-time and runtime verification that `TenantQuerier` and `AdminQuerier` are distinct types with enforced marker methods, preventing wrong-pool wiring at compile time.

| Test | What it proves |
|---|---|
| `TestInterfaceDistinction` (RLS-34/35 compile-time) | `fakeTenantQ` satisfies `TenantQuerier`; `fakeAdminQ` satisfies `AdminQuerier`; marker methods return `struct{}{}` |

**Trigger:** any change to `internal/dbtype/querier.go`

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

### S2-06 · Phase 4-C: Temporal executor E2E — `internal/temporal/integration_test.go`

**Purpose:** Integration-tagged tests for fail-closed Temporal behaviour and the TemporalExecutor
live path. Note: these tests call `TemporalExecutor.Execute` directly — they do NOT exercise
the agent-runtime HTTP path. For the full path E2E see S2-07.
Build tag: `//go:build integration`.

| Test | What it proves | Requirements |
|---|---|---|
| `TestTemporalConnect_Unavailable` | `Connect` returns non-nil error for unreachable Temporal | None (targets unused port) |
| `TestTemporalExecutor_EmptyPlan_Integration` | Nil/empty plan rejected before any RPC call; nil client proves guard runs first | None |
| `TestTemporalExecutor_LiveDAG` | `TemporalExecutor.Execute` against live Temporal + dag-worker; unique InvocationID per run | `THEM_TEMPORAL_E2E=true`, live Temporal + dag-worker |

**Run command:**
```bash
THEM_TEMPORAL_E2E=true TEMPORAL_HOST_PORT=localhost:7233 \
  go test -tags=integration -v -timeout 120s ./internal/temporal/...
```

**Trigger:** any change to `internal/temporal/temporal_executor.go`, `internal/temporal/canvas_workflow.go`, `cmd/dag-worker/main.go`, `docker-compose.yml` (temporal profile)

---

### S2-07 · Phase 4-C: Agent-runtime full-path E2E — `cmd/agent-runtime/e2e_integration_test.go`

**Purpose:** Full end-to-end test through the complete production path:
HTTP client → `Runtime.handle()` → A2A SDK → `executeSkill` → spec/binding DB lookup →
`TemporalExecutor.Execute` → Temporal → dag-worker → PostgreSQL.

Uses a timestamp-based unique message ID to guarantee a new Temporal workflow is started
on every run (no re-attachment to prior completed workflows).

| Test | What it proves | Requirements |
|---|---|---|
| `TestAgentRuntime_LiveE2E` | Full path TASK_STATE_COMPLETED; unique workflow ID per run; not re-attached | `THEM_AGENT_RUNTIME_E2E=true`, live Postgres + Redis + Temporal + dag-worker |
| `TestLoadBinding_CrossAgentRejection` | `loadBinding(bindingID)` rejects wrong agentID and wrong appID within same tenant | `THEM_AGENT_RUNTIME_E2E=true`, live Postgres |

Seeded prerequisites (permanent fixtures in dev DB):

| Resource | ID |
|---|---|
| tenant | `00000000-0000-0000-0000-000000000001` (slug: `default`) |
| application | `00000000-0000-0000-0000-000000000002` (E2E Test App) |
| agent | `00000000-0000-0000-0000-000000000003` (slug: `e2etestagent`, backend: `temporal`) |
| binding | `fa6ae508-412b-46e4-8da1-34441825c6c2` |

**Run command (from inside them-network container):**
```bash
THEM_AGENT_RUNTIME_E2E=true \
  DATABASE_HOST=them-postgres DATABASE_USER=them DATABASE_NAME=them DATABASE_PASSWORD=<pw> \
  REDIS_HOST=them-redis \
  TEMPORAL_HOST_PORT=temporal-frontend:7233 \
  SECRET_KEY=<key> \
  go test -tags=integration -v -timeout 120s ./cmd/agent-runtime/ -run 'TestAgentRuntime_LiveE2E|TestLoadBinding_CrossAgentRejection'
```

**Trigger:** any change to `cmd/agent-runtime/main.go` (handle, executeSkill, loadBinding, loadSpecByAgentID)

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

### S2-08 · RLS integration — `internal/db/rls_integration_test.go`

**Purpose:** Verifies RLS role attributes and connection GUC isolation using a live Postgres
instance with the A1 migration applied (them_owner, them_admin, them_app roles created).
Tests skip gracefully when prerequisites (roles, RLS enabled on tables) aren't met.

**Build tag:** `//go:build integration`

| Test | What it proves | Prereqs |
|---|---|---|
| `TestRLS30_AppRoleNoBypassRLS` | them_app.rolbypassrls = false | A1 migration |
| `TestRLS31_OwnerRoleNoLogin` | them_owner.rolcanlogin = false | A1 migration |
| `TestRLS31b_OwnerDirectConnectFails` | them_owner cannot connect as DSN (NOLOGIN) | A1 migration |
| `TestRLS32_AdminRoleBypassRLS` | them_admin.rolbypassrls = true | A1 migration |
| `TestRLS33_AdminQueryBypasses` | them_admin bypasses RLS (BYPASSRLS confirmed) | A1 + Phase B deployed |
| `TestRLS08_AppPoolFailClosed` | them_app without set_config returns 0 rows (fail-closed) | A1 + RLS on agents |
| `TestRLS10_FreshConnectionFailClosed` | Fresh them_app connection is fail-closed (MaxConns=1) | A1 + RLS on agents |
| `TestRLS11_GUCResetsAfterCommit` | GUC resets to '' after commit; reused connection is fail-closed | A1 + RLS on agents |
| `TestRLSPoolsInterface` | NewPools connects; App+Admin non-nil; NewAdminQuerier non-nil | THEM_DB_URL_APP + THEM_DB_URL_ADMIN |
| `TestRLS_TwoTenantFullIsolation` | Full two-tenant isolation: each tenant sees only own rows in mcp_servers, orchestrators, applications, app_mcp_credentials; cross-tenant INSERT blocked by WITH CHECK | A1 + Phase B/C/D migrations deployed |

**Run command:**
```bash
DATABASE_HOST=them-postgres DATABASE_USER=them DATABASE_NAME=them DATABASE_PASSWORD=<pw> \
THEM_DB_URL_APP=postgres://them_app:<pw>@them-postgres:5432/them \
THEM_DB_URL_ADMIN=postgres://them_admin:<pw>@them-postgres:5432/them \
go test -tags=integration -v ./internal/db/... -run 'TestRLS'
```

**Trigger:** any change to `internal/db/db.go`, `db/070_rls_roles.sql`, or any `db/07*_rls_phase_*.sql` (Phases A–D and beyond)

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
| `internal/temporal/canvas_workflow.go`, `internal/temporal/canvas_activities.go` | S1-76 |
| `internal/temporal/workerconfig/loader.go` | S1-61 + S1-93 |
| `internal/llm/` (any file) | S1-10 |
| `internal/agentregistry/registry.go` | S1-11 |
| `internal/agentgen/` (any file) | S1-48 + S1-50 + S1-54 + S1-65 + S1-71 + S1-72 + S1-73 + S1-74 + S1-75 |
| `internal/agentgen/compiler.go` | S1-50 + S1-54 + S1-63 + S1-65 + S1-75 |
| `internal/agentgen/interpreter.go` | S1-48 + S1-64 + S1-67 + S1-69 |
| `internal/agentgen/spec.go` | S1-50 + S1-63 + S1-65 + S1-67 + S1-69 + S1-72 + S1-73 + S1-74 + S1-75 |
| `internal/agentgen/node_executor.go` | S1-75 |
| `internal/agentgen/plan_compiler.go` | S1-72 + S1-73 + S1-74 |
| `internal/agentgen/executor.go` | S1-73 + S1-74 |
| `internal/agentgen/local_executor.go` | S1-73 + S1-74 |
| `internal/agentgen/nodes.go` | S1-54 + S1-50 + S1-65 + S1-69 + S1-71 + S1-74 |
| `internal/agentgen/noderegistry.go` | S1-54 + S1-50 + S1-65 + S1-69 + S1-71 |
| `internal/agentgen/mcp_caller.go` | S1-69 |
| `internal/agentgen/context.go` | S1-48 + S1-64 + S1-73 + S1-75 + S1-76 |
| `cmd/agent-runtime/main.go` | S1-60 + S1-62 + S1-65 |
| `cmd/agent-runtime/main.go` | S1-48 + S1-50 + S1-53 + S1-72 + S1-73 + S1-74 + S1 (full suite) |
| `internal/admin/applications.go` (GetAppParams/SetAppParam/DeleteAppParam handlers) | S1-66 |
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
| `internal/admin/agent_definition_schema.go` | S1-71 |
| `internal/admin/` (any file) | S1-15 + S1-25 + S1-34 + S1-42 + S1-43 + S1-44 + S1-45 + S1-49 + S1-50 + S1-51 + S1-71 + S1-92 |
| `internal/admin/dal/` (any file) | S1-15 + S1-25 + S1-34 + S1-42 + S1-43 + S1-44 + S1-45 + S1-49 + S1-51 + S1-92 + S2-05 (integration) |
| `internal/admin/dal/agent_definitions_publish.go` | S1-51 |
| `internal/admin/dal/agent_bindings.go` | S1-51 |
| `internal/admin/dal/definitions.go` | S1-42 |
| `internal/admin/dal/registry.go` | S1-45 |
| `internal/admin/dal/llm_providers.go` | S1-25 + S2-05 (integration) |
| `internal/admin/dal/agent_definitions.go` | S1-49 |
| `internal/admin/service/applications.go` | S1-25 + S1-33 + S1-60 |
| `internal/admin/service/` (any file) | S1-25 + S1-33 + S1-42 + S1-43 + S1-44 + S1-49 + S1-51 + S1-60 + S1-62 |
| `internal/admin/service/agent_definitions_publish.go` | S1-51 + S1-54 |
| `internal/agentgen/compiler.go` | S1-50 + S1-54 + S1-68 |
| `internal/agentgen/noderegistry.go` | S1-54 + S1-50 + S1-65 + S1-68 |
| `internal/agentgen/nodes.go` | S1-54 + S1-50 + S1-65 + S1-68 |
| `internal/admin/service/definitions.go` | S1-42 + S1-43 + S1-44 |
| `internal/admin/service/publish.go` | S1-43 + S1-44 |
| `internal/admin/dal/publish.go` | S1-43 + S1-44 |
| `internal/admin/service/agent_definitions.go` | S1-49 |
| `internal/admin/definitions.go` | S1-42 + S1-43 + S1-44 |
| `internal/admin/agent_definitions.go` | S1-49 |
| `internal/admin/registry.go` | S1-45 |
| `internal/admin/router.go` | S1-43 + S1-44 + S1-45 + S1-49 + S1-71 + S1-92 |
| `internal/admin/managed_apps.go` | S1-92 |
| `internal/admin/dal/managed_apps.go` | S1-92 |
| `internal/admin/tenants.go` | S1-94 |
| `internal/admin/dal/tenants.go` | S1-94 |
| `internal/crypto/fernet.go` | S1-26 |
| `internal/transport/transport.go` | S1-12 + S1-13 |
| `internal/metrics/metrics.go` | S1-27 |
| `internal/ratelimit/limiter.go` | S1-16 |
| `internal/quota/enforcer.go` | S1-95 + S1-35 |
| `internal/gate/gate.go` | S1-17 |
| `internal/epconfig/epconfig.go` | S1-18 |
| `internal/epconfig/pgx.go` | S1-18 |
| `internal/cache/auth_adapter.go` | S1-19 |
| `internal/cache/runstream_adapter.go` | S1-20 |
| `internal/runstream/stream.go` | S1-21 |
| `internal/reconciler/reconciler.go` | S1-22 |
| `internal/registry/resolver.go`, `internal/registry/pgx.go`, `internal/registry/types.go` | S1-41 + S1-43 + S1-44 |
| `internal/middleware/reaper.go` | S1-91 |
| `internal/middleware/job.go` | S1-88 |
| `internal/middleware/gate.go` | S1-84 |
| `internal/middleware/pipeline.go`, `internal/middleware/config.go`, `internal/middleware/processor.go` | S1-87 |
| `internal/middleware/av/clamav.go` | S1-87 |
| `internal/storage/storage.go` | S1-89 |
| `cmd/middleware-worker/main.go` | S1-87 + S1-88 + S1-89 + S1-91 |
| `internal/db/db.go` | S1-98 + S2-08 (integration) |
| `internal/dbtype/querier.go` | S1-99 |
| `db/070_rls_roles.sql` or any `db/07*_rls_phase_*.sql` | S2-08 (integration) |
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
| S1-11 | agentregistry | 17 |
| S1-12 | ws | 24 |
| S1-13 | sse | 23 |
| S1-14 | a2a | 30 |
| S1-15 | admin | 59 |
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
| S1-30 | artifacts (download handler) | 16 |
| S1-31 | auth/tenant_middleware (R-4b) | 15 |
| S1-32 | tenantctx (R-4b) | 8 |
| S1-33 | admin/service tenant isolation (R-4c1) | 21 |
| S1-34 | admin tenant HTTP enforcement (R-4c2) | 12 |
| S1-35 | execution lifecycle (unification refactor) | 22 |
| S1-36 | admin agent action endpoints (Wave 8: discover/test/security-scan) | 8 |
| S1-40 | authserver (Go auth service + OIDC flow + JWKS RS256 verification + cache + Step 16 RBAC + Step 17 tenant-lookup + Step 18 OIDC group role mapping) | 69 |
| S1-41 | registry (component definition resolver) | 12 |
| S1-42 | admin definitions (Phase B: application definition CRUD) | 12 |
| S1-43 | admin definitions validate (Phase C: ValidateDefinition) | 10 |
| S1-44 | admin definitions publish (Phase C: PublishDefinition) | 12 |
| S1-45 | admin registry handler (Phase D: ListComponentDefinitions) | 1 |
| S1-46 | history (DB role mapping + round-trip) | 4 |
| S1-47 | summarizer (LLM-based conversation summarizer) | 4 |
| S1-48 | agentgen (Phase 1 A2A Agent Runtime: invariants + interpreter) | 14 |
| S1-49 | agent definitions (Phase 2 Canvas A2A Builder CRUD) | 21 |
| S1-50 | agent definition compiler (BuildValidator: Issue type, Validate/CompileForPublish, severity split) | 20 |
| S1-51 | agent definition publish service | 11 |
| S1-52 | dashboard WebSocket handler | 13 |
| S1-53 | agent-runtime spec cache + skill routing + policy enforcement | 12 |
| S1-54 | node definition registry (all 12 types, metadata, Validate, ToInfo) | 18 |
| S1-60 | admin/service provider key encryption | 9 |
| S1-61 | temporal/workerconfig loader contracts | 2 |
| S1-93 | temporal/workerconfig managed app param substitution | 3 |
| S1-62 | admin/service app global params (AGP-1..8) | 8 |
| S1-63 | agentgen compiler app_param_ref (CMP-10..14) | 5 |
| S1-64 | agentgen interpreter app_param_ref (INT-10..14) | 5 |
| S1-65 | agent-runtime decodeAppGlobalParams (RT-20..24) | 5 |
| S1-66 | admin handler app params (HTTP-20..25+) | 11 |
| S1-67 | Stage 6 runtime contract enforcement (CONT-1..12) | 12 |
| S1-68 | explicit canvas data bindings (BND-1..10) | 10 |
| S1-69 | MCP call node + executor (MCP-1..10) | 10 |
| S1-72 | ExecutionPlan compiler — EP-1..10 + PC-LOOP-1..6 (plan_compiler_test.go) | 20 |
| S1-73 | LocalExecutor — EP-L1..L15 + concurrency limit + EP-LOOP-1..8 (local_executor_test.go) | 33 |
| S1-74 | DAG E2E smoke tests (BranchConvergence true/false + ParallelTransforms both run) | 3 |
| S1-75 | Phase 4-A: ExecuteNodeForActivity, ActivityIC, ExecutionBackend (NA-01..16) | 16 |
| S1-76 | Phase 4-B: CanvasAgentWorkflow, CanvasAgentActivities (CT-01..10 + CT-A..F + CT-CONC1 + CT-LOOP-DURABLE-1..7) | 21 |
| S1-77 | Phase 4-C + 5-B: TemporalExecutor (TE-01..13) | 13 |
| S1-78 | dag-worker SQL tenant scope | 4 |
| S1-79 | HITLStore Phase 5-B (HS-1..11): state machine, UpdateWaitToken, TrySignal CAS, MarkDone, RepeatedWait | 11 |
| S1-80 | agent-runtime HITL Phase 5-B (RT-HITL-1..5): ReturnsWorking, StoresHandle, HITLRequestHandler CancelTask/SubscribeToTask | 5 |
| S1-81 | Canvas HITL signal admin endpoint (CSIG-1..4): Success, NotFound, CrossTenant, WrongToken | 4 |
| S1-82 | A2A Call node Phase 5-C + 5-C gaps (A2A-1..18): NodeRegistered, Validate missing/valid, Execute no-caller/calls-caller/error/depth/self-call/depth-cap+HTTPA2ACaller cap, HumanWait local/temporal, HTTPA2ACaller integration (all 4 headers), DeriveOutputs default, fail-closed no-binding, stable request IDs, remote error sanitization, E2E LocalExecutor (headers+tenant isolation), E2E ExecuteNodeForActivity (depth propagation) | 18 |
| S1-83 | StreamOut node Phase 5-D (SO-1..10): ReadsFromVar, DefaultMediaType, ExplicitMediaType, MissingVar, DefaultFromVar, Validate_MissingFromVar, Validate_Valid, DeriveInputs, DeriveInputs_DefaultVar, FullPipeline (LLM→stream_out) | 10 |
| S1-84 | middleware/gate FileGate (quarantine-first): Disabled (no MinIO call), FetchFailsOpen, InvalidateCache, InterceptInline_Enabled (bytes to MinIO + job enqueued), InterceptInline_Disabled, StoreFail_FailsOpen | 6 |
| S1-85 | admin security_config handler (Phase 3): Get returns default, Put valid config 200, Put invalid JSON 400, Put av_scan.max_file_mb=0 → 422 | 4 |
| S1-86 | admin services stats handler: GetStats_OK (200 + security key in envelope), WindowParam (24h/7d/30d/"" all accepted) | 2 |
| S1-87 | middleware pipeline (Registry, Config, Pipeline: 16 tests) + av/clamav scanner (9 tests): TCP+Unix dial, INSTREAM protocol, null-byte response, fail-open, TestPipeline_ProcessorError_ReturnsError | 25 |
| S1-88 | middleware/job quarantine-first DAL: EnqueueWithQuarantine, LoadFileBytes_QuarantinePath (MinIO fetch), Complete_CleanPath (promote + delete quarantine), Complete_InfectedPath (metadata-only + delete quarantine), Complete_LegacyPath (backward compat) | 5 |
| S1-89 | internal/storage: New_InvalidEndpoint, New_ValidEndpoint, New_HTTPSEndpoint | 3 |
| S1-90 | orchestrator scan subscriber: FileScanningEvent (file_scanning emitted when gated), ScanResult_Clean (file event after clean), ScanResult_Infected (file_blocked + threat field), ScanResult_Timeout (fallback file event on timeout) | 4 |
| S1-91 | quarantine reaper: DeletesExpiredRows, NoRows, MinIOErrorDoesNotBlockDBDelete, EmptyStorageKeySkipsMinIO, QueryErrorIsHandled | 5 |
| S1-92 | Managed Apps catalog + platform bindings (MA-01..14): List_Empty, List_Populated, Create_Success, Create_MissingName, Get_Found, Get_NotFound, PutParams, Bindings_List, Binding_Upsert, Binding_MissingConfig, ListBindingsByTenant, ListBindingsByTenant_Empty, UpsertBindingByTenant, UpsertBindingByTenant_MissingConfig | 14 |
| S1-93 | workerconfig managed app params (MAP-01..04): ConfigSubstitution, NilSafe, ZeroNil, TenantProviderKey_NilPoolSafe | 4 |
| S1-94 | Tenant CRUD + PATCH + quota + members + email domain + group mappings handler (TN-01..25 + GM-01..07): List_Empty, List_Populated, Get_Found, Get_NotFound, Create_Success, Create_MissingSlug, Create_MissingDisplayName, Create_BadJSON, Patch_Success, Patch_NotFound, Patch_BadJSON, Patch_IDPConfigured, GetQuota_NotFound, GetQuota_Found, UpsertQuota_Success, UpsertQuota_BadPlan, UpsertQuota_BadJSON, ListMembers_Empty, ListMembers_Populated, AddMember_Success, AddMember_MissingUserID, AddMember_MissingRole, Patch_EmailDomain, Patch_EmailDomain_Clear, List_WithEmailDomain, ListGroupMappings_Empty, ListGroupMappings_Populated, UpsertGroupMapping_Success, UpsertGroupMapping_MissingGroupClaim, UpsertGroupMapping_InvalidRole, DeleteGroupMapping_Success, DeleteGroupMapping_NotFound | 32 |
| S1-95 | quota enforcer (QE-01..09): NilLimits, ConcurrentBelowLimit, ConcurrentAtLimit, RPMBelowLimit, RPMExceeded, DBError, MonthlyNilLimit, MonthlyBelowLimit, MonthlyExceeded | 9 |
| S1-96 | per-tenant LLM provider service (TLP-01..06): ListForTenant_ReturnsMerged, ListForTenant_EmptyReturnsEmptySlice, Upsert_PlatformNotFound_ReturnsNotFound, Upsert_MissingDefaultModel_ReturnsValidation, Upsert_Success_EncryptsKey, Upsert_InheritsDisplayNameFromPlatform | 6 |
| S1-97 | per-tenant LLM provider handler (TLP-01..05): List_200_Empty, List_400_MissingID, Upsert_200, Upsert_404_PlatformNotFound, Upsert_400_BadJSON | 5 |
| S1-98 | DB Pools (RLS): BadAppDSN, InterfaceAssertions, Close_NilSafe, TenantIDFormat | 4 |
| S1-99 | dbtype Querier interfaces (RLS): TestInterfaceDistinction | 1 |
| **S1 total** | | **1087** |
| S2-01 | integration | 4 |
| S2-02 | hybrid integration | 8 |
| S2-03 (streamer) | runstream streamer (Redis, in S1-23) | 1 |
| S2-03 (MAXLEN) | runstream MAXLEN + reconnect + cross-replica | 7 |
| S2-04 | admin tokens + sessions integration | 11 |
| S2-05 | admin/dal llm_providers integration | 11 |
| S2-08 | RLS integration (role attrs + GUC isolation): RLS-30, RLS-31, RLS-31b, RLS-32, RLS-33, RLS-08, RLS-10, RLS-11, PoolsInterface | 9 |
| **S2 total** | | **51** |
| S3 live | manual | 23 |
| **`go test ./...` total** | | **1034** |
