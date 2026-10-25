---
title: Lessons — Testing & CI
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## GitHub Actions CI silently rotted after the Python→Go migration (found 2026-09-23)

`.github/workflows/ci.yml` was written for the old Python-bridge stack and never updated when
commits `9f1c5b33` ("the-M is Go-only") and `4a6241b5` ("remove all remaining Python bridge
references") renamed/removed the containers it depended on. The "live stack" job referenced
`docker-compose.local.yml` (never existed), and tried to start/health-check `them-auth-service`
and `them-bridge` (renamed to `them-auth-go` and `them-go-bridge` respectively). Every CI run on
`main` and every PR had been failing at the "Build and start stack" step ever since, unnoticed.

**Why it wasn't caught earlier:** compose/container renames were made as part of larger feature
commits, and nothing in the workflow (or a pre-commit/PR check) cross-referenced service names in
`ci.yml` against `docker-compose.yml`. CI failure notifications were not being actively monitored.

**Fix:** replaced the workflow with a single `go-test` job (`go vet ./...` + `go test ./...`),
matching CLAUDE.md's mandatory "every `go/` change needs tests, run full suite before commit" rule.
Dropped the live-Docker-stack job and the now-unused `.github/docker-compose.ci.yml` override
entirely rather than trying to repair container names — the platform is Go-only now, and a
live-stack E2E job can be reintroduced deliberately later with the current service list if needed.

**Watch for:** any time a container/service is renamed or removed in `docker-compose.yml`, grep
`.github/workflows/*.yml` for the old name before considering the rename complete — CI can silently
reference stale container names for a long time since a failing "live stack" job doesn't block
anything locally and is easy to miss if notifications aren't watched.

---

## `t.Cleanup`'s LIFO order silently leaked test fixture rows on every single run (found 2026-09-28)

**Found while:** writing integration tests for Phase 1 of docs/plans/app-canvas-config-completeness-plan.md
(`app_deploy_scoped_config_integration_test.go`) — a fixed-slug agent seed collided with
`component_definitions_tenant_unique` on the SECOND run, even though the first run reported
`--- PASS`. Confirmed live: the passing run's own `t.Cleanup` for the seeded agent silently never
deleted it — the row (and its `component_definitions` row) survived every single run, pass or
fail, until this was found and fixed.

**Root cause:** `t.Cleanup` runs in LIFO order — the LAST registered cleanup runs FIRST. This
test registered cleanups in this order: (1) `setupAppScopedConfigApp`'s app-delete (registered
first, for the *source* app), (2) a second app-delete for the target app, (3) an agent-delete
cleanup registered inside a `seedAgent` helper called after both. LIFO execution ran them in
reverse: agent-delete FIRST, then the two app-deletes LAST. But `middleware_wirings`/
`app_agent_bindings` rows seeded against the source app still referenced the agent at that point
— the agent's own app hadn't been deleted yet, so its cascade hadn't fired. The `DELETE FROM
them.agents` inside the agent cleanup hit an FK violation and failed — silently, because the
cleanup closure discarded the error with `//nolint:errcheck` (the same discard pattern this
codebase's own integration-test helpers already use everywhere, e.g. `pool.Exec(...) //nolint:errcheck`
in `t.Cleanup`, which is normally fine for an idempotent `DELETE ... WHERE id = $1` that's
expected to sometimes hit zero rows — but is NOT fine when the delete can fail due to a live FK
reference from data the test itself just created).

**Fix:** two changes. (1) The agent's seed `slug` now carries a random suffix
(`substr(md5(random()::text),1,8)`, not just the fixed test name) so even a leftover row from a
future ordering mistake can't collide with a fresh run's insert — defense in depth, not the real
fix. (2) The actual fix: stopped registering the agent-delete cleanup inside `seedAgent` at all;
instead, one single `t.Cleanup` registered immediately after the deploy call explicitly deletes,
in the correct dependency order within one closure: the deployed (target) app first, then the
source app's own referencing `middleware_wirings`/`app_agent_bindings` rows, then the agents,
then their `component_definitions` rows — no longer relying on registration-order LIFO to get
this right across multiple independent `t.Cleanup` calls.

**Watch for:** whenever a test seeds more than one row with a foreign-key relationship and
registers `t.Cleanup` for each independently, LIFO ordering across *separate* `t.Cleanup` calls
is easy to get backwards — especially when the referencing rows are seeded by a DAL call being
tested (like `DeployApplication` here) rather than by the test's own explicit inserts, since it's
not visually obvious at the call site that a later `t.Cleanup` needs to run before an earlier
one. Prefer ONE `t.Cleanup` per logical "clean up everything I just created" step, with deletes
ordered explicitly inside it, over multiple independent per-resource `t.Cleanup` calls whenever
there's a real FK relationship between the seeded rows. And never assume a passing test means its
cleanup actually ran — check for leftover rows directly (`SELECT count(*) FROM ... WHERE
slug LIKE 'test-prefix%'` after a run) when a test seeds fixture data with FK relationships,
the same way this was actually caught: not by a test failure, but by running the *same* test
twice in a row and noticing the second run collided.

---

---

## `t.Cleanup` leaked an agent created BY the code under test, not just one seeded BY the test (found 2026-09-28)

**Found while:** the same Phase 2 round-trip test above — `TestDAL_ExportImportApplication_
RoundTrip` passed 3 consecutive times with zero test-reported errors, but a leftover-row check
after each run found new `agents`/`component_definitions` rows accumulating every single time.

**Root cause:** a variant of the same class of bug as the "`t.Cleanup`'s LIFO order" entry
above, but with a twist — this time the leaking row wasn't seeded by the TEST at all. It was
created BY `ImportApplication` itself: since the target tenant has no agent matching the
exported one's identity, import inserts a brand-new `agents` + `component_definitions` row pair
in the target tenant. The test only tracked `agentIDsToClean` for the ORIGINAL, test-seeded
source-side agent — the code-under-test's own newly-created row had no id captured anywhere,
so nothing ever cleaned it up.

**Fix:** after calling `ImportApplication`, query `SELECT id FROM them.agents WHERE tenant_id =
$1` against the target tenant and register cleanup for whatever comes back, in addition to the
originally-seeded agent ids — rather than trying to thread the new agent's id back out of
`ImportApplication`'s return value (which deliberately doesn't expose it, matching
`DeployApplication`'s own return shape).

**Watch for:** when a test's job is specifically to prove that the code under test CREATES new
rows (not just moves/copies existing ones), the cleanup can't rely on a list of ids the test
itself already knows about before calling the function — it has to look at what the function
actually left behind afterward. This is a strictly harder case than the earlier LIFO-ordering
entry, which was about ordering existing cleanups correctly; this one is about a cleanup target
that doesn't exist yet at the time cleanups are normally registered.

---
