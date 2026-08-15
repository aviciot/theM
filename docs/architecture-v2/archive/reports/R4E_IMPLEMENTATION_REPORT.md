# R-4e Implementation Report
# A2A Inbound Execution Path Alignment
# Date: 2026-08-01
# Status: COMPLETE

---

## 1. Summary

R-4e replaces the direct `orch.Run` call in the inbound A2A handler with the same
tenant-aware Temporal execution pipeline used by WS and SSE. The A2A path now
enforces authentication, EPConfig resolution, access control, admission gate, session
registration, and Temporal dispatch — with TenantID and ApplicationID sourced from
EPConfig (server side), never from the request payload.

---

## 2. Files Changed

| File | Change |
|---|---|
| `go/internal/epconfig/pgx.go` | Added `a.tenant_id` to `epConfigQuery`; updated `Scan` to read `TenantID` |
| `go/internal/a2a/server.go` | Complete rewrite: new `Server` struct, new `NewServer` signature, full R-4e pipeline |
| `go/internal/a2a/server_test.go` | Complete rewrite: 25 tests covering all R-4e requirements |
| `go/cmd/them/main.go` | Updated A2A Server wiring to pass new dependencies |
| `go/TEST_INDEX.md` | S1-14 expanded to 25 tests; counts updated |
| `docs/architecture-v2/R4E_IMPLEMENTATION_REPORT.md` | This document |
| `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` | Updated for next phase |

---

## 3. What R-4e Changed

### 3.1 EPConfig SQL query (`epconfig/pgx.go`)

The `epConfigQuery` was missing `a.tenant_id`. The `EPConfigRow.TenantID` and
`EPConfig.TenantID` fields already existed from R-4a/4b, but the DB query did not
select that column. Added `COALESCE(a.tenant_id::text, '')` and updated `Scan` to
populate `TenantID`. This benefits both the A2A path and WS/SSE (which previously
had `TenantID` empty from the DB even though the struct field existed).

### 3.2 A2A Server struct

**Removed:**
- `*orchestrator.Orchestrator` — direct in-process execution is gone

**Added:**
- `auth.Cache` (as `Authenticator`) — bearer token validation
- `epconfig.Loader` (as `EPConfigLoader`) — EP config resolution from `app_slug`
- `gate.Gate` (as `GateStore`) — admission gate
- `session.Store` (as `SessionStore`) — Redis session registration
- `temporal.Client` (as `TemporalClientExecutor`) — Temporal workflow dispatch
- `instanceID string` — for session info

### 3.3 handleMessageSend execution order

```
1. tryAuthenticate(r)           — non-enforcing bearer token extraction
2. epLoader.Load(app_slug)      — EPConfig resolution (ErrNotFound → 404)
3. AccessMode enforcement       — token EP + no token → 401
4. epconfig.CheckAccess(...)    — disabled/blocked → 403
5. Parse params, extract text
6. Generate runID, contextID (from params or new), sessionID
7. gate.Check(...)              — cap/rate/queue → 429/503
8. session.Register(...)        — Redis session; gate.Rollback on failure
9. gate.Confirm(...)
10. defer session.End + gate.Release (all paths)
11. bus.Subscribe (before workflow start)
12. recorder.CreateRun(...)     — TenantID + ApplicationID + EntryPointSlug
13. temporalCli.ExecuteWorkflow(GoTaskQueue, WorkflowInput{...})
14. wfRun.Get(ctx, &result)     — block until complete
15. Map WorkflowResult → rpcResult (completed state + text artifact)
```

### 3.4 Identity binding

| Field | Source |
|---|---|
| `TenantID` | `EPConfig.TenantID` from `app_slug → entry_points → applications.tenant_id` |
| `ApplicationID` | `EPConfig.AppID` from `app_slug → entry_points → applications.id` |
| `EntryPointSlug` | `app_slug` URL path parameter |
| `OrchestratorName` | `app_slug` (same convention as WS handler, line 515) |
| `RunID` | `newID()` — random 16-byte hex |
| `ContextID` | from `params.message.contextId` if provided; else `newID()` |
| `SessionID` | `newID()` — scoped to this HTTP request |

### 3.5 Wire format fix

The previous `rpcPart` type had `{"kind": "text", "text": "..."}`. A2A spec uses
field presence as the discriminator — the correct wire format is `{"text": "..."}`.
Fixed by replacing `rpcPart` with `rpcTextPart` (text-only). The `taskId` field
was added to `rpcResult` per spec.

### 3.6 Error mapping

| Failure | Response |
|---|---|
| Unknown `app_slug` | HTTP 404 + JSON-RPC error body |
| EP/App disabled | HTTP 403 + JSON-RPC error body |
| No token on token EP | HTTP 401 + JSON-RPC error body |
| Gate cap exceeded | HTTP 429 + JSON-RPC error body |
| Temporal failure | HTTP 500 + static "internal error" (never `err.Error()`) |
| Session register failure | HTTP 500 + static "internal error" + gate.Rollback |

---

## 4. Security Verification

**Token authorization scoped to resolved target:** The caller provides `app_slug` in
the URL. `EPConfig` is resolved server-side from that slug via `eploader.Load`. The
bearer token is validated via `authenticator.Validate`, but access enforcement is via
`epconfig.CheckAccess(resolvedCfg, tokenHash, userID)` — which checks the token hash
against the resolved EP's application block-list, not globally. This matches the
WS/SSE pattern and enforces that a valid token cannot access a different application's
entry point if that application blocks the token hash.

**TenantID injection prevention:** The `messageSendParams` struct has no `TenantID`
or `ApplicationID` fields. Even if a caller sends extra JSON fields, they are ignored
by `json.Unmarshal`. The `contextId` from params is accepted for multi-turn grouping
but carries no identity implications — TenantID and ApplicationID always come from
the DB-resolved EPConfig.

---

## 5. Test Results

```
go test ./...         → 29 packages, 0 failed
go test -race ./...   → 29 packages, 0 data races
Python sanity 01-04,15 → 55 passed, 0 failed
Docker image build    → success (all tests run during build)
```

### R-4e A2A tests (25 total):

- `TestA2A_MissingSlug_404` — PASS
- `TestA2A_DisabledEP_403` — PASS
- `TestA2A_BlockedToken_403` — PASS
- `TestA2A_MissingTokenOnTokenEP_401` — PASS
- `TestA2A_InvalidToken_401` — PASS
- `TestA2A_PublicEP_NoToken_OK` — PASS
- `TestA2A_CapExceeded_429` — PASS
- `TestA2A_TenantIDFromEPConfig` — PASS
- `TestA2A_ClientCannotOverrideTenantID` — PASS
- `TestA2A_WorkflowInputHasTenantID` — PASS
- `TestA2A_SessionRegistered` — PASS
- `TestA2A_SessionEndedOnCompletion` — PASS
- `TestA2A_GateReleasedOnCompletion` — PASS
- `TestA2A_GateRollbackOnRegisterFail` — PASS
- `TestA2A_RPCResult_CompletedState` — PASS
- `TestA2A_RPCResult_HasTaskID` — PASS
- `TestA2A_RPCError_WorkflowFailed` — PASS
- `TestA2A_ContextIDFromParams` — PASS
- `TestA2A_ContextIDGeneratedIfAbsent` — PASS
- `TestA2A_DirectOrchNotUsed_TemporalCalledInstead` — PASS
- `TestA2A_CleanupOnGateFailure` — PASS
- `TestA2AUnknownMethod` — PASS
- `TestA2AMalformedJSON` — PASS
- `TestA2A_TemporalNotConfigured_503` — PASS
- `TestA2A_TemporalInterface_Satisfied` — PASS

---

## 6. Live Validation

- Go bridge health: `{"status":"ok","checks":{"postgres":"ok","redis":"ok"}}` ✓
- A2A unknown slug → HTTP 404 with JSON-RPC error body ✓
- Agent card endpoint → correct JSON response ✓
- WS/SSE behavior: unchanged (no modifications to ws/sse handlers) ✓

---

## 7. Remaining Gaps

| Gap | Severity | Notes |
|---|---|---|
| No live A2A entry point in DB | Low | No `them.entry_points` rows of type `a2a` exist. Live end-to-end test requires creating one via admin API. Functional correctness proven by unit tests. |
| EPConfig SQL previously returned empty TenantID for WS/SSE | Fixed | `a.tenant_id` was missing from `epConfigQuery` since the column was added in R-4a. Now included. WS/SSE benefit from this fix too. |
| Async A2A (`returnImmediately: true`) | Out of scope | Requires task state persistence, `GetTask` endpoint, async lifecycle. Separate feature. |
| HITL for A2A | Out of scope | Requires async semantics. Separate feature. |
| Agent card `security_schemes` | Low | Card doesn't declare Bearer auth requirement. Callers can discover this from 401 responses. |
| Traefik proxy timeout for long A2A runs | Low | Synchronous A2A blocks the HTTP connection. Traefik default timeout may kill connections longer than ~5 min. Not changed in R-4e. |

---

## 8. Architecture Decisions

1. **OrchestratorName = appSlug**: WS uses `appSlug` directly (not a DB field) as
   `OrchestratorName` in `WorkflowInput`. A2A follows the same convention. No DB
   schema change needed.

2. **epConfigQuery fix**: The fix to include `a.tenant_id` benefits WS and SSE too —
   they were silently receiving empty `TenantID` from EPConfig even though the struct
   field and DB column existed. This fix closes an undetected gap in R-4a/4b.

3. **rpcTextPart (no "kind" field)**: The previous `{"kind":"text","text":"..."}` wire
   format was non-spec. Fixed to `{"text":"..."}` (field-presence discriminator). Since
   there are no known production callers of the A2A endpoint, this is not a breaking
   change.

4. **HTTP status codes for auth/access errors**: Unlike WS/SSE (which use HTTP-level
   errors directly before WS upgrade), A2A responses can carry both HTTP status and a
   JSON-RPC error body. This is done via `writeHTTPError` — callers see both the HTTP
   code and the JSON-RPC error object.

---

## 9. Next Phase

**Execution Lifecycle Unification across WS/SSE/A2A**

Now that all three inbound paths (WS, SSE, A2A) use the same execution pipeline
(EPConfig → gate → session → Temporal), the next step is to unify the shared
execution logic into a common helper or `ExecutionPipeline` struct — eliminating
the ~200-line duplication between ws/handler.go, sse/handler.go, and a2a/server.go.

This refactor should be planned in a separate session with an architecture review
before implementation. It does not fix any correctness issue — R-4e is complete and
correct as-is. The unification is purely a code quality improvement.

See `NEXT_SESSION_HANDOVER.md` for the exact scope of the next task.
