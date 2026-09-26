# Current Session State — the-M
# Last updated: 2026-09-26 — docs/APPFLOW_NAMED_PORTS_PLAN.md Phases 1-5 complete AND
# live-browser-verified (all 4 steps of docs/APP_CANVAS_VERIFICATION_PLAN.md's build-up
# checked directly by the user in the real UI: LLM->Agent, Condition true/false branching,
# Fork/Join, and the named-port drag-to-connect feature itself). New /app-canvas skill
# created capturing build/debug knowledge for future sessions. Also same session: a new
# thread, docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md — Phase 1 DONE (AppFlow's agent-calling
# path now recognizes all 4 real A2A response part kinds — text/file/data/raw — instead of
# silently dropping anything but text; found while investigating whether File Guard could be
# wired into App Canvas). Phases 2-3 (File Guard hook + frontend config UI) not started.
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
HEAD: `8e6785fc` (local; not yet pushed this session — ask before pushing, per this project's
git rules). Remote is `origin` → `aviciot/theM` on GitHub (credentials already configured in the
remote URL from a prior session).

**Note:** more than one session may be advancing `main` around the same time. Before pushing,
`git pull --rebase origin main` — if it conflicts in `go/TEST_INDEX.md` (running test-count totals)
or this file's header/recent-commits list, resolve by hand: both sides are append-only edits, keep
both sets of additions and fix up the running totals/headers to be consistent. Do not discard the
other session's entries.

Recent commits (newest first):
```
8e6785fc  fix(appflow): remove extra data-port dots from llm/condition nodes
6c8261de  feat(appflow): Phase 4 — data-port handles on llm/condition, guard real wiring
33bc72e4  feat(appflow): Phase 3 — read-only "Reads" panel for llm/condition nodes
95b01c56  feat(appflow): Phase 2 — extract shared templateVars/graphWalk helpers
737ce858  docs(claude): make chat answers short and friendly by default
4b7ca19e  feat(appflow): Phase 1 named data ports — llm output port registry metadata
a78c3ca9  docs: plan AppFlow named data ports (Phase 0 complete)
79fa7956  docs(current): record Platform-as-Tenant Phase 3 completion
d5ad1575  feat(db): Platform-as-Tenant Phase 3 — RLS verification
47407692  docs(current): record Platform-as-Tenant Phase 2 completion
9cf638a8  feat(db): Platform-as-Tenant Phase 2 — backend consolidation
8b9511b5  fix(ci): satisfy go vet lostcancel check in two context-cancellation tests
dc529235  feat(db): Platform-as-Tenant Phase 1 — migrate platform LLM rows to bootstrap tenant
91028cfa  fix(appflow): sync step cursor across fork so post-join node needs its own Step click
efb443e3  docs: remove closed playground EP investigation doc
935a3320  config(keycloak): point frontendUrl at LAN IP instead of localhost
5f1353d4  docs(current): record Phase 6 completion — App Canvas Debug Mode plan done
557254f2  feat(app-canvas): Debug Mode Phase 6 — Step controls with lockstep multi-branch pausing
950a587f  docs: record c1f01aa2 review follow-up — bounded debug lifetime + UI wiring
79ec09ef  feat(app-canvas): wire model selector + Base URL field into debug credential picker
f0adfecc  fix(appflow): bound debug-run lifetime and derive credential TTL from it
962ddb62  docs: record the 4-issue review follow-up for AppFlow runtime params
b715373b  fix(app-canvas): 4 review issues in per-node debug LLM credentials (d941aca3)
03133ca8  docs: AppFlow runtime-params plan complete + two lessons from this session
04e9f4ee  feat(app-canvas): per-node LLM credential picker in the debug panel
9a19aab4  feat(app-canvas): per-node debug LLM credential overrides + tenant-ownership fix
857034c1  feat(admin): the-M admin gets General/Custom LLM key parity with tenants
21569888  feat(admin): security_scanner as a third tenant_system_agent_config role
7cb2728d  feat(app-canvas): debug mode frontend — setup panel, Run All, real WS consumer (Phase 5)
b27d0423  fix(dashboard): /ws/dashboard run:* channels never delivered live events
46c9f9b8  feat(admin): LLM Providers UI/UX cleanup + seed missing gemini/groq rows
e8115751  docs(current): update HEAD reference to a3fdcc61
a3fdcc61  docs: tenant LLM provider keys plan — step 7 sign-off, all 7 steps complete
220adc05  feat(admin): tenant General/Custom switch for classifier & card_synthesizer (step 6)
283db880  feat(admin): tenant system-agent role config — general/custom mode (step 5)
8269eae2  feat(app-canvas): debug run backend — draft execution, no publish required (Phase 5 slice 1)
```

---

## START HERE — next session

**Active thread: `docs/APPFLOW_NAMED_PORTS_PLAN.md` — Phases 1-5 COMPLETE, including a 2026-09-26
follow-up round from the user's first real browser click-through.** Only Phase 6 (optional
static-validation pass, not yet approved) remains, and it's explicitly opt-in — do not start it
without asking first. Read that doc's Phase 5 "DONE" entry (both the original write-up and the
"Follow-up fixes, 2026-09-26" block right after it) before touching this feature again — 4 real
bugs were found live and fixed: a stale-closure bug that broke the popover on rapid successive
drags (fixed with a ref); `input_aliases` storing a copied var-name string instead of a real
reference to the source node (fixed — now `{alias: {source_node_id, source_var}}`, so renaming a
source's `output_var` propagates live and same-named outputs from different nodes stay
distinguishable); and a subtler one caught only by reading the actual Go runtime
(`go/internal/appflow/inline.go`) — `{{.aliasName}}` is real Go `text/template` syntax against a
flat `map[string]string`, so an early attempt at a dotted default alias (`LLM2.output`) would have
silently mis-resolved at runtime (a dot means chained field access to Go's template engine, not a
literal map key); fixed by using `_` as the joiner instead, with a dedicated regression test.
**Not yet re-verified live after this follow-up round** — recommend re-running the browser
walkthrough once more: drag `llm` → `condition` (should auto-bind silently, no popover), then
`llm` → `llm` immediately after (should show the 2-option popover — System Prompt / User Prompt —
positioned at the actual drop point; confirm this works even right after the first drag, not just
in isolation), then open the target's Reads panel and confirm the alias, its inline rename, its ✕
delete, and — new this round — an amber "drifted" notice if you rename the source node's Output
Variable field after the binding was made.

**Quick summary of what's done:**
- Phase 1: `llm` node's registry metadata (`go/internal/appflow/noderegistry.go`) got
  `OutputPorts` aliasing `output_var`. Zero runtime change.
- Phase 2: `extractTemplateVars`/`reachablePredecessors`/`reachableSuccessors` extracted to
  `frontend/src/lib/templateVars.ts` + `graphWalk.ts`; agent builder's `nodeVars.ts` re-exports.
- Phase 3: read-only "Reads" panel section (`cbv/appFlowVars.ts` + `cbv/panels/
  InlinePortsSection.tsx`) mounted in `InlineNodePanel.tsx` for `llm`/`condition`. **Live-verified
  by the user in the browser** — confirmed both the red "unresolved" and cyan "from 🧠 <node>"
  resolved states render correctly.
- Phase 4: attempted to add visible data-port `<Handle>` dots to `InlineNode` — **built, then
  reverted same session** after the user clarified (live, in the browser) that the intent was
  never a separate always-visible dot per port; it's one wire per connection, same 2 handles as
  today, with a picker menu handling multiplicity (that's Phase 5). The revert is commit
  `8e6785fc`. One real bug WAS found and fixed permanently during this phase and is still true
  today: `validateConnection` (`CanvasInner.tsx`) never inspected handle IDs at all — a guard
  rejecting any `data-`-prefixed handle ID was added and kept (currently a no-op since nothing
  produces that ID anymore, but harmless and correct to leave in place).
- **A real deployment bug was hit and fixed this session, unrelated to the port work itself but
  worth remembering**: after Phase 1's Go change, `them-go-bridge` was not rebuilt/restarted, so
  the live `/admin/node-types` response didn't include the new `output_ports` field for over a
  day of subsequent work — silently made Phase 4's dot invisible and caused a confusing debugging
  detour. Rebuilt via `docker compose ... build them-go-bridge` + `up -d them-go-bridge`, confirmed
  fixed via a direct `wget` against the endpoint before resuming UI verification. **Lesson: after
  any `go/` change in this kind of session, rebuild+restart `them-go-bridge` before doing ANY live
  UI verification that depends on it** — don't assume the running container picked it up.

**Phase 5 — DONE 2026-09-25.** Real drag-to-connect wiring, implemented exactly per the
user-confirmed spec: no new handles (reuses the 2 existing control ones); dragging from `llm`'s
single output straight to a target; no picker on the source side (llm only ever has one output
var); a small popover at the drop point on the target side only when the target has >1 nameable
field (`llm`: system+user prompt; `condition`: never ambiguous, auto-binds to `expression`). Data
model: `input_aliases: {alias: underlying_var}` inside the node's already-opaque `config` object —
confirmed zero serialization-layer changes needed. New files: `cbv/useInlinePortWiring.ts` (bind/
rename/delete logic, agent-builder-matching collision suffixing `output`/`output_2`/...),
`cbv/PortBindingPopover.tsx`, `cbv/panels/PortAliasField.tsx` (ported from the agent builder).
`InlinePortsSection.tsx`'s Reads panel gained inline rename + delete for any dragged-in alias.
Solved the "how do you get drop coordinates" gap the spec flagged as open: `onConnect` fires
before `onConnectEnd` in xyflow v12's own lifecycle (confirmed by reading `@xyflow/system`'s
source), so `handleConnect` stashes a pending popover request and `handleConnectEnd` reads
`event.clientX/clientY` off the very next callback to position it. The count-badge loose end was
explicitly confirmed out of scope for this pass — still deferred. Full detail, including the exact
collision-naming and text-insertion rules confirmed live with the user before writing code, in
`docs/APPFLOW_NAMED_PORTS_PLAN.md`'s Phase 5 "DONE" entry. 27 new tests (78 total), `npx tsc
--noEmit` clean project-wide. `npx next build` blocked by a pre-existing environment permission
issue unrelated to this change (`.next/trace` owned by a different local user) — not a regression,
not retried destructively. **Not live-browser-verified** — see this file's "START HERE" section
above for the exact walkthrough to run next session.

**Platform-as-Tenant (`docs/PLATFORM_AS_TENANT_PLAN.md`) is DONE — all 6 of 6 phases complete.**
No further work planned on that thread. Read it end to end if touching anything
`llm_providers`/`llm_provider_keys`/system-agent-role/debug-mode related, even though the plan
itself is closed — it has the full current-state map for how those pieces fit together now.

**Phase 6 (verification) is done** — re-ran the App Canvas Debug Mode walkthrough that originally
surfaced this whole plan, live, with the user. Confirmed both halves of the original bug are
fixed: Settings → LLM Providers now shows the bootstrap tenant's own 5 providers + MainKey via the
tenant self-service screen (Phase 4's fix), and `stage2-graph-llm-condition-v2`'s debug panel can
now actually start a run using that key — verified with a real `POST .../debug/start` call
against the live stack, `200` + a real `run_id`, on the app's still-unpublished draft.

**Four real bugs found and fixed along the way, none of them Phase 1-5 regressions:**
1. Phase 3's own RLS integration test (`go/internal/db/platform_as_tenant_rls_integration_test.go`)
   was leaking `rlsp3-*` fixture rows into the live database on every run — a `t.Cleanup`-vs-`defer`
   ordering bug (the pool closed before its own cleanup delete could use it). Fixed by moving the
   `Close` into `mustSuperPool` via `t.Cleanup` instead of a caller-side `defer`.
2. `them-go-bridge` was running a stale binary — a recent-looking image ≠ a rebuilt one. Rebuilding
   + restarting fixed a "Save allowed models → 404" symptom with **zero code change**. See
   `docs/LESSONS.md`'s new entry — don't trust live/manual verification without rebuilding first.
3. App Canvas Debug Mode's long-standing "must publish before debugging a canvas with an agent
   node" limitation (`docs/APP_CANVAS_DEBUG_PLAN.md`) was a real design gap, not an acceptable
   documented trade-off — the user pushed back on "just publish it" as an unacceptable workflow.
   Fixed by resolving agent IDs live at debug-start time
   (`go/internal/admin/service/appflow_debug.go`'s new `resolveDraftAgentIDs`), reusing the exact
   same server-side registry lookup `PublishDefinition` already used — no publish required anymore.
   9 new tests (`appflow_debug_agent_resolve_test.go`).
4. Continuing the same live debug run, the inspector showed "Done" for the LLM node but "No output
   captured for this node yet." `InlineLLMActivity`/`InvokeAgentActivity`
   (`go/internal/appflow/activities.go`) both hardcoded `node_done`'s trace detail to `""`,
   discarding the real `responseText`/`text` they'd already computed and used elsewhere (token
   streaming, the activity's own return value) — every other node kind (router, condition,
   fork/join) already traced its real result. Fixed by passing the real value instead of `""`; 2
   existing tests strengthened to assert it (`workflow_test.go`, AF-TR-01/03). See
   `docs/LESSONS.md`'s new entry and `docs/APP_CANVAS_DEBUG_PLAN.md`'s revised Phase 2 section.

`go build ./...` + `go vet ./...` clean. Full `go test ./...` — every package passes, including
`internal/a2a` (an earlier full-suite run in this same session had it time out; re-run clean,
confirmed a one-off environmental flake, not a regression — new row in `go/TEST_INDEX.md`).
`them-go-bridge` rebuilt+restarted twice this session (bugs 2 and 3);
`them-dag-worker`/`them-dag-worker-2`/`them-dag-worker-debug` all rebuilt+restarted for bug 4
(per `go/CLAUDE.md`'s trigger map for `internal/appflow/activities.go`); confirmed healthy.

**Phase 5 (tenant management UI) is done** — `/admin/tenants` (`frontend/src/app/admin/tenants/page.tsx`)
now shows an amber "Platform" badge (shield icon, tooltip "the-M's own operating tenant — cannot
be deleted") on the bootstrap tenant, in both the grid's `TenantCard` (next to the existing
enabled/IdP badges) and the side panel's header (next to the display name) — gated on
`tenant.is_bootstrap`, which the API already returned and `apiTypes.ts` already typed, so no
backend change was needed. Purely additive: the bootstrap tenant is still fully visible/manageable
in the list (not hidden), and the pre-existing deletion guard (`dal/tenants.go`'s `is_bootstrap =
false` filter, and the existing `!tenant.is_bootstrap` check that already hid the Danger Zone
section) was not touched — it already worked correctly. `ProvisionWizard.tsx` (tenant creation)
untouched, since the bootstrap tenant is seeded, never created through that flow.
`npx tsc --noEmit` 0 errors. No Go files touched, no Go test run needed per the trigger map.
**Not live-browser-verified** — same standing limitation as every phase of this plan so far, no
browser-automation tool available in this environment. Recommend a real logged-in check next:
`/admin/tenants` should show the badge on the bootstrap tenant's card and side panel, Danger Zone
still absent for it specifically.

**Phase 4 (frontend consolidation) is done** — removed the `isSuperAdmin` branch from Settings →
LLM Providers / System Agents (`frontend/src/app/admin/settings/page.tsx`,
`GeneralModePicker.tsx`, `LLMProvidersPanel.tsx`, `LLMProviderKeysPanel.tsx`); both screens now
always render the tenant-scoped view (`TenantRoleCard`, `listMyLLMProviders`, etc.) for the
caller's own tenant. `RoleCard.tsx` deleted (fully unused after the branch removal; its `Toggle`
component moved to a new standalone `Toggle.tsx` first, since `LLMProvidersPanel.tsx` still needs
it). `api.ts`/`apiTypes.ts` lost the now-fully-dead `getSystemAgents`/`putSystemAgents`/
`testSystemAgentLlm` and all `listPlatformProviders*`/`*PlatformProviderKey*` functions —
confirmed by grep these had zero remaining callers after the frontend branch removal, and their
backend routes (`/admin/system-agents`, `/admin/llm-providers/{name}/keys...`) had already been
deleted by Phase 2, so the removed branch was calling dead routes, not just "the wrong screen."
This is the phase that actually closes the original bug this whole plan started from — a
`super_admin` session as `avi`/`admin` can now reach the tenant self-service LLM Providers/System
Agents screens the same way any tenant admin does, scoped to the bootstrap tenant.
**Not live-browser-verified** — same standing limitation as every other phase of this plan, no
browser-automation tool available in this environment. Recommend a real logged-in check:
`avi`/`admin` → Settings → LLM Providers should show the bootstrap tenant's 5 providers +
"MainKey" (moved there by Phase 1) — this check and Phase 6's re-verification can be done
together in the same session.

**Phase 3 (RLS verification) is done** — `go/internal/db/platform_as_tenant_rls_integration_test.go`,
4 new tests run via `them_app`/`BeginTenantTx` (real RLS enforcement, not the BYPASSRLS admin pool
Phase 2's own tests used) against this box's live, already-migrated Postgres. Confirms decision 7's
asymmetric visibility exactly as designed: `llm_providers` rows ARE visible cross-tenant (read-only)
as platform defaults; `llm_provider_keys` rows are NOT, at all, to any other tenant. Turns Phase 1's
one-time manual `SET ROLE` check into a permanent regression test. Full detail, including a real
GRANT-level finding this phase's own test-writing surfaced (`them_app` has SELECT-only on both
tables — neither table's write RLS policy is reachable via `them_app` for any tenant; every real
write goes through the Admin/BYPASSRLS pool) and a **new, unrelated, not-yet-fixed issue** (two
pre-existing `internal/db` RLS tests — `TestRLS_TwoTenantFullIsolation`,
`TestRLS_CatalogVerification` — fail on schema drift unrelated to this plan: a stale
`component_definitions` constraint name, and three `tenant_role_*` tables missing `FORCE ROW LEVEL
SECURITY`), in `docs/PLATFORM_AS_TENANT_PLAN.md`'s "Phase 3 — COMPLETE" section.

**What this plan is:** the-M's own "platform-level" LLM provider/key config (used by classifier,
card_synthesizer, security_scanner, and previously invisible to App Canvas Debug Mode's General-mode
credential picker for apps owned by the-M's own bootstrap tenant) was represented as a parallel
`tenant_id IS NULL` convention on `them.llm_providers`/`them.llm_provider_keys`, with a full
duplicate set of DAL/service/handler/frontend code alongside the tenant-scoped equivalents every
normal tenant uses. This plan removes that duplication: the-M's own bootstrap tenant
(`00000000-0000-0000-0000-000000000001`, `is_bootstrap = true`) becomes the-M's real operating
tenant, using the exact same tenant-scoped code path as everyone else.

**Origin:** found live, this session, while walkthrough-testing App Canvas Debug Mode Phase 6
together with the user — `stage2-graph-llm-condition-v2` (owned by the bootstrap tenant) had no
usable key in the debug panel's General mode, and Settings → LLM Providers had no UI path for a
super_admin to reach the bootstrap tenant's own self-service screen (always showed the platform-wide
view instead, even for a super_admin whose own tenant membership IS the bootstrap tenant).

**Phase 1 (data + RLS) is done and verified live** — `db/110_platform_as_bootstrap_tenant.sql`,
committed and pushed as `dc529235`. 5 `llm_providers` rows + 1 `llm_provider_keys` row ("MainKey")
moved from `tenant_id IS NULL` to the bootstrap tenant; partial unique indexes replaced with normal
ones; `llm_providers_read`'s RLS policy rewritten to name the bootstrap tenant instead of checking
`IS NULL` (confirmed with the user: keep cross-tenant visibility of platform-default providers —
`llm_provider_keys`' own policy needed no change, confirmed asymmetric by a dedicated review pass).
Verified via `SET ROLE them_app` role-switching (not just reading the policy SQL): other tenants see
the bootstrap tenant's provider defaults but not its keys; writes to bootstrap-owned rows from
another tenant context are rejected. `go test ./...` + `go test -tags=integration
./internal/admin/...` both 0 failures against the live migrated DB. No Go/frontend code changed yet
— Phase 1 was data-only by design.

**Phase 2 (backend consolidation) is done and tested** — `GetProviderByNamePlatform`/
`resolvePlatformSystemAgentRole`/`llm_provider_keys_platform.go`/`SystemAgentsHandler` and all call
sites deleted; `classify.go`/`synthesize.go`/`security_scan_llm.go` now resolve their platform
fallback through `resolveSystemAgentRole` against the bootstrap tenant instead of a separate
NULL-tenant/`them.config` path; `dal/app_config.go`'s `GetProviderBaseURLs` and
`dal/llm_providers.go`'s `ListProvidersForTenant`/`ListProviders`/`CreateProvider` now reference
`tenantctx.BootstrapTenantID` explicitly instead of `IS NULL`. See
`docs/PLATFORM_AS_TENANT_PLAN.md`'s "Phase 2 — COMPLETE" section for full detail, including a real
(if already-dead-by-the-time-found) uniqueness gap this phase's own test run surfaced and fixed by
deleting 4 stale tests — not a live bug, but worth reading before assuming "all green" always means
"nothing to look at."

**One phase per session** — do not start Phase 4 in the same session as Phase 3, etc.

**Known, deliberately-deferred gap, unaffected by Phase 1 or 2:** the frontend's `isSuperAdmin`
branch (`frontend/src/app/admin/settings/page.tsx`) still routes any `super_admin`-role session to
the platform-only screen with no path to the bootstrap tenant's own self-service screen — that's
Phase 4's job specifically. A normal browser session as `avi`/`admin` still cannot reach
`/admin/my/llm-providers`'s UI today, even though the backend data now supports it correctly.

**Also still open, found during the same walkthrough, tracked separately (not part of this plan):**
the App Canvas Debug Mode credential picker's Custom-mode fields are free-text (provider/model as
plain `<input>`s) instead of dropdowns like General mode, and there's no visual "this will be used"
confirmation before clicking Run/Start — both real UX gaps in
`frontend/src/app/admin/applications/components/AppFlowLLMCredentialField.tsx`, not yet a plan doc,
not yet scheduled.

**Also still true, deferred, not part of this plan:** the user still has not re-saved the bootstrap
tenant's Anthropic `allowed_models` — their earlier attempt landed on the pre-migration platform row
and never took effect on data; this is a user follow-up action once Phase 4 makes the tenant
self-service screen reachable (or a direct API call, if unblocking sooner is wanted).

---

**Prior thread, fully complete, no further work planned:** App Canvas Debug Mode
(`docs/APP_CANVAS_DEBUG_PLAN.md`) — all 6 phases done as of `557254f2` (Step controls), plus one
review-found fix (`91028cfa` — a fork/join step-cursor staleness bug caught by the user's own review,
not by Phase 6's original tests). See that plan doc for full detail if this thread needs revisiting;
do not start new work on it without a new concrete requirement.

**CI fixed earlier this session (2026-09-23):** `.github/workflows/ci.yml` had been broken since the
Python→Go migration — it referenced a nonexistent `docker-compose.local.yml` and the removed
`them-auth-service`/`them-bridge` containers (renamed to `them-auth-go`/`them-go-bridge`). Every
run on `main` and every PR had been failing silently. Replaced with a single `go-test` job
(`go vet ./...` + `go test ./...`); dropped the live-Docker-stack job and the now-unused
`.github/docker-compose.ci.yml`. See `docs/LESSONS.md` for the full writeup. If a live-stack E2E
job is wanted later, it needs to be rebuilt from scratch against the current container list in
this file's Container Map, not repaired from the old one.

**Phase 5 + AppFlow Runtime Params (per-node debug LLM credential overrides) are both complete and
live-verified**, including two rounds of review follow-up this session — see
`docs/APPFLOW_RUNTIME_PARAMS_PLAN.md`'s three completion sections for full detail. Round 2 (most
recent): General-mode model selector + Custom-mode Base URL field wired into the picker UI, and
debug runs now have an actually-enforced wall-clock ceiling (`appflow.DebugRunMaxLifetime` =
3h30m, set as `WorkflowRunTimeout` — previously unset entirely for debug runs) with
`debugcred.TTL` derived from that same ceiling (3h40m = 3h30m + 10min cleanup margin) instead of an
independently-guessed flat 30 minutes. The bound is surfaced to the user via `expires_at`/a panel
badge, not just silently enforced.

**Manual browser verification still outstanding for round 2** (no headless browser available in
this environment all session — see the plan doc's round-2 completion section for the exact 7-step
walkthrough): confirm the model dropdown/free-text field renders correctly in General mode, the
Base URL input renders in Custom mode, and the `⏱ expires in 3h 30m` badge + upfront description
text render correctly in a real logged-in session.

**Known limitation carried over, not fixed this session:** a draft canvas containing agent nodes
still cannot be debugged until it has been published at least once (`unresolved_agent` — see the
Phase 5 backend-slice section of `docs/APP_CANVAS_DEBUG_PLAN.md`). Flows made entirely of inline
nodes are unaffected. Worth fixing before Phase 6 if agent-node debugging is a priority. Also
still true: `llm.AnthropicProvider` has no base URL parameter in this codebase at all — a debug
override's `BaseURL` for an `anthropic`-provider node is structurally unusable, not just unwired
(only matters for `openai`/`groq`/`ollama`/`vllm`/`lmstudio` today).

**Deferred, not this session's job:** `docs/APPFLOW_RUNTIME_PARAMS_PLAN.md`'s Runtime settings
screen migration (`flow-llm-nodes` reading from the same `RuntimeParams` declaration the debug
panel now uses) — a separate future phase, explicitly not started.

**Tenant LLM Provider Keys** (`docs/TENANT_LLM_PROVIDERS_PLAN.md`) — all 7 steps complete, plus
three follow-ups from an earlier session: LLM Providers tab UI/UX redesign + missing gemini/groq
seed rows, `security_scanner` promoted to a third general/custom role, and the-M admin
(super_admin) given full General/Custom parity with tenants (its own multi-key system, `db/108`)
— see the dated sections further below for full detail on each. Nothing further planned on this
thread unless new requirements surface. **Still never verified in a live browser** — recommend a
real logged-in click-through before trusting it in front of actual tenants or the platform admin.

**Before touching anything that publishes or consumes per-run trace events, or any WS/SSE
message-parsing code:** read `docs/LESSONS.md`'s three entries from this session and the prior
one — (1) `/ws/dashboard`'s `run:*` channels never delivered live events (an `XADD`-only write
vs. a pub/sub-only consumer), (2) that same channel type had no tenant-ownership check at all (a
real cross-tenant IDOR), and (3) the Phase 5 debug hook's `ws.onmessage` was silently dropping
every real event since it first shipped, because it gated on a field (`msg.type`) that real
events never carry. All three are the same class of "the wire contract wasn't actually verified
end-to-end" bug — worth re-reading before adding any new WS/SSE consumer or producer.

---

## AppFlow Runtime Params — review follow-up, 4 issues fixed — COMPLETE (2026-09-23)

Commits `b715373b` (fixes), `962ddb62` (docs), both pushed. Full detail in
`docs/APPFLOW_RUNTIME_PARAMS_PLAN.md`'s "Review follow-up" section. A review of `d941aca3` (the
per-node debug credential work below) found and this session fixed 4 issues, before Phase 6 /
Step Debug work begins:

1. **BaseURL silently dropped** — stored in the debug override, never passed into provider
   creation (`multiLLMFactory.NewProvider` only reads its own static per-provider map). Fixed with
   a new `NewProviderWithBaseURL` used only by the debug path. Verified live: two nodes, two
   different fake HTTP servers, each node's request landed on its own configured endpoint with the
   right model and API key — neither server saw the other's traffic.
2. **General mode's `key_id` never checked against the selected provider** — a key belonging to a
   different provider could be silently accepted and used against the wrong API. Now rejected with
   422 before the run is admitted.
3. **No per-node model selection in General mode** — added, validated against the provider's own
   `allowed_models` list when one is configured.
4. **Fixed 10-minute TTL was the only cleanup mechanism** — resized to 30 minutes (justified from
   the activity retry policy's actual worst-case timing, not a round number) and made the
   fallback, not the only path: `FinalizeRunActivity` now proactively deletes every node's override
   on a debug run's terminal state via a new cursor-based `Store.DeleteAllForRun`.

14 new unit tests, 3 new integration tests against live Redis. `go test ./...` 0 failures.
**Verified live, browser-equivalent** (no browser-automation tool available in this environment —
same limitation as every prior session on this plan): a Node script drove the exact HTTP/WS
sequence the browser's Debug button triggers, with two LLM nodes each pointed at its own fake
provider endpoint via Custom-mode overrides — confirmed each request reached its intended
endpoint with the correct model and credential, no cross-contamination, no secrets exposed, and
the app's saved Runtime settings untouched throughout. **Not done:** an actual browser
click-through of the picker UI's rendering/interaction — still blocked by the same missing
headless-Chromium system libraries (need `apt-get`/sudo, no password available).

---

## AppFlow Runtime Params — per-node debug LLM credentials — COMPLETE (2026-09-23)

Commits `9a19aab4` (backend), `04e9f4ee` (frontend), `03133ca8` (docs), all pushed. Full detail in
`docs/APPFLOW_RUNTIME_PARAMS_PLAN.md`'s "Implementation — COMPLETE" section. Summary:

Follow-on to Phase 5: the debug setup panel now scans the canvas for nodes that declare a runtime
parameter (today, every `llm`-kind node declares a required `llm_credential`) and renders **one
independent provider/key picker per node** — never merged into one shared selection. General mode
picks a saved tenant key (reuses the existing `them.llm_provider_keys` lookup); Custom mode is a
one-off key for that debug run only, never persisted anywhere. A required param with no override
supplied fails the whole `debug/start` call with 422 before a run is ever admitted — proven live
against a 3-LLM-node draft. Storage is a new `debugcred.Store` (Redis, key
`them:debug:{tenant}:{run}:{node}:llm_override`, 10-min TTL) — never through Temporal, since
`run_id` is already a fresh UUIDv4 per call so no locking is needed for the concurrency
requirement. A missing/expired credential fails loudly in `dbLLMCaller.Complete`, never silently
substitutes another key; a real bug where a nil credential store fell through to normal
resolution and nil-panicked was caught by this session's own tests before shipping.

**Two more real bugs found and fixed while live-testing this** (same live-testing method as
Phase 5 — a Node script run inside `them-frontend`, no browser-automation tool available):
1. **Cross-tenant IDOR**: `/ws/dashboard`'s `run:*` subscribe path never checked that the caller's
   tenant actually owned the run UUID — any tenant could read any other tenant's live trace. Fixed
   with a new `dashboard.RunOwnershipChecker`, fails closed.
2. **The Phase 5 debug hook had never actually worked in a browser**: `ws.onmessage` gated on a
   top-level `msg.type`, but real trace events carry no such field (only `ping`/`subscribed`/a
   WS-protocol error do) — every live `node_start`/`node_done` event was being silently dropped
   since the hook first shipped earlier the same day. Fixed.

18 new tests total (13 unit across 4 packages, 5 integration against live Redis). `go test ./...`
0 failures. One pre-existing, unrelated flaky race found under `-race`
(`internal/appflow.TestForkJoin_EmitsTraceForAllNodes`, inside the Temporal SDK's own test harness)
— flagged in `TEST_INDEX.md`, not fixed. `npx tsc --noEmit` 0 errors.

**Verified live end-to-end:** rebuilt + restarted `them-go-bridge`/`them-dag-worker`/
`them-dag-worker-2`/`them-dag-worker-debug` (all healthy); `them-frontend` hot-reloaded clean.
Created a real throwaway app + 3-LLM-node draft, confirmed `debug/start` with no overrides → 422
(no run admitted), then with 3 distinct per-node custom keys → 200, real run executed, and
`redis-cli GET` confirmed 3 separate Redis entries with 3 distinct `api_key` values correctly
scoped by tenant+run+node. Throwaway app deleted after testing.

**Not done — no actual logged-in browser click-through of the picker UI itself** (no
browser-automation tool available in this environment; Playwright's Chromium downloaded but its
shared-library deps couldn't be installed without interactive sudo). The full backend contract is
proven live; the picker component's own rendering/interaction has not been visually confirmed.

---

## App Canvas Debug Mode — Phase 5 frontend + /ws/dashboard live-delivery fix — COMPLETE (2026-09-23)

Commits `b27d0423` (backend fix), `7cb2728d` (frontend), both pushed. Full detail in
`docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 5 frontend section and `docs/LESSONS.md`'s new entry.
Summary:

Built the Phase 5 frontend the earlier backend slice (`8269eae2`) was waiting on: a new
`useAppFlowDebugSession` hook (`frontend/src/app/admin/applications/hooks/`) that POSTs
`/admin/applications/{id}/debug/start` with the canvas's selected entry point + a test message,
then subscribes to that run over `/ws/dashboard` and drives per-node debug state from real
`node_start`/`node_done`/`node_error`/`done`/`error` events — not a client-side simulator like the
agent builder's own debug feature. New `AppFlowDebugPanel.tsx` (setup form + Run All/Reset/Close)
and a new "▶ Debug" button in `CanvasBuilderView.tsx`'s top bar. `CanvasNodes.tsx`'s
`InlineNode`/`FlowControlNode` gained a debug-state border/glow overlay reusing the agent
builder's `StepNode.tsx` color scheme.

**A real, pre-existing backend bug was found and fixed while live-testing this** (no
browser-automation tool was available — Playwright's Chromium downloaded but its shared-library
deps couldn't be installed without interactive sudo in this container; verification instead used a
Node script run inside the already-running `them-frontend` container against the live stack over
the Docker network): `/ws/dashboard`'s `run:*` channels never delivered live events, only a
one-shot snapshot at subscribe time — because `internal/runstream.PublishEvent` only ever `XADD`s
to the run's Redis Stream, nothing ever `PUBLISH`es. Any run fast enough to finish before that
single snapshot round-trip (every mock-LLM AppFlow debug run tested this session: well under 1s
end-to-end) delivered zero events, live or otherwise — this silently also affected the
playground's own `run:*` trace pane, not just this new feature. Fixed in
`go/internal/dashboard/handler.go`: `run:*` channels are now tailed via the same
`internal/runstream.StreamFromRedis` replay+live-poll primitive `internal/ws`/`internal/sse`
already use for production routes, instead of plain pub/sub.

Tests: 2 new in `go/internal/dashboard/handler_test.go` — `go/TEST_INDEX.md` S1-52 bumped 13→15,
S1 total 1417→1419. `go test ./...` 0 failures, full suite (58 packages). `npx tsc --noEmit` 0
errors. `them-go-bridge` rebuilt and force-recreated; logs confirm healthy startup.

**Verified against the live stack end-to-end**, not just unit tests: created a real throwaway
application + draft definition (EP + inline LLM + condition + two branch LLM nodes, no agent
nodes) + its `them.entry_points` row via direct API calls, called `debug/start`, and drove the
exact `/ws/dashboard` subscribe flow the new frontend hook uses. Before the fix: ack then silence
for 20s despite the run completing correctly (confirmed via direct Redis `XRANGE`). After the fix:
the full `node_start→node_done→token→node_start→node_done→node_start→node_done→token→done`
sequence arrived live, in order, matching exactly what the frontend hook parses. Also confirmed
the documented "agent nodes need publish first" limitation is real, reproducing it as a 422
(`unresolved_agent`) against an existing draft that has agent nodes. The 9 throwaway
`phase5-probe-*` applications created during this verification were deleted from the live DB
after testing (confirmed with the user first).

**Not done — no actual logged-in browser click-through of the new "▶ Debug" button** (see above
for why, and what was done instead to compensate). The WS/backend contract it depends on is now
proven live end-to-end; recommend a manual UI pass before fully trusting the button/panel
rendering itself. Step controls (Phase 6) not built — explicitly out of scope for this phase.

---

## Tenant LLM Provider Keys — follow-up: the-M admin gets General/Custom parity — COMPLETE (2026-09-23)

Commit `857034c1`, not yet pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s "the-M admin gets the
same General/Custom parity tenants have" section for full detail. The user asked directly why
super_admin couldn't have the same multi-key general/custom system tenants just got — the honest
answer was there was no real reason, the first pass had just only wired it up for tenants.

**Schema:** `them.llm_provider_keys.tenant_id` made nullable (`db/108_platform_llm_provider_keys.sql`,
applied live) — `NULL` = platform-owned, mirroring `them.llm_providers.tenant_id`'s existing
convention. Old single unique/default constraints replaced with four indexes split by
NULL/non-NULL, same pattern `them.llm_providers` already uses.

**Go:** every `LLMProviderKeyService`/DAL method's `tenantID string` widened to `*string` (nil =
platform) rather than duplicated into parallel methods. New
`internal/admin/llm_provider_keys_platform.go` mirrors the tenant self-service key routes under
`/admin/llm-providers/{name}/keys...`. New `resolvePlatformSystemAgentRole` mirrors
`resolveSystemAgentRole`'s general/custom logic for the platform's own config;
`classifyAgent`/`synthesizeAppCard`/`llmCardAnalysis` all call it now instead of three separate
inline platform-fallback blocks. `them.config['system_agents']` roles gained `mode`/`key_id`.

**Frontend:** `RoleCard.tsx` (super_admin) gained the same General/Custom switch
`TenantRoleCard.tsx` already had. `LLMProvidersPanel`/`LLMProviderKeysPanel` gained the same
allowed-models + named-keys section for super_admin tenants already had.

**Found and fixed two pre-existing bugs while writing tests** (not introduced this session): a
service-layer test fake checked the wrong not-found flag (`GetProviderByNamePlatform` incorrectly
reused `tenantProviderNotFound`); and `tokens_sessions_integration_test.go` had bit-rotted to the
point of not compiling at all under `-tags=integration` — blocking every integration test in
`internal/admin`, including this change's own new ones, from running. Fixed by pointing it at the
already-correct `admin.NewPgxQuerier` instead of a stale hand-rolled copy. **One test in that file
remains broken at runtime** (`TestIntegration_CreateToken_201` — `TokensHandler.Create` now
requires tenant context the old test never sets up) — flagged in `go/TEST_INDEX.md`, not fixed,
pre-existing and unrelated.

20 new tests. `go test ./...` 0 failures full suite. `go build -tags=integration ./...` clean.
Integration tests run for real against this box's live `them-postgres`. `tsc --noEmit` 0 errors.
`them-go-bridge` rebuilt and force-recreated, confirmed healthy.

**Not live-verified this session** — same standing limitation as the whole plan: no
browser-automation tool, no direct curl probing. Recommend a real super_admin walkthrough: enable
a platform provider, save allowed models and a named key, set classifier to general mode using
that key, confirm a real classify call actually uses it.

---

## Tenant LLM Provider Keys — follow-up: security_scanner as a third role — COMPLETE (2026-09-23)

Commit `21569888`, not yet pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s "Follow-up" section
for full detail. Two pieces of post-sign-off work, both reusing the plan's own infrastructure:

**LLM Providers UI/UX redesign + missing gemini/groq seed rows** (commit `46c9f9b8`, already
pushed) — the user reviewed the live UI and found tenant admins had no enable/disable control
(only rendered for super_admin) and the allowed-models checklist looked bad; also found gemini and
groq never appeared despite being fully supported in code, because nobody ever seeded platform
rows for them (only `anthropic`/`openai` ever were). Fixed the toggle, redesigned the checklist as
labeled rows, and added `db/107_seed_gemini_groq_providers.sql` (applied live).

**security_scanner promoted to a third role** — the user asked whether the security scanner could
use a tenant's own key the same way the classifier does. Found the scan's LLM analysis ran inside
the separate Python `them-security-agent` container with one hardcoded platform-wide Anthropic
key, never resolved per-tenant. Moved that LLM step into go-bridge (`llmCardAnalysis`, new
`internal/admin/security_scan_llm.go`) using the exact same `resolveSystemAgentRole` step 5 built,
rather than passing a decrypted key across the process boundary to Python. Python now does
HTTP-surface probes only; go-bridge merges both results (`mergeSecurityScanResult`) before
persisting. `security_scanner` now gets its own General/Custom card for free via the existing
`TenantRoleCard` machinery — no new frontend component.

7 new tests. `go test ./...` 0 failures full suite. `them-go-bridge` and `them-security-agent`
both rebuilt and force-recreated, confirmed healthy via logs.

**Not live-verified** — same standing limitation as the whole plan: no browser tool, no direct
curl probing this session. Recommend running a real Security Scan (both with security_scanner
left on custom/platform-fallback, and with it set to general mode using a real tenant key) before
trusting the merged score/findings fully.

---

## Tenant LLM Provider Keys — PLAN COMPLETE — step 7 sign-off (2026-09-23)

See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s step 7 section for full detail. All 7 steps of the plan
are now done. This session's sign-off run:

- `go test ./...` — 0 failures, full suite, run fresh (not reused from an earlier step).
- `go test -race ./...` — found **one failure, confirmed pre-existing and unrelated**:
  `internal/llmgateway.TestHandler_Stream_200` (a genuine data race in `Service.Stream`'s
  goroutine, reproduced 1-of-3 runs in isolation). `git log` confirms `internal/llmgateway/
  handler.go` was last touched by commit `936ae696` (LLM Gateway Phase 2) — no commit in this plan
  (`465ffe93` through `220adc05`) touches that package. Not fixed here — flagged in
  `go/TEST_INDEX.md`'s new "flaky (pre-existing)" row as a dedicated follow-up.
- `npx tsc --noEmit` — 0 errors, run fresh.

**Never verified, across any session that worked on this plan:** a real logged-in browser
click-through, or a direct `curl` round trip against the live stack. No browser-automation tool
was available in any of those sessions; direct API probing was attempted once this session and
declined, and not repeated afterward. Every verification claim across all 7 steps is either a
unit/integration test (several run for real against live Postgres) or a container-log
observation (compile success, healthy startup) — never an end-to-end interactive check. Before
trusting this feature in front of real tenants, do a manual pass covering: enable a provider +
save allowed models, add/test/delete a named key, set a role to general mode and confirm a real
classify/synthesize call actually uses that tenant's own key (not the platform's), and set a role
to custom mode with its own key and test it.

---

## Tenant LLM Provider Keys — step 6: frontend General/Custom switch — COMPLETE (2026-09-23)

Commit `220adc05`, pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s step 6 write-up for
full detail. Summary:

**Design point confirmed with the user before implementing:** the System Agents tab's `RoleCard`
is wired to the platform-global `/admin/system-agents` route (super_admin only) — a plain tenant
admin opening this tab today gets a failed fetch and an "unavailable" banner. Since the new
General/Custom switch is inherently tenant-scoped, the tab now branches on role: super_admin keeps
`RoleCard` unchanged; tenant admin gets a new `TenantRoleCard`
(`frontend/src/app/admin/settings/TenantRoleCard.tsx`) backed by step 5's tenant-scoped route. The
platform-global config fetch on page load is now skipped entirely for tenant admins — this closes
the pre-existing false "unavailable" banner as a side effect, not the main goal of this change.

Also confirmed with the user: general-mode classifier/card_synthesizer calls run on **that
tenant's own API key against that tenant's own workspace only** — every DB lookup in step 5's
`resolveSystemAgentRole` is keyed by `tenantID`, so results never cross tenants.

**Backend addition:** the plan's step 6 spec called for Custom mode to keep "the existing Test
button, unchanged" — but the existing test route is super_admin-only, so a tenant admin can't call
it for their own custom config. Added a tenant-scoped mirror,
`POST /admin/my/system-agents/{role}/test-llm` (`TenantSystemAgentConfigHandler.Test`, new service
method `ResolveCustomTestInputs`) — same `probeLLMWithBase` probe, gap-fills from the tenant's own
stored `custom_*` fields, never from the platform config.

**Frontend:** `TenantRoleCard.tsx` (new) — General mode: provider dropdown (from
`listMyLLMProviders()`, filtered `enabled`) → key dropdown (from `listProviderKeys`, "use default
key" as the no-selection option), no free-text fields, no test button (testing happens at the key
level in the LLM Providers tab). Custom mode: provider/model/api_key/base_url/system_prompt form +
Test button, same shape as `RoleCard`'s, wired to the new tenant-scoped test route.
`apiTypes.ts`/`api.ts` gained the matching types and three new `themApi` functions.

Tests: 4 new service tests (`ResolveCustomTestInputs`), 3 new handler tests (the `Test` route) —
`go/TEST_INDEX.md` S1-147, S1-148 (S1 total 1410→1417). `go test ./...` 0 failures full suite.
`npx tsc --noEmit` 0 errors. `them-go-bridge` rebuilt (Dockerfile runs the full suite in-image, 0
failures confirmed again there) and force-recreated; logs confirm healthy startup, no crash loop.
`them-frontend` picked up the new component via its existing bind-mount + `npm run dev` hot
reload — logs confirm 0 compile errors and a live `GET /admin/settings` 200 (a real user request
observed in the logs, not a check this session ran itself).

**Not live-verified end-to-end this session** — no browser-automation tool was available, and
direct `curl` probing of the live API was not attempted (declined once earlier this session,
treated as a standing preference). The new tenant test route was only exercised through Go handler
tests with a fake DB. Recommend an actual logged-in tenant-admin click-through — switch to
General, pick a provider/key, save, reload and confirm persistence; switch to Custom, test a key,
save — before fully trusting this. Nothing in this repo's automated test suite drives that real
round trip yet.

---

## Tenant LLM Provider Keys — step 5: tenant_system_agent_config + resolution wiring — COMPLETE (2026-09-23)

Commit `283db880`, pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s step 5 write-up for
full detail. Summary:

New migration `db/106_tenant_system_agent_config.sql`, applied live to this box's `them-postgres`
this session: one row per `(tenant_id, role)` for the `classifier`/`card_synthesizer` system-agent
roles, `mode` CHECK IN ('general','custom'). RLS + grants match the `db/105` pattern; `key_id` FK
to `them.llm_provider_keys` is `ON DELETE SET NULL` — verified live via a dedicated integration
test that deleting a key a config row points at nulls the column rather than leaving a dangling
reference.

New `go/internal/admin/system_agent_resolve.go` (`resolveSystemAgentRole`) is the single resolution
point both `classifyAgent` and `synthesizeAppCard` now call: no row / custom-with-unset-fields
falls back to the platform-global `them.config['system_agents']` row unchanged; `mode="general"`
resolves through the tenant's own `them.llm_providers`/`them.llm_provider_keys`
(step 4's tables) and **never** falls back to a platform key when the tenant has none — the
concrete enforcement point for the plan's hard rule at this layer, proven by a dedicated test and
again through both call sites directly, not just the resolver in isolation.

**Bug fixed as part of this, not scope creep:** `classifyAgent` was hardcoded to call the Anthropic
Messages API directly — broken by construction for any tenant whose "general" mode resolves to a
non-Anthropic provider. Extracted `dispatchLLMText` out of `synthesize.go`'s existing multi-provider
dispatch logic (which `card_synthesizer` already had and `classifier` never did) so both roles share
one dispatch path now.

New tenant self-service route `GET`/`PUT /admin/my/system-agents/{role}/config`
(`go/internal/admin/tenant_system_agent_config.go`), mirroring the `/admin/my/llm-providers` naming
pattern from step 3/4. No frontend consumer yet (step 6).

**Found and closed a pre-existing test gap, not introduced here:** neither `classifyAgent` nor
`synthesizeAppCard` had a single direct test before this session (confirmed by grep, not assumed).
41 new tests total across DAL/service/handler/resolver/both call sites — `go/TEST_INDEX.md`
S1-142..146, S2-13. `go test ./...` 0 failures full suite; `go test -race ./internal/admin/...`
clean; 6 integration tests run for real against this box's live `them-postgres`.

**Not done / deferred (per the plan's own sequencing, not a regression):** step 6 (frontend
General/Custom switch on the role cards) — the new route has no UI yet. Not live-verified via
`curl`/browser against the running `them-go-bridge` — only exercised through Go handler tests with
a fake DB this session, same limitation as step 4 (no browser-automation tool available).

---

## Tenant LLM Provider Keys — step 4: frontend UI — COMPLETE (2026-09-23)

Commit `99434bb9`, pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s step 4 write-up for full detail. Summary:

`frontend/src/app/admin/settings/page.tsx`'s `llm_providers` tab (previously ~150 lines inlined in
the page component) was extracted into two new components, per the file-size rule — `page.tsx`
dropped from 300 to 199 lines:

- **`LLMProvidersPanel.tsx`** (new) — per-provider card. super_admin (platform rows) keeps the
  original single API-key input + Save, unchanged. Tenant admin (own rows) gets a new **Allowed
  models** checklist seeded from `PROVIDER_MODELS` (`settingsConstants.ts`) with a **Refresh from
  provider** button (disabled until a key exists) hitting `GET .../models?key_id=...`, plus a
  **Save allowed models** button using the existing `upsertMyLLMProvider` PUT with the new
  `allowed_models` field.
- **`LLMProviderKeysPanel.tsx`** (new) — the keys sub-section: table of named keys (masked value,
  default badge, last-test-result) with Set default / Test / Delete buttons and inline
  rename/rotate-secret inputs, plus an Add-key row. Mirrors `RoleCard.tsx`'s existing test-button
  loading/ok/error UX pattern.
- `apiTypes.ts` / `api.ts` — `LLMProviderOut`/`LLMProviderUpsertInput` gained `allowed_models`
  (already existed server-side since steps 2-3; this closed a frontend-only type gap). New
  `LLMProviderKeyOut`, create/patch/test/models-result types, and 7 new `themApi` functions
  wired to the tenant-self-service routes `LLMProviderKeysHandler` already exposed in step 3.
  **Naming collision found via `tsc`:** an unrelated pre-existing app-level single-key feature
  (`/admin/applications/{id}/provider-keys/{provider}`) already used the name `deleteProviderKey`
  — the new named-key delete function was named `deleteProviderNamedKey` instead.

**Verification:** `npx tsc --noEmit` — 0 errors, project-wide, run twice to confirm not a fluke.
**No live browser verification** — no browser-automation tool was available in this environment
this session. Did not attempt any direct `curl` probing of the live stack either (declined after
one such command was denied — treated as an implicit preference not to poke the live API directly
outside the UI). Recommend a manual logged-in pass before trusting this fully, in both the
tenant-admin and super_admin views, especially the Refresh-from-provider round trip against a real
provider key (no automated test in this repo exercises that live).

**Committed as `99434bb9`, pushed.** Files changed: `frontend/src/app/admin/settings/page.tsx`,
`frontend/src/app/admin/settings/LLMProvidersPanel.tsx` (new),
`frontend/src/app/admin/settings/LLMProviderKeysPanel.tsx` (new), `frontend/src/lib/api.ts`,
`frontend/src/lib/apiTypes.ts`.

**Not done / deferred (per the plan's own sequencing, not a regression):**
`them.tenant_system_agent_config` + classifier/card_synthesizer general/custom wiring — step 5,
now COMPLETE, see its own section above — and step 6's frontend switch on the role cards, still
not built. The platform-admin-on-tenant route mirror for keys
remains unbuilt (still not needed by anything).

---

## Tenant LLM Provider Keys — step 3: test-key + list-models endpoints — COMPLETE (2026-09-23)

Commit `6a6be2ca`. See `docs/TENANT_LLM_PROVIDERS_PLAN.md` for full detail. Summary:

New `LLMProviderKeysHandler` (`go/internal/admin/llm_provider_keys.go`) mounts 6 routes under
`/admin/my/llm-providers/{name}/keys...` (tenant self-service only, no platform-admin-on-tenant
mirror yet — not needed by anything today): list, create, patch (rename/rotate), delete,
set-default, test, plus `GET .../models?key_id=...` to refresh the live model list.

Every route resolves `{name}` to the caller's own tenant-scoped `them.llm_providers` row first, via
new `LLMProviderService.GetOwnProviderRow` — wraps the existing `GetProviderByNameForTenant` DAL
call (already tenant-scoped, already existed), translating `pgx.ErrNoRows` into a 404. **Never**
falls back to the platform row's id: a tenant that hasn't yet `PUT /my/llm-providers/{name}`'d to
create its own row gets 404 on every key route, not a key silently attached to the platform's row.
This is the concrete enforcement point for the "no platform-key fallback for tenants, ever" hard
rule, at the provider-resolution layer rather than only at LLM-call time.

New `go/internal/admin/llm_provider_models.go`: `fetchAnthropicModels`, `fetchOpenAICompatModels`
(covers openai/groq/any custom base_url), `fetchGeminiModels` — real `GET /v1/models`-equivalent
calls to each provider's actual API, dispatched by `listAvailableModels` the same way
`probeLLMWithBase` already dispatches test-key probes. The Test route reuses `probeLLMWithBase`
directly against the named key's own decrypted secret and the provider's `default_model`/`base_url`,
then records the outcome via the already-existing `LLMProviderKeyService.RecordTestResult`.

Tests: 2 new (`GetOwnProviderRow` — not-found and found-returns-tenant-row-not-platform-id), 6 new
for the models-fetch helpers (`httptest.Server`-backed, no real provider network calls), 6 new
handler tests (`LPK-01..06`, HTTP-layer 400/404 paths including the "unknown provider → 404, not a
misattributed key" case). All in `go/TEST_INDEX.md` as S1-135..137. `go test ./...` — 0 failures,
full suite (58 packages).

**Found but not fixed this session:** commit `465ffe93` (the prior session's DAL+service layer for
this same feature) added roughly 64 tests without a corresponding `go/TEST_INDEX.md` update — the S1
total stayed at 1349 across that commit despite `llm_provider_keys_test.go` (18 tests),
`llm_provider_keys_integration_test.go` (9 integration tests), and `allowed_models` cases folded into
`llm_providers_test.go`'s growth all landing in it. Flagged as an explicit "gap (465ffe93)" row in
`go/TEST_INDEX.md` rather than silently backfilled — reconstructing precise per-commit attribution
for a prior session's untracked additions was out of scope here. Worth a dedicated cleanup pass
before the running total is trusted as exact.

**Not done / deferred (per the plan's own sequencing, not a regression):** frontend UI (step 4 —
model checklist + refresh button + keys sub-section with add/rename/rotate/delete/set-default/test),
the platform-admin-on-tenant route mirror (optional; add later by mirroring
`LLMProviderKeysHandler.TenantScopedRoutes` the way `LLMProvidersHandler.TenantProviderRoutes`
already mirrors `TenantScopedRoutes`), and `them.tenant_system_agent_config` +
classifier/card_synthesizer general-vs-custom wiring (steps 5-6). Not deployed/rebuilt on this local
dev box this session — no container restart needed yet since nothing calls these new routes until
the frontend (step 4) exists.

---

## App Canvas Debug Mode — Phase 5, backend slice — COMPLETE (2026-09-23)

Commit `8269eae2`. Full design detail in `docs/APP_CANVAS_DEBUG_PLAN.md`'s "Phase 5 — design
decisions" section. Summary:

**Two prerequisite gaps found before any UI work could start** (neither the original plan nor
Phases 1-4 anticipated them):
1. Phase 2's `node_start`/`node_done`/`node_error` trace events were published to the run's Redis
   stream but **never reached a browser** — both `ws/handler.go`'s `writeEvent` and
   `sse/handler.go`'s `formatSSE` silently dropped (WS) or errored on (SSE) any event type they
   didn't already recognize.
2. **No run-trigger of any kind existed in the app-canvas UI**, debug or not — the only existing
   run-start paths (`ws/handler.go`, `sse/handler.go`) require a **published** entry point
   (`handle.EPConfig.ActiveDefinitionJSON`), but a canvas being debugged is normally an unpublished
   **draft**.

**Decisions made (confirmed with user):** debug runs the draft directly, no publish required —
publishing is a production/deployment step and must stay unrelated to debugging. A brand-new
dedicated route handles this, rather than adding a flag to the production WS/SSE routes, so
production request handling stays completely untouched.

**What was built:**
- `ws/handler.go`'s `writeEvent` and `sse/handler.go`'s `formatSSE` both gained a case for
  `node_start`/`node_done`/`node_error`, mapping the wire shape `{type, run_id, node_id, kind,
  detail?}` straight through — the actual production fix, independent of anything debug-specific.
- `execution.Lifecycle.AdmitDebug` (`internal/execution/lifecycle.go`) — a new sibling to `Admit`
  for admin-triggered debug runs: resolves `EPConfig` via the same real DB-backed `epLoader` (so
  tenant/app/EP ownership is still enforced — this is a capacity-admission bypass, not a security
  bypass), creates a real `them.runs` row via the recorder, but skips `gate.Check`/`Confirm` and
  `session.Register` entirely, since debug runs must never consume production admission capacity.
- New route `POST /admin/applications/{id}/debug/start` (`internal/admin/appflow_debug.go` →
  `internal/admin/service/appflow_debug.go` → `internal/admin/dal/definitions.go`'s new
  `GetLatestDraftDefinition`). Fetches the application's latest **draft** row (never
  `active_definition_id`), compiles + validates it via the same `appflow.Compile`/`Validate` calls
  `ws`/`sse` already use, then calls `AdmitDebug` + `StartAppFlow(debug=true)` — reusing
  `Lifecycle.StartAppFlow`'s existing debug-queue routing and forced-`full` log verbosity rather
  than duplicating Temporal dispatch logic.
- `admin.BuildRouter` gained a new `AppFlowDebugLifecycle` parameter (type alias for
  `service.AppFlowDebugStarter`) threaded from `cmd/them/main.go`'s existing `execLifecycle` — the
  admin router previously had no reference to `*execution.Lifecycle` at all.
- **Known limitation found while writing this slice's tests, documented in the plan doc:** a draft
  containing agent nodes can't be debugged until it's been **published at least once** —
  `appflow.ResolveAgentByInstanceID`'s `_resolved_agent_ids` stamp is only written at publish time.
  Flows made entirely of inline nodes (LLM, Condition, Router, etc.) are unaffected. Not fixed here
  — flagged for a later session.
- Tests: 12 new (S1-138..141 in `go/TEST_INDEX.md`, S1 total 1363→1375) — 2 wire-protocol
  forwarding tests (WS + SSE), 2 `AdmitDebug` tests, 5 `AppFlowDebugService.Start` tests (happy
  path, app-not-found, EP-slug-not-found, no-draft-saved, EP-missing-from-compiled-draft), 3
  `AppFlowDebugHandler` HTTP-mechanics tests. `go test ./...` — 0 failures, full suite, run twice.

**Not done — separate follow-up session:** the frontend UI (setup panel, Run All button,
`useAppFlowDebugSession` WS/SSE consumer hook, canvas node debug-state overlay). No live end-to-end
Temporal-workflow run was started against this new endpoint this session — verification was via
the service-layer tests calling `Start` directly with fakes, plus the wire-protocol tests proving
trace events now reach a WS/SSE client. Recommend a manual live check (same recipe Phases 1-4 used)
before fully trusting this against a real draft canvas.

---

## Security fix: cross-tenant IDOR on per-app config tables — COMPLETE (2026-09-23)

Commit `cc81978d`, pushed. Found during code review of Phase 4 below (not exploited in the wild,
caught before any real-world exposure was confirmed either way).

**The bug:** `GetAppLogVerbosity`/`UpsertAppLogVerbosity` (`them.app_debug_config`, new in Phase 4)
and `GetTemporalAppConfig`/`UpsertTemporalAppConfig` (`them.app_temporal_config`, older, the pattern
Phase 4 copied) took no tenant scoping at all. Neither table stores its own `tenant_id`, and no
ownership check existed anywhere in the handler → service → DAL chain — any authenticated tenant
admin could read or overwrite **another tenant's** trace-verbosity or Temporal execution settings
by guessing or knowing the target application's UUID. `RequireTenantAdmin` only checks the caller
has *some* tenant-admin role; it never constrained the `{id}` URL param to the caller's own tenant.

**The fix:**
- Both DAL methods now join to `them.applications` and require a `tenant_id` match. A cross-tenant
  call surfaces as `pgx.ErrNoRows` — mapped by the service layer (`ErrNotFound`) to HTTP 404, never
  data or a silent successful write.
- Handlers (`log_verbosity.go`, `temporal_config.go`) now read the caller's tenant from
  `tenantctx.MustTenantIDFromCtx(r.Context())` instead of trusting the URL alone, and validate `{id}`
  as a UUID up front via `uuid.Parse` — this also fixes a pre-existing bug where a malformed `{id}`
  returned 500 instead of 400 on the same code path.
- The one internal, non-HTTP caller (`Lifecycle.StartAppFlow`, via `TemporalConfigLoader` and
  `LogVerbosityLoader`) threads through the already-resolved, trusted tenant ID from
  `h.EPConfig.TenantID` — no new attack surface there since that path never took user input
  directly.
- **Verified against live Postgres, not just mocks:** 6 new integration tests
  (`go/internal/admin/dal/app_scoped_config_tenant_isolation_integration_test.go`) prove a genuine
  cross-tenant read and write are both rejected, including confirming an attacker's write attempt
  does not leak through and silently succeed. New handler-level tests
  (`go/internal/admin/config_handler_test.go`) cover the same 404 behavior over HTTP, plus the
  malformed-UUID-returns-400 fix.
- `go test ./...` — 0 failures, full suite, run twice for stability (one unrelated flaky timeout in
  `internal/a2a` under parallel-suite resource contention, reproduced as a one-off, confirmed clean
  in isolation and on a clean re-run — not caused by this change).

**Not done / not needed:** the same "no ownership check" class of gap may exist on other per-app
single-field config tables not touched here — this fix covered the two tables actually in scope
(the one just shipped in Phase 4, and the one it copied from). Worth a grep for other
`them.app_*_config`-shaped tables if a broader audit is ever wanted; not done as part of this fix.

---

## Tenant LLM Provider Keys — DAL + service layer — COMPLETE (2026-09-23)

Commit `465ffe93`, pushed. See `docs/TENANT_LLM_PROVIDERS_PLAN.md` for the full design — summary:

Adds `them.llm_provider_keys` (migration `db/105_llm_provider_keys.sql`) so a tenant can save
**multiple named API keys** per provider (not just the single `api_key_encrypted` column
`them.llm_providers` had room for), plus an `allowed_models` list on `them.llm_providers` so a
tenant can enable specific models independent of having a key saved yet. **Hard rule: no
platform-key fallback for tenants, ever** — if a tenant has no usable key, the feature simply
doesn't work for them.

**What landed:** DAL (`go/internal/admin/dal/llm_provider_keys.go`, `llm_providers.go` extended),
service layer (`go/internal/admin/service/llm_provider_keys.go`, `llm_providers.go` extended) — CRUD,
key masking, default-key swap, `run_usage` gets a new nullable FK column for per-key attribution.

**Not yet built (next steps per the plan doc):** test-key/list-models HTTP endpoints, the
classifier/card_synthesizer system-agent roles' general-vs-custom key picker wiring, frontend UI.

---

## App Canvas Debug Mode — Phase 4 (runtime log-verbosity setting) — COMPLETE (2026-09-22)

Commit `cda25d0c`, pushed. **Note:** this phase's own tenant-isolation gap was found and fixed
separately — see "Security fix" above; the `off`/`status`/`full` semantics described below are
unaffected by that fix, only the ownership-checking around them changed. See
`docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 4 section for full detail. Summary:

- **Open question resolved:** `off` = zero `them.run_steps` writes (full revert to Phase 2's
  live-Redis-only behavior for that run) — not a second "cheap" tier that still writes a row.
  `status` = minimal row (node_id/kind/status/latency, no output/error). `full` = today's Phase 3
  behavior (unchanged). Debug mode always forces `full` regardless of the app's setting.
- New table `them.app_debug_config` (migration `db/104_app_log_verbosity.sql`, applied to the live
  DB) — single-tier per-app setting, default `'status'`. Full Handler → Service → DAL stack added,
  mirroring the existing `app_temporal_config` pattern exactly (same file shapes, same validation
  helper, same route-mounting style). New admin routes:
  `GET`/`PUT /admin/applications/{id}/log-verbosity`.
- Resolved once per run in `Lifecycle.StartAppFlow` (new `LogVerbosityLoader` interface +
  `PgxLogVerbosityLoader`, mirroring `TemporalConfigLoader`), fail-open to the default on error,
  forced to `full` when `debug=true`. Threaded into `AppFlowWorkflowInput.LogVerbosity` and from
  there into every activity input struct and every `traceNode`/`emitTrace` call site (10 call
  sites across `workflow.go`, `graph.go`, `nodes.go`) — needed because the resolved value must
  cross the workflow→activity process boundary, unlike `Debug` which only ever needed to be read
  workflow-side.
- `persistTrace` branches on verbosity: `off` returns immediately (no DB call), `status` writes the
  row without output/error, `full` unchanged from Phase 3.
- New frontend Runtime tab "Trace Logging" (`RuntimeLogVerbosityTab.tsx`) — dropdown + inline
  description per level, mirrors `RuntimeTemporalTab.tsx`'s load/save structure.
- Tests: 5 service (LV-SVC-1..5), 5 handler (LV-1..5), 4 `Lifecycle.StartAppFlow` (S1-132..134 in
  `go/TEST_INDEX.md`, S1 total 1335→1349), 3 new integration tests (PT-5..7 in S2-12) — all run for
  real against the live `them-postgres` container on this box (joined via `them-network`, DSN read
  live from `them-dag-worker`'s own running env, never printed). `go test ./...` 0 failures, full
  suite. `tsc --noEmit` 0 errors.
- **Deployed:** `them-dag-worker`, `them-dag-worker-2`, `them-dag-worker-debug`, `them-go-bridge`,
  `them-frontend` all rebuilt and force-recreated on this local dev box; logs confirm healthy
  startup (dag-workers polling both queues, go-bridge answering `/health/live` 200, frontend
  serving `/login` 200), no crash loops.

**Not done / deferred (not a regression):** no live end-to-end Temporal-workflow run was started
this session to watch a real `off`/`status` run's rows (or lack thereof) land through the full
stack live — verification was via the integration test calling `persistTrace` directly (the same
function `emitTrace` calls internally) plus the full unit suite. Committed as `cda25d0c`, pushed.

---

## App Canvas Debug Mode — Phase 3 (durable trace storage) — COMPLETE (2026-09-22)

Commit `85a436bb`. See `docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 3 section for full detail. Summary:

- **Bug found and fixed first:** `them.run_steps.tool_call_id` was `TEXT NOT NULL` with no default,
  but its only writer (`internal/runrecorder.RecordAgentStep`) never included it in the INSERT —
  every orchestrator-mode step insert should have been failing this constraint against real
  Postgres. Not caught earlier because the unit test mocks the DB. Fixed by dropping the column
  (migration `db/103_run_steps_appflow_trace.sql`, applied to the live DB) — no reader depended on
  it beyond an always-empty display field. Full write-up: `docs/LESSONS.md`.
- Same migration adds nullable `node_id`/`node_kind` to `run_steps`, a default of `0` on
  `iteration` (AppFlow rows have no loop-iteration concept), and a partial unique index on
  `(run_id, node_id) WHERE node_id IS NOT NULL` so a node's `node_start` insert and later
  `node_done`/`node_error` update collapse into one row via `ON CONFLICT`.
- `go/internal/appflow/activities.go`: new `persistTrace` wired into `emitTrace` — the single choke
  point every node kind's trace event already flows through (Phase 2). Uses the existing Admin/
  BYPASSRLS pool (`a.DB`), same one `ExecuteHILActivity` already writes through — no RLS GUC
  handling needed, confirmed by dedicated research this session.
- `go/internal/admin/dal/` (`RunStep`, `GetRunDetail`) and `frontend/src/lib/apiTypes.ts`:
  `ToolCallID` → `NodeID`/`NodeKind`.
- `frontend/src/app/runs/runsTypes.ts`'s `buildGraph` now branches on "any step has a node_id" and
  renders AppFlow runs via new `buildDagGraph` (nodes as rows in `started_at` order, 1-second
  time-window grouping approximates parallel/fork rows) — reusing the existing row/parallel-row
  renderer rather than building a second visualizer. `RunGraph.tsx` gained a `dagnode` card kind.
  **Not a true branch/merge graph with drawn edges** — a heuristic ordering, flagged as a known
  limitation, not a structural fact (no explicit branch/group id exists in the trace data yet).
- Tests: `go/internal/appflow/trace_persist_integration_test.go` (new, `-tags=integration`, needs
  live Postgres since `persistTrace` calls a concrete `*pgxpool.Pool`) — S2-12 in
  `go/TEST_INDEX.md`, 4 tests, all passing against the live `them-postgres`. `go test ./...` 0
  failures, full suite (55 packages). `tsc --noEmit` 0 errors (no frontend test framework exists in
  this repo to add a unit test to).
- **Deployed:** `them-dag-worker`, `them-dag-worker-2`, `them-dag-worker-debug`, `them-go-bridge`,
  `them-frontend` all rebuilt and force-recreated on this local dev box; logs confirm healthy
  startup, no crash loops.

**Not done / deferred (not a regression):** no full live Temporal-workflow-through-WS round trip
was performed this session (unlike Phase 1/2) — the integration test calls `persistTrace` directly
with a real DB pool, proving the SQL/upsert logic, but a live workflow run writing a row through the
full stack hasn't been watched this session. Recommend a manual `temporal workflow start` check
(same recipe Phase 1/2 used) before fully trusting this in a truly live run. No per-app
log-verbosity setting yet (Phase 4) — every AppFlow run persists unconditionally right now, same as
Phase 2's live-emission behavior. `agent_id` on `run_steps` remains unpopulated by either writer —
noted, not addressed, out of scope for this phase.

---

## App Canvas Debug Mode — Phase 2 (live per-node trace events) — COMPLETE (2026-09-22)

Commit: `6104b349`. Full detail in `docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 2 section — summary:

- Every AppFlow node kind (Router, HIL, Agent, Inline LLM, Condition, Fork, Join) now publishes
  `node_start`/`node_done`/`node_error` to the run's existing Redis stream
  (`them:dash:run:{runID}:stream`), **unconditionally** — every run, debug or not, no app setting
  gates this yet (that's Phase 3/4's job). Scope was explicitly kept minimal per user direction:
  no per-node debug config, no `TraceMode`/redaction, no persistence, no full prompt/input/output
  capture — just enough small kind-specific detail (condition's chosen branch, router's chosen
  label, fork's branch count, HIL's approval outcome) to follow a run's actual path live.
- **Architecture was investigated, not assumed**, per explicit user request: evaluated Temporal
  Queries (pull-only, can't push live), Updates (wrong tool — solves synchronous external
  mutation), workflow memo/search attributes (visibility metadata, not a live feed), and
  `workflow.SideEffect` (makes a value replay-safe, not an I/O mechanism) — all rejected. A small
  trace-only Activity (`AppFlowTraceNodeEventActivity`) is the only Temporal-native way to publish
  an I/O-free workflow decision (Condition/Fork/Join) live; Router/HIL/Agent/Inline LLM already do
  I/O in their own activities, so they publish inline instead via a shared `emitTrace` helper.
- HIL is special-cased: `ExecuteHILActivity` only emits `node_start` — the approval outcome is
  only known later in `execHILNode` (`nodes.go`) after the signal/timer resolves, so that function
  calls the workflow-side `traceNode` helper directly once the decision is in.
- The join node's own trace fires from the `"fork"` case in `workflow.go`, once, after
  `wg.Wait()` — **not** from inside `walkBranch` (`graph.go`), since each fork branch's loop stops
  as soon as it reaches the join node and never actually visits it.
- `go/cmd/dag-worker/main.go`: new activity registered on the AppFlow worker; rebuilt and
  force-recreated `them-dag-worker`, `them-dag-worker-2`, and `them-dag-worker-debug` (Dockerfile
  runs `go test ./...` at build time — 0 failures confirmed again in-image). All three confirmed
  healthy and polling their correct queues post-restart.
- `go test ./...` 0 failures (full suite). 10 new tests (S1-130, S1-131 in `go/TEST_INDEX.md`; S1
  total 1325→1335): 7 activity-level (`workflow_test.go`), 3 workflow-level using a new
  `testsuite.WorkflowTestSuite` (`workflow_temporal_test.go`) — needed because `traceNode` calls
  `workflow.ExecuteActivity`, which can't be exercised by activity-level mocking alone.
- `docs/REDIS.md` updated: added the previously-undocumented `them:dash:run:{run_id}:stream` key
  (a pre-existing gap, not introduced this session) with the new event types noted. Confirmed and
  documented: unknown `type` values are silently ignored by both `sse/handler.go` and
  `ws/handler.go` (fail-open) — new types are safe to ship with zero consumer changes.
- **Live-verified on this local dev box**, not just unit tests: started a real `AppFlowWorkflow`
  via the `temporal` CLI with a Condition node, then `XRANGE`'d the run's actual Redis stream —
  confirmed `node_start`/`node_done` entries with the exact expected shape
  (`{"kind":"condition","node_id":"cond1",...,"detail":"branch=true"}`), followed by the normal
  `done` event.

**Not done / explicitly deferred (not a regression):** no persistence (Phase 3), no app-level
log-verbosity setting to gate anything (Phase 4), no UI consumption of these events yet (Phase
5/6). No per-node debug config (`DebugConfig`/`TraceMode`/redaction) — discussed and explicitly
deferred by the user; if it resurfaces, the candidate shape discussed was one generic field on
`AppFlowNode` (e.g. `TraceMode: ""|"full"|"redacted"|"off"`), not a per-field redaction schema.

---

## App Canvas Debug Mode — Phase 1 (debug worker pool + routing) — COMPLETE (2026-09-22)

Commit: `865ad388`. Full detail in `docs/APP_CANVAS_DEBUG_PLAN.md`'s Phase 1 section — summary:

- New Temporal task queue `appflow.AppFlowDebugTaskQueue = "appflow-dag-debug"`
  (`go/internal/appflow/workflow.go`), alongside the existing `AppFlowTaskQueue`.
- `AppFlowWorkflowInput.Debug bool` — new field. `AppFlowWorkflow` now dispatches **all** its
  activities (`shortAO` for finalize/HIL, `ao` for the main node walk) to the debug queue when
  set, via new pure helper `activityTaskQueueFor(debug bool)`.
- `Lifecycle.StartAppFlow` (`go/internal/execution/lifecycle.go`) signature changed — now takes
  `debug bool`, selects the Temporal `StartWorkflowOptions.TaskQueue` and sets `input.Debug`
  accordingly. **Both existing call sites** (`internal/ws/handler.go`, `internal/sse/handler.go`)
  updated to pass `debug=false` explicitly — zero behavior change for any app running today, since
  no entry point sets `true` yet.
- New env var `APPFLOW_TASK_QUEUE_OVERRIDE` (`go/internal/config/config.go`,
  `go/cmd/dag-worker/main.go`) — when set, the AppFlow worker in `cmd/dag-worker` polls that queue
  instead of the production one. Same binary/image, no build-time distinction. The worker's
  separate `canvas-dag-nodes` registration (agent-builder Temporal path) is unaffected by this
  var — every dag-worker container, including the debug one, still also polls
  `canvas-dag-nodes` (harmless idle capacity; that path has no debug queue, out of scope).
- New service `them-dag-worker-debug` in **both** `docker-compose.dev.yml` and
  `docker-compose.hetzner.yml` — same `Dockerfile.dag-worker` image,
  `APPFLOW_TASK_QUEUE_OVERRIDE=appflow-dag-debug` set. Both compose files validated with
  `config --quiet` (0 errors). Hetzner block has no RLS DSN vars, same pre-existing gap already
  flagged for `them-dag-worker`/`-2` there — not newly introduced, not fixed here either.
- `go test ./...` 0 failures (full suite, via `docker run golang:1.25-alpine` — no local Go
  toolchain on this box). 3 new tests: `TestActivityTaskQueueFor`
  (`internal/appflow/workflow_test.go`), `TestLifecycle_StartAppFlow_NotDebug_UsesProductionQueue`
  + `TestLifecycle_StartAppFlow_Debug_UsesDebugQueue` (`internal/execution/lifecycle_test.go`).
  `go/TEST_INDEX.md` updated (S1-128, S1-129; total 1322→1325).
- **Live-verified end-to-end on this local dev box:** built and started `them-dag-worker-debug`;
  logs confirmed `"appflow-worker polling" task_queue=appflow-dag-debug`. Used the `temporal` CLI
  (already present in the `temporal-admin-tools` container) to start an `AppFlowWorkflow`
  directly on `appflow-dag-debug` with `"debug":true` in its input. Confirmed via `docker logs`
  that only `them-dag-worker-debug` executed any activity for that workflow ID — `them-dag-worker`
  and `them-dag-worker-2` show no entries for it at all. The test workflow was deliberately given
  an invalid empty `run_id` so it would fail fast in `FinalizeRunActivity` without needing a real
  `them.runs` row — that failure is expected and irrelevant to what the test proves (queue
  routing, not full run correctness).

**Deployment state:** `them-dag-worker-debug` is built and running on this local dev box
(profile `temporal`, part of the normal `--profile temporal up -d` set going forward). The
Hetzner compose addition is config-only — **not built or deployed to the actual Hetzner host**,
consistent with how `them-dag-worker`/`-2` were handled there in the prior session.

**Not done / explicitly deferred to a later phase (not a regression):**
- No WS/HTTP entry point sets `debug=true` yet — that's Phase 5/6 (the setup panel + Run
  All/Step controls need to exist before there's a reason to flip the flag from the UI).
- No per-node trace events exist yet — that's Phase 2, and it has one explicit open question
  (unconditional vs debug-gated emission) flagged in the plan doc, not yet decided.
- Not pushed to `origin/main` — no push credentials confirmed available this session; push
  manually or confirm credentials before starting Phase 2 if remote sync matters.

---

**What this session did (2026-09-22), most recent first:**
- Fixed a real bug: Condition node's true/false handles overlapped in horizontal ("Graph") layout
  — spread logic used `left: X%` unconditionally, which has no effect on a Position.Right handle.
  Fixed in `CanvasNodes.tsx`. Removed the fully-dead "AI Advisor" button + its orphaned backing
  code (`AdvisorPanel.tsx` deleted — never imported anywhere, no backend endpoint existed for it).
- Renamed the app-canvas execution-mode dropdown "Local (Orchestrator)"/"Temporal DAG" →
  "Simple (Orchestrator)"/"Graph (Canvas Flow)" — the old names implied a non-Temporal path that
  doesn't exist; both modes run on Temporal. Display-text-only, confirmed safe (no code branches
  on the English strings).
- Added a second Temporal worker replica for the Graph engine (`them-dag-worker-2`) in dev, then
  discovered and fixed a bigger gap: **`docker-compose.hetzner.yml` had no `them-dag-worker` at
  all** — the Graph engine had never been deployable to production. Added both
  `them-dag-worker`/`them-dag-worker-2` there (config only, not deployed/built on this box).
- Found and fixed a live bug while testing: `them-agent-runtime` (hosts canvas-built A2A agents)
  was in a silent crash-restart loop — missing `THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN` env vars,
  present on every other Go service but this one. Fixed in `docker-compose.yml` (covers Hetzner
  too, which has no override for this service).
- Built app-canvas Export/Import JSON (mirrors the agent builder's existing feature) — pure
  frontend, no backend endpoint, deeper client-side shape validation than the agent builder's
  precedent. Split the oversized `CanvasBuilderView.tsx` (754 lines) into `CanvasTopBar.tsx` +
  `CanvasPalette.tsx` first, per the file-size rule, before adding the new feature.
- Verified live, end-to-end, via hand-built JSON pushed through the real API (not just unit
  tests): a Simple-mode app (Entry Point → Orchestrator → Agent) and a Graph-mode app (LLM node →
  Condition → branch → Agent) both ran successfully over a real WebSocket connection. Both test
  apps (`stage1-import-test`, `stage2-graph-llm-condition-v2`) are still live in the `default`
  tenant for manual UI inspection.
- Wrote `docs/APP_CANVAS_DEBUG_PLAN.md` — a 6-phase plan for real (non-simulated) per-node debug
  tracing for Graph-mode runs, with a separate Temporal worker pool for debug traffic. 5 design
  decisions confirmed with the user; 1 still open (see the plan doc's "Remaining open question").
  **This is the next body of work — start with Phase 1.**

**Known UI bug, not yet fixed, low priority:** application cards on the Applications list
overflow/don't reflow correctly — only fully visible by browser zoom-out. Found this session,
not investigated or fixed. Worth a look, not urgent.

**Deployment state:** all fixes above are live on this local dev box (`them-frontend`,
`them-go-bridge`, `them-agent-runtime`, `them-dag-worker-2` all rebuilt/force-recreated and
healthy). The two Hetzner compose additions (`them-agent-runtime` env vars via the shared base
file, `them-dag-worker`/`them-dag-worker-2`) are config-only — **not yet built or deployed to the
actual Hetzner host.**

---

## Bug fix: them-agent-runtime crash loop — THEM_DB_URL_APP missing — COMPLETE (2026-09-22)

Found while testing a Graph-mode app (LLM → Condition → canvas-built agent) end-to-end:
`them-agent-runtime` (both replicas, `docker-compose.yml`) was in a silent crash-restart loop —
`docker ps` showed "Restarting (1)" — because its `environment:` block never had
`THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN` set. Every other Go service in this repo already has them
(`them-go-bridge`, `them-go-worker(-2)`, `them-dag-worker(-2)`); this one was missed. Since the RLS
closure work (`go/CLAUDE.md` Step H2) made these hard requirements — every Go binary fails fast at
startup without them — this container has presumably been broken since that landed, not something
introduced this session. It went unnoticed until now because nothing in the Graph-mode test suite
so far actually invokes a **canvas-built** A2A agent (the `echo_agent` kind, hosted on
`them-agent-runtime`) — `test_42` and `test_40` both use `a2a_echo`, a separate standalone
test-agent container with no such dependency.

Fixed by adding the same two env lines to `them-agent-runtime`'s block in `docker-compose.yml`.
Rebuilt via `--force-recreate`; both replicas now report healthy. Confirmed live: a
Graph-mode app calling a canvas-built agent (`echo_agent`) now reaches it over the network
successfully (previously: DNS lookup failure, since Compose only creates a service-DNS entry for
running containers) — though that specific agent then returned HTTP 401 for an unrelated reason
(likely a canvas-agent invocation-auth requirement not wired up in this test — not investigated
further, since the goal was confirming the network/crash-loop fix, and the standalone `a2a_echo`
agent (no auth) worked end-to-end once substituted). **Flagged, not chased down:** why `echo_agent`
specifically 401s on A2A invocation from the dag-worker — worth a dedicated look before relying on
canvas-built agents in a Graph-mode flow.

**Hetzner:** `docker-compose.hetzner.yml` has no `them-agent-runtime` override — it inherits the
service definition entirely from the base `docker-compose.yml`, which is the file this fix landed
in. Production is covered by this same fix, no separate change needed.

---

## App canvas: "Simple"/"Graph" rename + second DAG worker replica — COMPLETE (2026-09-22)

Two small, unrelated changes made while testing the new app-canvas export/import feature together
with the user:

**Execution-mode rename** (`frontend/src/app/admin/applications/components/CanvasTopBar.tsx`,
lines 82-83, 127-128): the dropdown options "Local (Orchestrator)" / "Temporal DAG" and the
matching warning-banner text renamed to "Simple (Orchestrator)" / "Graph (Canvas Flow)". Both
names were confusing — "Local" implied a non-Temporal execution path that doesn't exist (the
classic Orchestrator runs on `them-go-worker`, a Temporal worker, exactly like the graph path
runs on `them-dag-worker` — there is no non-Temporal code path anywhere in this system). Confirmed
by research before renaming: this is the *only* place in the codebase these English display
strings appear; every comparison in frontend and Go code branches on the short stored values
`"local"`/`"temporal"` only, which are **unchanged** — this is a display-text-only rename, safe
by construction. `tsc --noEmit` 0 errors; `them-frontend` rebuilt and force-recreated.

**`them-dag-worker-2` replica added** (`docker-compose.dev.yml`): a second instance of the same
`Dockerfile.dag-worker` image, same `profiles: [temporal]` gate, polling the same two Temporal
task queues (`canvas-dag-nodes`, `appflow-dag`) as the primary — mirrors the existing
`them-go-worker`/`them-go-worker-2` pattern. Temporal's SDK distributes individual activities
(single node executions) across whichever of the two workers is free; this is standard Temporal
worker-pool behavior, not new code. Built, started, and confirmed polling both queues via
container logs. `test_42_appflow_inline_nodes.py` 29/29 with both replicas running (extra
confidence this doesn't break anything, not just a formality — proves a run's activities complete
correctly with two workers competing for the same queue).

**Trigger map updated** (`CLAUDE.md`, `go/CLAUDE.md`): any restart of `them-dag-worker` after
editing `internal/temporal/`, `cmd/dag-worker/`, or `internal/appflow/activities.go` must now also
restart `them-dag-worker-2`.

**`docker-compose.hetzner.yml` — UPDATE (2026-09-22):** turned out to be a bigger gap than
expected — `them-dag-worker` didn't exist in the production overlay **at all**, not just missing
its replica. The Graph/canvas-flow execution engine had never been deployed to production;
Hetzner only ever ran the classic Orchestrator (`them-go-worker`/`them-go-worker-2`). Added both
`them-dag-worker` and `them-dag-worker-2` to `docker-compose.hetzner.yml`, mirroring the
`them-go-worker`/`them-go-worker-2` env-var style already used in that file (`profiles:
[temporal]`, same `depends_on` health-check pattern). Validated with `docker compose ... config
--quiet` against the base `docker-compose.yml` + this overlay — 0 errors, both services confirmed
present via `config --services`. **Not built or started** — this box is local dev, not the
Hetzner host; actually deploying this is its own explicit production-deployment step, not done
here. **Pre-existing gap noted, not fixed:** neither `them-go-worker(-2)` nor the new
`them-dag-worker(-2)` entries in this file set `THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN` — the RLS
role DSNs `docs/CURRENT.md`'s "Step H2 — RLS Closure" section says are required (binaries fail
fast without them). Confirmed via grep: **zero** occurrences of either var anywhere in
`docker-compose.hetzner.yml`, for any service — this predates today's change and affects the
whole file equally, so it was flagged rather than silently fixed as a side effect of this task.

---

## Tenant LLM Self-Service + Platform Admin LLM Key Management — COMPLETE (2026-09-22)

Commit: `970b0cfb`. Tenant admins can now manage their own LLM provider API keys from the Settings page. Platform admins (super_admin) can manage the-M platform-level keys from the same page.

**What was built:**

- `go/internal/admin/llm_providers.go` — added `TenantScopedRoutes` with `GET /admin/my/llm-providers` and `PUT /admin/my/llm-providers/{name}`. Uses `tenantctx.MustTenantIDFromCtx` to scope to the caller's tenant. No super_admin required.
- `go/internal/admin/router.go` — mounted `TenantScopedRoutes` in the `tenantScoped` group.
- `go/internal/admin/llm_providers_test.go` — 2 new tests (LLP-TS-01..02): ListMine empty 200, UpsertMine invalid body 400.
- `frontend/src/app/admin/settings/page.tsx` — added "LLM Providers" tab:
  - tenant admin: calls `listMyLLMProviders` / `upsertMyLLMProvider` (own tenant)
  - super_admin: calls `listPlatformProviders` / `patchPlatformProvider` (platform-level)
  - shows enabled/disabled badge, masked key status, per-provider API key password input + Save button
- `frontend/src/lib/api.ts` — added `patchPlatformProvider`, `listMyLLMProviders`, `upsertMyLLMProvider`.
- `frontend/src/types/auth.ts` — added `tenant_id?: string` to `TheMUser` (auth `/me` already returns it).

**Navigation:** Settings → LLM Providers tab (visible to all authenticated admin users).

**Hard constraint still in effect:** Platform LLM API keys MUST NOT be used as fallback for tenant LLM calls. See `go/internal/llmresolve/llmresolve.go`.

**Next recommended tasks:**
1. Test: log in as tenant admin (payops_ai), go to Settings → LLM Providers, set an OpenAI key, verify it's saved.
2. Test: log in as super_admin, go to Settings → LLM Providers, verify platform-level keys show.
3. Gateway Phase 4 — profile pipeline steps (PII filter, cache, guardrails). Deferred — canvas session needs to stabilize first.

---

## Tenant LLM Provider Management — COMPLETE (2026-09-21)

Commit: `ecbd63fe`. Frontend + Go llmresolve change. No new migrations needed (uses existing `llm_providers.tenant_id` from migration 057 and existing DAL/service/routes).

**What was built:**

- `go/internal/llmresolve/llmresolve.go` — platform key fallback REMOVED. `ResolveProvider` now uses app-level key → tenant-level key only. Platform key is never used for tenant calls. base_url and pricing metadata still fall back to the platform row.
- `go/internal/llmresolve/llmresolve_integration_test.go` — updated `TestResolveProvider_NoPlatformKeyFallback` (was `FallsBackToPlatformWhenNoAppOrTenantKey`) to assert empty key + platform metadata available.
- `frontend/src/app/admin/tenants/page.tsx` — added "LLM Providers" tab to the tenant side panel (platform-admin view). Shows all providers with key status (`own key set` / `no key`), masked current key, and per-provider API key input + save.
- `frontend/src/lib/apiTypes.ts` — added `LLMProviderOut`, `LLMProviderUpsertInput`.
- `frontend/src/lib/api.ts` — added `listPlatformProviders()`, `listTenantProviders(tenantId)`, `upsertTenantProvider(tenantId, name, body)`.

**Hard constraint recorded:** Platform LLM API keys MUST NOT be used as fallback for tenant LLM calls. Tenants pay for their own usage. See `go/internal/llmresolve/llmresolve.go` comment.

**Next recommended tasks:**
1. Test the LLM Providers tab in the tenant panel — log in as super_admin, open Tenants, select a tenant, click "LLM Providers", set a key.
2. Gateway Phase 4 — profile pipeline steps (PII filter, cache, guardrails) wired into execution. Deferred — needs File Guard and parallel canvas session to stabilize first.
3. Canvas Node Registry Phase 6+ (if needed).

---

## Parallel design track — LLM Gateway (design only, nothing implemented)

**`docs/LLM_GATEWAY_DESIGN.md`** — committed `6853539c`. Design for governing **closed agents**
(cron jobs, scripts, internal services that expose no endpoint, so the-M cannot call them and
cannot register them in `them.agents`). They all call an LLM, so the-M proxies that call and
gains identity, tokens, cost, latency, audit and policy for agents it does not run.

This is a separate track from Inline Nodes below. **Do not start it in the same session as
Inline Nodes frontend work.**

### Decisions already settled (do not re-litigate)

- One ingress, OpenAI request shape, all providers behind it (`model` field selects the target)
- Stateless — LLM APIs resend full history; scale horizontally, no sessions
- New tables (5); `runs`/`run_usage` deliberately NOT reused — a closed agent has no
  orchestrator/session/goal to put in them
- Per-agent policy via components → profiles → assignment, reusing `middleware_defs`
- Text capture IS in scope, opt-in per client, encrypted + retention job in the same phase
- In-process pipeline, NOT Temporal (per-call latency)
- Lives inside `them-go-bridge`; keep code in `internal/llmgateway` so it can be split out later

### Phase 3 — COMPLETE (2026-09-19)

Admin CRUD backend + frontend UI. Commit: `52fcec69`. `go test ./...` — 1298 tests, 0 failures.

**What was built:**

- `go/internal/admin/dal/gateway.go` — DAL for all 5 gateway tables (clients, profiles, profile steps, policy, requests).
- `go/internal/admin/gateway.go` — `GatewayHandler` with 15 routes mounted in tenantScoped group.
  - `POST /admin/gateway/clients` — generates 32-byte random token, stores sha256 hash, returns token once.
  - `GET /admin/gateway/policy` — returns allow-all zero row when no policy row exists (fail-open).
  - `PUT /admin/gateway/policy` — upserts policy then returns current state.
- `go/internal/admin/router.go` — `gatewayAdmin.Routes(tenantScoped)` wired.
- `frontend/src/app/admin/gateway/page.tsx` — 4-tab UI (Clients, Profiles, Policy, Requests).
  - Clients tab: create with token reveal (one-time), delete.
  - Profiles tab: create, delete.
  - Policy tab: allowed models textarea, aliases JSON editor, max_tokens + monthly_budget inputs.
  - Requests tab: summary stats + request log table.
- `frontend/src/components/Sidebar.tsx` — "LLM Gateway" entry added after Agents in BUILD_TEST_NAV.
- `frontend/src/lib/api.ts` + `apiTypes.ts` — types + API methods for all gateway endpoints.
- `go/internal/admin/gateway_test.go` — 11 handler tests GW-ADM-01..11.
- `go/TEST_INDEX.md` — S1-121 added, S1 total 1298.

**Not yet wired:** text capture (request body storage in `gateway_request_bodies`) — deferred.
**Not yet wired:** File Guard pipeline steps (requires ApplicationID on gateway calls — deferred).

**Next recommended task:** rebuild + redeploy `them-go-bridge` to pick up the new admin routes, then verify via the UI.

---

### Phase 2 — COMPLETE (2026-09-19)

Governance enforced inside `internal/llmgateway` (no new tables, no new containers).
Commit: `936ae696`. `go test ./...` — 1287 tests, 0 failures.

**Decision recorded:** gateway spend does NOT count against `monthly_llm_tokens` (the hosted-agent token budget). The two budgets are independent. Rationale: `monthly_llm_tokens` is denominated in tokens for hosted runs; a cron job's budget is denominated in USD and must not silently exhaust the budget hosted apps depend on. See §14 open question — resolved as "no".

**What was built:**

- `go/internal/llmgateway/dal.go`:
  - `Policy` struct (`AllowedModels`, `ModelAliases`, `MaxTokensPerRequest`, `MonthlyBudgetUSD`).
  - `DAL.LoadPolicy(ctx, tenantID) (*Policy, error)` — reads `gateway_policies`; returns `nil, nil` on no row (allow-all).
  - `DAL.SumMonthlySpend(ctx, tenantID) (float64, error)` — `SUM(cost_usd)` from `gateway_requests` current calendar month.

- `go/internal/llmgateway/service.go`:
  - `PolicyEnforcer` interface (`LoadPolicy`, `SumMonthlySpend`).
  - `ErrModelBlocked` (→ HTTP 403), `ErrBudgetExceeded` (→ HTTP 429).
  - `Service.WithPolicyEnforcer(pe PolicyEnforcer) *Service` — fluent wiring.
  - `checkPolicy(ctx, tenantID, requestedModel) (resolvedModel, tokenCap, err)` — one DB call; applies aliases before allowlist check; budget fail-open on DB error (§16.2).
  - `applyTokenCap(requested, cap int) int` — clamps `max_tokens` to policy ceiling.
  - `Call` and `Stream` both run `checkPolicy` as step 0 (before quota check, before LLM call). `ModelServed` in `CallResult` is the post-alias model.

- `go/internal/llmgateway/handler.go`:
  - `callStatus(err)` maps errors to gateway_requests.status strings (`"blocked"`, `"rate_limited"`, `"error"`).
  - `gatewayErrType(err)` maps errors to JSON `error.type` strings for clients.
  - `gatewayHTTPStatus` extended: `ErrModelBlocked` → 403, `ErrBudgetExceeded` → 429.

- `go/cmd/them/main.go`: `gwSvc.WithPolicyEnforcer(gwDAL)` — production wiring.

- `go/internal/llmgateway/gateway_test.go`: 8 new tests GW-POL-01..08.

- `go/TEST_INDEX.md`: S1-120 added, S1 total 1287.

**No DB migration needed** — `gateway_policies` table already exists from Phase 1 (`db/100_llm_gateway.sql`). A tenant with no row in `gateway_policies` gets allow-all behaviour.

**Phase 3 COMPLETE.** See above. Next: rebuild + deploy `them-go-bridge`, then verify admin UI at `/admin/gateway`.

---

### Phase 1 — COMPLETE (2026-09-19)

Migration `db/100_llm_gateway.sql` applied to live DB (6 tables: gateway_clients,
gateway_requests, gateway_profiles, gateway_profile_steps, gateway_policies,
gateway_request_bodies; RLS + GRANTs). New package `internal/llmgateway` (handler →
service → DAL). Route `POST /{tenant_slug}/llm/v1/chat/completions` mounted via
`server.MountGateway` (exact Post() — not Mount("/"), per A2A outage lesson). 25 tests
(all pass). Container redeployed; logs confirm: `"LLM gateway mounted"`. Commit: `4a9402e7`.

**Hard constraints fixed in Phase 1 (do not re-litigate):**
- `token_hash TEXT` (sha256-hex) used as identity link in gateway_clients — NOT FK to
  access_tokens.id because access_tokens.id is UUID but TokenInfo.TokenID is int64 (user_id)
- `server.MountGateway` uses `router.Post("/{tenant_slug}/llm/v1/chat/completions", h.ServeHTTP)`
  — Mount("/") causes chi catch-all conflict (commit 7e9b7b1 A2A outage)
- `checkQuota` translates only `quota.ErrAPIRateLimited` → `ErrQuotaExceeded` (§16.2 fail-open:
  any other error from Redis/quota = allow through)
- `WriteRequest` uses Admin pool (BYPASSRLS) — tenant_id enforces isolation in the table

**Open question RESOLVED (Phase 2):** gateway spend does NOT count against `monthly_llm_tokens`. The two budgets are separate: `monthly_llm_tokens` is runs-only; `gateway_policies.monthly_budget_usd` is the sole spend cap for gateway traffic. `SumMonthlyTokens` was not changed.

---

### Phase 0 — COMPLETE (2026-09-19)

Fixed metering so the gateway will report correct numbers from its first request. All items
verified against the live DB. `go test ./...` — 0 failures (S1 total 1254, S2 total 59, see
`go/TEST_INDEX.md` S1-116..118, S2-10, S2-11).

1. **`SumMonthlyTokens` column bug fixed** (`internal/admin/dal/runs.go`) — `runs.created_at` →
   `runs.started_at`. The `monthly_llm_tokens` quota now actually enforces. Regression test:
   S2-10 (`internal/admin/dal/runs_integration_test.go`).
2. **OpenAI-compatible `stream_options.include_usage` added** (`internal/llm/openai.go`) — every
   streamed request now asks for usage accounting, so OpenAI/Groq/Ollama/vLLM report real token
   counts instead of always 0. Test: S1-116.
3. **Cost now sourced from `them.llm_providers.model_pricing`** — new `orchestrator.CostEstimator`
   interface + `Orchestrator.WithCostEstimator()` (`internal/orchestrator/orchestrator.go`);
   `internal/llmresolve.PricingTable` implements it and is loaded once per run in
   `workerconfig.RunConfig.LLMPricing` (same DB row already fetched for the API key/base_url —
   no extra query). Falls back to the old hardcoded Claude-only rate card
   (`internal/orchestrator/pricing.go`) when DB pricing has no entry for the model. Wired in
   `cmd/worker/main.go`'s `runOrchestratorFactory.Build`. Tests: S1-117, S1-118, S2-11.
4. **`internal/llmresolve` extracted** — new package holding the app → tenant → platform provider
   key/base_url/pricing precedence chain, previously duplicated (and drifted) between
   `internal/temporal/workerconfig/loader.go` and `cmd/dag-worker/main.go`'s `dbLLMCaller.resolveKey`.
   Both now call `llmresolve.Resolver.ResolveProvider`. `DecryptValue` fails loudly (returns an
   error) instead of silently returning ciphertext when no Fernet key is configured — the
   original bug in `loader.go:505`. Tests: S1-118, S2-11.

**Behavior changes made while unifying the two duplicate implementations (confirmed with the
user, not silent):**
- **Precedence order is now app-key → tenant-key → platform-key everywhere.** Before this
  change, `workerconfig` (Temporal worker, `cmd/worker`) checked tenant-key → app-key with
  **no platform fallback**, while `cmd/dag-worker` checked app-key → tenant-key → platform-key.
  The user chose dag-worker's order as the standard. Net effect: `cmd/worker` gained a platform
  fallback it didn't have, and an app-level key now beats a tenant-level key there (previously
  the reverse).
- **Security fix:** the app-level `provider_keys` lookup in `workerconfig/loader.go` was
  missing the `tenant_id` filter (`WHERE id = $1::uuid`, no tenant check) — `dag-worker` and
  `cmd/agent-runtime` already had it. `llmresolve.AppProviderKey` now requires `tenant_id` on
  every app-level lookup. Regression test: S2-11 (`TestAppProviderKey_CrossTenantAppID_ReturnsEmpty`).
- **`"plain:"` test-mode prefix bug found and fixed in the merge:** `workerconfig/loader.go`'s
  original `loadProviderKey` never handled the `"plain:"` prefix (written by
  `internal/admin/service/applications.go` `encryptKey` when no crypto key is configured) —
  `dag-worker` and `agent-runtime` did. Unnoticed until now because `workerconfig` always had a
  real Fernet key in practice. `llmresolve.ParseAppProviderKey` now handles it uniformly.

**Known gap — NOT fixed, deliberately deferred (confirm before touching):**
`cmd/agent-runtime` (`spec.go` `loadAppAPIKey`, `llm.go` `multiLLMFactory`) still has its own
**third, narrower** key-resolution path: app-level `provider_keys` → a single hardcoded platform
key (no tenant-scoped `them.llm_providers` fallback at all). It was **not** rewired to
`internal/llmresolve` in this phase — the user asked to scope Phase 0 to unifying the two worker
paths only; upgrading agent-runtime's precedence chain is a behavior change to a third subsystem
and needs its own explicit decision. If a tenant sets an OpenAI/etc. key only at the tenant
level (not per-app), agents run via `agent-runtime` will not find it today.

Phases 1–6 are in `docs/LLM_GATEWAY_DESIGN.md` §14. Phase 1 is COMPLETE (see above).
**Next recommended task: Phase 2 — governance** (new session).

**Open question RESOLVED (Phase 2):** gateway spend does NOT count against `monthly_llm_tokens`. Budgets are separate.

---

## Current migration slice

**Node Registry unification — `docs/NODE_REGISTRY_PLAN.md`. Phases 1-5 COMPLETE. Plan closed.**

Goal: a node is defined once; every canvas reads it. Today the same concept exists three ways —
agent-builder nodes in a code registry, app-canvas nodes hardcoded in 6 places, middleware in the
DB table `them.middleware_defs`. They will drift permanently unless unified.

| Phase | What | Status |
|---|---|---|
| 1 | Runtime split — provider/model off the canvas into the Runtime screen | ✅ COMPLETE (2026-09-19) |
| 2 | Extract shared registry into `internal/nodedefs` | ✅ COMPLETE (2026-09-19) |
| 3 | Register the 6 app-canvas nodes (llm, condition, router, hil, fork, join) | ✅ COMPLETE (2026-09-19) |
| 4 | App canvas renders from the registry (copy `StepNode.tsx`) | ✅ COMPLETE (2026-09-21) |
| 5 | Middleware adopts the node contract — stays in DB, gains edges/ports/config_fields | ✅ COMPLETE (2026-09-21) |

**Phase 2 — COMPLETE (2026-09-19).** New package `go/internal/nodedefs` holds the portable node
metadata (`Meta` struct: label/description/emoji/color/bg_color/edges/ports/config_fields/
usage_notes/examples) that was previously declared directly on `agentgen.NodeDef`. `NodeDef` and
`NodeTypeInfo` now embed `nodedefs.Meta`; `agentgen` keeps `ConfigFieldDoc`/`NodeExample`/
`PortDef`/`EdgeRules`/`Meta` as type aliases so no external caller changed. All 12
`RegisterNode(...)` call sites in `nodes.go`, plus 57 test-file `NodeDef{}` literals across
`local_executor_test.go` and `canvas_workflow_test.go`, were updated to nest the moved fields
under `Meta: nodedefs.Meta{...}` — values unchanged. `go test ./...` 0 failures (all packages);
`/admin/node-types` payload verified semantically identical before/after (parsed-JSON equality;
raw JSON key order changed because Go serialises embedded fields first — flagged as a deviation
from the plan's literal "byte-identical" gate, not a functional regression). No frontend files
touched (backend-only phase); `tsc --noEmit` 0 errors. Full detail in `docs/NODE_REGISTRY_PLAN.md`.

**Phase 3 — COMPLETE (2026-09-19).** New file `go/internal/appflow/noderegistry.go` —
`AppCanvasNodeInfo` (field-shape match with `agentgen.NodeTypeInfo`) + static registry for the 6
app-canvas kinds (`llm`, `condition`, `router`, `hil`, `fork`, `join`), sourced from
`CanvasNodes.tsx`'s existing `INLINE_META`/`FC_META` values, `validate.go`'s structural edge
rules, and `RouterConfig`/`HILConfig`/`InlineLLMConfig`/`InlineConditionConfig`'s real fields.
`go/internal/admin/node_types.go`'s `NodeTypesHandler` now merges `agentgen.AllNodeTypeInfos()`
(12) with `appflow.AllAppCanvasNodeInfos()` (6) into one sorted JSON array (18 total), marshalling
each family independently at the `json.RawMessage` level to avoid coupling the two packages'
distinct `Type` field types (`agentgen.StepType` vs plain `string`). 4 new appflow tests + 1 new
admin test + 1 updated admin test (count assertion 12→18). `go test ./...` 0 failures, fresh run
(1718 sub-test PASS, 0 FAIL); `go build ./...` clean. **Backend-only** — no frontend file touched,
so the app canvas still renders from the old hardcoded `INLINE_META`/`FC_META`/`NODE_PORTS`/JSX
exactly as before; only the registry now exists and is servable. `test_42` not re-run live (no
stack access this session) but `compiler.go`/`workflow.go`/`validate.go`/`graph.go` — the files it
exercises — are untouched, so it should be unaffected. Full detail in `docs/NODE_REGISTRY_PLAN.md`.

**Phase 4 — COMPLETE (2026-09-21).** App canvas palette, node rendering (`CanvasNodes.tsx`), and
serialization (`CanvasHelpers.ts`) now read from `GET /admin/node-types` via
`frontend/src/lib/nodeRegistry.ts` — the same shared cache the agent builder's `StepNode.tsx` uses
— instead of the hardcoded `FC_META`/`INLINE_META`/palette-array constants. `go test ./...` 0
failures (full suite); `tsc --noEmit` 0 errors; `test_42_appflow_inline_nodes.py` 29/29 live after
rebuild + force-recreate of `them-go-bridge` + `them-frontend`.

**Bug found and fixed first, before Phase 4 could safely proceed:** Phase 3 legitimately produced
two `/admin/node-types` entries typed `"llm"` (agentgen's `StepLLM` + appflow's app-canvas llm
node — confirmed by Phase 3's own test), but the frontend's shared cache indexed by bare `type` in
a flat map, so one would silently clobber the other once app-canvas became a second consumer of
the merged array. Fixed with a `family` tag (`"agentgen"|"appflow"`) stamped onto each entry at
the JSON-merge boundary in `go/internal/admin/node_types.go` (new helper `withFamily`, additive,
doesn't touch either package's structs) + `getNodeDef(type, family)` in `nodeRegistry.ts` (default
`family='agentgen'` — every existing agent-builder call site is unaffected). New test
`TestNodeTypesHandler_FamilyDisambiguatesDuplicateType` (S1-125).

**Handle-ID convention unified with the agent builder, by explicit user authorization this
session** (existing app-canvas flows are test data — fine to recreate): condition branch handles
are now `ctrl-out-true`/`ctrl-out-false` in React Flow, matching `nodeRegistry.ts`'s
`resolveOutputPorts` convention, instead of the bare `true`/`false` used before. **The wire format
is unchanged** — `ConnectionDef.label` sent to the backend is still the literal `"true"`/`"false"`
(`appflow/validate.go:158` matches on that string; not touched, out of scope).
`CanvasHelpers.ts`'s `canvasToDoc`/`docToCanvas` strip/re-add the `ctrl-out-` prefix at the
serialization boundary. One consequential bug fixed in the same commit:
`InlineNodePanel.tsx`'s branch-routing readout compared `sourceHandle === 'true'/'false'` directly
and would have permanently shown "not connected" after the convention change — fixed to compare
against `` `ctrl-out-${branch}` ``.

**Deliberately not done:** `FlowControlNodePanel.tsx`/`InlineNodePanel.tsx` config forms stay
hardcoded per-`node_type` — research this session confirmed the agent builder's own
`StepConfigSection.tsx` has no `config_fields`-driven generic form renderer either (only
visuals/handles/ports/policy are registry-driven there); Router/HIL's curated dropdowns and array
editors would regress into plain text inputs under a naive generic renderer. One real improvement
taken instead: panel headers/descriptions now read `getNodeDef(...).label`/`.description` instead
of duplicating backend copy as hardcoded strings. **No live browser click-through was performed**
(no browser in this session's environment) — the plan's Phase 4 gate asks for a human
save→reload→"condition still wired true/false" round trip in the UI; `test_42`'s API-level
publish/WS/condition-routing check (29/29) is a proxy for this, not a substitute. Recommend doing
the actual click-through before trusting this in production. Full detail in
`docs/NODE_REGISTRY_PLAN.md`.

**Phase 5 — COMPLETE (2026-09-21).** Migration `db/102_middleware_defs_node_contract.sql` adds 4
nullable columns to `them.middleware_defs` (`edges`, `input_ports`, `output_ports`,
`config_fields`) and seeds File Guard's real shape (1-in/1-out edges; `config_fields` mirroring
`MiddlewareNodePanel.tsx`'s 6 real fields). Applied to the live DB. `NodeTypesHandler` (backend)
gained a DB dependency for the first time — `NewNodeTypesHandler(db)` (nil-safe, was `struct{}`
through Phases 3-4) — and now merges a third family, `"middleware"`, sourced from
`dal.ListMiddlewareDefs`, into `GET /admin/node-types` alongside `agentgen`/`appflow`.
`Executable` is hardcoded `false` for every middleware entry — confirmed live that
`workflow.go`'s `case "middleware"` is still a pass-through no-op. `go test ./...` 0 failures
(full suite); `test_42` 29/29 live after rebuild + force-recreate of `them-go-bridge`. Verified
live: `GET /api/v1/admin/node-types` returns 21 entries, `file-guard` tagged
`family: "middleware"` with correct edges + 6 config_fields.

**Backend-only, by explicit user decision.** No frontend files touched — the canvas still fetches
File Guard's visuals via the older, separate `GET /admin/middleware-defs` call
(`CanvasBuilderView.tsx`'s `mwVisualById`). Retiring that in favor of the newly-merged
`/admin/node-types` entry (the plan's "Dropped: middleware as a UI special case" goal) is
explicitly deferred — File Guard is a real security feature and the user did not want its UI
touched in the same pass as this backend change. **This closes `docs/NODE_REGISTRY_PLAN.md`** —
all 5 phases complete. Full detail in `docs/NODE_REGISTRY_PLAN.md`.

**Not done / open for a future session (not urgent, not a regression):**
- Frontend cutover of File Guard's visuals to the unified registry (above).
- Actually executing middleware inside the app-canvas graph (`workflow.go`'s pass-through made
  real) — was always out of scope for this plan; needs its own design.
- A `config_fields`-driven generic form renderer — doesn't exist anywhere in this codebase yet;
  would let `MiddlewareNodePanel.tsx`/`FlowControlNodePanel.tsx`/`InlineNodePanel.tsx` render
  from data instead of hardcoded per-type forms, at the cost of losing today's curated dropdowns
  and array editors unless purpose-built.

**Phase 1 — COMPLETE (2026-09-19).** New table `them.app_flow_llm_overrides` (migration 101,
applied to live DB). `appflow.Compile` now emits `AppFlowSpec.LLMNodes`; new
`GET|PUT /admin/applications/{id}/flow-llm-nodes[/{node_id}]`; `Lifecycle.StartAppFlow` applies
stored overrides to the compiled spec before the workflow starts (fail-open). Frontend:
`InlineNodePanel.tsx`'s LLM provider/model is now read-only with a "configured in Runtime" hint;
new `RuntimeAppFlowLLMSection.tsx` in the Runtime screen's General tab. `go test ./...` 1310 tests
0 failures (S1-122); `tsc --noEmit` 0 errors; `test_42_appflow_inline_nodes.py` 29/29 (new step
[7b]) verified live after rebuild + force-recreate of `them-go-bridge` + `them-dag-worker`. Full
detail in `docs/NODE_REGISTRY_PLAN.md`.

**Known gaps flagged, not fixed (out of scope for Phase 1):** `RuntimeView.tsx` was already over
the 400-line guideline (594 lines) before this change, now 618 — a split was not attempted.
`go/TEST_INDEX.md`'s bottom-line total was already stale before this change; bumped with a note,
full reconciliation deferred.

**Next: Phase 2** — extract the shared registry into `internal/nodedefs`. Independent of 3-4.
Gate: `/admin/node-types` JSON byte-identical before/after (pure refactor).

**STOP RULE: no new app-canvas node types (Tool, Transform) until Phase 3 lands.** Adding one
today costs six hardcoded edits, then the same work again during migration.

Suggested model: **Sonnet 5** — these phases are mechanical (move code, register definitions,
copy an existing frontend pattern). Note managed settings pin Sonnet 4.6 on restart.

---

**Inline Nodes Phase 1 (LLM + Condition) — ✅ COMPLETE (backend + frontend + E2E).**

Plan: `docs/INLINE_NODES_PLAN.md` (authoritative; kept in sync with reality as steps landed).
Brief: `docs/INLINE_NODES_DESIGN_BRIEF.md`.
All 11 steps done. `go test ./...` zero failures (S1 total 1236); `tsc --noEmit` 0 errors;
E2E `test_42_appflow_inline_nodes.py` **21/21, rerun-clean** against the live stack.

**Deployed 2026-09-19** — all three containers rebuilt and **force-recreated** (`build` + `restart`
does not pick up a new image; see LESSONS.md):

| Container | Why it needed rebuilding |
|---|---|
| `them-dag-worker` | registers `InlineLLMActivity` at startup |
| `them-go-bridge` | publish-time `appflow.Validate` wiring (step 5) |
| `them-frontend` | all of steps 7–11 — it was still running a Sept 17 image, so none of the canvas UI was actually visible before this |

Verified after redeploy: `/admin/applications` serves HTTP 200, the "Inline / Logic" palette and
`InlineNodePanel.tsx` are present inside the running container, all three containers healthy, and
E2E `test_42` passed a third consecutive time against the fully redeployed stack.

**Still not done: a human click-through of the canvas.** Rendering is verified by typecheck, the
Next.js production build, and the served bundle; the data path is verified by E2E. Nobody has
dragged an inline node onto the canvas in a browser. Worth doing before calling this closed.

### Done (steps 1–11)

| Step | What | Commit |
|---|---|---|
| 1 | `internal/appflow/inline.go` — `InlineLLMConfig`, `InlineConditionConfig`, `FlowVars`, `renderFlowTemplate`, `isTruthy`, `flowFuncs` | `1c2eda02` |
| 6 | `CanvasNodePropertiesPanel` split 1003 → 7 files (all <400) | `08d5bfd4` |
| 2 | compiler `kind:"inline"` → llm/condition; condition edge labels; 6 new validation codes; `validate.go` extracted | `3c3bb90e` |
| — | `workflow.go` split → `workflow.go` / `activities.go` / `graph.go` (prereq for 3) | `ea1fb862` |
| 3 | `InlineLLMActivity` + `InlineLLMCaller`; llm/condition dispatch in main loop AND `walkBranch`; `nodes.go` extracted | `b2fe07d0` |
| 4 | dag-worker: `dbLLMCaller.Complete`, activity registered, `InlineLLM` wired | `23add7c6` |
| 5 | `isBuiltinKind` exemption + `appflow.Validate` wired into the validate/publish endpoint | `03574b52` |

### Frontend steps 7–11 (all complete)

| Step | What | Commit |
|---|---|---|
| 7-8 | `InlineNodeData` type; `'inline'` in `ComponentDefinitionSummary.kind`; `INLINE_META` + `InlineNode` (solid border vs flow-control's dashed; condition renders two labelled `true`/`false` source handles) | `70e402b5` |
| 9-10 | serialisation (`canvasToDoc`/`docToCanvas`), `NODE_PORTS.inline`, palette + drop handler, minimap colour, **three silent-data-loss fixes** | `0b7abe99` |
| 11 | `InlineNodePanel` (LLM + Condition config), shell dispatch, backend-mismatch banner, E2E test | this commit |

**Three silent-data-loss bugs fixed in step 9-10** — all typecheck fine and fail only at runtime:
1. `docToCanvas` never set `sourceHandle`, so a reloaded canvas drew both condition branches from
   one handle and the NEXT save dropped the true/false labels — a working gate became unroutable.
2. Edge ids were `e_${source}_${target}`, so a condition with both branches on one target produced
   duplicate ids and React Flow kept one. Ids now include the label.
3. `validateConnection` rejected a second edge between the same pair without considering
   `sourceHandle`, so "log either way, then continue" was refused as "already connected".

Proven by an executed round-trip check (12/12), not by inspection.

**Standing rule from step 1:** any condition-expression example added to the UI needs a
matching case in test AF-IN-02b. Go `text/template` has no string predicates — `contains`
et al. come from `flowFuncs` in `inline.go`.

### Deployment state: NOTHING DEPLOYED for this slice

No DB migration. Before manual testing:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d --force-recreate them-dag-worker them-go-bridge
docker logs them-dag-worker --tail 5   # confirm appflow-worker polling
```
`build` + `restart` does NOT pick up a new image — see LESSONS.md.

### Hard constraints discovered this session

- **Temporal-only.** "local" backend never executes the AppFlowSpec at all — it runs the
  bound orchestrator's `OrchestrationWorkflow`. There is no local graph walker to extend, so
  inline nodes work on the Temporal backend only. Step 11's warning banner is what makes that
  legible instead of a silent no-op. (Plan §4.1/§4.2; local execution is Phase 2.)
- **Publish behaviour changed.** Temporal-backend apps now fail publish on AppFlow topology
  errors. Existing published apps are unaffected until their next re-publish. Recommend
  re-publishing Temporal-backend apps deliberately after deploy to surface latent errors.
- **`temperature` is stored but NOT applied**, and streaming emits ONE token event with the
  full response, not per-token deltas. Both blocked on the same thing:
  `agentgen.LLMProvider.Complete` takes no options and is shared with the agent builder.
  The UI must label temperature accordingly. (Plan §11 items 2–3.)
- **File sizes:** the plan under-estimated three times. `compiler.go`, `workflow.go` (twice)
  each breached the 500-line stop. All split along real seams and verified as no-ops.
  `publish.go` (698) and `cmd/dag-worker/main.go` (897) were already over before this slice.
- **No local Go toolchain on this box.** Use Docker:
  `docker run --rm -v /opt/docker/them/go:/src -w /src golang:1.25-alpine go test ./...`
  (mount a module+build cache volume or every run re-downloads deps).
- **gofmt:** `internal/appflow/compiler.go`, `cmd/dag-worker/main.go` and ~45 other files were
  already gofmt-unclean at baseline. Don't mass-reformat; it buries real diffs.

---

## Deployment state

**Active deployment: local Linux server**

Stack startup command:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
```

UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is the sole auth service (HS256 JWT + bcrypt)
- **`them-bridge` (Python FastAPI) is permanently removed** — not in `docker-compose.yml`
- **`them-worker` (Python Temporal worker) is permanently removed** — not in `docker-compose.yml`. All WS/SSE sessions submit to `them-orchestration-go` handled by `them-go-worker`. `them-orchestration` queue is empty.
- `them-go-bridge` is the active API gateway on port 8002
- `them-go-worker` is the active Temporal worker — **no explicit profile in `docker-compose.dev.yml`**, starts by default
- `them-agent-runtime` runs 2 replicas (port 9300 internal), profile `[agents]`
- Frontend `THE_M_API_URL` points to `http://them-traefik:8088`
- Named Docker volumes: `them-postgres-data`, `them-redis-data` — `external: true` (`them-logs` volume removed — Python bridge is deleted)
- **Project name: `them_gateway`** — required for all compose commands

---

## Current migration slice

**AppFlow Phase B — Temporal Execution Controls — FULLY COMPLETE (backend + frontend)**

All Phase B items complete as of 2026-09-17. `go test ./...` — zero failures (1096 tests). Frontend deployed (HEAD 54db8231).

**What's done:**
- `db/099_temporal_config.sql`: `them.app_temporal_config` table (per-app override, FK → applications, ON DELETE CASCADE). GRANT to them_app/them_admin. ✅
- `go/internal/admin/dal/temporal_config.go`: `TemporalConfig` type + `GetTemporalPlatformConfig`, `UpsertTemporalPlatformConfig`, `GetTemporalAppConfig`, `UpsertTemporalAppConfig`, `MergeTemporalConfigs` (hardcoded defaults: max_concurrent=10, workflow_timeout=3600s, activity_timeout=600s, retry=3). ✅
- `go/internal/admin/service/config.go`: `GetTemporalPlatformConfig`, `PutTemporalPlatformConfig`, `GetTemporalEffectiveConfig`, `PutTemporalAppConfig`, `validateTemporalConfig` appended. ✅
- `go/internal/admin/service/service.go`: 4 methods added to `Dal` interface. ✅
- `go/internal/admin/temporal_config.go`: `TemporalConfigHandler` — `GET|PUT /admin/temporal-config` (platform, RequireSuperAdmin) + `GET|PUT /admin/applications/{id}/temporal-config` (per-app, RequireTenantAdmin). ✅
- `go/internal/admin/router.go`: `NewTemporalConfigHandler(dbq).PlatformRoutes(platformGlobal)` + per-app routes in tenantScoped group. ✅
- `go/internal/appflow/workflow.go`: `TemporalExecCfg` type; `AppFlowWorkflowInput.TemporalCfg *TemporalExecCfg`; workflow resolves `actTimeout` and `retryMax` from cfg (fail-open: hardcoded defaults when nil). ✅
- `go/internal/appflow/temporal_config_loader.go`: `PgxTemporalConfigLoader` — reads merged effective config at workflow start via `db.Pools`. ✅
- `go/internal/execution/lifecycle.go`: `TemporalConfigLoader` interface; `WithTemporalConfigLoader`; `StartAppFlow` calls loader, sets `wfOpts.WorkflowRunTimeout`, passes `TemporalCfg` in input. ✅
- `go/cmd/them/main.go`: `execLifecycle.WithTemporalConfigLoader(appflow.NewPgxTemporalConfigLoader(rlsPools))`. ✅
- Tests: S1-114 — 13 new tests (TC-SVC-1..9 service, TC-1..4 handler). ✅
- `go/TEST_INDEX.md`: S1-114 entry added, S1 total 1206, `go test ./...` total 1096. ✅

- `frontend/src/app/admin/temporal/page.tsx`: Platform admin Temporal settings page (super_admin, sidebar under Platform Admin). ✅
- `frontend/src/app/admin/applications/components/RuntimeTemporalTab.tsx`: App Runtime Temporal tab — effective values stat tiles + per-app override inputs (empty = inherit platform default). ✅
- `frontend/src/app/admin/applications/components/RuntimeView.tsx`: General / Temporal tab strip. ✅
- `frontend/src/components/Sidebar.tsx`: "Temporal" entry added under Platform Admin. ✅

**Migration notes:**
- Apply `db/099_temporal_config.sql` before rebuilding containers.
- Rebuild and restart `them-go-bridge` after applying (TemporalConfigLoader wired in main.go).

**AppFlow Phase A — Fork/Join Parallel Branch Execution — FULLY COMPLETE**

All Phase A items complete as of 2026-09-17. E2E test passes (13/13). See `docs/APPFLOW_FORK_JOIN_PLAN.md`.

**What's done:**
- `go/internal/appflow/compiler.go`: `fork` and `join` added to `compileNode` switch; `Validate()` checks fork≥2 outgoing edges, join≥2 incoming edges. ✅
- `go/internal/appflow/workflow.go`: Fork case — `workflow.NewWaitGroup(ctx)` + `workflow.Go` per branch + `walkBranch` helper. Join case — pass-through (branches stop at join node). `mergeBranchResults` joins non-empty branch results with `"\n"`. After join, main loop continues from first edge out of join node. ✅
- Frontend: `FC_META` extended with fork (⑂ amber) and join (⊕ green); palette entries added; `docToCanvas` display name lookup updated. ✅
- Tests: AF-11..14 (compiler), AF-WF-07..09 (workflow helpers); 29/29 appflow tests pass. `go test ./...` — zero failures. ✅
- E2E: `scripts/tests/test_41_appflow_fork_join.py` — 13/13 checks pass. ✅
- `docs/LESSONS.md`: entry added — `workflow.WaitGroup` must be created with `workflow.NewWaitGroup(ctx)` (zero-value panics at .Add(1)). ✅
- Both `them-go-bridge` (51 MB, Sep 17 10:56) and `them-dag-worker` (43 MB, Sep 17 11:00) rebuilt and running with fork/join code. ✅

**Critical lesson from this phase:** After rebuilding a Go binary in Docker, you MUST use `docker compose up -d --force-recreate <service>` — `docker compose build + restart` does NOT force the container to use the new image. See `docs/LESSONS.md`.

**Next recommended task:** AppFlow Phase C — see below for candidates.

---

**App Canvas Upgrade — Phase 3 (AppFlow DAG execution) — FULLY COMPLETE**

All Phase 3 items complete as of 2026-09-17. Full canvas E2E test passes (17/17).

**What's done:**
- `go/internal/appflow/compiler.go`: `Compile()` + `Validate()` — BFS, Router/HIL classification, edge labels from conn.Label, no DefinitionID fallback. `ResolveAgentByInstanceID` reads `_resolved_agent_ids` (server-stamped at publish). `ParseLLMConfig` takes `LLMOrchConfig`. ✅
- `go/internal/appflow/workflow.go`: `AppFlowWorkflow` — named returns + `defer` + `workflow.NewDisconnectedContext` → `FinalizeRunActivity` runs on ALL exit paths including cancellation. XAdd error returned (not swallowed). ✅
- `go/cmd/dag-worker/main.go`: `pgxRunStatusUpdater` idempotency guard (`AND status NOT IN ('completed','failed','canceled')`). `pgxAgentA2ACaller` implements `AgentInvoker` — queries agents by UUID, decrypts auth_token, POSTs A2A JSON-RPC `message/send`. ✅
- `go/internal/admin/dal/publish.go`: `PublishDefinition` stamps `_resolved_agent_ids` into definition JSON via jsonb merge. ✅
- `go/internal/admin/service/publish.go`: builds `resolvedAgentIDs` map (instance_id → cd.ID) in step 5b; passes to DAL. ✅
- `go/internal/epconfig/pgx.go` + `epconfig.go`: fetches `ao.llm_provider`/`ao.llm_model` in epConfigQuery; `EPConfig.OrchestratorLLMProvider`/`OrchestratorLLMModel` populated. ✅
- `go/internal/ws/handler.go` + `go/internal/sse/handler.go`: `ParseLLMConfig(LLMOrchConfig{...})` uses EP's orchestrator binding. ✅
- `db/098_hil_approvals.sql`: HIL approval table + GRANT to them_app/them_admin. Applied. ✅
- `go/internal/admin/hil_approvals.go`: `POST /api/v1/runs/{run_id}/hil/{node_id}/approve|reject` — RBAC-gated, DB update first, Temporal signal best-effort. ✅
- `go/internal/admin/dal/hil_approvals.go`: `GetHILApproval` + `UpdateHILApprovalStatus`. ✅
- `go/internal/appflow/workflow.go`: `InvokeAgentActivity` + `AgentInvoker` interface; agent case calls `AppFlowInvokeAgentActivityName`. ✅
- `go/internal/temporal/signaler.go`: `SignalNamedWorkflow` added (named signal to arbitrary workflow). ✅
- Frontend: config round-trip, edge labels, NODE_PORTS flowControl with `'result'` in accepts. ✅
- Tests: AF-C-01..04 (compiler), AF-WF-01..06 (finalize + agent invoke), AF-HIL-01..05 (HIL API); all Go tests pass. ✅
- E2E: `scripts/tests/test_39_appflow_hil.py` — 4/4 cases pass. ✅
- `go/cmd/dag-worker/main.go`: `InvokeByID` fixed to A2A v1.0 wire format (`SendMessage`, `ROLE_USER`, `A2A-Version: 1.0` header, no `kind` field in parts). Response parsed from `result.task.artifacts[].parts[].text`. ✅
- E2E: `scripts/tests/test_40_appflow_canvas_e2e.py` — 17/17 checks pass (create app+EP+def, publish, WS session, HIL pause, approve, run completed). ✅
- Both `them-go-bridge` and `them-dag-worker` rebuilt and running. ✅

**App Canvas Upgrade — Phase 2 (Router + HIL flow control nodes) — COMPLETE**

Completed 2026-09-16. Frontend-only changes. `them-frontend` rebuilt and running.

- `frontend/src/lib/apiTypes.ts`: `ConnectionDef.type` union extended with `'flow_control'`. ✅
- `frontend/src/app/admin/applications/types.ts`: `FlowControlNodeData` interface added; `CanvasNodeData` union updated. ✅
- `CanvasNodes.tsx`: `FC_META` lookup, `FlowControlNode` component (dashed border, emoji, cyan/purple), `NODE_TYPES['flowControl']` registered. ✅
- `CanvasHelpers.ts`: `genInstanceId` handles `'flow_control'`; `canvasToDoc` serializes flowControl nodes + connections; `docToCanvas` restores them. ✅
- `CanvasBuilderView.tsx`: `flow_control` drop handler; Flow Control palette section (Router + HIL draggable items). ✅
- `CanvasInner.tsx`: Minimap nodeColor for `flowControl` nodes (`#a855f7`). ✅

**App Canvas Upgrade — Phase 1 (Middleware node registry) — COMPLETE**

Completed 2026-09-16. `them-go-bridge` rebuilt and running.

- `db/097_middleware_defs_visual.sql`: Added `emoji`, `color`, `bg_color` columns to `middleware_defs`; seeded File Guard. Applied. ✅
- `go/internal/admin/dal/middleware_wirings.go`: `MiddlewareDefSummary` extended; `ListMiddlewareDefs` query updated. ✅
- `go/internal/admin/middleware_wirings.go`: `ListDefs` handler for `GET /admin/middleware-defs`. ✅
- `go/internal/admin/router.go`: Route `GET /admin/middleware-defs` registered in tenant-scoped group. ✅
- Frontend types + canvas: visual metadata served from DB, not hardcoded. ✅
- Tests: S1-111 `TestMiddlewareWirings_ListDefs`; 1164 tests, 0 failures. ✅

**Migrations applied:** `db/096_file_guard_seed.sql`, `db/097_middleware_defs_visual.sql`

**Next recommended task:** Phase 3 — App canvas DAG execution (local loop + Temporal). See `docs/APP_CANVAS_UPGRADE_PLAN.md` and `docs/HANDOVER_APP_CANVAS.md`.

---

**Step 38 — File Guard (Phase 1: per-agent file scanning via canvas) — COMPLETE**

All 6 steps complete and deployed (2026-09-16).

- **Step 1** `db/096_file_guard_seed.sql`: Builtin `file-guard` def seeded in `middleware_defs` + `component_definitions`. Applied. ✅
- **Step 2** `go/internal/middleware/gate.go`: `FileGate` resolves per-agent wiring first (`middleware_wirings` by `app_id+agent_slug`), falls back to app-level `security_config`. `GateInput.AgentSlug` added. Cache invalidation evicts all `appID:*` wiring entries. ✅
- **Step 3** `go/internal/admin/dal/middleware_wirings.go` + `go/internal/admin/middleware_wirings.go`: Full CRUD API under `GET|POST|PUT|DELETE /admin/applications/{id}/middleware-wirings`. Redis invalidation on write. 4 handler tests. ✅
- **Step 4** `frontend/.../CanvasNodePropertiesPanel.tsx`: Clicking a middleware (guard) node opens structured File Guard panel — enabled toggle, mode, max_file_size_mb, allowed/blocked types, notify toggle, Save/Remove. Wiring created/updated/deleted via API. ✅
- **Step 5** `go/internal/admin/dal/runs.go` + handler: `GET /api/v1/runs/{run_id}/guard-events` — returns `run_artifacts` rows where `scan_status != 'disabled'`, joined with `middleware_jobs`. Run history modal has a **Security** tab showing each intercepted file with scan status badge, size, content type, scanned timestamp. Count badge on tab turns red for infected/flagged. 2 handler tests (RG-1/RG-2). ✅
- **Step 6** `go/internal/admin/dal/applications.go` + handler: `GET /api/v1/admin/applications/{id}/guard-health` — returns per-agent wiring list + app-level aggregate (scanned/clean/blocked/pending/errors/last_event). RuntimeView Security section shows File Guard Health panel with stat tiles and per-agent wiring list. 2 handler tests (GH-H1/GH-H2). ✅

---

**Step H2 — RLS Closure (full superuser removal)**

Completed:
- `db/078_rls_phase_h2.sql`: Enables FORCE ROW LEVEL SECURITY on 4 remaining tables — application_definitions, managed_app_bindings, quarantine_artifacts (direct tenant_id isolation), component_definitions (split policy: SELECT own+NULL/platform globals, write own only; DML revoked from them_app). **All 28 them-schema tables now have RLS.**
- `go/internal/config/config.go`: `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` are now required at startup (fail fast if absent).
- `go/internal/config/config_test.go`: Tests CF-01 + CF-02 cover missing required DB URL detection.
- `go/cmd/them/main.go`, `go/cmd/worker/main.go`, `go/cmd/dag-worker/main.go`: All binaries fail if rlsPools cannot be created; all tenant data paths use `rlsPools.Admin` (BYPASSRLS); `database.Pool()` retained only for health check pinger (`appliveness.Loop`).
- `docker-compose.yml` / `docker-compose.dev.yml`: `THEM_DB_URL_APP` + `THEM_DB_URL_ADMIN` injected into all 4 Go service environments (them-go-bridge, them-go-worker, them-go-worker-2, them-dag-worker).
- `go/internal/db/rls_integration_test.go`: `TestRLS_TwoTenantFullIsolation` covers all 27 tenant-scoped RLS tables; `TestRLS_CatalogVerification` (CV-01..05) asserts catalog invariants. Fixture properly handles FK constraints (middleware_audit uses real run_artifact IDs).
- `go/TEST_INDEX.md` updated: CF-01, CF-02 added; S1 total → 1092.
- `go test ./...` — all packages pass, 0 failures. HEAD: b0cdb79.

⚠️ **After next deploy, restart all 4 Go containers** — `THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN` must be present in `.env` (run `./generate-env.sh` to regenerate).

All quota fields now enforced: `max_concurrent_runs`, `runs_per_minute`, `monthly_runs`, `api_requests_per_minute`, `monthly_llm_tokens`. `max_agents`, `max_apps`, `max_mcp_servers`, `max_users` enforced at Create time.

### Multi-tenant testing session (2026-09-07) — bugs fixed

Bugs found and fixed during end-to-end testing as `avi-test-admin`:

- **Publish 500**: `UpsertEntryPoint` used `ON CONFLICT (tenant_id, slug)` but DB constraint is `(application_id, slug)`. Fixed in `go/internal/admin/dal/publish.go`. Migration `db/083_grant_tenants_to_app.sql`: `GRANT SELECT ON them.tenants TO them_app` — `listAppQuery` JOINs `them.tenants` but `them_app` had no permission, causing GET /applications/{id} → 404 for all RLS-scoped requests. Playground `useEffect` refetches on window focus (stale app list after navigation). ProvisionWizard, delete tenant, logo, font self-hosting, Material Symbols — all complete from prior session.

**Current stack status:** All containers healthy. `avi-test` tenant clean (no apps). Playground WS URL uses `/{tenantSlug}/apps/{appSlug}/{epSlug}/ws` — backend serves this correctly for both `default` and `avi-test` tenants.

**Multi-tenant E2E testing COMPLETE (2026-09-07)** — WS chat verified for both tenants:
- `avi-test` tenant: "first" app WS chat → connected, LLM replied "Hello." ✅
- `default` tenant: "freddy" app WS chat → connected, LLM replied "Hey!" ✅

Bugs fixed and committed in this session:
- `49d8725` fix(tokens): null user_id FK violation on POST /admin/tokens — `NULLIF($4,0)::integer` + `COALESCE(user_id,0)` in SELECT + same fix in `pgx_querier.go`
- `6e8e781` fix: EP LLM fallback + interleaved pgx query + duplicate ListEntryPoints — worker now falls back to EP-level LLM when orchestrator row has NULL values; `ListApplications` collects all rows before running sub-queries; duplicate `ListEntryPoints` call removed from service layer

---

### Tenant-scoped URL restructure — COMPLETE (3ff2593 + d2a3760, 2026-09-07)

All runtime entry point URLs now require a `/{tenant_slug}` prefix:
- WS:    `/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws`
- SSE:   `/{tenant_slug}/apps/{app_slug}/{ep_slug}/sse`
- Voice: `/{tenant_slug}/apps/{app_slug}/{ep_slug}/voice/*`
- A2A:   `/{tenant_slug}/a2a/{app_slug}/{ep_slug}`
- Card:  `/{tenant_slug}/a2a/{app_slug}/{ep_slug}/.well-known/agent.json`

New: `go/internal/tenantctx/resolver.go` — `PgxSlugResolver` (DB lookup + 5-min cache) wired into ws, sse, a2a, and voice handlers via `WithSlugResolver()`. Unknown slugs → 404. Admin DAL structs + API responses include `tenant_slug`. Frontend `ConnTarget` + voice API calls carry `tenantSlug`. Traefik labels changed from `PathPrefix` to `PathRegexp` (`^/[^/]+/apps(/|$)` and `^/[^/]+/a2a(/|$)`). `Handle("/*")` catch-all in server (chi does not allow `Mount("/")`). 49/49 tests pass. Bridge rebuilt and running.

**⚠️ No existing clients to break — confirmed before implementation. Frontend playground updated.**

### Completed steps (this session)

- **Step 24 (c83f8eb subset)** — MCP server audit: `mcp_server.create/update/delete` wired into `AuditWriter`. Test AL-05b.
- **Step 25 (3fc072c)** — Removed `MCPServersHandler` legacy fallback path. `pools=nil` branch in agents/apps/orchestrators `openSvc` is intentionally kept as the unit-test escape hatch (never reached in production — clearly documented in comments).
- **Step 26 (c83f8eb)** — Audit log enrichment: `AuditEntry.Changes map[string]any`, `changesOf()` helper, all 4 update handlers (agent/app/mcp_server/tenant) now log request payload in `details` JSONB as `{"actor":"…","changes":{…}}`. Tests AL-06, AL-07, AL-08. `go test ./...` — 1096 tests, 0 failures.
- **Step 27** — Cross-tenant admin observability: `GET /api/v1/admin/observability/summary` (RequireSuperAdmin, Admin BYPASSRLS pool). New `go/internal/admin/dal/observability.go` (ListObservabilitySummary — one row per tenant: run_count_30d, total_llm_tokens_30d, max_agents, max_apps, agent_count, app_count). New `go/internal/admin/observability.go` (ObservabilityHandler). Wired in `router.go` platform-global group. Tests S1-101 (OBS-1..4). `go test ./...` — 1100 tests, 0 failures.
- **Security audit fixes** — Three audit-log secret-exposure bugs found and fixed (audit of Step 26 output):
  - `AgentInput.AuthToken`: handler now deletes `auth_token` key from changesOf map, adds `auth_token_changed=true` sentinel.
  - `MCPServerPatch.ProbeToken`: handler now deletes `probe_token` key, adds `probe_token_changed=true/cleared` sentinel.
  - `TenantIDPConfig.ClientSecret`: handler now deletes nested `idp_config.client_secret`, adds `client_secret_changed=true` sentinel.
  - Tests AL-09, AL-10, AL-11 cover all three redaction paths. S1-100: 7→10 tests. `go test ./...` — 1103 tests, 0 failures.
- **Migration 079** (`db/079_component_definitions_grant.sql`): Restores `GRANT INSERT, DELETE ON them.component_definitions TO them_app`. Migration 078 over-revoked these — Agent Create (CTE insert) and Delete (explicit DELETE) run via TenantTx (them_app role) and would have broken with 078 applied. Must apply 078+079 together.
- **Security fix — cross-tenant token exfiltration** (`go/internal/admin/dal/agents.go`, `go/internal/admin/agents.go`): Added `GetAgentTokenEncryptedForTenant(ctx, id, tenantID)` which filters by both `id AND tenant_id`. The Discover handler now uses the tenant-scoped lookup. Test CT-01 covers the cross-tenant check.
- **Production-path audit redaction tests** (`go/internal/admin/audit_redaction_test.go`, `audit_logs.go`): Added `NewAuditWriterForTest(q DBQuerier)` constructor; test AR-01 verifies that auth_token does not appear in audit JSONB, only `auth_token_changed=true` sentinel.
- **App pool permission gaps closed** (`db/080_grant_tenant_quotas_to_app.sql`, DAL fixes): See LESSONS.md for full root cause. Summary:
  - Removed `LEFT JOIN auth_service.users` from `agentSelectCols` and `agent_definitions` queries — `them_app` cannot cross into `auth_service` schema.
  - Migration 080: `GRANT SELECT ON them.tenant_quotas TO them_app` — required by `checkResourceQuota` in Create paths.
  - agents.Create and orchestrators.Create now return HTTP 409 for duplicate slug/name (SQLSTATE 23505 → ErrConflict).
- **Frontend observability page** (`frontend/src/app/admin/observability/page.tsx`): `/admin/observability` table with per-tenant run count (30d), LLM tokens (30d), agent/app quota with color coding. Sidebar entry added.

**HEAD: `2b05c2c feat(iam): Step 38-UI Changes 1+2 — user Membership tab + tenant Members tab`**

**E2E test 14 verified: 9/9 passed, rerun-clean** (2026-09-05 — two consecutive runs both 9/9 with no manual cleanup needed).

**RLS CLOSED** (2026-09-05): Two-tenant isolation integration test passes (24 table checks per tenant, cross-tenant INSERT rejected). Catalog verification passes (CV-01..05). AR-02 and AR-03 handler-path redaction tests added. `go test ./...` — 1037 pass, 0 fail. `go test -tags=integration ./internal/db/...` — all pass.

**Step 29 complete** (2026-09-05): Orchestrator hard-delete (`f787894`). E2E test cleanup fixed (`e455fae`) — delete now uses orchestrator name (route `/orchestrators/{name}`) instead of UUID.

### Step 29 — Orchestrator hard-delete: COMPLETE (f787894 + e455fae)

`DeleteOrchestrator` changed from soft-delete (`UPDATE enabled=false`) to hard-delete (`DELETE FROM`). The orchestrator name is now freed immediately for reuse. E2E test 14 cleanup fixed to use `name` (not UUID) in the delete path — test is now rerun-clean (9/9 two consecutive runs).

Also see LESSONS.md entry: "Orchestrator Delete is soft (UPDATE enabled=false), not hard".

### Step 28 — Admin CRUD TenantTx migration: ALREADY COMPLETE

Investigation (2026-09-05) confirms that `AgentsHandler`, `OrchestratorsHandler`, and `ApplicationsHandler` already use `BeginTenantTx` via `openSvc` — this migration was completed in a prior session before the Step 28 handover note was written.

**RLS status (verified):** All 28 active tenant tables have ENABLE + FORCE RLS with correct policies. Superuser removed from all runtime request paths. All tenant-scoped admin CRUD handlers (agents, apps, orchestrators, MCP servers) use `TenantTx` (RLS-enforced App pool). Defense-in-depth is active.

The following remain on the Admin pool by design (not regressions):
- `AuditWriter.Write` — audit_logs has INSERT-only RLS for them_app; reads require Admin pool
- `AuditLogsHandler.List` — SELECT on audit_logs requires Admin pool (INSERT-only RLS for them_app)
- Action endpoints in AgentsHandler (Discover/Test/SecurityScan) — use `legacyDAL` for cross-tenant/platform-global reads (`GetAgentBySlug("security_scanner")`, unscoped token lookups)

### Step 30 — Tenant self-service: COMPLETE (bfc98a2)

New `/api/v1/tenant/` route group accessible to `admin` OR `super_admin` roles (not super_admin-only).
- `GET /tenant/settings` — returns caller's own tenant (ID from JWT via tenantctx — never from URL)
- `PATCH /tenant/settings` — edit display_name/email_domain/idp_config; slug and enabled are read-only for self-service
- `GET /tenant/quota` — view own quota
- `RequireTenantAdmin` middleware added to `middleware.go`
- Frontend: `/tenant/settings` page (General + Quota tabs); "My Tenant" Sidebar nav item
- `go test ./...` — 1043 pass, 0 fail

### Step 31 — Quota enforcement: COMPLETE (1c2bc3a)

`api_requests_per_minute` and `monthly_llm_tokens` are now enforced at run Admit time.
- `api_requests_per_minute` → Redis INCR `rl:them:{tenant}:api:{minute}` TTL 90s → 429 ErrAPIRateLimited
- `monthly_llm_tokens` → DB SUM `total_tokens_in + total_tokens_out` from `them.runs` current month → 429 ErrMonthlyLLMTokensExceeded; fail-open on DB error
- `SumMonthlyTokens` DAL method added to `dal/runs.go`
- `MonthlyTokenCounter` interface + `WithTokenCounter` on `quota.Enforcer`
- 8 new unit tests (QE-10..17). `go test ./...` — 1051 pass, 0 fail.

### Step 32 — User Management: COMPLETE (9b5c320)

Full user CRUD API + frontend page. `POST /api/v1/admin/users` creates user with bcrypt password + tenant membership assignment. 12 new tests (UM-01–UM-12).

### Step 33 — Tenant login chain: FULLY COMPLETE

Three fixes applied across two commits (80924a1 + closure commit):

1. **CreateUser role contract**: `role` field → `RoleName` (platform role in `auth_service.roles`: super_admin/developer/analyst/viewer). `tenant_role` field → `TenantRole` (membership role: admin/member/viewer). Previous code discarded `tenant_role` and used `role` as membership role — broke when frontend sent both fields.

2. **`/me` returns JWT membership role**: `Me()` was returning `user.Role` (global DB role from `auth_service.users`). Fixed to return `claims.Role` (membership role from the JWT — what the bridge actually enforces).

3. **Regression tests** (`go/internal/authserver/tenant_login_test.go`):
   - UM-13: `TestTenantLogin_TwoTenantIsolation` — two users in different tenants, no-membership user blocked, /me returns membership role
   - UM-14: `TestTenantLogin_RefreshCarriesTenant` — refreshed token carries correct tenant_id and role
   - S1-40: 81 → 83 tests. Total: 1051 → 1053.

4. **Doc sync**: AUTH.md corrected (JWT claims example, two-role model, refresh behavior, user CRUD endpoints, migration status); STATUS.md marked historical; MULTITENANT_PLAN.md Gap 1 closed; INDEX.md updated.

Live smoke test (2026-09-06): create user → login → JWT has correct `tenant_id` and membership role → super_admin route returns 403 (correct).

### Pre-Step-34 Auth Hardening — COMPLETE (2026-09-06, 7920bf2)

Two auth correctness/security fixes applied before Step 34:

**Fix 1 — OIDC role separation** (`go/internal/authserver/oidc_store.go`, `oidc.go`):
- `UpsertOIDCUser` was using the group-mapping `role` for both `auth_service.roles` platform lookup AND `tenant_memberships.role`. If "admin" was mapped as a group role, the code tried to look up a platform role named "admin" (doesn't exist) — 500 error. If "super_admin" was mapped, it would have granted platform super_admin to OIDC users.
- Fix: platform role for all OIDC users is always `"viewer"` (hard-coded; no group mapping can change it). Membership role uses `validMemberRoles` guard. `super_admin` rejected at three layers: OIDCCallback (app layer), `UpsertOIDCUser` guard, and DB CHECK.
- Migration `db/081_tenant_group_mappings_safe_roles.sql`: CHECK on `them.tenant_group_mappings.role` restricted to `('admin','member','viewer')`. **⚠️ Migration 081 has NOT been verified as applied to the live DB — apply before enabling OIDC group mapping features.**

**Fix 2 — Refresh preserves tenant** (`go/internal/authserver/jwt.go`, `service.go`, `store.go`, `pgx.go`):
- `refreshClaims` now carries `TenantID`. `IssueRefreshToken(userID, tenantID)` signature updated everywhere (login + OIDC callback).
- `Refresh()` calls new `issuePairByTenantID` when `tenant_id` is present in refresh claims — re-validates the specific membership row (catches revoked memberships mid-session).
- `GetTenantMembershipByID` added to Store interface + pgx implementation.
- **⚠️ Backwards compatibility:** Refresh tokens issued before this commit (7920bf2) carry no `tenant_id`. These tokens use the legacy `issuePair(ctx, user, "")` fallback (picks first membership row). Affected multi-membership users must re-login to get a tenant-preserving refresh token. Single-tenant users are unaffected.

**Tests**: OIDC-28 (admin group → platform role stays viewer), OIDC-29 (super_admin group → rejected, falls back to viewer), OIDC-30 (refresh for multi-membership user preserves tenant B). 1053 → 1056 tests. `go test ./...` — 0 failures.

**Docs updated in same commit (7920bf2)**: MULTITENANT_PLAN.md (escalation risk closed, refresh limitation closed), SCHEMA.md (migration 081 entry + inline table description corrected), LESSONS.md (two new entries).

### Step 34 — Role-based nav + frontend route guards: COMPLETE (82a8f22)

**Frontend only. No Go changes. No DB changes.**

What was built:
- `frontend/src/hooks/useRequireSuperAdmin.ts`: new hook — reads `user.role` from authStore, redirects to `/admin/applications` if not `super_admin`.
- `frontend/src/components/Sidebar.tsx`: `ADMIN_NAV` split into two arrays — common items for all admins, and `SUPER_ADMIN_NAV` (Tenants/Users/Managed Apps/Observability) rendered only when `role === 'super_admin'`.
- `frontend/src/app/admin/tenants/page.tsx`, `users/page.tsx`, `observability/page.tsx`: each calls `useRequireSuperAdmin()` at top — direct-navigation guard.

### Step 35 — Tenant provisioning wizard: COMPLETE (c95976d)

4-step wizard on `/admin/tenants`: tenant → admin user → quota → done. `onCreated(t)` called after Step 1 so tenant appears in list immediately. Steps 2/3 are skippable. Skipped steps listed in amber warning on Step 4.

### Step 36 — Tenant onboarding banner: COMPLETE (34687ad)

`GetStartedBanner` on `/admin/applications` empty state. Role-aware headline + 3-step guide + "Create Application" CTA. Replaces the minimal dashed card.

### Step 34.5 — Router split (tenant admin access): COMPLETE (26ee921)

**Critical bug fix.** All `/admin` routes were under `RequireSuperAdmin` — tenant admins (role=admin) received 403 on Applications, Agents, Orchestrators, MCP Servers, Runs, Audit Logs. Steps 34 and 36 were visually correct but functionally broken.

`BuildRouter` split into two authorization tiers within a shared JWT group:
- **Tenant-scoped** (`RequireTenantAdmin` + `AdminTenantMiddleware`): agents, orchs, apps, tokens, MCP, agent-defs, audit logs, runs — accessible by `admin` AND `super_admin`
- **Platform-global** (`RequireSuperAdmin` only): tenants, users, llm-providers, monitoring, observability, sessions

RLS isolation preserved — `AdminTenantMiddleware` still sets `app.tenant_id` GUC on every tenant-scoped request.

6 new tests (TH-13–18): admin JWT → 200 on agents/apps/orchs/runs; admin JWT → 403 on llm-providers; member JWT → 403 on agents. **1062 tests, 0 failures.**

### Step 37 — SSO: tenant-admin IdP form + Keycloak test IdP: COMPLETE (7378d09)

**Frontend only. No Go changes. No DB changes.**

What was built:
- `frontend/src/app/tenant/settings/page.tsx`: added "SSO / Identity Provider" tab between General and Quota. Fields: `discovery_url`, `client_id`, `client_secret` (write-only — shows `••••••••••••••••` placeholder when `idp_configured=true` and user hasn't typed), `redirect_uri`. "Save SSO config" button → `PATCH /tenant/settings` with `idp_config`. "Clear SSO config" → `idp_config: null` after `window.confirm`. Status badge updates after save/clear.
- `docker-compose.dev.yml`: added `them-keycloak` service (`profile: sso`) — `quay.io/keycloak/keycloak:latest`, `start-dev --import-realm`, `KC_HTTP_RELATIVE_PATH=/auth/keycloak`, bind-mounts `./keycloak`, Traefik route at `/auth/keycloak` priority 130 (beats go-auth-go router at 120). Named volume `keycloak-data`.
- `keycloak/them-realm.json`: pre-configured `them` realm — confidential client `them-m` (secret `them-m-secret`, redirect `http://localhost:8088/auth/api/v1/auth/oidc/callback`), test user `testuser@example.com` / `testpass`.
- `scripts/tests/test_37_sso.py`: smoke test — login, configure IdP via `PATCH /tenant/settings`, verify `idp_configured=true` in response and GET readback, tenant-lookup check, clear config.

Start Keycloak test IdP:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile sso up -d them-keycloak
```
Discovery URL: `http://localhost:8088/auth/keycloak/realms/them`
Run smoke test: `python3 scripts/tests/test_37_sso.py`

### Step 37-S — Encrypt IdP client_secret at rest: COMPLETE (db7ddc3)

**Go only. No DB changes. No frontend changes.**

What was built:
- `go/internal/idpcrypto/idpcrypto.go`: AES-256-GCM Encrypt/Decrypt. Ciphertext format: `enc:<hex(nonce||ct)>`. Nil key → pass-through. Legacy plaintext (no `enc:` prefix) decrypts unchanged. 9 tests (IDP-1..9).
- `go/internal/admin/dal/dal.go`: `DB.WithIDPKey(key []byte)` — returns shallow copy with encryption key set; `DB.IDPEncrypt(s)` helper.
- `go/internal/admin/dal/tenants.go`: `PatchTenant` encrypts `client_secret` before `json.Marshal` when key is set.
- `go/internal/authserver/oidc_store.go`: `NewPgxOIDCStoreWithKey(pool, key)` constructor; `GetTenantIDPConfig` decrypts after unmarshal. Legacy plaintext passes through.
- `go/internal/authserver/config.go`: `IDPEncryptionKey` field; `ParseKey` validation at startup (fail-fast on malformed key).
- `go/internal/config/config.go`: Same field + validation for the bridge.
- `go/internal/admin/router.go`: `BuildRouter` gains `idpKey []byte` parameter.
- `go/internal/admin/tenants.go`, `tenant_self_service.go`: constructors gain `idpKey []byte`; call `.WithIDPKey(idpKey)`.
- `go/cmd/auth-server/main.go`, `go/cmd/them/main.go`: parse `IDP_ENCRYPTION_KEY` → pass to oidcStore / BuildRouter.
- `docker-compose.yml`: `IDP_ENCRYPTION_KEY=${THE_M_IDP_ENCRYPTION_KEY:-}` in `them-auth-go` and `them-go-bridge`.
- `generate-env.sh`, `generate-env.ps1`: derive `THE_M_IDP_ENCRYPTION_KEY` from `secrets.local` using `Derive-Secret "idp-encryption-key"`.

**Deployment note:** Run `./generate-env.sh` (or `.ps1`) to get `THE_M_IDP_ENCRYPTION_KEY` in `.env`, then rebuild and restart `them-auth-go` and `them-go-bridge`. Existing plaintext secrets continue to work (pass-through) — re-save each tenant's IdP config to encrypt them.

**49 packages pass, 0 failures.** (S1-IDP: 9 new tests)

### Session 2026-09-07 — A2A dispatcher fix (7e9b7b1)

**Root cause:** `srv.MountA2A` used `s.router.Mount("/", handler)` which conflicted with `srv.MountApps`'s `s.router.Handle("/*", handler)`. Chi's `Handle("/*")` registered last wins — all `POST /{tenant_slug}/a2a/…` requests landed in `appsDispatcher` which returned 404 for non-ws/sse/voice paths.

**Fix:** Removed `srv.MountA2A`; A2A handler passed as 4th arg to `appsDispatcher`; dispatcher now routes `strings.Contains(path, "/a2a/")` to it. Bridge rebuilt and restarted ✅.

**Runtime UI fixes (8b91491 + 8b445b7) — all resolved:**

1. **EP enable/disable toggle**: Added dedicated `PATCH /entry-points/{ep_id}/enabled` endpoint — only touches the `enabled` column. ✅
2. **history_window clamp**: Removed `history_window < 1 → 20` clamp; `0` now means "history off" and persists correctly. ✅
3. **Save button consolidation**: LLM & Memory section now has one Save per EP card (covers both LLM provider + history/summarizer). ✅
4. **history_window not returned by API**: `EntryPoint` struct and `ListEntryPoints` query were missing `history_window` — slider always reset to 20. Fixed: added field to struct + `COALESCE(ep.history_window, 20)` to SELECT. ✅

### Step 38-UI — IAM UI: COMPLETE (Changes 1–3)

**Completed in this session (2026-09-07):**

**Change 3 — `/tenant/members` page** ✅ commit `5038759`
- `GET /api/v1/tenant/members` — new handler in `tenant_self_service.go`; reads tenant_id from JWT (RequireTenantAdmin); calls `dal.ListMembers`; returns `TenantMember[]`.
- `UserUpdateInput.TenantRole *string` added to `store.go` / `pgx.go` — `UpdateUser` patches `tenant_memberships.role`; validates against `validMemberRoles`; returns `ErrInvalidRole` for invalid values → 400.
- `updateUserRequest.TenantRole` added to `user_mgmt_handlers.go`; `ErrInvalidRole` handled → 400.
- `frontend/src/app/tenant/members/page.tsx` — new page; read-only table (Username/Email/Role/Joined); slide-in panel with editable Membership Role dropdown (admin/member/viewer) for tenant admins; read-only for viewers.
- `Sidebar.tsx` — "Members" nav entry directly below "My Tenant".
- Tests: TSS-07, TSS-08 (GetMyMembers empty + populated); UM-07b (tenant_role updated); UM-07c (super_admin rejected → 400). 49 packages, 0 failures.

**Change 1 — `/admin/users` Membership tab** ✅ commit `2b05c2c`
- Added 4th tab "Membership" to `UserPanel` — shows tenant_slug (read-only) + tenant_role dropdown; Save calls `PATCH /auth/api/v1/admin/users/{id}` with `tenant_role`.
- Added role filter `<select>` above the table (All/super_admin/admin/member/viewer/inactive), client-side.

**Change 2 — `/admin/tenants` Members tab** ✅ commit `2b05c2c`
- Added 4th tab "Members" to `TenantPanel` — fetches `GET /api/v1/admin/tenants/{id}/members` on tab open; compact read-only table.
- `api.ts`: `listTenantMembers(tenantId)` + `TenantMember` type exported.

### Step 39 — IAM Stage 3: tenant-admin self-service SSO group mapping — COMPLETE (7f09eb2, 2026-09-09)

- `authserver/oidc_store.go`: `IDPConfig.GroupsClaim` + `UnmatchedAction`; `OIDCDebugRecord` struct; `WriteOIDCDebug`/`GetOIDCDebug` (Redis 24h TTL); `NewPgxOIDCStoreWithKeyAndRedis`
- `authserver/oidc_jwks.go`: `verifyRS256IDToken` populates `claims.RawPayload` from validated payload
- `authserver/oidc.go`: three-way outcome (matched/unmatched/lookup_error); AT-11: lookup_error → 503, reject login (never fall back to viewer on auth-system failure); unmatched+deny → 403; configurable claim via `extractGroups`; `WriteOIDCDebug` for all outcomes
- `authserver/config.go`: `RedisHost`/`RedisPort`/`RedisPassword` + `RedisAddr()`; `cmd/auth-server/main.go`: optional Redis client
- `admin/dal/tenants.go`: `TenantIDPConfig.GroupsClaim` + `UnmatchedAction`
- `admin/tenant_self_service.go`: `ListMyGroupMappings`, `UpsertMyGroupMapping` (rejects super_admin), `DeleteMyGroupMapping`, `GetOIDCDebug`; Redis field; updated constructor
- `docker-compose.yml`: `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD` for `them-auth-go`
- `db/089_idp_group_claim_config.sql`: re-applies role CHECK without super_admin (migration 081 was file-only)
- Frontend: `groups_claim` field + `unmatched_action` select in SSO tab; Group Mappings tab (list/add/delete); SSO Login Debug box
- Tests: OIDC-31..33 (configurable claim, deny, lookup error), TSS-13..20 (group CRUD, debug); 49 packages pass
- Verified live: `them-auth-go` Redis connected; GET /tenant/group-mappings → [] ✓; GET /tenant/settings → 200 ✓

### End-user auth Phase 1 — COMPLETE (2026-09-08)

**Plan:** `docs/END_USER_AUTH_PLAN.md` — Phase 1 of 3-phase end-user identity implementation.

**Migrations applied:**
- `db/084_external_user_history.sql`: `them.tasks.external_user_id TEXT`, `them.access_tokens.is_backend BOOLEAN DEFAULT false`
- `db/085_runs_external_user.sql`: `them.runs.external_user_id TEXT`

**Go changes (all tests pass, 0 failures):**
- `go/internal/auth/pgx_querier.go` + `token_cache.go`: `is_backend` fetched from DB, surfaced as `TokenInfo.IsBackend`
- `go/internal/transport/transport.go`: `ExternalUserID string` added to `RuntimeIdentity`
- `go/internal/domain/domain.go`: `ExternalUserID string` added to `Run`
- `go/internal/execution/request.go`: `ExternalUserID string` in `ExecutionRequest` + `ExecutionHandle`
- `go/internal/execution/lifecycle.go`: `ExternalUserID` propagated from request → `domain.Run`
- `go/internal/ws/handler.go` + `go/internal/sse/handler.go`: read `X-External-User` header only when `tokenInfo.IsBackend==true`; wire into `WorkflowInput.ExternalUserID`
- `go/internal/temporal/workflow.go`: `ExternalUserID string` in `WorkflowInput`
- `go/internal/temporal/activities.go`: `ExternalUserID` set in `RunContext`
- `go/internal/orchestrator/orchestrator.go`: `RunContext.ExternalUserID`; `HistoryLoader` and `CheckpointWriter` interfaces updated to include `externalUserID` parameter
- `go/internal/orchestrator/summary.go`: `SummaryStore` interface + `maybeSummarize` updated
- `go/internal/history/pgx.go`: `LoadHistory`, `WriteMessage`, `LoadSummary`, `SaveSummary`, `resolveRootTaskID` all thread `externalUserID` through
- `go/internal/runrecorder/recorder.go`: `external_user_id` written in `CreateRun` INSERT
- Tests: `TestHistory_CrossUser_Denied`, `TestHistory_ServiceToken_ExternalUserIsolation` added

**Trust boundary enforced:** `X-External-User` header accepted ONLY when `tokenInfo.IsBackend==true`. Mobile/browser tokens can never assert end-user identity.

**What Phase 1 enables:** A backend service (e.g. bank's app backend) can present a token with `is_backend=true` and include `X-External-User: customer-123` — all runs and tasks are tagged with `external_user_id`. `LoadHistory` filters by `external_user_id` when non-empty, preventing user A from reading user B's history with the same `context_id`.

**Security verification (2026-09-08) — three gaps confirmed against code:**

1. **Application authorization** — `CheckAccess` has no principal-type guard. Safe for Phase 2 because `AccessModeUser` mismatch rejects at admit time before `CheckAccess`. `allowed_principals` is Phase 3.

2. **History namespace collision** — `user_id` and `external_user_id` are different columns; same-value collision is impossible at SQL level. However: empty `externalUserID` skips the filter, so an internal session can read end-user turns sharing the same `context_id`. Fix: Phase 2 adds `user_id` to `them.tasks` and a per-user filter for internal sessions.

3. **Managed app consuming-tenant check** — `epConfigQuery` uses `ep.tenant_id = $1`. Managed app entry_points are owned by the platform tenant. A consuming tenant's URL slug will not match → managed apps are NOT reachable at runtime today. This is a separate Phase 5 gap (extend `epConfigQuery` to JOIN `managed_app_bindings`). Out of scope for Phases 1–4.

**Plan updated:** `docs/END_USER_AUTH_PLAN.md` is now v4 with verified gap analysis.

**What Phase 2 requires (revised):** the-M user JWT at entry points (`AccessModeUser`), `end_user` runtime-only role, `user_id` on `them.tasks` for internal history isolation, regression tests for all three boundary types. See `docs/END_USER_AUTH_PLAN.md`.

**What Phase 3 requires:** `allowed_principals` column on entry_points + enforcement in `CheckAccess`.

**What Phase 4 requires:** `JWKSAuthenticator` + `tenant_runtime_config` for bank-issued JWTs.

**What Phase 5 requires:** `epConfigQuery` JOIN on `managed_app_bindings` so consuming tenants can reach managed app entry points.

### End-user auth Phase 2 — COMPLETE (2026-09-08)

**Migrations:**
- `db/086_phase2_user_history.sql`: `them.tasks.user_id INT` + `them.runs.user_id INT` (index on each WHERE NOT NULL)
- `db/087_end_user_role.sql`: seed `auth_service.roles` with `end_user` (dashboard_access='none', rate_limit=1000, cost_limit_daily=$10, token_expiry=3600)

**Go changes:**
- `go/internal/epconfig/epconfig.go`: `AccessModeUser = "user_jwt"` constant
- `go/internal/domain/domain.go`: `UserID int64` on `Run`
- `go/internal/execution/request.go`: `UserID int64` on `ExecutionRequest` + `ExecutionHandle`
- `go/internal/execution/lifecycle.go`: step 3.5 — when `AccessMode==AccessModeUser`, validate HS256 JWT via `auth.ValidateHS256JWT`, assert `claims.TenantID == EP.TenantID`, set `req.UserID = claims.UserID`. `jwtSecret []byte` field + `WithJWTSecret(secret []byte)` added. Wired in `cmd/them/main.go` with `cfg.JWTSecret || cfg.SecretKey`.
- `go/internal/runrecorder/recorder.go`: `user_id` written in `CreateRun` INSERT ($10 arg)
- `go/internal/history/pgx.go`: all 5 functions gain `userID int64` parameter; dual-column SQL filter: `AND ($3 = '' OR t.external_user_id = $3) AND ($3 != '' OR $4 = 0 OR t.user_id = $4)`. `resolveRootTaskID` INSERT gains `user_id` column.
- `go/internal/orchestrator/orchestrator.go` + `summary.go`: interfaces + call sites updated for new signatures
- `go/internal/temporal/workflow.go` + `activities.go`: `UserID int64` in `WorkflowInput` and `RunContext`
- `go/internal/ws/handler.go` + `go/internal/sse/handler.go`: `UserID: handle.UserID` in `WorkflowInput`

**AccessModeUser authorization rule (documented):**  
Any authenticated member of the EP's tenant can invoke any `AccessModeUser` entry point. JWT validity + tenant match is the only gate. `allowed_principals` (Phase 3) is the scheduled fix for per-EP principal restrictions.

**History isolation invariant (dual-column):**
- `externalUserID != ""` → filter by `t.external_user_id` (external/backend runs)
- `externalUserID == ""` AND `userID != 0` → filter by `t.user_id` (internal THE-M user runs)
- Both empty → no user filter (service-token / anonymous runs)
- Legacy rows (both NULL) excluded from user-scoped queries by SQL NULL semantics ✓

**Tests added:**
- `internal/execution/lifecycle_test.go`: `TestAccessModeUser_ValidJWT_Admitted`, `TestAccessModeUser_InvalidJWT_Rejected`, `TestAccessModeUser_TenantMismatch_Rejected`, `TestAccessModeUser_NoToken_Rejected`, `TestAccessModeUser_NoSecret_Rejected`, `TestAccessModeUser_UserIDStoredOnRun`
- `internal/history/pgx_test.go`: `TestHistory_UserA_CannotReadUserB`, `TestHistory_InternalCannotReadExternalUser`, `TestHistory_LegacyRows_NotLeakedToUser`; existing tests updated to match new SQL signatures

**`go test ./...` — 54 packages, 0 failures. HEAD: 77bb7d0**

### End-user auth Phase 2 — Closure fixes (326f5a9, 2026-09-08)

Three issues identified in code review of ef51f6b, all resolved:

1. **RuntimeLogin endpoint** — `end_user`-role accounts can now authenticate via `POST /api/v1/auth/runtime-login` (username+password only). Skips `dashboard_access` gate; does NOT set dashboard cookies. 6 tests (`TestRuntimeLogin_*`).

2. **Refresh-token rejection** — `ValidateHS256JWT` now rejects tokens where `type != "" && type != "access"`. Prevents refresh tokens (same HMAC key, `type="refresh"`) from being used as bearer credentials at `AccessModeUser` EPs. 2 tests (`TestValidateHS256JWT_RefreshTokenRejected`, `TestValidateHS256JWT_AccessTokenAccepted`).

3. **History isolation tests** — 3 SQL-string-inspection tests replaced by `go/internal/history/pgx_integration_test.go` (build tag `integration`). 5 real integration tests call actual `Store.WriteMessage` + `Store.LoadHistory` against live PostgreSQL. Run: `go test -tags=integration ./internal/history/...` (requires `DATABASE_PASSWORD`).

**`go test ./...` — 54 packages, 0 failures. HEAD: 326f5a9**

See full detail in `docs/HANDOVER.md`.

### End-user auth Phase 2 — E2E CONFIRMED (7e107ab, 2026-09-08)

`user_id` persisted on `them.runs` confirmed via live WS invocation:
- Created `user_jwt` EP, `end_user` account, runtime-login → JWT `sub=38`
- WS connected, message sent, run created: `d0e50a33|user_id=38||check-uid-a321e8c9`
- Queried DB **before** user deletion → `user_id=38` present on run row
- `them.tasks.user_id` is empty because the run status was `failed` (orchestrator started but Temporal worker did not complete activity — expected, no LLM agent active during smoke test). `tasks.user_id` is written by the Temporal worker; bridge half is verified.
- FK `ON DELETE SET NULL` was masking all previous checks — root cause documented in `docs/HANDOVER.md`

**Phase 2 is CLOSED. All closure fixes + E2E runs.user_id confirmed.**

### End-user auth Phase 3 — COMPLETE (2026-09-08)

**`allowed_principals` principal type guard on entry points.**

- Migration `db/088_allowed_principals.sql` — applied to live DB
- `epconfig.go`: `AllowedPrincipals string` on `EPConfig`/`EPConfigRow`; `ErrPrincipalNotAllowed`; `CheckPrincipal(cfg, isBackend)` function; `buildConfig` normalises unknown → `"internal"`
- `pgx.go`: `COALESCE(ep.allowed_principals, 'internal')` in `epConfigQuery`
- `lifecycle.go`: step 5c — `CheckPrincipal` after `CheckAccess`; returns `AdmitErrForbidden` on mismatch
- Admin DAL: `AllowedPrincipals` on `EntryPoint`, `EntryPointRow`; `ListEntryPoints` + `UpsertEntryPoint` updated
- 17 new tests (EC-AP-01..10 in `epconfig_test.go`, LC-AP-01..07 in `lifecycle_test.go`)
- All 54 packages pass, 0 failures

**✅ Verification gate CLOSED (2026-09-09)** — `tasks.user_id` confirmed via live Temporal run with a2a-echo agent. User A (id=47): `runs.user_id=47` ✅; User B (id=48): `tasks.user_id=48` ✅. Root cause of previous NULLs: bridge image was 4 minutes older than Phase 2 code commit — stale binary, not a code bug. Bridge rebuilt from HEAD, gate closed. See `docs/HANDOVER.md`.

**`user_jwt` EPs are safe to enable for production end-users.**

---

### Tenant Management Fixes — COMPLETE (2026-09-09, c078e6d)

**Force-delete, IdP badge persistence, OIDC redirect URI correction, Keycloak tenant setup.**

#### Tenant force-delete (aa72059 + 304e442 + 18f9d72)

- Modal shows resource counts (applications / agents / users) before deleting
- Admin must enter their own password to unlock the "Delete everything" button
- `ForceDeleteTenant` now deletes all 9 FK-blocked child tables in dependency order:
  `run_artifacts → runs → access_tokens → orchestrators → component_definitions → application_definitions → applications → agents → memberships` — then the tenant row
- Error from force-delete is shown inside the modal (not on a different tab)
- Password verification uses the logged-in user's username (not hardcoded `'admin'`)

#### IdP badge persistence (9f18da3)

- **Root cause:** `GET /admin/tenants` returned `Tenant` (no `idp_configured` field) — badge appeared after Save (PATCH response has it) but vanished on refresh
- Fix: `ListTenants`, `GetTenant`, `CreateTenant` all now return `idp_config IS NOT NULL AS idp_configured`; `Tenant` struct gained `IDPConfigured bool`
- Test mock updated for new 9-column scan shape

#### OIDC redirect URI (c078e6d)

- **Root cause:** Keycloak client had `redirectUris: ["/auth/oidc/callback"]` but tenants stored `/auth/api/v1/auth/oidc/callback` (stale Python path)
- Fix: updated bank + rnd `idp_config.redirect_uri` in DB to `/auth/oidc/callback`
- Keycloak `them-m` client updated (both `localhost` and `10.55.125.43` variants)
- `keycloak/them-realm.json` fixed so container recreation uses correct URIs

#### Keycloak tenants configured (live DB, not committed)

| Tenant | Slug | IdP | Group mappings |
|---|---|---|---|
| Default Tenant | `default` | — | — |
| Bank | `bank` | Keycloak `them-m` | `bank-admins` → admin |
| R&D | `rnd` | Keycloak `them-m` | `developers` → admin, `qa` → member |

**Keycloak admin console:** `http://<host>:8088/auth/keycloak/` — user `admin` / `admin123`

Each tenant has its own Keycloak realm — fully isolated user databases. `unmatched_action=deny` on both tenants — users from the wrong realm cannot enter.

**`bank` realm** → `bank` tenant
- Discovery URL: `http://<host>:8088/auth/keycloak/realms/bank`
- Client ID: `them-m` / Secret: `them-m-bank-secret`
- Redirect URI: `http://<host>:8088/auth/oidc/callback`

| Username | Email | Group | Password | Tenant role |
|---|---|---|---|---|
| `bankadmin` | `bankadmin@bank.com` | `bank-admins` | `bankadmin` | admin |
| `avi2` | `avi2@bank.com` | `bank-admins` | `avi2pass` | admin |

**`rnd` realm** → `rnd` tenant
- Discovery URL: `http://<host>:8088/auth/keycloak/realms/rnd`
- Client ID: `them-m` / Secret: `them-m-rnd-secret`
- Redirect URI: `http://<host>:8088/auth/oidc/callback`

| Username | Email | Group | Password | Tenant role |
|---|---|---|---|---|
| `dev` | `dev@rnd.com` | `developers` | `devpass` | admin |
| `qa-user` | `qauser@rnd.com` | `qa` | `qa` | member |

**`them` realm** — used by `default` tenant only for legacy testing (not connected to any tenant IdP)

**`default` tenant** — no IdP configured. Local login only: `admin` / `admin123`. Emergency backdoor — never put this on SSO.

Start Keycloak (if not already up):
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile sso up -d them-keycloak
```

Test SSO login: Admin → Tenants → bank or rnd → SSO tab → "Test SSO Login".

⚠️ **Keycloak realm config is runtime state** — realms `bank` and `rnd` were created via kcadm and are stored in the `keycloak-data` Docker volume. If the volume is wiped, re-run the realm setup from `docs/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md`.

---

### LLM Provider Support — COMPLETE (2026-09-09, 4d4b232)

**New providers:** OpenAI-compatible wire format (streaming SSE, `delta.content`, tool_calls accumulation) wired into all three Go workers.

| Provider | Type | Auth |
|---|---|---|
| `anthropic` | Cloud | API key |
| `openai` | Cloud | API key |
| `groq` | Cloud | API key |
| `gemini` | Cloud | not yet wired (listed, key stored, no provider impl) |
| `ollama` | Self-hosted | none (sentinel key) |
| `vllm` | Self-hosted | optional |
| `lmstudio` | Self-hosted | optional |
| `elevenlabs` | Cloud / voice | API key |

**New files / key changes:**
- `go/internal/llm/openai.go` — `OpenAIProvider`: streaming SSE, configurable `baseURL`, skips `Authorization` header when `apiKey == "ollama"`. 9 tests (OAI-1..9).
- `go/internal/temporal/workerconfig/loader.go` — `RunConfig` gains `LLMBaseURL` + `SummarizerBaseURL`; DB fetch includes `base_url` column.
- `go/cmd/worker/main.go`, `go/cmd/agent-runtime/llm.go`, `go/cmd/dag-worker/main.go` — new cases for openai/groq/ollama/vllm/lmstudio.
- `go/internal/admin/service/applications.go` + `dal/applications.go` + `admin/applications.go` — `SetProviderKey` accepts `baseURL`; `UpsertProviderBaseURL` / `GetProviderBaseURLs` DAL methods; local providers may omit API key.
- `frontend/src/app/admin/applications/constants.ts` — `CLOUD_PROVIDERS_LIST`, `LOCAL_PROVIDERS_LIST`, model lists for all new providers.
- `frontend/src/app/admin/applications/components/RuntimeView.tsx` — Provider Keys section split into two collapsible groups (Cloud / Self-hosted); local providers show Endpoint URL input with per-provider default placeholder; configured local providers show host:port in badge instead of key hint.

**Ollama setup (3 steps):**
1. Set Endpoint URL to `http://<host>:11434` (leave API key blank)
2. Hit Save — ollama sentinel key stored, URL persisted
3. Configure orchestrator to use `ollama` provider + model name (e.g. `llama3.2`)

**All 53 Go packages pass. HEAD: 4d4b232**

---

### Next recommended task

**Phase 4 — Bank JWT / JWKS validation at runtime entry points** (see `docs/END_USER_AUTH_PLAN.md`)

End-user auth phases 1, 2, 3, and 5 are all complete. Phase 4 is the only remaining gap.

**What it enables:** A bank customer presents their own bank-issued JWT (from the bank's Keycloak/Auth0/etc.) directly to a WS/SSE entry point — no the-M account needed. The-M validates the JWT via JWKS, extracts `sub` as `external_user_id`. The bank manages its own users in its own IdP.

**Confirmed complete as of 2026-09-10 (verified in code):**
- Managed apps (Phase 5 of end-user plan) ✅ — `epConfigQuery` OR-clause on `managed_app_bindings`; consuming tenants can reach platform-owned EPs.
- Billing attributed to consuming tenant ✅ — `billingTenantID = req.TenantID` in `lifecycle.go` (commit `b660657`).
- The "known gap" from the previous entry is resolved — `lifecycle.go` uses `req.TenantID` (consuming tenant), not `resolvedCfg.TenantID` (platform tenant).

**Phase 4 scope:**
1. `db/09x_tenant_runtime_config.sql` — new table: `jwks_uri`, `issuer`, `audience`, `claim_mappings` per tenant
2. `JWKSAuthenticator` in `internal/auth/` or `internal/epconfig/` — validates RS256/ES256 JWTs from external IdPs at entry points (separate from the existing OIDC SSO dashboard flow)
3. `AccessModeExternal = "external_jwt"` constant + `Lifecycle.Admit` step
4. Tenant-admin UI — configure runtime JWKS URI / issuer / audience in tenant settings
5. Tests using live Keycloak as the bank IdP

**Three end-user flows that work today (without Phase 4):**
1. Backend-mediated — bank's backend holds an `is_backend=true` token, passes `X-External-User: customer-id` header
2. the-M `end_user` account — create account per customer, use `/auth/runtime-login` to get JWT, connect to `AccessModeUser` EPs
3. Managed app — consuming tenant binds to platform-published app; quota + billing charged to consuming tenant

Key reminders:
- Migration 081 (`db/081_tenant_group_mappings_safe_roles.sql`) — verified applied via migration 089 (2026-09-09) ✅
- Every Go change → `cd go && go test ./...` (must be zero failures before commit).
- Phase 1 Redis key patterns documented in `docs/REDIS.md` (Metrics Keys section).

### Known blockers / pre-conditions

- **THEM_DB_URL_APP and THEM_DB_URL_ADMIN must be in `.env`** before restarting containers — run `./generate-env.sh` if not present. The binaries will now fail fast if these are missing.

Currently running containers (verified):
```
them-go-bridge        ✅ healthy
them-go-worker        ✅ running (no profile — default service)
them-auth-go          ✅ healthy
them-agent-runtime-1  ✅ healthy (port 9300)
them-agent-runtime-2  ✅ healthy (port 9300)
them-frontend         ✅ running
them-postgres         ✅ healthy
them-redis            ✅ healthy
them-traefik          ✅ healthy
temporal-frontend     ✅ (with --profile temporal)
them-bridge (Python)  ❌ REMOVED — not in docker-compose.yml
them-worker (Python)  ❌ REMOVED — not in docker-compose.yml (them-orchestration queue empty; all traffic on them-orchestration-go)
them-dag-worker (Go)  ✅ Running — polls canvas-dag-nodes for CanvasAgentWorkflow
```

---

## Go route ownership (all confirmed via Traefik labels)

All routes are owned by `them-go-bridge` (`them-go-bridge-svc`, port 8002).
A catch-all router at priority 90 (`them-go-catchall`, `PathPrefix /`) ensures all unmatched paths reach Go.
Explicit routers at priority 110–150 still win over the catch-all.

### Admin — read
- `GET /api/v1/admin/agents` (list)
- `GET /api/v1/admin/orchestrators` (list)
- `GET /api/v1/admin/applications` (list)

### Admin — write
- `POST /api/v1/admin/agents` — create
- `PUT|PATCH|DELETE /api/v1/admin/agents/{id}` — update/delete
- `POST /api/v1/admin/agents/discover`, `/agents/{id}/test`, `/agents/{id}/security-scan`
- `POST|PUT|PATCH|DELETE /api/v1/admin/orchestrators/{name}`
- `POST /api/v1/admin/applications`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}`
- `POST /api/v1/admin/applications/{id}/entry-points`
- `PUT|PATCH|DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}`
- `PathRegexp /api/v1/admin/applications/{id}/.+` — all methods (covers provider-keys, runtime, agent-bindings subroutes)

### Admin — full ownership
- `GET /api/v1/admin/observability/summary` — cross-tenant aggregate (RequireSuperAdmin, Admin BYPASSRLS pool)
- `PathPrefix /api/v1/admin/system-agents` — all methods
- `PathPrefix /api/v1/admin/tokens` — all methods
- `PathPrefix /api/v1/admin/sessions` — all methods
- `PathPrefix /api/v1/admin/component-definitions` — all methods
- `PathPrefix /api/v1/admin/agent-definitions` — all methods
- `GET|PUT /api/v1/admin/llm-providers/routing/config`
- `PathPrefix /api/v1/admin/llm-providers` — all methods
- `GET|POST|PATCH|DELETE /api/v1/admin/monitoring-config`

### Runs
- `GET /api/v1/runs` (list)
- `GET /api/v1/runs/stats`
- `GET /api/v1/runs/{id}` (detail)
- `GET /api/v1/runs/{id}/tasks`
- `GET /api/v1/runs/{id}/artifacts`
- `PATCH /api/v1/runs/{id}/cancel`
- `DELETE /api/v1/runs/{id}`
- `POST /api/v1/runs/bulk-delete`
- `POST /api/v1/runs/{id}/signal`

### App entry points (WS/SSE/A2A/Voice)
- `GET /apps/{app_slug}/{ep_slug}/ws`
- `GET|POST /apps/{app_slug}/{ep_slug}/sse`
- `POST /a2a/{app_slug}/{ep_slug}` (A2A JSON-RPC)
- `GET /a2a/{app_slug}/{ep_slug}/.well-known/agent.json` (per-agent card)
- `GET|POST /apps/{app_slug}/{ep_slug}/voice/chat|stream|transcribe|tts`
- `GET /ws/orchestrate/{orch}/{ep}` (two-segment legacy path)
- `GET|POST /sse/orchestrate/{orch}/{ep}` (two-segment legacy path)

### Dashboard
- `GET /ws/dashboard`

### Health
- `GET|HEAD /health/live`, `/health/ready`


### Not in Go (no handler or Traefik route)
- `GET /api/v1/admin/users`, `/roles`, `/teams` — auth admin CRUD (served by `them-auth-service` on port 8701 directly from frontend; no Go handler needed unless we want to proxy it)
- `GET /runs/context/{ctx}/artifacts` — not used by admin UI
- Applications export/import/restore — Python-only, not migrated

---

## DB schema state (live)

All migrations applied through `db/072_rls_phase_c.sql` (Step 19 Phase C complete):

| Migration | Status |
|---|---|
| `db/001_schema.sql` through `db/027_*` | ✅ applied |
| `db/028_entry_points_tenant_scoped_slug.sql` | ✅ applied |
| `db/029_component_registry_foundation.sql` | ✅ applied |
| `db/030_component_subtype_adoption.sql` | ✅ applied |
| `db/031_phase_c_compiler_pins.sql` | ✅ applied |
| `db/032_ep_memory_config.sql` | ✅ applied — `entry_points` has 6 memory columns; `tasks.tenant_id` exists |
| `db/033_*` through `db/034_*` | ✅ applied |
| `db/035_agent_definitions.sql` | ✅ applied — `agent_definitions` table exists |
| `db/036_canvas_a2a_runtime.sql` | ✅ applied — `agent_runtime_specs` + `app_agent_bindings` exist |
| `db/037_agents_transport_canvas.sql` | ✅ applied — `agents_transport_check` includes `'canvas_a2a'` |
| `db/038_app_agent_params.sql` | ✅ applied — `app_agent_bindings.agent_params` JSONB column |
| `db/045_app_global_params.sql` | ✅ applied — `applications.app_params` JSONB column |
| `db/048_application_slug.sql` | ✅ applied — `applications.slug` column, `UNIQUE(tenant_id,slug)`, EP uniqueness relaxed to `UNIQUE(application_id,slug)` |
| `db/049_ep_agent_card.sql` | ✅ applied |
| `db/050_middleware_pipeline.sql` | ✅ applied — `run_artifacts` scan columns, `middleware_jobs`, `middleware_audit`, `applications.security_config` |
| `db/051_quarantine.sql` | ✅ applied — `quarantine_artifacts` table, `run_artifacts.data` nullable + `storage_key` column, `middleware_jobs.quarantine_id` |
| `db/052_middleware_jobs_nullable_artifact.sql` | ✅ applied — `middleware_jobs.artifact_id` made nullable; FK re-added allowing NULL; fixes quarantine-first FK violation |
| `db/053_*` through `db/071_rls_phase_b.sql` | ✅ applied — B1+B2 RLS on mcp_servers/tenant_group_mappings/agent_definitions/agent_runtime_specs |
| `db/072_rls_phase_c.sql` | ✅ applied — C2 RLS on agents/orchestrators/applications/entry_points/access_tokens |
| `db/073_rls_phase_d.sql` | ✅ applied — D2 EXISTS-based RLS on app_agent_bindings/app_orchestrators/app_mcp_credentials/middleware_wirings |

---

## Test state

```
go test ./...  — 53 packages, 0 failures (verified 2026-09-02, Session C scan subscriber)
S1-84: 6 gate tests (quarantine-first)
S1-85: 4 admin security_config handler tests
S1-87: 25 middleware pipeline + AV scanner tests
S1-88: 5 job DAL quarantine-path tests
S1-89: 3 storage client tests
S1-30: 16 artifact download handler tests (MinIO path, 410 infected, MinIO error 500)
S1-90: 4 orchestrator scan subscriber tests (FileScanningEvent, Clean, Infected, Timeout)
S1-14: 30 A2A server tests
S1-72..S1-83: all prior DAG/canvas/A2A tests passing
S2-06: 3 integration-tagged Temporal E2E tests
Total go test ./...: 946

Live e2e confirmed 2026-08-23:
  - run 23aeb8bf: streaming single zip artifact via a2a-stream ✅
  - run 5691b24a: streaming two files (HTML + zip) via a2a-stream ✅
App global params: e2e validated 2026-08-25 — GET/PUT/DELETE live ✅
```

---

## A2A feature state — COMPLETE AND LIVE VERIFIED (2026-08-23)

| Scenario | Agent | Status |
|---|---|---|
| Sync single file | `docu-writer` (HTML/PDF/MD) | ✅ live |
| Streaming single file | `a2a-stream` v1.1 | ✅ live (run 23aeb8bf) |
| Streaming multi-file | `a2a-stream` v1.2 | ✅ live (run 5691b24a) |

### Playground + Artifacts tab
- Artifacts tab renders all file types: `image/*` → `<img>`, `application/pdf` → iframe,
  `text/html` → srcDoc iframe, `text/markdown`/text → `<pre>`, unknown → download
- `ArtifactPart.data` (base64) in Go DAL + frontend API types for binary transport
- Binary artifacts base64-encoded in `GetRunArtifacts` for transport to browser

### Multi-artifact (A2A spec compliant)
- `extractA2AResult` loops ALL artifact objects and ALL parts within each
- Single file → backward-compat `{"artifact":{}}` shape
- Multiple files → `{"artifacts":[...]}` plural shape
- Orchestrator fans out each artifact to `emitArtifactEvent` + `run_artifacts` independently
- Strips both keys before LLM sees the result

### Streaming (SendStreamingMessage / SSE)
- `AgentConfig.SupportsStreaming` set from agent card `capabilities.streaming` on discover
- `invokeA2AStreaming`: `bufio.Scanner` SSE reader, `onArtifact` callback per `lastChunk:true` event
- Wire format: `"role":"ROLE_USER"` (string, not int), camelCase JSON tags (`artifactUpdate`, `lastChunk`)
- Non-streaming agents fall through to `InvokeForRun` transparently
- **All worker replicas must be rebuilt together** — Temporal load-balances across all workers on same task queue

### docu-writer agent
- Model: `claude-haiku-4-5-20251001` (async) — ~15-25s vs ~84s with sync Sonnet
- Formats: `html`, `markdown`, `pdf` (fpdf2)
- PDF: Claude → Markdown → fpdf2 → `bytes(pdf.output())` → `part.raw` (NOT `part.data`)
- `part.data` is `google.protobuf.Value` (JSON only) — cannot hold binary bytes
- Markdown fence stripping applied before rendering

### a2a-stream test agent (v1.2.0)
- Streams ~16 text words word-by-word (0.1s apart)
- Emits `stream_report.html` (`text/html`, text part with filename)
- Emits `stream_output.zip` (`application/zip`, raw bytes via `part.raw`)
- `capabilities.streaming: true` → `supports_streaming=true` set automatically on discover

---

## Canvas A2A Agent Builder — all phases complete

| Phase | What | State |
|---|---|---|
| A | Step config panels (LLM/HTTP/Transform/Response/Input forms) | ✅ |
| B | Skill editor, node library, data-flow subtitles, round-trip serialization | ✅ |
| C | `kind:"data"` part input mode; variadic `extraVars` in interpreter | ✅ |
| D | `a2a-go/v2` SDK replaces hand-rolled JSON-RPC dispatch; 12 tests (S1-53) | ✅ |
| Compiler | `go/internal/agentgen/compiler.go` — Compile + topoSort + DFS cycle detection | ✅ |
| Publish | `go/internal/admin/service/agent_definitions_publish.go` — compile + 3-table atomic CTE | ✅ |
| Binding UI | `AgentCredentialPanel` in applications page — per-slot credential entry | ✅ |
| Runtime wiring | `InvokeForRun` in `agentregistry` + `GetBindingID` | ✅ |
| Debug mode | Browser-side pipeline step-through with per-session provider+model+key (all 4 providers) | ✅ |
| Bug fixes | polJSON unmarshal, AllowedSkillIDs enforcement, skill selection by ID, slug cache | ✅ |
| BuildValidator UI | Debounced backend validation, node/field highlighting, issues panel, Publish gate | ✅ |
| Data-flow contracts | `VarRef`, `DeriveInputs/DeriveOutputs` on all 11 nodes, Stage 5 path-sensitive `validateDataFlow` | ✅ |
| Frontend spec consumer | `AgentValidationReport.StepContracts`; RightPanel READS/WRITES from compiled contract post-validate | ✅ |
| Explicit bindings Stage A | `PortDef`, `VarRef.SourceStep/SourcePort`, `Binding`/`canvasStep.Inputs`, `resolveBindings`, `validateBindings`, `BROKEN_BINDING`; backward-compat | ✅ |
| Explicit bindings Stage B | `api.ts` Binding/VarRef/AgentStepDoc.inputs; `nodeRegistry.ts` PortDef/input_ports/output_ports; `types.ts` StepData.inputs; `page.tsx` save/load round-trip | ✅ |
| Explicit bindings Stage C | StepNode data-port handles (orange input squares, indigo output squares); DataEdge dashed wire; onPipeConnect data-edge branch; isPipeConnectionValid skips data edges; both save paths derive inputs from data edges; load path reconstructs data edges from step.inputs | ✅ |
| Multi-port Phase 1+2 | NodeDef as single source of truth: `PortDef.Color/MaxConnections`, `ControlOutputPorts`, `DynamicOutputSource` in Go registry; `resolveInputPorts`/`resolveOutputPorts` in nodeRegistry.ts; StepNode zero-conditional rewrite; branch true/false named control handles; transform dynamic output ports from config | ✅ |
| Multi-port Phase 3 | BundleEdge: groups data edges between same node pair into port-rail cable visual; EdgeLabelRenderer dots+labels; count badge; `applyBundleGroups()`; data handles invisible (1×1) — geometry only; `useStore(s.edges)` for wired-port detection; Dagre height scales with port count | ✅ |
| Unified port model | All flow (→) and data ports in one unified hover-reveal list per side; no separate ctrl-in/ctrl-out center handles; `PortDot` scale+opacity CSS transition; wired ports permanently visible; branch gets named true/false flow out ports; port alias rename in RightPanel READS section | ✅ |
| Canvas port UX clean rewrite | Removed all backward-compat code (PORTS_V2 flag, PortDot component, breathing animation, legacy paths). `PortsPopover` as absolutely-positioned child of `StepNode`; ctrl handles as always-visible 18px circles with ‹/› arrows. `BundleEdge` rewritten: single bezier + circular N-badge + MappingSheet popover; `callDeleteMapping` module registry for delete callbacks. `resolveOutputPorts` always includes static ports. | ✅ |
| Canvas port bug fixes (cd9632f) | CRITICAL: PortsPopover duplicate Handle IDs → display-only (no `<Handle>` JSX). HIGH: PortsPanelContext broadcast closes all popovers on node/pane click. MEDIUM: card height now counts only data ports. LOW: dead `hasCtrlIn`/`hasCtrlOut` vars removed. | ✅ |

### Key security constraints (always in force)
- Credentials decrypted per-request, held only in `InvocationContext.Credentials`, never logged/persisted
- Port 9300 NOT in Traefik — agent-runtime is internal Docker network only
- `TaskState` has no credentials — re-decrypted from binding on each resume
- Binding invariant: `DefinitionID` pinned at publish, mismatch → 409

---

## LLM provider support (current) — commit f527ccf

**Fully wired providers (orchestrator worker + canvas agent runtime + dag worker):**

| Provider | Type | Notes |
|---|---|---|
| `anthropic` | Cloud | Native Anthropic Messages API |
| `openai` | Cloud | OpenAI Chat Completions API |
| `groq` | Cloud | OpenAI-compatible endpoint |
| `ollama` | Local | No auth header; set `base_url` in `them.llm_providers` |
| `vllm` | Local | OpenAI-compatible; set `base_url` in `them.llm_providers` |
| `lmstudio` | Local | OpenAI-compatible; set `base_url` in `them.llm_providers` |

**Not yet wired (UI lists them, backend will error):**
- `gemini` — different wire format; needs a dedicated implementation

**Key architecture:**
- Per-app keys stored in `applications.provider_keys` JSONB (AES-GCM encrypted)
- Format: `{"anthropic": {"ct": "enc:...", "hint": "XXXX"}}` (new) or `{"anthropic": "sk-ant-..."}` (legacy flat)
- **No global key fallback** — apps with no key get an explicit error (non-retryable Temporal failure), except `ollama` (unauthenticated — empty key is allowed)
- `them.llm_providers.base_url` is now read at run resolution time and threaded into `RunConfig.LLMBaseURL` / `RunConfig.SummarizerBaseURL`
- Worker: `resolveProvider` + `resolveSummarizerProvider` dispatch on provider name
- Agent-runtime / dag-worker: `multiLLMFactory.baseURLs` map carries per-provider custom endpoint URLs
- UI: Runtime tab → provider + model + API key per app; `ollama/vllm/lmstudio` appear in provider dropdowns

**To add a local LLM (e.g. Ollama):**
1. Insert a row into `them.llm_providers`: `name='ollama'`, `base_url='http://host.docker.internal:11434/v1'`, `api_key_encrypted=NULL`, `default_model='llama3.2'`
2. Set the app's Runtime provider to `ollama` and leave the API key field empty (or enter `ollama`)
3. Rebuild and restart `them-go-bridge`, `them-go-worker`, `them-agent-runtime`

---

## App-level agent params — fully complete (2026-08-23)

Backend + frontend + tests all done. See commits around 2f29cd3.

---

## App-level global named parameters — fully complete (2026-08-25)

**All 4 phases shipped (commits d2c4283..1e5c76c). DB migration applied.**

What was built:
- `db/045_app_global_params.sql` — `applications.app_params` JSONB column (AES-GCM secrets / plaintext non-secrets)
- DAL: `GetAppParams`, `SetAppParam`, `DeleteAppParam` on `*DB`
- Service: `GetAppParams`, `SetAppParam`, `DeleteAppParam`, `GetPlaintextAppParams`
- REST: `GET/PUT/DELETE /admin/applications/{id}/app-params[/{name}]`
- Compiler: `collectAppParamRefs` → `AgentSpec.AppParamRefs`; `AppParamRef` on `HTTPStepConfig`; `ModelOverrideParamRef` on `LLMStepConfig`
- Interpreter: `AppParamRef` (global) takes precedence over `AppParamKey` (per-binding); `injectAuthParam` helper; `ModelOverrideParamRef` takes precedence over `ModelOverrideParamKey`
- Runtime: `decodeAppGlobalParams` pure helper (testable); `loadAppGlobalParams` wired into `handle()` after `AgentParams`; fixed `plain:` prefix check (was dead code in error branch)
- Frontend (`api.ts`): `AppGlobalParam` interface + `getAppParams`/`setAppParam`/`deleteAppParam`
- Frontend (`RuntimeView.tsx`): "App Global Parameters" section — add/update/remove; type selector; secret masking
- Frontend (`RightPanel.tsx`): HTTP node → toggle Per-binding / App-global source; LLM node → `model_override_param_ref` free-text field
- Tests: S1-62 (AGP-1..8 service), S1-63 (CMP-10..14 compiler), S1-64 (INT-10..14 interpreter), S1-65 (RT-20..24 runtime), S1-66 (HTTP-20..25+ handler) — 34 new tests

**DB migration must be applied before deploying:**
```bash
docker cp db/045_app_global_params.sql them-postgres:/tmp/045.sql
docker exec them-postgres psql -U them -d them -f /tmp/045.sql
```

**Containers to rebuild after deploying:**
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge them-agent-runtime
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge them-agent-runtime
```

---

## Canvas Agent LLM Node Overrides — fully complete (2026-08-25)

**Removes old `model_override_param_ref`/`model_override_param_key` mechanism entirely.**
**No DB migration needed — uses existing `app_agent_bindings.config_overrides` JSONB column.**

What was built:
- `AgentSpec.LLMNodes []AgentLLMNodeSpec` — compiler collects all LLM steps (provider+model from canvas config) via `collectLLMNodes`; old `AppParamRefs`/`AgentAppParamRef` removed
- `InvocationContext.NodeLLMOverrides map[string]NodeLLMOverride` — per-node override map; loaded from `config_overrides["llm_nodes"][nodeID]` in agent-runtime via `extractNodeLLMOverrides`
- Interpreter `execLLM` reads `NodeLLMOverrides[step.ID]` before falling back to compiled provider+model
- DAL: `GetAgentLLMNodes` (reads spec `llm_nodes` + binding override map) + `UpsertNodeLLMOverride` (jsonb_set into `config_overrides["llm_nodes"][nodeID]`)
- Service: `GetAgentLLMNodes` + `PutNodeLLMOverride` (validates non-empty)
- REST: `GET /admin/applications/{id}/agents/{agent_id}/llm-nodes` + `PUT /agents/{id}/llm-nodes/{node_id}`
- Frontend proxy routing: added `/agents/[^/]+/llm-nodes` pattern to Go bridge
- RuntimeView: "Canvas Agent LLM Nodes" section — one card per LLM node, provider+model dropdowns (all 4 providers; ✓ marks those with saved key), Save per node
- RightPanel: removed MODEL OVERRIDE (APP GLOBAL PARAM) panel from LLM canvas node config
- Debug panel: replaced hardcoded `__anthropic_key` with `__debug_provider` (dropdown) + `__debug_model` (dropdown) + `__debug_api_key` (password) for session-level override
- Debug proxy (`/api/debug/llm`): now supports anthropic, openai, groq, gemini; normalizes all responses to Anthropic `content[{type,text}]` format for frontend consumption

**Action required after deploy:** Re-publish any canvas agents to get `llm_nodes` populated in the spec.

---

## MCP-1 Migration Status

**Completed in this session:**
- `them-mcp-service` binary — `go/cmd/mcp-service/main.go` + `go/internal/mcp/` (all files: config, dal, client, registry, leader, supervisor, health, executor, server, health_test)
- `Dockerfile.mcp-service` + `docker-compose.yml` service entry (port 8010, Traefik disabled)
- DB migrations applied: `db/041_mcp_servers.sql` (them.mcp_servers) + `db/042_mcp_app_credentials.sql` (them.app_mcp_credentials)
- Admin CRUD API: `go/internal/admin/dal/mcp_servers.go`, `go/internal/admin/service/mcp_servers.go`, `go/internal/admin/mcp_servers.go`
- Service added to router: `go/internal/admin/router.go` (tenantScoped group)
- Dal interface updated: `go/internal/admin/service/service.go`
- All fake Dal structs updated: `service_test.go`, `agent_definitions_test.go`, `definitions_publish_test.go`, `tenant_isolation_test.go`
- 11 new unit tests: `go/internal/admin/service/mcp_servers_test.go`
- Docs updated: REDIS.md, SCHEMA.md, CLAUDE.md (trigger map + container map), TEST_INDEX.md (S1-62), CURRENT.md

**What's done (UI-1 — commit 7c0ec59):**
- `frontend/src/lib/api.ts`: MCPServer, MCPTool, MCPCredentialMeta types + 9 themApi methods
- `frontend/src/components/Sidebar.tsx`: MCP Store nav entry after Agents
- `frontend/src/app/admin/mcp-servers/page.tsx`: full card grid + centered modal properties panel (General + Status & Tools tabs) + tool manifest viewer + ProbeButton + CreateModal + Sidebar

**What's done (MCP-2 — commit 901dc36):**
- DB: `db/043_mcp_streamable.sql` — constraint updated to include `streamable-http`, drop `stdio`
- `go/internal/config/config.go`: `MCPServiceURL` field read from `MCP_SERVICE_URL` env
- `go/internal/admin/mcp_servers.go`: `Probe` handler proxies to `them-mcp-service /internal/probe/{id}`
- `go/internal/admin/router.go`: `BuildRouter` gains `mcpServiceURL` param
- `docker-compose.yml`: `MCP_SERVICE_URL=http://them-mcp-service:8010` added to `them-go-bridge`
- Frontend: transport list updated (`streamable-http` default, `http`/`sse` legacy, `stdio` removed)
- Frontend: auth info banner in Create modal explains per-app credential flow

**What's done (UI-2 — commit 900b8d4):**
- `frontend/src/app/admin/applications/components/MCPCredentialsView.tsx`: new — per-application MCP credential management (key-set badge, Save/Update/Remove, flash feedback)
- `AppCard.tsx`: MCP button added (indigo, `electrical_services` icon)
- `ListView.tsx` + `page.tsx`: `mcp-credentials` view state + `openMCPCredentials` handler

**What's done (UI-3 — commit 9bf1328):**
- `go/internal/agentgen/spec.go`: `StepMCPCall = "mcp_call"` constant added
- `go/internal/agentgen/nodes.go`: `mcp_call` stub NodeDef registered (label "MCP Tool", 🔌, single-input/output, Execute=nil)
- `go/internal/agentgen/noderegistry_test.go`: allStepTypes 11→12; KnownStepTypesCount 11→12; mcp_call in StubTypesHaveNilExecute
- `frontend/src/lib/nodeRegistry.ts`: `mcp_call` UI supplement (indigo accent, server/tool summary)
- `frontend/src/app/admin/agents/builder/components/RightPanel.tsx`: `mcp_call` properties panel — server dropdown, tool dropdown/input, args template, output var, credentials info banner
- `them-go-bridge` rebuilt and restarted — `mcp_call` now appears in `GET /admin/node-types`

**What's done (MCP-3 — commit cf882d8):**
- `spec.go`: StepMCPCall constant + MCPCallConfig struct
- `nodes.go`: mcp_call node registered with Validate, Execute, DeriveInputs, DeriveOutputs; 11→12 types
- `interpreter.go`: MCPCaller interface + WithMCPCaller; execMCP + renderMCPArgs
- `mcp_caller.go`: HTTPMCPCaller — POST /internal/execute on them-mcp-service (stateless per call)
- `cmd/agent-runtime/main.go`: wires MCPServiceURL from config into HTTPMCPCaller; nil when URL unset
- 10 new tests MCP-1..10; go test ./... 815→825

**What's done (MCP-3 E2E — commit e6f9660):**
- `docker-compose.yml`: `MCP_SERVICE_URL=http://them-mcp-service:8010` + `depends_on: them-mcp-service` in agent-runtime
- Full E2E validated 2026-08-27: create → validate → publish → bind → A2A `SendMessage` → agent-runtime → them-mcp-service `/internal/execute` → `get_latest_session` tool → result written to `session_data` var → artifact in A2A response
- Failure paths: unknown tool → 422, unknown server slug → 422
- `them-mcp-service` already runs without a profile (default service)
- A2A SDK v2.5: method is `SendMessage` (not `message/send`), params.message must have `messageId`

**MCP-3 is FULLY COMPLETE and live-verified.**

---

## Data-flow architecture — current state (2026-08-27)

### What's live
- **`VarRef` + `DeriveInputs/DeriveOutputs`** on all 11 node types in `go/internal/agentgen/nodes.go`
- **`StepSpec.Inputs/Outputs []VarRef`** (omitempty) — populated by compiler, absent from canvas JSON, present in compiled `AgentSpec`
- **Stage 5 `validateDataFlow`** — path-sensitive available-definitions lattice (intersection over predecessors). A var is guaranteed only if written on every execution path to the reading step. Branch convergence handled correctly.
- **`AgentValidationReport.StepContracts`** — validate endpoint returns compiled `{inputs, outputs}` per step
- **RightPanel READS/WRITES panel** — uses authoritative compiled contract post-validate (shows `✓ compiled contract`), falls back to `extractNodeVars` (heuristic) for live pre-validate UX
- **`nodeVars.ts`** — unchanged; still used for live edge labels and pre-validate UX

### Boundary: canvas JSON stays clean
`Inputs`/`Outputs` are NEVER written to canvas JSON (the `definition` column). They exist only in:
1. The compiled `AgentSpec` (persisted to `agent_runtime_specs.spec`)
2. The validate endpoint response (`step_contracts` field, transient)

### What remains (per DATAFLOW_EXPLICIT_FEASIBILITY.md)
- Step 6: **COMPLETE** (fa879b7) — ExposedVars removed from TransformStepConfig; DB data-migrated; frontend cleaned up
- Stage 6: **COMPLETE** (0edcf2a) — Scoped input resolution + output-only promotion in interpreter.executeStep; ErrContractViolation type; execTransform simplified; 12 new CONT tests
- Explicit bindings (wiring vars between steps with explicit edges) — **Stages A/B/C COMPLETE**: Go compiler (PortDef, resolveBindings, validateBindings, BROKEN_BINDING, 10 BND tests); TypeScript types; canvas port handles (orange data-in, indigo data-out) + DataEdge dashed wire; onPipeConnect data-edge path; save/load round-trip; backward-compat. Runtime unchanged.
- Structured per-var trace events — not yet (requires trace sink design)
- Temporal/ADK integration — not yet

---

## DAG Execution Engine — complete through Phase 3 + hardening (2026-08-29)

Goal: upgrade the Canvas execution engine from sequential-only to real DAG fan-out/join.

| Phase | What | State |
|---|---|---|
| 0 | `ExecutionPlan`/`PlanNode`/`JoinMode` types + `CompileExecutionPlan()` + 4 tests (S1-72) | ✅ commit `0d99d68` |
| 1 | `ExecutionBackend` interface + `LocalExecutor` (goroutine fan-out, wait_all join, deep-copy, cancel) + 6 tests (S1-73) | ✅ commit `f5737c0` |
| 2 | Race detector validation — `go test -race ./...` green; `Interpreter.clone()` fix for `nextStepOverride` contention | ✅ commit `ddaca40` |
| 3 | Canvas unlock — `max_out: 0` on LLM/HTTP/A2ACall/HumanWait/MCPCall in `nodes.go` | ✅ commit `ddaca40` |
| Hardening | Branch-aware joins (`JoinBranchMerge` vs `JoinWaitAll`); deterministic merge (predecessor-keyed map + JoinOf order); causal error preservation; 4 new tests (S1-72/73 expanded) | ✅ commit `82c5be4` |
| Compiler fix | `classifyJoin()` rewrite — fixes fan-out source vs fan-out target level confusion; MixedFanOut + BranchMerge tests | ✅ commit `d471f9f` |
| Wired | `cmd/agent-runtime/main.go` uses `LocalExecutor` (not sequential `Interpreter.Execute`) | ✅ commit `a9528d6` |
| E2E tests | `agentgen_test.go` — 3 smoke tests through CompileExecutionPlan + LocalExecutor + real node types (S1-74) | ✅ commit `bd2ffbd` |
| StepParallel | `StepParallel.Execute` implemented (no-op fan-out coordinator); removed from stub list | ✅ commit `b5767b0` |
| 4-A | `ExecutionBackend` field in `AgentSpec`; `ExecuteNodeForActivity` adapter; `ActivityIC`; `Interpreter.Clone()`; 16 tests (S1-75) | ✅ commit `a1adbe8` |
| 4-B | `CanvasAgentWorkflow` + `ExecuteStepActivity` + conformance tests CT-01..CT-10 + CT-A..CT-F in `internal/temporal/`; 16 tests (S1-76) | ✅ commit `68da87c` |
| Pre-4-C | Unified `ExecutionPolicy` — `NodeDef` defaults, compiler resolution, `LocalExecutor` timeout, Temporal policy wiring, NoResult bug fix; 13 new tests (EP-1..9, EP-L1/2, CT-EP1/2) | ✅ |
| Pre-4-C hardening | LocalExecutor retry loop + backoff; non-retryable short-circuit; idempotency guard; `RequiresIdempotencyKey` logic fix; frontend Execution Policy section in node Properties; 9 new tests (EP-2b, EP-L3..EP-L8) | ✅ |
| Pre-4-C parity | Per-attempt timeout, vars isolation, typed non-retryable, idempotency guard in activity path, method-aware UI defaults; 5 new tests (EP-L9..EP-L13) | ✅ |
| Pre-4-C final | MCP mutating hard-clamp; removed string-match from `isNonRetryable`; fresh interp clone per retry; 3 new tests (EP-10, EP-L14, EP-L15) | ✅ commit `3a8f0f6` |
| Pre-4-C concurrency | Per-run `MaxConcurrentTasks` semaphore in `LocalExecutor` + `CanvasAgentWorkflow`; `DAG_WORKER_MAX_CONCURRENT_ACTIVITIES` config; `ResolveMaxConcurrentTasks`; 5 new tests (CONC-1..5) | ✅ commit `df4b19e` |
| 4-C | `TemporalExecutor`, `them-dag-worker`, `agent-runtime` wiring, Docker service | ✅ commit `0b68dcb` |
| 4-C hardening | 7 production blockers fixed: Compose env vars, fail-closed, stable workflow ID, policy concurrency, tenant-scoped DB queries, bounded cancel, integration tests | ✅ commits `1c44aa0`..`30f9f95` |
| 4-C gap-2 | 5 additional fixes: tenant-scope ALL lookups, safe errors, conditional Temporal overlay, HumanWait 24h timeout, real full-path E2E, binding 4-ID enforcement | ✅ commits `8d815cc`..`b3bd71a` |
| 4-D | Frontend execution_backend toggle (Local / ⚡ Temporal pill in top bar) | ✅ commit `7d39d44` |
| 5-A | StepLoop — LocalExecutor + Temporal + frontend config panel + durable loop architecture + canvas ports + gap fixes (compileLoopBodyPlan JoinOf/JoinMode, ExecuteBody onTerminal, bodyIterState isolation, BFS boundary, accum scoping) + 8 new tests (EP-LOOP-6/7/8, CT-LOOP-DURABLE-6/7, PC-LOOP-4/5/6) | ✅ |
| 5-B | HumanWait async — Phase 1 (commit `3b1052f`): HITLStore, PlanHasHumanWait, CanvasSubmitter/Signaler, Submit/SignalCanvasStep, executeSkill HITL async path, signalHITL; Phase 2 hardening (commit `0487797`): HITLHandle 6-field state machine (tenant_id, wait_token, state), UpdateWaitToken/TrySignal CAS/MarkDone, deterministic wait_token (sha256, no uuid), hitl_status workflow query handler, per-step timeout via workflow.Select, loop-body HumanWait, HITLRequestHandler (GetTask/SubscribeToTask/CancelTask), RedisA2ATaskStore (SDK taskstore.Store), signal endpoint moved to JWT-auth admin router `/admin/canvas-tasks/{task_id}/signal`; 20 total tests (HS-1..11, RT-HITL-1..5, CSIG-1..4) | ✅ commit `0487797` |
| 5-C | A2A Call node — `A2ACaller` abstraction, `HTTPA2ACaller`, depth tracking, self-call rejection, HumanWait+local validation, agent-runtime + dag-worker wiring | ✅ |
| 5-D | StreamOut node — `execStreamOut`, `StreamOutStepConfig`, `STREAM_OUT_MISSING_FROM_VAR` validation, `DeriveInputs` | ✅ |

### DAG join semantics (hardening summary)
- **JoinWaitAll**: join node whose predecessors originate from a non-Branch fan-out (e.g. LLM with `len(Next)>1`). All branches always run — must wait for all.
- **JoinBranchMerge**: join node whose predecessors are ALL direct arm-children of a single Branch step. Only one arm runs — first arrival continues; subsequent arrivals are silently dropped.
- Detection in `classifyJoin()`: JoinBranchMerge requires (1) every predecessor has exactly one parent, (2) that parent is a Branch step, (3) all share the same Branch parent B, (4) B's full Next set == predecessor set. Anything else → JoinWaitAll.
- Merge is deterministic: `joinState.arrived` is `map[string]map[string]PipelineVars` (predecessor-keyed); merge iterates `JoinOf` slice in order — later entries win on key collisions.
- `drainFirstCausalError`: reads all errors from buffered chan, prefers first non-`context.Canceled` over Canceled. Causal error survives sibling cancellation.

### Key files
- `go/internal/agentgen/spec.go` — `ExecutionPlan`, `PlanNode`, `JoinMode` (JoinNone/JoinWaitAll/JoinBranchMerge/JoinWaitAny)
- `go/internal/agentgen/plan_compiler.go` — `CompileExecutionPlan()`, `classifyJoin()`, `NodeByID()`
- `go/internal/agentgen/executor.go` — `ExecutionBackend` interface
- `go/internal/agentgen/local_executor.go` — `LocalExecutor` with goroutine fan-out + per-goroutine `clone()` + `joinState` + `drainFirstCausalError` + `deepCopyVars`
- `go/internal/agentgen/nodes.go` — canvas nodes; all fan-out-capable nodes have `MaxOut: 0` (unlimited)

---

## Middleware Security Pipeline — Phase 4 complete (2026-09-01)

### Overview
Pluggable per-application artifact security middleware that intercepts file artifacts from A2A agents before delivery to users.

### What was built

**Phase 1 — Foundation (prior session)**
- `db/050_middleware_pipeline.sql` — `run_artifacts.scan_status/scan_result/scanned_at`, `middleware_jobs`, `middleware_audit`, `applications.security_config`
- `go/internal/middleware/processor.go` — `Processor` interface, `Registry`, `Part`, `Result`
- `go/internal/middleware/config.go` — `SecurityConfig`, `AVScanConfig`, `MergeDefaults`, `Validate`, `EnabledProcessors`
- `go/internal/middleware/pipeline.go` — `Pipeline.Run` chaining with block-on-infected semantics
- `go/internal/middleware/job.go` — `JobDAL`: `Enqueue`, `Claim` (SKIP LOCKED), `LoadFileBytes` (reads `run_artifacts.data`), `Complete` (updates `run_artifacts.scan_status`), `Fail`, `WriteAudit`
- `go/internal/middleware/progress.go` — `ScanPublisher` → `them:scan:<artifactID>` Redis channel
- `go/internal/middleware/middleware_test.go` — 15 tests
- `go/internal/admin/security_config.go` — `GET|PUT /admin/applications/{id}/security-config`

**Phase 2 — ClamAV processor (prior session)**
- `go/internal/middleware/av/clamav.go` — INSTREAM protocol scanner (fail-open)
- `go/internal/middleware/av/clamav_test.go` — 9 tests (mock clamd)
- `go/cmd/middleware-worker/main.go` — worker binary polling middleware_jobs (8 goroutines)
- `Dockerfile.middleware-worker` — build + test at build time
- `docker-compose.dev.yml` — `them-clamd` + `them-middleware-worker` services (profile `security`)

**Phase 3 — Gateway intercept + download gate (this session)**
- `go/internal/middleware/gate.go` — `FileGate.Intercept()`: checks app security config (30s cache), fetches file from agent URL, stores in `run_artifacts` with `scan_status='pending'`, enqueues middleware_job; fail-open on any error
- `go/internal/middleware/pgx.go` — `PgxQuerier` adapter (implements `Querier` + `GateQuerier`)
- `go/internal/a2a/server.go` — `FileInterceptor` interface + `FileInterceptInput/Result` types; `WithFileGate` builder
- `go/internal/a2a/executor.go` — "file" event handler calls `fileGate.Intercept()` when set; replaces `download_url` with gated artifact URL `/api/v1/runs/{run_id}/artifacts/{artifact_id}`
- `go/internal/runrecorder/recorder.go` — `GetArtifactScanStatus()` lightweight query (no BYTEA)
- `go/internal/artifacts/handler.go` — Download gate: 202 when pending/scanning, 451 when infected, 200 when clean/disabled
- `go/cmd/them/main.go` — `fileGateAdapter` bridges `middleware.FileGate` to `a2a.FileInterceptor`; wired into A2A server via `WithFileGate`
- Tests: S1-30 +4, S1-84 (3), S1-85 (4)

**Phase 4 — Quarantine-first MinIO storage (this session)**
- `go/internal/storage/storage.go` — `*Client` wrapping minio-go: PutQuarantine, GetQuarantine, DeleteQuarantine, PutArtifact, PresignArtifact
- `go/go.mod` — added `minio/minio-go/v7 v7.3.0`
- `go/internal/config/config.go` — S3Endpoint/AccessKey/SecretKey/QuarantineBucket/ArtifactsBucket fields
- `go/internal/middleware/gate.go` — rewrite: writes bytes to MinIO quarantine, inserts `quarantine_artifacts` row (no BYTEA in Postgres), enqueues job with quarantine_id
- `go/internal/middleware/job.go` — EnqueueWithQuarantine; LoadFileBytes reads from MinIO; Complete promotes clean bytes to artifacts bucket / inserts infected row with data=NULL
- `go/cmd/them,worker,middleware-worker/main.go` — build storage.Client from S3 config; pass to NewFileGate / dal.Complete
- `db/051_quarantine.sql` — `quarantine_artifacts` table; `run_artifacts.data` nullable + `storage_key` column; `middleware_jobs.quarantine_id`
- Tests: S1-84 (6), S1-88 (5 new job DAL), S1-89 (3 new storage)

### Migrations applied
```
db/050_middleware_pipeline.sql                    ✅ applied
db/051_quarantine.sql                             ✅ applied
db/052_middleware_jobs_nullable_artifact.sql      ✅ applied
```

### Containers to rebuild to pick up quarantine-first changes
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge
# Security profile (ClamAV + middleware worker + MinIO):
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile security up -d
```

**Phase 5 — UI + WS scan subscription (prior session)**
- `go/internal/dashboard/handler.go` — `scan:<artifact_id>` added to `IsValidChannel`; `sendScanSnapshot` delivers current scan status from `them:scan:state:{artifactID}` Redis key; `sendSnapshots` dispatches to scan channel
- `go/internal/dashboard/handler_test.go` — `TestDashboard_ScanSnapshot` + `TestIsValidChannel` updated (2 new tests; S1-52: 11→13)
- `frontend/src/lib/apiTypes.ts` — `SecurityConfig`, `AVScanConfig`, `ArtifactScanEvent` types added
- `frontend/src/lib/api.ts` — `getSecurityConfig(appId)`, `putSecurityConfig(appId, cfg)` added to `themApi`
- `frontend/src/app/admin/applications/components/MonitorView.tsx` — `artifact_scan` event row with scan status badge (pending/scanning/clean/infected/error/disabled icons)
- `frontend/src/app/admin/applications/components/RuntimeView.tsx` — Security section: enable/disable file artifact scanning toggle + Save Security button

**Session B complete (2026-09-02, commit 3f74d70):**
- `internal/artifacts/handler.go`: three-path byte resolution: MinIO (storage_key set) → 410 Gone (infected, data=nil) → legacy Postgres BYTEA
- `internal/runrecorder/recorder.go`: `ArtifactMeta.StorageKey` + `COALESCE(storage_key,'')` in `GetArtifact`
- `cmd/them/main.go`: `storageClient *storage.Client` (concrete type); `artifacts.NewWithFetcher` wired
- Tests: S1-30 13→16 (MinIO path, 410 infected, MinIO error 500)
- them-go-bridge rebuilt and restarted ✅

**Session C complete (2026-09-02, commit 3cf93b1):**
- `internal/orchestrator/orchestrator.go`: `ScanResult`/`ScanSubscriber` interfaces; `WithScanSubscriber`; `emitArtifactEvent` emits `file_scanning` when gated, then goroutine waits and emits `file` (clean/error/timeout) or `file_blocked` (infected); `copyMap` helper
- `internal/orchestrator/scan_subscriber.go`: `RedisScanSubscriber` — subscribes to `them:run:<runID>` pub/sub, filters `artifact_scan_result` by artifactID, cancels on first match
- `cmd/worker/main.go`: `RedisScanSubscriber` wired into factory
- Tests: S1-90 (4 new orchestrator scan subscriber tests)
- them-go-worker rebuilt and restarted: polling ✅

**Session E complete (2026-09-02, commits 34d99bd–793e7f6):**
- `go/cmd/middleware-worker/main.go`: health heartbeat goroutine — writes `them:dash:services:health` (TTL 30s) every 10s; heartbeat does NOT publish to `services:stats` (scan job completion does)
- `go/internal/admin/services_stats.go`: reads health key, adds `worker_up bool` to response envelope
- `go/internal/admin/router.go`: passes `redis` to `NewServicesStatsHandler`
- `go/internal/dashboard/handler.go`: `sendServicesHealthSnapshot` — pushes `{type:services_health, worker_up}` on subscribe so badge is immediately correct on tab open
- `frontend/src/app/admin/services/page.tsx`: green/red "Scanner online/offline" badge; `load(showSpinner)` — WS-triggered refreshes are silent (no screen flash); `services_health` event updates badge only without re-fetch; state var `window` renamed to `timeWindow` (was shadowing `globalThis.window`, breaking WS connect); Recent jobs show `toLocaleString()` local time
- `go/internal/admin/dal/services_stats.go`: quarantine count filters `WHERE storage_key IS NOT NULL` — only counts files genuinely awaiting scan, not post-scan tombstones
- `frontend/src/lib/apiTypes.ts`: `worker_up: boolean` on `ServicesStats`
- Scan error decision: `error` outcome passes file through to user (fail-open) — intentional, can be changed to block

**Session D complete (2026-09-02, commit a174665):**
- `frontend/src/app/admin/playground/playgroundTypes.ts`: `FileMsg` gains `artifact_id`, `scanning`, `blocked`, `threat` fields
- `frontend/src/app/admin/playground/useChatConnection.ts`: `file_scanning` handler (spinner bubble); `file_blocked` handler (finds and replaces scanning bubble, or adds new blocked bubble); `file` handler (replaces scanning bubble in-place for clean result)
- `frontend/src/app/admin/playground/ChatColumn.tsx`: renders three states — scanning (spinner + "Scanning…" label), blocked (red border/icon + threat text), clean (download button + previews)
- TypeScript `tsc --noEmit` passes with zero errors ✅

### What's NOT done yet
- Reaper job for stuck quarantine objects (rows with `storage_key IS NOT NULL AND expires_at < now()`)
- Additional processors: `pii_redact`, `prompt_inject`, `schema_validate`, `audit_capture`

### Key design decisions
- **Quarantine-first**: file bytes go to MinIO quarantine bucket BEFORE any Postgres row; infected bytes never touch `run_artifacts.data`
- `run_artifacts.data` is now nullable — infected rows have `data=NULL, storage_key=NULL`
- `run_artifacts.storage_key` holds MinIO artifacts key for clean files (replaces BYTEA streaming)
- Two MinIO buckets: `them-quarantine` (bytes pre-scan, TTL 1hr) and `them-artifacts` (confirmed clean)
- Gateway fail-open: MinIO write error → disabled path, original URL used
- ClamAV via TCP `them-clamd:3310` (Unix socket cross-namespace doesn't work in Docker)
- Security scanning per-application via `applications.security_config` JSONB
- Download gate: 451 for infected, 202 for pending/scanning
- Redis cache invalidation: `them:security_config:invalidated:{app_id}` pub/sub on PUT

---

## A2A EP SDK Migration — COMPLETE (commit 45a0e23, 2026-08-31)

### What was done
- `go/internal/a2a/server.go`: rewritten to use `a2asrv.NewJSONRPCHandler` — 100% A2A v1.0 wire format
- `go/internal/a2a/executor.go` (new): `orchExecutorFunc` bridges `Lifecycle.Start` + run-stream bus to `iter.Seq2[a2a.Event, error]`; maps `token/file/done/error` bus events to SDK types
- `go/internal/a2a/card.go` (new): `buildSDKAgentCard` from DB row or fallback; served via `StaticAgentCardHandler`
- `go/cmd/them/main.go`: wires `RedisA2ATaskStore` via `WithTaskStore` + `WithSessionPublisher` retained
- `go/internal/a2a/server_test.go`: all fixtures updated for `SendMessage`/`SendStreamingMessage` and `TASK_STATE_COMPLETED`; 3 new compliance tests A2A-WF01/WF02/WF03
- `go/TEST_INDEX.md`: count 27→30, 3 new compliance rows

### Breaking change for external A2A clients
External clients calling `/a2a/{app_slug}/{ep_slug}` MUST update:
- Method name: `message/send` → `SendMessage`; `message/stream` → `SendStreamingMessage`
- TaskState: `"completed"` → `"TASK_STATE_COMPLETED"`, `"failed"` → `"TASK_STATE_FAILED"`
- Internal callers (agentregistry) already use `SendMessage`/`SendStreamingMessage` — unaffected.

---

## Frontend file-split refactor — COMPLETE (commit a54a796, 2026-08-31)

Split 4 oversized admin pages into focused sub-components. No logic changes. TypeScript passes with zero errors.

| Page | Before | page.tsx after | New files |
|---|---|---|---|
| `admin/agents/page.tsx` | 2,529 lines | 660 lines | `AgentCard.tsx`, `FolderHeader.tsx`, `AgentModals.tsx`, `agentTypes.ts`, `agentUtils.ts` |
| `admin/orchestrators/page.tsx` | 863 lines | 212 lines | `OrchestratorCard.tsx`, `OrchestratorForm.tsx`, `orchestratorConstants.ts` |
| `admin/mcp-servers/page.tsx` | 1,022 lines | 157 lines | `MCPBadges.tsx`, `MCPServerCard.tsx`, `MCPToolRow.tsx`, `MCPPropertiesPanel.tsx`, `MCPCreateModal.tsx`, `mcpConstants.ts` |
| `admin/settings/page.tsx` | 905 lines | 189 lines | `RoleCard.tsx`, `MonitoringPanel.tsx`, `settingsConstants.ts` |

## Frontend file-split — waves 1–5 (complete, 2026-08-31)

All splits: no logic changes. TypeScript passes with zero errors throughout.

| File | Before | After | New files |
|---|---|---|---|
| `lib/api.ts` | 1,152 lines | 476 lines | `lib/apiTypes.ts` (728 lines), `lib/apiClient.ts` (71 lines) |
| `applications/components/PropertiesPanel.tsx` | 936 lines | 125 lines | `panel/AppPanel.tsx`, `panel/EntryPointPanel.tsx`, `panel/OrchestratorPanel.tsx` (484), `panel/AgentPanel.tsx`, `panel/MiddlewarePanel.tsx`, `panel/panelStyles.ts` |
| `applications/components/CanvasBuilderView.tsx` | 1,152 lines | 625 lines | `cbv/CanvasNodePropertiesPanel.tsx` (573 lines) |
| `admin/playground/page.tsx` | 2,309 lines | 276 lines | `playgroundTypes.ts` (169), `MarkdownRenderer.tsx` (240), `DebugPanel.tsx` (588), `ChatColumn.tsx` (957) |
| `admin/playground/ChatColumn.tsx` | 957 lines | 495 lines | `ChatBubbles.tsx` (95), `useChatConnection.ts` (449) |

All oversized frontend files have been split. No files remain above 600 lines in the pages that were targeted.

---

## Go file-split refactor — in progress

### Completed this session
| File | Before | After | New files | Commit |
|---|---|---|---|---|
| `go/cmd/agent-runtime/main.go` | 1123 lines | 115 lines | `runtime.go` (376), `hitl.go` (205), `spec.go` (333), `llm.go` (150) | `ca51f2a` |

### Remaining candidates (next session picks one)

| File | Lines | Suggested split |
|---|---|---|
| `go/internal/agentgen/compiler.go` | 1056 | `compiler.go` (entry points) + `validate.go` (all validate* funcs) + `topo.go` (topoSort/resolveBindings/deriveStepVars/collect*) |
| `go/internal/agentgen/nodes.go` | 1040 | `nodes.go` (registry + spec types) + `nodes_exec.go` (all exec* funcs) |
| `go/internal/admin/applications.go` | 972 | `applications.go` (CRUD handlers) + `applications_llm.go` (TestLLM/Patch*/probe* funcs) |
| `go/internal/temporal/canvas_workflow.go` | 939 | Harder — single workflow; defer unless it grows |
| `go/internal/orchestrator/orchestrator.go` | 925 | Risky without full E2E; defer |
| `go/internal/agentregistry/registry.go` | 834 | `registry.go` (cache + lookup) + `registry_invoke.go` (A2A invocation logic) |
| `go/internal/admin/service/applications.go` | 756 | Mirrors handler split — defer until handler split is done |

**Start with `compiler.go` — clearest responsibility boundaries, pure functions, no live state.**

**Full step-by-step instructions (exact file map, imports, verification, commit message) are in:**
`docs/SPLIT_COMPILER_INSTRUCTIONS.md`

### First prompt for next session
> Read docs/SPLIT_COMPILER_INSTRUCTIONS.md in full before touching any code. Follow the procedure exactly: pre-flight → create compiler_validate.go → create compiler_topo.go → trim compiler.go → run all 4 verification steps → commit and push.

---

## Next recommended task (features)

### Phase 5-A: StepLoop — FULLY COMPLETE (commit 81c3a31)

Done (initial, commit a69f01a):
- `LoopConfig`: `ItemsVar`, `ItemVar`, `AccumVar`, `Condition`, `MaxIterations`, `BodySteps`
- `PlanNode.SubPlan *ExecutionPlan` — loop body compiled by `compileLoopBodyPlan`
- `plan_compiler.go`: `compileLoopBodyPlan`, `resolveLoopOuterNext`
- `nodes.go`: `execLoop` — LocalExecutor path only
- Frontend: Loop config panel in `StepConfigSection.tsx`
- Tests: EP-LOOP-1..5, CT-LOOP-1..3

Done (durable loop, commit 05351bd) — addresses all 4 Phase 5-A audit findings:
- `local_executor.go`: `ExecNodeWithPolicy` exported; `execNode` is now a thin wrapper
- `nodes.go`: `execLoop` uses `ExecNodeWithPolicy` per body step (retry/timeout per body node);
  accum_var snapshots only declared body `Outputs` keys; `Validate` errors on empty `BodySteps`
- `plan_compiler.go`: `ValidateLoopBodies` — unknown body step IDs + `MaxLoopHistoryBudget` (5000) check; wired into `agent-runtime` invocation path
- `canvas_workflow.go`: `runBranch` intercepts `StepLoop` before `ExecuteActivity`; `runLoopNode` iterates items sequentially, schedules each body step as its own `ExecuteStepActivity` with its own policy/retry/timeout/history entry. Branch inside body works.
- Tests: CT-LOOP-DURABLE-1..5, PC-LOOP-1..3; all 42 packages pass

Done (canvas ports, commit 81c3a31):
- `nodes.go`: `ControlOutputPorts: [{loop-body}, {loop-done}]`; `EdgeRules.MaxOut: 2`
- `nodeRegistry.ts`: loop added to `SUMMARY_FNS` (shows `items_var`)
- `useDefinitionLifecycle.ts`: both serialization paths (validation useEffect + `buildDefinitionDoc`) derive `body_steps` via BFS from `ctrl-out-loop-body` edge chain; set `next` to only `ctrl-out-loop-done` target; load path reconstructs both edges from `step.next[0]` (done) and `config.body_steps[0]` (body entry)

### Phase 5-B: HumanWait async — COMPLETE (two commits: `3b1052f` + pending hardening)

**Phase 1** (commit `3b1052f`) built the initial async path.

**Phase 2 hardening** (pending commit) adds:
- `HITLHandle` 6-field schema: `{workflow_id, run_id, tenant_id, step_id, wait_token, state}` — state machine "submitted"→"waiting"→"signalled"→deleted
- `UpdateWaitToken()`, `TrySignal()` (atomic CAS), `MarkDone()` on HITLStore
- Deterministic `wait_token` via `sha256(runID+":"+stepID+":"+counter)[:16]` — never `uuid.New()` in workflow code
- `hitl_status` workflow query handler registered at workflow start — polled by agent-runtime
- Per-step HITL timeout via `workflow.Select` + timer (configurable via `HumanWaitConfig.TimeoutSeconds`)
- `runBodyBranch` WaitingForHuman block — loop body `human_wait` support
- `HITLRequestHandler` — intercepts GetTask/SubscribeToTask/CancelTask for HITL A2A tasks; polls `QueryHITLStatus` to sync state; no permanent background goroutines
- `RedisA2ATaskStore` — proper `taskstore.Store` implementation connected to SDK via `WithTaskStore`
- `CanvasAwaiter`, `CanvasCanceler`, `CanvasHITLQuerier` interfaces on `TemporalExecutor`
- Signal endpoint moved from unauthenticated port 9300 to JWT-authenticated admin router: `POST /admin/canvas-tasks/{task_id}/signal` behind `RequireSuperAdmin + AdminTenantMiddleware`
- `CanvasTasksHandler` in `internal/admin/canvas_tasks.go` — tenant ownership check + TrySignal CAS
- 20 total tests: HS-1..11, RT-HITL-1..5, CSIG-1..4; `go test ./...` → 0 failures

### Phase 5-C: A2A call node — COMPLETE (commits 89c7e67 + pending gap fix)

**What was built (Phase 5-C initial, commit 89c7e67):**
- `go/internal/agentgen/a2a_caller.go`: `A2ACaller` interface + `HTTPA2ACaller` + `AgentEndpointResolver` + `DBAgentEndpointResolver`
- `go/internal/agentgen/interpreter.go`: `a2aCaller` field + `WithA2ACaller()` + `execA2ACall`
- `go/internal/agentgen/context.go`: `A2ACallDepth int` on `InvocationContext`
- `go/internal/agentgen/nodes.go`: `StepA2ACall` — Execute set; Validate checks required fields
- `go/internal/agentgen/node_executor.go`: `A2ACallDepth` on `ActivityIC`
- `go/internal/agentgen/compiler.go`: `validateHumanWaitBackend`
- `go/internal/agentgen/spec.go`: `A2ACallStepConfig` with AgentSlug/InputVar/OutputVar/TimeoutSeconds
- `go/cmd/agent-runtime/main.go` + `go/cmd/dag-worker/main.go`: wired HTTPA2ACaller

**What was fixed (Phase 5-C gap fixes, pending commit):**
- `A2ACallParams` struct — replaces positional `Call()` args; adds `InvocationID` + `StepID`
- `ResolvedEndpoint` struct — resolver now returns `AgentID` + `BindingID`
- `AgentEndpointQueryer.QueryAgentEndpoint` — takes `applicationID`; JOINs `app_agent_bindings + applications`; returns 4 columns
- `DBAgentEndpointResolver.ResolveEndpoint` — fail-closed when binding or endpoint missing
- `HTTPA2ACaller.Call` — sends `X-Them-Agent-Id` + `X-Them-Binding-Id` headers
- `stableCallUUID` — UUID v5 from `invocationID:stepID:agentSlug:role` so retries re-use same IDs
- `sanitizeRemoteError` — strips URLs → `[url-redacted]`, truncates at 300 chars
- 5 new tests: A2A-9b, A2A-14..18 (fail-closed, stable UUIDs, sanitized errors, E2E Local + Temporal)
- Total: 18 tests (A2A-1..18), suite total 898 → 916

**Security constraints enforced:**
- Binding required — no call without verified `app_agent_bindings` row (fail closed)
- `X-Them-Agent-Id` + `X-Them-Binding-Id` sent so callee can verify tenant ownership
- Endpoint + auth token from DB only — never user-supplied
- `X-Them-A2A-Depth` propagated; `MaxA2ACallDepth = 3` hard cap
- Self-call rejection before resolver is invoked
- Remote error messages sanitized (no internal URLs in logs/responses)
- No secrets in Temporal history

### Phase 5-D: StreamOut node — COMPLETE

**What was built (Phase 5-D):**
- `go/internal/agentgen/spec.go`: `StreamOutStepConfig{FromVar, MediaType}`
- `go/internal/agentgen/interpreter.go`: `execStreamOut` — reads `from_var`, defaults to `"output"`, sets `result.Text` + `result.MediaType` (same semantics as `execResponse`; incremental streaming is a transport-layer concern handled by agent-runtime's A2A artifact events)
- `go/internal/agentgen/nodes.go`: `StepStreamOut` — `Execute` wired to `execStreamOut`; `Validate` checks `from_var` required (`STREAM_OUT_MISSING_FROM_VAR`); `DeriveInputs` declares `from_var` as required; description updated (no longer stub)
- `go/internal/agentgen/noderegistry_test.go`: `StepStreamOut` moved from stubs list → implemented list; `TestNodeRegistry_StreamOutIsSink` asserts `Execute≠nil`; `TestNodeRegistry_StubTypesHaveNilExecute` updated (only `human_wait` remains)
- `go/internal/agentgen/compiler_test.go`: `stubGraph` updated from `stream_out` to `human_wait` (stream_out is no longer a stub)
- `go/internal/agentgen/stream_out_test.go`: 10 new tests (SO-1..10)
- `go/TEST_INDEX.md`: S1-83 added, totals 916→926

**Design note:** At the interpreter level, StreamOut and Response are functionally identical — both read a variable and set `result.Text`. The transport differentiation (incremental artifact events vs. single artifact) happens in `agent-runtime/main.go`'s `executeSkill`, which already emits `ArtifactEvent` at the end of every execution. A true token-by-token streaming path would require a callback/writer interface injected into the interpreter — that's a future transport-layer enhancement, not a canvas-node concern.

**Next recommended task:**
- **IAM Stage 2 — COMPLETE (2c930cd)**. Group Mappings tab deployed, Keycloak mapper configured, `bank-admins` group + `bankadmin` user created, mapping `bank-admins → admin` active for `avi-test`.
- **IAM Stage 3 — COMPLETE (7f09eb2)**. Tenant-admin self-service SSO group mapping. Configurable groups_claim, unmatched_action=deny|viewer, self-service CRUD (/tenant/group-mappings), OIDC debug endpoint (/tenant/oidc-debug), Redis-backed debug records, AT-11 corrected (lookup_error→503, never fall back to viewer). 3 new OIDC tests (OIDC-31..33), 8 new TSS tests (TSS-13..20). Deployed: them-auth-go (Redis connected), them-go-bridge (new routes active), them-frontend (Group Mappings tab + debug box). Verified: GET /tenant/group-mappings → [] ✓, GET /tenant/oidc-debug (no email) → 400 ✓, GET /tenant/settings → 200 ✓.
- **IAM Stage 4 (next)** — Browser verification: (a) bankadmin SSO login → admin role, (b) avi1 SSO login → viewer, (c) bankadmin: /tenant/settings Group Mappings tab works end-to-end, (d) debug box shows bankadmin's OIDC login record after SSO. Also verify AT-13: mapping edit → refresh (no change), then mapping edit → new SSO login → refresh (new role).
- **Phase 4** — Bank JWT / JWKS validation (`JWKSAuthenticator`, `tenant_runtime_config` table)
- UI: StreamOut properties panel in canvas `RightPanel.tsx` (from_var + media_type fields) — mirrors Response panel
- UI: A2A Call node properties panel in canvas RightPanel (slug + var config)

### Phase 4-C Advisory items (deferred)
- Advisory A: DB round-trips per Temporal activity (4 queries/node) — cache spec in `ActivityIC`
- Advisory B: `PipelineVars` payload growth — prune vars before each `StepActivityInput`
- Advisory C: DB pool (20) vs DAGWorkerMaxConcurrentActivities (50) mismatch — raise pool
- Advisory D: dag-worker health/readiness HTTP endpoint (currently no /healthz)
- Advisory E: HumanWait — RESOLVED by Phase 5-B. `input-required` path fully async. Reconnect via SDK `SubscribeToTask` (no code needed — SDK handles it).

### Other tasks (lower priority)
- DAG live canvas validation — smoke test a Branch/Parallel canvas agent live with `--profile temporal`
- Docker E2E test (`THEM_TEMPORAL_E2E=true`) against live stack to validate all 7 blockers end-to-end
- Auth admin CRUD Go proxy — when `them-auth-service` Python retirement is decided

Do NOT begin multiple subsystems in the same session.

---

## Known blockers

0. ~~**LIVE METERING BUGS**~~ — **FIXED** (commit `ed3620dc`, 2026-09-19, same day this blocker was
   recorded). This item was added by a doc-only design-review session running in parallel with the
   Phase 0 implementation session; the two were not sequenced, so this blocker briefly described
   already-fixed code. All three sub-bugs (0a/0b/0c below) are now independently re-verified against
   the shipped code and covered by tests — see "Phase 0 — COMPLETE" above for the full list of
   changes, `go/TEST_INDEX.md` S1-116..118 + S2-10/S2-11 for tests, and `go test ./...` (0 failures,
   S1 1254 / S2 59) for verification.

   | # | Bug | Status |
   |---|---|---|
   | 0a | `SumMonthlyTokens` filtered on `runs.created_at` (nonexistent column) | ✅ Fixed — `started_at`. Regression test S2-10. |
   | 0b | `decryptValue` returned ciphertext verbatim when no Fernet key configured | ✅ Fixed — `internal/llmresolve.Resolver.DecryptValue` now returns an error instead. Test: `TestDecryptValue_NoKeyConfigured_Ciphertext_FailsLoud` (S1-118). |
   | 0c | OpenAI-compatible path never sent `stream_options.include_usage`; cost used a hardcoded Claude-only table | ✅ Fixed — `stream_options.include_usage: true` sent on every request (S1-116); cost now reads `them.llm_providers.model_pricing` via `orchestrator.CostEstimator` (S1-117), falling back to the old hardcoded table only when DB pricing has no entry for the model. |

   **Lesson for next time:** when a design-review session and an implementation session run
   against the same doc in the same window, the design-review session should check `git log` /
   re-read the target file immediately before committing, not just before starting — this blocker
   was accurate when drafted and stale by the time it was pushed.

1. **Migration 078+079 must be applied together** — Migration 078 (`078_rls_phase_h2.sql`) over-revokes `INSERT/DELETE` on `component_definitions` from `them_app`. This breaks Agent Create and Delete at runtime. Apply `db/079_component_definitions_grant.sql` **in the same psql session** as 078, or apply 079 first if 078 is already in (but not yet live). Then restart all 4 Go containers.

2. **Auth admin CRUD (users/roles/teams)** — `them-auth-service` (Python, port 8701) still serves user/role/team management. Frontend hits it directly. No Go proxy until we decide to retire the Python binary.

2. **Wave 9 tenant items** — session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims. Not started.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `go/internal/authserver/`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`
- **`them-bridge` (Python FastAPI) has been deleted.** `app/`, `Dockerfile`, `Dockerfile.worker` removed from filesystem. Not in `docker-compose.yml`.
- **`them-worker` (Python Temporal) has been deleted.** Not in `docker-compose.yml`. All WS/SSE sessions submit to `them-orchestration-go`.
- **No global LLM key fallback.** Apps with no key get an explicit error.
- **No secrets in Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history.**
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** Global key must not be written or read.
- **EP cache key is `"{tenantID}:{appSlug}:{epSlug}"`.** Invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{appSlug}:{epSlug}"`. DB resolves by `(tenant_id, app_slug, ep_slug)`.
- **`entry_points.tenant_id` is NOT NULL.** `UNIQUE(application_id, slug)` enforced at DB level (relaxed from tenant-scoped to application-scoped in migration 048).
- **`applications.slug` is NOT NULL** after migration 048. `UNIQUE(tenant_id, slug)` enforced at DB level. URL shape is `/apps/{app_slug}/{ep_slug}/...`.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** — never globally by name.
- **Project name: `them_gateway`** — required for all compose commands.

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Update this file at session end — do NOT create new NEXT_SESSION_*.md files.
3. ADRs are permanent — never archive them.
4. Trust code over docs; update docs when they diverge.
5. Documentation changes ship in same commit as the code they describe.
