---
title: Lessons — Guards, Middleware & File Scanning
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-09-27 — A shared pipeline's "clean vs flagged" status was silently wrong since day one

**Symptom:** building a PII Guard demo app with `mode="warn"`, the guard genuinely detected an
email (confirmed directly: `pii.Detector.Process` returns `Outcome:"flagged"` for this exact
input+config, verified both in an isolated unit test AND via a live debug trace print inside the
running container) — but the run showed completely normal output, no trace mention of PII at all,
indistinguishable from the guard doing nothing. Spent a long stretch chasing this as a stale-binary
/ deployment problem (rebuilt all 3 dag-worker images multiple times, including `--no-cache`, before
finding the real cause) — that instinct was reasonable given this session's earlier real staleness
bugs, but this time the deployed code genuinely was current.
**Root cause:** `internal/middleware/pipeline.go`'s `Pipeline.Run` derives its final, non-blocking
status with `finalStatus := "clean"; if hadError { finalStatus = "error" }` — it never checked
whether any processor's `Result.Outcome` was `"flagged"`. `PipelineResult`'s own doc comment always
listed `"flagged"` as a real possible value, but the code to actually produce it (for the
non-blocking case) was simply never written. `mode="block"` never hit this bug (blocking has its
own separate, correct early-return path using `r.Outcome` directly); `mode="redact"`'s own text
substitution wasn't broken either, since `Modified` is applied to `current` unconditionally, before
the buggy status line — only the status *label* was silently wrong, for every non-blocking flagged
result, since the feature shipped.
**Fix:** added a `hadFlag` bool alongside the existing `hadError`, set when any processor's
`Outcome == "flagged"`; `finalStatus` becomes `"flagged"` when set (still overridden by `"error"`,
same precedence `hadError` already had). One existing test
(`TestPipeline_ModifiedPartPassedToNext`) had encoded the bug as if it were intended behavior —
its own assertion literally said `"expected clean (flagged but not blocking)"` — corrected instead
of left alone.
**Watch for:** when a shared, reused engine (`Pipeline.Run` serves both `av_scan`'s
clean/infected/error model and pii_redact/prompt_inject's clean/flagged/error model) has a status
enum wider than what the original author's own test cases exercised, grep for literal outcome
strings the code CAN produce (`grep -rn 'Outcome:' internal/middleware/*/`) and confirm each one has
at least one test walking it all the way through to the aggregate result — not just the individual
processor's own return value. A processor returning the right `Result` proves nothing about whether
the thing that aggregates results across processors actually preserves it. And: before assuming
"my rebuild didn't take effect" a second or third time in the same investigation, prove the binary
is stale with something more direct than `strings | grep <symbol-name>` — Go's compiler strips
unused function names/doc comments from a normal (non-debug) build, so a missing symbol name in
`strings` output proves nothing; a literal string LITERAL actually referenced by a live code path
(an error message, a log field) is the only reliable signal, and even then, cross-check the
container's actual running image ID (`docker inspect <container> --format '{{.Image}}'`) against
the freshest build's image ID before trusting either signal.

---

---

## 2026-09-27 — Two silent data-corruption bugs found live while adding llm-node guard wirings

**Symptom 1:** `ON CONFLICT ON CONSTRAINT uq_mw_wiring_app_node DO UPDATE` failed
live with `ERROR: constraint "uq_mw_wiring_app_node" does not exist
(SQLSTATE 42704)`, even though `\d them.middleware_wirings` clearly lists
`uq_mw_wiring_app_node` under Indexes.
**Root cause:** it's a **partial unique index**
(`CREATE UNIQUE INDEX ... WHERE node_id IS NOT NULL AND node_id <> ''`),
not a named table constraint. Postgres's `ON CONFLICT ON CONSTRAINT` only
accepts real constraints (`pg_constraint` rows) — a unique index alone,
partial or not, is not one, even though `\d` lists both under the same
"Indexes" heading and it's easy to assume they're interchangeable.
**Fix:** use the inference-target form instead:
`ON CONFLICT (application_id, node_id) WHERE node_id IS NOT NULL AND node_id <> ''`
— matching the index's own predicate exactly.
**Watch for:** before writing `ON CONFLICT ON CONSTRAINT <name>`, check
`SELECT conname FROM pg_constraint WHERE conrelid = '<table>'::regclass`
first — if the name isn't in that list, it's an index, not a constraint,
and needs the column-list + WHERE-clause form instead. `\d` doesn't
distinguish the two visually.

**Symptom 2:** every "Save Guard Config" click (a config-only update, no
`node_id` in the request body) was silently setting the wiring's `node_id`
back to `NULL` — found live when a wiring correctly created with
`node_id="llm_1"` came back with `node_id=""` after nothing but a config
save.
**Root cause:** `UpdateMiddlewareWiring`'s SQL had
`node_id = CASE WHEN $6 = '' THEN NULL ELSE $6 END` — meaning "the caller's
NodeID param is empty" was treated as "the caller wants to clear it,"
instead of "the caller didn't send one." Since the real frontend call site
(`AgentGuardsSection.tsx`'s `handleSaveConfig`) never sends `node_id` at
all on a config-only save, this fired on nearly every save, and had
presumably been doing so since `node_id` was first added — a
previously-unexplained "wiring lost its scoping" symptom from an earlier
session was very likely this exact bug.
**Fix:** changed the CASE to `CASE WHEN $6 = '' THEN node_id ELSE $6 END`
— an empty incoming value now preserves the column's current value instead
of nulling it. Added a regression test
(`TestDAL_UpdateMiddlewareWiring_ConfigOnlySave_PreservesNodeID`) that
fails against the old SQL and passes against the fix.
**Watch for:** any `UPDATE ... SET col = CASE WHEN $n = '' THEN NULL ELSE $n
END` pattern is making an assumption — "empty means clear it" — that is
usually wrong for a partial-update endpoint where a field can legitimately
be omitted from the request. The safe default for an optional field on an
UPDATE is "preserve the existing value when not provided," not "clear it."
This exact bug shape can recur anywhere else in this codebase that uses the
same CASE-to-NULL idiom on an UPDATE statement — worth grepping for before
trusting another one uncritically.

---

---

## 2026-09-02 — them-go-worker panic: nil pointer in FileGate.quarantineAndEnqueue

**Symptom:** Run hangs, never completes. Temporal logs show "activity Heartbeat timeout". Worker logs show `panic: runtime error: invalid memory address or nil pointer dereference` at `gate.go:171` inside `quarantineAndEnqueue`. Worker restarts, run stays stuck in `running`.
**Root cause:** `FileGate.store` is nil when `THE_M_S3_ENDPOINT` is not set (no MinIO configured). `InterceptInline` checked `cfg.Enabled` and processors but called `g.store.PutQuarantine(...)` without checking `g.store != nil` first.
**Fix:** Added nil guard at the top of `quarantineAndEnqueue` — returns `{ScanStatus: "disabled"}` (fail-open) when store is nil. Added regression test `TestFileGate_NilStore_DoesNotPanic`.
**Watch for:** Any new path through FileGate that calls storage methods must guard `g.store != nil`. The same nil-store scenario can occur if `storage.New()` fails at startup — it logs a warning but sets the store to nil and continues.

---

## 2026-09-02 — middleware_jobs.artifact_id FK violation silently killed every quarantine job

**Symptom:** Security scanner Services tab always showed zeros. All scanned files had `scan_status='disabled'` even with MinIO running and security enabled. `quarantine_artifacts` rows were written but no `middleware_jobs` rows appeared.

**Root cause:** `db/050_middleware_pipeline.sql` defined `middleware_jobs.artifact_id` as `NOT NULL REFERENCES run_artifacts(id)`. `EnqueueWithQuarantine` passed the quarantine UUID as `artifact_id`. That UUID exists only in `quarantine_artifacts` — not yet in `run_artifacts` (promotion happens only after a clean scan). The FK violation caused every INSERT to fail at the DB level.

**Fail-open disguise:** `gate.go` catches the `EnqueueWithQuarantine` error and returns `GateResult{ArtifactID: qID, ScanStatus: "pending", Enqueued: false}`. The non-empty ArtifactID made the orchestrator treat the file as handled, so files were stuck in quarantine forever with no visible error.

**Fix (commit fd10362):**
1. `db/052_middleware_jobs_nullable_artifact.sql` — drops NOT NULL; drops+re-adds FK allowing NULL.
2. `EnqueueWithQuarantine` — no longer inserts `artifact_id` (leaves NULL at enqueue time).
3. `Claim` query — `COALESCE(artifact_id, quarantine_id)` so the worker gets a usable UUID.
4. `markJobDone` — sets `artifact_id` after promotion creates the `run_artifacts` row.

**Watch for:** Never insert `artifact_id` at enqueue time in the quarantine path. Quarantine flow order: MinIO write → `quarantine_artifacts` row → `middleware_jobs` (artifact_id=NULL) → worker scans → promotes to `run_artifacts` → sets `artifact_id`.

---

---

## A canvas node could only ever have ONE guard wired to it — a silent limitation nobody noticed (found 2026-09-28/29)

**Found while:** building a test app for the Guard Output Ports feature — importing an app that
wired both PII Guard and Prompt-Injection Guard onto the same `llm_1` node failed live with
`duplicate key value violates unique constraint "uq_mw_wiring_app_node"`.

**Root cause:** `db/018_graph_compiler.sql` (2026-09-17) added `uq_mw_wiring_app_node` — a unique
index on `(application_id, node_id)` — at a time when File Guard was the ONLY guard kind that
existed. At that point "one wiring per node" and "one guard per node" were the same rule by
coincidence, not by design. Once PII Guard and Prompt-Injection Guard were added weeks later,
this silently became a real product limitation: a node could never carry more than one guard
type, and nothing ever tested or exercised the 2-guards-on-one-node case, because nobody had
tried it live until this session. Confirmed by querying the live table: zero apps, ever, had
2 wirings on the same `node_id` before the fix.

**A subtlety that made this easy to miss:** the FRONTEND (`AgentGuardsSection.tsx`, built earlier
this same session) already matched a wiring by `(node_id, def_slug)` — written correctly for the
multi-guard case from day one, purely because that's the natural way to look up "does THIS guard
have a wiring on THIS node," not because anyone deliberately designed for multi-guard-per-node.
The bug was invisible until an actual database write with two guards on one node was attempted —
which nothing in the UI flow before this session had ever done, since the Guards panel section
is per-guard-per-node already and nobody had tested checking two boxes on the same node.

**Fix:** widened the index to `(application_id, node_id, def_id)` — one wiring per node PER
GUARD TYPE. `CreateMiddlewareWiring`'s `ON CONFLICT` target updated to match. Re-saving the same
guard on the same node still upserts in place (unchanged behavior); two different guards on one
node now insert as two independent rows.

**Watch for:** any unique constraint written when a feature has exactly one "kind" of a thing
(here: one guard kind) is a landmine for the day a second kind is added — the constraint's real
intent ("one X per node") gets silently narrowed to "one of the only X that exists per node,"
and nothing will fail until someone actually tries the N>1 case. When adding a second variant of
something that previously had only one, explicitly grep for every unique index/constraint touching
that table and ask "was this written assuming only one kind could ever exist?" — don't wait for a
live duplicate-key error to find out.
