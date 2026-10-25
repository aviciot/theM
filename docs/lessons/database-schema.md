---
title: Lessons — Database & Schema
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-07-05 — agents FK to tasks must be ON DELETE SET NULL, not RESTRICT

**Symptom:** Deleting an agent via the UI returned HTTP 500 with `ForeignKeyViolationError: update or delete on table "agents" violates foreign key constraint "tasks_agent_id_fkey"`.
**Root cause:** `them.tasks.agent_id` and `them.run_steps.agent_id` were defined as plain `REFERENCES them.agents(id)` — which defaults to `ON DELETE RESTRICT`. Any agent that was ever invoked (has child task or run_step rows) cannot be deleted.
**Fix:** Both FKs changed to `ON DELETE SET NULL`. Task and run_step history is preserved; `agent_id` becomes NULL to indicate the agent no longer exists. Applied live via `ALTER TABLE` and updated `db/001_schema.sql`.
**Watch for:** Any new FK from a history/audit table to an admin-managed entity should default to `ON DELETE SET NULL` or `ON DELETE CASCADE`, not `RESTRICT`. History must survive entity deletion.

---

---

## ON CONFLICT Target Must Match an Actual Unique Constraint (2026-09-06)

`UpsertEntryPoint` in `dal/publish.go` used `ON CONFLICT (tenant_id, slug)` but the DB only has a unique constraint on `(application_id, slug)` (`uq_entry_points_app_slug`). Postgres rejects any ON CONFLICT clause that doesn't reference an existing unique or exclusion constraint — the error is a generic runtime DB error, not a duplicate key violation.

This caused every `Publish Definition` call to return HTTP 500 silently (the error was not logged before the fix).

**Fix:** Changed to `ON CONFLICT (application_id, slug)` to match the actual constraint. The semantic is also correct: slugs are unique per-application, not per-tenant-globally.

**Watch for:** When writing ON CONFLICT clauses, always verify the column set matches an existing unique index in the DB schema (`\d table_name` in psql). Unit tests with fake DALs won't catch this — only integration tests hitting real Postgres will.

---

## `them.run_steps.tool_call_id`: NOT NULL column never populated by its writer (found 2026-09-22)

`db/001_schema.sql` declared `tool_call_id TEXT NOT NULL` with no default, but
`internal/runrecorder.RecordAgentStep` — the only writer — never included it in its INSERT column
list. This means every orchestrator-mode `run_steps` insert should have violated the NOT NULL
constraint against a real Postgres instance, for as long as that mismatch existed.

**Why the test suite never caught it:** `TestRecordAgentStep_insertsCorrectly`
(`internal/runrecorder/recorder_test.go`) uses a `mockDB` that only records the SQL string and args
passed to `Exec` — it never executes against real Postgres, so a NOT NULL violation can never
surface there. Found only because App Canvas Debug Mode Phase 3 needed to extend this same table
and a wider audit of it (schema + every reader/writer) was done first.

**Fix (migration `db/103_run_steps_appflow_trace.sql`):** dropped the column outright rather than
adding a default. Confirmed first that no reader needed it for anything beyond a
`COALESCE(tool_call_id, '')` display field — nothing correlates on it.

**Watch for:** any column added as `NOT NULL` to a table whose only writer is covered by a
mock-only unit test. A schema-level constraint violation is invisible to that kind of test by
construction — it needs either an integration test against real Postgres, or a manual check of the
live DB, to ever surface. Consider this a standing reason to spot-check "does every NOT NULL column
in a hot-path table actually get written" before trusting a table's shape from migrations alone —
this repo already has a documented precedent (docs/CURRENT.md's `them-agent-runtime` crash-loop
entry, 2026-09-22) of exactly this class of drift going unnoticed for a long time.

---

## New SERIAL/BIGSERIAL table: table GRANT is not enough, the sequence needs its own GRANT (found 2026-09-23)

`db/105_llm_provider_keys.sql` created `them.llm_provider_keys` (BIGSERIAL PK) and granted
`them_admin`/`them_app` access on the table itself — but every `INSERT` still failed with
`permission denied for sequence llm_provider_keys_id_seq (SQLSTATE 42501)`.

**Why:** `db/070_rls_roles.sql` has `GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO
them_admin/them_app` — but that statement only affects sequences that existed in the schema *at
the moment it ran*. It is not a standing default privilege applied to sequences created later.
Every migration since 070 that adds a `SERIAL`/`BIGSERIAL` column has needed (and — audit this if
touching an older one — may be missing) its own explicit
`GRANT USAGE, SELECT ON SEQUENCE them.<table>_id_seq TO <role>;` alongside the table grant.

**Why it wasn't caught by inspection:** `\dp them.llm_provider_keys` showed the table grants as
correct. Sequences carry a completely separate ACL (`\dp them.llm_provider_keys_id_seq`) that
nothing checks by default — the failure only appears at actual INSERT time, which is exactly why
the integration test (real Postgres, not a mock) caught it and a table-grant read-through would
not have.

**Fix:** added the explicit sequence grant to the migration and re-ran it against the live DB.

**Watch for:** any new migration that creates a table with a `SERIAL`/`BIGSERIAL` PK. Grant the
sequence in the same migration as the table, and verify with a real INSERT via an integration test
— not just `\dp` on the table — before considering the migration done.
