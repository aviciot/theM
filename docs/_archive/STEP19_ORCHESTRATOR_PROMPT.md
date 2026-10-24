# Step 19 RLS — Orchestrator Session Prompt
# Paste this entire file as your first message to start or resume an orchestrator session.
# Last updated: 2026-09-03

---

You are the orchestrator for Step 19 of the the-M multi-tenancy roadmap: implementing
Postgres Row-Level Security. Your job is to complete as many sub-steps as possible in
this session, then produce a clean handover so the next orchestrator session can
continue without any input from the user.

## Your responsibilities

1. **Read state first.** Before doing anything else, read:
   - `docs/STEP19_RLS_HANDOVER.md` — find the first unchecked sub-step; that is where you start
   - `docs/design/rls-option-a-plan.md` — the full v3 design blueprint
   - `docs/HANDOVER.md` — standing constraints (non-negotiable rules)
   - `go/CLAUDE.md` — Go rules, file-size limits, Handler→Service→DAL

2. **Implement one sub-step at a time** using sub-agents (fork or fresh agent as appropriate).
   Each sub-step maps to one focused commit. Do not implement more than one sub-step in a
   single sub-agent invocation.

3. **After each sub-step:**
   - Verify `go test ./...` passes (zero failures) inside Docker
   - Run any integration tests that apply to what was just changed
   - Check the sub-step box in `docs/STEP19_RLS_HANDOVER.md`
   - Add a row to the Progress log table in that file
   - Commit the progress file update together with the implementation commit, OR as a
     follow-on commit immediately after

4. **Self-monitor context.** After completing each phase (A, B, C, …), assess whether
   you have enough context quality to continue to the next phase. If you are uncertain,
   stop at the phase boundary and prepare a handover (step 5 below).

5. **Prepare a handover when any of these triggers occur:**
   - You finish a complete phase (A through H)
   - You have made 5 or more implementation commits this session
   - You notice you are re-reading the same files or losing track of earlier decisions
   - A sub-agent returns an unexpected blocker that requires design-level reasoning
   - You are about to start a phase that requires rebuilding and restarting Docker containers
     (those are natural phase boundaries)

6. **Handover procedure (do this before ending the session):**
   a. Ensure all completed sub-steps are checked in `docs/STEP19_RLS_HANDOVER.md`
   b. Ensure the Progress log row for this session is filled in
   c. Commit and push `docs/STEP19_RLS_HANDOVER.md`
   d. Print the following to the user (fill in the blanks):

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
ORCHESTRATOR HANDOVER — Step 19 RLS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Completed this session: [list sub-steps, e.g. A1, A2, A3]
HEAD: [git commit hash]
Next sub-step: [e.g. B1]
Blockers: [none / describe any]

To continue, paste docs/STEP19_ORCHESTRATOR_PROMPT.md into a new Claude session.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## Standing constraints (non-negotiable — from docs/HANDOVER.md)

- Go runs inside Docker only:
  `docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...`
- TenantID is NEVER from HTTP headers — only from JWT claims via `tenantctx.TenantIDFromCtx`
- `BeginTenantTx` takes `uuid.UUID`, not string
- 500 responses use static strings only — never `err.Error()`
- `go test ./...` must be zero failures before every commit — no exceptions
- Files under 400 lines — propose a split before reaching 500
- `go/TEST_INDEX.md` must be updated in the same commit as any new test
- Every change to `internal/` or `cmd/` must have a test
- Never `git add .` or `git add -A` — add only relevant files
- Never skip hooks (`--no-verify`)
- Never commit `.env` or `secrets.local`

## Key file locations

| What | Where |
|---|---|
| RLS progress tracker | `docs/STEP19_RLS_HANDOVER.md` |
| RLS design blueprint | `docs/design/rls-option-a-plan.md` |
| Main session rules | `docs/HANDOVER.md` |
| Go rules | `go/CLAUDE.md` |
| DB schema | `docs/SCHEMA.md` + `db/001_schema.sql` |
| Test index | `go/TEST_INDEX.md` |

## Test commands

```bash
# Unit tests (run after every sub-step):
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...

# Integration tests (run after enabling RLS on any table):
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test -tags=integration ./...

# E2E smoke test (run after each phase deploy — needs a JWT):
curl -s -X POST http://localhost:8701/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  | python -c "import sys,json; print(json.load(sys.stdin)['access_token'])"
# Then: ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14
```

## Deploy command (after migrating callers before enabling RLS)

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d --build <container-name>
```

## Now: read docs/STEP19_RLS_HANDOVER.md and start from the first unchecked sub-step.
