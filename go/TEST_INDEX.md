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

**Trigger:** any change to `internal/session/session.go`

---

### S1-07 · Event bus — `internal/event/bus_test.go`

**Purpose:** In-process fan-out bus — never blocks on slow consumers.

| Test | What it proves |
|---|---|
| `TestPublish_specificTopic` | Event delivered to matching subscriber |
| `TestPublish_wrongTopic` | Event NOT delivered to non-matching subscriber |
| `TestWildcard` | `"*"` subscriber receives all topics |
| `TestSlowConsumer` | Full channel → event dropped, bus does not block |
| `TestUnsubscribe` | Unsubscribe closes channel, no further events delivered |
| `TestConcurrentPublish` | Concurrent publishes → no data race (run with `-race`) |

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

| Test | What it proves |
|---|---|
| `TestCreateRun_callsCorrectSQL` | `INSERT INTO them.runs` with correct column order |
| `TestUpdateRunStatus_withErrorMessage` | `UPDATE` sets `ended_at`, `status`, `error_message` |
| `TestUpdateRunStatus_completed` | Completed run → empty error_message |
| `TestRecordUsage_insertsCorrectly` | `INSERT INTO them.run_usage` correct args |
| `TestRecordStep_insertsCorrectly` | `INSERT INTO them.run_steps` correct args |
| `TestDBError_propagates` | DB error is wrapped and returned, not swallowed |

**Trigger:** any change to `internal/runrecorder/recorder.go`

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

**Purpose:** Agent invocation routing + two-level Redis cache + pub/sub invalidation.

| Test | What it proves |
|---|---|
| `TestInvokeMock` | Mock adapter returns immediately without HTTP |
| `TestInvokeA2A` | A2A adapter sends correct JSON-RPC 2.0 request, extracts result |
| `TestCacheMissThenPopulate` | Cache miss → DB load → Redis populated |
| `TestPubSubInvalidation` | Pub/sub message on `them:agents:invalidate` → in-process cache cleared |
| `TestUnknownSlug` | Unknown agent slug → `ErrUnknownAgent` (typed sentinel) |

**Trigger:** any change to `internal/agentregistry/registry.go`

---

### S1-12 · WebSocket handler — `internal/ws/handler_test.go`

**Purpose:** WS connection lifecycle — auth, session, orchestration, disconnect, and Gate contract enforcement.

| Test | What it proves |
|---|---|
| `TestUnauthenticated` | No token → 401 before upgrade |
| `TestAuthenticatedUpgrade` | Valid token → 101 Switching Protocols |
| `TestMessageAndDone` | User message → token events → `{"type":"done"}` received |
| `TestDisconnectEndsSession` | Client close → `session.Store.End` called |
| `TestGateCapExceeded` | Gate returns `ErrCapExceeded` → 503 before WS upgrade; session never registered |
| `TestGateAdmittedAndReleased` | Gate admitted → Check→Confirm called; Release called on session end |
| `TestGateRollbackOnRegisterFailure` | `session.Register` fails → Gate.Rollback called; Confirm never called |

**Trigger:** any change to `internal/ws/handler.go`

---

### S1-13 · SSE handler — `internal/sse/handler_test.go`

**Purpose:** Server-Sent Events endpoint and Gate contract enforcement.

| Test | What it proves |
|---|---|
| `TestSSEUnauthenticated` | No token → 401 |
| `TestSSETokenEvents` | Valid auth + message → token events in SSE format |
| `TestSSEDoneClosesStream` | Done event → stream closed |
| `TestSSEGateCapExceeded` | Gate returns `ErrCapExceeded` → 503 before SSE headers sent |
| `TestSSEGateAdmittedAndReleased` | Gate admitted → Check→Confirm called; Release called on stream end |
| `TestSSEGateRollbackOnRegisterFailure` | `session.Register` fails → Gate.Rollback called; error SSE event emitted |

**Trigger:** any change to `internal/sse/handler.go`

---

### S1-14 · A2A server — `internal/a2a/server_test.go`

**Purpose:** JSON-RPC 2.0 protocol compliance.

| Test | What it proves |
|---|---|
| `TestA2AMessageSend` | `message/send` → correct JSON-RPC result with `state: completed` |
| `TestA2AUnknownMethod` | Unknown method → error code `-32601` |
| `TestA2AMalformedJSON` | Unparseable body → error code `-32700` |

**Trigger:** any change to `internal/a2a/server.go`

---

### S1-15 · Admin API — `internal/admin/admin_test.go`

**Purpose:** CRUD correctness, cache invalidation, Temporal signal wiring.

| Test | What it proves |
|---|---|
| `TestListAgentsEmptyArray` | Empty DB → returns `[]` not `null` (JSON safety) |
| `TestCreateAgent` | POST → 201 with `Location` header |
| `TestGetNonexistentAgent` | Unknown ID → 404 |
| `TestListRunsContextIDFilter` | `?context_id=` → correct SQL WHERE clause |
| `TestSignalRun` | POST `.../signal` → Temporal `SignalWorkflow` called with correct args |

**Trigger:** any change to `internal/admin/agents.go`, `orchestrators.go`, `applications.go`, `runs.go`

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

## Suite 2 — Integration tests (`go test -tags=integration ./...`)

Requires live Postgres + Redis + the Go binary. Run after deployment to staging or production.
Build tag: `//go:build integration` — skipped by default with `go test ./...`.

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
| `internal/auth/jwt.go` | S1-04 |
| `internal/auth/token_cache.go` | S1-05 |
| `internal/session/session.go` | S1-06 |
| `internal/event/bus.go` | S1-07 |
| `internal/domain/domain.go` | S1-08 |
| `internal/runrecorder/recorder.go` | S1-09 |
| `internal/llm/` (any file) | S1-10 |
| `internal/agentregistry/registry.go` | S1-11 |
| `internal/ws/handler.go` | S1-12 |
| `internal/sse/handler.go` | S1-13 |
| `internal/a2a/server.go` | S1-14 |
| `internal/admin/` (any file) | S1-15 |
| `internal/ratelimit/limiter.go` | S1-16 |
| `internal/gate/gate.go` | S1-17 |
| `cmd/them/main.go` | S1 (full suite) |
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
| S1-01 | config | 9 |
| S1-02 | health | 5 |
| S1-03 | server | 4 |
| S1-04 | auth/jwt | 9 |
| S1-05 | auth/token_cache | 5 |
| S1-06 | session | 7 |
| S1-07 | event | 6 |
| S1-08 | domain | 3 |
| S1-09 | runrecorder | 6 |
| S1-10 | llm | 6 |
| S1-11 | agentregistry | 5 |
| S1-12 | ws | 7 |
| S1-13 | sse | 6 |
| S1-14 | a2a | 3 |
| S1-15 | admin | 5 |
| S1-16 | ratelimit | 3 |
| S1-17 | gate | 16 |
| **S1 total** | | **105** |
| S2-01 | integration | 4 |
| **S2 total** | | **4** |
| S3 live | manual | 23 |
| **Grand total** | | **132** |
