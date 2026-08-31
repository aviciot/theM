# Reattach / Tab-Switch Design Review

**Date:** 2026-08-19
**Scope:** Playground tab-switch re-attach feature (WS probe → `GetActiveRunByContextID` → stream or `no_active_run`).
**Verdict:** **Scrap the WS-probe design. Ship Option D now (history-only), keep Option A on the shelf.**

---

## 1. Is the fundamental approach sound?

**No.** Sending an empty-content `message` frame as a "probe" and routing it through the
full admission pipeline is the wrong shape for a liveness check, for three independent reasons:

1. **A liveness check is a read; Admit is a write.** `Lifecycle.Admit`
   (`go/internal/execution/lifecycle.go:127`) does gate reservation → `session.Register`
   → `gate.Confirm` → `recorder.CreateRun`. Every probe therefore burns a gate slot, a
   session hash, and a `them.runs` row (status `admitted`), then immediately unwinds them in
   `Release`. Using a state-mutating admission path to answer "is anything running?" is a
   category error. The orphan `failed` rows are not a cosmetic bug to be filtered — they are
   the *designed* output of running a write path for a read question.

2. **The probe races the gate against the thing it's probing for.** If a run is genuinely
   active and the EP cap is 1, the probe's own `gate.Check` can be rejected with
   `ErrCapExceeded` *before* the reattach lookup ever runs — the probe competes with the very
   workflow it wants to observe. The feature can deny itself.

3. **The transport (WS upgrade) is far heavier than the question.** Answering "is run X live?"
   requires one indexed SELECT. Paying a WS upgrade + full admission for it is disproportionate,
   and it forces the probe/answer protocol to live inside the message loop, which is what
   produced the `handle.RunID` overwrite bug and the double-probe noise.

The **EP-mismatch bug** (probe fires against the currently-selected EP, but the stored
`contextId` may belong to a run on a different EP) is a *symptom* of coupling the check to a
connection. A run is identified by `context_id`; it is not owned by an EP slug. Any check keyed
on "the EP the user happens to have selected" is structurally wrong. Note `GetActiveRunByContextID`
(`recorder.go:181`) is already correctly EP-agnostic (keyed on `context_id` + `tenant_id`) — but
the WS handler can only reach it *after* admitting against a specific EP, re-introducing the coupling
the lookup avoided.

---

## 2. Should the re-attach feature exist at all?

**Almost certainly not as live re-attachment.** Weigh what is actually lost on tab-switch:

- **(a) Live streaming tokens while away** — the workflow runs on Temporal regardless. Redis
  Streams retains the events. The only loss is *watching them arrive character-by-character in
  real time*. For a playground/admin testing surface, this is close to zero user value.
- **(b) Empty chat panel on return** — this is the real annoyance, and it is fully solved by
  loading `contextMessages(ctxId)`, which the frontend **already calls** in the `no_active_run`
  branch (`page.tsx:1223`).

So the entire server-side reattach machinery exists to preserve (a) — mid-stream live
re-attachment — which is the least valuable half and the source of all the complexity and bugs.
This is a poor trade.

---

## 3. Minimal correct fix — option analysis

| Option | Orphan runs? | Fixes EP mismatch? | Server complexity | Restores (b) | Restores (a) live |
|---|---|---|---|---|---|
| **A** HTTP `GET .../ws/status?context_id=` | No | Yes (context-keyed, EP-agnostic) | Low (new read handler + route) | Yes | Yes, if kept |
| **B** `?probe=1` on WS URL, skip Admit | No | Only if lookup made EP-agnostic | Medium (branch inside upgrade path) | Yes | Yes |
| **C** Move Admit after first message | No | No (still per-connection/EP) | High (lifecycle refactor, risk) | Yes | Yes |
| **D** History-only, delete reattach | No (no probe at all) | N/A (no probe) | **Negative** (delete code) | Yes | No |

- **Option B** keeps the check bolted onto the WS upgrade path — the exact coupling that caused
  the bugs — and forces a new "skip Admit" branch through the most security-sensitive code
  (tenant resolution, access mode). Rejected.
- **Option C** is a large refactor of the admission ordering invariant (Admit-before-upgrade is
  load-bearing: it lets pre-upgrade failures be clean HTTP responses, `handler.go:180`). High
  risk for a playground nicety. Rejected.
- **Option A** is the correct design *if live reattach is wanted*: a read endpoint answers the
  read question, EP-agnostic, no admission side effects, and the client opens a WS only when a
  run is confirmed live. Keep as the future path.
- **Option D** deletes the problem. Given §2, it is the right immediate move.

---

## 4. Recommendation

**Do Option D now.** Remove live re-attachment; on playground mount, load conversation history
and let the user re-engage. If product later insists on live mid-stream reattach, add **Option A**
(a dedicated read endpoint) — never resurrect the WS probe.

### Concrete changes

**Frontend — `frontend/src/app/admin/playground/page.tsx`**
- In the mount effect (~1150–1165): replace the `reattach(saved)` call with a direct history
  load via `themApi.contextMessages(saved, 200)`, mapping to `ChatMsg[]` exactly as the current
  `no_active_run` branch already does (lines 1223–1228). Set `contextId(saved)` and
  `restoredSession`; do **not** open a WS.
- Delete the `reattach` `useCallback` entirely (~1200–1260), including its `no_active_run` /
  `ready` / `token` streaming branches. This removes the double-probe (two mounts × one probe)
  at the source.
- Keep the `them:playground:ctx:{ep}` persistence effect (1168–1172) — history restore still
  needs the saved id.

**Go — remove the now-dead probe path**
- `go/internal/ws/handler.go`: delete the re-attach block (294–334) — the `isReattachProbe`
  logic, the `GetActiveRunByContextID` call, the `ready`/`no_active_run` writes, and the reattach
  `streamEvents` branch. Keep the `clientContextID` → `handle.ContextID` assignment (287–289):
  that is legitimate multi-turn continuity, not part of the probe.
- `go/internal/ws/handler.go`: remove the `RunLookup` interface (line ~90), the `runLookup`
  field (~110), and its setter (~145) **only if** nothing else consumes them (grep confirms WS
  is the sole caller).
- `go/internal/runrecorder/recorder.go`: `GetActiveRunByContextID` (181–194) and `ActiveRun`
  (170–174) become unused. Either delete them with their tests
  (`recorder_test.go:341–370`, `TEST_INDEX.md:391–392`) or, if you want to keep them for a
  future Option A endpoint, leave them and add a one-line comment marking them reserved. Deleting
  is cleaner; the query is trivial to re-add.
- Update `go/TEST_INDEX.md` in the same commit if the tests are removed.

**Guard against the double-mount even in the history path**
- StrictMode double-invokes effects in dev. The history load is idempotent (a SELECT), so it is
  harmless — but wire the mount effect through a `useRef` "did-restore" guard so it runs once.
  This is the correct place to kill the double-fire, not the server.

### What to undo from the 3 days of fixes
- The `handle.RunID`-overwrite fix and the "mark released probe as failed" behavior become moot
  once probes no longer exist — they were compensating for orphan rows the probe created. The
  underlying `Release` orphan-run guard (`lifecycle.go:426`) is *correct and unrelated*; keep it.

### Tests
- `go test ./internal/ws/... ./internal/runrecorder/...` after the deletions.
- Manual: run a message in playground, switch tabs mid-run, return → chat repopulates from
  history (final answer visible), no new `failed` rows in `them.runs`, exactly one history fetch.
