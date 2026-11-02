---
title: Database Migrations (how they work today and how to add one)
type: guide
status: current
last_verified: 2026-11-02   # drift check run live; guards unit/integration tested
---

# Database Migrations

## How it works today
- Migrations are plain SQL files `db/NNN_name.sql` (`001`…`129`), applied **by hand** with `psql -f`. There is **no migration runner**, no CI step, and no automatic fresh-install path.
- `them.schema_migrations` (columns `version`, `description`, `applied_at`) is advisory: only some files insert themselves into it (22 of 122; 52 rows, last 2026-09-24), so it cannot tell you what is applied.
- Other files in `db/`: `002_seed.sql` / `003_users_seed.sql` (seed data), `validate_r4a.sql` (one-off RLS validation), `110_platform_as_bootstrap_tenant_DOWN.sql` (the only rollback script), `clickhouse/` (events store, see `docs/architecture/events-pipeline.md`), `README.md` (stale, Python-era).
- The live database is the source of truth, not the files: `db/001_schema.sql` lacks many tables, and a few migrations describe objects the live DB does not have (migration 026's `app_orchestrators.tenant_id`, 055's managed-app tables). Replaying all files will **not** rebuild the live schema (backlog F-35/F-36/F-105).
- Current schema as it really is: `docs/reference/schema-generated.md` (regenerate: `python3.12 scripts/gen_reference.py schema`).

## Detecting a missing migration (backlog F-135)
Migration `125` was committed with the code that needs it and never applied, so gateway usage writes failed for days with only a warning. Three guards now exist:
1. **Drift check** — `python3.12 scripts/check_migrations.py` (needs the stack) lists migrations whose `CREATE TABLE` / `ADD COLUMN` objects are missing from the live database. Objects dropped by a later migration are not reported, and `db/known_drift.txt` documents the few that are deliberately absent, so **any `MISSING` line is real**. Exit code 1 on drift; `--verbose` shows what was explained away. `scripts/deploy.sh up` runs it first.
2. **Startup check** — the bridge, auth service, dag workers and orchestrator worker call `dbversion.Enforce` (`go/internal/dbversion`): when `dbversion.Expected` (the newest migration the code needs) is not in `them.schema_migrations` they log an ERROR naming the file; with `THE_M_REQUIRE_MIGRATIONS=true` they refuse to start.
3. **Lint** — every migration numbered 127 or higher must end by inserting itself into `them.schema_migrations`; the pre-commit hook checks newly added files and CI (`migrations-lint`) checks all of them (`scripts/check_migrations.py --lint`).
`scripts/tests/test_46_check_migrations_unit.py` unit-tests the checker (no stack). Status 2026-11-02: the drift check reports no unexplained drift.

## Add a migration (checklist)
1. **Number:** next free number (currently `130`). Never reuse a number — five numbers are already duplicated (003, 010, 025, 032, 110) and `060`–`069` and `093` are unused.
2. **Write it idempotent and transactional:** `BEGIN; … COMMIT;`, `IF NOT EXISTS` / `ON CONFLICT DO NOTHING`, and end with `INSERT INTO them.schema_migrations (version, description, applied_at) VALUES ('NNN_name', '…', NOW()) ON CONFLICT (version) DO NOTHING;`.
3. **New tenant-scoped table** (see `docs/architecture/multi-tenancy-rls.md`): `tenant_id` (or a parent FK), `ENABLE` + `FORCE ROW LEVEL SECURITY`, a `them_app` policy using `NULLIF(current_setting('app.tenant_id', true), '')::uuid`, `GRANT`s to **both** `them_app` and `them_admin` (BYPASSRLS does not replace GRANT), and an explicit `GRANT USAGE, SELECT ON SEQUENCE` for any serial column. Owner should be `them_owner`.
4. **Per-application config table:** also register it in `appScopedConfigTables` (`go/internal/admin/dal/app_deploy.go`) so export/import and deploy carry it (`docs/architecture/app-export-import-deploy.md`).
5. **Redis or code changes that depend on it:** update the owning architecture doc in the same commit (the pre-commit hook enforces this).
6. **Apply to a scratch copy first** (e.g. `createdb`/restore a dump into a throwaway database), then to the real one:
   ```bash
   docker cp db/NNN_name.sql them-postgres:/tmp/m.sql
   docker exec them-postgres psql -U them -d them -v ON_ERROR_STOP=1 -f /tmp/m.sql
   ```
   Take a dump first for anything that alters or drops (`docker exec them-postgres pg_dump -U them them > backup.sql`).
7. **Record + verify:** end the file with `INSERT INTO them.schema_migrations …` (required, lint-enforced), bump `Expected` in `go/internal/dbversion/dbversion.go` (a test fails if it lags the newest file), then `python3.12 scripts/check_migrations.py`, `python3.12 scripts/gen_reference.py schema` (commit the regenerated file), `cd go && go test -tags=integration ./internal/db/...` against a **test** database (it creates data), then `go test ./internal/admin/...`.
8. **Production:** apply before deploying code that needs it (`docs/guides/deploy-and-env.md`).

## Rollback
Only migration 110 has a DOWN script. Otherwise restore the pre-migration dump. Write a `NNN_*_DOWN.sql` next to destructive migrations.

## Known gaps
See backlog F-35, F-36, F-105: no runner or tracking, stale `001_schema.sql`/README, duplicate and missing numbers, files drifted from the live schema, one pending migration (125).

## Related
`docs/architecture/multi-tenancy-rls.md`, `docs/reference/schema-generated.md`, `docs/lessons/database-schema.md`, `docs/guides/deploy-and-env.md`
