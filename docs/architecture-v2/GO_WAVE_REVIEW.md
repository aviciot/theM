# Go Runtime — Waves 1-4 Architecture Review
# Date: 2026-07-24

---

## Push Result

The remote requires HTTPS credentials — run `git push origin main` yourself. All commits are clean locally:

```
72824e0  fix: test_11 bearer token uses user_id=1 (admin), not non-existent 99
5b32995  fix: Go admin handlers — align schemas, add HS256 auth, fix all admin routes
1828e39  docs: update STATUS.md routing table and CLAUDE.md trigger map for Waves 1-4
123e04f  feat: Wave 4 — Go bridge takes WS and SSE traffic for app entry points
fd3fe9e  feat: Wave 3 — gate, agent registry, and pod heartbeat wired in Go main.go
7dae98e  feat: Wave 2 — Go bridge takes admin write routes + PATCH method aliases
0ceef8d  feat: Wave 1 — Go bridge takes /health/live, /health/ready and GET admin reads
f897d5a  test: Phase 3 — Go↔Python parity contract test (test 37)
077770f  fix: P3 — unify Go↔Python Redis pub/sub channel names
2738347  fix: P1 — Go↔Python Temporal seam: workflow ID, HITL signal name, signal target
609b1d8  docs: Go runtime migration inventory + implementation plan
```

---

## Final Test Totals

**929 passed, 0 failed, 6 skipped**

The 6 skips are legitimate env gaps (`structlog`/`fastapi` missing on host, `code_agent` unreachable). None are masked failures.

---

## Why were schema mismatches discovered at cutover and not earlier?

The Go admin handlers were written against a schema specification that diverged from the live deployed schema. The root cause is structural: **there is no shared schema contract between Go and Python**. The Python `models.py` is the authoritative source, but no code generation, shared SQL file, or schema test enforced parity. The Go structs were written by reading Python source manually, and the manual read got wrong column names, missing columns, and wrong ID types. The schema mismatches would have been caught by integration tests against the live DB, but none existed at the time of implementation — the Go unit tests used in-memory fakes (`fakeRows`, `fakeDB`), which can only test behavior, not schema alignment.

---

## Canonical Source of Truth for DB Schema and API Contracts

| Concern | Canonical source | Go equivalent |
|---|---|---|
| DB schema | `db/001_schema.sql` | None — structs hand-written |
| Python API contract | `app/routers/admin_*.py` (Pydantic models) | None — structs hand-written |
| Redis key names | `docs/REDIS.md` | String literals in each package |
| EP types | Python `_VALID_EP_TYPES` list | `validEPTypes` map in `applications.go:14` — comment says "must stay in sync" |

There is no code generation, no shared proto/OpenAPI spec, no schema introspection. Parity is maintained by reading Python source and manually replicating it.

---

## SQL Query Location — Scattered, Not Centralized

All SQL is inline in the handler files. There is no repository/storage layer:

```
admin/agents.go:89        const q = "SELECT id::text, slug, ..."  (23 columns)
admin/orchestrators.go:97 const orchSelectCols = "id::text, name, ..."
admin/applications.go:131 ep slug lookup inside cache invalidation helper
admin/runs.go:64          const runSelectCols = ...
```

The `orchSelectCols` and `runSelectCols` constants are the only structural relief — they prevent column-list drift between `Get` and `List` within one file. They do not help across files, and they do not exist in `agents.go` or `applications.go`.

---

## Business Logic in Handlers

Both SQL and business logic live in handlers. Specifically:

- **Default value injection** (`agents.go:161–174`): `Create` sets `Transport="a2a_async"`, `MaxConcurrency=5`, `MaxRetries=2`, `TimeoutSeconds=30` when omitted — business policy in the handler.
- **Soft-delete semantics** (`agents.go:316`, `orchestrators.go:244`, `applications.go:234`): `Delete` sets `enabled=false`. The handler decides this, not a service layer.
- **Temporal workflow ID construction** (`runs.go:183–195`): `Signal` constructs `"ctx-" + contextID` — protocol knowledge baked into the handler.
- **Pre-update SELECT for cache invalidation** (`applications.go:312–316`): mutation handler fetches old slug before updating, purely to invalidate both old and new cache keys.
- **EP type validation** (`applications.go:14–28`): domain constraint enforced in the handler via `isValidEPType()`.

---

## Duplicated Types Between Packages

**`tokenHash` function — 3 copies:**
- `auth/jwt.go:289` (canonical)
- `ws/handler.go:679` (copy, comment says so)
- `sse/handler.go:62` (copy, comment says so)

**5 interfaces duplicated between `ws` and `sse`:** `Authenticator`, `SessionStore`, `GateStore`, `EPConfigLoader`, `TemporalClientExecutor` — identical signatures declared independently in each package.

**`AgentConfig` (`agentregistry`) and `Agent` (`admin`)** cover the same DB table with different field sets — overlapping projections of the same source with no shared definition.

---

## Temporary Compatibility Code Inventory

All of the following exist only to bridge Go↔Python during migration:

| Location | What it is |
|---|---|
| `admin/agents.go:83` | `r.Patch("/agents/{id}", h.Update)` — PATCH alias for Python frontend |
| `admin/orchestrators.go:80` | `r.Patch("/orchestrators/{name}", h.Update)` — same |
| `admin/applications.go:83,88` | Two PATCH aliases for app + EP update |
| `admin/agents.go:14–15` | Comment: "Field names match Python's AgentOut schema exactly" |
| `admin/orchestrators.go:14` | Comment: "Field names match Python's OrchestratorOut schema exactly" |
| `admin/runs.go:16` | Comment: "Field names match Python's RunOut schema" |
| `admin/runs.go:182` | Hardcoded `"ctx-"` prefix — "Python's OrchestrationWorkflow registers as ctx-{context_id}" |
| `admin/applications.go:15` | `validEPTypes` comment: "Must stay in sync with Python's _VALID_EP_TYPES list" |
| `ws/handler.go:679`, `sse/handler.go:62` | `tokenHash` copies with comments about matching Python's hash format |
| `runrecorder/recorder.go:44–46` | `NewRecorder` — "alias for New for backward compatibility" |
| `runrecorder/recorder.go:104–107` | `UpdateStatus` — "compatibility wrapper over UpdateRunStatus" |
| `agentregistry/registry.go:19` | `invalidateChannel` value comment: "matches Python admin_agents.py publisher" |
| `main.go:281–311` | `buildAppsHandler` — URL param rewriting (`slug` → `entry_point_slug`) to map `/apps/{slug}/ws` into the WS handler's expected param name |

---

## Did Parity Fixes Preserve the Python Contract?

Yes. The schema rewrites (`agents.go`, `orchestrators.go`, etc.) were driven by comparing Go output against live Python output field-by-field. The Go test structs in `admin_test.go` were updated to match the new correct structs — the tests were not weakened to accept wrong behavior, they were corrected to test the right schema.

---

## Package/Layer Map

**Intended clean separation:**
```
request → handler → service/runtime → repository/storage → PostgreSQL / Redis / Temporal
```

**Actual current layout:**
```
request
  → chi router (server.go)
    → admin handler (agents.go / orchestrators.go / applications.go / runs.go)
        ↓ SQL inline here (no service or repository layer)
        ↓ business logic inline here (defaults, soft-delete, type validation)
        ↓ cache invalidation inline here
      → pgxpool.Pool (direct)
      → admin.AdminCache (Redis, direct)
      → temporal.Signaler (for HITL only)

request
  → ws/handler.go or sse/handler.go
      ↓ auth inline (tokenHash, Validate)
      ↓ gate inline (Check → Register → Confirm)
      ↓ session inline
      ↓ run recording inline
      → orchestrator.Orch (agentic loop)
        → llm.Provider (Anthropic)
        → agentregistry.Registry
      → temporal.Client (workflow start)
      → runstream.Dispatcher (event fan-out)
```

**Deviations from clean separation:**
1. **No repository layer** — admin handlers hold SQL directly. Handlers are both controller and DAO.
2. **No service layer** — business logic (defaults, soft-delete, validation, cache invalidation) lives in handlers.
3. **No shared domain package for admin types** — DB row struct = HTTP response struct = the same Go struct.
4. **WS and SSE handlers are not handlers** — they own session lifecycle, gate admission, auth, run recording, and Temporal workflow management. They are larger than the entire service layer should be.
5. **main.go contains routing logic** — `buildAppsHandler` at line 281 does URL parameter rewriting that belongs in a middleware or handler adapter.

---

## Top 3 Spaghetti Risks

**1. `ws/handler.go` and `sse/handler.go` as parallel universes.**
Five interfaces duplicated, `tokenHash` duplicated, every feature (Temporal, streaming modes, gate, EP config) added independently to both. Adding voice or WebRTC either copies the pattern again or forces a painful shared-base extraction.

**2. Admin handlers with no repository layer.**
12 distinct SQL strings in `applications.go` alone. When the schema adds columns or joins, every SELECT in every handler must be updated. The `orchSelectCols`/`runSelectCols` constants are the only mitigation and they don't cross file boundaries.

**3. `main.go:run()` as a 260-line sequential wiring monolith.**
Each new subsystem is another numbered block. `buildAppsHandler` (routing logic) is already embedded here. As optional profiles multiply, this becomes impossible to test or restructure without touching everything.

---

## Recommendation

**Perform a focused refactor before Wave 5.**

Wave 5 will add more routes into the same `internal/admin/` pattern, more handler duplication, and more inline SQL. Without a repository layer, the next 5 admin routes will add another 200 lines of inline SQL across 5 files. Without extracting the shared WS/SSE base, the next transport doubles the interface count again.

The refactor needed is narrow and bounded — not a redesign:

1. **Extract a `dal` (data access layer) package** under `internal/admin/dal/` — move all SQL constants and `pgx` scan calls there. Handlers become thin HTTP translators. This is mechanical, low-risk, and immediately stops the SQL sprawl before Wave 5 adds 4 more admin endpoints.

2. **Extract shared interfaces and `tokenHash` into `internal/transport/`** — `ws` and `sse` import from it. Kills 5 duplicate interface definitions and 2 duplicate functions. Required before adding a third transport.

3. **Nothing else.** PATCH aliases, compatibility comments, and the `buildAppsHandler` URL rewriting are load-bearing during migration — leave them until Python is fully decommissioned. `main.go` wiring length is a readability problem, not a correctness problem — address after Wave 5.

The refactor touches no external contracts, no DB schema, no Traefik config, and no Temporal. It can be done in one session before Wave 5 begins.
