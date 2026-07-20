# THEM Go Gateway — Schema Migration Backlog

Last updated: 2026-07-20

This file tracks pending PostgreSQL schema changes that are deferred because they
require coordination between the Go gateway and the Python platform, or because
the Go gateway is not yet the sole writer to the relevant tables.

Each entry states: the gap, the proposed fix, backward-compatibility constraints,
migration timing, rollout/rollback plan, and any related identity fields.

---

## MIG-001 — Add user identity to `them.runs`

### Current gap

`them.runs` currently has no column for the identity that initiated a run:

```sql
-- current schema (relevant columns only)
CREATE TABLE them.runs (
    id               TEXT PRIMARY KEY,
    context_id       TEXT NOT NULL,
    application_id   TEXT,
    entry_point_slug TEXT,
    status           TEXT NOT NULL,
    started_at       TIMESTAMPTZ,
    ended_at         TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ,
    error_message    TEXT
);
```

Consequence: **anonymous and authenticated runs are indistinguishable in persisted
audit history.** The Go gateway stores `session.SessionInfo.UserID = 0` in Redis
for anonymous sessions, but nothing in the DB distinguishes them from runs that
had a real authenticated user with `TokenID > 0`.

### Proposed columns

```sql
ALTER TABLE them.runs
    ADD COLUMN user_id       BIGINT    NULL,  -- FK-like: NULL = anonymous/unknown
    ADD COLUMN session_id    TEXT      NULL,  -- gate session UUID (ephemeral but useful for correlation)
    ADD COLUMN client_ip     INET      NULL,  -- request remote addr (after proxy headers resolved)
    ADD COLUMN ep_type       TEXT      NULL;  -- denormalised from entry_points.ep_type at run time
```

**`user_id`** — The `TokenID` from `them.access_tokens`. NULL for anonymous public EP
sessions (`AccessMode == "public"` with no bearer token). 0 must never be stored
(0 is the zero-value sentinel in Go, not a valid DB token ID). Only store when
`tokenInfo.TokenID > 0`.

**`session_id`** — The ephemeral session UUID generated per-connection by the WS/SSE
handler. Already generated; currently stored only in Redis, not DB. Enables
correlating a run with its session's Redis state and gate admission record.

**`client_ip`** — The resolved client IP after processing `X-Forwarded-For` / `X-Real-IP`
headers (following the same logic that rate-limiting or WAF would use). Useful for
abuse investigations. NULL if the IP cannot be determined.

**`ep_type`** — Denormalised `entry_point_type` at run-creation time. Avoids a JOIN
to `them.entry_points` for analytics queries over large run sets.

### Backward compatibility with Python

The Python platform (`app/`) is the current primary writer to `them.runs` via
`init_run_activity`. As of the Go gateway Phase 5.4, both platforms write to this
table independently.

**The Python platform would need to be updated to populate these new columns, or the
columns must be NULLABLE with sensible defaults.** All four proposed columns are
NULLABLE, so:

- Python-written rows will have `NULL` in all four new columns.
- Go-written rows will populate `user_id` (when authenticated), `session_id`, and
  `client_ip`; `ep_type` is derivable at write time.
- Analytics queries must treat `NULL user_id` as "unknown/legacy" not "anonymous";
  only `user_id IS NOT NULL AND user_id = 0` would mean "explicitly anonymous",
  but since we never store 0, `NULL` covers both legacy and anonymous runs.

### Migration timing

**Deferred until the Go gateway becomes the sole writer to `them.runs`.**

Rationale: applying this migration while the Python platform is also writing would
require updating both writers atomically, which is operationally risky and requires
a Python deploy before or alongside the Go deploy. Migrating while two writers
exist adds coordination cost with no immediate user-visible benefit.

Trigger: when the Python bridge's `init_run_activity` is removed or bypassed.

### Rollout plan

1. Create migration file (e.g., `migrations/0010_runs_identity.sql`):
   ```sql
   BEGIN;
   ALTER TABLE them.runs
       ADD COLUMN IF NOT EXISTS user_id    BIGINT NULL,
       ADD COLUMN IF NOT EXISTS session_id TEXT   NULL,
       ADD COLUMN IF NOT EXISTS client_ip  INET   NULL,
       ADD COLUMN IF NOT EXISTS ep_type    TEXT   NULL;
   -- No NOT NULL constraints; existing rows keep NULL values.
   -- No FK constraint on user_id (access_tokens rows may be deleted after revocation).
   COMMIT;
   ```
2. Deploy Go gateway version that populates these columns in `runrecorder.CreateRun`.
3. Update `runrecorder.go` — add `user_id`, `session_id`, `client_ip`, `ep_type`
   to the `INSERT` statement; pass them from `ws/sse.Handler` via a `domain.RunMeta`
   field on `domain.Run`.
4. Update `GET /api/v1/runs` and `GET /api/v1/runs/{id}` to expose the new columns.
5. (Future) Update Python platform `init_run_activity` to also populate these fields
   when the Python writer is still active.

### Rollback plan

```sql
-- Rollback is non-destructive: drop the new nullable columns.
ALTER TABLE them.runs
    DROP COLUMN IF EXISTS user_id,
    DROP COLUMN IF EXISTS session_id,
    DROP COLUMN IF EXISTS client_ip,
    DROP COLUMN IF EXISTS ep_type;
```

No data is lost; the base columns remain intact. The Go binary can be rolled back to
a version that does not pass the new fields without any schema incompatibility.

### Related identity fields (not in `them.runs`)

| Field | Current location | Populated | Gap |
|---|---|---|---|
| `user_id` (TokenID) | Redis session Hash `them:sess:{id}` | Always (0 for anonymous) | Not persisted to DB |
| `session_id` | Redis session Hash key | Always | Not written to `them.runs` |
| `client_ip` | Not captured anywhere | Never | Both DB and Redis lack this |
| `ep_type` | `them.entry_points.ep_type` | At row creation | Not denormalised into runs |
| `token_hash` | Redis L2 `them:session:token:{hash}` | For authenticated sessions | Never written to any persistent store other than L2 TTL cache |

The Redis session data (`them:sess:{session_id}`) already includes `user_id`,
`orchestrator_name`, `ep_slug`, `context_id`, and `started_at`. These are
ephemeral (TTL 90s) and lost after session end. The DB migration is the only way
to make identity data durable for audit purposes.

---

## MIG-002 — Add CHECK constraint on `them.entry_points.ep_type` (future)

### Current gap

`them.entry_points.ep_type` is a free-form TEXT column. The application layer
validates the allowed set (`websocket`, `sse`, `voice`) but the DB has no CHECK
constraint, so direct SQL inserts or future code bugs can store invalid types.

### Proposed change

```sql
ALTER TABLE them.entry_points
    ADD CONSTRAINT entry_points_ep_type_check
    CHECK (ep_type IN ('websocket', 'sse', 'voice'));
```

### Migration timing

Safe to apply immediately once both the Go gateway and Python platform agree on
the allowed set. No Python deploy required — this is a constraint, not a schema
addition. **Deferred** until the `voice` EP type is confirmed stable and no other
EP types are under development.

### Rollback plan

```sql
ALTER TABLE them.entry_points
    DROP CONSTRAINT IF EXISTS entry_points_ep_type_check;
```

---

*Add new migration entries above as gaps are identified.*
