---
title: ADR-004 Run Identity and Event Channel (Go-only; supersedes ADR-001)
type: decision
status: current
last_verified: 2026-10-25
---

# ADR-004: Run identity and event channel (Go-only)

**Status:** Accepted — records how the system works today (verified against code 2026-10-25). Supersedes ADR-001.
**Date:** 2026-10-25

## Context
ADR-001 (July) described a Go gateway talking to a **Python** Temporal worker, a `…:tokens` Pub/Sub channel, and
"the Temporal workflow ID is the same as the run ID". The Python worker is gone, events moved to Redis Streams
(ADR-003), and workflow IDs are no longer the run ID. Docs and code (notably the reconciler, see below) still assumed the old rule.

## Decision (as implemented)
1. **The bridge pre-generates all IDs** in `execution.Lifecycle.Admit`: `runID`, `contextID` (unless the client supplies one) and `sessionID`, all UUID v4 (`newRunID`). The `them.runs.id` row uses this `runID`.
2. **Subscribe before start.** Handlers open `runstream.StreamFromRedis(runID)` before `Lifecycle.Start/StartAppFlow`.
3. **Events travel on a Redis Stream** `them:dash:run:{runID}:stream`, written by workers (`runstream.PublishEvent`, AppFlow activities), read with replay from `last_event_id` (ADR-003).
4. **Temporal workflow IDs are not the run ID:**
   - orchestrator runs: `{tenantID}:ctx-{contextID}` (`temporal.WorkflowIDForContext`)
   - AppFlow runs: `appflow:{tenantID}:{runID}` (`appflow.WorkflowIDForRun`)
   - canvas agents: `{tenantID}:canvas:{agentID}:{invocationID}`
   Anything that needs the workflow for a run must derive the ID (admin signal does this via the run's `context_id`), never assume `workflowID == runID`.
5. **Run row status:** `CreateRun` inserts `running` (the `admitted` state in `Lifecycle` exists only in memory; the DB CHECK constraint does not allow it).

## Consequences
- One ID (`runID`) identifies the run in Postgres, Redis and events; the workflow needs a lookup.
- `internal/reconciler` still calls `DescribeWorkflowExecution(runID)` and therefore never finds a workflow (backlog F-08).
- ADR-003's run-stream TTLs were not carried over from the Python publisher (backlog F-95).

## Related
- `docs/architecture/run-lifecycle.md`, `docs/decisions/003-redis-streams-event-delivery.md`, `docs/decisions/002-reconciler-status-mapping.md`
