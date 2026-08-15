# the-M — Migration to Temporal (To-Be Design)

**Platform**: the-M (codebase at `/opt/docker/odin`)
**Document type**: Migration design (To-Be)
**Companion document**: `the-M — Current Orchestration Architecture (As-Is)`, 2026-07-11
**Decision**: Full migration to Temporal, self-hosted (no cloud)
**Scope**: Execution runtime only. See Section 8 for explicit non-goals.

---

## 1. Decision Summary

After evaluating Temporal against Restate for the-M's orchestration engine, **Temporal** was selected.

**Why:**
- Target profile is "medium scale with room to grow, including long-running executions" — this favors Temporal's operational maturity (heartbeat timeouts, deep retry/cancellation semantics, proven horizontal scale) over Restate's lighter operational footprint.
- The-M's current architecture already separates durable state (Postgres/Redis, shared across replicas) from execution state (in-process generator, per-replica). This is conceptually identical to Temporal's Workflow/Activity split — the migration is a natural fit, not a rearchitecture.
- Self-hosted deployment is required (no cloud). Temporal supports Postgres as its persistence backend, which the-M already operates and its team already knows.

**Explicitly out of scope for this document:** the choice itself is final for planning purposes. This document does not re-litigate Temporal vs. Restate.

---

## 2. What This Migration Solves

Directly mapped to the pain points in the As-Is document, Section 16:

| As-Is Pain Point | Resolved By |
|---|---|
| #1 — No retry logic in `a2a_async_adapter.py` | Activity Retry Policies |
| #2 — Cancellation does not propagate | Cancellation Scopes on Activities |
| #3 — No resume after restart | Workflow Event History replay |
| #4 — In-flight tasks continue after WS disconnect | Workflow lifecycle decoupled from client connection |
| #5 — `input-required` is a dead state | Signals (pause and resume on external event) |
| #6 — Remote task ID not persisted | Activity input/output is part of Event History automatically |
| #10 — Orchestrator cache missing `budget_tokens` | Not solved by migration — application-level bug, tracked separately |

Not solved by this migration (explicitly out of scope, see Section 8):
- A2A output/input schema compatibility between agents
- Explicit Plan/Evaluator orchestration logic (Plan-Execute-Evaluate pattern)
- #8, #9 in As-Is Section 16 (all-or-nothing gather edge case, stub edge types) — application-level, addressed independently

---

## 3. To-Be Architecture Overview

### 3.1 Core Mapping

| As-Is Component | To-Be Component |
|---|---|
| `task_runner.run()` (async generator) | Temporal **Workflow** function |
| `_invoke_agent()` → `A2aAsyncAdapter.stream_invoke()` | Temporal **Activity** |
| `asyncio.gather()` over tool calls | Concurrent Activity futures awaited inside the Workflow |
| `task_store._TRANSITIONS` state machine | Workflow execution status (Temporal-native) — see 3.4 |
| `_reaper_loop()` (manual deadline enforcement) | Removed — replaced by Activity `StartToCloseTimeout` / `HeartbeatTimeout` |
| Traefik sticky sessions | Removed — any replica/worker can process any Workflow Task |
| `input-required` dead state | Workflow blocks on `workflow.wait_condition()` awaiting a **Signal** |
| Manual Redis pub/sub on state change | Workflow queries + Temporal Web UI real-time state (no manual publish needed) |
| Context memory summary (Redis, TTL-based) | Workflow-local state (part of Event History) or a Temporal **Local Activity**-backed cache — see 5.3 |

### 3.2 New Component Map

| Container | Role | Notes |
|---|---|---|
| `them-traefik` | Reverse proxy | Sticky sessions **removed** — plain round-robin |
| `them-postgres` | PostgreSQL 16 | Retains `them` schema (billing, admin, artifacts) **and** hosts Temporal's persistence schema (new, separate namespace/DB) |
| `them-redis` | Redis | Retains: agent registry cache, rate limiting, auth token cache. **Removed**: context memory summary storage, task state pub/sub |
| `them-auth-service` | Unchanged | No interaction with Temporal |
| `them-bridge` | FastAPI API + WebSocket edge | Becomes a thin edge: accepts connections, starts/signals Workflows, streams Workflow updates back to client |
| `temporal-frontend` | **New** — Temporal server (frontend service) | Self-hosted, Postgres-backed |
| `temporal-history` / `temporal-matching` / `temporal-worker-service` | **New** — Temporal internal services | Can run combined (`temporal server start-dev`-style) for medium scale, split later if needed |
| `them-worker` | **New** — Temporal Worker process(es) | Hosts Workflow + Activity definitions; replaces the in-process agentic loop |
| `temporal-ui` | **New** — Temporal Web UI | Self-hosted, points at the same Postgres |
| Downstream A2A agents | Unchanged | Still called via A2A JSON-RPC — now from inside Activities, not from `task_runner.py` directly |

### 3.3 Request Lifecycle (To-Be)

1. Client connects → `them-bridge` (WS/SSE), token validated as today
2. `them-bridge` starts a Temporal Workflow: `client.start_workflow(OrchestrationWorkflow, args, id=f"run-{uuid}")`
3. `them-bridge` opens a Workflow **Query** or **Update-with-start** subscription to stream progress back to the client (token-by-token LLM streaming requires special handling — see 5.1)
4. Inside the Workflow: the agentic loop runs as Workflow code — LLM calls happen via Activity (LLM calls are non-deterministic and must not run directly in Workflow code), tool calls dispatch as concurrent Activities
5. Each Activity wraps one A2A `_invoke_agent()` call — retries, timeouts, and cancellation are configured per Activity
6. On completion, the Workflow returns its result; `them-bridge` relays the final `done` event and closes the WS
7. If the client disconnects mid-run: **the Workflow keeps running.** A reconnect with the same `context_id`/Workflow ID can re-attach and continue receiving updates.
8. If `them-bridge` or the Temporal Worker process crashes mid-run: the Workflow resumes automatically on any available worker, replaying Event History to the last completed step.

### 3.4 Task/Run State — What Changes

The As-Is `them.tasks` / `them.runs` state machine (Section 20 of As-Is doc) is **not deleted**, but its role changes:

- **Execution state authority** moves to Temporal (Workflow status: `Running`, `Completed`, `Failed`, `Canceled`, `Terminated`, `TimedOut`, `ContinuedAsNew`).
- **`them.tasks` / `them.runs` become a reporting/analytics projection**, updated via Activities that write to Postgres for the dashboard, billing, and admin APIs the-M already has. They are no longer the source of truth for whether a run can resume — Temporal's Event History is.
- This avoids a "big bang" rewrite of the dashboard, run history, and billing APIs, which can continue reading from Postgres largely unchanged.

---

## 4. Human-in-the-Loop Design

Directly resolves As-Is Section 11 and Pain Point #5.

```
Workflow reaches a point requiring human input
  → Workflow calls workflow.wait_condition(lambda: self.human_response is not None)
  → Workflow is now suspended (durably — this survives worker/process restarts)
  → An external system (admin UI, approval endpoint) calls:
        client.get_workflow_handle(workflow_id).signal("submit_human_response", payload)
  → Signal handler sets self.human_response
  → wait_condition unblocks, Workflow continues from exactly that point
```

This replaces the dead `input-required` handling in `_invoke_agent()` event processing (As-Is Section 6) with a real pause/resume mechanism, with no time limit imposed by the runtime (a business-level timeout can still be added via `workflow.wait_condition(..., timeout=...)`).

---

## 5. Design Decisions Requiring Follow-Up

These are flagged rather than fully resolved, because they depend on details not yet confirmed.

### 5.1 Token-Level LLM Streaming to the Client

The As-Is system streams LLM tokens to the client in real time (Section 3, step 17). Temporal Workflows are not designed to stream arbitrary data continuously to an external client mid-execution — Activities execute atomically from the Workflow's perspective.

**Recommended approach:** the LLM call itself runs as an Activity that also handles streaming directly to the client (e.g., via a side-channel — writing tokens to a Redis pub/sub channel or directly to the client-facing edge) while the Activity result (final text + tool calls) is what gets recorded in Workflow history. This keeps the streaming UX identical to today while keeping the Workflow itself deterministic.

**This needs a short spike before full implementation** to confirm the approach performs acceptably under load.

### 5.2 Workflow ID / Context ID Mapping

`context_id` (As-Is Section 7) should likely map to a Temporal Workflow ID scheme (e.g., one Workflow per `context_id`, using `signal-with-start` for each new user turn) rather than one Workflow per `run_id`, to preserve today's multi-turn behavior naturally through Workflow state instead of re-querying Postgres each turn. This changes the `_load_context_history()` / `_build_messages_from_store()` pattern (As-Is Section 4) — conversation history can live in Workflow state directly.

**This is a meaningful design decision that affects the Workflow's shape and should be confirmed before implementation starts.**

### 5.3 Context Memory Summary

The Redis-TTL-based summary (As-Is Section 21.8, Pain Point #7) can become plain Workflow-local state (part of Event History, no TTL, no loss on Redis eviction). This is a direct improvement with no open design question.

---

## 6. What Stays Unchanged

- A2A protocol usage (JSON-RPC 2.0, task states, artifacts, agent cards) — untouched. Activities call the same `A2aAsyncAdapter` logic.
- Agent registration, admin CRUD, LLM provider abstraction (`providers/`)
- Billing, cost tracking, run history APIs (read from Postgres projection)
- Auth model (`them-auth-service`, token validation)
- Rate limiting (Redis fixed-window)
- the-M's own A2A inbound server (`a2a_server.py`) — external callers still interact with the-M the same way; internally, inbound requests now start a Workflow instead of `asyncio.create_task()`

---

## 7. Migration Phases

1. **Infrastructure**: stand up self-hosted Temporal (Postgres-backed), Temporal Worker container, Temporal Web UI. No application changes yet.
2. **Wrap the core loop**: port `task_runner.run()` to a Workflow definition; port `_invoke_agent()` to an Activity. Validate against a single test agent (`a2a-echo`) end-to-end, including a deliberate mid-run worker kill to confirm resume.
3. **Migrate remaining agents**: vision-agent, docu-writer, debate stack — one at a time, with the existing `them-bridge` REST/admin surface unchanged.
4. **Human-in-the-loop**: implement Signal-based pause/resume, replacing the dead `input-required` handling.
5. **Cutover**: `them-bridge` starts routing new runs through Workflows exclusively; remove `_reaper_loop`, remove sticky session config from Traefik, deprecate `them.tasks`/`them.runs` as execution-state authority (keep as reporting projection).
6. **Cleanup**: remove now-dead code paths (manual state machine transitions used only for execution control, not reporting).

---

## 8. Explicit Non-Goals (Deferred / Separate Work)

- **A2A output/input schema enforcement between agents.** Neither A2A nor this migration validates that Agent A's output shape matches Agent B's expected input shape — this remains an LLM-judgment call, same as today. A schema-validation layer (JSON Schema check before dispatch) is a separate, independent project.
- **Explicit Plan / Evaluator orchestration logic.** The current implicit ReAct-style loop (LLM decides next action, LLM decides completion) is preserved as-is in the Workflow. Moving to an explicit Plan-Execute-Evaluate pattern is a business-logic change independent of the runtime, and is deferred until after this migration is stable in production.
- **Scale sizing.** No specific concurrent-run targets or SLAs were available at time of writing. The design favors horizontal scalability (stateless workers, no sticky sessions) but does not commit to specific capacity numbers.

---

## 9. Open Questions Before Implementation Starts

1. Confirm Workflow ID scheme: one per `context_id` vs. one per `run_id` (Section 5.2).
2. Confirm approach for token-level streaming (Section 5.1) — spike recommended.
3. Confirm whether `them.tasks`/`them.runs` should remain as-is for reporting, or be reshaped now that they're no longer execution-authoritative.
4. Confirm Temporal Worker deployment topology (single worker pool vs. per-agent-type worker pools, relevant if per-agent `max_concurrency` semaphores need equivalent Task Queue-level limits).
