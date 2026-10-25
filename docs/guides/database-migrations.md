---
title: Database Migrations (how they work today and how to add one)
type: guide
status: current
last_verified: 2026-10-25   # files read, live catalog queried with scripts/check_migrations.py; no migration applied
---

# Database Migrations

## How it works today
- Migrations are plain SQL files `db/NNN_name.sql` (122 files, `001`…`125`), applied **by hand** with `psql -f`. There is **no migration runner**, no CI step, and no automatic fresh-install path.
- `them.schema_migrations` (columns `version`, `description`, `applied_at`) is advisory: only some files insert themselves into it (22 of 122; 52 rows, last 2026-09-24), so it cannot tell you what is applied.
- Other files in `db/`: `002_seed.sql` / `003_users_seed.sql` (seed data), `validate_r4a.sql` (one-off RLS validation), `110_platform_as_bootstrap_tenant_DOWN.sql` (the only rollback script), `clickhouse/` (events store, see `docs/architecture/events-pipeline.md`), `README.md` (stale, Python-era).
- The live database is the source of truth, not the files: `db/001_schema.sql` lacks many tables, and a few migrations describe objects the live DB does not have (migration 026's `app_orchestrators.tenant_id`, 055's managed-app tables). Replaying all files will **not** rebuild the live schema (backlog F-35/F-36/F-105).
- Current schema as it really is: `docs/reference/schema-generated.md` (regenerate: `python3.12 scripts/gen_reference.py schema`).

## What is pending right now
Run the read-only drift check (needs the stack):
```bash
python3.12 scripts/check_migrations.py
```
It lists migrations whose `CREATE TABLE` / `ADD COLUMN` objects are missing. On 2026-10-25 the only genuinely pending one is **`125_gateway_user_id.sql`** (`gateway_requests.user_id`; the gateway code already writes it — backlog F-42). The other hits are objects dropped by later migrations (015, 017, 123) or drifted from the files (026, 055).

## Add a migration (checklist)
1. **Number:** next free number (currently `126`). Never reuse a number — five numbers are already duplicated (003, 010, 025, 032, 110) and `060`–`069` and `093` are unused.
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
7. **Verify:** `python3.12 scripts/check_migrations.py`, `python3.12 scripts/gen_reference.py schema` (commit the regenerated file), `cd go && go test -tags=integration ./internal/db/...` against a **test** database (it creates data), then `go test ./internal/admin/...`.
8. **Production:** apply before deploying code that needs it (`docs/guides/deploy-and-env.md`).

## Rollback
Only migration 110 has a DOWN script. Otherwise restore the pre-migration dump. Write a `NNN_*_DOWN.sql` next to destructive migrations.

## Known gaps
See backlog F-35, F-36, F-105: no runner or tracking, stale `001_schema.sql`/README, duplicate and missing numbers, files drifted from the live schema, one pending migration (125).

## Related
`docs/architecture/multi-tenancy-rls.md`, `docs/reference/schema-generated.md`, `docs/lessons/database-schema.md`, `docs/guides/deploy-and-env.md`
