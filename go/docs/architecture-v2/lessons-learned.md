# Lessons Learned — Go Gateway

**Updated:** 2026-07-20

This document captures non-obvious behaviors, platform traps, and integration
surprises that burned us during development. Read it before making any judgment
call about "why does this work this way."

---

## L-01: gorilla/websocket raw TCP close causes Python websockets 16.x ConnectionClosedError before recv

**Context:** Go bridge uses `gorilla/websocket`. Python clients use `websockets 16.x`.

**What happened:** When the Go bridge closes a WebSocket with a raw TCP close (no
WebSocket close handshake), the Python `websockets` library raises
`ConnectionClosedError` on the next `recv()` call rather than returning the close frame
cleanly. In `websockets` 16.x this became a hard error instead of the 15.x soft EOF.

**Fix:** Always close with a proper WebSocket close frame:
```go
conn.WriteMessage(websocket.CloseMessage,
    websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
```
Do not rely on `conn.Close()` alone — that drops the TCP connection without the WS
handshake and breaks Python clients.

---

## L-02: Windows cp1252 terminal crashes on Unicode log output

**Context:** Running `docker compose logs -f` or piping Go log output on Windows with
the default CP1252 terminal encoding.

**What happened:** Structured JSON logs containing Unicode characters (especially em
dashes `—` in log messages) cause the terminal to crash or corrupt output with
`UnicodeDecodeError` equivalent on Windows.

**Fix:**
- Set `LOG_FORMAT=text` in the Windows soak environment so logs use ASCII-safe text format.
- Or run: `chcp 65001` before streaming logs to switch the terminal to UTF-8.
- All log message strings in Go code should avoid Unicode outside of user-supplied content.

---

## L-03: docker cp path mangling on Windows/Git Bash breaks psql -f

**Context:** `soak_setup_db.sh` originally used `docker cp file them-postgres:/tmp/foo.sql`
followed by `docker exec them-postgres psql -U them -d them -f /tmp/foo.sql`.

**What happened:** On Windows with Git Bash, the `/tmp/foo.sql` path in `docker cp` is
mangled by MSYS path translation into `C:/Program Files/Git/tmp/foo.sql` or similar,
causing `docker cp` to fail or copy to the wrong path.

**Fix:** Replace `docker cp + psql -f` with `docker exec -i container psql ... < file`.
Git Bash handles the `< file` stdin redirect natively without path mangling, and the
file content is piped directly into psql without any intermediate file on the container.

```bash
# Before (broken on Windows):
docker cp "${file}" them-postgres:/tmp/soak_migration.sql
docker exec them-postgres psql -U them -d them -f /tmp/soak_migration.sql

# After (works on Windows + Linux):
docker exec -i them-postgres psql -U them -d them -q < "${file}"
```

---

## L-04: Soak schema mismatch — seed SQL must match actual schema, not assumed schema

**Context:** The soak setup script seeds rows into `them.runs` and `them.orchestrators`.
These tables evolve across phases with new columns added via migration files.

**What happened:** After adding the `updated_at`, `application_id`, and `entry_point_slug`
columns in later migrations, the original soak seed SQL became inconsistent. It either
omitted required columns or referenced column names that no longer existed (e.g., the
schema was checked against phase-8 assumptions but the live DB was at phase-11).

**Fix:**
1. Always read `\d them.runs` before writing seed SQL to confirm the current schema.
2. The seed SQL in `soak_setup_db.sh` uses `ON CONFLICT DO NOTHING / DO UPDATE` for
   idempotency — this requires the column list in the INSERT to match exactly.
3. Add a pre-check in the setup script: if `them.runs` exists, skip migrations and
   go directly to seeding. This prevents running migrations twice on an already-migrated DB.

---

## L-05: Temporal healthcheck using `temporal workflow list` fails during cold-start

**Context:** `docker-compose.yml` temporal-frontend service had a healthcheck that ran
`temporal workflow list --address localhost:7233 --namespace default`.

**What happened:** During container startup, Temporal may be listening on port 7233 but
not yet ready to serve namespace queries. The CLI returns a gRPC error, Docker marks
the service unhealthy, and dependent services (Go bridges) fail to start.

**Fix:** Use a simple TCP connectivity check instead:
```yaml
healthcheck:
  test: ["CMD-SHELL", "nc -z localhost 7233 || exit 1"]
```
This confirms the frontend is accepting connections without requiring the namespace
to be fully initialized. The Go bridges have their own retry logic on Temporal client
connect; they do not require Docker-level healthcheck to pass first.

---

## L-06: Reconciler DryRun must default to true — any failure mode must not enable writes

**Context:** `RECONCILER_DRY_RUN` controls whether the reconciler writes to the DB.

**Design principle:** The config loader uses `getEnvBoolSafe` — a dedicated helper
where `safeDefault=true`. Any unset, empty, or invalid value returns `true`, never `false`.
This means a misconfigured deployment cannot accidentally enable writes.

**Implication:** To enable writes you must explicitly and correctly set
`RECONCILER_DRY_RUN=false`. A typo like `RECONCILER_DRY_RUN=False` or
`RECONCILER_DRY_RUN=0` is intentionally rejected and falls back to true.

Note: `strconv.ParseBool` accepts `"1"`, `"t"`, `"T"`, `"TRUE"`, `"true"`, `"True"`,
`"0"`, `"f"`, `"F"`, `"FALSE"`, `"false"`, `"False"` — lowercase and uppercase both
work for valid values. Only truly invalid strings (e.g., `"yes"`, `"no"`) fall back.

---

## L-07: Token cache invalidation must use the hash, not the raw plaintext

**Context:** Admin token endpoints (PATCH, DELETE) need to evict the token from the
L1+L2 cache after mutation, but the admin path never holds the raw plaintext (it is
shown once at creation and never stored).

**What happened:** `auth.Cache.Revoke(ctx, rawToken)` hashes the token internally, so
it cannot be called from the admin service which only has `token_hash`.

**Fix:** `TokenService.invalidate(ctx, hash)` bypasses `Revoke` and calls the
underlying cache primitives directly:
- `cache.Del(ctx, "them:session:token:"+hash)` — evicts L2
- `cache.Publish(ctx, "them:token:revoked", hash)` — triggers cross-pod L1 eviction

This is byte-identical to `auth_service/routers/tokens.py:invalidate_token(token_hash)`.
The key insight: the `them:session:token:{hash}` Redis key uses the hash, not the raw
token, so the admin path can invalidate without ever seeing the plaintext.

---

## L-08: Python JSON booleans vs Go JSON booleans across bridges

**Context:** Python's `json.dumps` serializes Python `True`/`False` as JSON
`true`/`false`. Go's `encoding/json` does the same. However, older Python test scripts
that compared raw string output (e.g., `check "enabled" "True"` in bash) fail when
pointing at the Go bridge because Go outputs `true` (lowercase JSON) while Python's
response body uses `true` too — but bash-comparison test scripts compare the raw string
value returned by `python3 -c "print(d.get('enabled'))"` which prints `True` (Python
bool repr, not JSON).

**Fix:** The Python↔Go contract tests in `test_go_wave5_contracts.py` parse JSON
responses and compare Python objects, never raw strings. This avoids the
True/true/1 comparison trap. When writing any new cross-bridge test, always use
`json.loads()` and compare Python types.

---

## L-09: pgx.ErrNoRows must be used in tests that check IsNoRows sentinel

**Context:** `dal.IsNoRows(err)` calls `errors.Is(err, pgx.ErrNoRows)`.

**What happened:** A test using `fakeDB{execRetErr: errors.New("no rows")}` triggered
`ExecReturning → Scan → return err` but the returned error was a plain `errors.New`,
not `pgx.ErrNoRows`. The service's `IsNoRows` check returned false, the 500 path was
taken instead of the 404 path, and the test failed.

**Fix:** Always use `pgx.ErrNoRows` (import `"github.com/jackc/pgx/v5"`) when writing
test doubles that simulate "no rows" conditions for the admin DAL. A generic error
string is not equivalent — pgx uses `errors.Is` identity, not string matching.
