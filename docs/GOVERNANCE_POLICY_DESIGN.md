# Governance Policy — Design (Step 1)
# Status: DRAFT for review — no code yet. Written 2026-10-19.
# Follows: docs/HANDOVER_OBSERVABILITY_POLICY.md (Option A chosen)

## Goal

One policy model that answers, per request: **what may this caller spend, which models may it use, and how much do we record about it?**
It must scale from today's "tenant budget" to "AI control tower for the org" without a schema rewrite each time.

Two concerns, one shape, kept as separate sections of the same document:

| Section | Question | Failure mode |
|---|---|---|
| `limits` | May this call proceed? (tokens / USD / requests / models) | Block, warn, downgrade |
| `logging` | What do we persist about it? | Capture level, sampling |

## Scopes (who a policy applies to)

```
org (future) → tenant → application → gateway client → end user
```

| Scope | Identity key | Exists today? |
|---|---|---|
| tenant | `tenant_id` | yes (`gateway_policies`) |
| application | `application_id` | yes (app / entry point) |
| gateway client | `gateway_clients.id` (via `policy_profile_id`) | yes |
| end user | caller-supplied subject (see Open Q1) | **no** |
| org / team / model-group | — | future; scope table is open-ended |

**Resolution rules (this is the key decision):**
- **`limits` = ALL applicable scopes are enforced; the tightest wins.** A user under a $50 cap inside a tenant with $5,000 left is stopped at $50. A tenant out of budget stops every user.
- **`logging` = most specific scope wins** (user > client > app > tenant > default `minimal`). Debugging one client must not require changing the whole tenant.
- A scope with no policy is simply skipped (inherits). Default for everything: `logging.level=minimal`, no limits (matches today's fail-open for limits).

## Policy document (JSONB, versioned)

```jsonc
{
  "schema_version": 1,
  "limits": {
    "models":   { "allow": ["claude-*"], "aliases": {"fast": "claude-haiku-4-5-20251001"} },
    "per_request": { "max_tokens": 4096 },
    "budgets": [
      { "metric": "usd",    "period": "month", "limit": 500,    "on_exceed": "block"  },
      { "metric": "tokens", "period": "day",   "limit": 2000000,"on_exceed": "warn"   },
      { "metric": "requests","period": "minute","limit": 60,    "on_exceed": "throttle"}
    ],
    "warn_at": 0.8
  },
  "logging": {
    "level": "minimal",            // minimal | metrics | debug | audit
    "sample_rate": 1.0,            // applies to debug/audit capture only; metering is never sampled
    "capture_bodies": false,
    "capture_pii_decisions": false,
    "capture_file_guard": false,
    "retention_days": null
  }
}
```

Design points:
- `budgets` is a **list of `{metric, period, limit, on_exceed}`**, not fixed columns. New metrics (e.g. `images`, `tool_calls`) or periods are data, not migrations.
- `on_exceed`: `block` (429 `budget_exceeded`), `warn` (header + event, allow), `throttle` (429 with Retry-After), `downgrade` (reroute to a cheaper alias) — later.
- **Metering is never sampled or disabled.** Only *debug/audit capture* is policy-controlled. Usage counters must stay exact (billing + budget correctness).
- Levels: `minimal` = `runs` + `run_usage` + `gateway_requests`; `metrics` adds per-node timing/status (no I/O); `debug` adds `run_steps` I/O; `audit` adds bodies + PII/file-guard decisions.

## Storage

One table (option **b** in the export/import rule — it must be editable live without a publish, and it spans scopes that are not canvas-owned):

```sql
them.governance_policies (
  id            UUID PK,
  tenant_id     UUID NOT NULL,           -- RLS boundary, always set
  scope_type    TEXT NOT NULL,           -- 'tenant'|'application'|'client'|'user'
  scope_id      TEXT NOT NULL,           -- uuid or subject string
  doc           JSONB NOT NULL,          -- the document above
  version       INT NOT NULL DEFAULT 1,
  updated_at    TIMESTAMPTZ, updated_by TEXT,
  UNIQUE (tenant_id, scope_type, scope_id)
)
```

- Existing `gateway_policy_profiles` / `gateway_policies` stay readable. Loader maps legacy columns (`allowed_models`, `max_tokens_per_request`, `monthly_budget_usd`) into the doc shape at the lowest precedence, so rollout needs no data migration.
- **Export/import:** `scope_type='application'` rows must be added to the Deploy-to-Tenant copy list in `go/internal/admin/dal/app_deploy.go` (project hard rule). Tenant/client/user rows are tenant-level governance, not canvas config, and are deliberately not copied.

## Enforcement (counters)

- Redis counters per **(tenant, scope_type, scope_id, metric, period-bucket)**, e.g. `them:budget:{tenant}:{scope_type}:{scope_id}:usd:2026-10`. Atomic `INCRBYFLOAT`, TTL = period end + grace.
- Check = read every applicable scope's counter in one pipelined round trip; Postgres `SUM` is the **fallback only** when Redis is down (fail closed for `block` budgets).
- Counters are a cache; a periodic reconcile job rebuilds them from `gateway_requests` / `run_usage`.
- Policy docs cached in Redis (existing `them:gateway:policy:*` pattern) with invalidate-on-write.
- All new keys documented in `docs/REDIS.md`.

## Proposed Go layout

New package `go/internal/governance/`:
- `policy.go` — `Policy`, `Limits`, `Logging` types + `Validate()`
- `resolve.go` — merge across scopes (tightest limits, most-specific logging)
- `meter.go` — Redis counters + DB fallback (step 3 of the plan)
- `dal.go` — Handler → Service → DAL, no SQL in handlers
Callers (`runrecorder`, `llmgateway`, `dag-worker` activities) take a resolved `EffectivePolicy` — they never read raw scopes.

## Slicing

1. **This slice:** types + `Validate()` + `Resolve()` + tests, migration + `docs/SCHEMA.md` + `db/001_schema.sql`, legacy-column mapping. No behavior change.
2. Wire `logging.level` into `runrecorder` / gateway writes.
3. Redis meters + enforcement (`limits.budgets`), incl. user scope.
4. Load test (200 users), then admin UI.

## Decisions (2026-10-19)

1. **User identity = the existing runtime identity model, no new mechanism.** `docs/END_USER_AUTH_PLAN.md` already defines it: `UserID` (the-M user / token owner), `ExternalUserID` (`X-External-User` from a backend token, or external JWT `sub`), `IsBackend`. Each entry point's Access Policy already decides which credential is accepted, so identity is naturally per-EP. The `user` scope key is a normalized principal string:
   - `internal:<user_id>` / `external:<external_user_id>` (tenant-scoped, so no cross-issuer collision).
   - Gateway clients without an end user have no `user` scope (client scope only).
   - A per-EP override (e.g. "meter by external user" vs "by client") can be added later as an optional `governance.identity` field in the EP config; not needed for slice 1.
2. **`on_exceed` is an open string enum**, default `block`. Unknown actions are rejected by `Validate()` today, but the enforcement point is a registry (`action name -> handler`), so adding `downgrade`, `notify`, `approval` etc. means registering a handler, not changing the schema.
3. **Table:** see "Relationship to existing gateway policy" below.

## Relationship to existing gateway policy

Today: `gateway_policies` (one row per tenant, legacy) and `gateway_policy_profiles` (named bundles assigned to gateway clients via `policy_profile_id`). Both only cover the **gateway** and only tenant/client levels. They have no app scope, no user scope, no logging section, no multi-metric budgets.

`governance_policies` is the generalisation, not a second system: it adds the missing scopes and sections. Existing profiles keep working and are read through the legacy mapping; a client's `policy_profile_id` is just how the client scope finds its policy. Once the new loader is proven the legacy columns can be deprecated.

## Decision: single table from day one (2026-10-19)

- Slice 1 copies rows from `gateway_policies` and `gateway_policy_profiles` into `governance_policies`, and switches the gateway runtime and admin screens to read only the new table.
- The old tables stay in the DB, unused, as a rollback net.
- **Drop the old tables as early as possible once stable:** a dedicated follow-up migration (`DROP TABLE gateway_policies, gateway_policy_profiles` + remove `gateway_clients.policy_profile_id`), done after the load test passes and a short soak with no reads of the old tables. Tracked in `docs/STATUS.md` with an owner and a target date so it does not linger.

## Update: `profile` scope (2026-10-19)

Gateway clients pick a reusable named bundle ("Production", "Dev"), so the table has a fifth `scope_type = 'profile'` (with `name`), and `gateway_clients.governance_profile_id` references it. Resolution loads the client's profile doc as the **client** scope. Per-client overrides use `scope_type='client'`.
