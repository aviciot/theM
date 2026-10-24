---
title: Code Findings Backlog (from the docs reboot)
type: plan
status: current
last_verified: 2026-10-24
---

# Code Findings Backlog

Bugs, risks and inefficient code found while writing docs from the code. **Not fixed yet** — we fix them after the docs reboot is complete. All found by reading code, none reproduced live.

**Rule:** every session that finds a new bug/risk/inefficiency adds a row here (same commit). When one is fixed, set Status to `fixed (<commit>)` and update the owning architecture doc. Never delete rows.

Severity: **H** security / data loss / silent wrong result · **M** wrong behaviour · **L** cleanup.

| ID | Sev | Area | Finding | Where | Status |
|---|---|---|---|---|---|
| F-01 | H | Canvas app | PII / prompt-injection / file checks do not run inside fork branches | `appflow/graph.go` `walkBranch` | open |
| F-02 | H | Canvas app | A failed fork branch is ignored; run can finish `completed` | `appflow/workflow.go` fork case | open |
| F-03 | M | Canvas app | Fork branches only support agent/llm/condition/orchestrator/middleware/join; other nodes fail (and F-02 hides it) | `appflow/graph.go` | open |
| F-04 | M | Canvas app | Agent-only flows may send no answer over WS/SSE (`done.final_text` dropped; only LLM nodes emit `token`) | `ws/handler.go`, `sse/handler.go`, `appflow/activities.go` | open |
| F-05 | M | Canvas app | `node_*` events with output detail go to end-user WS/SSE clients, even outside debug — confirm intended | `appflow/activities.go` `emitTrace` | open |
| F-06 | L | Canvas app | `Validate` has no checks for http nodes, router labels vs edges, unknown kinds | `appflow/validate.go` | open |
| F-07 | L | Canvas app | HIL rejection stored as `failed`; rejection comment lost | `appflow/activities.go` Finalize | open |
| F-08 | H | Run lifecycle | Reconciler never matches runs (looks up run ID, real workflow IDs differ); also dry-run by default | `reconciler/reconciler.go` | open |
| F-09 | M | Run lifecycle | `session.Store.Touch` never called; gate/session keys expire after 90s, caps undercount long sessions | `session/session.go`, `cmd/them/main.go` | open |
| F-10 | M | Run lifecycle | Failures before `orchestrator.Run` send no `error` event and leave run `running` | `temporal/activities.go` | open |
| F-11 | M | Run lifecycle | Bridge cannot start without Temporal (nil client panics `NewLifecycle`); log says "inline path" | `cmd/them/main.go` | open |
| F-12 | M | Run lifecycle | WS drops `file` + `canceled/terminated/timed_out`; SSE drops the 3 terminal types; only WS supports follow-up messages | `ws/handler.go`, `sse/handler.go` | open |
| F-13 | L | Run lifecycle | Per-user daily cost ignores gateway spend (`GetUserDailySpendFromGateway` unused) | `execution/lifecycle.go` | open |
| F-14 | L | Docs/code | Stale comments (Python-era, in-process bus, "agents rejected", `local` = goroutine loop) | ws, sse, appflow, publish | open |
| F-15 | L | Docs | `REDIS.md` has Python-era keys, lacks `them:events`, `them:events:dlq`, `them:ep:gate:queue:{slug}` | `docs/REDIS.md` | open |
| F-16 | L | Config | Root `CLAUDE.md` has Python-era rules and a wrong container map; says 2 dag workers (compose has 4 + debug) | `CLAUDE.md` | open |
| F-17 | L | Ops | `version:` still in `docker-compose.yml`; many stray status/result files in repo root | repo root | open |
